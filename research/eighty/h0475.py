# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 474
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    start_ts = int(datetime(2018, 7, 26, tzinfo=timezone.utc).timestamp())
    
    cursor = conn.execute("""
        SELECT it.symbol_id, it.accession, it.shares, it.price, it.value, it.filed_ts
        FROM insider_trades it
        WHERE it.code = 'P' AND it.filed_ts >= ?
        ORDER BY it.symbol_id, it.filed_ts
    """, (start_ts,))
    
    purchases = cursor.fetchall()
    
    purchases_by_symbol_date = defaultdict(list)
    for p in purchases:
        decision_date = datetime.fromtimestamp(p['filed_ts'], tz=timezone.utc).date()
        purchases_by_symbol_date[(p['symbol_id'], decision_date)].append(p)
    
    opportunities = 0
    calls = []
    
    for (symbol_id, decision_date), day_purchases in purchases_by_symbol_date.items():
        decision_ts_end = int(datetime.combine(decision_date, datetime.max.time().replace(tzinfo=timezone.utc)).timestamp())
        
        so_cursor = conn.execute("""
            SELECT value FROM fundamentals
            WHERE symbol_id = ? AND metric = 'SharesOutstanding' AND fetched_at <= ?
            ORDER BY fetched_at DESC LIMIT 1
        """, (symbol_id, decision_ts_end))
        so_row = so_cursor.fetchone()
        if not so_row:
            continue
        shares_outstanding = float(so_row['value'])
        if shares_outstanding <= 0:
            continue
        
        bars_cursor = conn.execute("""
            SELECT ts, open, high, low, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') < ?
            ORDER BY ts DESC LIMIT 60
        """, (symbol_id, decision_date.isoformat()))
        bars = bars_cursor.fetchall()
        if len(bars) < 60:
            continue
        
        dollar_volumes = []
        turnovers = []
        for bar in bars:
            dv = bar['close'] * bar['volume']
            dollar_volumes.append(dv)
            turnover = bar['volume'] / shares_outstanding
            turnovers.append(turnover)
        
        dollar_volumes.sort()
        turnovers.sort()
        median_dv = dollar_volumes[len(dollar_volumes)//2]
        median_turnover = turnovers[len(turnovers)//2]
        
        if median_dv <= 1_000_000:
            continue
        if median_turnover >= 0.005:
            continue
        
        bar_cursor = conn.execute("""
            SELECT open, high, low, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') = ?
        """, (symbol_id, decision_date.isoformat()))
        bar = bar_cursor.fetchone()
        if not bar:
            continue
        
        opportunities += 1
        
        qualified = False
        for p in day_purchases:
            purchase_value = p['value']
            if purchase_value <= 2 * median_dv:
                continue
            vwap_proxy = (bar['high'] + bar['low'] + bar['close']) / 3
            purchase_price = p['price']
            if abs(purchase_price - vwap_proxy) / vwap_proxy > 0.02:
                continue
            qualified = True
            break
        
        if not qualified:
            continue
        
        label_cursor = conn.execute("""
            SELECT up FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = 21 AND date(ts, 'unixepoch') = ?
        """, (symbol_id, decision_date.isoformat()))
        label_row = label_cursor.fetchone()
        if not label_row:
            continue
        hit = 1 if label_row['up'] == 1 else 0
        
        calls.append((symbol_id, decision_date, hit))
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    calls.sort(key=lambda x: x[1])
    
    n_calls = len(calls)
    split_idx = int(n_calls * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]
    
    issued = n_calls
    hits = sum(c[2] for c in calls)
    precision = hits / issued if issued > 0 else 0.0
    base_rate = precision
    
    distinct_days = len(set(c[1] for c in calls))
    
    day_counts = defaultdict(int)
    for c in calls:
        day_counts[c[1]] += 1
    design_effect = sum(cnt * cnt for cnt in day_counts.values()) / issued if issued > 0 else 1.0
    effective_n = issued / design_effect if design_effect > 0 else 0.0
    
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(c[2] for c in sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
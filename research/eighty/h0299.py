# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 298
# cycle_index: 21
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

    cursor = conn.cursor()
    
    # Get all gifts from officers/directers
    cursor.execute("""
        SELECT symbol_id, insider, shares, filed_ts
        FROM insider_trades
        WHERE code = 'A'
        AND (LOWER(title) LIKE '%officer%' OR LOWER(title) LIKE '%director%')
    """)
    gifts = cursor.fetchall()
    
    if not gifts:
        print("INSUFFICIENT=1")
        return 0
    
    # Get all symbols with daily bars
    cursor.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'")
    symbols_with_bars = set(row[0] for row in cursor.fetchall())
    
    # Get insider purchase history (code='P') for conflict checking
    cursor.execute("""
        SELECT symbol_id, insider, filed_ts
        FROM insider_trades
        WHERE code = 'P'
    """)
    purchases = cursor.fetchall()
    purchase_by_insider = defaultdict(list)
    for symbol_id, insider, filed_ts in purchases:
        purchase_by_insider[(symbol_id, insider)].append(filed_ts)
    
    # Process each gift to generate calls
    calls = []
    opportunities = set()
    
    for symbol_id, insider, shares, disclosure_ts in gifts:
        if symbol_id not in symbols_with_bars:
            continue
            
        opportunities.add((symbol_id, disclosure_ts))
        
        # Check conflict: purchase in prior 10 trading days
        conflict = False
        for p_ts in purchase_by_insider.get((symbol_id, insider), []):
            # Approximate 10 trading days = 14 calendar days
            if disclosure_ts - 14*24*60*60 <= p_ts < disclosure_ts:
                conflict = True
                break
        if conflict:
            continue
        
        # Get trailing 20-day average volume
        cursor.execute("""
            SELECT AVG(volume)
            FROM (
                SELECT volume
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts < ?
                ORDER BY ts DESC
                LIMIT 20
            )
        """, (symbol_id, disclosure_ts))
        avg_vol_row = cursor.fetchone()
        if not avg_vol_row or avg_vol_row[0] is None or avg_vol_row[0] == 0:
            continue
        avg_vol = avg_vol_row[0]
        
        # Size threshold check
        if shares <= 5 * avg_vol:
            continue
        
        # Check 252 trading days history at disclosure date
        cursor.execute("""
            SELECT COUNT(*)
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        """, (symbol_id, disclosure_ts))
        history_count = cursor.fetchone()[0]
        if history_count < 252:
            continue
        
        # Get close at disclosure date
        cursor.execute("""
            SELECT close
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts = ?
        """, (symbol_id, disclosure_ts))
        close_row = cursor.fetchone()
        if not close_row:
            continue
        close_at_signal = close_row[0]
        
        # Get close 10 trading days later
        cursor.execute("""
            SELECT ts, close
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts > ?
            ORDER BY ts ASC
            LIMIT 1
        """, (symbol_id, disclosure_ts))
        next_day_row = cursor.fetchone()
        if not next_day_row:
            continue
        
        # Find 10th trading day after
        next_ts = next_day_row[0]
        cursor.execute("""
            SELECT ts, close
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts > ?
            ORDER BY ts ASC
            LIMIT 1 OFFSET 9
        """, (symbol_id, disclosure_ts))
        tenth_day_row = cursor.fetchone()
        if not tenth_day_row:
            continue
        
        close_at_10 = tenth_day_row[1]
        fwd_return = (close_at_10 - close_at_signal) / close_at_signal
        is_down = fwd_return < 0
        
        calls.append({
            'symbol_id': symbol_id,
            'disclosure_ts': disclosure_ts,
            'is_down': is_down
        })
    
    conn.close()
    
    if not calls:
        print("INSUFFICIENT=1")
        return 0
    
    # Split into sealed (most recent 20%) and rest
    calls_sorted = sorted(calls, key=lambda x: x['disclosure_ts'])
    sealed_idx = int(len(calls_sorted) * 0.8)
    sealed_calls = calls_sorted[sealed_idx:]
    rest_calls = calls_sorted[:sealed_idx]
    
    # Calculate metrics
    def calc_metrics(call_list):
        if not call_list:
            return None, None, None, None, None
        issued = len(call_list)
        hits = sum(1 for c in call_list if c['is_down'])
        precision = hits / issued if issued > 0 else 0
        
        # Base rate of down moves within issued subset
        down_count = sum(1 for c in call_list if c['is_down'])
        base_rate = down_count / issued if issued > 0 else 0
        
        # Distinct days
        distinct_days = len(set(c['disclosure_ts'] for c in call_list))
        
        # Design effect: cluster by symbol
        symbol_counts = defaultdict(int)
        symbol_down = defaultdict(int)
        for c in call_list:
            symbol_counts[c['symbol_id']] += 1
            if c['is_down']:
                symbol_down[c['symbol_id']] += 1
        
        n_clusters = len(symbol_counts)
        if n_clusters == 0:
            return issued, precision, base_rate, distinct_days, issued
        
        # Calculate intra-cluster correlation
        overall_mean = hits / issued
        between_var = 0
        within_var = 0
        
        for sym in symbol_counts:
            n_i = symbol_counts[sym]
            p_i = symbol_down[sym] / n_i if n_i > 0 else 0
            between_var += n_i * ((p_i - overall_mean) ** 2)
            within_var += n_i * p_i * (1 - p_i)
        
        if issued - n_clusters <= 0:
            return issued, precision, base_rate, distinct_days, issued
        
        between_var /= (n_clusters - 1)
        within_var /= (issued - n_clusters)
        
        total_var = between_var + within_var
        if total_var == 0:
            return issued, precision, base_rate, distinct_days, issued
        
        icc = between_var / total_var
        avg_cluster_size = issued / n_clusters
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        return issued, precision, base_rate, distinct_days, effective_n
    
    metrics_rest = calc_metrics(rest_calls)
    metrics_sealed = calc_metrics(sealed_calls)
    
    if not metrics_rest or not metrics_sealed:
        print("INSUFFICIENT=1")
        return 0
    
    issued_rest, precision_rest, base_rate_rest, distinct_days_rest, effective_n_rest = metrics_rest
    sealed_issued, sealed_precision, sealed_base, sealed_days, sealed_effective = metrics_sealed
    
    # Print required output
    print(f"ISSUED={issued_rest}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_rest:.4f}")
    print(f"BASE_RATE={base_rate_rest:.4f}")
    print(f"DISTINCT_DAYS={distinct_days_rest}")
    print(f"EFFECTIVE_N={effective_n_rest:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    return 0

if __name__ == "__main__":
    main()
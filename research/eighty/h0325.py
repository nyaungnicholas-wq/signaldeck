# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 324
# cycle_index: 47
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from datetime import datetime

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 21
ENTRY_WINDOW = 30
LAG_13F_DAYS = 45

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()
    
    # Get all symbols with both 13F and insider data
    cur.execute("""
        SELECT DISTINCT symbol_id 
        FROM inst_holdings 
        INTERSECT 
        SELECT DISTINCT symbol_id 
        FROM insider_trades WHERE code = 'P'
    """)
    symbols = [r['symbol_id'] for r in cur.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get insider purchases with filing dates
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM insider_trades 
        WHERE code = 'P' AND filed_ts IS NOT NULL
    """)
    insider_buys = {}
    for r in cur.fetchall():
        sid = r['symbol_id']
        dt = datetime.utcfromtimestamp(r['filed_ts']).date()
        insider_buys.setdefault(sid, []).append(dt)
    
    # Get 13F holdings aggregated by quarter
    cur.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
    """)
    holdings = {}
    for r in cur.fetchall():
        sid = r['symbol_id']
        period = datetime.strptime(r['period'], '%Y-%m-%d').date()
        holdings.setdefault(sid, {})[period] = r['total_shares']
    
    # Find opportunities
    opportunities = []
    
    for sid in symbols:
        if sid not in holdings or sid not in insider_buys:
            continue
        
        # Get sorted quarters for this symbol
        quarters = sorted(holdings[sid].keys())
        if len(quarters) < 2:
            continue
        
        # Check most recent 13F filing for increase
        recent_period = quarters[-1]
        prev_period = quarters[-2]
        if holdings[sid][recent_period] <= holdings[sid][prev_period]:
            continue
        
        # 13F disclosure date (lagged 45 days)
        disclosure_date = datetime.utcfromtimestamp(
            int((recent_period - datetime(1970,1,1).date()).days * 86400) + LAG_13F_DAYS * 86400
        ).date()
        
        # Find insider purchases within window after disclosure
        window_end = disclosure_date.fromordinal(disclosure_date.toordinal() + ENTRY_WINDOW)
        valid_buys = [
            buy_date for buy_date in insider_buys[sid]
            if disclosure_date <= buy_date <= window_end
        ]
        
        if not valid_buys:
            continue
        
        # Signal date is the latest qualifying insider buy
        signal_date = max(valid_buys)
        signal_ts = int((signal_date - datetime(1970,1,1).date()).days * 86400)
        
        opportunities.append((sid, signal_date, signal_ts))
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Sort opportunities by signal date for temporal split
    opportunities.sort(key=lambda x: x[1])
    n_total = len(opportunities)
    n_sealed = max(1, n_total // 5)  # last 20%
    
    # Get labels for all opportunities
    results = []
    for sid, signal_date, signal_ts in opportunities:
        cur.execute("""
            SELECT up 
            FROM prediction_outcomes 
            WHERE symbol_id = ? AND horizon = ? 
            AND ts <= ? 
            ORDER BY ts DESC LIMIT 1
        """, (sid, HORIZON, signal_ts))
        row = cur.fetchone()
        if row is None:
            continue
        results.append((sid, signal_date, bool(row['up'])))
    
    conn.close()
    
    if len(results) < 2:
        print("INSUFFICIENT=1")
        return
    
    # Split into train and sealed
    n_results = len(results)
    n_sealed_actual = max(1, n_results // 5)
    train = results[:-n_sealed_actual]
    sealed = results[-n_sealed_actual:]
    
    # Calculate metrics
    issued = len(results)
    issued_days = set()
    for sid, dt, _ in results:
        issued_days.add(dt)
    
    # Design effect (cluster by day)
    day_counts = {}
    for sid, dt, _ in results:
        day_counts[dt] = day_counts.get(dt, 0) + 1
    
    if len(issued_days) > 0:
        avg_cluster = issued / len(issued_days)
        # Simplified DEFF: 1 + (avg_cluster - 1)
        deff = 1 + (avg_cluster - 1) if avg_cluster > 1 else 1
    else:
        deff = 1
    
    effective_n = issued / deff if deff > 0 else 0
    
    # Precision and base rate
    hits = sum(1 for _, _, up in results if up)
    precision = hits / issued if issued > 0 else 0
    base_rate = precision  # Base rate of predicted class (up) within issued
    
    sealed_hits = sum(1 for _, _, up in sealed if up)
    sealed_precision = sealed_hits / len(sealed) if sealed else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={n_results}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={len(issued_days)}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
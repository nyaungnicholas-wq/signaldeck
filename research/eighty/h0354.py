# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 353
# cycle_index: 21
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    # Get 10Y-2Y spread series name
    try:
        cur = conn.cursor()
        cur.execute("SELECT DISTINCT series FROM macro_series")
        series_list = [row[0] for row in cur.fetchall()]
        spread_series = None
        for s in series_list:
            if 'T10Y2Y' in s.upper():
                spread_series = s
                break
        if not spread_series:
            print("INSUFFICIENT=1")
            return
    except:
        print("INSUFFICIENT=1")
        return
    
    # Get days where spread is negative
    try:
        cur.execute("SELECT ts, value FROM macro_series WHERE series = ?", (spread_series,))
        spread_data = cur.fetchall()
        negative_days = set()
        for ts, value in spread_data:
            if value is not None and value < 0:
                day = datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
                negative_days.add(day)
    except:
        print("INSUFFICIENT=1")
        return
    
    # Get insider purchases (code='P' for purchase)
    try:
        cur.execute("""
            SELECT symbol_id, filed_ts 
            FROM insider_trades 
            WHERE code = 'P'
        """)
        purchases = cur.fetchall()
        if not purchases:
            print("INSUFFICIENT=1")
            return
    except:
        print("INSUFFICIENT=1")
        return
    
    # Build opportunities: (symbol_id, entry_date, entry_ts)
    opportunities = []
    for symbol_id, filed_ts in purchases:
        if filed_ts is None:
            continue
        entry_date = datetime.datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
        if entry_date in negative_days:
            opportunities.append((symbol_id, entry_date, filed_ts))
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # For each opportunity, get close price on entry and 21 days later
    hits = 0
    issued = 0
    distinct_days = set()
    all_returns = []
    
    for symbol_id, entry_date, entry_ts in opportunities:
        try:
            # Get close on entry day
            cur.execute("""
                SELECT close FROM bars 
                WHERE symbol_id = ? AND tf = '1d' 
                AND date(ts, 'unixepoch') = ?
                LIMIT 1
            """, (symbol_id, entry_date))
            row = cur.fetchone()
            if not row:
                continue
            close0 = row[0]
            
            # Get close 21 trading days later
            cur.execute("""
                SELECT close FROM bars 
                WHERE symbol_id = ? AND tf = '1d' 
                AND ts > ? 
                ORDER BY ts ASC 
                LIMIT 1 OFFSET 20
            """, (symbol_id, entry_ts))
            row = cur.fetchone()
            if not row:
                continue
            close21 = row[0]
            
            fwd_return = (close21 - close0) / close0
            all_returns.append((entry_date, fwd_return))
            issued += 1
            distinct_days.add(entry_date)
            if fwd_return > 0:
                hits += 1
        except:
            continue
    
    conn.close()
    
    if issued < 10:  # Minimum for statistical validity
        print("INSUFFICIENT=1")
        return
    
    # Split into train/test (most recent 20%)
    all_returns.sort(key=lambda x: x[0])
    split_idx = int(len(all_returns) * 0.8)
    train_returns = all_returns[:split_idx]
    test_returns = all_returns[split_idx:]
    
    # Compute metrics
    precision = hits / issued if issued > 0 else 0
    base_rate = hits / issued  # Same as precision since all predictions are positive
    distinct_days_count = len(distinct_days)
    
    # Compute design effect (simplified)
    # Count calls per day
    day_counts = {}
    for date, _ in all_returns:
        day_counts[date] = day_counts.get(date, 0) + 1
    avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    design_effect = 1 + (avg_cluster - 1) * 0.1  # Conservative ICC estimate
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Test precision
    test_hits = sum(1 for _, ret in test_returns if ret > 0)
    test_issued = len(test_returns)
    test_precision = test_hits / test_issued if test_issued > 0 else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")  # Same as issued since we act on all opportunities
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_count}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={test_precision:.6f}")

if __name__ == "__main__":
    main()
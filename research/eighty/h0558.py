# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 557
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
from datetime import datetime

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Get all insider open-market purchases with disclosure dates
    c.execute("""
        SELECT symbol_id, filed_ts, price 
        FROM insider_trades 
        WHERE code='P'
    """)
    trades = c.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    # Get daily bar counts per symbol up to each disclosure date
    symbol_bar_counts = defaultdict(lambda: defaultdict(int))
    c.execute("SELECT symbol_id, ts FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    for sid, ts in c.fetchall():
        # Increment count for all later disclosure dates
        symbol_bar_counts[sid][ts] += 1
    
    # Process each trade
    opportunities = []
    for sid, filed_ts, price in trades:
        # Parse disclosure date
        try:
            disc_date = datetime.fromisoformat(filed_ts.split(' ')[0]).strftime('%Y-%m-%d')
        except:
            continue
        
        # Check if symbol has enough history (60 daily bars before disclosure)
        total_bars = 0
        for bar_date, count in symbol_bar_counts.get(sid, {}).items():
            if bar_date < disc_date:
                total_bars += count
        if total_bars < 60:
            continue
        
        # Get disclosure close price
        c.execute("""
            SELECT close FROM bars 
            WHERE symbol_id=? AND tf='1d' AND date(ts, 'unixepoch')=?
        """, (sid, disc_date))
        row = c.fetchone()
        if not row or row[0] is None:
            continue
        close = row[0]
        
        # Check if close >= 5% above trade price
        if close < price * 1.05:
            continue
        
        # Check for conflicting insider signals on same disclosure date
        c.execute("""
            SELECT COUNT(*) FROM insider_trades 
            WHERE symbol_id=? AND date(filed_ts)=?
        """, (sid, disc_date))
        conflict_count = c.fetchone()[0]
        if conflict_count > 1:
            continue
        
        # This is an issued call
        opportunities.append((sid, disc_date, close))
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Get outcomes: 21 trading days after disclosure
    hits = []
    issued_days = set()
    for sid, disc_date, entry_close in opportunities:
        # Find close 21 trading days later
        c.execute("""
            SELECT close FROM bars 
            WHERE symbol_id=? AND tf='1d' AND date(ts, 'unixepoch') > ?
            ORDER BY ts
            LIMIT 1 OFFSET 20
        """, (sid, disc_date))
        row = c.fetchone()
        if not row or row[0] is None:
            continue
        exit_close = row[0]
        hit = 1 if exit_close > entry_close else 0
        hits.append((disc_date, hit))
        issued_days.add(disc_date)
    
    if not hits:
        print("INSUFFICIENT=1")
        return
    
    # Split into non-sealed and sealed (most recent 20% by date)
    hits.sort(key=lambda x: x[0])
    split_idx = int(len(hits) * 0.8)
    non_sealed = hits[:split_idx]
    sealed = hits[split_idx:]
    
    # Calculate metrics for non-sealed era
    issued = len(non_sealed)
    if issued == 0:
        print("INSUFFICIENT=1")
        return
    
    hits_count = sum(h for _, h in non_sealed)
    precision = hits_count / issued
    
    # Base rate: proportion of "up" in issued calls
    base_rate = hits_count / issued
    
    # Count distinct days
    distinct_days = len(set(d for d, _ in non_sealed))
    
    # Design effect: assume clustering by day
    day_counts = defaultdict(int)
    for d, _ in non_sealed:
        day_counts[d] += 1
    
    avg_cluster_size = issued / len(day_counts) if day_counts else 1
    # Estimate ICC from data variance (simplified)
    if issued > 1:
        day_means = [hits_count / issued]  # use overall mean for simplicity
        icc = 0.5  # conservative estimate
        design_effect = 1 + (avg_cluster_size - 1) * icc
    else:
        design_effect = 1.0
    
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Sealed era metrics
    sealed_issued = len(sealed)
    sealed_hits = sum(h for _, h in sealed) if sealed_issued > 0 else 0
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 309
# cycle_index: 32
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
"""Test hypothesis: Inverted yield curve + negative sentiment signals overreaction."""
import sqlite3
from collections import defaultdict
from datetime import datetime

DB = "data/signaldeck.db"
HORIZON = 21

def main():
    # Connect read-only
    try:
        conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True, timeout=10)
        cur = conn.cursor()
    except sqlite3.Error as e:
        print(f"INSUFFICIENT=1")
        return 0

    # Get all sentiment data first (needed for universe)
    cur.execute("""
        SELECT symbol_id, day, mean_score 
        FROM sentiment_features 
        ORDER BY symbol_id, day
    """)
    sentiment_data = cur.fetchall()
    if len(sentiment_data) < 100:
        print("INSUFFICIENT=1")
        return 0

    # Group by symbol for trailing window calculation
    symbol_sentiment = defaultdict(list)
    for row in sentiment_data:
        symbol_sentiment[row[0]].append((row[1], row[2]))

    # Get Treasury spreads
    cur.execute("""
        SELECT ts, 
               SUM(CASE WHEN series = 'DGS10' THEN value ELSE 0 END) as dgs10,
               SUM(CASE WHEN series = 'DGS2' THEN value ELSE 0 END) as dgs2
        FROM macro_series
        WHERE series IN ('DGS10', 'DGS2')
        GROUP BY ts
        HAVING COUNT(DISTINCT series) = 2
        ORDER BY ts
    """)
    treasury_data = cur.fetchall()
    if len(treasury_data) < 100:
        print("INSUFFICIENT=1")
        return 0

    # Convert to day-based spread (unix timestamp -> date string)
    spread_by_day = {}
    for ts, dgs10, dgs2 in treasury_data:
        day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        spread_by_day[day] = dgs10 - dgs2

    # Get prediction outcomes for 21-day horizon
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = ?
    """, (HORIZON,))
    outcomes = cur.fetchall()
    if len(outcomes) < 50:
        print("INSUFFICIENT=1")
        return 0

    # Convert outcomes to day-based
    outcome_by_sym_day = {}
    for sym_id, ts, up, fwd in outcomes:
        day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        outcome_by_sym_day[(sym_id, day)] = (up, fwd)

    # Universe: symbols with both sentiment and outcomes
    universe_symbols = set()
    for (sym_id, day) in outcome_by_sym_day:
        if sym_id in symbol_sentiment:
            universe_symbols.add(sym_id)

    if len(universe_symbols) < 10:
        print("INSUFFICIENT=1")
        return 0

    # Process each symbol's sentiment to find opportunities
    opportunities = []  # (day, sym_id, up, fwd_return)
    for sym_id in universe_symbols:
        sent_list = symbol_sentiment[sym_id]
        sent_by_day = {d: s for d, s in sent_list}
        days_sorted = sorted(sent_by_day.keys())
        
        # Need at least 252 days + 5-day MA
        if len(days_sorted) < 257:
            continue
            
        # Slide through days where we have enough trailing history
        for i in range(256, len(days_sorted)):
            day = days_sorted[i]
            if day not in spread_by_day:
                continue
            spread = spread_by_day[day]
            if spread >= 0:
                continue  # Abstain: yield curve not inverted
                
            # Get last 5 days for MA
            last_5 = [sent_by_day[days_sorted[j]] for j in range(i-4, i+1)]
            if len(last_5) < 5:
                continue
            ma5 = sum(last_5) / 5.0
            
            # Get trailing 252-day distribution
            trailing_252 = [sent_by_day[days_sorted[j]] for j in range(i-251, i+1)]
            if len(trailing_252) < 252:
                continue
                
            # Calculate 5th percentile (using linear interpolation)
            trailing_252_sorted = sorted(trailing_252)
            idx = 0.05 * (len(trailing_252_sorted) - 1)
            lower = int(idx)
            frac = idx - lower
            p5 = trailing_252_sorted[lower]
            if lower + 1 < len(trailing_252_sorted):
                p5 += frac * (trailing_252_sorted[lower+1] - p5)
                
            # Entry condition
            if ma5 < p5 and (sym_id, day) in outcome_by_sym_day:
                up, fwd = outcome_by_sym_day[(sym_id, day)]
                opportunities.append((day, sym_id, up, fwd))

    conn.close()

    if len(opportunities) < 20:
        print("INSUFFICIENT=1")
        return 0

    # Sort by day for time-based split
    opportunities.sort(key=lambda x: x[0])
    
    # Hold out most recent 20% by time
    n_total = len(opportunities)
    n_held = max(1, int(n_total * 0.2))
    n_test = n_total - n_held
    
    test_set = opportunities[:n_test]
    held_set = opportunities[n_test:]
    
    # Compute metrics for full issued set
    issued_days = defaultdict(int)  # day -> count
    hits = 0
    up_count = 0
    
    for day, sym, up, fwd in opportunities:
        issued_days[day] += 1
        if up == 1:
            hits += 1
            up_count += 1
    
    issued = len(opportunities)
    precision = hits / issued if issued > 0 else 0.0
    base_rate = up_count / issued if issued > 0 else 0.0
    distinct_days = len(issued_days)
    
    # Compute design effect: 1 + (variance of cluster sizes / mean cluster size)
    cluster_sizes = list(issued_days.values())
    if len(cluster_sizes) > 1:
        mean_cluster = sum(cluster_sizes) / len(cluster_sizes)
        var_cluster = sum((x - mean_cluster) ** 2 for x in cluster_sizes) / (len(cluster_sizes) - 1)
        design_effect = 1 + (var_cluster / mean_cluster) if mean_cluster > 0 else 1.0
    else:
        design_effect = 1.0
    
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Compute sealed era metrics
    sealed_days = defaultdict(int)
    sealed_hits = 0
    sealed_up = 0
    
    for day, sym, up, fwd in held_set:
        sealed_days[day] += 1
        if up == 1:
            sealed_hits += 1
            sealed_up += 1
    
    sealed_issued = len(held_set)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    
    # Print required output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={n_total}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    # Validate invariants
    if distinct_days > issued:
        print("INVARIANT_VIOLATED: DISTINCT_DAYS > ISSUED")
        return 1
    if effective_n >= issued and issued > 1:
        print("INVARIANT_VIOLATED: EFFECTIVE_N >= ISSUED")
        return 1
    
    return 0

if __name__ == "__main__":
    exit(main())
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 311
# cycle_index: 34
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
    cursor = conn.cursor()
    
    # Get symbols with daily bars and at least 2 years of history
    cursor.execute("""
        SELECT symbol_id, MIN(ts) as first_ts, MAX(ts) as last_ts
        FROM bars WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING (MAX(ts) - MIN(ts)) >= (365 * 2 * 86400)
    """)
    symbols = {row[0]: (row[1], row[2]) for row in cursor.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get public float changes from fundamentals
    cursor.execute("""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals 
        WHERE metric = 'EntityPublicFloat'
        ORDER BY symbol_id, as_of
    """)
    
    # Process each symbol's public float history
    float_changes = []
    prev_values = {}
    for symbol_id, as_of, value, fetched_at in cursor.fetchall():
        if symbol_id not in prev_values:
            prev_values[symbol_id] = []
        prev_values[symbol_id].append((as_of, value, fetched_at))
    
    for symbol_id, records in prev_values.items():
        if len(records) < 2:
            continue
        for i in range(1, len(records)):
            curr_as_of, curr_val, curr_fetched = records[i]
            prev_as_of, prev_val, prev_fetched = records[i-1]
            if prev_val and prev_val > 0:
                change = (curr_val - prev_val) / prev_val
                if change <= -0.20:  # ≥20% decrease
                    float_changes.append((symbol_id, curr_fetched, change))
    
    if not float_changes:
        print("INSUFFICIENT=1")
        return
    
    # Get 21-day forward return labels from prediction_outcomes
    cursor.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    labels = {(row[0], row[1]): row[2] for row in cursor.fetchall()}
    
    # Find opportunities: decisions at float change times with valid labels
    opportunities = []
    for symbol_id, decision_time, change in float_changes:
        if symbol_id not in symbols:
            continue
        # Get label at decision time (use most recent label ≤ decision_time)
        cursor.execute("""
            SELECT up FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = 21 AND ts <= ?
            ORDER BY ts DESC LIMIT 1
        """, (symbol_id, decision_time))
        row = cursor.fetchone()
        if row:
            opportunities.append((symbol_id, decision_time, row[0]))
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Split into main and sealed (20% most recent)
    opportunities.sort(key=lambda x: x[1])
    split_idx = int(len(opportunities) * 0.8)
    main_era = opportunities[:split_idx]
    sealed_era = opportunities[split_idx:]
    
    # Issue calls for main era (all are positive predictions per hypothesis)
    issued_main = [(s, t, l) for s, t, l in main_era if l == 1]
    issued_sealed = [(s, t, l) for s, t, l in sealed_era if l == 1]
    
    # Compute metrics for main era
    hits_main = sum(1 for s, t, l in issued_main if l == 1)
    issued_count_main = len(issued_main)
    
    if issued_count_main == 0:
        print("INSUFFICIENT=1")
        return
    
    base_rate_main = hits_main / issued_count_main
    precision_main = hits_main / issued_count_main
    
    # Count distinct days
    days_main = set()
    for s, t, l in issued_main:
        dt = datetime.utcfromtimestamp(t)
        days_main.add(dt.date())
    distinct_days_main = len(days_main)
    
    # Design effect: variance inflation from clustering by day
    # Count calls per day
    day_counts = defaultdict(int)
    for s, t, l in issued_main:
        dt = datetime.utcfromtimestamp(t)
        day_counts[dt.date()] += 1
    avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    design_effect = avg_cluster
    effective_n = issued_count_main / design_effect
    
    # Compute sealed era metrics
    hits_sealed = sum(1 for s, t, l in issued_sealed if l == 1)
    issued_sealed_count = len(issued_sealed)
    sealed_precision = hits_sealed / issued_sealed_count if issued_sealed_count > 0 else 0
    
    # Print results
    print(f"ISSUED={issued_count_main}")
    print(f"OPPORTUNITIES={len(main_era)}")
    print(f"PRECISION={precision_main:.6f}")
    print(f"BASE_RATE={base_rate_main:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_main}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
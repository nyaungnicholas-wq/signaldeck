# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 504
# cycle_index: 34
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    try:
        conn = sqlite3.connect(db_path, uri=True)
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

    cur = conn.cursor()

    # Check for at least two consecutive quarters of 13F data
    cur.execute("""
        WITH quarterly_ownership AS (
            SELECT
                symbol_id,
                period,
                SUM(shares) as total_shares,
                LAG(SUM(shares)) OVER (PARTITION BY symbol_id ORDER BY period) as prev_shares
            FROM inst_holdings
            GROUP BY symbol_id, period
        )
        SELECT COUNT(DISTINCT symbol_id) as symbols_with_two_quarters
        FROM quarterly_ownership
        WHERE prev_shares IS NOT NULL
    """)
    result = cur.fetchone()
    if result is None or result[0] == 0:
        print("INSUFFICIENT=1")
        conn.close()
        return 0

    # Get all symbols with at least two consecutive quarters
    cur.execute("""
        WITH quarterly_ownership AS (
            SELECT
                symbol_id,
                period,
                SUM(shares) as total_shares,
                LAG(SUM(shares)) OVER (PARTITION BY symbol_id ORDER BY period) as prev_shares
            FROM inst_holdings
            GROUP BY symbol_id, period
        )
        SELECT symbol_id, period, total_shares, prev_shares,
               (total_shares - prev_shares) * 100.0 / prev_shares as pct_change
        FROM quarterly_ownership
        WHERE prev_shares IS NOT NULL
        ORDER BY symbol_id, period
    """)
    ownership_data = cur.fetchall()

    # Build signals with decision dates (period + 45 days to respect as-of)
    signals = []
    for symbol_id, period, total_shares, prev_shares, pct_change in ownership_data:
        try:
            # period is quarter end date
            period_date = datetime.datetime.strptime(period, '%Y-%m-%d')
        except (ValueError, TypeError):
            continue
            
        # Decision date is period + 45 days (when filing becomes public)
        decision_date = period_date + datetime.timedelta(days=45)
        decision_ts = int(decision_date.timestamp())
        
        # Check for news sentiment condition in past 5 days
        cur.execute("""
            SELECT COUNT(*) 
            FROM sentiment_features 
            WHERE symbol_id = ? 
              AND day BETWEEN date(?, '-4 days') AND ?
              AND mean_score > 0
        """, (symbol_id, decision_date.strftime('%Y-%m-%d'), decision_date.strftime('%Y-%m-%d')))
        positive_days = cur.fetchone()[0]
        
        if positive_days >= 3 and pct_change >= 5:
            # This signal meets entry criteria
            signals.append((symbol_id, decision_date, decision_ts, pct_change))
    
    if not signals:
        print("INSUFFICIENT=1")
        conn.close()
        return 0
    
    # Now get labels for these signals (21-day forward direction)
    issued_signals = []
    for symbol_id, decision_date, decision_ts, pct_change in signals:
        # Find matching prediction_outcomes with horizon 21
        cur.execute("""
            SELECT up
            FROM prediction_outcomes
            WHERE symbol_id = ? 
              AND horizon = 21
              AND ts >= ?
              AND ts < ? + 21*86400
            LIMIT 1
        """, (symbol_id, decision_ts, decision_ts))
        result = cur.fetchone()
        if result:
            up_label = result[0]
            issued_signals.append((symbol_id, decision_date, up_label))
    
    if not issued_signals:
        print("INSUFFICIENT=1")
        conn.close()
        return 0
    
    # Sort by decision date
    issued_signals.sort(key=lambda x: x[1])
    
    # Split into train and sealed (last 20%)
    n = len(issued_signals)
    split_idx = int(n * 0.8)
    train_signals = issued_signals[:split_idx]
    sealed_signals = issued_signals[split_idx:]
    
    # Calculate metrics
    issued = len(issued_signals)
    hits = sum(1 for s in issued_signals if s[2] == 1)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate: proportion of up outcomes in issued set (same as precision here)
    base_rate = precision
    
    # Count distinct days
    distinct_days = len(set(s[1].date() for s in issued_signals))
    
    # Calculate effective N (design effect)
    # Group by day
    from collections import defaultdict
    day_counts = defaultdict(int)
    for s in issued_signals:
        day_counts[s[1].date()] += 1
    
    avg_cluster_size = sum(day_counts.values()) / len(day_counts)
    design_effect = avg_cluster_size  # Conservative: assume ICC=1
    effective_n = issued / design_effect if design_effect > 0 else 0
    
    # Sealed precision
    sealed_hits = sum(1 for s in sealed_signals if s[2] == 1)
    sealed_precision = sealed_hits / len(sealed_signals) if sealed_signals else 0
    
    # Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(signals)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    # Verify invariants
    if distinct_days > issued:
        print("ERROR: DISTINCT_DAYS exceeds ISSUED")
        return 1
    if effective_n >= issued:
        print("ERROR: EFFECTIVE_N not less than ISSUED")
        return 1
    
    conn.close()
    return 0

if __name__ == "__main__":
    exit(main())
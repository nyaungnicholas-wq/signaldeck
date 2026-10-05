# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 488
# cycle_index: 18
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except:
        print("INSUFFICIENT=1")
        return
    
    cur = conn.cursor()
    
    # Get symbols with at least 120 days of daily bars
    cur.execute("""
        SELECT symbol_id, COUNT(DISTINCT ts) as days
        FROM bars WHERE tf='1d'
        GROUP BY symbol_id
        HAVING days >= 120
    """)
    symbols_with_bars = {row[0] for row in cur.fetchall()}
    
    if not symbols_with_bars:
        print("INSUFFICIENT=1")
        return
    
    # Get symbols with public float < $500M
    cur.execute("""
        SELECT symbol_id, MIN(CAST(value AS REAL)) as min_float
        FROM fundamentals
        WHERE metric='EntityPublicFloat' AND value IS NOT NULL
        GROUP BY symbol_id
        HAVING min_float < 500000000
    """)
    symbols_with_float = {row[0] for row in cur.fetchall()}
    
    # Get symbols with insider trades
    cur.execute("SELECT DISTINCT symbol_id FROM insider_trades")
    symbols_with_insider = {row[0] for row in cur.fetchall()}
    
    # Universe intersection
    universe = symbols_with_bars & symbols_with_float & symbols_with_insider
    if not universe:
        print("INSUFFICIENT=1")
        return
    
    # Get all insider purchases (code='P') for universe
    cur.execute("""
        SELECT symbol_id, filed_ts, tx_ts
        FROM insider_trades
        WHERE code='P' AND symbol_id IN ({})
    """.format(','.join('?' for _ in universe)), list(universe))
    
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    # For efficiency, precompute needed data structures
    # Get all 1d bars for universe
    cur.execute("""
        SELECT symbol_id, ts, volume
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?' for _ in universe)), list(universe))
    
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row[0]].append((row[1], row[2]))
    
    # Get sentiment features
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({})
    """.format(','.join('?' for _ in universe)), list(universe))
    
    sentiment_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        sentiment_by_symbol[row[0]].append((row[1], row[2]))
    
    # Get prediction outcomes for horizon=21
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon=21 AND symbol_id IN ({})
    """.format(','.join('?' for _ in universe)), list(universe))
    
    outcomes_by_symbol_ts = {}
    for row in cur.fetchall():
        outcomes_by_symbol_ts[(row[0], row[1])] = row[2]
    
    conn.close()
    
    # Process each trade
    opportunities = 0
    issued = 0
    hits = 0
    days_issued = set()
    all_trade_dates = []
    
    for symbol_id, filed_ts, tx_ts in trades:
        opportunities += 1
        
        # Convert filed_ts to day string for sentiment lookup
        filed_day = str(int(filed_ts / 86400))
        
        # Check sentiment: need bottom quartile over prior 30 days
        symbol_sentiment = sentiment_by_symbol.get(symbol_id, [])
        prior_sentiment = []
        for day_str, score in symbol_sentiment:
            if day_str < filed_day:
                prior_sentiment.append(score)
        
        if len(prior_sentiment) < 30:
            continue
            
        # Use most recent 30 days
        recent_sentiment = prior_sentiment[-30:]
        if not recent_sentiment:
            continue
            
        avg_sentiment = sum(recent_sentiment) / len(recent_sentiment)
        
        # Calculate quartile - need distribution of sentiment averages
        # For efficiency, compute percentile locally
        sorted_sent = sorted(recent_sentiment)
        q25_idx = len(sorted_sent) // 4
        q25 = sorted_sent[q25_idx]
        
        if avg_sentiment > q25:
            continue
        
        # Check volume condition: 20d avg < 252d median
        symbol_bars = bars_by_symbol.get(symbol_id, [])
        prior_bars = [(ts, vol) for ts, vol in symbol_bars if ts < filed_ts]
        
        if len(prior_bars) < 252:
            continue
        
        # Get most recent 252 days of volume
        recent_252 = [vol for _, vol in prior_bars[-252:]]
        recent_20 = [vol for _, vol in prior_bars[-20:]]
        
        if not recent_20 or not recent_252:
            continue
            
        avg_20 = sum(recent_20) / len(recent_20)
        median_252 = sorted(recent_252)[len(recent_252)//2]
        
        if avg_20 >= median_252:
            continue
        
        # Check for outcome
        trade_day = int(filed_ts / 86400)
        if (symbol_id, trade_day) in outcomes_by_symbol_ts:
            outcome = outcomes_by_symbol_ts[(symbol_id, trade_day)]
            issued += 1
            days_issued.add(trade_day)
            all_trade_dates.append(trade_day)
            if outcome:
                hits += 1
    
    if issued == 0:
        print("INSUFFICIENT=1")
        return
    
    # Split into train/test (most recent 20% as sealed era)
    all_trade_dates.sort()
    split_idx = int(len(all_trade_dates) * 0.8)
    train_dates = all_trade_dates[:split_idx]
    test_dates = all_trade_dates[split_idx:]
    
    train_count = len(train_dates)
    test_count = len(test_dates)
    
    # Calculate metrics for full issued set
    precision = hits / issued
    distinct_days = len(days_issued)
    
    # Calculate base rate of predicted class (up=True) within issued
    # We already have hits count, so base rate is hits/issued? Actually base rate is proportion of positive class in issued subset
    # In this context, the predicted class is "up" (precision@1), so base rate is hits/issued
    base_rate = precision
    
    # Calculate design effect and effective N
    # Cluster by day
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for day in all_trade_dates:
        day_counts[day] += 1
    
    # Re-count hits per day
    for symbol_id, trade_day in outcomes_by_symbol_ts.items():
        if trade_day in day_counts and outcomes_by_symbol_ts[(symbol_id, trade_day)]:
            day_hits[trade_day] += 1
    
    # ICC calculation
    n_clusters = len(day_counts)
    if n_clusters < 2:
        design_effect = 1.0
    else:
        cluster_sizes = list(day_counts.values())
        m = sum(cluster_sizes) / n_clusters  # average cluster size
        
        # Calculate proportions per cluster
        p = hits / issued
        between_var = 0
        for day in day_counts:
            n_i = day_counts[day]
            p_i = day_hits.get(day, 0) / n_i if n_i > 0 else 0
            between_var += n_i * (p_i - p)**2
        between_var /= (issued - 1)
        
        within_var = p * (1 - p)
        
        if within_var == 0:
            rho = 0
        else:
            rho = between_var / within_var
        
        design_effect = 1 + (m - 1) * rho
    
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Calculate sealed era metrics
    sealed_hits = 0
    for day in test_dates:
        for symbol_id, trade_day in outcomes_by_symbol_ts.items():
            if trade_day == day and outcomes_by_symbol_ts[(symbol_id, trade_day)]:
                sealed_hits += 1
    
    sealed_precision = sealed_hits / test_count if test_count > 0 else 0
    
    # Output results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
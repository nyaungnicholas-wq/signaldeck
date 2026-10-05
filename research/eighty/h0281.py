# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 280
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
from datetime import datetime, timedelta
import math

def connect_db():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def get_disclosure_date(ts):
    """Convert epoch to 'YYYY-MM-DD' string."""
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def main():
    conn = connect_db()
    c = conn.cursor()
    
    # Step 1: Get all insider open-market purchases (Form 4, code='P')
    c.execute("""
        SELECT symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
    """)
    insider_purchases = c.fetchall()
    if not insider_purchases:
        print("INSUFFICIENT=1")
        return
    
    # Step 2: Build daily news sentiment aggregates from sentiment_features
    c.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        ORDER BY symbol_id, day
    """)
    sentiment_data = c.fetchall()
    
    # Organize by symbol and date
    sentiment_by_symbol = defaultdict(list)
    for symbol_id, day, mean_score in sentiment_data:
        sentiment_by_symbol[symbol_id].append((day, mean_score))
    
    # Step 3: Get prediction outcomes for horizon=21
    c.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    outcomes = c.fetchall()
    outcomes_dict = defaultdict(dict)
    for symbol_id, ts, up in outcomes:
        day = get_disclosure_date(ts)
        outcomes_dict[symbol_id][day] = up
    
    conn.close()
    
    # Step 4: Identify opportunities and signals
    opportunities = []
    signals = []
    
    for symbol_id, tx_ts, filed_ts in insider_purchases:
        disclosure_date = get_disclosure_date(filed_ts)
        
        # Get sentiment history for this symbol
        if symbol_id not in sentiment_by_symbol:
            continue
        sentiments = sentiment_by_symbol[symbol_id]
        
        # Filter to dates <= disclosure_date
        prior_sentiments = [(day, score) for day, score in sentiments if day <= disclosure_date]
        if len(prior_sentiments) < 20:
            continue
        
        # Compute 20-day moving average of mean_score on disclosure_date
        # Find the index of disclosure_date in the list
        date_scores = [score for day, score in prior_sentiments if day <= disclosure_date]
        if len(date_scores) < 20:
            continue
        
        # Get last 20 days up to disclosure_date
        last_20 = date_scores[-20:]
        ma20 = sum(last_20) / 20
        
        # Compute 10th percentile of all MA20 values up to disclosure_date
        ma20_values = []
        for i in range(19, len(date_scores)):
            window = date_scores[i-19:i+1]
            ma20_values.append(sum(window)/20)
        
        if not ma20_values:
            continue
        
        # 10th percentile calculation
        sorted_ma20 = sorted(ma20_values)
        idx = math.ceil(0.1 * len(sorted_ma20)) - 1
        p10 = sorted_ma20[idx]
        
        # Step 5: Check condition and get label
        if ma20 < p10:
            # Check if we have outcome data
            if symbol_id in outcomes_dict and disclosure_date in outcomes_dict[symbol_id]:
                up = outcomes_dict[symbol_id][disclosure_date]
                opportunities.append((symbol_id, disclosure_date, up))
                signals.append((symbol_id, disclosure_date, up))
            else:
                opportunities.append((symbol_id, disclosure_date, None))
        else:
            opportunities.append((symbol_id, disclosure_date, None))
    
    if not signals:
        print("INSUFFICIENT=1")
        return
    
    # Step 6: Split into training (80%) and sealed (20%) by time
    all_days = sorted(set(day for _, day, _ in opportunities))
    split_idx = int(len(all_days) * 0.8)
    training_days = set(all_days[:split_idx])
    sealed_days = set(all_days[split_idx:])
    
    # Step 7: Compute statistics for training period
    training_signals = [(s, d, u) for s, d, u in signals if d in training_days]
    training_opportunities = [(s, d, u) for s, d, u in opportunities if d in training_days]
    
    if not training_signals:
        print("INSUFFICIENT=1")
        return
    
    # Count observations by (symbol, day)
    signal_days = defaultdict(set)
    for symbol_id, day, _ in training_signals:
        signal_days[symbol_id].add(day)
    
    distinct_days = set()
    for symbol_id, day, _ in training_signals:
        distinct_days.add(day)
    
    # Compute base rate (precision if all calls up)
    hits = sum(1 for _, _, up in training_signals if up == 1)
    precision = hits / len(training_signals)
    
    # Base rate = proportion of up in the full training set
    all_up = sum(1 for _, _, up in training_opportunities if up == 1)
    base_rate = all_up / len(training_opportunities) if training_opportunities else 0
    
    # Step 8: Compute design effect for clustering by day
    # Group signals by day
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for symbol_id, day, up in training_signals:
        day_counts[day] += 1
        if up == 1:
            day_hits[day] += 1
    
    k = len(day_counts)
    n = len(training_signals)
    
    # Compute ICC (intra-class correlation)
    p_bar = hits / n
    variance_between = sum(day_counts[d] * (day_hits[d]/day_counts[d] - p_bar)**2 for d in day_counts) / (k-1)
    variance_within = sum(day_counts[d] * (day_hits[d]/day_counts[d])*(1 - day_hits[d]/day_counts[d]) for d in day_counts) / (n-k)
    
    icc = variance_between / (variance_between + variance_within) if (variance_between + variance_within) > 0 else 0
    mean_cluster_size = n / k
    deff = 1 + (mean_cluster_size - 1) * icc
    effective_n = n / deff
    
    # Step 9: Compute sealed era statistics
    sealed_signals = [(s, d, u) for s, d, u in signals if d in sealed_days]
    sealed_hits = sum(1 for _, _, up in sealed_signals if up == 1)
    sealed_precision = sealed_hits / len(sealed_signals) if sealed_signals else 0
    
    # Step 10: Lower bound of confidence interval (normal approximation)
    se = math.sqrt(precision * (1 - precision) / effective_n)
    lower_bound = precision - 1.96 * se
    
    # Print required outputs
    print(f"ISSUED={len(training_signals)}")
    print(f"OPPORTUNITIES={len(training_opportunities)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={len(distinct_days)}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
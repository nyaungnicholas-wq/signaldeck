# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 419
# cycle_index: 10
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get all insider trades (code P = purchase, code A = award, but we need open-market purchases)
    # The hypothesis says "open-market purchase", which are trades with code 'P' (purchase) in insider_trades
    # We also need to ensure these are Form 4 filings (but that's implicit in insider_trades table)
    cur.execute("""
        SELECT symbol_id, filed_ts, tx_ts
        FROM insider_trades 
        WHERE code = 'P'
    """)
    purchases = cur.fetchall()
    
    if len(purchases) == 0:
        print("INSUFFICIENT=1")
        return
    
    # Get sentiment features (5-day moving averages)
    # We need mean_score from sentiment_features for each symbol and day
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
    """)
    sentiment_data = cur.fetchall()
    
    # Build sentiment lookup: symbol_id -> {day_str: mean_score}
    sentiment_lookup = defaultdict(dict)
    for symbol_id, day_str, mean_score in sentiment_data:
        sentiment_lookup[symbol_id][day_str] = mean_score
    
    # Get all prediction outcomes for horizon 21
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes 
        WHERE horizon = 21
    """)
    outcomes = cur.fetchall()
    
    # Build outcomes lookup: symbol_id -> {ts: (up, fwd_return)}
    outcomes_lookup = defaultdict(dict)
    for symbol_id, ts, up, fwd_return in outcomes:
        outcomes_lookup[symbol_id][ts] = (up, fwd_return)
    
    # Process each purchase to generate decision points
    decision_points = []
    
    for symbol_id, filed_ts, tx_ts in purchases:
        # Convert filed_ts to date string (YYYY-MM-DD)
        dt = datetime.utcfromtimestamp(filed_ts)
        day_t = dt.strftime('%Y-%m-%d')
        
        # Check if we have sentiment data for this symbol on day_t
        if symbol_id not in sentiment_lookup:
            continue
        if day_t not in sentiment_lookup[symbol_id]:
            continue
            
        # Calculate t-10 date
        dt_minus10 = dt - timedelta(days=10)
        day_t_minus10 = dt_minus10.strftime('%Y-%m-%d')
        
        # Check if we have sentiment data for t-10
        if day_t_minus10 not in sentiment_lookup[symbol_id]:
            continue
        
        # Calculate 5-day moving averages
        # We need mean_score for the 5 days ending at day_t and day_t_minus10
        # Get the 5 days ending at day_t
        dates_t = []
        for i in range(5):
            d = dt - timedelta(days=i)
            dates_t.append(d.strftime('%Y-%m-%d'))
        dates_t.reverse()
        
        # Check if we have all 5 days of sentiment
        valid_t = all(d in sentiment_lookup[symbol_id] for d in dates_t)
        if not valid_t:
            continue
            
        # Calculate 5-day average for day_t
        scores_t = [sentiment_lookup[symbol_id][d] for d in dates_t]
        avg_t = sum(scores_t) / len(scores_t)
        
        # Get the 5 days ending at day_t_minus10
        dates_t10 = []
        for i in range(5):
            d = dt_minus10 - timedelta(days=i)
            dates_t10.append(d.strftime('%Y-%m-%d'))
        dates_t10.reverse()
        
        # Check if we have all 5 days of sentiment
        valid_t10 = all(d in sentiment_lookup[symbol_id] for d in dates_t10)
        if not valid_t10:
            continue
            
        # Calculate 5-day average for day_t_minus10
        scores_t10 = [sentiment_lookup[symbol_id][d] for d in dates_t10]
        avg_t10 = sum(scores_t10) / len(scores_t10)
        
        # Check condition: avg_t >= avg_t10 + 0.2
        if avg_t < avg_t10 + 0.2:
            continue
            
        # Check if we have outcome for this symbol and timestamp
        if symbol_id not in outcomes_lookup:
            continue
        if filed_ts not in outcomes_lookup[symbol_id]:
            continue
            
        up, fwd_return = outcomes_lookup[symbol_id][filed_ts]
        
        decision_points.append({
            'symbol_id': symbol_id,
            'day': day_t,
            'ts': filed_ts,
            'up': up,
            'fwd_return': fwd_return,
            'hit': up == 1  # We predict up, so hit if actual up is 1
        })
    
    if len(decision_points) == 0:
        print("INSUFFICIENT=1")
        return
    
    # Sort decision points by timestamp to split into in-sample and sealed era (most recent 20%)
    decision_points.sort(key=lambda x: x['ts'])
    n = len(decision_points)
    split_idx = int(n * 0.8)
    
    in_sample = decision_points[:split_idx]
    sealed = decision_points[split_idx:]
    
    # Calculate metrics for in-sample
    issued_in = len(in_sample)
    hits_in = sum(dp['hit'] for dp in in_sample)
    precision_in = hits_in / issued_in if issued_in > 0 else 0
    
    # Base rate in issued calls (proportion of up=1)
    base_rate_in = hits_in / issued_in if issued_in > 0 else 0
    
    # Distinct days in issued calls (in-sample only for this calculation, as per instructions)
    distinct_days_in = len(set(dp['day'] for dp in in_sample))
    
    # Calculate design effect (DEFF) and effective N for in-sample
    # Group by day to calculate clustering
    day_counts = defaultdict(int)
    for dp in in_sample:
        day_counts[dp['day']] += 1
    
    m = len(day_counts)  # number of clusters (days)
    if m > 1:
        # Calculate variance between clusters and within clusters
        overall_mean = base_rate_in
        sum_sq_between = 0
        sum_sq_within = 0
        
        for day, count in day_counts.items():
            day_points = [dp for dp in in_sample if dp['day'] == day]
            day_hits = sum(dp['hit'] for dp in day_points)
            day_mean = day_hits / count
            
            # Between-cluster variance contribution
            sum_sq_between += count * (day_mean - overall_mean) ** 2
            
            # Within-cluster variance contribution
            for dp in day_points:
                sum_sq_within += (dp['hit'] - day_mean) ** 2
        
        msb = sum_sq_between / (m - 1)
        n0 = 1 / (m - 1) * (issued_in - sum(count**2 for count in day_counts.values()) / issued_in)
        msw = sum_sq_within / (issued_in - m)
        
        if msb > msw:
            icc = (msb - msw) / (msb + (n0 - 1) * msw)
        else:
            icc = 0
        
        avg_cluster_size = issued_in / m
        deff = 1 + (avg_cluster_size - 1) * icc
    else:
        deff = 1
    
    effective_n_in = issued_in / deff if deff > 0 else issued_in
    
    # Calculate sealed era metrics
    issued_sealed = len(sealed)
    hits_sealed = sum(dp['hit'] for dp in sealed)
    precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0
    
    # Print results
    print(f"ISSUED={issued_in}")
    print(f"OPPORTUNITIES={n}")  # Total decision points considered
    print(f"PRECISION={precision_in:.4f}")
    print(f"BASE_RATE={base_rate_in:.4f}")
    print(f"DISTINCT_DAYS={distinct_days_in}")
    print(f"EFFECTIVE_N={effective_n_in:.4f}")
    print(f"SEALED_PRECISION={precision_sealed:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 438
# cycle_index: 29
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'data/signaldeck.db'

def get_connection():
    return sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)

def load_data():
    conn = get_connection()
    cursor = conn.cursor()
    
    # Get all symbols with at least 100 days of daily news sentiment
    cursor.execute("""
        SELECT symbol_id, COUNT(DISTINCT day) as n_days
        FROM sentiment_features
        GROUP BY symbol_id
        HAVING n_days >= 100
    """)
    symbols_with_sentiment = set(row[0] for row in cursor.fetchall())
    
    if not symbols_with_sentiment:
        return None
    
    # Load sentiment features: symbol_id -> [(day_str, mean_score)]
    cursor.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({})
        ORDER BY symbol_id, day
    """.format(','.join(str(s) for s in symbols_with_sentiment)))
    
    sentiment_data = defaultdict(list)
    for symbol_id, day_str, mean_score in cursor.fetchall():
        sentiment_data[symbol_id].append((day_str, mean_score))
    
    # Load daily price bars: symbol_id -> [(day_str, close)]
    cursor.execute("""
        SELECT symbol_id, date(ts, 'unixepoch') as day_str, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join(str(s) for s in symbols_with_sentiment)))
    
    price_data = defaultdict(list)
    for symbol_id, day_str, close in cursor.fetchall():
        price_data[symbol_id].append((day_str, close))
    
    # Load prediction outcomes for labels
    cursor.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    
    labels = defaultdict(dict)
    for symbol_id, ts, up, fwd_return in cursor.fetchall():
        day_str = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        labels[symbol_id][day_str] = (up, fwd_return)
    
    conn.close()
    return sentiment_data, price_data, labels

def generate_signals(sentiment_data, price_data, labels):
    signals = []
    
    for symbol_id in sentiment_data:
        sent_days = sentiment_data[symbol_id]
        price_days = price_data.get(symbol_id, [])
        
        if len(price_days) < 21:
            continue
            
        # Convert to dictionaries for easier lookup
        sent_dict = {day: score for day, score in sent_days}
        price_dict = {day: close for day, close in price_days}
        
        # Align dates
        common_dates = sorted(set(sent_dict.keys()) & set(price_dict.keys()))
        
        if len(common_dates) < 41:
            continue
            
        # Generate signals for each possible decision day
        for i, decision_day in enumerate(common_dates):
            # Need at least 20 days of history for moving averages
            if i < 19:
                continue
                
            # Need at least 21 days forward for label
            decision_idx = common_dates.index(decision_day)
            if decision_idx + 21 >= len(common_dates):
                continue
                
            # Get last 20 days of sentiment scores
            sentiment_window = [sent_dict[common_dates[j]] 
                               for j in range(i-19, i+1) 
                               if common_dates[j] in sent_dict]
            
            if len(sentiment_window) != 20:
                continue
                
            # Check if 20-day sentiment MA has been strictly positive for 10+ consecutive days
            consecutive_positive = 0
            all_consecutive_positive = True
            
            for j in range(i-19, i+1):
                day = common_dates[j]
                if day in sent_dict:
                    # Compute 20-day moving average for this day
                    window = []
                    for k in range(max(0, j-19), j+1):
                        prev_day = common_dates[k]
                        if prev_day in sent_dict:
                            window.append(sent_dict[prev_day])
                    
                    if len(window) == 20:
                        ma = sum(window) / 20
                        if ma > 0:
                            consecutive_positive += 1
                        else:
                            consecutive_positive = 0
                    else:
                        consecutive_positive = 0
                else:
                    consecutive_positive = 0
                
                if consecutive_positive < 10:
                    all_consecutive_positive = False
                    break
            
            if not all_consecutive_positive:
                continue
                
            # Check 20-day price return
            price_today = price_dict.get(common_dates[i])
            price_20_days_ago = price_dict.get(common_dates[i-19])
            
            if price_today is None or price_20_days_ago is None or price_20_days_ago == 0:
                continue
                
            price_return = (price_today - price_20_days_ago) / price_20_days_ago
            
            if not (-0.02 <= price_return <= 0.02):
                continue
                
            # Get forward 21-day return
            forward_day = common_dates[decision_idx + 21]
            if forward_day not in price_dict or common_dates[i] not in price_dict:
                continue
                
            forward_return = (price_dict[forward_day] - price_dict[common_dates[i]]) / price_dict[common_dates[i]]
            
            # Get label
            label_up = 1 if forward_return > 0 else 0
            
            signals.append({
                'symbol_id': symbol_id,
                'decision_day': common_dates[i],
                'forward_day': forward_day,
                'label_up': label_up,
                'forward_return': forward_return
            })
    
    return signals

def calculate_metrics(signals, test_start_idx):
    if not signals:
        return None
        
    # Sort signals by decision day
    signals.sort(key=lambda x: x['decision_day'])
    
    # Split into train and sealed (most recent 20%)
    total_signals = len(signals)
    sealed_start = int(total_signals * 0.8)
    train_signals = signals[:sealed_start]
    sealed_signals = signals[sealed_start:]
    
    # Calculate overall metrics
    issued = len(signals)
    hits = sum(1 for s in signals if s['label_up'] == 1)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate of predicted class (up) within issued subset
    up_count = sum(1 for s in signals if s['label_up'] == 1)
    base_rate = up_count / issued if issued > 0 else 0
    
    # Distinct days in issued calls
    distinct_days = len(set(s['decision_day'] for s in signals))
    
    # Calculate design effect for effective sample size
    # Cluster by day
    day_clusters = defaultdict(list)
    for s in signals:
        day_clusters[s['decision_day']].append(s['label_up'])
    
    # Calculate intracluster correlation (ICC)
    total_variance = 0
    between_variance = 0
    
    if issued > 1:
        # Overall proportion
        p = hits / issued
        
        # Calculate total variance
        total_variance = sum((1 if s['label_up'] == 1 else 0 - p) ** 2 for s in signals) / (issued - 1)
        
        # Calculate between-cluster variance
        cluster_means = []
        cluster_sizes = []
        for day, outcomes in day_clusters.items():
            if len(outcomes) > 1:
                cluster_mean = sum(outcomes) / len(outcomes)
                cluster_means.append(cluster_mean)
                cluster_sizes.append(len(outcomes))
        
        if len(cluster_means) > 1:
            grand_mean = sum(cluster_means) / len(cluster_means)
            between_variance = sum((m - grand_mean) ** 2 for m in cluster_means) / (len(cluster_means) - 1)
    
    # Design effect calculation
    avg_cluster_size = sum(len(v) for v in day_clusters.values()) / len(day_clusters) if day_clusters else 1
    
    if total_variance > 0 and len(day_clusters) > 1:
        # Calculate ICC
        within_variance = total_variance - between_variance
        icc = between_variance / (between_variance + within_variance) if (between_variance + within_variance) > 0 else 0
        
        # Design effect
        deff = 1 + (avg_cluster_size - 1) * icc
    else:
        deff = 1  # Perfect independence (worst case assumption)
    
    effective_n = issued / deff
    
    # Sealed era metrics
    sealed_hits = sum(1 for s in sealed_signals if s['label_up'] == 1)
    sealed_precision = sealed_hits / len(sealed_signals) if sealed_signals else 0
    
    return {
        'issued': issued,
        'precision': precision,
        'base_rate': base_rate,
        'distinct_days': distinct_days,
        'effective_n': effective_n,
        'sealed_precision': sealed_precision,
        'deff': deff
    }

def main():
    # Load data
    data = load_data()
    if data is None:
        print("INSUFFICIENT=1")
        return
        
    sentiment_data, price_data, labels = data
    
    # Generate signals
    signals = generate_signals(sentiment_data, price_data, labels)
    
    if not signals:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics
    metrics = calculate_metrics(signals, 0)
    
    if metrics is None:
        print("INSUFFICIENT=1")
        return
    
    # Print results
    print(f"ISSUED={metrics['issued']}")
    print(f"OPPORTUNITIES={metrics['issued']}")  # Each signal is a decision point
    print(f"PRECISION={metrics['precision']:.4f}")
    print(f"BASE_RATE={metrics['base_rate']:.4f}")
    print(f"DISTINCT_DAYS={metrics['distinct_days']}")
    print(f"EFFECTIVE_N={metrics['effective_n']:.1f}")
    print(f"SEALED_PRECISION={metrics['sealed_precision']:.4f}")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 304
# cycle_index: 27
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime
from collections import defaultdict
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cursor = conn.cursor()
        
        # Get symbols with both daily bars and sentiment data from 2012 onward
        cursor.execute("""
            SELECT s.id, s.symbol
            FROM symbols s
            WHERE EXISTS (
                SELECT 1 FROM bars b 
                WHERE b.symbol_id = s.id AND b.tf = '1d'
                AND b.ts >= strftime('%s', '2012-01-01')
            )
            AND EXISTS (
                SELECT 1 FROM sentiment_features sf
                WHERE sf.symbol_id = s.id
                AND sf.day >= '2012-01-01'
            )
        """)
        symbols = [row['id'] for row in cursor.fetchall()]
        
        if not symbols:
            print("INSUFFICIENT=1")
            return
            
        # Pre-load all data
        all_calls = []  # List of (day_str, symbol_id, is_hit)
        all_opportunities = []
        
        for symbol_id in symbols:
            # Get bars with volume
            cursor.execute("""
                SELECT ts, close, volume
                FROM bars
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts
            """, (symbol_id,))
            bars = cursor.fetchall()
            
            # Get sentiment scores
            cursor.execute("""
                SELECT day, mean_score
                FROM sentiment_features
                WHERE symbol_id = ? AND mean_score IS NOT NULL
                ORDER BY day
            """, (symbol_id,))
            sentiment_rows = cursor.fetchall()
            
            if len(bars) < 253 or len(sentiment_rows) < 253:
                continue
                
            # Convert bars to dict: ts -> (close, volume)
            bar_dict = {}
            for row in bars:
                ts = row['ts']
                dt = datetime.utcfromtimestamp(ts)
                day_str = dt.strftime('%Y-%m-%d')
                bar_dict[day_str] = {'close': row['close'], 'volume': row['volume']}
            
            # Convert sentiment to dict: day -> score
            sentiment_dict = {row['day']: row['mean_score'] for row in sentiment_rows}
            
            # Get common days
            common_days = sorted(set(bar_dict.keys()) & set(sentiment_dict.keys()))
            
            if len(common_days) < 253:
                continue
            
            # Create indexed list for easy slicing
            day_index = {day: i for i, day in enumerate(common_days)}
            
            # Process each decision point
            for i in range(252, len(common_days)):
                t_day = common_days[i]
                t_data = bar_dict[t_day]
                
                # Get 252-day windows
                window_days = common_days[i-251:i+1]
                
                # Sentiment window
                sentiment_window = [sentiment_dict[d] for d in window_days]
                t_sentiment = sentiment_dict[t_day]
                
                # Check bottom 5th percentile
                count_below = sum(1 for x in sentiment_window if x < t_sentiment)
                sentiment_percentile = count_below / 252.0
                
                if sentiment_percentile > 0.05:
                    continue
                
                # Volume window
                volume_window = [bar_dict[d]['volume'] for d in window_days]
                t_volume = t_data['volume']
                
                # Check below 20th percentile
                count_below_vol = sum(1 for x in volume_window if x < t_volume)
                volume_percentile = count_below_vol / 252.0
                
                if volume_percentile > 0.20:
                    continue
                
                # Check 200-day moving average
                if i < 199:
                    continue
                
                ma_window_days = common_days[i-199:i+1]
                ma_closes = [bar_dict[d]['close'] for d in ma_window_days]
                ma200 = sum(ma_closes) / 200.0
                
                if t_data['close'] < ma200:
                    continue
                
                # Check 5-day decline > 15%
                if i < 5:
                    continue
                
                past_days = common_days[i-5:i]
                if len(past_days) >= 5:
                    start_close = bar_dict[past_days[0]]['close']
                    current_close = t_data['close']
                    decline = (current_close - start_close) / start_close
                    if decline < -0.15:
                        continue
                
                # Issue call - predict up in next 21 days
                # Find outcome in prediction_outcomes
                t_ts = int(datetime.strptime(t_day, '%Y-%m-%d').timestamp())
                cursor.execute("""
                    SELECT up
                    FROM prediction_outcomes
                    WHERE symbol_id = ? AND horizon = 21 AND ts = ?
                """, (symbol_id, t_ts))
                outcome_row = cursor.fetchone()
                
                if outcome_row:
                    is_hit = 1 if outcome_row['up'] else 0
                    all_calls.append((t_day, symbol_id, is_hit))
                
                all_opportunities.append(t_day)
        
        conn.close()
        
        if not all_calls:
            print("INSUFFICIENT=1")
            return
        
        # Sort all calls by day
        all_calls.sort(key=lambda x: x[0])
        
        # Split into training and sealed (last 20%)
        n_total = len(all_calls)
        split_idx = int(n_total * 0.8)
        training_calls = all_calls[:split_idx]
        sealed_calls = all_calls[split_idx:]
        
        # Compute metrics for all calls
        issued = len(all_calls)
        hits = sum(c[2] for c in all_calls)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate within issued subset (same as precision by definition)
        base_rate = precision
        
        # Distinct days
        distinct_days = len(set(c[0] for c in all_calls))
        
        # Effective N (design effect)
        # Group by day to compute cluster sizes
        day_clusters = defaultdict(list)
        for day, _, hit in all_calls:
            day_clusters[day].append(hit)
        
        # Calculate ICC using variance of cluster means and overall variance
        cluster_means = [sum(cluster)/len(cluster) for cluster in day_clusters.values()]
        overall_mean = hits / issued if issued > 0 else 0
        
        # Variance of cluster means
        variance_cluster_means = sum((m - overall_mean)**2 for m in cluster_means) / len(cluster_means)
        
        # Overall variance
        variance_overall = sum((hit - overall_mean)**2 for _, _, hit in all_calls) / issued if issued > 0 else 1
        
        icc = variance_cluster_means / variance_overall if variance_overall > 0 else 0
        
        # Average cluster size
        avg_cluster_size = issued / len(day_clusters) if day_clusters else 1
        
        # Design effect
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Sealed precision
        sealed_hits = sum(c[2] for c in sealed_calls)
        sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
        
        # Print required lines
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={len(all_opportunities)}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
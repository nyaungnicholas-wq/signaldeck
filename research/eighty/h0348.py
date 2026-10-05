# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 347
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return
    
    try:
        # Get universe: symbols with daily bars and sentiment_features from 2018-07 onward
        # Use daily bars (tf='1d') and sentiment_features
        # Respect as-of: bars.ts < label ts, sentiment_features.day < label date
        
        # We need to compute:
        # 1. 10-day news sentiment average (using sentiment_features.mean_score)
        # 2. EPS growth rate YoY (using fundamentals)
        # 3. 5% threshold over 2-year rolling window
        
        # First get all symbols that have both daily bars and sentiment_features after 2018-07
        cursor = conn.cursor()
        cursor.execute("""
            SELECT DISTINCT s.id 
            FROM symbols s
            JOIN bars b ON s.id = b.symbol_id AND b.tf = '1d' AND b.ts >= 1530230400  -- 2018-07-01
            JOIN sentiment_features sf ON s.id = sf.symbol_id AND sf.day >= '2018-07-01'
            WHERE s.market = 'stocks'
            LIMIT 2000
        """)
        symbol_ids = [row[0] for row in cursor.fetchall()]
        
        if len(symbol_ids) < 50:
            print("INSUFFICIENT=1")
            conn.close()
            return
        
        # Get all decision points (daily bars after 2018-07)
        # For each symbol, for each day (as unix timestamp), we need:
        # - 10-day average of sentiment up to that day
        # - EPS growth rate (YoY) using fundamentals
        
        # We'll process in batches by symbol to avoid huge memory usage
        all_calls = []
        total_opportunities = 0
        
        for symbol_id in symbol_ids:
            # Get daily bars for this symbol (2018-07 onward)
            cursor.execute("""
                SELECT ts FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND ts >= 1530230400
                ORDER BY ts
            """, (symbol_id,))
            bar_ts = [row[0] for row in cursor.fetchall()]
            
            if len(bar_ts) < 100:  # Need enough history
                continue
            
            # Get sentiment features for this symbol
            cursor.execute("""
                SELECT day, mean_score FROM sentiment_features
                WHERE symbol_id = ? AND day >= '2018-07-01'
                ORDER BY day
            """, (symbol_id,))
            sent_data = cursor.fetchall()
            sent_dict = {}
            for day, score in sent_data:
                # Convert day string to unix timestamp (start of day UTC)
                from datetime import datetime
                dt = datetime.strptime(day, '%Y-%m-%d')
                ts = int(dt.timestamp())
                sent_dict[ts] = score
            
            # Get EPS data for this symbol
            cursor.execute("""
                SELECT fetched_at, value, as_of FROM fundamentals
                WHERE symbol_id = ? AND metric = 'EPS'
                ORDER BY fetched_at
            """, (symbol_id,))
            eps_data = cursor.fetchall()
            
            # Process each bar timestamp
            for ts in bar_ts:
                total_opportunities += 1
                
                # Compute 10-day sentiment average up to this ts
                # Get last 10 trading days with sentiment data
                recent_sent = []
                for prev_ts in reversed(bar_ts):
                    if prev_ts >= ts:
                        continue
                    if prev_ts in sent_dict:
                        recent_sent.append(sent_dict[prev_ts])
                    if len(recent_sent) >= 10:
                        break
                
                if len(recent_sent) < 10:
                    continue
                
                avg_sent = sum(recent_sent) / len(recent_sent)
                
                # Compute 2-year distribution of 10-day averages
                # For efficiency, we'll use approximation: sample recent 2-year window
                window_sent = []
                window_start = ts - (2 * 365 * 24 * 3600)
                for prev_ts in reversed(bar_ts):
                    if prev_ts >= ts or prev_ts < window_start:
                        continue
                    if prev_ts in sent_dict:
                        # Recompute 10-day average for this previous point
                        prev_recent = []
                        for pp_ts in reversed(bar_ts):
                            if pp_ts >= prev_ts:
                                continue
                            if pp_ts in sent_dict:
                                prev_recent.append(sent_dict[pp_ts])
                            if len(prev_recent) >= 10:
                                break
                        if len(prev_recent) >= 10:
                            prev_avg = sum(prev_recent) / len(prev_recent)
                            window_sent.append(prev_avg)
                    if len(window_sent) >= 200:  # Sample for efficiency
                        break
                
                if len(window_sent) < 50:  # Need enough for distribution
                    continue
                
                # Compute 5th percentile threshold
                window_sent.sort()
                idx = int(len(window_sent) * 0.05)
                threshold = window_sent[idx]
                
                # Check if current average is in bottom 5%
                if avg_sent > threshold:
                    continue  # Not in bottom 5%
                
                # Now check EPS growth rate (YoY)
                # Get most recent EPS fetched before ts
                current_eps = None
                for fetched_at, value, as_of in reversed(eps_data):
                    if fetched_at <= ts:
                        try:
                            current_eps = float(value)
                        except (ValueError, TypeError):
                            continue
                        break
                
                if current_eps is None:
                    continue
                
                # Get EPS from approximately 1 year ago (within ±30 days)
                one_year_ago = ts - 365 * 24 * 3600
                eps_year_ago = None
                for fetched_at, value, as_of in eps_data:
                    if fetched_at <= one_year_ago and abs(fetched_at - one_year_ago) < 30 * 24 * 3600:
                        try:
                            eps_year_ago = float(value)
                        except (ValueError, TypeError):
                            continue
                        break
                
                if eps_year_ago is None or eps_year_ago == 0:
                    continue
                
                # Compute growth rate
                growth = (current_eps - eps_year_ago) / abs(eps_year_ago)
                if growth <= 0:
                    continue  # Not positive
                
                # Conditions met: issue a call (up)
                # Now get the label from prediction_outcomes with horizon=21
                cursor.execute("""
                    SELECT up, fwd_return FROM prediction_outcomes
                    WHERE symbol_id = ? AND ts = ? AND horizon = 21
                """, (symbol_id, ts))
                result = cursor.fetchone()
                
                if result is None:
                    continue  # No label available
                
                up, fwd_return = result
                if up is None:
                    continue
                
                all_calls.append({
                    'symbol_id': symbol_id,
                    'ts': ts,
                    'up': up,
                    'fwd_return': fwd_return
                })
        
        conn.close()
        
        if len(all_calls) < 10:
            print("INSUFFICIENT=1")
            return
        
        # Sort calls by timestamp
        all_calls.sort(key=lambda x: x['ts'])
        
        # Split into train (80%) and sealed (20%) by time
        split_idx = int(len(all_calls) * 0.8)
        train_calls = all_calls[:split_idx]
        sealed_calls = all_calls[split_idx:]
        
        # Calculate metrics
        issued = len(all_calls)
        hits = sum(1 for call in all_calls if call['up'] == 1)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate of "up" in issued calls
        base_rate = hits / issued
        
        # Distinct days in issued calls
        distinct_days = len(set(call['ts'] // 86400 for call in all_calls))
        
        # Calculate design effect for effective sample size
        # Cluster by day, calculate intra-class correlation
        if distinct_days > 1:
            # Group calls by day
            from collections import defaultdict
            day_groups = defaultdict(list)
            for call in all_calls:
                day = call['ts'] // 86400
                day_groups[day].append(call['up'])
            
            # Calculate variance between and within clusters
            total_var = 0
            between_var = 0
            
            grand_mean = base_rate
            
            for day, labels in day_groups.items():
                n_cluster = len(labels)
                if n_cluster == 0:
                    continue
                
                cluster_mean = sum(labels) / n_cluster
                
                # Within-cluster variance
                within_var = sum((1 - cluster_mean) ** 2 if label == 1 
                               else (0 - cluster_mean) ** 2 
                               for label in labels) / n_cluster
                total_var += within_var * n_cluster
                
                # Between-cluster variance
                between_var += n_cluster * (cluster_mean - grand_mean) ** 2
            
            # Total variance
            total_var = total_var / issued
            
            # Intra-class correlation
            ICC = between_var / (between_var + total_var) if (between_var + total_var) > 0 else 0
            
            # Average cluster size
            avg_cluster_size = issued / distinct_days
            
            # Design effect
            design_effect = 1 + (avg_cluster_size - 1) * ICC
            effective_n = issued / design_effect if design_effect > 0 else issued
        else:
            effective_n = issued
            design_effect = 1
        
        # Sealed era metrics
        sealed_issued = len(sealed_calls)
        sealed_hits = sum(1 for call in sealed_calls if call['up'] == 1)
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={total_opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
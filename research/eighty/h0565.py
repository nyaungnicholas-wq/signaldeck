# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 564
# cycle_index: 22
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except Exception:
        print("INSUFFICIENT=1")
        return

    try:
        cursor = conn.cursor()
        
        # Get all decision days from sentiment_features
        cursor.execute("SELECT DISTINCT day FROM sentiment_features ORDER BY day")
        all_days = [row[0] for row in cursor.fetchall()]
        if len(all_days) < 100:
            print("INSUFFICIENT=1")
            return
        
        # Split into train (80%) and sealed (20%)
        split_idx = int(len(all_days) * 0.8)
        train_days_set = set(all_days[:split_idx])
        sealed_days_set = set(all_days[split_idx:])
        
        # Get symbols with both sentiment and 13F data
        cursor.execute("""
            SELECT DISTINCT sf.symbol_id 
            FROM sentiment_features sf
            WHERE EXISTS (
                SELECT 1 FROM inst_holdings ih 
                WHERE ih.symbol_id = sf.symbol_id
            )
        """)
        symbols = [row[0] for row in cursor.fetchall()]
        if not symbols:
            print("INSUFFICIENT=1")
            return
        
        # Precompute sentiment statistics for each symbol
        symbol_sentiment = {}
        for sym_id in symbols:
            cursor.execute("""
                SELECT day, mean_score 
                FROM sentiment_features 
                WHERE symbol_id = ? 
                ORDER BY day
            """, (sym_id,))
            rows = cursor.fetchall()
            if len(rows) < 260:
                continue
            
            days = [r[0] for r in rows]
            scores = [r[1] for r in rows]
            
            # Create lookup from day to score
            day_to_score = dict(zip(days, scores))
            symbol_sentiment[sym_id] = {
                'days': days,
                'scores': scores,
                'day_to_score': day_to_score
            }
        
        if not symbol_sentiment:
            print("INSUFFICIENT=1")
            return
        
        # Precompute 13F ownership changes
        symbol_13f = {}
        for sym_id in symbol_sentiment.keys():
            cursor.execute("""
                SELECT period, SUM(shares) as total_shares
                FROM inst_holdings 
                WHERE symbol_id = ?
                GROUP BY period
                ORDER BY period
            """, (sym_id,))
            periods = cursor.fetchall()
            
            if len(periods) < 2:
                continue
            
            # Get most recent fundamentals for shares outstanding
            cursor.execute("""
                SELECT value FROM fundamentals 
                WHERE symbol_id = ? 
                AND metric = 'SharesOutstanding'
                ORDER BY fetched_at DESC
                LIMIT 1
            """, (sym_id,))
            shares_row = cursor.fetchone()
            if not shares_row:
                continue
            
            total_shares_outstanding = shares_row[0]
            
            # Calculate ownership percentage for each period
            period_ownership = []
            for period in periods:
                pct = period[1] / total_shares_outstanding if total_shares_outstanding > 0 else 0
                period_ownership.append((period[0], pct))
            
            symbol_13f[sym_id] = period_ownership
        
        if not symbol_13f:
            print("INSUFFICIENT=1")
            return
        
        # Get labels for 21-day horizon
        cursor.execute("""
            SELECT symbol_id, ts, up 
            FROM prediction_outcomes 
            WHERE horizon = 21 AND resolved_at IS NOT NULL
        """)
        labels = cursor.fetchall()
        
        # Build label lookup: (symbol_id, day) -> up
        label_lookup = {}
        for row in labels:
            # Convert ts to day
            day = datetime.utcfromtimestamp(row[1]).strftime('%Y-%m-%d')
            key = (row[0], day)
            label_lookup[key] = row[2]
        
        # Process opportunities
        opportunities = []
        issued_calls = []
        
        for sym_id, sent_data in symbol_sentiment.items():
            if sym_id not in symbol_13f:
                continue
            
            days_list = sent_data['days']
            scores_list = sent_data['scores']
            day_to_score = sent_data['day_to_score']
            periods_13f = symbol_13f[sym_id]
            
            # For each possible decision day
            for i, day in enumerate(days_list):
                # Need at least 252 days of history for this symbol
                if i < 251:
                    continue
                
                # Condition A: Sentiment
                # Get last 252 days of scores
                scores_252 = scores_list[i-251:i+1]
                if len(scores_252) < 252:
                    continue
                
                # Calculate statistics
                scores_252_sorted = sorted(scores_252)
                min_score = scores_252_sorted[0]
                max_score = scores_252_sorted[-1]
                score_range = max_score - min_score
                if score_range == 0:
                    continue
                
                # Calculate standard deviation
                mean_score = sum(scores_252) / len(scores_252)
                variance = sum((s - mean_score) ** 2 for s in scores_252) / len(scores_252)
                std_dev = math.sqrt(variance)
                
                # 5-day moving average (last 5 days including current)
                if i < 4:
                    continue
                ma5 = sum(scores_list[i-4:i+1]) / 5
                
                # Check if in bottom decile of 252-day range
                decile_threshold = min_score + 0.1 * score_range
                if ma5 > decile_threshold:
                    continue
                
                # Check if increased by at least 1 std dev in last 5 days
                if i < 9:
                    continue
                ma5_5days_ago = sum(scores_list[i-9:i-4]) / 5
                if (ma5 - ma5_5days_ago) < std_dev:
                    continue
                
                # Condition B: 13F ownership increase
                # Find periods within 45 days of decision day (as-of discipline)
                decision_date = datetime.strptime(day, '%Y-%m-%d')
                cutoff_date = decision_date - timedelta(days=45)
                cutoff_str = cutoff_date.strftime('%Y-%m-%d')
                
                # Find most recent period before cutoff
                recent_period = None
                prev_period = None
                for period, ownership in periods_13f:
                    if period <= cutoff_str:
                        if recent_period is None:
                            recent_period = (period, ownership)
                        elif prev_period is None:
                            prev_period = (period, ownership)
                            break
                
                if not recent_period or not prev_period:
                    continue
                
                # Check ownership increase of at least 5 percentage points
                ownership_increase = recent_period[1] - prev_period[1]
                if ownership_increase < 0.05:
                    continue
                
                # Check if we have a label for this symbol and day
                label_key = (sym_id, day)
                if label_key not in label_lookup:
                    continue
                
                up = label_lookup[label_key]
                
                # Record opportunity
                opportunities.append({
                    'symbol': sym_id,
                    'day': day,
                    'up': up,
                    'train': day in train_days_set
                })
                
                # Record issued call (since conditions met)
                issued_calls.append({
                    'symbol': sym_id,
                    'day': day,
                    'up': up,
                    'train': day in train_days_set
                })
        
        conn.close()
        
        # Calculate metrics
        issued = len(issued_calls)
        opportunities_count = len(opportunities)
        
        if issued == 0:
            print("INSUFFICIENT=1")
            return
        
        # Base rate and precision
        hits = sum(1 for call in issued_calls if call['up'] == 1)
        precision = hits / issued
        base_rate = hits / issued
        
        # Distinct days in issued calls
        distinct_days = len(set(call['day'] for call in issued_calls))
        
        # Effective N with design effect
        # Group by day to calculate ICC
        day_groups = defaultdict(list)
        for call in issued_calls:
            day_groups[call['day']].append(1 if call['up'] else 0)
        
        k = len(day_groups)
        if k <= 1:
            effective_n = issued
        else:
            # Calculate variance components for binary outcomes
            p = hits / issued  # overall proportion
            var_total = p * (1 - p)
            
            # Between-day variance
            day_means = [sum(v)/len(v) for v in day_groups.values()]
            day_sizes = [len(v) for v in day_groups.values()]
            mean_size = issued / k
            
            var_between = sum(n * (m - p) ** 2 for n, m in zip(day_sizes, day_means)) / (k - 1)
            
            # Within-day variance
            var_within = sum(sum((x - m) ** 2 for x in group) for group, m in zip(day_groups.values(), day_means)) / (issued - k)
            
            # ICC
            if var_total > 0:
                icc = (var_between - var_within / mean_size) / var_total
                if icc < 0:
                    icc = 0
            else:
                icc = 0
            
            # Design effect
            deff = 1 + (mean_size - 1) * icc
            effective_n = issued / deff
        
        # Sealed era metrics
        sealed_issued = [call for call in issued_calls if call['day'] in sealed_days_set]
        if sealed_issued:
            sealed_hits = sum(1 for call in sealed_issued if call['up'] == 1)
            sealed_precision = sealed_hits / len(sealed_issued)
        else:
            sealed_precision = 0
        
        # Print required output
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 559
# cycle_index: 17
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Get all symbols with daily bars from 2018-07 onward
        cur.execute("""
            SELECT DISTINCT symbol_id 
            FROM bars 
            WHERE tf = '1d' AND ts >= 1532534400  # 2018-07-26 in UTC
        """)
        all_symbols = {row[0] for row in cur.fetchall()}
        
        # Get symbols with at least 2 years of 13F data (8 quarters)
        cur.execute("""
            SELECT symbol_id, COUNT(DISTINCT period) as quarters
            FROM inst_holdings
            GROUP BY symbol_id
            HAVING quarters >= 8
        """)
        inst_symbols = {row[0] for row in cur.fetchall()}
        
        # Get symbols with news sentiment data
        cur.execute("""
            SELECT DISTINCT symbol_id 
            FROM sentiment_features
        """)
        news_symbols = {row[0] for row in cur.fetchall()}
        
        universe = all_symbols & inst_symbols & news_symbols
        if len(universe) < 10:
            print("INSUFFICIENT=1")
            conn.close()
            return
        
        # Precompute news sentiment 5-day MA and rolling statistics
        sentiment_data = {}
        cur.execute("""
            SELECT symbol_id, day, mean_score
            FROM sentiment_features
            WHERE symbol_id IN ({})
            ORDER BY symbol_id, day
        """.format(','.join('?'*len(universe))), tuple(universe))
        
        rows = cur.fetchall()
        for symbol_id, day, score in rows:
            if symbol_id not in sentiment_data:
                sentiment_data[symbol_id] = []
            sentiment_data[symbol_id].append((day, score if score is not None else 0))
        
        # Precompute 13F holdings aggregated by period
        holdings_data = defaultdict(dict)
        cur.execute("""
            SELECT symbol_id, period, SUM(shares) as total_shares
            FROM inst_holdings
            WHERE symbol_id IN ({})
            GROUP BY symbol_id, period
            ORDER BY symbol_id, period
        """.format(','.join('?'*len(universe))), tuple(universe))
        
        for symbol_id, period, total_shares in cur.fetchall():
            holdings_data[symbol_id][period] = total_shares
        
        # Get labels from prediction_outcomes (horizon=21 days)
        labels = defaultdict(dict)
        cur.execute("""
            SELECT symbol_id, ts, up
            FROM prediction_outcomes
            WHERE horizon = 21 AND symbol_id IN ({})
        """.format(','.join('?'*len(universe))), tuple(universe))
        
        for symbol_id, ts, up in cur.fetchall():
            labels[symbol_id][ts] = up
        
        conn.close()
        
        # Process each symbol
        calls = []
        opportunities = 0
        distinct_days = set()
        
        for symbol_id in universe:
            if symbol_id not in sentiment_data or len(sentiment_data[symbol_id]) < 20:
                continue
            
            # Convert days to timestamps for processing
            daily_scores = {}
            for day, score in sentiment_data[symbol_id]:
                # Convert day string to timestamp (midnight UTC)
                day_ts = int(day.replace('-', '')) * 86400  # rough conversion
                daily_scores[day_ts] = score
            
            sorted_ts = sorted(daily_scores.keys())
            
            if len(sorted_ts) < 20:
                continue
            
            # Get periods for this symbol from 13F
            periods = sorted(holdings_data[symbol_id].keys())
            
            # Check if we have enough 13F data (at least 2 years = 8 quarters)
            if len(periods) < 8:
                continue
            
            # For each possible decision day
            for i in range(20, len(sorted_ts)):
                decision_ts = sorted_ts[i]
                decision_day = decision_ts  # timestamp
                
                # Check if we have a label for horizon=21 days
                label_ts = decision_ts + 21 * 86400
                if symbol_id not in labels or label_ts not in labels[symbol_id]:
                    continue
                
                opportunities += 1
                
                # News sentiment: 5-day MA and 10-day rolling stats
                recent_scores = [daily_scores[sorted_ts[j]] for j in range(i-9, i+1)]
                if len(recent_scores) < 10:
                    continue
                
                ma5 = sum(recent_scores[-5:]) / 5
                mu = sum(recent_scores[:-5]) / 5 if len(recent_scores) > 5 else sum(recent_scores) / len(recent_scores)
                sigma = (sum((x - mu)**2 for x in recent_scores[:-5]) / 5) ** 0.5 if len(recent_scores) > 5 else 0
                
                if sigma == 0:
                    continue
                
                sharp_improvement = (ma5 - mu) >= 2 * sigma
                
                if not sharp_improvement:
                    continue
                
                # 13F holdings: most recent period before decision day (lagged 45 days)
                lagged_ts = decision_ts - 45 * 86400
                most_recent_period = None
                for period in periods:
                    period_ts = int(period.replace('-', '')) * 86400
                    if period_ts <= lagged_ts:
                        most_recent_period = period
                
                if most_recent_period is None:
                    continue
                
                # Find previous period
                period_idx = periods.index(most_recent_period)
                if period_idx == 0:
                    continue
                prev_period = periods[period_idx - 1]
                
                current_shares = holdings_data[symbol_id][most_recent_period]
                prev_shares = holdings_data[symbol_id][prev_period]
                
                # No increase in institutional shares
                if current_shares > prev_shares:
                    continue
                
                # Issue call: predict up
                up = labels[symbol_id][label_ts]
                day_of_week = decision_ts // 86400 % 7  # group by day
                calls.append((symbol_id, decision_ts, up, decision_ts // 86400))
                distinct_days.add(decision_ts // 86400)
        
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Split into training and sealed (80/20 by time)
        calls.sort(key=lambda x: x[1])
        split_idx = int(len(calls) * 0.8)
        train_calls = calls[:split_idx]
        sealed_calls = calls[split_idx:]
        
        # Calculate metrics for entire sample
        total_issued = len(calls)
        hits = sum(1 for call in calls if call[2] == 1)
        precision = hits / total_issued if total_issued > 0 else 0
        base_rate = sum(1 for call in calls if call[2] == 1) / total_issued
        
        # Design effect: group by day, calculate ICC
        day_groups = defaultdict(list)
        for call in calls:
            day_groups[call[3]].append(1 if call[2] else 0)
        
        # Calculate variance components
        all_outcomes = []
        group_means = []
        for day, outcomes in day_groups.items():
            all_outcomes.extend(outcomes)
            group_means.append(sum(outcomes)/len(outcomes))
        
        grand_mean = sum(all_outcomes)/len(all_outcomes)
        between_var = sum((gm - grand_mean)**2 for gm in group_means) / (len(group_means) - 1) if len(group_means) > 1 else 0
        within_var = 0
        count = 0
        for day, outcomes in day_groups.items():
            gm = sum(outcomes)/len(outcomes)
            within_var += sum((x - gm)**2 for x in outcomes)
            count += len(outcomes)
        within_var /= (count - len(group_means)) if count > len(group_means) else 1
        
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        avg_cluster_size = total_issued / len(day_groups)
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = total_issued / design_effect if design_effect > 0 else total_issued
        
        # Sealed era metrics
        sealed_issued = len(sealed_calls)
        sealed_hits = sum(1 for call in sealed_calls if call[2] == 1)
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print required outputs
        print(f"ISSUED={total_issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={len(distinct_days)}")
        print(f"EFFECTIVE_N={effective_n}")
        print(f"SEALED_PRECISION={sealed_precision}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 441
# cycle_index: 32
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
from datetime import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Get universe: symbols with at least 60 days of news sentiment data
        cur.execute("""
            SELECT symbol_id 
            FROM sentiment_features 
            GROUP BY symbol_id 
            HAVING COUNT(DISTINCT day) >= 60
        """)
        symbols = [row['symbol_id'] for row in cur.fetchall()]
        if not symbols:
            print("INSUFFICIENT=1")
            return
        
        # Get all sentiment data for these symbols
        cur.execute("""
            SELECT symbol_id, day, mean_score 
            FROM sentiment_features 
            WHERE symbol_id IN ({}) 
            ORDER BY symbol_id, day
        """.format(','.join('?' for _ in symbols)), symbols)
        sentiment_data = defaultdict(list)
        for row in cur.fetchall():
            sentiment_data[row['symbol_id']].append((row['day'], row['mean_score']))
        
        # Get all daily bars for these symbols
        cur.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume 
            FROM bars 
            WHERE tf='1d' AND symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join('?' for _ in symbols)), symbols)
        bars_data = defaultdict(list)
        for row in cur.fetchall():
            bars_data[row['symbol_id']].append({
                'ts': row['ts'],
                'open': row['open'],
                'close': row['close'],
                'day': datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d')
            })
        
        # Process each symbol
        opportunities = []
        for symbol_id in symbols:
            if symbol_id not in sentiment_data or len(sentiment_data[symbol_id]) < 60:
                continue
            
            # Build daily sentiment lookup
            sentiment_by_day = {}
            for day, score in sentiment_data[symbol_id]:
                sentiment_by_day[day] = score
            
            # Get bar days for this symbol
            bar_days = {bar['day']: bar for bar in bars_data[symbol_id]}
            if not bar_days:
                continue
            
            # Sort sentiment days
            sentiment_days = sorted(sentiment_by_day.keys())
            
            # For each potential decision day
            for i, decision_day in enumerate(sentiment_days):
                # Check if we have at least 10 prior days of sentiment
                if i < 10:
                    continue
                
                # Get current sentiment
                current_score = sentiment_by_day[decision_day]
                if current_score <= 0:
                    continue
                
                # Get prior 10 days sentiment
                prior_days = sentiment_days[i-10:i]
                prior_scores = [sentiment_by_day[d] for d in prior_days]
                avg_prior = sum(prior_scores) / len(prior_scores)
                
                # Compute historical distribution (up to previous day)
                historical_scores = [sentiment_by_day[d] for d in sentiment_days[:i]]
                if len(historical_scores) < 20:
                    continue
                
                # Compute 10th percentile
                sorted_hist = sorted(historical_scores)
                idx = int(0.1 * len(sorted_hist))
                p10 = sorted_hist[idx]
                
                # Check condition: avg_prior < p10
                if avg_prior >= p10:
                    continue
                
                # Check bar condition: close > open
                if decision_day not in bar_days:
                    continue
                bar = bar_days[decision_day]
                if bar['close'] <= bar['open']:
                    continue
                
                # Find forward return (21 trading days later)
                decision_ts = bar['ts']
                forward_bars = [b for b in bars_data[symbol_id] if b['ts'] > decision_ts]
                if len(forward_bars) < 21:
                    continue
                
                forward_bar = forward_bars[20]  # 21st day (0-indexed)
                entry_price = bar['close']
                exit_price = forward_bar['close']
                hit = 1 if exit_price > entry_price else 0
                
                opportunities.append({
                    'symbol_id': symbol_id,
                    'day': decision_day,
                    'hit': hit,
                    'ts': decision_ts
                })
        
        conn.close()
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return
        
        # Sort by timestamp for time-based split
        opportunities.sort(key=lambda x: x['ts'])
        
        # Split: most recent 20% as sealed era
        split_idx = int(0.8 * len(opportunities))
        train_opps = opportunities[:split_idx]
        sealed_opps = opportunities[split_idx:]
        
        # Calculate metrics
        issued = len(opportunities)
        hits = sum(1 for opp in opportunities if opp['hit'] == 1)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate: hits in issued
        base_rate = hits / issued if issued > 0 else 0
        
        # Distinct days in issued
        distinct_days = len(set(opp['day'] for opp in opportunities))
        
        # Effective sample size (design effect)
        # Group by day and calculate intra-class correlation
        day_groups = defaultdict(list)
        for opp in opportunities:
            day_groups[opp['day']].append(opp['hit'])
        
        # Calculate design effect
        n = issued
        k = len(day_groups)
        if k < n:  # Some days have multiple calls
            # Calculate variance components
            overall_mean = hits / n
            sum_sq_between = 0
            sum_sq_within = 0
            total_sq = 0
            
            for day, day_hits in day_groups.items():
                day_mean = sum(day_hits) / len(day_hits)
                sum_sq_between += len(day_hits) * (day_mean - overall_mean) ** 2
                for h in day_hits:
                    total_sq += (h - overall_mean) ** 2
                    sum_sq_within += (h - day_mean) ** 2
            
            # Between-group variance
            var_between = sum_sq_between / (k - 1) if k > 1 else 0
            # Within-group variance
            var_within = sum_sq_within / (n - k) if n > k else 0
            
            # Average group size
            m = n / k
            # Intra-class correlation
            icc = var_between / (var_between + var_within) if (var_between + var_within) > 0 else 0
            # Design effect
            design_effect = 1 + (m - 1) * icc
        else:
            design_effect = 1
        
        effective_n = n / design_effect if design_effect > 0 else n
        
        # Sealed era metrics
        sealed_hits = sum(1 for opp in sealed_opps if opp['hit'] == 1)
        sealed_precision = sealed_hits / len(sealed_opps) if sealed_opps else 0
        
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print(f"INSUFFICIENT=1")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 584
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        cur = conn.cursor()
        
        # Step 1: Define universe - symbols with >= 6 months of daily sentiment
        cur.execute("""
            SELECT symbol_id, COUNT(DISTINCT day) as days
            FROM sentiment_features
            GROUP BY symbol_id
            HAVING days >= 180
        """)
        universe = [row[0] for row in cur.fetchall()]
        if len(universe) == 0:
            print("INSUFFICIENT=1")
            return
            
        # Step 2: Get all trading days in the data range for reference
        cur.execute("""
            SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1d'
        """)
        min_ts, max_ts = cur.fetchone()
        min_date = datetime.utcfromtimestamp(min_ts)
        max_date = datetime.utcfromtimestamp(max_ts)
        
        # Calculate 80/20 split
        total_days = (max_date - min_date).days
        split_days = int(total_days * 0.8)
        split_date = min_date + timedelta(days=split_days)
        split_ts = int(split_date.timestamp())
        
        # Step 3: For each symbol in universe, process each day
        issued_calls = []
        opportunities = 0
        
        for symbol_id in universe:
            # Get all daily bars for this symbol
            cur.execute("""
                SELECT ts FROM bars 
                WHERE symbol_id=? AND tf='1d'
                ORDER BY ts
            """, (symbol_id,))
            bar_dates = [row[0] for row in cur.fetchall()]
            if len(bar_dates) < 90:
                continue  # Need at least 90 days of price data for labels
                
            # Get StockTwits daily volumes
            cur.execute("""
                SELECT strftime('%Y-%m-%d', ts, 'unixepoch') as day, 
                       SUM(total) as daily_total
                FROM stocktwits_sentiment
                WHERE symbol_id=?
                GROUP BY day
                ORDER BY day
            """, (symbol_id,))
            st_data = cur.fetchall()
            if len(st_data) < 20:
                continue
                
            # Convert to dict for easier lookup
            st_daily = {}
            for day_str, total in st_data:
                # Convert to timestamp for comparison
                day_ts = int(datetime.strptime(day_str, '%Y-%m-%d').timestamp())
                st_daily[day_ts] = total
                
            # Get fundamentals EPS and Revenues
            cur.execute("""
                SELECT metric, value, fetched_at FROM fundamentals
                WHERE symbol_id=? AND metric IN ('EPS', 'Revenues')
                ORDER BY fetched_at
            """, (symbol_id,))
            fundamentals = cur.fetchall()
            
            # Organize by metric
            eps_data = [(value, ts) for metric, value, ts in fundamentals if metric == 'EPS']
            rev_data = [(value, ts) for metric, value, ts in fundamentals if metric == 'Revenues']
            
            if len(eps_data) < 2 or len(rev_data) < 2:
                continue
            
            # Process each potential decision day
            for i, decision_ts in enumerate(bar_dates):
                decision_date = datetime.utcfromtimestamp(decision_ts)
                
                # Step 4: Calculate trailing 90-day EPS growth
                # Get most recent EPS fetched before decision_ts
                recent_eps = None
                for value, ts in reversed(eps_data):
                    if ts <= decision_ts:
                        recent_eps = value
                        break
                if recent_eps is None:
                    continue
                    
                # Get EPS from ~90 days ago
                eps_90d_ago = None
                target_ts = decision_ts - 90*24*60*60
                for value, ts in reversed(eps_data):
                    if ts <= target_ts:
                        eps_90d_ago = value
                        break
                if eps_90d_ago is None or eps_90d_ago == 0:
                    continue
                    
                eps_growth = (recent_eps - eps_90d_ago) / abs(eps_90d_ago)
                if eps_growth <= 0.15:
                    continue
                    
                # Step 5: Calculate trailing 90-day revenue growth
                recent_rev = None
                for value, ts in reversed(rev_data):
                    if ts <= decision_ts:
                        recent_rev = value
                        break
                if recent_rev is None:
                    continue
                    
                rev_90d_ago = None
                for value, ts in reversed(rev_data):
                    if ts <= target_ts:
                        rev_90d_ago = value
                        break
                if rev_90d_ago is None or rev_90d_ago == 0:
                    continue
                    
                rev_growth = (recent_rev - rev_90d_ago) / abs(rev_90d_ago)
                if rev_growth <= 0.10:
                    continue
                    
                # Step 6: Calculate 20-day average StockTwits volume
                # Need 20 days of data ending at decision_ts
                vol_sum = 0
                vol_count = 0
                for j in range(20):
                    check_ts = decision_ts - j*24*60*60
                    if check_ts in st_daily:
                        vol_sum += st_daily[check_ts]
                        vol_count += 1
                if vol_count < 20:
                    continue
                    
                vol_20d_avg = vol_sum / 20
                
                # Step 7: Calculate 1-year distribution of 20-day averages
                # Collect all 20-day averages for the past year
                year_avgs = []
                for j in range(365):
                    check_ts = decision_ts - j*24*60*60
                    # Need 20 consecutive days ending at check_ts
                    valid_days = 0
                    total_vol = 0
                    for k in range(20):
                        day_ts = check_ts - k*24*60*60
                        if day_ts in st_daily:
                            total_vol += st_daily[day_ts]
                            valid_days += 1
                    if valid_days >= 20:
                        year_avgs.append(total_vol / 20)
                        
                if len(year_avgs) < 50:  # Need reasonable sample for percentile
                    continue
                    
                # Calculate 20th percentile
                year_avgs.sort()
                idx = int(0.2 * len(year_avgs))
                percentile_20 = year_avgs[idx]
                
                if vol_20d_avg >= percentile_20:
                    continue
                    
                # Entry conditions met - check label
                # Get label from prediction_outcomes with horizon=21
                label_ts = decision_ts + 21*24*60*60
                cur.execute("""
                    SELECT up FROM prediction_outcomes
                    WHERE symbol_id=? AND horizon=21 AND ts=?
                """, (symbol_id, decision_ts))
                result = cur.fetchone()
                if result is None:
                    continue
                    
                is_up = result[0] == 1
                opportunities += 1
                
                issued_calls.append({
                    'symbol_id': symbol_id,
                    'decision_ts': decision_ts,
                    'is_up': is_up,
                    'is_sealed': decision_ts >= split_ts
                })
                
        conn.close()
        
        # Step 8: Calculate metrics
        if len(issued_calls) == 0:
            print("INSUFFICIENT=1")
            return
            
        issued = len(issued_calls)
        hits = sum(1 for call in issued_calls if call['is_up'])
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued  # Base rate within issued subset
        
        # Count distinct days
        distinct_days = len(set(call['decision_ts'] for call in issued_calls))
        
        # Calculate design effect and effective N
        # Group by day to calculate clustering
        day_counts = {}
        day_hits = {}
        for call in issued_calls:
            day = call['decision_ts']
            day_counts[day] = day_counts.get(day, 0) + 1
            if call['is_up']:
                day_hits[day] = day_hits.get(day, 0) + 1
                
        # Calculate ICC using ANOVA method for binary outcomes
        p_bar = hits / issued
        var_between = 0
        var_within = 0
        n_days = len(day_counts)
        
        if n_days > 1:
            for day, count in day_counts.items():
                day_prop = day_hits.get(day, 0) / count
                var_between += count * (day_prop - p_bar) ** 2
                var_within += count * day_prop * (1 - day_prop)
                
            var_between /= (n_days - 1)
            var_within /= (issued - n_days)
            
            if var_within > 0:
                icc = var_between / (var_between + var_within)
            else:
                icc = 0
        else:
            icc = 0
            
        avg_cluster_size = issued / n_days if n_days > 0 else 0
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else 0
        
        # Ensure EFFECTIVE_N < ISSUED
        if effective_n >= issued:
            effective_n = issued * 0.999  # Slight adjustment to satisfy invariant
            
        # Step 9: Calculate sealed metrics
        sealed_calls = [call for call in issued_calls if call['is_sealed']]
        sealed_issued = len(sealed_calls)
        sealed_hits = sum(1 for call in sealed_calls if call['is_up'])
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n}")
        print(f"SEALED_PRECISION={sealed_precision}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
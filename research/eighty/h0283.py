# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 282
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'data/signaldeck.db'

def main():
    try:
        conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Get all trading days from daily bars (symbol with most data)
        cur.execute("""
            SELECT ts FROM bars 
            WHERE tf='1d' 
            ORDER BY ts
        """)
        all_timestamps = [row[0] for row in cur.fetchall()]
        
        if len(all_timestamps) < 100:
            print("INSUFFICIENT=1")
            return
            
        # Convert to dates and get unique trading days
        trading_dates = sorted(set(datetime.utcfromtimestamp(ts).date() for ts in all_timestamps))
        
        # Identify quarter boundaries (calendar quarters)
        def get_quarter_key(d):
            return (d.year, (d.month-1)//3 + 1)
            
        quarters = defaultdict(list)
        for d in trading_dates:
            quarters[get_quarter_key(d)].append(d)
            
        # For each quarter, find fifth-to-last trading day
        entry_days = []
        quarter_ends = {}
        for qkey, qdays in sorted(quarters.items()):
            if len(qdays) >= 5:
                entry_day = qdays[-5]  # fifth-to-last
                quarter_end = qdays[-1]  # last trading day of quarter
                entry_days.append(entry_day)
                quarter_ends[entry_day] = quarter_end
                
        if len(entry_days) < 2:
            print("INSUFFICIENT=1")
            return
            
        # Prepare data structures
        all_calls = []  # (entry_day, symbol_id, is_correct)
        opportunities_count = 0
        
        # Process each entry day
        for entry_day in entry_days:
            entry_ts = int(datetime.combine(entry_day, datetime.min.time()).timestamp())
            
            # Get symbols with at least 65 prior trading days
            # First get trading days up to entry_day
            prior_days = [d for d in trading_dates if d <= entry_day]
            if len(prior_days) < 65:
                continue
                
            # Get all symbols with daily bars on entry_day
            cur.execute("""
                SELECT symbol_id, close, volume FROM bars
                WHERE tf='1d' AND ts = ?
            """, (entry_ts,))
            day_data = cur.fetchall()
            
            if not day_data:
                continue
                
            # Calculate trailing 60-day return and 20-day ADV for each symbol
            symbol_signals = []
            for symbol_id, close_price, volume in day_data:
                # Get 60 prior trading days (including entry_day)
                # We need 60 bars ending with entry_day
                lookback_ts = int(datetime.combine(
                    prior_days[-60] if len(prior_days) >= 60 else prior_days[0],
                    datetime.min.time()
                ).timestamp())
                
                cur.execute("""
                    SELECT close FROM bars
                    WHERE symbol_id = ? AND tf='1d' AND ts BETWEEN ? AND ?
                    ORDER BY ts
                """, (symbol_id, lookback_ts, entry_ts))
                closes = [row[0] for row in cur.fetchall()]
                
                if len(closes) < 60:
                    continue
                    
                # Trailing 60-day return
                trailing_return = (closes[-1] / closes[0]) - 1
                
                # 20-day average dollar volume
                adv_timestamps = [int(datetime.combine(d, datetime.min.time()).timestamp()) 
                                for d in prior_days[-20:]]
                placeholders = ','.join('?'*len(adv_timestamps))
                cur.execute(f"""
                    SELECT close, volume FROM bars
                    WHERE symbol_id = ? AND tf='1d' AND ts IN ({placeholders})
                """, (symbol_id,) + tuple(adv_timestamps))
                adv_data = cur.fetchall()
                
                if len(adv_data) < 20:
                    continue
                    
                adv_dollar = sum(c * v for c, v in adv_data) / 20
                if adv_dollar <= 10_000_000:
                    continue
                    
                symbol_signals.append((symbol_id, trailing_return))
                opportunities_count += 1
                
            if not symbol_signals:
                continue
                
            # Calculate cross-sectional decile
            returns = [r for _, r in symbol_signals]
            returns.sort()
            decile_cutoff = returns[int(len(returns) * 0.9)] if len(returns) > 10 else returns[-1]
            
            # Issue calls for top decile (already filtered by ADV)
            called_symbols = set()
            quarter_end = quarter_ends[entry_day]
            quarter_end_ts = int(datetime.combine(quarter_end, datetime.min.time()).timestamp())
            
            for symbol_id, trailing_return in symbol_signals:
                if trailing_return >= decile_cutoff and symbol_id not in called_symbols:
                    # Get forward return from entry to quarter_end
                    cur.execute("""
                        SELECT close FROM bars
                        WHERE symbol_id = ? AND tf='1d' AND ts = ?
                    """, (symbol_id, quarter_end_ts))
                    end_price = cur.fetchone()
                    
                    if end_price:
                        fwd_return = (end_price[0] / close_price) - 1
                        is_correct = fwd_return > 0
                        all_calls.append((entry_day, symbol_id, is_correct))
                        called_symbols.add(symbol_id)
        
        if not all_calls:
            print("INSUFFICIENT=1")
            return
            
        # Split into training and sealed (most recent 20% by time)
        all_calls.sort(key=lambda x: x[0])
        split_idx = int(len(all_calls) * 0.8)
        train_calls = all_calls[:split_idx]
        sealed_calls = all_calls[split_idx:]
        
        # Calculate metrics
        def calculate_metrics(calls):
            if not calls:
                return None, None, None, None, None, None
                
            issued = len(calls)
            correct = sum(1 for _, _, is_correct in calls if is_correct)
            precision = correct / issued if issued > 0 else 0
            
            # Base rate within issued subset (proportion of UP outcomes)
            base_rate = precision  # Since we only issue UP calls, this equals precision
            
            # Distinct days
            days = set(day for day, _, _ in calls)
            distinct_days = len(days)
            
            # Design effect calculation (clustering by day)
            day_groups = defaultdict(list)
            for day, _, is_correct in calls:
                day_groups[day].append(1 if is_correct else 0)
            
            # Overall mean
            p = precision
            
            # Calculate ICC
            total_var = p * (1 - p)
            
            # Between-day variance
            day_means = []
            day_ns = []
            for day, outcomes in day_groups.items():
                day_means.append(sum(outcomes) / len(outcomes))
                day_ns.append(len(outcomes))
            
            if len(day_means) < 2:
                # Cannot compute design effect with <2 clusters
                design_effect = 1.0
            else:
                mean_day_mean = sum(day_means) / len(day_means)
                between_var = sum(n * (m - mean_day_mean)**2 for n, m in zip(day_ns, day_means)) / (issued - 1)
                
                # Within-day variance
                within_var = 0
                for outcomes in day_groups.values():
                    for x in outcomes:
                        within_var += (x - (sum(outcomes)/len(outcomes)))**2
                within_var /= (issued - len(day_groups))
                
                # ICC
                if within_var == 0:
                    icc = 0
                else:
                    m0 = (issued - sum(n**2 for n in day_ns) / issued) / (len(day_groups) - 1)
                    icc = (between_var - within_var) / (between_var + (m0 - 1) * within_var)
                
                # Design effect
                avg_cluster_size = issued / len(day_groups)
                design_effect = 1 + (avg_cluster_size - 1) * icc
            
            effective_n = issued / design_effect if design_effect > 0 else issued
            
            return issued, precision, base_rate, distinct_days, effective_n
        
        # Calculate for full and sealed
        full_metrics = calculate_metrics(all_calls)
        sealed_metrics = calculate_metrics(sealed_calls)
        
        if not full_metrics or not sealed_metrics:
            print("INSUFFICIENT=1")
            return
            
        issued_full, precision_full, base_rate_full, distinct_days_full, effective_n_full = full_metrics
        issued_sealed, precision_sealed, _, _, _ = sealed_metrics
        
        # Check invariants
        if distinct_days_full > issued_full:
            print("INSUFFICIENT=1")
            return
        if effective_n_full >= issued_full:
            print("INSUFFICIENT=1")
            return
            
        # Print results
        print(f"ISSUED={issued_full}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision_full:.6f}")
        print(f"BASE_RATE={base_rate_full:.6f}")
        print(f"DISTINCT_DAYS={distinct_days_full}")
        print(f"EFFECTIVE_N={effective_n_full:.6f}")
        print(f"SEALED_PRECISION={precision_sealed:.6f}")
        
        conn.close()
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
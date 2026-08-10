# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 445
# cycle_index: 36
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Get all US stocks with daily news sentiment data
    c.execute("""
        SELECT DISTINCT sf.symbol_id, s.symbol
        FROM sentiment_features sf
        JOIN symbols s ON sf.symbol_id = s.id
        WHERE s.market = 'stocks' AND s.active = 1
    """)
    symbols = {row[0]: row[1] for row in c.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get unemployment claims data (ICSA)
    c.execute("""
        SELECT ts/86400 as day_num, value
        FROM macro_series
        WHERE series = 'ICSA'
        ORDER BY ts
    """)
    unemp_data = c.fetchall()
    if len(unemp_data) < 56:
        print("INSUFFICIENT=1")
        return
    
    # Convert to dictionary: day_num -> value
    unemp_dict = {}
    for day_num, value in unemp_data:
        unemp_dict[day_num] = value
    
    # Calculate 4-week moving averages and check condition
    unemp_condition_days = set()
    sorted_days = sorted(unemp_dict.keys())
    for i in range(56, len(sorted_days)):
        current_day = sorted_days[i]
        # Calculate current 28-day moving average
        current_window = [unemp_dict[d] for d in sorted_days[i-27:i+1]]
        current_ma = sum(current_window) / len(current_window)
        
        # Check past 4 weeks (each week: moving average decreased)
        valid = True
        for weeks_back in range(1, 5):
            window_start = i - 7 * weeks_back - 27
            window_end = i - 7 * weeks_back
            if window_start < 0:
                valid = False
                break
            past_window = [unemp_dict[d] for d in sorted_days[window_start:window_end+1]]
            past_ma = sum(past_window) / len(past_window)
            if current_ma >= past_ma:
                valid = False
                break
        
        if valid:
            unemp_condition_days.add(current_day)
    
    # Get sentiment data for each symbol
    c.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        ORDER BY symbol_id, day
    """)
    sentiment_data = c.fetchall()
    
    # Organize by symbol
    sentiment_by_symbol = {}
    for symbol_id, day, score in sentiment_data:
        if symbol_id not in symbols:
            continue
        if symbol_id not in sentiment_by_symbol:
            sentiment_by_symbol[symbol_id] = []
        sentiment_by_symbol[symbol_id].append((day, score))
    
    # Process each symbol
    all_signals = []
    for symbol_id, symbol in symbols.items():
        if symbol_id not in sentiment_by_symbol:
            continue
        
        points = sentiment_by_symbol[symbol_id]
        if len(points) < 21:
            continue
        
        # Convert day strings to datetime objects
        dates = [datetime.strptime(d, '%Y-%m-%d') for d, _ in points]
        scores = [s for _, s in points]
        
        # Calculate daily sentiment change and trailing statistics
        for i in range(20, len(points) - 21):  # Need 20 days trailing + 21 days forward
            current_day_str = points[i][0]
            current_day = datetime.strptime(current_day_str, '%Y-%m-%d')
            day_num = int(current_day.timestamp() / 86400)
            
            # Check unemployment condition
            if day_num not in unemp_condition_days:
                continue
            
            # Calculate daily sentiment change ΔS_t
            delta_s = scores[i] - scores[i-1]
            
            # Calculate trailing 20-day statistics for ΔS
            trailing_deltas = [scores[j] - scores[j-1] for j in range(i-19, i+1)]
            mean_delta = sum(trailing_deltas) / len(trailing_deltas)
            var_delta = sum((d - mean_delta)**2 for d in trailing_deltas) / len(trailing_deltas)
            std_delta = var_delta ** 0.5
            
            # Check sentiment condition: ΔS_t <= mean(ΔS) - 0.5 * std(ΔS)
            if delta_s > mean_delta - 0.5 * std_delta:
                continue
            
            # Look for label in prediction_outcomes
            forward_date = dates[i+21]
            forward_date_str = forward_date.strftime('%Y-%m-%d')
            
            c.execute("""
                SELECT up, fwd_return
                FROM prediction_outcomes
                WHERE symbol_id = ? 
                AND horizon = 21
                AND ts >= ?
                LIMIT 1
            """, (symbol_id, current_day_str))
            
            row = c.fetchone()
            if row is None:
                continue
            
            up = row[0]  # 1 for positive return, 0 for negative
            
            all_signals.append({
                'symbol': symbol,
                'symbol_id': symbol_id,
                'date': current_day_str,
                'day_num': day_num,
                'up': up,
                'delta_s': delta_s,
                'mean_delta': mean_delta,
                'std_delta': std_delta
            })
    
    conn.close()
    
    if not all_signals:
        print("INSUFFICIENT=1")
        return
    
    # Sort signals by date
    all_signals.sort(key=lambda x: x['date'])
    
    # Split into in-sample and sealed era (most recent 20%)
    n = len(all_signals)
    split_idx = int(n * 0.8)
    in_sample = all_signals[:split_idx]
    sealed_era = all_signals[split_idx:]
    
    # Helper function to calculate metrics
    def calculate_metrics(signals):
        if not signals:
            return 0, 0, 0, 0
        
        issued = len(signals)
        hits = sum(1 for s in signals if s['up'] == 1)
        precision = hits / issued if issued > 0 else 0
        
        # Count distinct days
        distinct_days = len(set(s['date'] for s in signals))
        
        # Calculate base rate (precision would equal base rate if unskilled)
        base_rate = hits / issued if issued > 0 else 0
        
        # Calculate design effect for effective N
        # Group by day
        day_groups = {}
        for s in signals:
            day = s['date']
            if day not in day_groups:
                day_groups[day] = []
            day_groups[day].append(s['up'])
        
        # Calculate ICC
        K = len(day_groups)
        if K < 2:
            design_effect = 1.0
        else:
            total_mean = base_rate
            between_var = 0
            within_var = 0
            total_count = issued
            
            for day, outcomes in day_groups.items():
                if len(outcomes) > 1:
                    day_mean = sum(outcomes) / len(outcomes)
                    between_var += len(outcomes) * (day_mean - total_mean) ** 2
                    
                    # Within-day variance
                    p_day = day_mean
                    within_var += len(outcomes) * p_day * (1 - p_day)
            
            if K > 1:
                between_var /= (K - 1)
                within_var /= (total_count - K)
                
                m0 = total_count / K  # average cluster size
                icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
                design_effect = 1 + (m0 - 1) * icc
            else:
                design_effect = 1.0
        
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        return issued, precision, distinct_days, base_rate, effective_n
    
    # Calculate metrics for in-sample
    issued_in, precision_in, distinct_in, base_rate_in, effective_n_in = calculate_metrics(in_sample)
    
    # Calculate sealed era metrics
    issued_sealed, precision_sealed, distinct_sealed, base_rate_sealed, _ = calculate_metrics(sealed_era)
    
    # Output required lines
    print(f"ISSUED={issued_in}")
    print(f"OPPORTUNITIES={len(all_signals)}")
    print(f"PRECISION={precision_in:.4f}")
    print(f"BASE_RATE={base_rate_in:.4f}")
    print(f"DISTINCT_DAYS={distinct_in}")
    print(f"EFFECTIVE_N={effective_n_in:.4f}")
    print(f"SEALED_PRECISION={precision_sealed:.4f}")

if __name__ == "__main__":
    main()
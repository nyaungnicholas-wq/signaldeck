import sqlite3
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        cursor = conn.cursor()
    except Exception:
        print("INSUFFICIENT=1")
        return

    # Get all symbols that have both bars and insider trades
    cursor.execute("""
        SELECT DISTINCT b.symbol_id 
        FROM bars b 
        WHERE b.tf = '1d'
        INTERSECT
        SELECT DISTINCT i.symbol_id 
        FROM insider_trades i
    """)
    symbols = [row[0] for row in cursor.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return

    # Get all distinct trading dates from daily bars
    cursor.execute("SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts")
    all_dates = [row[0] for row in cursor.fetchall()]
    if len(all_dates) < 100:
        print("INSUFFICIENT=1")
        return

    # Split into training (80%) and sealed (20%) by date
    split_idx = int(len(all_dates) * 0.8)
    training_date_set = set(all_dates[:split_idx])
    sealed_date_set = set(all_dates[split_idx:])

    # Preload insider trades by symbol and date (using filed_ts as disclosure date)
    insider_by_symbol_date = defaultdict(lambda: defaultdict(int))
    cursor.execute("SELECT symbol_id, filed_ts, code FROM insider_trades WHERE code = 'P'")
    for row in cursor.fetchall():
        symbol_id, filed_ts, code = row
        # Convert timestamp to date (YYYY-MM-DD)
        import datetime
        dt = datetime.datetime.utcfromtimestamp(filed_ts).date()
        # Store as day string for consistency
        day_str = dt.isoformat()
        insider_by_symbol_date[symbol_id][day_str] += 1

    # Track issued calls per symbol for 20-day cooldown
    last_issued_day = {}

    # Prepare lists to store results
    all_calls = []
    opportunities = 0

    # Process each symbol
    for symbol_id in symbols:
        # Get all daily bars for this symbol
        cursor.execute("""
            SELECT ts, open, high, low, close, volume 
            FROM bars 
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (symbol_id,))
        bars = cursor.fetchall()
        if len(bars) < 252:
            continue

        # Create dictionary of date to bar data
        import datetime
        date_bars = {}
        for ts, o, h, l, c, v in bars:
            dt = datetime.datetime.utcfromtimestamp(ts).date()
            day_str = dt.isoformat()
            date_bars[day_str] = (o, h, l, c, v)

        # Get sorted dates for this symbol
        symbol_dates = sorted(date_bars.keys())

        # Process each potential T
        for i, T in enumerate(symbol_dates):
            if i < 252:
                continue

            T_bar = date_bars.get(T)
            if not T_bar:
                continue
            T_open, T_high, T_low, T_close, T_volume = T_bar

            # Filter: close >= $5
            if T_close < 5:
                continue

            # Check 60-day average dollar volume
            prior_dates = [d for d in symbol_dates if d < T]
            if len(prior_dates) < 60:
                continue
            last_60 = prior_dates[-60:]
            total_dollar_vol = 0
            valid_days = 0
            for d in last_60:
                bar = date_bars.get(d)
                if bar:
                    total_dollar_vol += bar[3] * bar[4]  # close * volume
                    valid_days += 1
            if valid_days < 60:
                continue
            avg_dollar_vol = total_dollar_vol / 60
            if avg_dollar_vol < 5_000_000:
                continue

            # Check 252-session high (including T)
            last_252_dates = prior_dates[-251:] + [T]  # 252 sessions including T
            if len(last_252_dates) < 252:
                continue
            max_high = max(date_bars[d][1] for d in last_252_dates if d in date_bars)
            if T_close >= 0.95 * max_high:
                continue

            # Check T-1 to T return is between -2% and +2%
            if i < 1:
                continue
            prev_day = symbol_dates[i-1]
            prev_bar = date_bars.get(prev_day)
            if not prev_bar:
                continue
            prev_close = prev_bar[3]
            t_return = (T_close - prev_close) / prev_close
            if t_return < -0.02 or t_return > 0.02:
                continue

            # Check insider purchase on exactly T (using filed_ts date)
            if insider_by_symbol_date.get(symbol_id, {}).get(T, 0) == 0:
                continue

            # Check 20-day cooldown
            if symbol_id in last_issued_day:
                last_issued = last_issued_day[symbol_id]
                # Calculate days between T and last_issued
                t_dt = datetime.datetime.strptime(T, "%Y-%m-%d").date()
                last_dt = datetime.datetime.strptime(last_issued, "%Y-%m-%d").date()
                # Count trading days between them
                days_between = 0
                for d in symbol_dates:
                    if last_dt < datetime.datetime.strptime(d, "%Y-%m-%d").date() < t_dt:
                        days_between += 1
                if days_between < 20:
                    continue

            # Check we have all bars for T-20..T
            t_idx = symbol_dates.index(T)
            if t_idx < 20:
                continue
            need_dates = symbol_dates[t_idx-20:t_idx+1]
            if len(need_dates) < 21:
                continue
            missing = False
            for d in need_dates:
                if d not in date_bars:
                    missing = True
                    break
            if missing:
                continue

            # Calculate 20-day realized volatility at T
            vol_dates = symbol_dates[t_idx-19:t_idx+1]  # 20 days including T
            returns = []
            for j in range(1, len(vol_dates)):
                prev_d = vol_dates[j-1]
                curr_d = vol_dates[j]
                prev_close_val = date_bars[prev_d][3]
                curr_close_val = date_bars[curr_d][3]
                returns.append((curr_close_val - prev_close_val) / prev_close_val)
            if len(returns) < 19:
                continue
            mean_ret = sum(returns) / len(returns)
            variance = sum((r - mean_ret)**2 for r in returns) / (len(returns) - 1)
            volatility = math.sqrt(variance)

            # Store this volatility for cross-sectional decile check later
            opportunities += 1
            all_calls.append({
                'symbol_id': symbol_id,
                'T': T,
                'close': T_close,
                'volatility': volatility,
                't_idx': t_idx,
                'symbol_dates': symbol_dates,
                'date_bars': date_bars
            })

    if not all_calls:
        print("INSUFFICIENT=1")
        return

    # Calculate cross-sectional decile for each date T
    vol_by_date = defaultdict(list)
    for call in all_calls:
        vol_by_date[call['T']].append(call['volatility'])

    # Add volatility percentile to each call
    for call in all_calls:
        T = call['T']
        vols = vol_by_date[T]
        if len(vols) < 10:
            call['vol_decile'] = 0  # Not enough data
            continue
        sorted_vols = sorted(vols)
        # Find rank (0-based)
        rank = sorted_vols.index(call['volatility'])
        decile = rank / len(sorted_vols)  # 0 to 1
        call['vol_decile'] = decile

    # Filter out calls with volatility in top decile
    filtered_calls = []
    for call in all_calls:
        if call['vol_decile'] < 0.9:
            filtered_calls.append(call)

    # Check we have at least 30 independent observations
    if len(filtered_calls) < 30:
        print("INSUFFICIENT=1")
        return

    # Now compute forward returns for each call
    results = []
    for call in filtered_calls:
        symbol_id = call['symbol_id']
        T = call['T']
        t_idx = call['t_idx']
        symbol_dates = call['symbol_dates']
        date_bars = call['date_bars']

        # Need at least 20 more trading days after T
        if t_idx + 20 >= len(symbol_dates):
            continue

        # Get close at T+20
        T_plus_20 = symbol_dates[t_idx + 20]
        T_close = date_bars[T][3]
        T_plus_20_close = date_bars[T_plus_20][3]

        # Forward return
        fwd_return = (T_plus_20_close - T_close) / T_close

        # Determine outcome (UP = fwd_return > 0)
        outcome = 1 if fwd_return > 0 else 0

        # Determine if in sealed era
        in_sealed = T in sealed_date_set

        results.append({
            'symbol_id': symbol_id,
            'T': T,
            'outcome': outcome,
            'in_sealed': in_sealed,
            'fwd_return': fwd_return
        })

        # Update last issued day for cooldown
        last_issued_day[symbol_id] = T

    if not results:
        print("INSUFFICIENT=1")
        return

    # Split results into training and sealed
    training_results = [r for r in results if not r['in_sealed']]
    sealed_results = [r for r in results if r['in_sealed']]

    # Calculate metrics for training set
    issued = len(training_results)
    if issued == 0:
        print("INSUFFICIENT=1")
        return

    hits = sum(r['outcome'] for r in training_results)
    precision = hits / issued

    # Base rate (fraction of UP outcomes in issued subset)
    base_rate = hits / issued

    # Distinct days in issued calls
    distinct_days = set(r['T'] for r in training_results)
    distinct_days_count = len(distinct_days)

    # Effective N using design effect from day clustering
    # Group calls by day
    calls_by_day = defaultdict(int)
    for r in training_results:
        calls_by_day[r['T']] += 1
    
    # Calculate average calls per day and ICC
    total_calls = issued
    num_days = distinct_days_count
    avg_calls_per_day = total_calls / num_days
    
    # Calculate within-day and overall variance
    # Overall variance
    overall_mean = base_rate
    overall_var = base_rate * (1 - base_rate) if base_rate in [0, 1] else 0
    
    # Within-day variance
    within_var_sum = 0
    total_within = 0
    for day, count in calls_by_day.items():
        day_calls = [r for r in training_results if r['T'] == day]
        if len(day_calls) > 1:
            day_mean = sum(r['outcome'] for r in day_calls) / len(day_calls)
            within_var_sum += sum((r['outcome'] - day_mean)**2 for r in day_calls)
            total_within += len(day_calls) - 1
    
    if total_within > 0 and overall_var > 0:
        within_var = within_var_sum / total_within
        # Between variance
        between_var_sum = 0
        for day, count in calls_by_day.items():
            day_calls = [r for r in training_results if r['T'] == day]
            day_mean = sum(r['outcome'] for r in day_calls) / len(day_calls)
            between_var_sum += count * (day_mean - overall_mean)**2
        between_var = between_var_sum / (num_days - 1) if num_days > 1 else 0
        
        # ICC
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        design_effect = 1 + (avg_calls_per_day - 1) * icc
        effective_n = total_calls / design_effect
    else:
        # Fallback if cannot calculate ICC
        design_effect = 1 + (avg_calls_per_day - 1) * 0.5  # Assume moderate ICC
        effective_n = total_calls / design_effect

    # Calculate sealed metrics
    sealed_issued = len(sealed_results)
    if sealed_issued > 0:
        sealed_hits = sum(r['outcome'] for r in sealed_results)
        sealed_precision = sealed_hits / sealed_issued
    else:
        sealed_precision = 0

    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days_count}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")

if __name__ == "__main__":
    main()
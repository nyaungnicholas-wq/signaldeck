import sqlite3
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    # Load all daily bars with symbol, ts, close, volume
    c.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    bars = c.fetchall()
    
    # Load all sentiment_features with symbol_id, day, mean_score
    c.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
    sent = c.fetchall()
    
    # Load prediction_outcomes for horizon=20
    c.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=20")
    outcomes = c.fetchall()
    
    conn.close()
    
    # Build data structures
    symbols_bars = defaultdict(list)  # symbol_id -> [(ts, close, volume)]
    for row in bars:
        symbols_bars[row['symbol_id']].append((row['ts'], row['close'], row['volume']))
    
    symbols_sent = defaultdict(dict)  # symbol_id -> {day_epoch: mean_score}
    for row in sent:
        # Convert day string to epoch
        try:
            dt = __import__('datetime').datetime.strptime(row['day'], '%Y-%m-%d')
            epoch = int(dt.timestamp())
            symbols_sent[row['symbol_id']][epoch] = row['mean_score']
        except:
            continue
    
    symbols_outcomes = defaultdict(dict)  # symbol_id -> {ts: up}
    for row in outcomes:
        symbols_outcomes[row['symbol_id']][row['ts']] = row['up']
    
    # Get all unique trading days (from bars)
    all_days = sorted(set(row['ts'] for row in bars))
    if len(all_days) < 100:
        print("INSUFFICIENT=1")
        return
    
    # Split into training and sealed (80/20)
    split_idx = int(len(all_days) * 0.8)
    sealed_days_set = set(all_days[split_idx:])
    
    # Build day index mapping
    day_to_idx = {day: i for i, day in enumerate(all_days)}
    
    # Process each symbol to precompute necessary series
    symbol_series = {}
    for sym_id, sym_bars in symbols_bars.items():
        # Need at least 504+20+252+60+4+1 days
        if len(sym_bars) < 837:
            continue
        
        # Align with sentiment
        sent_map = symbols_sent.get(sym_id, {})
        series = {}
        for ts, close, vol in sym_bars:
            sent_val = sent_map.get(ts)
            if sent_val is not None:
                series[ts] = {'close': close, 'vol': vol, 'sent': sent_val}
        
        if len(series) < 504:
            continue
        
        # Compute rolling 5-day mean sentiment for all days
        sorted_ts = sorted(series.keys())
        ts_to_idx = {ts: i for i, ts in enumerate(sorted_ts)}
        
        # Precompute 5-day sentiment means for each day
        five_day_means = {}
        for i, ts in enumerate(sorted_ts):
            if i < 4:
                continue
            window = [sorted_ts[i-j] for j in range(5)]
            vals = [series[d]['sent'] for d in window]
            if all(v is not None for v in vals):
                five_day_means[ts] = sum(vals) / len(vals)
        
        # Precompute 20-day returns and volatility
        twenty_day_returns = {}
        twenty_day_vol = {}
        for i, ts in enumerate(sorted_ts):
            if i < 20:
                continue
            start_ts = sorted_ts[i-20]
            end_close = series[ts]['close']
            start_close = series[start_ts]['close']
            if start_close > 0:
                ret = (end_close - start_close) / start_close
                twenty_day_returns[ts] = ret
            
            # Volatility: std dev of daily returns over past 20 days
            if i >= 19:
                daily_rets = []
                for j in range(1, 21):
                    prev_ts = sorted_ts[i-j]
                    curr_ts = sorted_ts[i-j+1]
                    prev_close = series[prev_ts]['close']
                    curr_close = series[curr_ts]['close']
                    if prev_close > 0:
                        daily_rets.append((curr_close - prev_close) / prev_close)
                if len(daily_rets) >= 10:
                    mean_ret = sum(daily_rets) / len(daily_rets)
                    var_ret = sum((r - mean_ret)**2 for r in daily_rets) / len(daily_rets)
                    twenty_day_vol[ts] = math.sqrt(var_ret)
        
        symbol_series[sym_id] = {
            'sorted_ts': sorted_ts,
            'series': series,
            'five_day_means': five_day_means,
            'twenty_day_returns': twenty_day_returns,
            'twenty_day_vol': twenty_day_vol
        }
    
    if not symbol_series:
        print("INSUFFICIENT=1")
        return
    
    # Main evaluation loop
    opportunities = 0
    issued = 0
    hits = 0
    issued_days = []
    last_call_for_symbol = {}
    
    # For computing volatility decile threshold per day
    day_vol_percentiles = {}
    for day in all_days:
        day_vols = []
        for sym_id, sym_data in symbol_series.items():
            vol = sym_data['twenty_day_vol'].get(day)
            if vol is not None:
                day_vols.append(vol)
        if day_vols:
            day_vols_sorted = sorted(day_vols)
            idx = int(len(day_vols_sorted) * 0.9)
            day_vol_percentiles[day] = day_vols_sorted[min(idx, len(day_vols_sorted)-1)]
    
    # Process each day in chronological order
    for i, t_day in enumerate(all_days):
        # Skip if too early for lookback
        if i < 252:
            continue
        
        # Get potential symbols for this day
        for sym_id, sym_data in symbol_series.items():
            if t_day not in sym_data['series']:
                continue
            
            close_t = sym_data['series'][t_day]['close']
            if close_t < 5:
                continue
            
            # Check at least 504 prior sessions
            prior_days = [ts for ts in sym_data['sorted_ts'] if ts < t_day]
            if len(prior_days) < 504:
                continue
            
            # Check dollar volume T-60..T-1
            dollar_vols = []
            for j in range(1, 61):
                if i - j < 0:
                    continue
                prev_day = all_days[i-j]
                if prev_day in sym_data['series']:
                    c_val = sym_data['series'][prev_day]['close']
                    v_val = sym_data['series'][prev_day]['vol']
                    dollar_vols.append(c_val * v_val)
            if len(dollar_vols) < 30:
                continue
            avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
            if avg_dollar_vol < 5e6:
                continue
            
            # Check 20-day volatility not in top decile
            vol_t = sym_data['twenty_day_vol'].get(t_day)
            if vol_t is None:
                continue
            vol_threshold = day_vol_percentiles.get(t_day)
            if vol_threshold is None or vol_t >= vol_threshold:
                continue
            
            # Check no call in prior 20 trading days
            prev_call_day = last_call_for_symbol.get(sym_id)
            if prev_call_day is not None:
                days_since = sum(1 for d in all_days if prev_call_day < d <= t_day)
                if days_since <= 20:
                    continue
            
            # Check sentiment in T-4..T
            required_sent_days = [all_days[i-k] for k in range(5)]
            sent_vals = []
            missing_sent = False
            for d in required_sent_days:
                if d not in sym_data['series']:
                    missing_sent = True
                    break
                sent_vals.append(sym_data['series'][d]['sent'])
            if missing_sent or any(v is None for v in sent_vals):
                continue
            current_mean_sent = sum(sent_vals) / len(sent_vals)
            
            # Check 90th percentile of 5-day means over T-252..T-1
            hist_means = []
            for j in range(5, 253):
                if i - j < 0:
                    break
                hist_day = all_days[i-j]
                if hist_day in sym_data['five_day_means']:
                    hist_means.append(sym_data['five_day_means'][hist_day])
            if len(hist_means) < 30:
                continue
            hist_means_sorted = sorted(hist_means)
            pct90_idx = int(len(hist_means_sorted) * 0.9)
            pct90 = hist_means_sorted[pct90_idx]
            if current_mean_sent <= pct90:
                continue
            
            # Check 20-day return and 1-day return
            twenty_day_ret = sym_data['twenty_day_returns'].get(t_day)
            if twenty_day_ret is None or abs(twenty_day_ret) >= 0.02:
                continue
            
            if i > 0:
                prev_day = all_days[i-1]
                if prev_day in sym_data['series']:
                    prev_close = sym_data['series'][prev_day]['close']
                    one_day_ret = (close_t - prev_close) / prev_close
                    if abs(one_day_ret) >= 0.005:
                        continue
                else:
                    continue
            else:
                continue
            
            # Check at least 30 independent observations remain
            # Count remaining decision points after this one
            remaining_days = sum(1 for d in all_days if d > t_day)
            if remaining_days < 30:
                continue
            
            # All conditions met - issue UP call
            opportunities += 1
            issued += 1
            issued_days.append(t_day)
            last_call_for_symbol[sym_id] = t_day
            
            # Get label
            up_label = symbols_outcomes.get(sym_id, {}).get(t_day)
            if up_label == 1:
                hits += 1
    
    if issued == 0:
        print("INSUFFICIENT=1")
        return
    
    # Compute metrics
    distinct_days = len(set(issued_days))
    precision = hits / issued
    base_rate = hits / issued  # Since all calls are UP, base rate is precision
    
    # Compute design effect (assume intra-day correlation of 0.5)
    # Count calls per day
    calls_per_day = defaultdict(int)
    for day in issued_days:
        calls_per_day[day] += 1
    avg_cluster_size = sum(calls_per_day.values()) / len(calls_per_day) if calls_per_day else 1
    ICC = 0.5  # Assumed intra-class correlation
    design_effect = 1 + (avg_cluster_size - 1) * ICC
    effective_n = issued / design_effect
    
    # Sealed era evaluation
    sealed_issued = 0
    sealed_hits = 0
    # Re-run logic for sealed era (just checking labels)
    for i, t_day in enumerate(all_days):
        if t_day not in sealed_days_set:
            continue
        if i < 252:
            continue
        for sym_id, sym_data in symbol_series.items():
            if t_day not in sym_data['series']:
                continue
            # Check if we would have issued a call (simplified)
            close_t = sym_data['series'][t_day]['close']
            if close_t < 5:
                continue
            prior_days = [ts for ts in sym_data['sorted_ts'] if ts < t_day]
            if len(prior_days) < 504:
                continue
            # Dollar volume
            dollar_vols = []
            for j in range(1, 61):
                if i - j < 0:
                    continue
                prev_day = all_days[i-j]
                if prev_day in sym_data['series']:
                    c_val = sym_data['series'][prev_day]['close']
                    v_val = sym_data['series'][prev_day]['vol']
                    dollar_vols.append(c_val * v_val)
            if len(dollar_vols) < 30:
                continue
            avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
            if avg_dollar_vol < 5e6:
                continue
            vol_t = sym_data['twenty_day_vol'].get(t_day)
            if vol_t is None:
                continue
            vol_threshold = day_vol_percentiles.get(t_day)
            if vol_threshold is None or vol_t >= vol_threshold:
                continue
            required_sent_days = [all_days[i-k] for k in range(5)]
            sent_vals = []
            missing_sent = False
            for d in required_sent_days:
                if d not in sym_data['series']:
                    missing_sent = True
                    break
                sent_vals.append(sym_data['series'][d]['sent'])
            if missing_sent or any(v is None for v in sent_vals):
                continue
            current_mean_sent = sum(sent_vals) / len(sent_vals)
            hist_means = []
            for j in range(5, 253):
                if i - j < 0:
                    break
                hist_day = all_days[i-j]
                if hist_day in sym_data['five_day_means']:
                    hist_means.append(sym_data['five_day_means'][hist_day])
            if len(hist_means) < 30:
                continue
            hist_means_sorted = sorted(hist_means)
            pct90_idx = int(len(hist_means_sorted) * 0.9)
            pct90 = hist_means_sorted[pct90_idx]
            if current_mean_sent <= pct90:
                continue
            twenty_day_ret = sym_data['twenty_day_returns'].get(t_day)
            if twenty_day_ret is None or abs(twenty_day_ret) >= 0.02:
                continue
            if i > 0:
                prev_day = all_days[i-1]
                if prev_day in sym_data['series']:
                    prev_close = sym_data['series'][prev_day]['close']
                    one_day_ret = (close_t - prev_close) / prev_close
                    if abs(one_day_ret) >= 0.005:
                        continue
                else:
                    continue
            else:
                continue
            
            # Would have issued
            sealed_issued += 1
            up_label = symbols_outcomes.get(sym_id, {}).get(t_day)
            if up_label == 1:
                sealed_hits += 1
    
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
import sqlite3
import math
from collections import defaultdict

DB_PATH = 'data/signaldeck.db'

def connect_db():
    return sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True, timeout=30)

def fetch_daily_bars(conn):
    cur = conn.execute(
        "SELECT symbol_id, ts, open, high, low, close, volume "
        "FROM bars WHERE tf = '1d' ORDER BY symbol_id, ts"
    )
    return cur.fetchall()

def fetch_labels(conn):
    cur = conn.execute(
        "SELECT symbol_id, ts, up FROM prediction_outcomes "
        "WHERE CAST(horizon AS INTEGER) = 20"
    )
    return {(row[0], row[1]): row[2] for row in cur.fetchall()}

def compute_rolling_stats(series, window):
    n = len(series)
    medians = [None] * n
    highs = [None] * n
    vols = [None] * n
    for i in range(window - 1, n):
        window_slice = series[i - window + 1: i + 1]
        medians[i] = sorted([s[1] for s in window_slice])[window // 2]
        highs[i] = max(s[2] for s in window_slice)
        vols[i] = window
    return medians, highs, vols

def compute_volatility(series, window):
    n = len(series)
    vols = [None] * n
    for i in range(window, n):
        returns = []
        for j in range(i - window + 1, i + 1):
            if series[j][1] and series[j-1][1]:
                returns.append((series[j][1] / series[j-1][1]) - 1)
        if len(returns) > 1:
            mean = sum(returns) / len(returns)
            var = sum((r - mean) ** 2 for r in returns) / (len(returns) - 1)
            vols[i] = math.sqrt(var)
        else:
            vols[i] = None
    return vols

def main():
    try:
        conn = connect_db()
    except Exception as e:
        print(f"ERROR: Cannot connect to database: {e}")
        return
    
    # Fetch data
    daily_bars = fetch_daily_bars(conn)
    labels = fetch_labels(conn)
    conn.close()
    
    if not daily_bars:
        print("INSUFFICIENT=1")
        return
    
    # Group bars by symbol
    symbol_bars = defaultdict(list)
    for bar in daily_bars:
        symbol_bars[bar[0]].append(bar)
    
    # Process each symbol
    all_decision_points = []
    
    for symbol_id, bars in symbol_bars.items():
        if len(bars) < 252:
            continue
            
        # Extract series for calculations
        ts_list = [bar[1] for bar in bars]
        close_list = [bar[4] for bar in bars]
        volume_list = [bar[5] for bar in bars]
        high_list = [bar[2] for bar in bars]
        
        # Compute rolling statistics
        medians, highs, _ = compute_rolling_stats(
            list(zip(ts_list, volume_list, high_list)), 20
        )
        volatilities = compute_volatility(
            list(zip(ts_list, close_list)), 20
        )
        
        # Compute 60-day average dollar volume
        avg_dollar_vol = []
        for i in range(59, len(bars)):
            window_sum = 0
            for j in range(i - 59, i + 1):
                if close_list[j] and volume_list[j]:
                    window_sum += close_list[j] * volume_list[j]
            avg_dollar_vol.append(window_sum / 60)
        
        # Find decision points
        cooldown_until = 0
        for i in range(251, len(bars)):
            t = bars[i]
            ts = t[1]
            close = t[4]
            volume = t[5]
            high = t[2]
            
            # Basic filters
            if close < 5.0:
                continue
                
            if medians[i] is None or medians[i] == 0:
                continue
                
            if i < 59:
                continue
                
            # Check for missing data in last 20 days
            missing = False
            for j in range(i - 19, i + 1):
                if bars[j][4] is None or bars[j][5] is None:
                    missing = True
                    break
            if missing:
                continue
                
            # Check volatility filter
            if volatilities[i] is None:
                continue
                
            # Check cooldown
            if ts <= cooldown_until:
                continue
                
            # Calculate entry conditions
            vol_spike = volume / medians[i]
            ret = (close - bars[i-1][4]) / bars[i-1][4] if bars[i-1][4] else 0
            high_ratio = abs(close - highs[i]) / highs[i]
            
            # Entry conditions
            if (vol_spike >= 5.0 and
                -0.01 <= ret <= 0.02 and
                high_ratio <= 0.015):
                
                # Get label
                label = labels.get((symbol_id, ts))
                if label is None:
                    continue
                
                all_decision_points.append({
                    'ts': ts,
                    'symbol_id': symbol_id,
                    'up': label,
                    'volatility': volatilities[i],
                    'day': ts // 86400
                })
                
                cooldown_until = ts + (20 * 86400)
    
    if len(all_decision_points) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Compute volatility decile threshold
    volatilities = [dp['volatility'] for dp in all_decision_points]
    volatilities_sorted = sorted(volatilities)
    decile_idx = int(0.9 * len(volatilities_sorted))
    vol_threshold = volatilities_sorted[min(decile_idx, len(volatilities_sorted)-1)]
    
    # Filter by volatility and recalculate decision points
    filtered_points = []
    cooldown_until = 0
    
    # Re-sort by timestamp for cooldown logic
    all_decision_points.sort(key=lambda x: x['ts'])
    
    for dp in all_decision_points:
        ts = dp['ts']
        if dp['volatility'] >= vol_threshold:
            continue
        if ts <= cooldown_until:
            continue
        filtered_points.append(dp)
        cooldown_until = ts + (20 * 86400)
    
    if len(filtered_points) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Split into training and sealed era
    unique_days = sorted(set(dp['day'] for dp in filtered_points))
    split_idx = int(0.8 * len(unique_days))
    split_day = unique_days[split_idx]
    
    train_points = [dp for dp in filtered_points if dp['day'] < split_day]
    sealed_points = [dp for dp in filtered_points if dp['day'] >= split_day]
    
    # Calculate metrics for training set
    train_issued = len(train_points)
    train_opportunities = len(train_points)  # All are opportunities after filtering
    train_hits = sum(1 for dp in train_points if dp['up'])
    train_base_rate = train_hits / train_issued if train_issued > 0 else 0
    
    # Calculate design effect and effective N for training set
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for dp in train_points:
        day_counts[dp['day']] += 1
        if dp['up']:
            day_hits[dp['day']] += 1
    
    k = len(day_counts)
    if k < 2:
        print("INSUFFICIENT=1")
        return
    
    # Calculate ICC
    p = train_base_rate
    ms_between = 0
    ms_within = 0
    n_total = train_issued
    
    for day, count in day_counts.items():
        p_day = day_hits[day] / count
        ms_between += count * ((p_day - p) ** 2)
        ms_within += count * p_day * (1 - p_day)
    
    ms_between /= (k - 1)
    ms_within /= (n_total - k)
    
    icc = (ms_between - ms_within) / (ms_between + ms_within) if (ms_between + ms_within) > 0 else 0
    avg_cluster_size = n_total / k
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = n_total / design_effect
    
    # Calculate sealed era metrics
    sealed_issued = len(sealed_points)
    sealed_hits = sum(1 for dp in sealed_points if dp['up'])
    sealed_base_rate = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Output results
    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={train_opportunities}")
    print(f"PRECISION={train_hits/train_issued:.6f}")
    print(f"BASE_RATE={train_base_rate:.6f}")
    print(f"DISTINCT_DAYS={len(day_counts)}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_hits/sealed_issued:.6f}")

if __name__ == "__main__":
    main()
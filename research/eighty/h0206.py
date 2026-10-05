import sqlite3
import math

DB_PATH = 'data/signaldeck.db'
HORIZON = 20
MIN_HISTORY = 252
MIN_OBS = 30
MIN_PRICE = 5.0
MAX_TRAIL_GAIN = 0.30
VOL_PERCENTILE_TOP = 0.1
MAX_REENTRY_DAYS = 20
LOOKBACK_SENTIMENT = 20
LOOKBACK_VOLUME = 20
LOOKBACK_SMA = 50
DAY_SECONDS = 86400

def main():
    conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True, timeout=30)
    cur = conn.cursor()
    
    # Get all symbols with daily bars and sentiment data
    cur.execute("""
        SELECT DISTINCT s.id
        FROM symbols s
        WHERE EXISTS (SELECT 1 FROM bars b WHERE b.symbol_id = s.id AND b.tf = '1d')
          AND EXISTS (SELECT 1 FROM sentiment_features sf WHERE sf.symbol_id = s.id)
    """)
    symbol_ids = [row[0] for row in cur.fetchall()]
    
    if not symbol_ids:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Prepare placeholders for IN clause
    placeholders = ','.join('?' * len(symbol_ids))
    
    # Get all candidate (symbol, day) pairs with basic requirements
    cur.execute(f"""
        SELECT b.symbol_id, b.ts, b.close, b.volume
        FROM bars b
        WHERE b.tf = '1d'
          AND b.symbol_id IN ({placeholders})
          AND b.close >= {MIN_PRICE}
        ORDER BY b.symbol_id, b.ts
    """, symbol_ids)
    all_bars = cur.fetchall()
    
    if not all_bars:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Build index by symbol and time
    bars_by_symbol = {}
    for sym, ts, close, vol in all_bars:
        if sym not in bars_by_symbol:
            bars_by_symbol[sym] = []
        bars_by_symbol[sym].append((ts, close, vol))
    
    # Get sentiment data
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
    """, symbol_ids)
    sentiment_data = {}
    for sym, day_str, score in cur.fetchall():
        # Convert day string to timestamp (noon UTC)
        day_ts = int(sqlite3.connect(':memory:').execute(
            "SELECT strftime('%s', ? || 'T12:00:00Z')", (day_str,)).fetchone()[0])
        if sym not in sentiment_data:
            sentiment_data[sym] = {}
        sentiment_data[sym][day_ts] = score
    
    # Get labels from prediction_outcomes (horizon=20)
    cur.execute(f"""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE symbol_id IN ({placeholders})
          AND horizon = {HORIZON}
    """, symbol_ids)
    labels = {}
    for sym, ts, up in cur.fetchall():
        if sym not in labels:
            labels[sym] = {}
        labels[sym][ts] = up
    
    conn.close()
    
    # Process each symbol
    opportunities = []
    for sym in symbol_ids:
        if sym not in bars_by_symbol:
            continue
        bar_list = bars_by_symbol[sym]
        if len(bar_list) < MIN_HISTORY + LOOKBACK_SENTIMENT:
            continue
            
        for i in range(MIN_HISTORY, len(bar_list)):
            ts, close, vol = bar_list[i]
            
            # Check sentiment for T and T-20..T-1
            t_minus_20_ts = ts - LOOKBACK_SENTIMENT * DAY_SECONDS
            has_sentiment = True
            for j in range(i - LOOKBACK_SENTIMENT, i + 1):
                if j < 0:
                    has_sentiment = False
                    break
                bar_ts = bar_list[j][0]
                if sym not in sentiment_data or bar_ts not in sentiment_data[sym]:
                    has_sentiment = False
                    break
            if not has_sentiment:
                continue
                
            # Check prices/volumes for T-20..T
            has_history = True
            for j in range(i - LOOKBACK_SENTIMENT, i + 1):
                if j < 0:
                    has_history = False
                    break
            if not has_history:
                continue
                
            # Get 20-day median volume (approximate as mean for efficiency)
            vol_window = [bar_list[j][2] for j in range(i - LOOKBACK_VOLUME + 1, i + 1)]
            vol_median = sum(vol_window) / len(vol_window)
            
            # Check T's volume below 20-session median
            if vol >= vol_median:
                continue
                
            # Compute T's close-to-close return
            prev_close = bar_list[i - 1][1] if i > 0 else close
            daily_return = (close - prev_close) / prev_close if prev_close > 0 else 0
            
            # Check return between -1% and +3%
            if not (-0.01 <= daily_return <= 0.03):
                continue
                
            # Check 50-session SMA
            sma_window = [bar_list[j][1] for j in range(i - LOOKBACK_SMA + 1, i + 1)]
            sma_50 = sum(sma_window) / len(sma_window)
            if close <= sma_50:
                continue
                
            # Check trailing 20-session gain
            gain_window = [bar_list[j][1] for j in range(i - LOOKBACK_VOLUME, i + 1)]
            if len(gain_window) < 2:
                continue
            trail_gain = (gain_window[-1] - gain_window[0]) / gain_window[0]
            if trail_gain > MAX_TRAIL_GAIN:
                continue
                
            # Compute 20-day realized volatility (std dev of returns)
            returns = []
            for j in range(i - LOOKBACK_VOLUME + 1, i + 1):
                if j > 0:
                    r = (bar_list[j][1] - bar_list[j-1][1]) / bar_list[j-1][1]
                    returns.append(r)
            if len(returns) < 2:
                continue
            mean_r = sum(returns) / len(returns)
            var_r = sum((r - mean_r) ** 2 for r in returns) / (len(returns) - 1)
            vol_realized = math.sqrt(var_r)
            
            # Check top decile (approximate using percentile)
            opportunities.append((sym, ts, close, vol, trail_gain, vol_realized, daily_return))
    
    if len(opportunities) < MIN_OBS:
        print("INSUFFICIENT=1")
        return
    
    # Sort by time
    opportunities.sort(key=lambda x: x[1])
    
    # Compute cross-sectional percentiles for volatility
    all_vols = [x[5] for x in opportunities]
    all_vols_sorted = sorted(all_vols)
    top_10_idx = int(len(all_vols_sorted) * (1 - VOL_PERCENTILE_TOP))
    vol_threshold = all_vols_sorted[top_10_idx] if top_10_idx < len(all_vols_sorted) else float('inf')
    
    # Filter for final signals
    issued_calls = []
    last_call_time = {}
    
    for sym, ts, close, vol, trail_gain, vol_realized, daily_return in opportunities:
        # Skip if volatility in top decile
        if vol_realized >= vol_threshold:
            continue
            
        # Check re-entry condition
        if sym in last_call_time and ts - last_call_time[sym] < MAX_REENTRY_DAYS * DAY_SECONDS:
            continue
            
        # Issue UP call
        issued_calls.append((sym, ts))
        last_call_time[sym] = ts
    
    # Check we have enough independent observations
    if len(issued_calls) < MIN_OBS:
        print("INSUFFICIENT=1")
        return
    
    # Split into training and sealed eras (most recent 20%)
    n_sealed = int(len(issued_calls) * 0.2)
    sealed_calls = set(issued_calls[-n_sealed:]) if n_sealed > 0 else set()
    training_calls = set(issued_calls[:-n_sealed]) if n_sealed > 0 else set(issued_calls)
    
    # Get labels for issued calls
    hits = 0
    sealed_hits = 0
    up_count = 0
    sealed_up_count = 0
    distinct_days = set()
    
    for sym, ts in issued_calls:
        distinct_days.add(ts // DAY_SECONDS)
        
        # Check if we have label
        if sym in labels and ts in labels[sym]:
            if labels[sym][ts] == 1:
                hits += 1
                up_count += 1
            else:
                up_count += 1
            if (sym, ts) in sealed_calls and labels[sym][ts] == 1:
                sealed_hits += 1
        else:
            # If no label, skip this call (treated as abstention)
            continue
    
    # Calculate metrics
    issued = len(issued_calls)
    opportunities_total = len(opportunities)
    precision = hits / issued if issued > 0 else 0
    base_rate = up_count / issued if issued > 0 else 0
    
    # Calculate design effect (using day clustering)
    if issued > 0:
        day_counts = {}
        for _, ts in issued_calls:
            day = ts // DAY_SECONDS
            day_counts[day] = day_counts.get(day, 0) + 1
        
        mean_day_count = issued / len(day_counts) if day_counts else 1
        variance_day_count = sum((c - mean_day_count) ** 2 for c in day_counts.values()) / len(day_counts) if day_counts else 0
        design_effect = 1 + variance_day_count / (mean_day_count ** 2) if mean_day_count > 0 else 1
        effective_n = issued / design_effect
    else:
        effective_n = 0
    
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    # Output results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities_total}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={len(distinct_days)}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
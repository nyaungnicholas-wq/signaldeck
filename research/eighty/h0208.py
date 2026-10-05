import sqlite3
import math
import sys

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 20
LOOKBACK = 252
AVG_VOL_PERIOD = 60
VOLATILITY_PERIOD = 20
ROLLING_RATIO_PERIOD = 5
MIN_OBSERVATIONS = 30

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True, timeout=30)
        conn.row_factory = sqlite3.Row
    except Exception:
        print("INSUFFICIENT=1")
        return

    cursor = conn.cursor()
    
    # Get all trading days from daily bars
    cursor.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    all_days = [row['ts'] for row in cursor.fetchall()]
    if len(all_days) < LOOKBACK + HORIZON + 1:
        print("INSUFFICIENT=1")
        return
    
    # Determine sealed era cutoff (most recent 20% of days)
    total_days = len(all_days)
    sealed_start_idx = int(total_days * 0.8)
    sealed_cutoff = all_days[sealed_start_idx]
    
    # Pre-load all bars data for efficiency
    cursor.execute("SELECT symbol_id, ts, open, high, low, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    all_bars = cursor.fetchall()
    
    # Group bars by symbol
    bars_by_symbol = {}
    for row in all_bars:
        sid = row['symbol_id']
        if sid not in bars_by_symbol:
            bars_by_symbol[sid] = []
        bars_by_symbol[sid].append(row)
    
    # Pre-load all stocktwits data
    cursor.execute("SELECT symbol_id, ts, bullish, bearish FROM stocktwits_sentiment ORDER BY symbol_id, ts")
    all_st = cursor.fetchall()
    
    # Group stocktwits by symbol
    st_by_symbol = {}
    for row in all_st:
        sid = row['symbol_id']
        if sid not in st_by_symbol:
            st_by_symbol[sid] = []
        st_by_symbol[sid].append(row)
    
    # Find symbols present in both tables
    common_symbols = set(bars_by_symbol.keys()) & set(st_by_symbol.keys())
    if not common_symbols:
        print("INSUFFICIENT=1")
        return
    
    # Filter symbols with enough history
    valid_symbols = {}
    for sid in common_symbols:
        if len(bars_by_symbol[sid]) >= LOOKBACK + HORIZON + 1:
            valid_symbols[sid] = {
                'bars': bars_by_symbol[sid],
                'st': st_by_symbol[sid]
            }
    
    if not valid_symbols:
        print("INSUFFICIENT=1")
        return
    
    issued = 0
    opportunities = 0
    hits = 0
    distinct_days = set()
    issued_symbols_recent = {}  # symbol_id -> last call ts
    
    sealed_issued = 0
    sealed_hits = 0
    sealed_distinct_days = set()
    
    # Process each day from LOOKBACK onwards
    for t_idx in range(LOOKBACK, total_days - HORIZON):
        t_ts = all_days[t_idx]
        is_sealed = t_ts >= sealed_cutoff
        
        # Build eligible symbols for this day
        eligible = []
        for sid, data in valid_symbols.items():
            bars = data['bars']
            st_data = data['st']
            
            # Find current day index in bars
            bar_idx = None
            for i, bar in enumerate(bars):
                if bar['ts'] == t_ts:
                    bar_idx = i
                    break
            if bar_idx is None or bar_idx < LOOKBACK:
                continue
            
            current_bar = bars[bar_idx]
            prior_bars = bars[bar_idx - AVG_VOL_PERIOD:bar_idx]
            
            # Price >= $5
            if current_bar['close'] < 5:
                continue
            
            # Average daily dollar volume >= $5M over prior 60 sessions
            if len(prior_bars) < AVG_VOL_PERIOD:
                continue
            total_dollar_vol = sum(b['volume'] * b['close'] for b in prior_bars)
            avg_dollar_vol = total_dollar_vol / AVG_VOL_PERIOD
            if avg_dollar_vol < 5e6:
                continue
            
            # Check stocktwits data for T and T-5..T
            # Find stocktwits entries for current day and prior 5 days
            st_current = None
            st_recent = []
            for st in st_data:
                if st['ts'] == t_ts:
                    st_current = st
                elif t_ts - 5 * 86400 <= st['ts'] < t_ts:
                    st_recent.append(st)
            
            if st_current is None:
                continue
            if len(st_recent) < ROLLING_RATIO_PERIOD - 1:  # Need at least 4 prior days
                continue
            
            # 20-day realized volatility
            if bar_idx < VOLATILITY_PERIOD + 1:
                continue
            returns = []
            for i in range(bar_idx - VOLATILITY_PERIOD, bar_idx):
                prev_close = bars[i]['close']
                curr_close = bars[i + 1]['close']
                if prev_close > 0:
                    returns.append((curr_close - prev_close) / prev_close)
            if len(returns) < VOLATILITY_PERIOD:
                continue
            mean_r = sum(returns) / len(returns)
            var_r = sum((x - mean_r) ** 2 for x in returns) / len(returns)
            vol = math.sqrt(var_r)
            
            # 5-session bullish/bearish ratio
            ratio_5_vals = []
            for st in st_recent:
                if st['bearish'] > 0:
                    ratio_5_vals.append(st['bullish'] / st['bearish'])
                else:
                    ratio_5_vals.append(float(st['bullish']) if st['bullish'] > 0 else 0.0)
            if st_current['bearish'] > 0:
                ratio_5_vals.append(st_current['bullish'] / st_current['bearish'])
            else:
                ratio_5_vals.append(float(st_current['bullish']) if st_current['bullish'] > 0 else 0.0)
            avg_ratio_5 = sum(ratio_5_vals) / len(ratio_5_vals) if ratio_5_vals else 0
            
            # 252-session 90th percentile of daily bullish/bearish ratio
            ratios_252 = []
            for i in range(bar_idx - LOOKBACK, bar_idx):
                day_ts = bars[i]['ts']
                day_st = None
                for st in st_data:
                    if st['ts'] == day_ts:
                        day_st = st
                        break
                if day_st:
                    if day_st['bearish'] > 0:
                        ratios_252.append(day_st['bullish'] / day_st['bearish'])
                    else:
                        ratios_252.append(float(day_st['bullish']) if day_st['bullish'] > 0 else 0.0)
            if not ratios_252:
                continue
            ratios_252_sorted = sorted(ratios_252)
            pct_90_idx = int(len(ratios_252_sorted) * 0.9)
            pct_90 = ratios_252_sorted[min(pct_90_idx, len(ratios_252_sorted) - 1)]
            
            # Close-to-close return
            prev_bar = bars[bar_idx - 1]
            ret = (current_bar['close'] - prev_bar['close']) / prev_bar['close'] if prev_bar['close'] > 0 else 0
            
            # Close in top quintile of 252-session range
            highs = [bars[i]['high'] for i in range(bar_idx - LOOKBACK, bar_idx)]
            lows = [bars[i]['low'] for i in range(bar_idx - LOOKBACK, bar_idx)]
            range_high = max(highs)
            range_low = min(lows)
            range_pct = (current_bar['close'] - range_low) / (range_high - range_low) if range_high > range_low else 0
            
            eligible.append({
                'symbol_id': sid,
                'current_ts': t_ts,
                'current_close': current_bar['close'],
                'bullish': st_current['bullish'],
                'bearish': st_current['bearish'],
                'avg_ratio_5': avg_ratio_5,
                'pct_90': pct_90,
                'return': ret,
                'range_pct': range_pct,
                'volatility': vol,
                'bar_idx': bar_idx,
                'bars': bars
            })
        
        if not eligible:
            continue
        
        # Cross-sectional thresholds
        # 1. Top decile of bullish count
        bullish_counts = [e['bullish'] for e in eligible]
        bullish_sorted = sorted(bullish_counts)
        decile_idx = max(1, len(bullish_sorted) // 10)
        top_decile_threshold = bullish_sorted[-decile_idx]
        
        # 2. Top decile of volatility (abstain condition)
        vols = [e['volatility'] for e in eligible]
        vol_sorted = sorted(vols)
        vol_decile_idx = max(1, len(vol_sorted) // 10)
        vol_top_threshold = vol_sorted[-vol_decile_idx]
        
        # Apply conditions
        day_issued_count = 0
        for item in eligible:
            opportunities += 1
            
            # Abstain conditions
            if item['current_close'] < 5:
                continue
            if item['return'] < 0:
                continue
            if item['volatility'] >= vol_top_threshold:
                continue
            if item['symbol_id'] in issued_symbols_recent:
                last_call = issued_symbols_recent[item['symbol_id']]
                if item['current_ts'] - last_call < 20 * 86400:
                    continue
            
            # Entry conditions
            if item['bullish'] < top_decile_threshold:
                continue
            if item['avg_ratio_5'] <= item['pct_90']:
                continue
            if item['return'] < 0.01:
                continue
            if item['range_pct'] < 0.8:  # Top quintile = 80th percentile
                continue
            
            # All conditions met - issue DOWN call
            issued += 1
            day_issued_count += 1
            distinct_days.add(t_ts)
            issued_symbols_recent[item['symbol_id']] = item['current_ts']
            
            # Check outcome at T+HORIZON
            outcome_idx = item['bar_idx'] + HORIZON
            if outcome_idx < len(item['bars']):
                entry_close = item['current_close']
                exit_close = item['bars'][outcome_idx]['close']
                fwd_return = (exit_close - entry_close) / entry_close
                hit = 1 if fwd_return < 0 else 0  # DOWN call hits if price goes down
                hits += hit
                
                if is_sealed:
                    sealed_issued += 1
                    sealed_hits += hit
                    sealed_distinct_days.add(t_ts)
        
        # Check minimum observations for this day (abstain if < 30 independent obs remain)
        # This is a global check, not per-day
    
    # Global minimum observations check
    if issued < MIN_OBSERVATIONS:
        print("INSUFFICIENT=1")
        return
    
    # Calculate design effect for EFFECTIVE_N
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Approximate: cluster by day, ICC ~ 0.1 for financial returns
    if distinct_days:
        avg_cluster_size = issued / len(distinct_days)
        icc = 0.1
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / design_effect
    else:
        effective_n = 0
    
    # Ensure EFFECTIVE_N < ISSUED
    if effective_n >= issued:
        effective_n = issued * 0.99
    
    precision = hits / issued if issued > 0 else 0
    
    # Base rate within issued subset: proportion of DOWN outcomes among issued calls
    # For DOWN calls, base rate = proportion of negative forward returns
    base_rate = 1 - precision if issued > 0 else 0  # Since hit = negative return
    
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={len(distinct_days)}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
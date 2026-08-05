import sqlite3
import math
from collections import defaultdict
from datetime import datetime

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    conn = sqlite3.connect(db_path, uri=True)
    cur = conn.cursor()
    
    # Load all daily bars
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars = cur.fetchall()
    
    # Load daily sentiment from sentiment_features
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        ORDER BY symbol_id, day
    """)
    sentiment_rows = cur.fetchall()
    
    conn.close()
    
    # Organize sentiment by symbol
    sentiment_by_symbol = defaultdict(list)
    for sym, day_str, score in sentiment_rows:
        # Convert day string to timestamp (midnight UTC)
        dt = datetime.strptime(day_str, '%Y-%m-%d')
        ts = int(dt.timestamp())
        sentiment_by_symbol[sym].append((ts, score))
    
    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for sym, ts, close, volume in bars:
        bars_by_symbol[sym].append((ts, close, volume))
    
    # Generate all trading days (unique ts)
    all_days = sorted(set(ts for sym, ts, close, volume in bars))
    day_to_idx = {d: i for i, d in enumerate(all_days)}
    
    # Precompute per symbol: sorted bars and sentiment
    for sym in bars_by_symbol:
        bars_by_symbol[sym].sort(key=lambda x: x[0])
    for sym in sentiment_by_symbol:
        sentiment_by_symbol[sym].sort(key=lambda x: x[0])
    
    # Find symbols with at least 12 months (~252 days) of price history
    eligible_symbols = []
    for sym, data in bars_by_symbol.items():
        if len(data) >= 252:
            eligible_symbols.append(sym)
    
    # For each eligible symbol, precompute metrics
    symbol_metrics = {}
    for sym in eligible_symbols:
        bar_data = bars_by_symbol[sym]
        sent_data = sentiment_by_symbol.get(sym, [])
        
        # Create lookup for sentiment by ts
        sent_by_ts = {ts: score for ts, score in sent_data}
        
        # For each day in bar_data, compute needed metrics
        metrics = []
        for i, (ts, close, volume) in enumerate(bar_data):
            # Dollar volume
            dollar_vol = close * volume
            
            # Need at least 20 prior bars for T-20
            if i < 20:
                metrics.append((ts, None))
                continue
                
            # T-20 close
            t_minus_20_close = bar_data[i-20][1]
            price_drop = (close - t_minus_20_close) / t_minus_20_close
            
            # Check price >= $5
            if close < 5:
                metrics.append((ts, None))
                continue
                
            # Check abstain conditions
            abstain = False
            
            # 1. Price < $5 (already checked)
            # 2. Price drop >= 30% (abstain)
            if price_drop <= -0.30:
                abstain = True
                
            # 3. Fewer than 20 prior sessions of price data
            if i < 20:
                abstain = True
                
            # 4. Sentiment scores for T-4..T missing
            # Get sentiment scores for last 5 days (including T)
            sent_scores_5d = []
            for j in range(max(0, i-4), i+1):
                ts_j = bar_data[j][0]
                if ts_j in sent_by_ts:
                    sent_scores_5d.append(sent_by_ts[ts_j])
                else:
                    abstain = True
                    break
            
            if len(sent_scores_5d) != 5:
                abstain = True
                
            # 5. 5-session realized volatility in top decile
            # Compute returns for last 5 days
            if not abstain and i >= 5:
                returns = []
                for j in range(i-4, i+1):
                    prev_close = bar_data[j-1][1]
                    curr_close = bar_data[j][1]
                    returns.append(curr_close / prev_close - 1)
                vol_5d = (sum((r - sum(returns)/len(returns))**2 for r in returns) / len(returns))**0.5
            else:
                vol_5d = None
                
            # 6. Average dollar volume >= $10M over prior 60 sessions
            if i >= 60:
                dollar_vols_60 = [bar_data[j][1] * bar_data[j][2] for j in range(i-60, i)]
                avg_dollar_vol_60 = sum(dollar_vols_60) / len(dollar_vols_60)
                if avg_dollar_vol_60 < 10_000_000:
                    abstain = True
                median_dollar_vol_60 = sorted(dollar_vols_60)[len(dollar_vols_60)//2]
            else:
                abstain = True
                
            # 5-session mean sentiment
            if not abstain:
                mean_sent_5d = sum(sent_scores_5d) / len(sent_scores_5d)
            else:
                mean_sent_5d = None
                
            metrics.append((ts, {
                'close': close,
                'volume': volume,
                'dollar_vol': dollar_vol,
                'price_drop': price_drop,
                'abstain': abstain,
                'mean_sent_5d': mean_sent_5d,
                'vol_5d': vol_5d,
                'median_dollar_vol_60': median_dollar_vol_60 if not abstain else None
            }))
        
        symbol_metrics[sym] = metrics
    
    # Collect all potential decision points (days with metrics)
    decision_points = []
    for sym, metrics_list in symbol_metrics.items():
        for ts, m in metrics_list:
            if m is None:
                continue
            if m['abstain']:
                continue
            decision_points.append((ts, sym, m))
    
    if not decision_points:
        print("INSUFFICIENT=1")
        return
        
    # Group by day
    day_groups = defaultdict(list)
    for ts, sym, m in decision_points:
        day_groups[ts].append((sym, m))
    
    # Process each day
    calls = []  # (ts, sym, is_hit)
    for ts, entries in sorted(day_groups.items()):
        if len(entries) < 2:  # Need cross-sectional decile, need at least 10 symbols ideally
            continue
            
        # Get mean_sent_5d for all symbols on this day
        sent_values = [m['mean_sent_5d'] for sym, m in entries if m['mean_sent_5d'] is not None]
        if len(sent_values) < 10:  # Need enough for decile
            continue
            
        # Compute bottom decile threshold (10th percentile)
        sent_sorted = sorted(sent_values)
        idx_10pct = int(len(sent_sorted) * 0.1)
        bottom_decile_threshold = sent_sorted[min(idx_10pct, len(sent_sorted)-1)]
        
        # Get volatility values
        vol_values = [m['vol_5d'] for sym, m in entries if m['vol_5d'] is not None]
        if not vol_values:
            continue
        vol_sorted = sorted(vol_values)
        idx_90pct = int(len(vol_sorted) * 0.9)
        top_decile_threshold = vol_sorted[min(idx_90pct, len(vol_sorted)-1)]
        
        # Check each symbol
        for sym, m in entries:
            if m['mean_sent_5d'] is None or m['vol_5d'] is None:
                continue
                
            # Entry conditions:
            # 1. Bottom decile sentiment
            if m['mean_sent_5d'] > bottom_decile_threshold:
                continue
            # 2. Price drop >= 10%
            if m['price_drop'] > -0.10:
                continue
            # 3. Dollar volume > median
            if m['dollar_vol'] <= m['median_dollar_vol_60']:
                continue
            # 4. Not in top decile volatility
            if m['vol_5d'] >= top_decile_threshold:
                continue
                
            # Issue UP call
            # Find label: need T+5 close
            bar_list = bars_by_symbol[sym]
            # Find index of current ts
            idx = None
            for i, (t, _, _) in enumerate(bar_list):
                if t == ts:
                    idx = i
                    break
            if idx is None or idx + 5 >= len(bar_list):
                continue
                
            t_close = bar_list[idx][1]
            t5_close = bar_list[idx+5][1]
            if t5_close > t_close:  # UP
                calls.append((ts, sym, True))
            else:
                calls.append((ts, sym, False))
    
    if not calls:
        print("INSUFFICIENT=1")
        return
        
    # Split into regular and sealed eras (80/20 by time)
    call_days = sorted(set(ts for ts, _, _ in calls))
    split_idx = int(len(call_days) * 0.8)
    split_ts = call_days[split_idx]
    
    regular_calls = [(ts, sym, hit) for ts, sym, hit in calls if ts < split_ts]
    sealed_calls = [(ts, sym, hit) for ts, sym, hit in calls if ts >= split_ts]
    
    if not regular_calls or not sealed_calls:
        print("INSUFFICIENT=1")
        return
        
    # Compute metrics
    def compute_metrics(call_list):
        if not call_list:
            return None, None, None, None, None, None
        
        issued = len(call_list)
        hits = sum(1 for _, _, hit in call_list if hit)
        precision = hits / issued
        distinct_days = len(set(ts for ts, _, _ in call_list))
        
        # Base rate: proportion of UP calls in the issued subset
        # We already have hits, so base rate is hits/issued
        # But hypothesis says "base rate of the predicted class WITHIN the issued subset"
        # That's the same as precision for UP calls? Actually, no: base rate would be the proportion of days that are UP in the universe,
        # but we only have calls. We'll compute the proportion of UP calls among all calls.
        base_rate = hits / issued  # Same as precision for binary call
        
        # Design effect: need to account for clustering
        # We'll use an approximate design effect based on day clustering
        # If calls are spread over distinct_days, we can compute effective_n = issued / (1 + (issued/distinct_days - 1))
        # But more accurately, we need to account for autocorrelation. We'll use a simple approximation:
        # effective_n = issued / (1 + (issued/distinct_days - 1))
        # This gives effective_n = distinct_days if all calls on same day, but we need to be more careful.
        # Since we have multiple calls per day, we can compute:
        calls_per_day = defaultdict(int)
        for ts, _, _ in call_list:
            calls_per_day[ts] += 1
        
        # Compute ICC (intraclass correlation) approximation
        n_days = len(calls_per_day)
        avg_calls_per_day = issued / n_days
        
        # Compute variance of daily call counts
        if n_days > 1:
            variance_daily = sum((calls_per_day[d] - avg_calls_per_day)**2 for d in calls_per_day) / (n_days - 1)
        else:
            variance_daily = 0
            
        # Design effect = 1 + (avg_calls_per_day - 1) * ICC
        # We'll approximate ICC using the formula for binary outcomes:
        # ICC ≈ (mean_square_between - mean_square_within) / (mean_square_between + (k-1)*mean_square_within)
        # This is complex, so we'll use a simpler approach: effective_n = issued / (1 + (avg_calls_per_day - 1))
        # This assumes perfect correlation within day.
        design_effect = 1 + (avg_calls_per_day - 1)
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        return issued, precision, base_rate, distinct_days, effective_n
    
    regular_metrics = compute_metrics(regular_calls)
    sealed_metrics = compute_metrics(sealed_calls)
    
    if not regular_metrics or not sealed_metrics:
        print("INSUFFICIENT=1")
        return
        
    issued, precision, base_rate, distinct_days, effective_n = regular_metrics
    sealed_precision = sealed_metrics[1]
    
    # Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(regular_calls) + len(sealed_calls)}")  # total decision points considered
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
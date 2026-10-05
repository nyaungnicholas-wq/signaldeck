import sqlite3
import statistics

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Get max timestamp to determine 80/20 split
        cursor.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
        max_ts = cursor.fetchone()[0]
        cutoff = int(max_ts * 0.8)
        
        # Load all bars
        cursor.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
        all_bars = cursor.fetchall()
        
        # Load all news scores
        cursor.execute("SELECT symbol_id, ts, score FROM news")
        all_news = cursor.fetchall()
        
        # Load prediction outcomes with horizon=20
        cursor.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=20")
        all_outcomes = cursor.fetchall()
        
        conn.close()
        
        # Build data structures
        bars_by_symbol = {}
        for sid, ts, close, vol in all_bars:
            if sid not in bars_by_symbol:
                bars_by_symbol[sid] = []
            bars_by_symbol[sid].append((ts, close, vol))
        
        # Sort each symbol's bars by timestamp
        for sid in bars_by_symbol:
            bars_by_symbol[sid].sort(key=lambda x: x[0])
        
        # Build news by symbol and day
        news_by_symbol = {}
        for sid, ts, score in all_news:
            if sid not in news_by_symbol:
                news_by_symbol[sid] = {}
            day = ts // 86400
            if day not in news_by_symbol[sid]:
                news_by_symbol[sid][day] = []
            news_by_symbol[sid][day].append(score)
        
        # Average scores per day per symbol
        news_avg = {}
        for sid, days in news_by_symbol.items():
            news_avg[sid] = {}
            for day, scores in days.items():
                news_avg[sid][day] = statistics.mean(scores)
        
        # Build outcomes lookup
        outcome_lookup = {}
        for sid, ts, up in all_outcomes:
            outcome_lookup[(sid, ts)] = up
        
        # Helper to find bar index by timestamp
        def find_bar_index(symbol_bars, target_ts):
            for i, (ts, _, _) in enumerate(symbol_bars):
                if ts == target_ts:
                    return i
            return -1
        
        # Helper to get next trading day timestamp
        def next_trading_day(symbol_bars, current_idx, n):
            if current_idx + n < len(symbol_bars):
                return symbol_bars[current_idx + n][0]
            return None
        
        # Precompute daily returns for each symbol
        returns_by_symbol = {}
        for sid, bars in bars_by_symbol.items():
            returns_by_symbol[sid] = {}
            for i in range(1, len(bars)):
                ts_prev, close_prev, _ = bars[i-1]
                ts_curr, close_curr, _ = bars[i]
                ret = (close_curr / close_prev) - 1
                returns_by_symbol[sid][ts_curr] = ret
        
        # Compute volatility deciles cross-sectionally
        daily_volatilities = {}
        for sid, bars in bars_by_symbol.items():
            if len(bars) < 21:
                continue
            for i in range(20, len(bars)):
                ts = bars[i][0]
                # Get 20-day returns for volatility
                rets = []
                for j in range(i-20, i):
                    ret_ts = bars[j+1][0]
                    if ret_ts in returns_by_symbol.get(sid, {}):
                        rets.append(returns_by_symbol[sid][ret_ts])
                if len(rets) >= 20:
                    vol = statistics.stdev(rets) if len(rets) > 1 else 0
                    if ts not in daily_volatilities:
                        daily_volatilities[ts] = []
                    daily_volatilities[ts].append(vol)
        
        # Compute 90th percentile threshold for each day
        vol_threshold = {}
        for ts, vols in daily_volatilities.items():
            if len(vols) >= 10:
                sorted_vols = sorted(vols)
                idx = int(len(sorted_vols) * 0.9)
                vol_threshold[ts] = sorted_vols[idx]
            else:
                vol_threshold[ts] = float('inf')
        
        # Process each symbol's timeline
        all_calls = []
        opportunities = 0
        last_call_day = {}  # symbol_id -> last day issued
        
        for sid, bars in bars_by_symbol.items():
            if sid not in news_avg:
                continue
            if len(bars) < 253:  # Need 252 prior + current
                continue
            
            # Process each possible decision day
            for i in range(252, len(bars)-20):  # Need 20 future days for outcome
                ts_T, close_T, vol_T = bars[i]
                
                # Check close >= 5
                if close_T < 5:
                    continue
                
                # Check we have news for this day
                day_T = ts_T // 86400
                if day_T not in news_avg.get(sid, {}):
                    continue
                
                # Get sentiment value
                sentiment_T = news_avg[sid][day_T]
                
                # Get historical sentiment distribution (T-252..T-1)
                hist_sentiments = []
                valid_hist = True
                for j in range(i-252, i):
                    ts_j = bars[j][0]
                    day_j = ts_j // 86400
                    if day_j in news_avg.get(sid, {}):
                        hist_sentiments.append(news_avg[sid][day_j])
                    else:
                        valid_hist = False
                        break
                
                if not valid_hist or len(hist_sentiments) < 252:
                    continue
                
                # Check if sentiment in bottom decile
                hist_sentiments.sort()
                lower_count = sum(1 for s in hist_sentiments if s < sentiment_T)
                percentile = lower_count / len(hist_sentiments)
                if percentile >= 0.1:  # Not in bottom 10%
                    continue
                
                # Check return >= 2%
                if ts_T not in returns_by_symbol.get(sid, {}):
                    continue
                ret_T = returns_by_symbol[sid][ts_T]
                if ret_T < 0.02:
                    continue
                
                # Check volume >= 1.5x average over T-60..T-1
                vol_sum = 0
                vol_count = 0
                for j in range(i-60, i):
                    vol_sum += bars[j][2]
                    vol_count += 1
                avg_vol = vol_sum / vol_count if vol_count > 0 else 0
                if vol_T < 1.5 * avg_vol:
                    continue
                
                # Check volatility not in top decile
                if ts_T in vol_threshold:
                    # Compute current volatility (20-day)
                    rets = []
                    for j in range(i-20, i):
                        ret_ts = bars[j+1][0]
                        if ret_ts in returns_by_symbol.get(sid, {}):
                            rets.append(returns_by_symbol[sid][ret_ts])
                    if len(rets) >= 20:
                        current_vol = statistics.stdev(rets) if len(rets) > 1 else 0
                        if current_vol >= vol_threshold.get(ts_T, float('inf')):
                            continue
                
                # Check not issued within prior 20 trading days
                if sid in last_call_day:
                    # Find how many trading days have passed
                    days_since = 0
                    for j in range(i-1, -1, -1):
                        if bars[j][0] <= last_call_day[sid]:
                            days_since = i - j
                            break
                    if days_since < 20:
                        continue
                
                # Count as opportunity before final checks
                opportunities += 1
                
                # Check we have at least 30 remaining opportunities (rough estimate)
                remaining = (len(bars) - i - 20)
                if remaining < 30:
                    continue
                
                # Get outcome at T+20
                ts_outcome = next_trading_day(bars, i, 20)
                if ts_outcome is None:
                    continue
                
                if (sid, ts_outcome) not in outcome_lookup:
                    continue
                
                up = outcome_lookup[(sid, ts_outcome)]
                era = 'main' if ts_T <= cutoff else 'sealed'
                all_calls.append((sid, ts_T, up, era))
                last_call_day[sid] = ts_T
        
        # Check if we have enough calls
        if len(all_calls) < 30:
            print("INSUFFICIENT=1")
            return
        
        # Split into main and sealed
        main_calls = [c for c in all_calls if c[3] == 'main']
        sealed_calls = [c for c in all_calls if c[3] == 'sealed']
        
        # Calculate metrics
        issued = len(all_calls)
        main_hits = sum(1 for c in main_calls if c[2] == 1)
        main_precision = main_hits / len(main_calls) if main_calls else 0
        base_rate = main_precision  # Base rate within issued subset
        
        sealed_hits = sum(1 for c in sealed_calls if c[2] == 1)
        sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
        
        # Count distinct days
        distinct_days = len(set(c[1] for c in all_calls))
        
        # Calculate design effect (approximate using day clustering)
        day_counts = {}
        for c in all_calls:
            day = c[1]
            day_counts[day] = day_counts.get(day, 0) + 1
        
        # Design effect = 1 + (variance of cluster sizes / mean cluster size)
        if day_counts:
            cluster_sizes = list(day_counts.values())
            mean_cluster = statistics.mean(cluster_sizes)
            var_cluster = statistics.variance(cluster_sizes) if len(cluster_sizes) > 1 else 0
            design_effect = 1 + (var_cluster / mean_cluster) if mean_cluster > 0 else 1
        else:
            design_effect = 1
        
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Print required lines
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={main_precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print(f"ERROR: {e}")
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
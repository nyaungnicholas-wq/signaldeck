import sqlite3
from collections import defaultdict
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
        cursor = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0

    try:
        # Get all symbols
        cursor.execute("SELECT id, symbol FROM symbols")
        symbols = {row[0]: row[1] for row in cursor.fetchall()}
        
        # Get daily bars for all symbols
        cursor.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume
            FROM bars
            WHERE tf = '1d'
            ORDER BY symbol_id, ts
        """)
        
        bars_by_symbol = defaultdict(list)
        for row in cursor.fetchall():
            symbol_id, ts, o, h, l, c, v = row
            if all(x is not None for x in [ts, o, h, l, c, v]):
                bars_by_symbol[symbol_id].append((ts, o, h, l, c, v))
        
        # Get prediction outcomes (labels)
        cursor.execute("""
            SELECT symbol_id, ts, up
            FROM prediction_outcomes
            WHERE horizon = 20
        """)
        labels = {}
        for row in cursor.fetchall():
            labels[(row[0], row[1])] = row[2]
        
        # Get all trading days in sorted order for cooldown tracking
        cursor.execute("SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts")
        all_trading_days = [row[0] for row in cursor.fetchall()]
        trading_day_to_idx = {day: i for i, day in enumerate(all_trading_days)}
        
        conn.close()
        
        # Process each symbol
        opportunities = []
        for symbol_id, bars in bars_by_symbol.items():
            if len(bars) < 252:
                continue
            
            # Sort by timestamp
            bars.sort(key=lambda x: x[0])
            ts_list = [b[0] for b in bars]
            
            # Precompute features
            n_bars = len(bars)
            returns = [None]
            for i in range(1, n_bars):
                prev_c = bars[i-1][4]
                curr_c = bars[i][4]
                if prev_c > 0:
                    returns.append(curr_c / prev_c - 1)
                else:
                    returns.append(None)
            
            for t_idx in range(252, n_bars):
                T = bars[t_idx][0]
                T_close = bars[t_idx][4]
                T_high = bars[t_idx][2]
                T_low = bars[t_idx][3]
                
                # Check if we have label for this T
                label_key = (symbol_id, T)
                if label_key not in labels:
                    continue
                
                # Basic filter: close >= $5
                if T_close < 5:
                    continue
                
                # Check for missing values in required window
                has_missing = False
                for i in range(t_idx - 252, t_idx + 1):
                    if bars[i][4] is None:
                        has_missing = True
                        break
                if has_missing:
                    continue
                
                # Check 60-day average dollar volume
                total_dollar_vol = 0
                count_vol = 0
                for i in range(t_idx - 60, t_idx):
                    c = bars[i][4]
                    v = bars[i][5]
                    if c is not None and v is not None:
                        total_dollar_vol += c * v
                        count_vol += 1
                
                if count_vol < 60 or (total_dollar_vol / count_vol) < 5_000_000:
                    continue
                
                # Check 60-day return minimum
                min_return = None
                for i in range(t_idx - 60, t_idx):
                    if returns[i] is not None:
                        if min_return is None or returns[i] < min_return:
                            min_return = returns[i]
                
                if min_return is None:
                    continue
                
                # Check T's return
                T_return = returns[t_idx]
                if T_return is None:
                    continue
                
                # Entry conditions
                if T_return > -0.05:
                    continue
                
                # Check close above midpoint
                midpoint = (T_high + T_low) / 2
                if T_close <= midpoint:
                    continue
                
                # Check return is minimum of T-60..T-1
                if T_return != min_return:
                    continue
                
                # Check 20-day realized volatility in top decile
                vol_window = []
                for i in range(t_idx - 20, t_idx + 1):
                    if i < len(returns) and returns[i] is not None:
                        vol_window.append(returns[i])
                
                if len(vol_window) < 20:
                    continue
                
                mean_ret = sum(vol_window) / len(vol_window)
                variance = sum((r - mean_ret) ** 2 for r in vol_window) / (len(vol_window) - 1)
                vol_20 = math.sqrt(variance)
                
                # Store opportunity
                opportunities.append((symbol_id, T, vol_20))
        
        # Calculate cross-sectional volatility decile for each T
        vol_by_ts = defaultdict(list)
        for symbol_id, T, vol in opportunities:
            vol_by_ts[T].append(vol)
        
        vol_deciles = {}
        for T, vols in vol_by_ts.items():
            vols_sorted = sorted(vols)
            idx = int(0.9 * len(vols_sorted))
            vol_deciles[T] = vols_sorted[idx]
        
        # Filter opportunities with volatility and cooldown
        filtered_opportunities = []
        last_call_ts = {}  # symbol_id -> last call timestamp
        cooldown_days = 20
        
        for symbol_id, T, vol in opportunities:
            # Check volatility decile
            if T in vol_deciles and vol >= vol_deciles[T]:
                continue
            
            # Check cooldown (20 trading days)
            if symbol_id in last_call_ts:
                last_idx = trading_day_to_idx[last_call_ts[symbol_id]]
                curr_idx = trading_day_to_idx[T]
                if curr_idx - last_idx < cooldown_days:
                    continue
            
            filtered_opportunities.append((symbol_id, T))
            last_call_ts[symbol_id] = T
        
        # Check 30 independent observations minimum
        distinct_days = set(T for _, T in filtered_opportunities)
        if len(distinct_days) < 30:
            print("INSUFFICIENT=1")
            return 0
        
        # Split into train and sealed eras
        if not filtered_opportunities:
            print("INSUFFICIENT=1")
            return 0
        
        all_ts = sorted(set(T for _, T in filtered_opportunities))
        split_idx = int(len(all_ts) * 0.8)
        sealed_start = all_ts[split_idx]
        
        # Count metrics
        issued = len(filtered_opportunities)
        opportunities_count = len(opportunities)
        distinct_days_issued = len(distinct_days)
        
        # Calculate precision and base rate
        hits = 0
        sealed_hits = 0
        sealed_issued = 0
        
        for symbol_id, T in filtered_opportunities:
            if labels.get((symbol_id, T), 0) == 1:
                hits += 1
                if T >= sealed_start:
                    sealed_hits += 1
            if T >= sealed_start:
                sealed_issued += 1
        
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0
        
        # Calculate effective sample size (design effect)
        if distinct_days_issued > 0:
            day_counts = defaultdict(int)
            for _, T in filtered_opportunities:
                day_counts[T] += 1
            
            avg_cluster_size = sum(day_counts.values()) / len(day_counts)
            
            # Calculate intracluster correlation
            overall_mean = hits / issued
            ss_between = 0
            ss_within = 0
            
            for day, count in day_counts.items():
                day_hits = sum(1 for s, t in filtered_opportunities if t == day and labels.get((s, t), 0) == 1)
                day_mean = day_hits / count
                ss_between += count * (day_mean - overall_mean) ** 2
                ss_within += count * day_mean * (1 - day_mean)
            
            k = len(day_counts)
            if k > 1 and issued > k:
                variance_between = ss_between / (k - 1)
                variance_within = ss_within / (issued - k)
                
                if variance_between + variance_within > 0:
                    icc = variance_between / (variance_between + variance_within)
                    design_effect = 1 + (avg_cluster_size - 1) * icc
                else:
                    design_effect = 1
            else:
                design_effect = 1
        else:
            design_effect = 1
        
        effective_n = issued / design_effect if design_effect > 0 else 0
        
        # Check invariants
        if distinct_days_issued > issued:
            print("INSUFFICIENT=1")
            return 0
        if effective_n >= issued:
            print("INSUFFICIENT=1")
            return 0
        
        # Calculate sealed precision
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={distinct_days_issued}")
        print(f"EFFECTIVE_N={effective_n}")
        print(f"SEALED_PRECISION={sealed_precision}")
        
        return 0
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0

if __name__ == "__main__":
    exit(main())
import sqlite3
import math
from collections import defaultdict
import bisect

def main():
    db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = db.cursor()
    
    # Load all daily bars with symbol_id, ts, close, volume
    rows = cur.execute(
        "SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts"
    ).fetchall()
    
    if not rows:
        print("INSUFFICIENT=1")
        return
    
    # Group by symbol_id, preserving order
    symbol_bars = defaultdict(list)
    for sym, ts, close, vol in rows:
        symbol_bars[sym].append((ts, close, vol))
    
    # Load prediction outcomes for horizon=20 (labels)
    labels = cur.execute(
        "SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=20"
    ).fetchall()
    label_map = defaultdict(dict)
    for sym, ts, up in labels:
        label_map[sym][ts] = up
    
    db.close()
    
    # Precompute for each symbol
    all_candidates = []  # (ts, sym, idx_in_bars)
    
    for sym, bar_list in symbol_bars.items():
        n = len(bar_list)
        if n < 252:
            continue
        
        # Precompute rolling medians, max closes, etc.
        for i in range(251, n):
            ts, close, vol = bar_list[i]
            if close < 5.0:
                continue
            
            # Average daily dollar volume over prior 60 days
            if i < 60:
                continue
            total_dollar_vol = 0.0
            count_dollar_vol = 0
            ok = True
            for j in range(i-60, i):
                c, v = bar_list[j][1], bar_list[j][2]
                if c is None or v is None:
                    ok = False
                    break
                if v and c:
                    total_dollar_vol += c * v
                    count_dollar_vol += 1
            if not ok or count_dollar_vol < 60:
                continue
            avg_dollar_vol = total_dollar_vol / count_dollar_vol
            if avg_dollar_vol < 10_000_000:
                continue
            
            # Check missing data in T-60..T
            if i < 60:
                continue
            ok = True
            for j in range(i-60, i+1):
                c, v = bar_list[j][1], bar_list[j][2]
                if c is None or v is None:
                    ok = False
                    break
            if not ok:
                continue
            
            # 20-day realized volatility
            if i < 20:
                continue
            log_returns = []
            for k in range(i-19, i+1):
                prev = bar_list[k-1][1]
                curr = bar_list[k][1]
                if prev and curr and prev > 0:
                    log_returns.append(math.log(curr/prev))
            if len(log_returns) < 20:
                continue
            mean = sum(log_returns) / len(log_returns)
            var = sum((r - mean) ** 2 for r in log_returns) / (len(log_returns) - 1)
            vol20 = math.sqrt(var)
            
            # T-20 close
            if i < 20:
                continue
            close_T_minus20 = bar_list[i-20][1]
            if close_T_minus20 is None or close_T_minus20 == 0:
                continue
            
            # Max close over T-60..T-1
            max_c = max(bar_list[j][1] for j in range(i-60, i))
            if max_c is None or max_c == 0:
                continue
            
            # Median volume over T-60..T-1
            vols = [bar_list[j][2] for j in range(i-60, i)]
            vols.sort()
            median_vol = vols[30] if len(vols) > 30 else vols[-1]
            if median_vol == 0:
                continue
            
            all_candidates.append((ts, sym, i, vol20, max_c, median_vol, close_T_minus20))
    
    if not all_candidates:
        print("INSUFFICIENT=1")
        return
    
    # Sort candidates by ts
    all_candidates.sort(key=lambda x: x[0])
    
    # Extract all unique timestamps to split into sealed/non-sealed
    all_ts = sorted(set(c[0] for c in all_candidates))
    sealed_cutoff = all_ts[int(len(all_ts) * 0.8)] if len(all_ts) > 0 else None
    
    # Process in time order
    issued_calls = []  # (ts, sym, label)
    last_call_idx = defaultdict(int)  # sym -> index in issued_calls for last call
    day_volatilities = defaultdict(list)  # ts -> list of vol20
    
    # First pass: compute volatilities per day for cross-sectional decile
    for ts, sym, idx, vol20, max_c, median_vol, close_T_minus20 in all_candidates:
        day_volatilities[ts].append(vol20)
    
    # Compute 90th percentile per day
    day_90th = {}
    for ts, vols in day_volatilities.items():
        vols_sorted = sorted(vols)
        p90_idx = int(len(vols_sorted) * 0.9)
        day_90th[ts] = vols_sorted[p90_idx] if p90_idx < len(vols_sorted) else vols_sorted[-1]
    
    # Second pass: filter for UP calls
    for ts, sym, idx, vol20, max_c, median_vol, close_T_minus20 in all_candidates:
        bar_list = symbol_bars[sym]
        close_T = bar_list[idx][1]
        
        # Conditions for UP call
        # 1) T's close-to-close return <= -3%
        if idx == 0:
            continue
        prev_close = bar_list[idx-1][1]
        if prev_close is None or prev_close == 0:
            continue
        ret_T = (close_T - prev_close) / prev_close
        if ret_T > -0.03:
            continue
        
        # 2) volume >= 3.0x median volume over T-60..T-1
        if close_T is None or vol is None:
            continue
        vol_T = bar_list[idx][2]
        if vol_T < 3.0 * median_vol:
            continue
        
        # 3) close <= 90% of max close over T-60..T-1
        if close_T > 0.9 * max_c:
            continue
        
        # 4) close <= 90% of T-20's close
        if close_T > 0.9 * close_T_minus20:
            continue
        
        # Abstention: volatility in top 10% cross-sectionally
        p90 = day_90th.get(ts)
        if p90 is not None and vol20 > p90:
            continue
        
        # Abstention: call in prior 20 trading days for same symbol
        # Find last call index for this symbol
        last_idx = last_call_idx.get(sym, -1)
        if last_idx >= 0:
            last_call_ts = issued_calls[last_idx][0]
            # Count trading days between last_call_ts and ts
            # Since bars are trading days, find indices in bar_list
            last_idx_in_bars = -1
            curr_idx_in_bars = -1
            for j in range(len(bar_list)):
                if bar_list[j][0] == last_call_ts:
                    last_idx_in_bars = j
                if bar_list[j][0] == ts:
                    curr_idx_in_bars = j
                    break
            if last_idx_in_bars >= 0 and curr_idx_in_bars >= 0:
                trading_days_diff = curr_idx_in_bars - last_idx_in_bars
                if trading_days_diff <= 20:
                    continue
        
        # Get label
        label = label_map.get(sym, {}).get(ts)
        if label is None:
            continue
        
        issued_calls.append((ts, sym, label))
        last_call_idx[sym] = len(issued_calls) - 1
    
    # Check for at least 30 independent observations in non-sealed era
    non_sealed_calls = [(ts, sym, lab) for ts, sym, lab in issued_calls if ts < sealed_cutoff]
    if len(non_sealed_calls) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Compute metrics for non-sealed era
    issued_non_sealed = len(non_sealed_calls)
    opportunities_non_sealed = sum(1 for ts, sym, idx, vol20, max_c, median_vol, close_T_minus20 in all_candidates if ts < sealed_cutoff)
    precision_non_sealed = sum(1 for ts, sym, lab in non_sealed_calls if lab == 1) / issued_non_sealed if issued_non_sealed > 0 else 0
    base_rate_non_sealed = precision_non_sealed  # base rate of UP within issued subset
    
    distinct_days_non_sealed = len(set(ts for ts, sym, lab in non_sealed_calls))
    
    # Effective sample size: cluster by day
    day_counts = defaultdict(int)
    day_up_counts = defaultdict(int)
    for ts, sym, lab in non_sealed_calls:
        day_counts[ts] += 1
        if lab == 1:
            day_up_counts[ts] += 1
    
    if issued_non_sealed == 0:
        print("INSUFFICIENT=1")
        return
    
    # Overall proportion
    p = precision_non_sealed
    
    # Cluster means
    cluster_means = []
    for ts in day_counts:
        if day_counts[ts] > 0:
            cluster_means.append(day_up_counts[ts] / day_counts[ts])
    
    # Overall variance
    total_var = p * (1 - p)
    
    # Between-cluster variance
    mean_cluster_mean = sum(cluster_means) / len(cluster_means) if cluster_means else 0
    between_var = sum((m - mean_cluster_mean) ** 2 for m in cluster_means) / (len(cluster_means) - 1) if len(cluster_means) > 1 else 0
    
    # ICC
    if total_var > 0:
        icc = between_var / total_var
    else:
        icc = 0
    
    # Average cluster size
    m = issued_non_sealed / len(day_counts) if day_counts else 1
    
    # Design effect
    design_effect = 1 + (m - 1) * icc
    
    effective_n = issued_non_sealed / design_effect if design_effect > 0 else issued_non_sealed
    
    # Sealed era precision
    sealed_calls = [(ts, sym, lab) for ts, sym, lab in issued_calls if ts >= sealed_cutoff]
    if not sealed_calls:
        sealed_precision = 0.0
    else:
        sealed_precision = sum(1 for ts, sym, lab in sealed_calls if lab == 1) / len(sealed_calls)
    
    # Output
    print(f"ISSUED={issued_non_sealed}")
    print(f"OPPORTUNITIES={opportunities_non_sealed}")
    print(f"PRECISION={precision_non_sealed:.4f}")
    print(f"BASE_RATE={base_rate_non_sealed:.4f}")
    print(f"DISTINCT_DAYS={distinct_days_non_sealed}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
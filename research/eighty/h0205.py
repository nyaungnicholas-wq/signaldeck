import sqlite3
import datetime
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Get symbols that have both daily bars and insider trades
    c.execute("""
        SELECT DISTINCT symbol_id 
        FROM bars WHERE tf='1d' 
        INTERSECT 
        SELECT DISTINCT symbol_id FROM insider_trades
    """)
    symbols = [r[0] for r in c.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Load all daily bars for these symbols
    c.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' AND symbol_id IN ({}) ORDER BY symbol_id, ts".format(','.join('?'*len(symbols))), symbols)
    bars_by_sym = defaultdict(list)
    for sid, ts, close, vol in c.fetchall():
        bars_by_sym[sid].append((ts, close, vol))
    
    # Load all insider purchases (code='P', shares>0)
    c.execute("SELECT symbol_id, insider, filed_ts FROM insider_trades WHERE code='P' AND shares>0 AND symbol_id IN ({})".format(','.join('?'*len(symbols))), symbols)
    purchases = defaultdict(lambda: defaultdict(set))
    for sid, insider, filed_ts in c.fetchall():
        day = datetime.datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
        purchases[sid][day].add(insider)
    
    # Get all distinct trading days across all symbols
    all_days_set = set()
    for sid in symbols:
        for ts, _, _ in bars_by_sym[sid]:
            all_days_set.add(ts)
    all_days = sorted(all_days_set)
    day_to_idx = {d: i for i, d in enumerate(all_days)}
    
    # Helper: get index of a timestamp in global all_days
    def day_idx(ts):
        return day_to_idx.get(ts, -1)
    
    # Precompute for each symbol and each possible T:
    #  - We need 252 prior sessions, so T must have index >=252 in symbol's own bar list
    #  - We'll compute all conditions and store candidate calls
    candidate_calls = []
    for sid in symbols:
        bars = bars_by_sym[sid]
        if len(bars) < 272:  # 252 + 20 for forward
            continue
        # Map timestamp to position in this symbol's bar list
        ts_to_pos = {ts: i for i, (ts, _, _) in enumerate(bars)}
        # For each possible T (must be a day with insider purchases)
        for disc_date, insiders in purchases[sid].items():
            if len(insiders) < 2:
                continue
            # Find if disc_date corresponds to a bar for this symbol
            # We need to convert disc_date to a timestamp that matches a bar's ts
            # Since bars.ts is a unix epoch, we need to find the bar with ts on that calendar day
            # We'll search through the symbol's bars for a ts that falls on disc_date
            disc_dt = datetime.datetime.strptime(disc_date, '%Y-%m-%d')
            disc_ts_candidates = []
            for ts, close, vol in bars:
                bar_dt = datetime.datetime.utcfromtimestamp(ts)
                if bar_dt.date() == disc_dt.date():
                    disc_ts_candidates.append((ts, close, vol))
            if not disc_ts_candidates:
                continue
            # Usually one bar per day; take the first
            T_ts, T_close, T_vol = disc_ts_candidates[0]
            T_idx_in_sym = ts_to_pos[T_ts]
            
            # Check 252 prior sessions
            if T_idx_in_sym < 251:  # need at least 252 bars before T (indices 0..251)
                continue
            
            # Get prior 60 bars for avg dollar volume
            prior_60 = bars[T_idx_in_sym-60:T_idx_in_sym]  # T_idx_in_sym is exclusive? We want before T
            if len(prior_60) < 60:
                continue
            avg_dollar_vol = sum(c*v for _, c, v in prior_60) / 60
            if avg_dollar_vol < 10_000_000:
                continue
            
            # Price >= $5
            if T_close < 5:
                continue
            
            # 200-day moving average (close prices)
            prior_200 = bars[T_idx_in_sym-200:T_idx_in_sym+1]  # include T
            if len(prior_200) < 200:
                continue
            ma200 = sum(c for _, c, _ in prior_200) / 200
            if T_close <= ma200:
                continue
            
            # Return on T between -1% and +3%
            if T_idx_in_sym == 0:
                continue
            prev_close = bars[T_idx_in_sym-1][1]
            ret_on_T = T_close / prev_close - 1
            if ret_on_T < -0.01 or ret_on_T > 0.03:
                continue
            
            # Volume >= 1.5x 20-session median volume (of volumes prior to T)
            prior_20_vol = [bars[j][2] for j in range(T_idx_in_sym-20, T_idx_in_sym)]
            if len(prior_20_vol) < 20:
                continue
            # Compute median of prior_20_vol
            sorted_vol = sorted(prior_20_vol)
            mid = 10
            median_vol = (sorted_vol[mid-1] + sorted_vol[mid]) / 2
            if T_vol < 1.5 * median_vol:
                continue
            
            # Compute 20-session realized volatility (stdev of daily returns) at T
            # Use returns from T-19 to T (20 returns, need 21 prices)
            vol_window = bars[T_idx_in_sym-20:T_idx_in_sym+1]
            if len(vol_window) < 21:
                continue
            returns = []
            for i in range(1, len(vol_window)):
                c_prev = vol_window[i-1][1]
                c_curr = vol_window[i][1]
                if c_prev > 0:
                    returns.append(c_curr / c_prev - 1)
            if len(returns) < 2:
                continue
            mean_r = sum(returns) / len(returns)
            var_r = sum((r - mean_r) ** 2 for r in returns) / (len(returns) - 1)
            vol_20 = math.sqrt(var_r)
            
            # We'll later filter cross-sectionally for top decile
            candidate_calls.append({
                'sid': sid,
                'T_ts': T_ts,
                'T_date': disc_date,
                'vol_20': vol_20
            })
    
    if len(candidate_calls) < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Group candidates by T_date to compute cross-sectional decile
    candidates_by_date = defaultdict(list)
    for call in candidate_calls:
        candidates_by_date[call['T_date']].append(call)
    
    # Filter out calls where vol_20 is in top decile for that day
    filtered = []
    for day, calls in candidates_by_date.items():
        if len(calls) == 0:
            continue
        vols = [call['vol_20'] for call in calls]
        vols_sorted = sorted(vols)
        # 90th percentile index
        idx90 = int(math.ceil(0.9 * len(vols_sorted))) - 1
        if idx90 < 0:
            idx90 = 0
        vol_90th = vols_sorted[idx90]
        for call in calls:
            if call['vol_20'] <= vol_90th:  # not in top decile
                filtered.append(call)
    
    if len(filtered) < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Now enforce no repeat calls within 20 trading days per symbol
    # We need a mapping from trading day timestamps to consecutive index
    # Use all_days list
    # We'll keep for each symbol the last call's day index
    last_call_idx = {}
    final_calls = []
    for call in filtered:
        sid = call['sid']
        T_ts = call['T_ts']
        T_idx = day_idx(T_ts)
        if T_idx == -1:
            continue
        if sid in last_call_idx:
            if T_idx - last_call_idx[sid] <= 20:
                continue
        final_calls.append(call)
        last_call_idx[sid] = T_idx
    
    if len(final_calls) < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Now compute forward returns and labels
    labeled = []
    for call in final_calls:
        sid = call['sid']
        bars = bars_by_sym[sid]
        ts_to_pos = {ts: i for i, (ts, _, _) in enumerate(bars)}
        T_pos = ts_to_pos.get(call['T_ts'])
        if T_pos is None or T_pos + 20 >= len(bars):
            continue
        T_close = bars[T_pos][1]
        T20_close = bars[T_pos + 20][1]
        fwd_ret = T20_close / T_close - 1
        is_up = 1 if fwd_ret > 0 else 0
        call['is_up'] = is_up
        call['T_idx_global'] = day_idx(call['T_ts'])
        labeled.append(call)
    
    if not labeled:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Sort by global day index
    labeled.sort(key=lambda x: x['T_idx_global'])
    
    # Split into train (first 80%) and sealed (last 20%)
    split = int(len(labeled) * 0.8)
    train = labeled[:split]
    sealed = labeled[split:]
    
    # Metrics
    issued = len(labeled)
    opportunities = len(final_calls)  # considered calls (before forward return filter)
    hits = sum(c['is_up'] for c in labeled)
    precision = hits / issued
    base_rate = precision  # base rate of UP in issued subset
    distinct_days = len(set(c['T_date'] for c in labeled))
    
    # Design effect: group by day, compute within-day variance
    day_groups = defaultdict(list)
    for c in labeled:
        day_groups[c['T_date']].append(c['is_up'])
    
    # Design effect = 1 + (mean_cluster_size - 1) * ICC
    # We compute ICC as variance between groups / total variance
    grand_mean = sum(c['is_up'] for c in labeled) / issued
    total_var = sum((c['is_up'] - grand_mean)**2 for c in labeled) / (issued - 1)
    between_var = 0.0
    for day, labels in day_groups.items():
        n = len(labels)
        mean_g = sum(labels) / n
        between_var += n * (mean_g - grand_mean)**2
    between_var /= (len(day_groups) - 1)
    
    if total_var == 0:
        icc = 0
    else:
        # Average cluster size
        avg_n = issued / len(day_groups)
        icc = (between_var - total_var / avg_n) / (total_var * (avg_n - 1) / avg_n)
        if icc < 0:
            icc = 0
    
    design_effect = 1 + icc * (issued / len(day_groups) - 1)
    effective_n = issued / design_effect
    
    # Sealed precision
    sealed_hits = sum(c['is_up'] for c in sealed)
    sealed_precision = sealed_hits / len(sealed) if sealed else 0.0
    
    # Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == '__main__':
    main()
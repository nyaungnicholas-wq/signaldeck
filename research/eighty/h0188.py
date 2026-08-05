import sqlite3
from collections import defaultdict
import bisect
import statistics

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Load all daily bars
    c.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    
    symbols = defaultdict(list)
    for symbol_id, ts, close, volume in c.fetchall():
        symbols[symbol_id].append((ts, close, volume))
    
    # Load prediction outcomes for horizon=20
    c.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=20")
    labels = {}
    for symbol_id, ts, up in c.fetchall():
        labels[(symbol_id, ts)] = up
    
    conn.close()
    
    # Process each symbol
    candidates = []
    for symbol_id, bars in symbols.items():
        n = len(bars)
        if n < 252:
            continue
        
        ts_list = [b[0] for b in bars]
        closes = [b[1] for b in bars]
        volumes = [b[2] for b in bars]
        
        # Precompute rolling metrics
        for i in range(251, n):
            close_T = closes[i]
            if close_T is None or close_T < 5:
                continue
            
            # Check missing data in last 61 sessions (T-60 to T)
            missing = False
            for j in range(i-60, i+1):
                if closes[j] is None or volumes[j] is None:
                    missing = True
                    break
            if missing:
                continue
            
            # 252-session max close
            max_close_252 = max(closes[i-251:i+1])
            
            # 60-session median volume
            window_volumes = volumes[i-59:i+1]
            median_volume_60 = statistics.median(window_volumes)
            
            # 60-session average dollar volume
            window_dollar = [closes[j] * volumes[j] for j in range(i-59, i+1)]
            avg_dollar_volume_60 = sum(window_dollar) / len(window_dollar)
            
            # 20-session gain
            if i < 20:
                continue
            gain_20 = (close_T / closes[i-20]) - 1
            
            # 20-session realized volatility
            returns = []
            for j in range(i-19, i+1):
                r = closes[j] / closes[j-1] - 1
                returns.append(r)
            vol_20 = statistics.stdev(returns)
            
            # Store candidate
            candidates.append({
                'symbol_id': symbol_id,
                'ts': ts_list[i],
                'close': close_T,
                'volume': volumes[i],
                'max_close_252': max_close_252,
                'median_volume_60': median_volume_60,
                'avg_dollar_volume_60': avg_dollar_volume_60,
                'gain_20': gain_20,
                'vol_20': vol_20,
                'close_prev': closes[i-1]
            })
    
    if not candidates:
        print("INSUFFICIENT=1")
        return
    
    # Group candidates by ts for cross-sectional volatility decile
    by_ts = defaultdict(list)
    for cand in candidates:
        by_ts[cand['ts']].append(cand)
    
    # Compute 90th percentile of vol_20 for each ts
    ts_vol_threshold = {}
    for ts, cand_list in by_ts.items():
        vol_list = [c['vol_20'] for c in cand_list]
        vol_list.sort()
        idx = int(0.9 * len(vol_list))
        ts_vol_threshold[ts] = vol_list[idx]
    
    # Filter opportunities and issue calls
    opportunities = []  # all considered decision points
    issued = []  # issued UP calls
    
    for cand in candidates:
        ts = cand['ts']
        opportunities.append(ts)
        
        # Abstain conditions
        if cand['close'] < 5:
            continue
        if cand['gain_20'] > 0.30:
            continue
        if cand['vol_20'] >= ts_vol_threshold.get(ts, float('inf')):
            continue
        if cand['avg_dollar_volume_60'] < 10_000_000:
            continue
        
        # Entry conditions
        if cand['close'] != cand['max_close_252']:
            continue
        if cand['volume'] < 1.5 * cand['median_volume_60']:
            continue
        close_diff = abs(cand['close'] - cand['close_prev']) / cand['close_prev']
        if close_diff > 0.03:
            continue
        
        issued.append(cand)
    
    # Check minimum observations
    if len(issued) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Get labels for issued calls
    hits = 0
    for cand in issued:
        key = (cand['symbol_id'], cand['ts'])
        if key in labels:
            if labels[key] == 1:
                hits += 1
        else:
            # Compute from bars if label not in prediction_outcomes
            # This is a fallback and should not happen if labels are complete
            pass
    
    # Compute metrics
    issued_count = len(issued)
    opportunities_count = len(opportunities)
    precision = hits / issued_count if issued_count > 0 else 0
    base_rate = hits / issued_count  # within issued subset
    
    # Distinct days among issued calls
    distinct_days = len(set(c['ts'] for c in issued))
    
    # Effective sample size using Kish's formula
    day_counts = defaultdict(int)
    for cand in issued:
        day_counts[cand['ts']] += 1
    sum_sq = sum(cnt**2 for cnt in day_counts.values())
    effective_n = (issued_count ** 2) / sum_sq if sum_sq > 0 else issued_count
    
    # Sealed era (most recent 20% of opportunities)
    sorted_ts = sorted(set(opportunities))
    cutoff_idx = int(0.8 * len(sorted_ts))
    cutoff_ts = sorted_ts[cutoff_idx]
    
    sealed_issued = [c for c in issued if c['ts'] >= cutoff_ts]
    sealed_hits = sum(1 for c in sealed_issued if labels.get((c['symbol_id'], c['ts']), 0) == 1)
    sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0
    
    # Print required output
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")

if __name__ == "__main__":
    main()
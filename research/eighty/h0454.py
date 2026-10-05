# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 453
# cycle_index: 44
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    
    # Get all symbols with short_volume data
    symbols = conn.execute("SELECT symbol_id FROM short_volume GROUP BY symbol_id").fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Build price and volume data for all 1d bars
    price_data = defaultdict(dict)
    for row in conn.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d'"):
        symbol_id, ts, close, volume = row
        dt = datetime.utcfromtimestamp(ts)
        day_str = dt.strftime('%Y-%m-%d')
        price_data[symbol_id][day_str] = (close, volume)
    
    # Build short_volume data per symbol per day
    short_data = defaultdict(dict)
    for row in conn.execute("SELECT symbol_id, day, short_vol, total_vol FROM short_volume"):
        symbol_id, day, short_vol, total_vol = row
        if total_vol > 0:
            short_data[symbol_id][day] = short_vol / total_vol
    
    # Find all possible decision dates from short_volume
    all_dates = set()
    for data in short_data.values():
        all_dates.update(data.keys())
    all_dates = sorted(all_dates)
    
    if len(all_dates) < 25:
        print("INSUFFICIENT=1")
        return
    
    # Split into train and sealed (last 20%)
    split_idx = int(len(all_dates) * 0.8)
    sealed_cutoff = all_dates[split_idx]
    
    issued_calls = []
    
    for symbol_id in [s[0] for s in symbols]:
        sv_days = sorted(short_data.get(symbol_id, {}).keys())
        if len(sv_days) < 25:
            continue
        
        # Build aligned lists
        for i in range(20, len(sv_days)):
            entry_date = sv_days[i]
            lookback_dates = sv_days[i-20:i+1]
            
            # Check 20 days of short-volume data exist
            if len(lookback_dates) < 21:
                continue
            
            # Calculate average daily dollar volume over last 20 days
            total_dollar_vol = 0
            valid_dollar_vol_days = 0
            for d in lookback_dates:
                if d in price_data.get(symbol_id, {}):
                    close, volume = price_data[symbol_id][d]
                    total_dollar_vol += close * volume
                    valid_dollar_vol_days += 1
            
            if valid_dollar_vol_days < 20:
                continue
            
            avg_dollar_vol = total_dollar_vol / valid_dollar_vol_days
            if avg_dollar_vol < 5_000_000:
                continue
            
            # Need 5-day lookback for signal
            if i < 5:
                continue
            
            lookback_5d = sv_days[i-5:i+1]
            if len(lookback_5d) < 6:
                continue
            
            # Calculate 5-day change in short-volume ratio
            if lookback_5d[0] not in short_data[symbol_id] or lookback_5d[-1] not in short_data[symbol_id]:
                continue
            
            ratio_start = short_data[symbol_id][lookback_5d[0]]
            ratio_end = short_data[symbol_id][lookback_5d[-1]]
            ratio_change = ratio_end - ratio_start
            
            # Calculate 5-day price change
            if lookback_5d[0] not in price_data.get(symbol_id, {}) or lookback_5d[-1] not in price_data.get(symbol_id, {}):
                continue
            
            price_start = price_data[symbol_id][lookback_5d[0]][0]
            price_end = price_data[symbol_id][lookback_5d[-1]][0]
            price_change = (price_end - price_start) / price_start
            
            # Signal conditions: rising short ratio during price decline
            if ratio_change <= 0 or price_change >= 0:
                continue
            
            # Check 5-day forward return (label)
            # Find the 5th trading day after entry_date in price_data
            future_dates = sorted([d for d in price_data.get(symbol_id, {}).keys() if d > entry_date])
            if len(future_dates) < 5:
                continue
            
            forward_date = future_dates[4]  # 5th day after entry
            forward_price = price_data[symbol_id][forward_date][0]
            entry_price = price_data[symbol_id][entry_date][0]
            forward_return = (forward_price - entry_price) / entry_price
            
            hit = 1 if forward_return < 0 else 0
            issued_calls.append((symbol_id, entry_date, hit))
    
    conn.close()
    
    if len(issued_calls) == 0:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics
    issued = len(issued_calls)
    hits = sum(1 for _, _, h in issued_calls if h == 1)
    precision = hits / issued
    base_rate = hits / issued
    
    # Distinct days
    distinct_days = len(set(d for _, d, _ in issued_calls))
    
    # Effective sample size using design effect
    # Cluster by day
    day_clusters = defaultdict(list)
    for _, d, h in issued_calls:
        day_clusters[d].append(h)
    
    # Calculate intra-class correlation
    k = len(day_clusters)
    n = issued
    n_j = [len(v) for v in day_clusters.values()]
    p_j = [sum(v)/len(v) for v in day_clusters.values()]
    p_bar = hits / n
    
    # Between-cluster variance
    ss_b = sum(n_j[j] * (p_j[j] - p_bar)**2 for j in range(k))
    m0 = sum(n_j) - sum(n_j[j]**2 for j in range(k)) / n
    
    # Within-cluster variance
    ss_w = 0
    for cluster in day_clusters.values():
        for val in cluster:
            ss_w += (val - p_bar)**2
    ss_w -= ss_b
    
    if m0 > 0 and k > 1:
        var_b = ss_b / (k - 1)
        var_w = ss_w / (n - k) if n > k else 0
        icc = var_b / (var_b + var_w) if (var_b + var_w) > 0 else 0
        design_effect = 1 + (sum(n_j)/k - 1) * icc
        effective_n = issued / design_effect
    else:
        effective_n = issued  # fallback
    
    # Sealed era metrics
    sealed_calls = [(s, d, h) for s, d, h in issued_calls if d >= sealed_cutoff]
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(1 for _, _, h in sealed_calls if h == 1)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")

if __name__ == "__main__":
    main()
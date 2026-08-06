#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get all symbols with at least 252 daily bars
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM bars WHERE tf='1d'
        GROUP BY symbol_id
        HAVING cnt >= 252
    """)
    symbols = [row[0] for row in cur.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get all daily bars for candidate symbols
    cur.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars WHERE tf='1d'
        ORDER BY symbol_id, ts
    """)
    all_rows = cur.fetchall()
    
    # Organize by symbol
    bars_by_symbol = defaultdict(list)
    for row in all_rows:
        symbol_id = row[0]
        if symbol_id in symbols:
            bars_by_symbol[symbol_id].append(row[1:])  # ts,open,high,low,close,volume
    
    # Get prediction outcomes for horizon=20
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon=20
    """)
    labels = {(row[0], row[1]): row[2] for row in cur.fetchall()}
    
    # Process each symbol
    opportunities = []
    issued_calls = []
    
    for symbol_id in symbols:
        bars = bars_by_symbol[symbol_id]
        n = len(bars)
        if n < 252:
            continue
        
        # Precompute closes and volumes
        closes = [b[4] for b in bars]
        volumes = [b[5] for b in bars]
        timestamps = [b[0] for b in bars]
        
        # Track last call timestamp for this symbol
        last_call_ts = None
        
        for i in range(251, n):
            ts = timestamps[i]
            close = closes[i]
            high = bars[i][2]
            low = bars[i][3]
            volume = volumes[i]
            
            # Condition: close >= $5
            if close < 5:
                continue
            
            # Condition: close within 95% of day's range
            if high == low:
                continue
            pos_in_range = (close - low) / (high - low)
            if pos_in_range < 0.95:
                continue
            
            # Condition: volume >= 2x avg volume over T-60..T-1
            if i < 60:
                continue
            avg_vol = sum(volumes[i-60:i]) / 60
            if volume < 2 * avg_vol:
                continue
            
            # Condition: close within 5% of 52-week high (T-251..T-1)
            max_close = max(closes[i-251:i])
            if close < 0.95 * max_close:
                continue
            
            # Condition: 20-day return between 0% and +10%
            if i < 20:
                continue
            ret20 = (close - closes[i-20]) / closes[i-20]
            if ret20 < 0 or ret20 > 0.10:
                continue
            
            # Condition: 1-day return between +0.5% and +5%
            ret1d = (close - closes[i-1]) / closes[i-1]
            if ret1d < 0.005 or ret1d > 0.05:
                continue
            
            # Condition: 20-day realized volatility not in top decile (will check cross-sectionally later)
            # Compute returns
            returns = []
            for j in range(i-19, i+1):
                returns.append((closes[j] - closes[j-1]) / closes[j-1])
            mean_ret = sum(returns) / 20
            var = sum((r - mean_ret) ** 2 for r in returns) / 19
            vol = math.sqrt(var)
            
            # Abstain if call in prior 20 trading days
            if last_call_ts is not None:
                # Approximate 20 trading days as 28 calendar days (market hours only not exact)
                if ts - last_call_ts < 28 * 86400:
                    continue
            
            # Record opportunity
            opportunities.append((symbol_id, ts, vol))
    
    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Split into training (80%) and sealed (20%) by time
    all_ts = sorted(set(o[1] for o in opportunities))
    cutoff_idx = int(len(all_ts) * 0.8)
    cutoff_ts = all_ts[cutoff_idx]
    
    training = [o for o in opportunities if o[1] <= cutoff_ts]
    sealed = [o for o in opportunities if o[1] > cutoff_ts]
    
    # Cross-sectional volatility filter on training set
    # Group by timestamp
    by_ts = defaultdict(list)
    for o in training:
        by_ts[o[1]].append(o)
    
    filtered_training = []
    for ts, ops in by_ts.items():
        vols = [o[2] for o in ops]
        vols.sort()
        threshold = vols[int(len(vols) * 0.9)]  # 90th percentile
        for op in ops:
            if op[2] <= threshold:
                filtered_training.append(op)
    
    # Apply call frequency filter on training set
    final_training = []
    last_calls = {}  # symbol_id -> last_ts
    for op in sorted(filtered_training, key=lambda x: x[1]):
        symbol_id, ts, _ = op
        if symbol_id in last_calls and ts - last_calls[symbol_id] < 28 * 86400:
            continue
        final_training.append(op)
        last_calls[symbol_id] = ts
    
    # Apply same filters on sealed set, considering calls from training
    sealed_filtered = []
    for op in sealed:
        symbol_id, ts, vol = op
        # Check volatility (use training thresholds)
        if ts in by_ts:
            vols = [o[2] for o in by_ts[ts]]
            vols.sort()
            if len(vols) > 0:
                threshold = vols[int(len(vols) * 0.9)]
                if vol > threshold:
                    continue
        # Check call frequency
        if symbol_id in last_calls and ts - last_calls[symbol_id] < 28 * 86400:
            continue
        sealed_filtered.append(op)
        last_calls[symbol_id] = ts  # Update for sequential processing
    
    # Combine for total issued
    total_issued = final_training + sealed_filtered
    
    # Get labels
    hits = 0
    base_pos = 0
    for op in total_issued:
        symbol_id, ts, _ = op
        key = (symbol_id, ts)
        if key in labels:
            if labels[key] == 1:
                hits += 1
                base_pos += 1
    
    # Compute metrics
    issued = len(total_issued)
    precision = hits / issued if issued > 0 else 0
    base_rate = base_pos / issued if issued > 0 else 0
    
    # Distinct days
    distinct_days = len(set(o[1] for o in total_issued))
    
    # Design effect (assuming calls are not independent due to clustering)
    # Group calls by date, then compute effective sample size
    date_counts = defaultdict(int)
    for op in total_issued:
        date_counts[op[1]] += 1
    avg_per_day = issued / distinct_days if distinct_days > 0 else 1
    # Simple design effect: 1 + (intracluster correlation) * (cluster_size - 1)
    # Assume moderate correlation of 0.1
    design_effect = 1 + 0.1 * (avg_per_day - 1)
    effective_n = issued / design_effect
    
    # Sealed precision
    sealed_hits = 0
    sealed_issued = len(sealed_filtered)
    for op in sealed_filtered:
        symbol_id, ts, _ = op
        key = (symbol_id, ts)
        if key in labels and labels[key] == 1:
            sealed_hits += 1
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
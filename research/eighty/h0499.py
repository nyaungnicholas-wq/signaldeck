# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 498
# cycle_index: 28
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import statistics

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get all daily bars for all symbols
    cur.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    rows = cur.fetchall()
    
    if not rows:
        print("INSUFFICIENT=1")
        return
    
    # Group by symbol
    symbols = {}
    for sid, ts, close in rows:
        if sid not in symbols:
            symbols[sid] = []
        symbols[sid].append((ts, close))
    
    # Calculate 20% cutoff for sealed era
    all_timestamps = [ts for sid_data in symbols.values() for ts, _ in sid_data]
    if not all_timestamps:
        print("INSUFFICIENT=1")
        return
    max_ts = max(all_timestamps)
    cutoff_ts = int(max_ts - 0.2 * (max_ts - min(all_timestamps)))
    
    calls = []
    opportunities = 0
    
    for sid, data in symbols.items():
        if len(data) < 250:
            continue
        
        closes = [c for _, c in data]
        timestamps = [t for t, _ in data]
        
        # Calculate MAs
        ma200 = []
        ma50 = []
        for i in range(len(closes)):
            if i >= 199:
                ma200.append(sum(closes[i-199:i+1]) / 200)
            else:
                ma200.append(None)
            if i >= 49:
                ma50.append(sum(closes[i-49:i+1]) / 50)
            else:
                ma50.append(None)
        
        # Find downtrend periods (250 sessions below MA200)
        below_ma200 = []
        for i in range(len(closes)):
            if ma200[i] is not None:
                below_ma200.append(closes[i] < ma200[i])
            else:
                below_ma200.append(False)
        
        # Check for 250 consecutive days below MA200
        downtrend_ends = []
        count = 0
        for i in range(len(below_ma200)):
            if below_ma200[i]:
                count += 1
                if count >= 250:
                    downtrend_ends.append(i)
            else:
                count = 0
        
        # Find crossover after downtrend
        for idx in downtrend_ends:
            # Need at least one more day for crossover
            if idx + 1 >= len(closes):
                continue
            
            if ma50[idx] is None or ma200[idx] is None:
                continue
            if ma50[idx+1] is None or ma200[idx+1] is None:
                continue
            
            # Crossover: MA50 crosses above MA200
            if ma50[idx] < ma200[idx] and ma50[idx+1] >= ma200[idx+1]:
                # Call issued at next session's open (idx+2)
                if idx + 2 < len(timestamps):
                    call_ts = timestamps[idx+2]
                    opportunities += 1
                    
                    # Resolve at idx+2+21
                    if idx + 2 + 21 < len(timestamps):
                        # Check as-of: call_ts must be before cutoff or in sealed era
                        # We'll collect all calls, then split later
                        calls.append((sid, call_ts, idx+2, idx+2+21))
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Get labels from prediction_outcomes
    # Need horizon=21, ts matching call timestamps
    call_info = {}
    for sid, call_ts, call_idx, res_idx in calls:
        cur.execute("SELECT up FROM prediction_outcomes WHERE symbol_id=? AND ts=? AND horizon=21", 
                   (sid, call_ts))
        row = cur.fetchone()
        if row:
            up = row[0]
            call_info[(sid, call_ts)] = up
    
    # Split into regular and sealed
    regular_calls = []
    sealed_calls = []
    for sid, call_ts, call_idx, res_idx in calls:
        if (sid, call_ts) in call_info:
            up = call_info[(sid, call_ts)]
            if call_ts < cutoff_ts:
                regular_calls.append((sid, call_ts, up))
            else:
                sealed_calls.append((sid, call_ts, up))
    
    # Calculate metrics for regular set
    if not regular_calls:
        print("INSUFFICIENT=1")
        return
    
    issued = len(regular_calls)
    hits = sum(1 for _, _, up in regular_calls if up)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate
    up_count = sum(1 for _, _, up in regular_calls if up)
    base_rate = up_count / issued if issued > 0 else 0
    
    # Distinct days
    days = set(call_ts for _, call_ts, _ in regular_calls)
    distinct_days = len(days)
    
    # Design effect - cluster by day
    day_clusters = {}
    for sid, call_ts, up in regular_calls:
        day = call_ts // 86400  # Approximate day
        if day not in day_clusters:
            day_clusters[day] = []
        day_clusters[day].append(up)
    
    # Calculate ICC
    cluster_means = []
    all_vals = []
    for day, vals in day_clusters.items():
        cluster_mean = sum(vals) / len(vals)
        cluster_means.append(cluster_mean)
        all_vals.extend(vals)
    
    if len(cluster_means) < 2:
        # Not enough clusters for ICC, use conservative estimate
        design_effect = 1.1
    else:
        grand_mean = sum(all_vals) / len(all_vals)
        between_var = statistics.variance(cluster_means) if len(cluster_means) > 1 else 0
        within_var = 0
        for day, vals in day_clusters.items():
            p = sum(vals) / len(vals)
            within_var += len(vals) * p * (1 - p)
        within_var /= len(all_vals)
        
        if between_var + within_var == 0:
            design_effect = 1.1
        else:
            icc = between_var / (between_var + within_var)
            avg_cluster_size = issued / len(day_clusters)
            design_effect = 1 + (avg_cluster_size - 1) * icc
    
    effective_n = issued / design_effect
    
    # Sealed precision
    sealed_hits = sum(1 for _, _, up in sealed_calls if up)
    sealed_issued = len(sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    conn.close()
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
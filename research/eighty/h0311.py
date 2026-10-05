# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 310
# cycle_index: 33
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'
conn = sqlite3.connect(DB_PATH, uri=True)
cur = conn.cursor()

# Get all daily bars
cur.execute("""
    SELECT symbol_id, ts, close, volume FROM bars WHERE tf = '1d' ORDER BY symbol_id, ts
""")
bars = cur.fetchall()

# Build per-symbol lists
from collections import defaultdict
symbol_bars = defaultdict(list)
for symbol_id, ts, close, volume in bars:
    symbol_bars[symbol_id].append((ts, close, volume))

# Get prediction outcomes for 21-day horizon
cur.execute("""
    SELECT symbol_id, ts, up, fwd_return FROM prediction_outcomes WHERE horizon = 21
""")
labels = {}
for symbol_id, ts, up, fwd_return in cur.fetchall():
    labels[(symbol_id, ts)] = (up, fwd_return)

# Process symbols
records = []
for symbol_id, bar_list in symbol_bars.items():
    n = len(bar_list)
    if n < 253:  # need at least 252 prior + current day
        continue
    for i in range(252, n):
        T_ts, T_close, T_volume = bar_list[i]
        # Check if 21-day label exists
        if (symbol_id, T_ts) not in labels:
            continue
        # Check as-of: label available at T
        up_label, fwd_return_label = labels[(symbol_id, T_ts)]
        if up_label is None:
            continue
        # Compute returns
        close_252 = bar_list[i-252][1]
        close_126 = bar_list[i-126][1]
        close_21 = bar_list[i-21][1]
        if close_252 == 0 or close_126 == 0 or close_21 == 0:
            continue
        ret252 = (T_close - close_252) / close_252
        ret126 = (T_close - close_126) / close_126
        ret21 = (T_close - close_21) / close_21
        # Check returns are not zero or missing
        if ret126 == 0 or ret21 == 0:
            continue
        # Compute average daily dollar volume over last 63 days
        dollar_vols = []
        for j in range(max(0, i-62), i+1):  # include T
            d_ts, d_close, d_volume = bar_list[j]
            dollar_vols.append(d_close * d_volume)
        avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
        if avg_dollar_vol < 1_000_000:
            continue
        records.append((symbol_id, T_ts, ret252, ret126, ret21, up_label))

if len(records) < 100:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Sort by timestamp
records.sort(key=lambda x: x[1])

# Determine train/seal split
all_days = sorted(set(r[1] for r in records))
n_days = len(all_days)
split_idx = int(n_days * 0.8)
seal_cutoff = all_days[split_idx-1]

# Split records
train_records = [r for r in records if r[1] <= seal_cutoff]
seal_records = [r for r in records if r[1] > seal_cutoff]

def process_set(recs):
    # Group by day
    day_groups = defaultdict(list)
    for r in recs:
        day_groups[r[1]].append(r)
    
    issued = 0
    opportunities = len(recs)
    hits = 0
    issued_days = []
    
    for day, day_recs in day_groups.items():
        # Cross-sectional percentiles of 252-day return
        ret252_vals = [r[2] for r in day_recs]
        if not ret252_vals:
            continue
        sorted_ret = sorted(ret252_vals)
        p95 = sorted_ret[int(len(sorted_ret) * 0.95)] if len(sorted_ret) > 1 else sorted_ret[-1]
        p05 = sorted_ret[int(len(sorted_ret) * 0.05)] if len(sorted_ret) > 1 else sorted_ret[0]
        
        for r in day_recs:
            symbol_id, ts, ret252, ret126, ret21, up_label = r
            if ret252 >= p95 and ret126 > 0 and ret21 > 0:
                # Up call
                issued += 1
                issued_days.append(ts)
                if up_label == 1:
                    hits += 1
            elif ret252 <= p05 and ret126 < 0 and ret21 < 0:
                # Down call
                issued += 1
                issued_days.append(ts)
                if up_label == 0:
                    hits += 1
    
    # Base rate within issued
    if issued == 0:
        return None
    
    # Compute base rate: proportion of up labels in issued set
    up_count = 0
    for r in recs:
        if (r[2] >= p95 and r[3] > 0 and r[4] > 0) or (r[2] <= p05 and r[3] < 0 and r[4] < 0):
            if r[5] == 1:
                up_count += 1
    base_rate = up_count / issued if issued > 0 else 0
    
    # Effective sample size
    # Count per-day
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for r in recs:
        if (r[2] >= p95 and r[3] > 0 and r[4] > 0) or (r[2] <= p05 and r[3] < 0 and r[4] < 0):
            day_counts[r[1]] += 1
            if r[5] == 1:
                day_hits[r[1]] += 1
    
    if issued == 0:
        return None
    
    p = hits / issued  # overall precision
    
    # Compute ICC
    k = list(day_counts.values())
    m = len(k)
    if m <= 1:
        design_effect = 1.0
    else:
        # Between-day variance
        sum_sq = 0
        for d, cnt in day_counts.items():
            p_d = day_hits[d] / cnt
            sum_sq += cnt * (p_d - p)**2
        MSB = sum_sq / (m - 1)
        
        # Within-day variance
        sum_var = 0
        for d, cnt in day_counts.items():
            p_d = day_hits[d] / cnt
            sum_var += (cnt - 1) * p_d * (1 - p_d)
        MSW = sum_var / (issued - m)
        
        ICC = MSB / (MSB + MSW) if (MSB + MSW) > 0 else 0
        m_bar = issued / m
        design_effect = 1 + (m_bar - 1) * ICC
    
    effective_n = issued / design_effect
    
    distinct_days = len(set(issued_days))
    
    return {
        'issued': issued,
        'opportunities': opportunities,
        'precision': hits / issued,
        'base_rate': base_rate,
        'distinct_days': distinct_days,
        'effective_n': effective_n
    }

train_stats = process_set(train_records)
if train_stats is None:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

seal_stats = process_set(seal_records)
seal_precision = seal_stats['precision'] if seal_stats else 0

print(f"ISSUED={train_stats['issued']}")
print(f"OPPORTUNITIES={train_stats['opportunities']}")
print(f"PRECISION={train_stats['precision']:.6f}")
print(f"BASE_RATE={train_stats['base_rate']:.6f}")
print(f"DISTINCT_DAYS={train_stats['distinct_days']}")
print(f"EFFECTIVE_N={train_stats['effective_n']:.6f}")
print(f"SEALED_PRECISION={seal_precision:.6f}")

conn.close()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 523
# cycle_index: 53
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
from collections import defaultdict

def median(lst):
    lst = sorted(lst)
    n = len(lst)
    if n % 2 == 1:
        return lst[n//2]
    else:
        return (lst[n//2 - 1] + lst[n//2]) / 2

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
c = conn.cursor()

# Get all daily bars
c.execute("SELECT symbol_id, ts, open, high, low, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
bars = c.fetchall()
conn.close()

# Group by symbol
by_symbol = defaultdict(list)
for row in bars:
    symbol_id, ts, open_p, high, low, close, vol = row
    by_symbol[symbol_id].append((ts, open_p, high, low, close, vol))

# Process each symbol
opportunities = []
issued_calls = []
for symbol_id, symbol_bars in by_symbol.items():
    n = len(symbol_bars)
    if n < 40:
        continue
    
    # Precompute rolling stats
    for i in range(39, n - 5):
        entry_ts, entry_open, entry_high, entry_low, entry_close, entry_vol = symbol_bars[i]
        prev_ts, prev_open, prev_high, prev_low, prev_close, prev_vol = symbol_bars[i-1]
        
        # Check trailing 40 bars condition
        if i < 39:
            continue
            
        # Compute trailing 20-day median dollar volume (ending i-1)
        dollar_vols = []
        for j in range(i-20, i):
            _, _, _, _, close_j, vol_j = symbol_bars[j]
            dollar_vols.append(close_j * vol_j)
        median_dollar_vol = median(dollar_vols)
        if median_dollar_vol < 1_000_000:
            continue
            
        # Compute trailing 20-day median volume (ending i-1)
        vols = [symbol_bars[j][5] for j in range(i-20, i)]
        median_vol = median(vols)
        
        # Entry conditions
        gap = entry_open / prev_close - 1
        cond1 = gap >= 0.03
        cond2 = entry_close < prev_close
        day_range = entry_high - entry_low
        cond3 = entry_close <= entry_low + 0.25 * day_range if day_range > 0 else False
        cond4 = entry_vol >= 1.5 * median_vol
        cond5 = entry_close >= 5
        
        # Record opportunity (considered)
        opportunities.append((symbol_id, entry_ts))
        
        if not (cond1 and cond2 and cond3 and cond4 and cond5):
            continue
            
        # Get forward return over next 5 trading days
        future_idx = i + 5
        future_close = symbol_bars[future_idx][4]
        fwd_return = (future_close - entry_close) / entry_close
        hit = 1 if fwd_return < 0 else 0
        
        issued_calls.append((symbol_id, entry_ts, hit))

# Convert timestamps to dates for distinct days
def ts_to_date(ts):
    return datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

# Split into training and sealed eras (most recent 20% by time)
if issued_calls:
    all_dates = sorted(set(ts for _, ts, _ in issued_calls))
    split_idx = int(len(all_dates) * 0.8)
    if split_idx < len(all_dates):
        sealed_cutoff = all_dates[split_idx]
    else:
        sealed_cutoff = all_dates[-1]
else:
    sealed_cutoff = None

# Compute metrics
issued = len(issued_calls)
opportunities_count = len(opportunities)
if issued == 0:
    print("INSUFFICIENT=1")
else:
    hits = sum(hit for _, _, hit in issued_calls)
    precision = hits / issued
    
    # Base rate within issued subset
    down_calls = [hit for _, _, hit in issued_calls]
    base_rate = sum(down_calls) / len(down_calls)
    
    # Distinct days among issued calls
    distinct_days = len(set(ts for _, ts, _ in issued_calls))
    
    # Design effect calculation (clustering by day)
    day_groups = defaultdict(int)
    for _, ts, hit in issued_calls:
        day = ts_to_date(ts)
        day_groups[day] += 1
    
    # Compute design effect using ICC approximation
    avg_cluster_size = issued / len(day_groups) if day_groups else 0
    # Estimate ICC from variance of day means
    day_means = []
    for day, count in day_groups.items():
        day_hits = sum(1 for d, _, h in issued_calls if ts_to_date(d) == day and h == 1)
        day_means.append(day_hits / count)
    
    if len(day_means) > 1:
        overall_mean = precision
        var_between = sum((m - overall_mean)**2 for m in day_means) / (len(day_means) - 1)
        var_within = precision * (1 - precision)
        icc = var_between / (var_between + var_within) if (var_between + var_within) > 0 else 0
    else:
        icc = 0
    
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Sealed era metrics
    sealed_calls = [(s, t, h) for s, t, h in issued_calls if t >= sealed_cutoff] if sealed_cutoff else []
    sealed_issued = len(sealed_calls)
    if sealed_issued > 0:
        sealed_hits = sum(h for _, _, h in sealed_calls)
        sealed_precision = sealed_hits / sealed_issued
    else:
        sealed_precision = 0.0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={int(effective_n)}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 410
# cycle_index: 1
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict

DB_PATH = 'data/signaldeck.db'
conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
conn.row_factory = sqlite3.Row
cursor = conn.cursor()

# Get all daily bars ordered by symbol and timestamp
cursor.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
bars = cursor.fetchall()

# Organize by symbol: list of (ts, close) in chronological order
symbol_bars = defaultdict(list)
for row in bars:
    symbol_bars[row['symbol_id']].append((row['ts'], row['close']))

# Get prediction_outcomes for horizon=21
cursor.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=21")
outcomes = {(r['symbol_id'], r['ts']): r['up'] for r in cursor.fetchall()}
conn.close()

# Precompute per symbol: rolling min of 252 closes, 21-day return, and eligibility
eligible_calls = []  # (ts, symbol_id, up_label)

for sym_id, ts_close_list in symbol_bars.items():
    n = len(ts_close_list)
    if n < 756:
        continue  # Not enough history
    
    # Precompute rolling 252-day min of closes
    min_252 = [None] * n
    for i in range(n - 251):
        # min of closes[i] to closes[i+251]
        min_val = min(close for _, close in ts_close_list[i:i+252])
        min_252[i+251] = min_val
    
    # Check each day from index 755 onward (need at least 756 days before)
    for i in range(755, n):
        ts_i, close_i = ts_close_list[i]
        
        # Check 252-session low
        if min_252[i] is None:
            continue
        if close_i != min_252[i]:
            continue
        
        # Check prior 21-day return negative
        if i < 21:
            continue
        close_t_minus_21 = ts_close_list[i-21][1]
        close_t_minus_1 = ts_close_list[i-1][1]
        if close_t_minus_21 == 0:
            continue
        prior_return = (close_t_minus_1 - close_t_minus_21) / close_t_minus_21
        if prior_return >= 0:
            continue
        
        # Check price >= $2
        if close_i < 2:
            continue
        
        # Check market return on day ts_i >= +1.0%
        # Compute equal-weight mean return of all eligible symbols on day ts_i
        day_returns = []
        for other_sym, other_list in symbol_bars.items():
            # Binary search for ts_i in other_list
            low, high = 0, len(other_list) - 1
            idx = None
            while low <= high:
                mid = (low + high) // 2
                if other_list[mid][0] == ts_i:
                    idx = mid
                    break
                elif other_list[mid][0] < ts_i:
                    low = mid + 1
                else:
                    high = mid - 1
            if idx is None:
                continue
            
            # Need previous day's close for this other symbol
            if idx < 1:
                continue
            other_close_today = other_list[idx][1]
            other_close_yesterday = other_list[idx-1][1]
            if other_close_yesterday == 0:
                continue
            other_return = (other_close_today - other_close_yesterday) / other_close_yesterday
            
            # Check if other symbol is eligible on day ts_i
            # Need at least 756 days before ts_i and close>=2
            if idx < 755:
                continue
            if other_close_today < 2:
                continue
            day_returns.append(other_return)
        
        if not day_returns:
            continue
        market_return = sum(day_returns) / len(day_returns)
        if market_return < 0.01:
            continue
        
        # Get label from prediction_outcomes
        label = outcomes.get((sym_id, ts_i))
        if label is None:
            continue
        
        eligible_calls.append((ts_i, sym_id, label))

if not eligible_calls:
    print("INSUFFICIENT=1")
    sys.exit(0)

# Sort by timestamp
eligible_calls.sort(key=lambda x: x[0])

# Split into training and sealed era (most recent 20%)
n_total = len(eligible_calls)
seal_idx = int(n_total * 0.8)
sealed_calls = eligible_calls[seal_idx:]
train_calls = eligible_calls[:seal_idx]

# Compute base metrics
hits = sum(1 for _, _, up in eligible_calls if up == 1)
issued = n_total
base_rate = hits / issued if issued > 0 else 0

# Distinct days in issued calls
distinct_days = len(set(ts for ts, _, _ in eligible_calls))

# Effective N: design effect via day clustering
# Group calls by day
day_groups = defaultdict(list)
for ts, _, up in eligible_calls:
    day_groups[ts].append(up)

# Compute ICC and design effect
k = len(day_groups)
if k <= 1:
    design_effect = 1.0  # No clustering effect
else:
    # Overall proportion
    p = hits / issued if issued > 0 else 0
    # Variance of the outcome
    var_total = p * (1 - p) if 0 < p < 1 else 1e-9
    
    # Variance between days
    sum_sq = 0.0
    total_in_groups = 0
    for day, outcomes in day_groups.items():
        n_j = len(outcomes)
        p_j = sum(outcomes) / n_j
        sum_sq += n_j * (p_j - p) ** 2
        total_in_groups += n_j
    var_between = sum_sq / (k - 1) if k > 1 else 0
    
    icc = var_between / var_total if var_total > 0 else 0
    avg_cluster_size = total_in_groups / k
    design_effect = 1 + (avg_cluster_size - 1) * icc

effective_n = issued / design_effect if design_effect > 0 else issued

# Sealed era metrics
sealed_hits = sum(1 for _, _, up in sealed_calls if up == 1)
sealed_issued = len(sealed_calls)
sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0

# Print required lines
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={issued}")  # We only count issued as opportunities considered
print(f"PRECISION={hits/issued:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
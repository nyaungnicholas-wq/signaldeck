# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 303
# cycle_index: 26
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime
from collections import defaultdict

# Open database read-only
db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = db.cursor()

# Check required data exists
required_tables = ['bars', 'stocktwits_sentiment', 'prediction_outcomes']
for t in required_tables:
    try:
        cur.execute(f"SELECT COUNT(*) FROM {t}")
    except sqlite3.OperationalError:
        print("INSUFFICIENT=1")
        exit(0)

# Get daily price data
cur.execute("SELECT symbol_id, ts FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
price_data = defaultdict(list)
for symbol_id, ts in cur.fetchall():
    price_data[symbol_id].append(ts)

# Get stocktwits data and aggregate by day
cur.execute("SELECT symbol_id, ts, bullish, bearish, total FROM stocktwits_sentiment")
st_data = defaultdict(list)
for symbol_id, ts, bullish, bearish, total in cur.fetchall():
    st_data[symbol_id].append((ts, bullish, bearish, total))

# Get outcomes for horizon=21
cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=21")
outcomes = defaultdict(dict)
for symbol_id, ts, up in cur.fetchall():
    outcomes[symbol_id][ts] = up

db.close()

# Helper: convert epoch to date string
def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

# Process each symbol
all_calls = []
for symbol_id in set(price_data.keys()) & set(st_data.keys()):
    prices = price_data[symbol_id]
    st = st_data[symbol_id]
    
    # Need at least 252 price days and 21 ST days
    if len(prices) < 252 or len(st) < 21:
        continue
    
    # Sort ST by timestamp
    st.sort(key=lambda x: x[0])
    
    # Compute daily aggregated ST data
    daily_st = defaultdict(lambda: [0, 0, 0])  # [bullish, bearish, total]
    for ts, bullish, bearish, total in st:
        day = epoch_to_date(ts)
        daily_st[day][0] += bullish
        daily_st[day][1] += bearish
        daily_st[day][2] += total
    
    # Get sorted list of trading days
    trading_days = sorted([epoch_to_date(ts) for ts in prices])
    
    # Build rolling averages for ST
    st_days = sorted(daily_st.keys())
    if len(st_days) < 21:
        continue
    
    # For each potential call date
    last_call = None
    for i, day in enumerate(trading_days):
        # Check basic requirements
        if i < 251:  # Need 252 price days up to this point
            continue
        
        # Get ST data up to this day
        relevant_st = [d for d in st_days if d <= day]
        if len(relevant_st) < 21:
            continue
        
        # Get 21-day window of ST data
        last_21_st = relevant_st[-21:]
        total_msgs = sum(daily_st[d][2] for d in last_21_st)
        avg_msgs = total_msgs / 21
        
        # Check condition (1): total messages today >= 50
        if daily_st[day][2] < 50:
            continue
        
        # Check condition (2): 21-day avg in top decile of trailing 252-day history
        if len(relevant_st) >= 252:
            trailing_252_st = relevant_st[-252:]
            trailing_avgs = []
            for j in range(len(trailing_252_st) - 20):
                window = trailing_252_st[j:j+21]
                window_total = sum(daily_st[d][2] for d in window)
                trailing_avgs.append(window_total / 21)
            
            # Find 90th percentile
            trailing_avgs.sort()
            idx = int(len(trailing_avgs) * 0.9)
            p90 = trailing_avgs[idx]
            
            if avg_msgs < p90:
                continue
        else:
            continue
        
        # Check condition (3): bullish ratio between 0.42 and 0.58
        total_bull = sum(daily_st[d][0] for d in last_21_st)
        total_bear = sum(daily_st[d][1] for d in last_21_st)
        total_trades = total_bull + total_bear
        
        if total_trades == 0:
            continue
            
        bullish_ratio = total_bull / total_trades
        if not (0.42 <= bullish_ratio <= 0.58):
            continue
        
        # Check cooldown: no call for this symbol within prior 21 trading days
        if last_call is not None:
            idx_last = trading_days.index(last_call)
            idx_current = trading_days.index(day)
            if idx_current - idx_last < 21:
                continue
        
        # We have a valid call
        # Get outcome
        call_ts = int(datetime.strptime(day, '%Y-%m-%d').timestamp())
        if symbol_id in outcomes and call_ts in outcomes[symbol_id]:
            up = outcomes[symbol_id][call_ts]
            hit = 0 if up == 0 else 1  # DOWN call: up=0 is hit
            all_calls.append((day, symbol_id, hit))
            last_call = day

if len(all_calls) == 0:
    print("INSUFFICIENT=1")
    exit(0)

# Split into regular and sealed era
all_calls.sort(key=lambda x: x[0])  # Sort by date
cutoff_idx = int(len(all_calls) * 0.8)
regular_calls = all_calls[:cutoff_idx]
sealed_calls = all_calls[cutoff_idx:]

# Compute metrics
issued = len(all_calls)
hits = sum(c[2] for c in all_calls)
precision = hits / issued

# Base rate of DOWN (up=0) in issued subset
down_calls = sum(1 for c in all_calls if c[2] == 0)
base_rate = down_calls / issued

# Distinct days
distinct_days = len(set(c[0] for c in all_calls))

# Design effect and effective N
day_counts = defaultdict(int)
for c in all_calls:
    day_counts[c[0]] += 1

n = issued
k = len(day_counts)
if k > 1:
    # Compute design effect using intraclass correlation
    # For binary outcomes: ICC = (MSB - MSW) / (MSB + (m-1)*MSW)
    m = n / k
    
    # Overall proportion
    p = precision
    
    # Compute MSW and MSB
    msw_sum = 0
    msb_sum = 0
    
    for day, count in day_counts.items():
        day_hits = sum(c[2] for c in all_calls if c[0] == day)
        p_day = day_hits / count
        
        # Within-day variance for binary: p*(1-p)
        msw_sum += count * p_day * (1 - p_day)
        
        # Between-day contribution
        msb_sum += count * (p_day - p) ** 2
    
    msw = msw_sum / (n - k)
    msb = msb_sum / (k - 1)
    
    icc = (msb - msw) / (msb + (m - 1) * msw) if (msb + (m - 1) * msw) != 0 else 0
    design_effect = 1 + (m - 1) * icc
else:
    design_effect = n  # Worst case: all calls on one day

effective_n = n / design_effect

# Sealed era precision
sealed_issued = len(sealed_calls)
if sealed_issued > 0:
    sealed_hits = sum(c[2] for c in sealed_calls)
    sealed_precision = sealed_hits / sealed_issued
else:
    sealed_precision = 0.0

# Output
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={len(all_calls)}")  # Same as issued since we counted only issued
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.4f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")
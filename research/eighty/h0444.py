# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 443
# cycle_index: 34
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta

# Connect read-only
conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
cur = conn.cursor()

# Check if we have enough data by verifying tables exist and have rows
try:
    cur.execute("SELECT count(*) FROM bars WHERE tf='1d'")
    bars_count = cur.fetchone()[0]
    cur.execute("SELECT count(*) FROM sentiment_features")
    sent_count = cur.fetchone()[0]
    if bars_count < 1000 or sent_count < 1000:
        print("INSUFFICIENT=1")
        exit(0)
except:
    print("INSUFFICIENT=1")
    exit(0)

# Get the latest date in bars for 1d
cur.execute("SELECT max(ts) FROM bars WHERE tf='1d'")
latest_ts = cur.fetchone()[0]
if not latest_ts:
    print("INSUFFICIENT=1")
    exit(0)
latest_dt = datetime.utcfromtimestamp(latest_ts)

# Calculate the 20% holdout cutoff (sealed era starts 80% from start)
cur.execute("SELECT min(ts) FROM bars WHERE tf='1d'")
earliest_ts = cur.fetchone()[0]
if not earliest_ts:
    print("INSUFFICIENT=1")
    exit(0)
total_days = (latest_dt - datetime.utcfromtimestamp(earliest_ts)).days
cutoff_days = int(total_days * 0.8)
cutoff_dt = latest_dt - timedelta(days=cutoff_days)
cutoff_ts = int(cutoff_dt.timestamp())

# Get all symbols with both bars and sentiment data
cur.execute("""
    SELECT DISTINCT b.symbol_id 
    FROM bars b 
    INNER JOIN sentiment_features s ON b.symbol_id = s.symbol_id 
    WHERE b.tf = '1d' AND b.ts <= ?
""", (cutoff_ts,))
symbols = [row[0] for row in cur.fetchall()]
if not symbols:
    print("INSUFFICIENT=1")
    exit(0)

# Prepare containers
issued_calls = []
opportunities = []

# Process each symbol
for symbol_id in symbols:
    # Get all daily bars for this symbol up to cutoff, ordered by ts
    cur.execute("""
        SELECT ts, close, volume 
        FROM bars 
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts
    """, (symbol_id, cutoff_ts))
    bars = cur.fetchall()
    if len(bars) < 252:  # Need at least 252 days for 52-week low
        continue
    
    # Get sentiment data for this symbol, ordered by day
    cur.execute("""
        SELECT day, mean_score 
        FROM sentiment_features 
        WHERE symbol_id = ?
        ORDER BY day
    """, (symbol_id,))
    sent_data = cur.fetchall()
    if not sent_data:
        continue
    
    # Convert sentiment days to timestamps for easy lookup
    sent_dict = {}
    for day_str, score in sent_data:
        day_dt = datetime.strptime(day_str, '%Y-%m-%d')
        sent_dict[int(day_dt.timestamp())] = score
    
    # Process each day as potential decision point
    for i in range(252, len(bars)):
        ts_i, close_i, vol_i = bars[i]
        dt_i = datetime.utcfromtimestamp(ts_i)
        
        # Skip if no sentiment data for this day
        if ts_i not in sent_dict:
            continue
        
        # Get sentiment score for this day
        sent_i = sent_dict[ts_i]
        
        # Condition 1: Close is lowest in 252 sessions
        lowest_252 = min(close for _, close, _ in bars[i-252:i+1])
        if close_i > lowest_252:
            continue
        
        # Condition 2: Volume below 20-day average
        vol_window = [vol for _, _, vol in bars[i-20:i+1]]  # includes current day
        if len(vol_window) < 20:
            continue
        avg_vol = sum(vol_window) / len(vol_window)
        if vol_i >= avg_vol:
            continue
        
        # Condition 3: Sentiment in bottom 5% of 1-year distribution
        # Get sentiment scores for past 365 days (using day timestamps)
        sent_window = []
        for j in range(max(0, i-365), i+1):
            ts_j = bars[j][0]
            if ts_j in sent_dict:
                sent_window.append(sent_dict[ts_j])
        
        if len(sent_window) < 30:  # Need reasonable sample
            continue
        
        sent_window_sorted = sorted(sent_window)
        p5_idx = int(len(sent_window_sorted) * 0.05)
        if sent_i > sent_window_sorted[p5_idx]:
            continue
        
        # Check abstain conditions
        # (a) Price within 5% of 200-day moving average
        if i >= 200:
            ma200 = sum(close for _, close, _ in bars[i-200:i]) / 200
            pct_from_ma = abs(close_i - ma200) / ma200
            if pct_from_ma <= 0.05:
                continue
        
        # (b) Sentiment below 10th percentile for 3+ consecutive days
        if i >= 2:
            # Compute 10th percentile of past year sentiment
            p10_threshold = sent_window_sorted[int(len(sent_window_sorted) * 0.10)] if len(sent_window_sorted) > 0 else None
            if p10_threshold is not None:
                consecutive_below = 0
                for k in range(max(0, i-2), i+1):
                    ts_k = bars[k][0]
                    if ts_k in sent_dict:
                        if sent_dict[ts_k] < p10_threshold:
                            consecutive_below += 1
                        else:
                            break
                if consecutive_below >= 3:
                    continue
        
        # If we get here, we issue a call
        # Find the corresponding prediction_outcomes with horizon 21 days
        # Horizon 21 days = 21 * 86400 seconds
        horizon_ts = ts_i + 21 * 86400
        cur.execute("""
            SELECT up 
            FROM prediction_outcomes 
            WHERE symbol_id = ? AND ts = ? AND horizon = '21d'
        """, (symbol_id, ts_i))
        result = cur.fetchone()
        if not result:
            continue
        
        up = result[0]  # 1 for up, 0 for down/flat
        
        opportunities.append(ts_i)
        issued_calls.append((ts_i, up, symbol_id))

# Separate into regular and sealed eras
regular_calls = [call for call in issued_calls if call[0] <= cutoff_ts]
sealed_calls = [call for call in issued_calls if call[0] > cutoff_ts]

# Calculate metrics for regular era
total_issued = len(regular_calls)
total_opportunities = len(opportunities)
if total_issued == 0:
    print("INSUFFICIENT=1")
    exit(0)

hits = sum(1 for call in regular_calls if call[1] == 1)
precision = hits / total_issued if total_issued > 0 else 0

# Base rate within issued subset
base_rate = hits / total_issued if total_issued > 0 else 0

# Distinct days
distinct_days = len(set(datetime.utcfromtimestamp(call[0]).date() for call in regular_calls))

# Design effect for effective N
# Group calls by day
calls_by_day = {}
for call in regular_calls:
    day = datetime.utcfromtimestamp(call[0]).date()
    if day not in calls_by_day:
        calls_by_day[day] = 0
    calls_by_day[day] += 1

# Calculate design effect using variance of calls per day
mean_per_day = total_issued / distinct_days if distinct_days > 0 else 0
if mean_per_day > 0 and distinct_days > 1:
    variance = sum((count - mean_per_day) ** 2 for count in calls_by_day.values()) / distinct_days
    design_effect = 1 + variance / mean_per_day
else:
    design_effect = 1.0  # Default if only one day or no variation

effective_n = total_issued / design_effect if design_effect > 0 else total_issued

# Sealed era metrics
sealed_issued = len(sealed_calls)
if sealed_issued > 0:
    sealed_hits = sum(1 for call in sealed_calls if call[1] == 1)
    sealed_precision = sealed_hits / sealed_issued
else:
    sealed_precision = 0.0

# Print required metrics
print(f"ISSUED={total_issued}")
print(f"OPPORTUNITIES={total_opportunities}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.4f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")

conn.close()
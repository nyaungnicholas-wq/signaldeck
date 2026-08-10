# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 479
# cycle_index: 9
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
import statistics
import math

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
cursor = db.cursor()

# Get all insider purchase transactions with their symbols and dates
cursor.execute("""
    SELECT i.symbol_id, i.filed_ts, i.tx_ts, i.code
    FROM insider_trades i
    WHERE i.code = 'P'
    ORDER BY i.filed_ts
""")
purchases = cursor.fetchall()

if not purchases:
    print("INSUFFICIENT=1")
    exit(0)

# Get all symbols with daily bars
cursor.execute("""
    SELECT symbol_id, COUNT(*) as n_days
    FROM bars
    WHERE tf = '1d'
    GROUP BY symbol_id
    HAVING n_days >= 250
""")
symbol_days = {row[0]: row[1] for row in cursor.fetchall()}

if not symbol_days:
    print("INSUFFICIENT=1")
    exit(0)

# Pre-load all news scores by symbol and date (as unix day)
cursor.execute("""
    SELECT symbol_id, CAST(ts / 86400 AS INTEGER) as day, score
    FROM news
    WHERE score IS NOT NULL
    ORDER BY symbol_id, ts
""")
news_data = defaultdict(list)
for sym, day, score in cursor.fetchall():
    if sym in symbol_days:
        news_data[sym].append((day, score))

if not news_data:
    print("INSUFFICIENT=1")
    exit(0)

# Build rolling sentiment statistics per symbol per day
sentiment_stats = {}
for sym in news_data:
    days_scores = news_data[sym]
    if len(days_scores) < 10:
        continue
    
    # Calculate 10-day rolling average and 90th percentile over 250 days
    rolling_avgs = []
    daily_avg = []
    
    # First compute daily average (multiple news per day)
    day_to_scores = defaultdict(list)
    for day, score in days_scores:
        day_to_scores[day].append(score)
    
    sorted_days = sorted(day_to_scores.keys())
    if len(sorted_days) < 250:
        continue
    
    # Compute daily averages
    daily_avgs = []
    for day in sorted_days:
        avg = sum(day_to_scores[day]) / len(day_to_scores[day])
        daily_avgs.append((day, avg))
    
    # Compute 10-day rolling average
    rolling_10 = []
    for i in range(9, len(daily_avgs)):
        window = [avg for _, avg in daily_avgs[i-9:i+1]]
        rolling_avg = sum(window) / len(window)
        rolling_10.append((daily_avgs[i][0], rolling_avg))
    
    # Compute 90th percentile over 250-day windows
    stats_by_day = {}
    for i in range(249, len(rolling_10)):
        window = [avg for _, avg in rolling_10[i-249:i+1]]
        p90 = sorted(window)[int(0.9 * len(window))]
        stats_by_day[rolling_10[i][0]] = (rolling_10[i][1], p90)
    
    sentiment_stats[sym] = stats_by_day

# Now evaluate each insider purchase
opportunities = []
for sym, filed_ts, tx_ts, code in purchases:
    if sym not in sentiment_stats:
        continue
    if sym not in symbol_days:
        continue
    
    filed_day = int(filed_ts / 86400)
    
    # Check if we have sentiment stats for this symbol on this day
    if filed_day not in sentiment_stats[sym]:
        continue
    
    rolling_avg, p90 = sentiment_stats[sym][filed_day]
    if rolling_avg <= p90:
        continue
    
    # Get 21-day forward price direction
    # First get close price on or after filed_ts (as-of discipline: use only past data)
    cursor.execute("""
        SELECT close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC
        LIMIT 1
    """, (sym, filed_ts))
    row = cursor.fetchone()
    if not row:
        continue
    start_price = row[0]
    
    # Get close price 21 trading days later
    cursor.execute("""
        SELECT close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        ORDER BY ts
        LIMIT 21
    """, (sym, filed_ts))
    rows = cursor.fetchall()
    if len(rows) < 21:
        continue
    end_price = rows[-1][0]
    
    forward_return = (end_price - start_price) / start_price
    up = 1 if forward_return > 0 else 0
    
    opportunities.append({
        'sym': sym,
        'filed_ts': filed_ts,
        'up': up,
        'forward_return': forward_return
    })

db.close()

if not opportunities:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by time to split into training and sealed eras
opportunities.sort(key=lambda x: x['filed_ts'])
split_idx = int(len(opportunities) * 0.8)
training = opportunities[:split_idx]
sealed = opportunities[split_idx:]

# Calculate metrics for training set
issued = len(training)
if issued == 0:
    print("INSUFFICIENT=1")
    exit(0)

hits = sum(1 for o in training if o['up'] == 1)
precision = hits / issued
base_rate = precision  # Base rate within issued subset

# Count distinct days
distinct_days = len(set(int(o['filed_ts'] / 86400) for o in training))

# Calculate design effect for effective N
# Group calls by day
day_counts = defaultdict(int)
for o in training:
    day = int(o['filed_ts'] / 86400)
    day_counts[day] += 1

# Calculate intra-class correlation
daily_up_rates = []
for day, count in day_counts.items():
    day_up = sum(1 for o in training if int(o['filed_ts'] / 86400) == day and o['up'] == 1)
    daily_up_rates.append(day_up / count)

if len(daily_up_rates) > 1:
    variance_between = statistics.variance(daily_up_rates)
    variance_total = precision * (1 - precision)
    if variance_total > 0:
        icc = variance_between / variance_total
        avg_cluster_size = issued / len(day_counts)
        deff = 1 + (avg_cluster_size - 1) * icc
    else:
        deff = 1.0
else:
    deff = 1.0

effective_n = issued / deff if deff > 0 else issued

# Calculate sealed era metrics
sealed_issued = len(sealed)
if sealed_issued > 0:
    sealed_hits = sum(1 for o in sealed if o['up'] == 1)
    sealed_precision = sealed_hits / sealed_issued
else:
    sealed_precision = 0.0

# Print results
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={len(opportunities)}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
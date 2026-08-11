# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 552
# cycle_index: 10
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import re
from datetime import datetime, timedelta
from collections import defaultdict

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = db.cursor()

# Check required tables exist
required = ['bars', 'symbols', 'news', 'sentiment_features', 'prediction_outcomes']
cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
tables = set(row[0] for row in cur.fetchall())
if not all(t in tables for t in required):
    print("INSUFFICIENT=1")
    exit(0)

# Get universe: symbols in news/sentiment with >=252 bars
cur.execute("""
    SELECT n.symbol_id, COUNT(DISTINCT b.ts) as bar_count
    FROM news n
    JOIN bars b ON n.symbol_id = b.symbol_id AND b.tf = '1d'
    GROUP BY n.symbol_id
    HAVING bar_count >= 252
""")
symbols_data = cur.fetchall()
if not symbols_data:
    print("INSUFFICIENT=1")
    exit(0)

symbol_ids = [row[0] for row in symbols_data]
placeholders = ','.join(['?'] * len(symbol_ids))

# Get all daily bars for these symbols
cur.execute(f"""
    SELECT symbol_id, ts, close FROM bars
    WHERE symbol_id IN ({placeholders}) AND tf = '1d'
    ORDER BY symbol_id, ts
""", symbol_ids)
bars_data = cur.fetchall()

# Get all news headlines with timestamps
cur.execute(f"""
    SELECT symbol_id, ts, headline FROM news
    WHERE symbol_id IN ({placeholders})
    ORDER BY symbol_id, ts
""", symbol_ids)
news_data = cur.fetchall()

# Get sentiment scores
cur.execute(f"""
    SELECT symbol_id, day, mean_score FROM sentiment_features
    WHERE symbol_id IN ({placeholders})
    ORDER BY symbol_id, day
""", symbol_ids)
sentiment_data = cur.fetchall()

# Get labels (prediction_outcomes with horizon=10)
cur.execute(f"""
    SELECT symbol_id, ts, up FROM prediction_outcomes
    WHERE symbol_id IN ({placeholders}) AND horizon = 10
""", symbol_ids)
labels_data = cur.fetchall()
labels = {}
for sid, ts, up in labels_data:
    if up is not None:
        labels[(sid, ts)] = 1 if up else 0

# Organize data by symbol
bars_by_sym = defaultdict(list)
for sid, ts, close in bars_data:
    bars_by_sym[sid].append((ts, close))

news_by_sym = defaultdict(list)
for sid, ts, headline in news_data:
    news_by_sym[sid].append((ts, headline))

sentiment_by_sym = defaultdict(list)
for sid, day, score in sentiment_data:
    sentiment_by_sym[sid].append((day, score))

# Find latest timestamp in bars to determine 20% holdout
all_ts = [ts for sid, ts, _ in bars_data]
if not all_ts:
    print("INSUFFICIENT=1")
    exit(0)
max_ts = max(all_ts)
cutoff_ts = max_ts - int(0.2 * (max_ts - min(all_ts)))

# Process each symbol
all_calls = []
opportunities = 0

for sid in symbol_ids:
    if sid not in bars_by_sym:
        continue
    
    bars = bars_by_sym[sid]
    ts_list = [ts for ts, _ in bars]
    close_dict = {ts: close for ts, close in bars}
    
    if len(ts_list) < 252:
        continue
    
    news = news_by_sym.get(sid, [])
    sentiment = sentiment_by_sym.get(sid, [])
    
    # Convert news timestamps to date strings for digit detection
    news_by_day = defaultdict(list)
    for ts, headline in news:
        day_str = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        has_digit = bool(re.search(r'\d', headline))
        news_by_day[day_str].append(has_digit)
    
    # Convert sentiment to dict: day_str -> mean_score
    sent_by_day = {}
    for day, score in sentiment:
        sent_by_day[day] = score
    
    # Compute 20-day SMA for each day
    close_by_ts = {ts: close for ts, close in bars}
    sma20 = {}
    for i in range(len(ts_list)):
        if i < 19:
            continue
        window = [close_by_ts[ts_list[j]] for j in range(i-19, i+1)]
        sma20[ts_list[i]] = sum(window) / 20
    
    # Track last 10 trading days for cooldown
    last_call_ts = -9999
    
    # Iterate through trading days (as timestamps)
    for i, ts in enumerate(ts_list):
        if ts <= last_call_ts + 10 * 86400:
            continue
            
        day_str = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        
        # Check condition (a): at least one headline with digit on day t
        if day_str not in news_by_day:
            continue
        if not any(news_by_day[day_str]):
            continue
        
        # Check condition (b): sentiment positive and top decile of trailing 252 days
        if day_str not in sent_by_day:
            continue
        score = sent_by_day[day_str]
        if score <= 0:
            continue
            
        # Get trailing 252 days of sentiment scores
        trailing_scores = []
        for j in range(i-251, i+1):
            if j < 0:
                continue
            trail_day = datetime.utcfromtimestamp(ts_list[j]).strftime('%Y-%m-%d')
            if trail_day in sent_by_day:
                trailing_scores.append(sent_by_day[trail_day])
        
        if len(trailing_scores) < 252:
            continue
            
        # Compute 90th percentile
        trailing_scores_sorted = sorted(trailing_scores)
        idx = int(0.9 * len(trailing_scores_sorted))
        percentile_90 = trailing_scores_sorted[idx]
        
        if score < percentile_90:
            continue
            
        # Check condition (c): close above 20-day SMA
        if ts not in sma20:
            continue
        if close_by_ts[ts] <= sma20[ts]:
            continue
            
        # Check label exists
        label = labels.get((sid, ts))
        if label is None:
            continue
            
        opportunities += 1
        
        # Check condition (d): no call in prior 10 trading days
        if ts - last_call_ts < 10 * 86400:
            continue
            
        # Issue call
        all_calls.append((ts, label, ts <= cutoff_ts))
        last_call_ts = ts

db.close()

# Compute metrics
if len(all_calls) == 0:
    print("INSUFFICIENT=1")
    exit(0)

total_calls = len(all_calls)
hits = sum(1 for _, label, _ in all_calls if label == 1)
precision = hits / total_calls
base_rate = precision  # Base rate within issued subset is same as precision for UP calls

# Count distinct days among issued calls
call_days = set()
for ts, _, _ in all_calls:
    day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
    call_days.add(day)
distinct_days = len(call_days)

# Compute design effect: cluster by day
day_clusters = defaultdict(list)
for ts, label, _ in all_calls:
    day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
    day_clusters[day].append(label)

# Compute ICC (intraclass correlation) using ANOVA method
cluster_means = []
for day, labels_list in day_clusters.items():
    cluster_means.append(sum(labels_list) / len(labels_list))

grand_mean = sum(cluster_means) / len(cluster_means)

# Mean square between (MSB)
n_clusters = len(cluster_means)
cluster_sizes = [len(labels_list) for labels_list in day_clusters.values()]
avg_cluster_size = sum(cluster_sizes) / n_clusters
ssb = sum(n * (m - grand_mean)**2 for n, m in zip(cluster_sizes, cluster_means))
msb = ssb / (n_clusters - 1)

# Mean square within (MSW)
ssw = 0
for day, labels_list in day_clusters.items():
    m = sum(labels_list) / len(labels_list)
    ssw += sum((x - m)**2 for x in labels_list)
msw = ssw / (total_calls - n_clusters)

# ICC
icc = (msb - msw) / (msb + (avg_cluster_size - 1) * msw) if msw > 0 else 0
if icc < 0:
    icc = 0

# Design effect and effective N
design_effect = 1 + (avg_cluster_size - 1) * icc
effective_n = total_calls / design_effect

# Sealed era precision
sealed_calls = [(ts, label) for ts, label, is_sealed in all_calls if not is_sealed]
if sealed_calls:
    sealed_hits = sum(1 for _, label in sealed_calls if label == 1)
    sealed_precision = sealed_hits / len(sealed_calls)
else:
    sealed_precision = 0.0

# Adjust precision for 10bp round-trip cost
adjusted_precision = precision - 0.001

print(f"ISSUED={total_calls}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
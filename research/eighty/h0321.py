# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 320
# cycle_index: 43
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
conn.row_factory = sqlite3.Row

# Get all sentiment days with mean_score
sentiment = conn.execute("""
    SELECT symbol_id, day, mean_score
    FROM sentiment_features
    WHERE day >= '2012-01-01'
""").fetchall()

# Get all daily bars for symbol_ids in sentiment, with previous close
bars = conn.execute("""
    SELECT symbol_id, date(ts, 'unixevent day') as day, close,
           LAG(close) OVER (PARTITION BY symbol_id ORDER BY ts) as prev_close
    FROM bars
    WHERE tf = '1d'
""").fetchall()

# Create lookup dictionaries
sentiment_by_sym_day = {}
for row in sentiment:
    key = (row['symbol_id'], row['day'])
    sentiment_by_sym_day[key] = row['mean_score']

bars_by_sym_day = {}
for row in bars:
    key = (row['symbol_id'], row['day'])
    bars_by_sym_day[key] = (row['close'], row['prev_close'])

# Get all 21-day horizon outcomes
outcomes = conn.execute("""
    SELECT symbol_id, date(ts, 'unixevent day') as day, up
    FROM prediction_outcomes
    WHERE horizon = 21
""").fetchall()

outcome_by_sym_day = {}
for row in outcomes:
    key = (row['symbol_id'], row['day'])
    outcome_by_sym_day[key] = row['up']

conn.close()

# Find opportunities
opportunities = []
seen = set()
for (symbol_id, day), mean_score in sentiment_by_sym_day.items():
    prev_key = (symbol_id, prev_day) if (prev_day := ...) else None
    # Need previous day in sentiment
    # Calculate previous day date string
    year, month, day_num = map(int, day.split('-'))
    if day_num > 1:
        prev_day = f"{year}-{month:02d}-{day_num-1:02d}"
    else:
        # Handle month/year boundaries approximately
        if month == 1:
            prev_day = f"{year-1}-12-31"
        else:
            prev_day = f"{year}-{month-1:02d}-28"  # approximation
    
    prev_score = sentiment_by_sym_day.get((symbol_id, prev_day))
    if prev_score is None:
        continue
    
    sentiment_drop = mean_score - prev_score
    if sentiment_drop > -0.5:
        continue
    
    bar_data = bars_by_sym_day.get((symbol_id, day))
    if bar_data is None:
        continue
    close, prev_close = bar_data
    if prev_close is None:
        continue
    if close < prev_close:
        continue
    
    outcome = outcome_by_sym_day.get((symbol_id, day))
    if outcome is None:
        continue
    
    opportunities.append((symbol_id, day, outcome))

if not opportunities:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by day for splitting
opportunities.sort(key=lambda x: x[1])

# Split into train and sealed (most recent 20%)
total = len(opportunities)
sealed_start = int(total * 0.8)
train = opportunities[:sealed_start]
sealed = opportunities[sealed_start:]

# Compute stats
issued = len(opportunities)
base_hits = sum(1 for _, _, up in opportunities if up == 1)
base_rate = base_hits / issued if issued > 0 else 0.0

sealed_hits = sum(1 for _, _, up in sealed if up == 1)
sealed_precision = sealed_hits / len(sealed) if sealed else 0.0

# Distinct days
days_used = set(day for _, day, _ in opportunities)
distinct_days = len(days_used)

# Design effect calculation: cluster by day
day_counts = {}
for _, day, _ in opportunities:
    day_counts[day] = day_counts.get(day, 0) + 1
avg_cluster_size = sum(day_counts.values()) / len(day_counts) if day_counts else 1

# Calculate ICC approximately
day_proportions = []
for day, count in day_counts.items():
    day_hits = sum(1 for _, d, up in opportunities if d == day and up == 1)
    day_proportions.append(day_hits / count)

if len(day_proportions) > 1:
    mean_prop = sum(day_proportions) / len(day_proportions)
    var_prop = sum((p - mean_prop)**2 for p in day_proportions) / len(day_proportions)
    icc = var_prop / (base_rate * (1 - base_rate)) if base_rate > 0 and base_rate < 1 else 0
else:
    icc = 0

deff = 1 + (avg_cluster_size - 1) * icc
effective_n = issued / deff if deff > 0 else issued

print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={total}")
print(f"PRECISION={base_rate:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 362
# cycle_index: 30
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import datetime

DB_PATH = 'data/signaldeck.db'
HORIZON = 21
MIN_SENTIMENT_DAYS = 100

conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
cursor = conn.cursor()

# Get all symbol_ids with sufficient sentiment data
cursor.execute("""
    SELECT symbol_id, COUNT(DISTINCT day) as days
    FROM sentiment_features
    GROUP BY symbol_id
    HAVING days >= ?
""", (MIN_SENTIMENT_DAYS,))
valid_symbols = {row[0] for row in cursor.fetchall()}

if not valid_symbols:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Get sentiment data for valid symbols
cursor.execute("""
    SELECT symbol_id, day, mean_score
    FROM sentiment_features
    WHERE symbol_id IN ({})
    ORDER BY symbol_id, day
""".format(','.join('?' * len(valid_symbols))), list(valid_symbols))
sentiment_rows = cursor.fetchall()

# Build sentiment dict: symbol_id -> list of (day, score)
sentiment_by_symbol = {}
for symbol_id, day, score in sentiment_rows:
    if symbol_id not in sentiment_by_symbol:
        sentiment_by_symbol[symbol_id] = []
    sentiment_by_symbol[symbol_id].append((day, score))

# Get daily close prices
cursor.execute("""
    SELECT symbol_id, ts, close
    FROM bars
    WHERE tf = '1d' AND symbol_id IN ({})
    ORDER BY symbol_id, ts
""".format(','.join('?' * len(valid_symbols))), list(valid_symbols))
price_rows = cursor.fetchall()

# Build price dict: symbol_id -> list of (date_str, close)
price_by_symbol = {}
for symbol_id, ts, close in price_rows:
    date_str = datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
    if symbol_id not in price_by_symbol:
        price_by_symbol[symbol_id] = []
    price_by_symbol[symbol_id].append((date_str, close))

# Get prediction outcomes for horizon=21
cursor.execute("""
    SELECT symbol_id, ts, up
    FROM prediction_outcomes
    WHERE horizon = ?
    AND symbol_id IN ({})
""".format(','.join('?' * len(valid_symbols))), [HORIZON] + list(valid_symbols))
outcome_rows = cursor.fetchall()

# Build outcome dict: symbol_id -> list of (date_str, up)
outcome_by_symbol = {}
for symbol_id, ts, up in outcome_rows:
    date_str = datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
    if symbol_id not in outcome_by_symbol:
        outcome_by_symbol[symbol_id] = []
    outcome_by_symbol[symbol_id].append((date_str, up))

# Generate signals
signals = []
for symbol_id in valid_symbols:
    if symbol_id not in sentiment_by_symbol or symbol_id not in price_by_symbol:
        continue
    if symbol_id not in outcome_by_symbol:
        continue
        
    sentiment_data = sentiment_by_symbol[symbol_id]
    price_data = price_by_symbol[symbol_id]
    outcome_data = outcome_by_symbol[symbol_id]
    
    # Build lookup dicts
    sentiment_lookup = {day: score for day, score in sentiment_data}
    price_lookup = {day: close for day, close in price_data}
    outcome_lookup = {day: up for day, up in outcome_data}
    
    # Get sorted dates
    all_dates = sorted(set(sentiment_lookup.keys()) & set(price_lookup.keys()))
    
    if len(all_dates) < MIN_SENTIMENT_DAYS + 5:
        continue
    
    # Compute 5-day moving averages and returns
    for i in range(5, len(all_dates)):
        current_date = all_dates[i]
        
        # Check if we have enough sentiment history
        recent_dates = all_dates[i-4:i+1]
        if len(recent_dates) < 5:
            continue
        
        # Compute 5-day sentiment MA
        sentiment_scores = [sentiment_lookup[d] for d in recent_dates if d in sentiment_lookup]
        if len(sentiment_scores) < 5:
            continue
        ma_current = sum(sentiment_scores) / len(sentiment_scores)
        
        # Check previous day MA
        if i < 6:
            continue
        prev_dates = all_dates[i-5:i]
        prev_sentiment = [sentiment_lookup[d] for d in prev_dates if d in sentiment_lookup]
        if len(prev_sentiment) < 5:
            continue
        ma_prev = sum(prev_sentiment) / len(prev_sentiment)
        
        # Check conditions
        if not (ma_current > 0.5 and ma_prev <= -0.5):
            continue
        
        # Check 60-day window
        if i < 65:
            continue
        window_dates = all_dates[i-64:i]
        window_ma_values = []
        for j in range(4, len(window_dates)):
            window_slice = window_dates[j-4:j+1]
            window_scores = [sentiment_lookup[d] for d in window_slice if d in sentiment_lookup]
            if len(window_scores) >= 5:
                window_ma = sum(window_scores) / len(window_scores)
                window_ma_values.append(window_ma)
        
        if not window_ma_values or any(ma > 0.5 for ma in window_ma_values):
            continue
        
        # Check 5-day return
        if current_date not in price_lookup or all_dates[i-5] not in price_lookup:
            continue
        current_close = price_lookup[current_date]
        prev_close = price_lookup[all_dates[i-5]]
        ret_5d = (current_close - prev_close) / prev_close
        
        if ret_5d >= 0:
            continue
        
        # Check outcome
        if current_date in outcome_lookup:
            up = outcome_lookup[current_date]
            signals.append((symbol_id, current_date, up))

if not signals:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Split into train and sealed (most recent 20%)
signals.sort(key=lambda x: x[1])
split_idx = int(len(signals) * 0.8)
train_signals = signals[:split_idx]
sealed_signals = signals[split_idx:]

# Calculate metrics for train set
issued = len(train_signals)
opportunities = len(signals)
if issued == 0:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

hits = sum(1 for _, _, up in train_signals if up == 1)
precision = hits / issued
base_rate = sum(1 for _, _, up in train_signals if up == 1) / issued

# Count distinct days
distinct_days = len(set(date for _, date, _ in train_signals))

# Calculate design effect (day clustering)
from collections import defaultdict
day_counts = defaultdict(int)
for _, date, _ in train_signals:
    day_counts[date] += 1

if len(day_counts) == 1:
    design_effect = 1.0
else:
    # Simple intra-class correlation estimate
    n_clusters = len(day_counts)
    avg_cluster_size = issued / n_clusters
    
    # Calculate variance between and within
    overall_mean = base_rate
    
    # Between-cluster variance
    between_var = 0
    for day, count in day_counts.items():
        day_ups = sum(1 for _, d, up in train_signals if d == day and up == 1)
        day_mean = day_ups / count if count > 0 else 0
        between_var += count * ((day_mean - overall_mean) ** 2)
    between_var /= (n_clusters - 1) if n_clusters > 1 else 1
    
    # Within-cluster variance
    within_var = 0
    for day, count in day_counts.items():
        day_ups = sum(1 for _, d, up in train_signals if d == day and up == 1)
        day_mean = day_ups / count if count > 0 else 0
        within_var += day_ups * (1 - day_mean) + (count - day_ups) * day_mean
    within_var /= (issued - n_clusters) if issued > n_clusters else 1
    
    # ICC estimate
    if between_var + within_var > 0:
        icc = between_var / (between_var + within_var)
    else:
        icc = 0
    
    # Design effect
    design_effect = 1 + (avg_cluster_size - 1) * icc

effective_n = issued / design_effect if design_effect > 0 else issued

# SEALED_PRECISION
sealed_issued = len(sealed_signals)
if sealed_issued == 0:
    sealed_precision = 0.0
else:
    sealed_hits = sum(1 for _, _, up in sealed_signals if up == 1)
    sealed_precision = sealed_hits / sealed_issued

# Print results
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.4f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")

conn.close()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 497
# cycle_index: 27
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

conn = sqlite3.connect(DB_PATH, uri=True, timeout=30)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

# Helper: convert day string to epoch
def day_to_epoch(day_str):
    return int(datetime.strptime(day_str, '%Y-%m-%d').timestamp())

# Get all insider purchases (code='P') with their filed dates
cur.execute("""
    SELECT symbol_id, filed_ts 
    FROM insider_trades 
    WHERE code = 'P'
""")
purchases = cur.fetchall()

# Group by (symbol_id, filed_day)
purchases_by_day = defaultdict(list)
for row in purchases:
    symbol_id = row['symbol_id']
    filed_day = datetime.utcfromtimestamp(row['filed_ts']).strftime('%Y-%m-%d')
    purchases_by_day[(symbol_id, filed_day)].append(row['filed_ts'])

# Precompute which symbols have sufficient history (252 days of 1d bars)
cur.execute("SELECT symbol_id, ts FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
bars_by_symbol = defaultdict(list)
for row in cur.fetchall():
    bars_by_symbol[row['symbol_id']].append(row['ts'])

def has_252_days_history(symbol_id, cutoff_ts):
    if symbol_id not in bars_by_symbol:
        return False
    ts_list = bars_by_symbol[symbol_id]
    # Count bars up to cutoff
    count = sum(1 for ts in ts_list if ts <= cutoff_ts)
    return count >= 252

# Precompute sentiment features by symbol and day
cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
sentiment_by_symbol_day = defaultdict(dict)
for row in cur.fetchall():
    sentiment_by_symbol_day[row['symbol_id']][row['day']] = row['mean_score']

# For each sentiment, we need 252-day history to compute percentile
def get_sentiment_percentile(symbol_id, day_str, lookback=252):
    if symbol_id not in sentiment_by_symbol_day:
        return None
    days = sorted(sentiment_by_symbol_day[symbol_id].keys())
    try:
        idx = days.index(day_str)
    except ValueError:
        return None
    if idx < lookback - 1:
        return None
    window_days = days[idx-lookback+1:idx+1]
    scores = [sentiment_by_symbol_day[symbol_id][d] for d in window_days]
    scores.sort()
    percentile_idx = int(0.05 * len(scores))
    if percentile_idx >= len(scores):
        percentile_idx = len(scores) - 1
    return scores[percentile_idx]

# Decision points: each (symbol_id, day) from purchases where conditions met
decision_points = []  # (symbol_id, day_str, epoch, is_issued)
opportunities_count = 0

for (symbol_id, day_str), filed_timestamps in purchases_by_day.items():
    epoch = day_to_epoch(day_str)
    
    # Check history requirement
    if not has_252_days_history(symbol_id, epoch):
        continue
    
    # Get sentiment percentile
    pct = get_sentiment_percentile(symbol_id, day_str)
    if pct is None:
        continue
    
    opportunities_count += 1
    current_score = sentiment_by_symbol_day[symbol_id].get(day_str)
    if current_score is None:
        continue
    
    # Issue if sentiment <= 5th percentile
    is_issued = (current_score <= pct)
    decision_points.append((symbol_id, day_str, epoch, is_issued))

# Get labels from prediction_outcomes for horizon=21
cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=21")
labels = cur.fetchall()
label_map = {}
for row in labels:
    key = (row['symbol_id'], row['ts'])
    label_map[key] = row['up']

# Match decision points to labels
hits = []
issued_days = []
for symbol_id, day_str, epoch, is_issued in decision_points:
    if not is_issued:
        continue
    # Find label: need prediction_outcomes with ts = epoch (21-day horizon)
    up = label_map.get((symbol_id, epoch))
    if up is None:
        continue  # No label available, skip
    hits.append(up)
    issued_days.append(day_str)

# Split into train and sealed (most recent 20% of issued)
if issued_days:
    sorted_indices = sorted(range(len(issued_days)), key=lambda i: issued_days[i])
    sealed_cutoff = int(0.8 * len(sorted_indices))
    train_indices = sorted_indices[:sealed_cutoff]
    sealed_indices = sorted_indices[sealed_cutoff:]
else:
    train_indices = []
    sealed_indices = []

# Compute metrics
issued_count = len(hits)
if issued_count == 0:
    print("INSUFFICIENT=1")
else:
    precision = sum(hits) / issued_count
    base_rate = precision  # predicted class base rate within issued subset
    
    distinct_days = len(set(issued_days))
    
    # Compute design effect for temporal clustering
    # Group issued calls by day
    day_counts = defaultdict(int)
    for day in issued_days:
        day_counts[day] += 1
    # Average cluster size
    avg_cluster = sum(day_counts.values()) / len(day_counts)
    # Estimate ICC for binary outcomes (simplified: use within-day homogeneity)
    # For binary, ICC can be approximated as (p_between - p_within) / (p_between + (k-1)*p_within)
    # Compute p_between (proportion of hits per day)
    day_hit_rates = []
    for day in day_counts:
        day_hits = sum(1 for i, d in enumerate(issued_days) if d == day and hits[i])
        day_hit_rates.append(day_hits / day_counts[day])
    p_between = sum(day_hit_rates) / len(day_hit_rates) if day_hit_rates else 0
    p_within = sum(hits) / issued_count
    if avg_cluster > 1 and p_within < 1:
        icc = max(0, (p_between - p_within) / (p_between + (avg_cluster-1)*p_within))
    else:
        icc = 0
    deff = 1 + (avg_cluster - 1) * icc
    effective_n = issued_count / deff
    
    # Sealed precision
    sealed_hits = [hits[i] for i in sealed_indices]
    sealed_precision = sum(sealed_hits) / len(sealed_hits) if sealed_hits else 0
    
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

conn.close()
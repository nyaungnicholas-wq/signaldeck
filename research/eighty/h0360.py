# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 359
# cycle_index: 27
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import statistics

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
db.row_factory = sqlite3.Row

# Check if we have necessary data
cur = db.execute("SELECT COUNT(*) FROM inst_holdings")
inst_count = cur.fetchone()[0]
cur = db.execute("SELECT COUNT(*) FROM news")
news_count = cur.fetchone()[0]
cur = db.execute("SELECT COUNT(*) FROM bars WHERE tf='1d'")
bars_count = cur.fetchone()[0]
cur = db.execute("SELECT COUNT(DISTINCT symbol_id) FROM prediction_outcomes WHERE horizon='21d' OR horizon=21")
label_count = cur.fetchone()[0]

if inst_count < 2 or news_count < 20 or bars_count < 100 or label_count < 10:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

# Helper functions
def get_inst_holdings(symbol_id):
    cur = db.execute("""
        SELECT period, SUM(shares) as total_shares
        FROM inst_holdings
        WHERE symbol_id=?
        GROUP BY period
        ORDER BY period
    """, (symbol_id,))
    return cur.fetchall()

def get_news_sentiment(symbol_id, end_ts, days=20):
    start_ts = end_ts - days * 86400
    cur = db.execute("""
        SELECT AVG(score) as avg_score
        FROM news
        WHERE symbol_id=? AND ts BETWEEN ? AND ?
    """, (symbol_id, start_ts, end_ts))
    row = cur.fetchone()
    return row[0] if row else None

def get_news_sentiment_history(symbol_id, end_ts):
    cur = db.execute("""
        SELECT ts, score
        FROM news
        WHERE symbol_id=? AND ts <= ?
        ORDER BY ts
    """, (symbol_id, end_ts))
    return cur.fetchall()

def get_price_change(symbol_id, ts):
    start_ts = ts - 20 * 86400
    cur = db.execute("""
        SELECT close FROM bars
        WHERE symbol_id=? AND tf='1d' AND ts <= ?
        ORDER BY ts DESC LIMIT 1
    """, (symbol_id, ts))
    current = cur.fetchone()
    cur = db.execute("""
        SELECT close FROM bars
        WHERE symbol_id=? AND tf='1d' AND ts BETWEEN ? AND ?
        ORDER BY ts DESC LIMIT 1
    """, (symbol_id, start_ts, ts))
    old = cur.fetchone()
    if current and old and old[0] > 0:
        return (current[0] - old[0]) / old[0]
    return None

def get_label(symbol_id, ts):
    cur = db.execute("""
        SELECT up FROM prediction_outcomes
        WHERE symbol_id=? AND (horizon='21d' OR horizon=21) AND ts=?
        LIMIT 1
    """, (symbol_id, ts))
    row = cur.fetchone()
    if row:
        return row[0]
    cur = db.execute("""
        SELECT up FROM prediction_outcomes
        WHERE symbol_id=? AND (horizon='21d' OR horizon=21) AND ts <= ?
        ORDER BY ts DESC LIMIT 1
    """, (symbol_id, ts))
    row = cur.fetchone()
    return row[0] if row else None

# Get all symbols with inst_holdings
cur = db.execute("SELECT DISTINCT symbol_id FROM inst_holdings")
symbols = [r[0] for r in cur.fetchall()]

decision_points = []
for symbol_id in symbols:
    holdings = get_inst_holdings(symbol_id)
    if len(holdings) < 2:
        continue
    
    for i in range(1, len(holdings)):
        prev = holdings[i-1]
        curr = holdings[i]
        if prev[1] > 0:
            qoq = (curr[1] - prev[1]) / prev[1]
            if qoq >= 0.10:
                # Parse period (assume YYYY-MM-DD format)
                try:
                    period_date = datetime.strptime(curr[0], "%Y-%m-%d")
                except:
                    continue
                # Add 45 days lag
                signal_date = period_date + timedelta(days=45)
                signal_ts = int(signal_date.timestamp())
                decision_points.append((symbol_id, signal_ts, qoq))

# Evaluate each decision point
calls = []
for symbol_id, signal_ts, qoq in decision_points:
    # Check news sentiment condition
    avg_sentiment = get_news_sentiment(symbol_id, signal_ts)
    if avg_sentiment is None:
        continue
    
    # Get historical distribution for bottom 20%
    history = get_news_sentiment_history(symbol_id, signal_ts)
    if len(history) < 50:
        continue
    
    # Compute rolling 20-day averages
    scores_by_day = defaultdict(list)
    for ts, score in history:
        day = ts // 86400
        scores_by_day[day].append(score)
    
    daily_avgs = []
    sorted_days = sorted(scores_by_day.keys())
    for i, day in enumerate(sorted_days):
        # Get past 20 days including current
        start_day = day - 20
        recent_days = [d for d in sorted_days if start_day <= d <= day]
        if len(recent_days) >= 10:  # At least half the days have data
            all_scores = []
            for d in recent_days:
                all_scores.extend(scores_by_day[d])
            if all_scores:
                daily_avgs.append((day, statistics.mean(all_scores)))
    
    if len(daily_avgs) < 20:
        continue
    
    # Find current position in distribution
    current_day = signal_ts // 86400
    current_avg = None
    for day, avg in daily_avgs:
        if day == current_day:
            current_avg = avg
            break
    
    if current_avg is None:
        # Find nearest day before
        for day, avg in reversed(daily_avgs):
            if day < current_day:
                current_avg = avg
                break
    
    if current_avg is None:
        continue
    
    # Calculate 20th percentile
    historical_avgs = [avg for day, avg in daily_avgs if day < current_day]
    if len(historical_avgs) < 10:
        continue
    
    historical_avgs.sort()
    percentile_20 = historical_avgs[int(len(historical_avgs) * 0.2)]
    
    # Check if current is in bottom 20%
    if current_avg > percentile_20:
        continue
    
    # Check price change condition
    price_change = get_price_change(symbol_id, signal_ts)
    if price_change is None or price_change >= 0.05:
        continue
    
    # Get label
    label = get_label(symbol_id, signal_ts)
    if label is None:
        continue
    
    calls.append((signal_ts, symbol_id, label))

# Sort by time and split into train/sealed
calls.sort(key=lambda x: x[0])
if not calls:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

split_idx = int(len(calls) * 0.8)
train_calls = calls[:split_idx]
sealed_calls = calls[split_idx:]

# Compute metrics
issued = len(calls)
opportunities = len(decision_points)

# Precision for all issued
if issued > 0:
    hits = sum(1 for _, _, label in calls if label == 1)
    precision = hits / issued
else:
    precision = 0

# Base rate (same as precision for this binary case)
base_rate = precision

# Distinct days
distinct_days = len(set(ts // 86400 for ts, _, _ in calls))

# Design effect and effective N
# Count calls per day
day_counts = defaultdict(int)
for ts, _, _ in calls:
    day = ts // 86400
    day_counts[day] += 1
avg_calls_per_day = issued / distinct_days if distinct_days > 0 else 1
design_effect = 1 + (avg_calls_per_day - 1) * 0.5  # Assume ICC=0.5
if design_effect < 1.01:
    design_effect = 1.01
effective_n = issued / design_effect

# Sealed precision
sealed_issued = len(sealed_calls)
if sealed_issued > 0:
    sealed_hits = sum(1 for _, _, label in sealed_calls if label == 1)
    sealed_precision = sealed_hits / sealed_issued
else:
    sealed_precision = 0

db.close()

print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.2f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")
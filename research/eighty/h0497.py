# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 496
# cycle_index: 26
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
"""Test the 8-K slow-diffusion hypothesis."""

import sqlite3
import datetime
from collections import defaultdict, Counter

# Connect to read-only database
conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

# Get all 8-K filings with their symbols
cur.execute("""
    SELECT f.symbol_id, f.filed_ts, s.symbol
    FROM filings f
    JOIN symbols s ON f.symbol_id = s.id
    WHERE f.form = '8-K'
""")
filings = [(row['symbol_id'], row['symbol'], row['filed_ts']) for row in cur.fetchall()]
conn.close()

if not filings:
    print("INSUFFICIENT=1")
    exit(0)

# Reopen for queries (SQLite connection can only be used in one thread)
conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = conn.cursor()

# Helper: get trading days for a symbol before a timestamp
def get_trading_days(symbol_id, before_ts):
    cur.execute("""
        SELECT DISTINCT ts FROM bars 
        WHERE symbol_id = ? AND tf = '1d' AND ts < ?
        ORDER BY ts
    """, (symbol_id, before_ts))
    return [row[0] for row in cur.fetchall()]

# Helper: get close price at timestamp
def get_close(symbol_id, ts):
    cur.execute("SELECT close FROM bars WHERE symbol_id = ? AND tf = '1d' AND ts = ?", 
                (symbol_id, ts))
    row = cur.fetchone()
    return row[0] if row else None

# Helper: get 252 daily absolute returns for a symbol before a timestamp
def get_252_abs_returns(symbol_id, before_ts):
    cur.execute("""
        SELECT ts, close FROM bars 
        WHERE symbol_id = ? AND tf = '1d' AND ts < ?
        ORDER BY ts DESC LIMIT 253
    """, (symbol_id, before_ts))
    rows = cur.fetchall()
    if len(rows) < 253:
        return None
    closes = [row[1] for row in reversed(rows)]
    returns = [abs((closes[i] - closes[i-1]) / closes[i-1]) for i in range(1, len(closes))]
    return sorted(returns)

# Helper: get next trading day after timestamp
def get_next_trading_day(symbol_id, after_ts):
    cur.execute("""
        SELECT ts FROM bars 
        WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        ORDER BY ts ASC LIMIT 1
    """, (symbol_id, after_ts))
    row = cur.fetchone()
    return row[0] if row else None

# Helper: check if there's another 8-K in prior 5 trading days
def has_recent_8k(symbol_id, trading_days, current_idx):
    if current_idx < 5:
        return False
    prior_days = trading_days[current_idx-5:current_idx]
    if not prior_days:
        return False
    placeholders = ','.join(['?'] * len(prior_days))
    cur.execute(f"""
        SELECT COUNT(*) FROM filings 
        WHERE symbol_id = ? AND form = '8-K' AND filed_ts IN ({placeholders})
    """, [symbol_id] + prior_days)
    return cur.fetchone()[0] > 0

# Helper: get label from prediction_outcomes for 5-day horizon
def get_label(symbol_id, ts):
    cur.execute("""
        SELECT up FROM prediction_outcomes 
        WHERE symbol_id = ? AND horizon = 5 AND ts >= ? AND ts < ?
        LIMIT 1
    """, (symbol_id, ts, ts + 604800))  # 5 days in seconds approx
    row = cur.fetchone()
    return row[0] if row else None

# Process all filings
opportunities = []
issued_calls = []

for symbol_id, symbol, filed_ts in filings:
    # Get all trading days for this symbol
    trading_days = get_trading_days(symbol_id, filed_ts + 31536000)  # +1 year
    if len(trading_days) < 252:
        continue
    
    # Find filing date index (closest trading day <= filed_ts)
    filing_idx = None
    for i, day in enumerate(trading_days):
        if day >= filed_ts:
            break
        filing_idx = i
    
    if filing_idx is None or filing_idx < 252:
        continue
    
    # Check no other 8-K in prior 5 trading days
    if has_recent_8k(symbol_id, trading_days, filing_idx):
        continue
    
    # Get t1 (next trading day after filing)
    t1_idx = filing_idx + 1
    if t1_idx >= len(trading_days):
        continue
    t1_ts = trading_days[t1_idx]
    
    # Get close prices
    filing_close = get_close(symbol_id, trading_days[filing_idx])
    t1_close = get_close(symbol_id, t1_ts)
    if not filing_close or not t1_close or filing_close == 0:
        continue
    
    # Calculate return from filing to t1
    ret = (t1_close - filing_close) / filing_close
    
    # Get 252 daily absolute returns before filing date
    abs_returns = get_252_abs_returns(symbol_id, trading_days[filing_idx])
    if not abs_returns:
        continue
    
    # Calculate current absolute return
    abs_ret = abs(ret)
    
    # Check if in top decile (>= 90th percentile)
    threshold_idx = int(len(abs_returns) * 0.9)
    if abs_ret < abs_returns[threshold_idx]:
        continue
    
    # Record as opportunity
    opportunities.append((symbol_id, symbol, t1_ts, ret))
    
    # Check if we have a label
    label = get_label(symbol_id, t1_ts)
    if label is None:
        continue
    
    # Issue call in direction of return
    prediction = 1 if ret > 0 else 0
    issued_calls.append({
        'symbol_id': symbol_id,
        't1_ts': t1_ts,
        'prediction': prediction,
        'label': label,
        'correct': prediction == label
    })

conn.close()

# Calculate statistics
if not issued_calls:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by t1_ts for split
issued_calls.sort(key=lambda x: x['t1_ts'])

# Split into main and sealed (last 20%)
n_total = len(issued_calls)
n_sealed = max(1, int(n_total * 0.2))
sealed_calls = issued_calls[-n_sealed:]
main_calls = issued_calls[:-n_sealed]

# Calculate base rate (proportion of up=1 in issued calls)
n_up = sum(1 for call in issued_calls if call['label'] == 1)
base_rate = n_up / len(issued_calls)

# Calculate precision for main and sealed
def precision(calls):
    if not calls:
        return 0.0
    return sum(1 for c in calls if c['correct']) / len(calls)

main_precision = precision(main_calls)
sealed_precision = precision(sealed_calls)

# Count distinct days in issued calls
distinct_days = len(set(call['t1_ts'] for call in issued_calls))

# Calculate design effect for clustering by day
day_counts = Counter(call['t1_ts'] for call in issued_calls)
cluster_sizes = list(day_counts.values())
n_clusters = len(cluster_sizes)
mean_cluster_size = len(issued_calls) / n_clusters
var_cluster_size = sum((size - mean_cluster_size) ** 2 for size in cluster_sizes) / (n_clusters - 1) if n_clusters > 1 else 0
design_effect = 1 + (var_cluster_size / mean_cluster_size) if mean_cluster_size > 0 else 1
effective_n = len(issued_calls) / design_effect

# Print results
print(f"ISSUED={len(issued_calls)}")
print(f"OPPORTUNITIES={len(opportunities)}")
print(f"PRECISION={main_precision}")
print(f"BASE_RATE={base_rate}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n}")
print(f"SEALED_PRECISION={sealed_precision}")
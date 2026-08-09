# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 429
# cycle_index: 20
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

db_path = 'file:data/signaldeck.db?mode=ro'
conn = sqlite3.connect(db_path, uri=True)
cursor = conn.cursor()

# Check if required tables exist
required_tables = ['insider_trades', 'macro_series', 'prediction_outcomes', 'symbols']
cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
tables = set(row[0] for row in cursor.fetchall())
for table in required_tables:
    if table not in tables:
        print("INSUFFICIENT=1")
        conn.close()
        exit(0)

# Get insider purchases (code='P') with symbol_id and filed_ts
cursor.execute("""
SELECT symbol_id, filed_ts 
FROM insider_trades 
WHERE code='P'
""")
purchases = cursor.fetchall()
if not purchases:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Get macro series data for VIX and DGS10
cursor.execute("""
SELECT ts, series, value 
FROM macro_series 
WHERE series IN ('VIXCLS', 'DGS10')
""")
macro_data = cursor.fetchall()
macro_by_series = defaultdict(list)
for ts, series, value in macro_data:
    macro_by_series[series].append((ts, value))

# Sort by timestamp
for series in macro_by_series:
    macro_by_series[series].sort(key=lambda x: x[0])

def get_macro_value(series_name, ts, lookback_days=20):
    """Get macro value at or before timestamp ts."""
    if series_name not in macro_by_series:
        return None
    data = macro_by_series[series_name]
    # Binary search for closest value <= ts
    left, right = 0, len(data) - 1
    result = None
    while left <= right:
        mid = (left + right) // 2
        if data[mid][0] <= ts:
            result = data[mid][1]
            left = mid + 1
        else:
            right = mid - 1
    return result

def compute_sma(series_name, ts, window=20):
    """Compute simple moving average for a series."""
    if series_name not in macro_by_series:
        return None
    data = macro_by_series[series_name]
    # Get all values up to ts
    values = [val for t, val in data if t <= ts]
    if len(values) < window:
        return None
    return sum(values[-window:]) / window

def compute_change(series_name, ts, days=20):
    """Compute change over days for a series."""
    if series_name not in macro_by_series:
        return None
    data = macro_by_series[series_name]
    values = [(t, val) for t, val in data if t <= ts]
    if len(values) < days + 1:
        return None
    current = values[-1][1]
    past = values[-(days+1)][1]
    return current - past

# Process each purchase to check conditions and get labels
opportunities = []
issued_calls = []

for symbol_id, filed_ts in purchases:
    # Check VIX condition
    vix_sma20 = compute_sma('VIXCLS', filed_ts, 20)
    vix_current = get_macro_value('VIXCLS', filed_ts)
    if vix_current is None or vix_sma20 is None or vix_current <= vix_sma20:
        continue
    
    # Check 10-year yield condition
    dgs10_change = compute_change('DGS10', filed_ts, 20)
    if dgs10_change is None or dgs10_change < 0.10:  # 10 basis points = 0.10%
        continue
    
    # Get label from prediction_outcomes
    cursor.execute("""
    SELECT up FROM prediction_outcomes 
    WHERE symbol_id=? AND horizon=21 AND ts<=?
    ORDER BY ts DESC LIMIT 1
    """, (symbol_id, filed_ts))
    result = cursor.fetchone()
    if not result:
        continue
    
    up = result[0]
    # filed_ts is unix epoch - convert to date for day counting
    decision_date = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
    opportunities.append({
        'symbol_id': symbol_id,
        'ts': filed_ts,
        'date': decision_date,
        'up': up
    })
    issued_calls.append({
        'symbol_id': symbol_id,
        'ts': filed_ts,
        'date': decision_date,
        'up': up
    })

conn.close()

if not issued_calls:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by timestamp
issued_calls.sort(key=lambda x: x['ts'])

# Split into train and sealed (most recent 20%)
n = len(issued_calls)
train_size = int(n * 0.8)
train_calls = issued_calls[:train_size]
sealed_calls = issued_calls[train_size:]

# Compute metrics
def compute_metrics(calls):
    if not calls:
        return 0, 0, 0, 0, 0, 0
    
    hits = sum(1 for call in calls if call['up'])
    precision = hits / len(calls)
    
    # Base rate of up within issued calls
    base_rate = precision
    
    # Distinct days
    days = set(call['date'] for call in calls)
    distinct_days = len(days)
    
    # Design effect for effective sample size
    # Cluster by date
    date_clusters = defaultdict(list)
    for call in calls:
        date_clusters[call['date']].append(call)
    
    total_clusters = len(date_clusters)
    if total_clusters == 0:
        design_effect = 1.0
    else:
        # Average cluster size
        avg_cluster_size = len(calls) / total_clusters
        # Intracluster correlation approximation using variance of outcomes per cluster
        cluster_means = []
        for date, cluster_calls in date_clusters.items():
            cluster_means.append(sum(1 for c in cluster_calls if c['up']) / len(cluster_calls))
        
        overall_mean = sum(1 for c in calls if c['up']) / len(calls)
        cluster_variance = sum((m - overall_mean) ** 2 for m in cluster_means) / (total_clusters - 1) if total_clusters > 1 else 0
        
        # Approximate ICC using variance components
        outcome_variance = overall_mean * (1 - overall_mean)
        if outcome_variance > 0:
            # ICC approximation: ratio of between-cluster variance to total variance
            icc = cluster_variance / (cluster_variance + outcome_variance)
        else:
            icc = 0
        
        design_effect = 1 + (avg_cluster_size - 1) * icc
    
    effective_n = len(calls) / design_effect
    
    return len(calls), precision, base_rate, distinct_days, effective_n

train_issued, train_precision, train_base_rate, train_days, train_effective_n = compute_metrics(train_calls)
sealed_issued, sealed_precision, sealed_base_rate, sealed_days, sealed_effective_n = compute_metrics(sealed_calls)

print(f"ISSUED={train_issued}")
print(f"OPPORTUNITIES={len(opportunities)}")
print(f"PRECISION={train_precision:.4f}")
print(f"BASE_RATE={train_base_rate:.4f}")
print(f"DISTINCT_DAYS={train_days}")
print(f"EFFECTIVE_N={train_effective_n:.4f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")
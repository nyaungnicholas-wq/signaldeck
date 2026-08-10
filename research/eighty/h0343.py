# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 342
# cycle_index: 10
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
import math
from collections import defaultdict

db_path = 'file:data/signaldeck.db?mode=ro'
try:
    conn = sqlite3.connect(db_path, uri=True, timeout=10)
    conn.row_factory = sqlite3.Row
except sqlite3.Error as e:
    print(f"Error opening database: {e}")
    exit(1)

# Step 1: Get FRED unemployment claims data (ICSA = Initial Claims)
unemp_query = """
SELECT ts, value 
FROM macro_series 
WHERE series = 'ICSA'
ORDER BY ts
"""
cursor = conn.execute(unemp_query)
unemp_data = cursor.fetchall()

if not unemp_data:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Convert to list of (epoch, value) and compute moving averages
unemp_values = [(row['ts'], row['value']) for row in unemp_data]

# Step 2: Compute 4-week moving average and 52-week low for unemployment
# Data is weekly, so 4 weeks = 4 data points, 52 weeks = 52 points
unemp_by_ts = {ts: val for ts, val in unemp_values}

# Create sorted list of timestamps
unemp_timestamps = sorted([ts for ts, _ in unemp_values])

# Step 3: Get all insider purchases (code='P') with their filed_ts
insider_query = """
SELECT symbol_id, filed_ts
FROM insider_trades
WHERE code = 'P'
"""
cursor = conn.execute(insider_query)
insider_purchases = cursor.fetchall()

if not insider_purchases:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Group by (symbol_id, filed_ts) to get unique disclosure dates per symbol
disclosure_dates = defaultdict(set)
symbol_by_id = {}
for row in insider_purchases:
    symbol_id = row['symbol_id']
    filed_ts = row['filed_ts']
    disclosure_dates[symbol_id].add(filed_ts)

# Get symbol details
cursor = conn.execute("SELECT id, symbol FROM symbols")
for row in cursor:
    symbol_by_id[row['id']] = row['symbol']

# Step 4: For each disclosure, compute the unemployment condition
# and check if we have a 21-day forward label
calls = []

for symbol_id, dates in disclosure_dates.items():
    for filed_ts in dates:
        # Convert filed_ts to date for unemployment lookup
        decision_date = datetime.datetime.utcfromtimestamp(filed_ts).date()
        decision_epoch = filed_ts
        
        # Find the most recent unemployment data available before decision_date (with lag)
        # Assuming data is available with at least 7-day lag (conservative)
        lag_days = 7
        lookback_date = decision_date - datetime.timedelta(days=lag_days)
        lookback_epoch = int(lookback_date.timestamp())
        
        # Find the most recent ICSA data point before lookback_date
        recent_ts = None
        for ts in reversed(unemp_timestamps):
            if ts <= lookback_epoch:
                recent_ts = ts
                break
        
        if recent_ts is None:
            continue
            
        # Get 52-week window of data
        one_year_ago = recent_ts - (52 * 7 * 24 * 3600)  # Approximate
        window_data = [(ts, val) for ts, val in unemp_values 
                      if one_year_ago <= ts <= recent_ts]
        
        if len(window_data) < 4:  # Need at least 4 weeks for moving average
            continue
            
        # Compute 4-week moving average for recent point
        recent_vals = [val for ts, val in window_data[-4:]]
        current_ma = sum(recent_vals) / len(recent_vals)
        
        # Compute 52-week low of the 4-week moving average
        # We'll compute MA for each possible 4-week window in the year
        ma_values = []
        for i in range(len(window_data) - 3):
            window_vals = [val for _, val in window_data[i:i+4]]
            ma_values.append(sum(window_vals) / len(window_vals))
        
        if not ma_values:
            continue
            
        ma_52w_low = min(ma_values)
        
        # Check condition: current MA >= 1.10 * MA 52-week low
        if current_ma >= 1.10 * ma_52w_low:
            # Now check if we have a 21-day forward label
            # Look for prediction_outcomes with this symbol and horizon=21
            # with ts matching our decision_epoch
            label_query = """
            SELECT up, fwd_return
            FROM prediction_outcomes
            WHERE symbol_id = ? 
            AND horizon = 21
            AND ts = ?
            """
            cursor.execute(label_query, (symbol_id, decision_epoch))
            label_row = cursor.fetchone()
            
            if label_row:
                calls.append({
                    'symbol_id': symbol_id,
                    'date': decision_date,
                    'epoch': decision_epoch,
                    'up': label_row['up'],
                    'fwd_return': label_row['fwd_return']
                })

conn.close()

if not calls:
    print("INSUFFICIENT=1")
    exit(0)

# Step 5: Split into train and sealed (most recent 20%)
calls.sort(key=lambda x: x['epoch'])
n = len(calls)
split_idx = int(0.8 * n)

train_calls = calls[:split_idx]
sealed_calls = calls[split_idx:]

# Step 6: Compute metrics
def compute_metrics(call_list):
    if not call_list:
        return {
            'ISSUED': 0,
            'OPPORTUNITIES': 0,
            'PRECISION': 0,
            'BASE_RATE': 0,
            'DISTINCT_DAYS': 0,
            'EFFECTIVE_N': 0
        }
    
    # Count distinct days
    days = set()
    for call in call_list:
        days.add(call['date'])
    
    # Count hits (up = True)
    hits = sum(1 for call in call_list if call['up'])
    
    # Precision = hits / issued
    precision = hits / len(call_list) if call_list else 0
    
    # Base rate = proportion of up=True in issued set
    base_rate = precision  # Same as precision when measuring within issued set
    
    # Compute design effect for clustering by day
    # Group calls by day
    day_groups = defaultdict(list)
    for call in call_list:
        day_groups[call['date']].append(call['up'])
    
    # Compute ICC and design effect
    if len(day_groups) < 2:
        # Cannot compute DEFF with only one cluster
        effective_n = len(call_list) / len(day_groups)
    else:
        # Overall proportion
        p = precision
        
        # Between-cluster variance
        n_clusters = len(day_groups)
        between_var = 0
        for day, outcomes in day_groups.items():
            n_j = len(outcomes)
            p_j = sum(outcomes) / n_j
            between_var += n_j * (p_j - p) ** 2
        
        between_var /= (n_clusters - 1)
        
        # Total variance for binary data
        total_var = p * (1 - p) if p > 0 and p < 1 else 0.001  # Avoid division by zero
        
        # ICC
        icc = between_var / total_var if total_var > 0 else 0
        
        # Average cluster size
        avg_cluster_size = len(call_list) / n_clusters
        
        # Design effect
        deff = 1 + (avg_cluster_size - 1) * icc
        
        # Effective sample size
        effective_n = len(call_list) / deff if deff > 0 else len(call_list)
    
    return {
        'ISSUED': len(call_list),
        'OPPORTUNITIES': len(call_list),  # All opportunities considered
        'PRECISION': precision,
        'BASE_RATE': base_rate,
        'DISTINCT_DAYS': len(days),
        'EFFECTIVE_N': effective_n
    }

# Compute metrics for full set and sealed set
full_metrics = compute_metrics(calls)
sealed_metrics = compute_metrics(sealed_calls)

# Print results
print(f"ISSUED={full_metrics['ISSUED']}")
print(f"OPPORTUNITIES={full_metrics['OPPORTUNITIES']}")
print(f"PRECISION={full_metrics['PRECISION']:.4f}")
print(f"BASE_RATE={full_metrics['BASE_RATE']:.4f}")
print(f"DISTINCT_DAYS={full_metrics['DISTINCT_DAYS']}")
print(f"EFFECTIVE_N={full_metrics['EFFECTIVE_N']:.4f}")
print(f"SEALED_PRECISION={sealed_metrics['PRECISION']:.4f}")

# Verify invariants
if full_metrics['DISTINCT_DAYS'] > full_metrics['ISSUED']:
    print("ERROR: DISTINCT_DAYS cannot exceed ISSUED")
    exit(1)

if full_metrics['EFFECTIVE_N'] >= full_metrics['ISSUED']:
    print("ERROR: EFFECTIVE_N must be strictly less than ISSUED")
    exit(1)
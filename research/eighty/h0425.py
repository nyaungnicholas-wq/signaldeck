# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 424
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict

db_path = 'file:data/signaldeck.db?mode=ro'
conn = sqlite3.connect(db_path, uri=True)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

# Get all candidates with horizon=21 and known outcome
cur.execute("""
    SELECT symbol_id, ts, up, fwd_return
    FROM prediction_outcomes
    WHERE horizon=21 AND up IS NOT NULL
""")
rows = cur.fetchall()
if not rows:
    print("INSUFFICIENT=1")
    exit(0)

# Precompute EPS data (QoQ > 5% growth)
eps_by_symbol = defaultdict(dict)
cur.execute("""
    SELECT symbol_id, as_of, fetched_at, value
    FROM fundamentals
    WHERE metric='EPS'
""")
for r in cur.fetchall():
    sym = r['symbol_id']
    eps_by_symbol[sym][r['as_of']] = {'value': r['value'], 'fetched_at': r['fetched_at']}

# Precompute public float data (YoY < -10% change)
float_by_symbol = defaultdict(dict)
cur.execute("""
    SELECT symbol_id, as_of, fetched_at, value
    FROM fundamentals
    WHERE metric='EntityPublicFloat'
""")
for r in cur.fetchall():
    sym = r['symbol_id']
    float_by_symbol[sym][r['as_of']] = {'value': r['value'], 'fetched_at': r['fetched_at']}

# Precompute insider trades (code='P' for purchase, filed_ts only)
insider_by_symbol = defaultdict(list)
cur.execute("""
    SELECT symbol_id, filed_ts
    FROM insider_trades
    WHERE code='P'
""")
for r in cur.fetchall():
    insider_by_symbol[r['symbol_id']].append(r['filed_ts'])

# Precompute daily volume (for 20-day avg)
cur.execute("""
    SELECT symbol_id, ts, volume
    FROM bars
    WHERE tf='1d'
""")
vol_data = defaultdict(list)
for r in cur.fetchall():
    vol_data[r['symbol_id']].append((r['ts'], r['volume']))

def get_eps_growth(symbol_id, ts):
    """Check EPS QoQ growth > 5% as of ts."""
    eps = eps_by_symbol.get(symbol_id, {})
    valid = []
    for as_of, data in eps.items():
        if data['fetched_at'] <= ts and data['value'] is not None:
            valid.append((as_of, data['value']))
    if len(valid) < 2:
        return False
    valid.sort(key=lambda x: x[0], reverse=True)
    current, previous = valid[0][1], valid[1][1]
    if previous == 0:
        return False
    return (current - previous) / abs(previous) > 0.05

def get_float_change(symbol_id, ts):
    """Check public float YoY change < -10% as of ts."""
    flt = float_by_symbol.get(symbol_id, {})
    valid = []
    for as_of, data in flt.items():
        if data['fetched_at'] <= ts and data['value'] is not None:
            valid.append((as_of, data['value']))
    if len(valid) < 2:
        return False
    valid.sort(key=lambda x: x[0], reverse=True)
    current_as_of, current_val = valid[0]
    # Find an older one at least 365 days earlier
    target = current_as_of - 365*86400
    older_val = None
    for as_of, val in valid[1:]:
        if as_of <= target:
            older_val = val
            break
    if older_val is None or older_val == 0:
        return False
    return (current_val - older_val) / abs(older_val) < -0.10

def has_recent_purchase(symbol_id, ts):
    """Check for insider purchase filed in past 30 days."""
    purchases = insider_by_symbol.get(symbol_id, [])
    cutoff = ts - 30*86400
    return any(p >= cutoff and p <= ts for p in purchases)

def avg_volume_20d(symbol_id, ts):
    """Get 20-day average volume ending at ts."""
    vols = vol_data.get(symbol_id, [])
    cutoff = ts - 20*86400
    recent = [v for t, v in vols if cutoff <= t <= ts]
    if len(recent) < 20:
        return 0
    return sum(recent) / len(recent)

# Process each candidate
issued = []
opportunities = 0
day_counts = defaultdict(int)

for r in rows:
    sym, ts, up, fwd_return = r['symbol_id'], r['ts'], r['up'], r['fwd_return']
    opportunities += 1
    
    # Check all entry conditions
    if not (get_eps_growth(sym, ts) and
            get_float_change(sym, ts) and
            has_recent_purchase(sym, ts)):
        continue
    
    # Check volume threshold
    if avg_volume_20d(sym, ts) < 100000:
        continue
    
    day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
    day_counts[day] += 1
    issued.append({
        'symbol_id': sym,
        'ts': ts,
        'day': day,
        'up': up,
        'fwd_return': fwd_return
    })

if not issued:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by ts for splitting
issued.sort(key=lambda x: x['ts'])
n = len(issued)
split_idx = int(n * 0.8)

in_sample = issued[:split_idx]
sealed = issued[split_idx:]

# Calculate metrics
total_issued = n
hits_in_sample = sum(1 for x in in_sample if x['up'])
precision_in_sample = hits_in_sample / split_idx if split_idx > 0 else 0

hits_sealed = sum(1 for x in sealed if x['up'])
precision_sealed = hits_sealed / len(sealed) if sealed else 0

base_rate = precision_in_sample  # predicted class within issued subset

distinct_days = len(day_counts)

# Calculate design effect (clustering by day)
day_groups = defaultdict(list)
for item in issued:
    day_groups[item['day']].append(1)
k = len(day_groups)
m_bar = n / k
# Between-day variance of proportions (simplified)
p_bar = precision_in_sample
day_props = [sum(day_groups[d])/len(day_groups[d]) for d in day_groups]
var_between = sum((p - p_bar)**2 for p in day_props) / k
var_within = p_bar * (1 - p_bar)
if var_within > 0:
    icc = var_between / var_within
    deff = 1 + (m_bar - 1) * icc
else:
    deff = 1.0
effective_n = n / deff

# Output
print(f"ISSUED={total_issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision_in_sample:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.4f}")
print(f"SEALED_PRECISION={precision_sealed:.4f}")

conn.close()
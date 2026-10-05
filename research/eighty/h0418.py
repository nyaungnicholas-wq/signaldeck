# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 417
# cycle_index: 8
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

conn = sqlite3.connect(DB_PATH, uri=True)
cursor = conn.cursor()

# Check for 13F data with enough history
cursor.execute("SELECT COUNT(DISTINCT symbol_id) FROM inst_holdings")
n_symbols_13f = cursor.fetchone()[0]

# Check for public float data
cursor.execute("SELECT COUNT(DISTINCT symbol_id) FROM fundamentals WHERE metric='EntityPublicFloat'")
n_symbols_float = cursor.fetchone()[0]

if n_symbols_13f < 10 or n_symbols_float < 10:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Get symbols with daily bars
cursor.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
daily_symbols = {row[0] for row in cursor.fetchall()}

# Get symbols with enough 13F history (at least 9 quarters for 8-quarter change)
cursor.execute("""
    SELECT symbol_id, COUNT(DISTINCT period) as quarter_count
    FROM inst_holdings
    GROUP BY symbol_id
    HAVING quarter_count >= 9
""")
eligible_13f = {row[0] for row in cursor.fetchall()}

# Get symbols with public float data
cursor.execute("""
    SELECT symbol_id 
    FROM fundamentals 
    WHERE metric='EntityPublicFloat'
    GROUP BY symbol_id
""")
eligible_float = {row[0] for row in cursor.fetchall()}

universe = daily_symbols & eligible_13f & eligible_float

if len(universe) < 10:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Preload 13F data grouped by symbol and period
cursor.execute("""
    SELECT symbol_id, period, SUM(shares) as total_shares
    FROM inst_holdings
    WHERE symbol_id IN ({})
    GROUP BY symbol_id, period
    ORDER BY symbol_id, period
""".format(','.join('?' * len(universe))), tuple(universe))

shares_by_symbol_period = defaultdict(dict)
for symbol_id, period, total_shares in cursor.fetchall():
    shares_by_symbol_period[symbol_id][period] = total_shares

# Preload public float data
cursor.execute("""
    SELECT symbol_id, as_of, value, fetched_at
    FROM fundamentals
    WHERE metric='EntityPublicFloat' AND symbol_id IN ({})
    ORDER BY symbol_id, as_of, fetched_at
""".format(','.join('?' * len(universe))), tuple(universe))

float_data = defaultdict(list)
for symbol_id, as_of, value, fetched_at in cursor.fetchall():
    float_data[symbol_id].append((as_of, value, fetched_at))

# Function to get public float value at a decision time
def get_public_float(symbol_id, decision_ts):
    """Get most recent public float before decision_ts"""
    if symbol_id not in float_data:
        return None, None
    
    # Sort by fetched_at (knowability date)
    records = sorted(float_data[symbol_id], key=lambda x: x[2])
    best = None
    for as_of, value, fetched_at in records:
        if fetched_at and fetched_at <= decision_ts:
            best = (as_of, value)
    return best

# Function to get 8-quarter change in institutional ownership
def get_8q_change(symbol_id, decision_ts):
    """Calculate 8-quarter change from most recent quarter available before decision_ts"""
    periods = sorted(shares_by_symbol_period.get(symbol_id, {}).keys())
    if len(periods) < 9:
        return None, None
    
    # Find most recent period available before decision_ts (with 45-day lag for filing)
    most_recent = None
    for period in reversed(periods):
        period_date = datetime.strptime(period, '%Y-%m-%d')
        # Assume filing occurs 45 days after period end
        filing_date = period_date + timedelta(days=45)
        if filing_date.timestamp() <= decision_ts:
            most_recent = period
            break
    
    if most_recent is None:
        return None, None
    
    # Find period 8 quarters earlier (2 years)
    most_recent_idx = periods.index(most_recent)
    if most_recent_idx < 8:
        return None, None
    
    earlier_period = periods[most_recent_idx - 8]
    
    current_shares = shares_by_symbol_period[symbol_id][most_recent]
    earlier_shares = shares_by_symbol_period[symbol_id][earlier_period]
    
    if earlier_shares == 0:
        return None, None
    
    change = (current_shares - earlier_shares) / earlier_shares
    return change, most_recent

# Collect all decision points from daily bars
cursor.execute("""
    SELECT symbol_id, ts
    FROM bars
    WHERE tf='1d' AND symbol_id IN ({})
    ORDER BY ts
""".format(','.join('?' * len(universe))), tuple(universe))

all_decision_points = cursor.fetchall()

# Calculate base rate from all prediction outcomes (21-day horizon)
cursor.execute("""
    SELECT up, COUNT(*) 
    FROM prediction_outcomes 
    WHERE horizon=21
    GROUP BY up
""")
outcome_counts = dict(cursor.fetchall())
total_outcomes = sum(outcome_counts.values())
base_rate = outcome_counts.get(1, 0) / total_outcomes if total_outcomes > 0 else 0

# Process opportunities
opportunities = []  # List of (symbol_id, ts, up_outcome)
symbol_ts_map = defaultdict(list)

for symbol_id, ts in all_decision_points:
    symbol_ts_map[symbol_id].append(ts)

# For each symbol, generate opportunities
for symbol_id in universe:
    ts_list = symbol_ts_map.get(symbol_id, [])
    if not ts_list:
        continue
    
    for ts in ts_list:
        # Check 13F condition
        change_8q, recent_period = get_8q_change(symbol_id, ts)
        if change_8q is None:
            continue
        
        # Check public float condition
        float_info = get_public_float(symbol_id, ts)
        if float_info is None:
            continue
        
        float_as_of, current_float = float_info
        
        # Get public float from ~1 year ago (4 quarters back)
        periods = sorted(shares_by_symbol_period.get(symbol_id, {}).keys())
        recent_period_idx = periods.index(recent_period) if recent_period in periods else -1
        if recent_period_idx >= 4:
            year_ago_period = periods[recent_period_idx - 4]
            # Get public float closest to that period's date
            year_ago_float_info = get_public_float(symbol_id, 
                datetime.strptime(year_ago_period, '%Y-%m-%d').timestamp() + timedelta(days=100).timestamp())
            if year_ago_float_info:
                _, year_ago_float = year_ago_float_info
                if year_ago_float and year_ago_float > 0:
                    float_change = (current_float - year_ago_float) / year_ago_float
                    if float_change <= -0.15:
                        # Get outcome from prediction_outcomes
                        cursor.execute("""
                            SELECT up FROM prediction_outcomes
                            WHERE symbol_id=? AND ts=? AND horizon=21
                            LIMIT 1
                        """, (symbol_id, ts))
                        result = cursor.fetchone()
                        if result:
                            opportunities.append((symbol_id, ts, result[0]))

conn.close()

if not opportunities:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by timestamp and split into development (80%) and sealed (20%)
opportunities.sort(key=lambda x: x[1])
n_total = len(opportunities)
n_dev = int(n_total * 0.8)

dev_opportunities = opportunities[:n_dev]
sealed_opportunities = opportunities[n_dev:]

if not dev_opportunities or not sealed_opportunities:
    print("INSUFFICIENT=1")
    exit(0)

# Calculate 8-quarter change distribution for top decile threshold
changes = []
for symbol_id, ts, _ in dev_opportunities:
    change, _ = get_8q_change(symbol_id, ts)
    if change is not None:
        changes.append(change)

if not changes:
    print("INSUFFICIENT=1")
    exit(0)

# Calculate 90th percentile
changes_sorted = sorted(changes)
idx = int(0.9 * len(changes_sorted))
top_decile_threshold = changes_sorted[idx]

# Filter for issued calls in development set
dev_issued = []
day_counts = defaultdict(int)

for symbol_id, ts, up in dev_opportunities:
    change, _ = get_8q_change(symbol_id, ts)
    if change is not None and change >= top_decile_threshold:
        dev_issued.append((symbol_id, ts, up))
        day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        day_counts[day] += 1

if not dev_issued:
    print("INSUFFICIENT=1")
    exit(0)

# Filter for issued calls in sealed set using same threshold
sealed_issued = []
for symbol_id, ts, up in sealed_opportunities:
    change, _ = get_8q_change(symbol_id, ts)
    if change is not None and change >= top_decile_threshold:
        sealed_issued.append((symbol_id, ts, up))

# Calculate metrics
hits_dev = sum(1 for _, _, up in dev_issued if up == 1)
hits_sealed = sum(1 for _, _, up in sealed_issued if up == 1)

precision_dev = hits_dev / len(dev_issued) if dev_issued else 0
precision_sealed = hits_sealed / len(sealed_issued) if sealed_issued else 0

# Base rate in issued subset
base_rate_dev = hits_dev / len(dev_issued) if dev_issued else 0

# Distinct days in issued calls
distinct_days = len(day_counts)

# Calculate design effect (cluster by day)
n_clusters = len(day_counts)
if n_clusters > 0:
    avg_cluster_size = len(dev_issued) / n_clusters
    # Calculate ICC from binomial data
    total = len(dev_issued)
    if total > 1:
        p = hits_dev / total
        variance = p * (1 - p)
        
        # Within-cluster variance
        sum_within = 0
        for day, count in day_counts.items():
            # Get hits in this cluster
            cluster_hits = sum(1 for s, t, u in dev_iced if datetime.utcfromtimestamp(t).strftime('%Y-%m-%d') == day and u == 1)
            if count > 1:
                cluster_var = cluster_hits / count * (1 - cluster_hits / count)
                sum_within += (count - 1) * cluster_var
        
        within_var = sum_within / (total - n_clusters) if total > n_clusters else 0
        between_var = variance - within_var if variance > within_var else 0
        icc = between_var / variance if variance > 0 else 0
        design_effect = 1 + (avg_cluster_size - 1) * icc
    else:
        design_effect = 1
else:
    design_effect = 1

effective_n = len(dev_issued) / design_effect if design_effect > 0 else 0

# Output results
print(f"ISSUED={len(dev_issued)}")
print(f"OPPORTUNITIES={len(dev_opportunities)}")
print(f"PRECISION={precision_dev:.4f}")
print(f"BASE_RATE={base_rate_dev:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.4f}")
print(f"SEALED_PRECISION={precision_sealed:.4f}")
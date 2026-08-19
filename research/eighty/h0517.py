# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 516
# cycle_index: 46
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
db.row_factory = sqlite3.Row

# Get insider purchases with expectancy scores
trades = db.execute("""
    SELECT i.symbol_id, i.filed_ts, i.code
    FROM insider_trades i
    WHERE i.code = 'P'
""").fetchall()

if not trades:
    print("INSUFFICIENT=1")
    exit(0)

opportunities = []
for trade in trades:
    sym_id, file_ts, _ = trade
    
    # Get expectancy score for this symbol at horizon 21, as of file_ts
    score_row = db.execute("""
        SELECT mean_fwd
        FROM expectancy
        WHERE symbol_id = ? AND horizon = 21 AND updated_at <= ?
        ORDER BY updated_at DESC LIMIT 1
    """, (sym_id, file_ts)).fetchone()
    if not score_row:
        continue
    score = score_row[0]
    
    # Check at least 20 trading days in past 60 days (daily bars)
    bar_count = db.execute("""
        SELECT COUNT(*)
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        AND ts BETWEEN ? AND ?
    """, (sym_id, file_ts - 60*86400, file_ts)).fetchone()[0]
    if bar_count < 20:
        continue
    
    # Get base price on or before file_ts
    base_bar = db.execute("""
        SELECT ts, close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC LIMIT 1
    """, (sym_id, file_ts)).fetchone()
    if not base_bar:
        continue
    
    base_ts = base_bar[0]
    base_close = base_bar[1]
    
    # Get price 21 trading days later
    future_bar = db.execute("""
        SELECT close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        ORDER BY ts ASC LIMIT 21
    """, (sym_id, base_ts)).fetchall()
    
    if len(future_bar) < 21:
        continue
    
    fwd_close = future_bar[-1][0]
    up = 1 if fwd_close > base_close else 0
    fwd_return = (fwd_close - base_close) / base_close
    
    opportunities.append({
        'symbol_id': sym_id,
        'filed_ts': file_ts,
        'score': score,
        'up': up,
        'fwd_return': fwd_return,
        'base_ts': base_ts
    })

if not opportunities:
    print("INSUFFICIENT=1")
    exit(0)

# Group by date to compute terciles per day
by_date = defaultdict(list)
for opp in opportunities:
    date = opp['filed_ts'] // 86400
    by_date[date].append(opp)

issued = []
for date, day_opps in by_date.items():
    scores = [o['score'] for o in day_opps]
    scores_sorted = sorted(scores)
    n = len(scores_sorted)
    tercile_idx = n // 3
    low_tercile = scores_sorted[tercile_idx - 1] if tercile_idx > 0 else scores_sorted[0]
    
    for opp in day_opps:
        if opp['score'] <= low_tercile:
            issued.append(opp)

if not issued:
    print("INSUFFICIENT=1")
    exit(0)

# Sort all opportunities by time for train/test split
opportunities.sort(key=lambda x: x['filed_ts'])
n_total = len(opportunities)
cutoff_idx = int(n_total * 0.8)
sealed_cutoff_ts = opportunities[cutoff_idx]['filed_ts']

# Split issued calls
train_issued = [o for o in issued if o['filed_ts'] < sealed_cutoff_ts]
sealed_issued = [o for o in issued if o['filed_ts'] >= sealed_cutoff_ts]

# Calculate metrics
total_opportunities = len(opportunities)
total_issued = len(issued)
hits = sum(1 for o in issued if o['up'] == 1)
precision = hits / total_issued

# Base rate within opportunities
base_rate = sum(1 for o in opportunities if o['up'] == 1) / total_opportunities

# Distinct days among issued
issued_dates = set()
for o in issued:
    issued_dates.add(o['filed_ts'] // 86400)
distinct_days = len(issued_dates)

# Calculate design effect and effective N
# Cluster by day
by_day = defaultdict(list)
for o in issued:
    by_day[o['filed_ts'] // 86400].append(o['up'])

# Overall mean
overall_p = hits / total_issued
group_means = []
group_variances = []
group_sizes = []
for day_labels in by_day.values():
    m = len(day_labels)
    p = sum(day_labels) / m
    group_means.append(p)
    group_variances.append(p * (1 - p))
    group_sizes.append(m)

# Between-group variance
between_var = sum((p - overall_p)**2 for p in group_means) / len(by_day)
within_var = sum(group_variances) / len(by_day)
avg_cluster_size = sum(group_sizes) / len(by_day)

# ICC and design effect
if (between_var + within_var) > 0:
    icc = between_var / (between_var + within_var)
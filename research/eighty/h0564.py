# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 563
# cycle_index: 21
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
import statistics

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
db.execute("PRAGMA journal_mode=WAL")
cur = db.cursor()

def percentile(sorted_list, p):
    """Return p-th percentile (0-100) of sorted_list"""
    if not sorted_list:
        return 0
    n = len(sorted_list)
    k = (p / 100) * (n - 1)
    f = math.floor(k)
    c = math.ceil(k)
    if f == c:
        return sorted_list[int(k)]
    return sorted_list[f] * (c - k) + sorted_list[c] * (k - f)

# Get universe: symbols with insider trades and daily price data from 2018 onward
cur.execute("""
    SELECT DISTINCT i.symbol_id 
    FROM insider_trades i
    INNER JOIN symbols s ON i.symbol_id = s.id
    WHERE s.market = 'stocks'
""")
universe = {row[0] for row in cur.fetchall()}

# Filter to symbols with daily price data from 2018 onward
cur.execute("""
    SELECT DISTINCT symbol_id 
    FROM bars 
    WHERE tf = '1d' AND ts >= 1532601600  -- 2018-07-26
""")
daily_symbols = {row[0] for row in cur.fetchall()}
universe = universe.intersection(daily_symbols)

if len(universe) == 0:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

# Get all VIX data for computing conditions
cur.execute("SELECT ts, value FROM macro_series WHERE series = 'VIX'")
vix_rows = cur.fetchall()
vix_data = {}
for ts, value in vix_rows:
    day_ts = ts // 86400 * 86400  # Truncate to day
    vix_data[day_ts] = float(value)

if len(vix_data) < 262:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

# Get insider trades with code 'P' (open-market purchases) for universe
cur.execute("""
    SELECT symbol_id, filed_ts, tx_ts
    FROM insider_trades
    WHERE code = 'P' AND symbol_id IN ({})
    ORDER BY filed_ts
""".format(','.join('?' * len(universe))), tuple(universe))
trades = cur.fetchall()

if not trades:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

# Precompute daily volume percentiles for each symbol
symbol_volume_history = defaultdict(list)
cur.execute("""
    SELECT symbol_id, ts, volume 
    FROM bars 
    WHERE tf = '1d' AND symbol_id IN ({})
    ORDER BY symbol_id, ts
""".format(','.join('?' * len(universe))), tuple(universe))

for sym_id, ts, vol in cur.fetchall():
    symbol_volume_history[sym_id].append((ts, vol))

# Precompute VIX rolling statistics
sorted_vix_days = sorted(vix_data.keys())
vix_rolling_stats = {}
for i, day in enumerate(sorted_vix_days):
    if i < 251:
        continue
    window = sorted_vix_days[i-251:i]
    vix_values = [vix_data[d] for d in window]
    avg_10 = statistics.mean(vix_values[-10:])
    percentile_95 = percentile(vix_values, 95)
    vix_rolling_stats[day] = (avg_10, percentile_95)

# Process each trade
opportunities = []
for sym_id, filed_ts, tx_ts in trades:
    # As-of discipline: use filed_ts (when became public), not tx_ts
    decision_ts = filed_ts
    decision_day = decision_ts // 86400 * 86400
    
    # Check VIX condition: 10-day avg > 95th percentile of past 252 sessions
    if decision_day not in vix_rolling_stats:
        continue
    
    avg_10, pct_95 = vix_rolling_stats[decision_day]
    if avg_10 <= pct_95:
        continue
    
    # Check volume condition: volume on decision day < 10th percentile of 60-day volume
    sym_vols = symbol_volume_history.get(sym_id, [])
    if not sym_vols:
        continue
    
    # Find volume on decision day (closest before or on decision day)
    decision_vol = None
    for ts, vol in reversed(sym_vols):
        if ts <= decision_ts:
            decision_vol = vol
            break
    
    if decision_vol is None:
        continue
    
    # Get 60-day volume window (prior 60 trading days)
    vol_window = [vol for ts, vol in sym_vols if ts <= decision_ts][-60:]
    if len(vol_window) < 20:  # Need reasonable sample
        continue
    
    pct_10 = percentile(vol_window, 10)
    if decision_vol >= pct_10:
        continue
    
    # Get 21-day forward return
    # Find close price on decision day
    close_decision = None
    for ts, vol in reversed(sym_vols):
        if ts <= decision_ts:
            # Need to get close price from bars
            cur.execute("""
                SELECT close FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND ts = ?
            """, (sym_id, ts))
            row = cur.fetchone()
            if row:
                close_decision = row[0]
            break
    
    if close_decision is None:
        continue
    
    # Find close price 21 trading days later
    close_forward = None
    trading_days_after = 0
    for ts, vol in sym_vols:
        if ts <= decision_ts:
            continue
        trading_days_after += 1
        if trading_days_after == 21:
            cur.execute("""
                SELECT close FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND ts = ?
            """, (sym_id, ts))
            row = cur.fetchone()
            if row:
                close_forward = row[0]
            break
    
    if close_forward is None:
        continue
    
    # Calculate return and direction
    fwd_return = (close_forward - close_decision) / close_decision
    up = 1 if fwd_return > 0 else 0
    
    opportunities.append({
        'symbol_id': sym_id,
        'decision_ts': decision_ts,
        'decision_day': decision_day,
        'up': up,
        'fwd_return': fwd_return
    })

if len(opportunities) < 10:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

# Sort by decision time
opportunities.sort(key=lambda x: x['decision_ts'])

# Split into training and sealed (most recent 20%)
split_idx = int(len(opportunities) * 0.8)
train_opp = opportunities[:split_idx]
sealed_opp = opportunities[split_idx:]

if len(train_opp) == 0 or len(sealed_opp) == 0:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

# Issue calls: one per (symbol, decision_day)
issued_train = {}
for opp in train_opp:
    key = (opp['symbol_id'], opp['decision_day'])
    if key not in issued_train:
        issued_train[key] = opp

# Compute metrics for training set
issued_list = list(issued_train.values())
issued_count = len(issued_list)
opportunity_count = len(train_opp)

# Calculate hits and base rate
hits = sum(1 for o in issued_list if o['up'] == 1)
precision = hits / issued_count if issued_count > 0 else 0
base_rate = hits / issued_count if issued_count > 0 else 0

# Count distinct days
distinct_days = len({o['decision_day'] for o in issued_list})

# Calculate effective N with design effect (clustering by day)
day_counts = defaultdict(int)
for o in issued_list:
    day_counts[o['decision_day']] += 1

if len(day_counts) > 1:
    # Calculate intraclass correlation for binary outcomes
    overall_p = hits / issued_count if issued_count > 0 else 0
    # Between-group variance
    day_means = [sum(1 for o in issued_list if o['decision_day'] == day and o['up'] == 1) / count 
                 for day, count in day_counts.items()]
    between_var = statistics.variance(day_means) if len(day_means) > 1 else 0
    
    # Within-group variance (binomial approximation)
    within_var = overall_p * (1 - overall_p) if overall_p > 0 and overall_p < 1 else 0.01
    
    # ICC
    icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
    
    # Design effect
    m = issued_count / len(day_counts)  # Average cluster size
    design_effect = 1 + (m - 1) * icc
    effective_n = issued_count / design_effect
else:
    effective_n = issued_count

# Calculate sealed precision
sealed_issued = {}
for opp in sealed_opp:
    key = (opp['symbol_id'], opp['decision_day'])
    if key not in sealed_issued:
        sealed_issued[key] = opp

sealed_list = list(sealed_issued.values())
sealed_hits = sum(1 for o in sealed_list if o['up'] == 1)
sealed_precision = sealed_hits / len(sealed_list) if sealed_list else 0

# Print results
print(f"ISSUED={issued_count}")
print(f"OPPORTUNITIES={opportunity_count}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.2f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")

db.close()
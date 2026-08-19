# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 495
# cycle_index: 25
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
from statistics import mean, stdev

DB = 'file:data/signaldeck.db?mode=ro'

# Load spread data
conn = sqlite3.connect(DB, uri=True)
cur = conn.cursor()
cur.execute("SELECT ts, value FROM macro_series WHERE series='BAMLH0A0HYM2'")
spread_rows = cur.fetchall()
if not spread_rows:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)
spread_data = [(ts, val) for ts, val in spread_rows]
spread_data.sort(key=lambda x: x[0])

# Build spread series and compute trailing 252-day 90th percentile per day
spread_dict = {ts: val for ts, val in spread_data}
spread_dates = [ts for ts, _ in spread_data]
def get_percentile(date_ts, lookback=252, pct=90):
    idx = None
    for i, ts in enumerate(spread_dates):
        if ts >= date_ts:
            idx = i
            break
    if idx is None:
        return None
    window = spread_data[max(0, idx-lookback+1):idx+1]
    if len(window) < lookback:
        return None
    vals = [val for _, val in window]
    vals.sort()
    k = int(len(vals) * pct / 100)
    return vals[min(k, len(vals)-1)]

# Load insider trades (Form 4 purchases only)
cur.execute("""
    SELECT symbol_id, filed_ts 
    FROM insider_trades 
    WHERE code='P' 
    AND filed_ts >= '2018-07-01' 
    AND filed_ts <= '2026-08-04'
""")
insider_rows = cur.fetchall()
if not insider_rows:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)
# Parse filed_ts to epoch
insider_events = []
for sym_id, filed in insider_rows:
    try:
        dt = datetime.strptime(filed, '%Y-%m-%d %H:%M:%S')
    except ValueError:
        dt = datetime.strptime(filed, '%Y-%m-%d')
    epoch = int(dt.timestamp())
    insider_events.append((sym_id, epoch, filed[:10]))

# Precompute 21-day forward returns for all symbols we might need
# We need bars for each symbol that appears in insider_events
needed_syms = set(sym for sym, _, _ in insider_events)
cur.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d'")
bars_data = cur.fetchall()
# Organize by symbol and time
bars_by_sym = defaultdict(list)
for sym, ts, close in bars_data:
    if sym in needed_syms:
        bars_by_sym[sym].append((ts, close))

# For each symbol, sort by ts and precompute 21-day forward returns
forward_returns = defaultdict(dict)  # (sym, epoch) -> fwd_return
for sym, bars in bars_by_sym.items():
    bars.sort(key=lambda x: x[0])
    n = len(bars)
    for i in range(n-21):
        entry_ts, entry_close = bars[i]
        exit_ts, exit_close = bars[i+21]
        fwd = (exit_close - entry_close) / entry_close
        forward_returns[sym][entry_ts] = fwd

# Now evaluate each insider event
opportunities = []
issued = []
for sym_id, decision_ts, decision_date in insider_events:
    opportunities.append((decision_ts, decision_date))
    # Get spread value on decision date
    spread_threshold = get_percentile(decision_ts)
    if spread_threshold is None:
        continue
    # Get spread on decision date
    day_str = decision_date
    # Find closest spread ts <= decision_ts
    best_ts = None
    for ts in spread_dates:
        if ts <= decision_ts:
            best_ts = ts
        else:
            break
    if best_ts is None:
        continue
    spread_val = spread_dict[best_ts]
    if spread_val <= spread_threshold:
        continue
    # Check if we have a 21-day forward return from bars
    if sym_id in forward_returns and decision_ts in forward_returns[sym_id]:
        fwd = forward_returns[sym_id][decision_ts]
        issued.append((decision_ts, decision_date, fwd))

if not issued:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Compute metrics
total_opps = len(opportunities)
total_issued = len(issued)
precision = sum(1 for _, _, fwd in issued if fwd > 0) / total_issued
base_rate = precision  # Same as precision within issued set
distinct_days = len(set(date for _, date, _ in issued))

# Design effect: cluster by day
day_counts = defaultdict(int)
for _, date, _ in issued:
    day_counts[date] += 1
avg_cluster = mean(day_counts.values())
# Compute intraclass correlation (approximate)
all_returns = [fwd for _, _, fwd in issued]
overall_mean = mean(all_returns)
between_var = sum(day_counts[d] * (mean([fwd for _, date, fwd in issued if date == d]) - overall_mean)**2 for d in day_counts) / (total_issued - 1)
total_var = stdev(all_returns)**2 if len(all_returns) > 1 else 0
if total_var > 0:
    rho = between_var / total_var
else:
    rho = 0
design_effect = 1 + (avg_cluster - 1) * rho
effective_n = total_issued / design_effect

# Split sealed era (most recent 20% by time)
issued.sort(key=lambda x: x[0])
cutoff = int(len(issued) * 0.8)
sealed = issued[cutoff:]
sealed_precision = sum(1 for _, _, fwd in sealed if fwd > 0) / len(sealed) if sealed else 0

print(f"ISSUED={total_issued}")
print(f"OPPORTUNITIES={total_opps}")
print(f"PRECISION={precision}")
print(f"BASE_RATE={base_rate}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n}")
print(f"SEALED_PRECISION={sealed_precision}")

conn.close()
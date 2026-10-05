# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 279
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

DB = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
c = DB.cursor()

# Get symbols with daily bars and compute 50-day high and 20-day avg dollar volume for each day
# We'll compute for all possible entry points from fundamentals
# First get fundamentals for publicFloat
c.execute("""
    SELECT symbol_id, as_of, fetched_at, value 
    FROM fundamentals 
    WHERE metric = 'EntityPublicFloat' AND value IS NOT NULL
    ORDER BY symbol_id, fetched_at
""")
floats = {}  # symbol_id -> list of (as_of, fetched_at, value) sorted by fetched_at
for row in c.fetchall():
    symbol_id, as_of, fetched_at, value = row
    if symbol_id not in floats:
        floats[symbol_id] = []
    floats[symbol_id].append((as_of, fetched_at, float(value)))

# For each symbol with >=2 snapshots, compute quarter-over-quarter changes
# Need to find consecutive quarters by fetched_at
# Each snapshot becomes available after fetched_at, so entry is next trading day
entry_candidates = []  # (symbol_id, entry_ts, float_change_pct)
for symbol_id, snapshots in floats.items():
    if len(snapshots) < 2:
        continue
    # Sort by fetched_at to get chronological availability
    snapshots.sort(key=lambda x: x[1])  # x[1] is fetched_at
    for i in range(1, len(snapshots)):
        prev_fetched = snapshots[i-1][1]
        curr_fetched = snapshots[i][1]
        prev_val = snapshots[i-1][2]
        curr_val = snapshots[i][2]
        if prev_val <= 0:
            continue
        change_pct = (curr_val - prev_val) / prev_val
        if change_pct >= -0.05:  # We want decrease >= 5% (negative change)
            continue
        # Entry is day after curr_fetched. Need to find next trading day with bar.
        # We'll get the next bar's ts > curr_fetched
        c.execute("""
            SELECT MIN(ts) FROM bars 
            WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        """, (symbol_id, curr_fetched))
        next_ts = c.fetchone()[0]
        if next_ts is None:
            continue
        entry_candidates.append((symbol_id, next_ts, change_pct))

# Now we need to filter by entry conditions:
# 1. close within 10% of 50-day high
# 2. 20-day avg dollar volume >= $5M
# Also need label 42 trading days later.

# Get all daily bars for relevant symbols to compute rolling metrics
# We'll build a dict: symbol_id -> list of (ts, close, volume) sorted by ts
# Get unique symbol_ids from candidates
candidate_symbols = set(s[0] for s in entry_candidates)
bars_data = {}
for sym in candidate_symbols:
    c.execute("""
        SELECT ts, close, volume FROM bars 
        WHERE symbol_id = ? AND tf = '1d' 
        ORDER BY ts
    """, (sym,))
    bars_data[sym] = c.fetchall()

# Now process each candidate
opportunities = []  # all candidates considered
issued = []  # (symbol_id, entry_ts, up) for calls issued

for symbol_id, entry_ts, change_pct in entry_candidates:
    bars = bars_data[symbol_id]
    # Find entry index
    entry_idx = None
    for idx, (ts, close, volume) in enumerate(bars):
        if ts == entry_ts:
            entry_idx = idx
            break
    if entry_idx is None:
        continue
    
    # Need at least 50 prior bars for 50-day high and 20 prior for avg volume
    if entry_idx < 50:
        continue
    
    # 50-day high
    high_50 = max(bars[entry_idx-50:entry_idx][0] for _ in range(1))  # Actually need to compute
    high_50 = max(b[1] for b in bars[entry_idx-50:entry_idx])
    close = bars[entry_idx][1]
    if close < 0.90 * high_50:
        continue
    
    # 20-day avg dollar volume
    vol_20 = sum(b[1]*b[2] for b in bars[entry_idx-20:entry_idx]) / 20
    if vol_20 < 5_000_000:
        continue
    
    # Check label: need up 42 trading days later
    target_idx = entry_idx + 42
    if target_idx >= len(bars):
        continue
    # Get close at entry and 42 days later
    close_entry = bars[entry_idx][1]
    close_future = bars[target_idx][1]
    up = 1 if close_future > close_entry else 0
    
    opportunities.append((symbol_id, entry_ts))
    issued.append((symbol_id, entry_ts, up))

DB.close()

# Compute metrics
if not issued:
    print("INSUFFICIENT=1")
else:
    # All metrics from issued list
    n = len(issued)
    hits = sum(1 for x in issued if x[2] == 1)
    precision = hits / n
    
    # Base rate is proportion of up in issued set
    base_rate = precision  # Since we're only predicting up
    
    # Distinct days
    distinct_days = len(set(x[1] for x in issued))
    
    # Effective N with design effect from day clustering
    # Count calls per day
    from collections import Counter
    day_counts = Counter(x[1] for x in issued)
    m = n / len(day_counts)  # average cluster size
    # Intracluster correlation approximation using variance of daily success rates
    # Simple approach: use design effect formula deff = 1 + (m-1)*ICC
    # Compute ICC as variance of daily proportions / total variance
    total_var = precision * (1 - precision) if precision != 1 and precision != 0 else 0.001
    daily_props = []
    for day, count in day_counts.items():
        day_hits = sum(1 for x in issued if x[1] == day and x[2] == 1)
        daily_props.append(day_hits / count if count > 0 else 0)
    if len(daily_props) > 1:
        mean_daily = sum(daily_props) / len(daily_props)
        var_between = sum((p - mean_daily)**2 for p in daily_props) / (len(daily_props) - 1)
        ICC = var_between / total_var if total_var > 0 else 0
    else:
        ICC = 0
    deff = 1 + (m - 1) * ICC
    effective_n = n / deff
    
    # Sealed era: last 20% of issued by entry_ts
    issued_sorted = sorted(issued, key=lambda x: x[1])
    sealed_start = int(n * 0.8)
    sealed = issued_sorted[sealed_start:]
    sealed_hits = sum(1 for x in sealed if x[2] == 1)
    sealed_precision = sealed_hits / len(sealed) if sealed else 0
    
    print(f"ISSUED={n}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
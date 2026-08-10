# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 372
# cycle_index: 40
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = db.cursor()

# Get symbols with both insider purchases and daily bars
cur.execute("""
    SELECT DISTINCT s.id
    FROM symbols s
    WHERE EXISTS (SELECT 1 FROM insider_trades it WHERE it.symbol_id = s.id AND it.code = 'P')
      AND EXISTS (SELECT 1 FROM bars b WHERE b.symbol_id = s.id AND b.tf = '1d')
""")
symbols = {row[0] for row in cur.fetchall()}

# Get all insider purchases for those symbols with valid price
cur.execute("""
    SELECT symbol_id, filed_ts, price
    FROM insider_trades
    WHERE code = 'P' AND price > 0
""")
purchases = cur.fetchall()

# Build list of candidate opportunities
opps = []
for sym, filed_ts, buy_price in purchases:
    if sym not in symbols:
        continue
    # Get daily bars for symbol
    cur.execute("""
        SELECT ts, close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """, (sym,))
    bars = cur.fetchall()
    if not bars:
        continue
    # Need at least 120 prior trading days before disclosure
    # filed_ts is disclosure; we need first trading day after filed_ts
    # Find first bar with ts > filed_ts
    entry_idx = None
    for i, (ts, close) in enumerate(bars):
        if ts > filed_ts:
            entry_idx = i
            break
    if entry_idx is None or entry_idx < 120:
        continue
    # Check condition: prior close <= 0.95 * buy_price
    prior_close = bars[entry_idx - 1][1]
    if prior_close > 0.95 * buy_price:
        continue
    # Check we have forward 21 trading days
    if entry_idx + 21 >= len(bars):
        continue
    # Compute forward return: from prior close to close 21 days later
    horizon_close = bars[entry_idx + 21][1]
    hit = 1 if horizon_close > prior_close else 0
    opps.append((sym, bars[entry_idx][0], hit))

db.close()

if not opps:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by entry time
opps.sort(key=lambda x: x[1])

# Apply cooldown (one per symbol per 21-day window)
issued = []
last_entry = {}  # symbol -> last entry timestamp
for sym, entry_ts, hit in opps:
    if sym in last_entry:
        # Check 21 trading days (approx calendar days) cooldown
        if entry_ts - last_entry[sym] < 21 * 24 * 3600:
            continue
    issued.append((sym, entry_ts, hit))
    last_entry[sym] = entry_ts

n_issued = len(issued)
n_opps = len(opps)

if n_issued == 0:
    print("INSUFFICIENT=1")
    exit(0)

# Compute metrics
hits = sum(1 for _, _, h in issued if h == 1)
precision = hits / n_issued
base_rate = hits / n_issued  # same as precision (within issued subset)

# Compute distinct days
distinct_days = len(set(ts for _, ts, _ in issued))

# Compute effective N (design effect)
day_counts = defaultdict(int)
for _, ts, _ in issued:
    day = ts // 86400  # unix day
    day_counts[day] += 1
cluster_sizes = list(day_counts.values())
avg_cluster = sum(cluster_sizes) / len(cluster_sizes)
# ICC approximation (simplified)
total = n_issued
n_clusters = len(cluster_sizes)
if n_clusters > 1:
    variance_between = sum((c - avg_cluster) ** 2 for c in cluster_sizes) / (n_clusters - 1)
    variance_within = avg_cluster  # simplified for binary outcomes
    icc = max(0, (variance_between - variance_within / avg_cluster) / (variance_between + (avg_cluster - 1) * variance_within / avg_cluster)) if avg_cluster > 0 else 0
    deff = 1 + (avg_cluster - 1) * icc
    effective_n = total / deff
else:
    effective_n = total

# Split into sealed era (most recent 20% of issued)
if n_issued >= 5:
    split_idx = int(n_issued * 0.8)
    sealed = issued[split_idx:]
    sealed_hits = sum(1 for _, _, h in sealed if h == 1)
    sealed_precision = sealed_hits / len(sealed) if sealed else 0
else:
    sealed_precision = 0

print(f"ISSUED={n_issued}")
print(f"OPPORTUNITIES={n_opps}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.2f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
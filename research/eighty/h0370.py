# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 369
# cycle_index: 37
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

# Get symbols with ≥252 trading days of bars AND ≥1 insider sale (code='S')
symbols_with_enough_bars = set()
for row in db.execute("""
    SELECT symbol_id, COUNT(DISTINCT date(ts, 'unixepoch')) as n_days
    FROM bars
    WHERE tf='1d'
    GROUP BY symbol_id
    HAVING n_days >= 252
"""):
    symbols_with_enough_bars.add(row[0])

symbols_with_insider_sales = set()
for row in db.execute("""
    SELECT DISTINCT symbol_id
    FROM insider_trades
    WHERE code='S'
"""):
    symbols_with_insider_sales.add(row[0])

universe = symbols_with_enough_bars & symbols_with_insider_sales

# Precompute all daily sentiment features per symbol
sentiment_by_symbol = defaultdict(list)
for row in db.execute("""
    SELECT symbol_id, day, mean_score
    FROM sentiment_features
    WHERE symbol_id IN ({})
    ORDER BY symbol_id, day
""".format(','.join('?' * len(universe))), list(universe)):
    symbol_id, day, mean_score = row
    if mean_score is not None:
        sentiment_by_symbol[symbol_id].append((day, float(mean_score)))

# For each symbol, compute rolling 252-day 90th percentile thresholds
sentiment_percentiles = defaultdict(dict)
for symbol_id, entries in sentiment_by_symbol.items():
    days = [e[0] for e in entries]
    scores = [e[1] for e in entries]
    n = len(scores)
    if n < 252:
        continue
    for i in range(251, n):
        window = scores[i-251:i+1]
        # Compute 90th percentile via linear interpolation
        sorted_win = sorted(window)
        idx = 0.9 * (len(sorted_win) - 1)
        lo = int(math.floor(idx))
        hi = min(lo + 1, len(sorted_win) - 1)
        frac = idx - lo
        p90 = sorted_win[lo] + frac * (sorted_win[hi] - sorted_win[lo])
        sentiment_percentiles[symbol_id][days[i]] = p90

# Precompute daily bars per symbol for momentum and forward returns
bars_by_symbol = defaultdict(dict)
for row in db.execute("""
    SELECT symbol_id, date(ts, 'unixepoch') as day, close
    FROM bars
    WHERE tf='1d' AND symbol_id IN ({})
    ORDER BY symbol_id, ts
""".format(','.join('?' * len(universe))), list(universe)):
    symbol_id, day, close = row
    bars_by_symbol[symbol_id][day] = float(close)

# Process insider sales and form opportunities
all_opportunities = []  # (symbol_id, decision_date, hit)
for row in db.execute("""
    SELECT symbol_id, date(filed_ts, 'unixepoch') as decision_date
    FROM insider_trades
    WHERE code='S'
    AND symbol_id IN ({})
""".format(','.join('?' * len(universe))), list(universe)):
    symbol_id, decision_date = row
    
    # Condition (a): Need 252 days of sentiment history up to decision_date
    if symbol_id not in sentiment_percentiles or decision_date not in sentiment_percentiles[symbol_id]:
        continue
    
    # Condition (b): Already filtered to code='S'
    
    # Condition (c): Sentiment in top decile on decision_date
    bars = bars_by_symbol.get(symbol_id, {})
    if decision_date not in bars:
        continue
    
    # Get sentiment score on decision_date
    sentiment_entries = sentiment_by_symbol.get(symbol_id, [])
    sentiment_on_date = None
    for day, score in sentiment_entries:
        if day == decision_date:
            sentiment_on_date = score
            break
    if sentiment_on_date is None:
        continue
    
    p90 = sentiment_percentiles[symbol_id][decision_date]
    if sentiment_on_date < p90:
        continue
    
    # Condition (d): 20-day positive momentum
    sorted_days = sorted(bars.keys())
    try:
        idx = sorted_days.index(decision_date)
    except ValueError:
        continue
    if idx < 20:
        continue
    close_now = bars[decision_date]
    close_20ago = bars[sorted_days[idx - 20]]
    if close_now <= close_20ago:
        continue
    
    # Compute 21-day forward return label
    if idx + 21 >= len(sorted_days):
        continue
    close_future = bars[sorted_days[idx + 21]]
    fwd_return = (close_future - close_now) / close_now
    is_down = fwd_return < 0  # predicted class is down
    
    all_opportunities.append((symbol_id, decision_date, is_down))

# Hold out most recent 20% as sealed era
all_opportunities.sort(key=lambda x: x[1])
n_total = len(all_opportunities)
n_sealed = int(0.2 * n_total) if n_total > 0 else 0
main_era = all_opportunities[:-n_sealed] if n_sealed > 0 else all_opportunities
sealed_era = all_opportunities[-n_sealed:] if n_sealed > 0 else []

# Compute metrics for main era
issued_main = len(main_era)
hits_main = sum(1 for x in main_era if x[2])
precision_main = hits_main / issued_main if issued_main > 0 else 0
base_rate_main = precision_main  # same as precision in issued subset

# Compute design effect (cluster by symbol)
symbol_counts = defaultdict(int)
for x in main_era:
    symbol_counts[x[0]] += 1
if issued_main > 0:
    p = precision_main
    var_clustered = 0.0
    for symbol_id, count in symbol_counts.items():
        # variance within cluster
        cluster_hits = sum(1 for x in main_era if x[0] == symbol_id and x[2])
        p_j = cluster_hits / count if count > 0 else 0
        var_clustered += count * p_j * (1 - p_j)
    # variance between clusters
    for symbol_id, count in symbol_counts.items():
        cluster_hits = sum(1 for x in main_era if x[0] == symbol_id and x[2])
        p_j = cluster_hits / count if count > 0 else 0
        var_clustered += count * (p_j - p) ** 2
    var_clustered /= issued_main
    var_srs = p * (1 - p) / issued_main if issued_main > 0 else 0
    deff = var_clustered / var_srs if var_srs > 0 else float('inf')
    effective_n = issued_main / deff if deff > 0 else issued_main
else:
    deff = 1.0
    effective_n = issued_main

# Distinct days in issued calls
distinct_days_main = len(set(x[1] for x in main_era))

# Sealed era metrics
issued_sealed = len(sealed_era)
hits_sealed = sum(1 for x in sealed_era if x[2])
precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0

# Check invariants
if distinct_days_main > issued_main:
    print("INSUFFICIENT=1")
    exit(0)
if effective_n >= issued_main:
    print("INSUFFICIENT=1")
    exit(0)

# Print required metrics
print(f"ISSUED={issued_main}")
print(f"OPPORTUNITIES={n_total}")
print(f"PRECISION={precision_main:.4f}")
print(f"BASE_RATE={base_rate_main:.4f}")
print(f"DISTINCT_DAYS={distinct_days_main}")
print(f"EFFECTIVE_N={effective_n:.4f}")
print(f"SEALED_PRECISION={precision_sealed:.4f}")

db.close()
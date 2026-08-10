# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 421
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

# Connect to database read-only
db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = db.cursor()

# Get VIX data from macro_series
cur.execute("""
    SELECT ts, value FROM macro_series WHERE series = 'VIX' ORDER BY ts
""")
vix_data = cur.fetchall()
if not vix_data:
    print("INSUFFICIENT=1")
    exit(0)

# Build VIX lookup by date (YYYY-MM-DD)
vix_by_date = {}
for ts, val in vix_data:
    day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
    vix_by_date[day] = val

# Get insider purchases (code='P') with filed_ts
cur.execute("""
    SELECT symbol_id, filed_ts FROM insider_trades WHERE code = 'P'
""")
insider_trades = cur.fetchall()

# Get symbols with at least 2 years of daily data
cur.execute("""
    SELECT symbol_id, MIN(ts), MAX(ts), COUNT(*) as days
    FROM bars WHERE tf = '1d'
    GROUP BY symbol_id
    HAVING days >= 504
""")
symbols_with_data = {}
for sym, min_ts, max_ts, days in cur.fetchall():
    symbols_with_data[sym] = (min_ts, max_ts)

# Filter insider trades to symbols with sufficient data
valid_insider_trades = [
    (sym, filed_ts) for sym, filed_ts in insider_trades
    if sym in symbols_with_data
]

if not valid_insider_trades:
    print("INSUFFICIENT=1")
    exit(0)

# Get all unique decision dates (filed_ts)
decision_dates = sorted(set(filed_ts for _, filed_ts in valid_insider_trades))

# For each decision date, check VIX condition
issued_calls = []
opportunities = []

for sym, filed_ts in valid_insider_trades:
    decision_day = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
    
    # Get VIX on decision date (or nearest prior)
    vix_val = None
    for d in sorted(vix_by_date.keys(), reverse=True):
        if d <= decision_day:
            vix_val = vix_by_date[d]
            break
    
    if vix_val is None:
        opportunities.append((sym, filed_ts, 0))
        continue
    
    # Calculate 90th percentile of VIX over prior 252 days
    prior_dates = [d for d in vix_by_date.keys() if d <= decision_day]
    if len(prior_dates) < 252:
        opportunities.append((sym, filed_ts, 0))
        continue
    
    # Sort dates and take last 252
    prior_dates_sorted = sorted(prior_dates)[-252:]
    prior_vix_values = [vix_by_date[d] for d in prior_dates_sorted]
    
    # Calculate 90th percentile
    k = 0.9 * (len(prior_vix_values) - 1)
    f = int(k)
    c = f + 1
    if c >= len(prior_vix_values):
        c = len(prior_vix_values) - 1
    
    vix_90 = prior_vix_values[f] + (k - f) * (prior_vix_values[c] - prior_vix_values[f])
    
    if vix_val > vix_90:
        # Issue call
        opportunities.append((sym, filed_ts, 1))
        issued_calls.append((sym, filed_ts))
    else:
        opportunities.append((sym, filed_ts, 0))

if not issued_calls:
    print("INSUFFICIENT=1")
    exit(0)

# Get forward returns for issued calls (21 trading days)
# First, get all daily bars for relevant symbols
symbol_ids = set(sym for sym, _ in issued_calls)
placeholders = ','.join(['?'] * len(symbol_ids))

cur.execute(f"""
    SELECT symbol_id, ts, close FROM bars 
    WHERE symbol_id IN ({placeholders}) AND tf = '1d'
    ORDER BY symbol_id, ts
""", list(symbol_ids))
bars_data = cur.fetchall()

# Organize bars by symbol
bars_by_sym = defaultdict(list)
for sym, ts, close in bars_data:
    bars_by_sym[sym].append((ts, close))

# Function to get forward return
def get_forward_return(sym, decision_ts):
    sym_bars = bars_by_sym[sym]
    if not sym_bars:
        return None
    
    # Find bar at or just after decision date
    idx = None
    for i, (ts, _) in enumerate(sym_bars):
        if ts >= decision_ts:
            idx = i
            break
    
    if idx is None:
        return None
    
    # Need 21 trading days ahead
    if idx + 21 >= len(sym_bars):
        return None
    
    entry_close = sym_bars[idx][1]
    exit_close = sym_bars[idx + 21][1]
    return (exit_close / entry_close) - 1

# Compute outcomes
outcomes = []
for sym, ts in issued_calls:
    fwd_return = get_forward_return(sym, ts)
    if fwd_return is not None:
        outcomes.append((sym, ts, fwd_return))

# Sort by time for splitting into eras
outcomes.sort(key=lambda x: x[1])

# Split into train and sealed (20% most recent)
split_idx = int(len(outcomes) * 0.8)
train_outcomes = outcomes[:split_idx]
sealed_outcomes = outcomes[split_idx:]

# Compute metrics
def compute_metrics(outcomes_list):
    if not outcomes_list:
        return 0, 0, 0, 0, 0
    
    issued = len(outcomes_list)
    hits = sum(1 for _, _, ret in outcomes_list if ret > 0)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate of predicted class (up) within issued subset
    base_rate = precision
    
    # Distinct days
    days = set()
    for _, ts, _ in outcomes_list:
        day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        days.add(day)
    distinct_days = len(days)
    
    # Design effect calculation (cluster by day)
    clusters = defaultdict(list)
    for _, ts, ret in outcomes_list:
        day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        clusters[day].append(1 if ret > 0 else 0)
    
    # Calculate ICC and design effect
    if len(clusters) <= 1:
        design_effect = 1.0
    else:
        # Calculate overall mean
        all_hits = []
        for hits in clusters.values():
            all_hits.extend(hits)
        p = sum(all_hits) / len(all_hits)
        
        # Between-cluster variance
        cluster_means = [sum(hits)/len(hits) for hits in clusters.values()]
        cluster_sizes = [len(hits) for hits in clusters.values()]
        m = sum(cluster_sizes) / len(clusters)  # average cluster size
        
        between_var = sum(size * (mean - p)**2 for mean, size in zip(cluster_means, cluster_sizes)) / (len(clusters) - 1)
        
        # Within-cluster variance
        within_var = 0
        for hits in clusters.values():
            mean_h = sum(hits) / len(hits)
            within_var += sum((h - mean_h)**2 for h in hits)
        within_var /= (len(all_hits) - len(clusters))
        
        total_var = between_var + within_var
        if total_var == 0:
            icc = 0
        else:
            icc = between_var / total_var
        
        design_effect = 1 + (m - 1) * icc
    
    effective_n = issued / design_effect
    
    return issued, precision, base_rate, distinct_days, effective_n

train_issued, train_precision, train_base_rate, train_days, train_effective_n = compute_metrics(train_outcomes)
sealed_issued, sealed_precision, sealed_base_rate, sealed_days, sealed_effective_n = compute_metrics(sealed_outcomes)

print(f"ISSUED={train_issued}")
print(f"OPPORTUNITIES={len(opportunities)}")
print(f"PRECISION={train_precision:.4f}")
print(f"BASE_RATE={train_base_rate:.4f}")
print(f"DISTINCT_DAYS={train_days}")
print(f"EFFECTIVE_N={train_effective_n:.4f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")

db.close()
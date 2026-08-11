# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 546
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = db.cursor()

# Check if we have enough data: insider_trades with Form 4 purchase code 'P'
# Need insider trades, symbols with 252+ days of daily bars, and prediction_outcomes for 21-day horizon
cur.execute("SELECT COUNT(*) FROM insider_trades WHERE code='P'")
if cur.fetchone()[0] < 10:
    print("INSUFFICIENT=1")
    exit(0)

# Get all insider open-market purchases (code='P') with both trade and filing dates
# Use filed_ts for as-of discipline (when info becomes public)
cur.execute("""
SELECT i.symbol_id, i.filed_ts, i.tx_ts, i.insider, i.title, i.shares, i.price, i.value
FROM insider_trades i
WHERE i.code='P' AND i.filed_ts IS NOT NULL AND i.tx_ts IS NOT NULL
""")
trades = cur.fetchall()

# Get symbols with at least 252 days of daily data
cur.execute("""
SELECT symbol_id, COUNT(DISTINCT ts) as days
FROM bars
WHERE tf='1d'
GROUP BY symbol_id
HAVING days >= 252
""")
valid_symbols = {row[0] for row in cur.fetchall()}

# Filter trades to valid symbols
trades = [t for t in trades if t[0] in valid_symbols]

if len(trades) < 10:
    print("INSUFFICIENT=1")
    exit(0)

# For each trade, check if closing price on tx_ts is >= 95% of 52-week high
# Need to compute 52-week high (252 trading days) up to and including tx_ts
signals = []
for symbol_id, filed_ts, tx_ts, insider, title, shares, price, value in trades:
    # Get daily bars for this symbol up to and including tx_ts
    cur.execute("""
    SELECT ts, high, close FROM bars
    WHERE symbol_id=? AND tf='1d' AND ts <= ?
    ORDER BY ts DESC
    LIMIT 252
    """, (symbol_id, tx_ts))
    bars = cur.fetchall()
    
    if len(bars) < 252:
        continue
    
    # Check if we have a bar exactly on tx_ts
    if bars[0][0] != tx_ts:
        continue
    
    close_price = bars[0][2]
    high_52w = max(bar[1] for bar in bars)
    
    if close_price >= 0.95 * high_52w:
        signals.append((symbol_id, filed_ts, tx_ts, close_price, high_52w))

if len(signals) < 10:
    print("INSUFFICIENT=1")
    exit(0)

# For each signal, check if there's a 21-day horizon prediction outcome
# filed_ts is the decision time, we need outcomes starting from filed_ts with horizon=21
opportunities = []
for symbol_id, filed_ts, tx_ts, close_price, high_52w in signals:
    # Get prediction outcomes for this symbol with horizon=21 days
    # ts must be >= filed_ts (can't use future outcomes)
    # Use basis_epoch to ensure we don't use lookahead
    cur.execute("""
    SELECT ts, up, fwd_return, resolved_at, basis_epoch
    FROM prediction_outcomes
    WHERE symbol_id=? AND horizon=21 AND ts >= ? AND resolved_at IS NOT NULL
    ORDER BY ts ASC
    LIMIT 1
    """, (symbol_id, filed_ts))
    
    row = cur.fetchone()
    if row is None:
        continue
    
    outcome_ts, up, fwd_return, resolved_at, basis_epoch = row
    
    # Check that we have this data at decision time (filed_ts)
    # basis_epoch is when the outcome was computed - must be <= filed_ts
    if basis_epoch is not None and basis_epoch > filed_ts:
        continue
    
    # resolved_at is when the outcome was resolved - must be in the future relative to filed_ts
    if resolved_at <= filed_ts:
        continue
    
    # This is a valid opportunity
    opportunities.append({
        'symbol_id': symbol_id,
        'filed_ts': filed_ts,
        'tx_ts': tx_ts,
        'close_price': close_price,
        'high_52w': high_52w,
        'outcome_ts': outcome_ts,
        'up': up,
        'fwd_return': fwd_return,
        'resolved_at': resolved_at
    })

if len(opportunities) < 10:
    print("INSUFFICIENT=1")
    exit(0)

# Sort opportunities by filed_ts to split into train/holdout (80/20)
opportunities.sort(key=lambda x: x['filed_ts'])
split_idx = int(len(opportunities) * 0.8)
train_opps = opportunities[:split_idx]
holdout_opps = opportunities[split_idx:]

# Count independent observations (symbol, UTC day)
# Each opportunity is one (symbol, day) because we only issue one call per (symbol, filing_date)
# Calculate metrics for train set
issued_train = len(train_opps)
base_up_train = sum(1 for opp in train_opps if opp['up'])
precision_train = base_up_train / issued_train if issued_train > 0 else 0

# Calculate base rate of up within issued calls
base_rate_train = precision_train

# Calculate distinct days and design effect for train set
days_count = defaultdict(int)
for opp in train_opps:
    # Convert filed_ts to date
    from datetime import datetime
    day = datetime.utcfromtimestamp(opp['filed_ts']).strftime('%Y-%m-%d')
    days_count[day] += 1

distinct_days_train = len(days_count)
if distinct_days_train == 0:
    print("INSUFFICIENT=1")
    exit(0)

# Calculate design effect using variance of cluster sizes
# DEFF = 1 + (mean(m_i) - 1) * ICC, approximate with variance of cluster sizes
# For binary outcomes, can use variance of proportions within clusters
cluster_sizes = list(days_count.values())
mean_cluster = sum(cluster_sizes) / len(cluster_sizes)
if mean_cluster <= 1:
    deff = 1.0
else:
    # Approximate ICC from variance of cluster proportions
    # Get proportion of ups per cluster
    day_up_counts = defaultdict(int)
    for opp in train_opps:
        day = datetime.utcfromtimestamp(opp['filed_ts']).strftime('%Y-%m-%d')
        if opp['up']:
            day_up_counts[day] += 1
    
    cluster_props = [day_up_counts[day]/size for day, size in days_count.items()]
    overall_prop = base_up_train / issued_train if issued_train > 0 else 0
    
    # Variance of cluster proportions
    var_cluster = sum((p - overall_prop)**2 for p in cluster_props) / len(cluster_props) if len(cluster_props) > 0 else 0
    
    # ICC approximation from variance components
    # For binary: ICC = (var_between - p(1-p)/m) / (p(1-p) + var_between)
    p_q = overall_prop * (1 - overall_prop) if overall_prop > 0 and overall_prop < 1 else 0.01
    var_within = p_q  # approximation
    
    if var_cluster > var_within:
        icc = (var_cluster - var_within/mean_cluster) / (var_within + var_cluster)
        icc = max(0, min(1, icc))  # bound between 0 and 1
    else:
        icc = 0
    
    deff = 1 + (mean_cluster - 1) * icc

effective_n_train = issued_train / deff if deff > 0 else issued_train

# Calculate for holdout set
issued_holdout = len(holdout_opps)
base_up_holdout = sum(1 for opp in holdout_opps if opp['up'])
precision_holdout = base_up_holdout / issued_holdout if issued_holdout > 0 else 0

# Print required lines
print(f"ISSUED={issued_train}")
print(f"OPPORTUNITIES={len(opportunities)}")
print(f"PRECISION={precision_train:.4f}")
print(f"BASE_RATE={base_rate_train:.4f}")
print(f"DISTINCT_DAYS={distinct_days_train}")
print(f"EFFECTIVE_N={effective_n_train:.2f}")
print(f"SEALED_PRECISION={precision_holdout:.4f}")

db.close()
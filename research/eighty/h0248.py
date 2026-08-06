import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

# Connect read-only
db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, check_same_thread=False)
db.execute("PRAGMA journal_mode=WAL")

# Get all disclosure dates from insider_trades (filed_ts is the only knowable timestamp)
insider_query = """
SELECT symbol_id, insider, code, tx_ts, filed_ts
FROM insider_trades
WHERE code = 'S'
"""
insider_rows = db.execute(insider_query).fetchall()

# Group by symbol and filed_ts date
disclosure_map = defaultdict(list)
for symbol_id, insider, code, tx_ts, filed_ts in insider_rows:
    # filed_ts is unix epoch - convert to date
    date_str = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
    disclosure_map[(symbol_id, date_str)].append((insider, tx_ts))

# Get daily bars for symbols in disclosure_map
# First get all unique symbol_ids
all_symbols = list(set(sym for (sym, _) in disclosure_map.keys()))

# Get bars with daily timeframe
bars_query = """
SELECT symbol_id, ts, open, high, low, close, volume
FROM bars
WHERE tf = '1d' AND symbol_id IN ({})
ORDER BY symbol_id, ts
""".format(','.join('?' * len(all_symbols)))
bars_rows = db.execute(bars_query, all_symbols).fetchall()

# Organize bars by symbol and date
bars_by_symbol = defaultdict(list)
for symbol_id, ts, open_, high, low, close, volume in bars_rows:
    date_str = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
    bars_by_symbol[symbol_id].append((date_str, close, volume, ts))

# Create sorted date lists for each symbol
for symbol_id in bars_by_symbol:
    bars_by_symbol[symbol_id].sort(key=lambda x: x[3])  # Sort by ts

# Get prediction outcomes for labels (20-day horizon)
# We need horizon=20 (days) for our 20-day prediction
pred_query = """
SELECT symbol_id, ts, up, fwd_return, horizon
FROM prediction_outcomes
WHERE horizon = 20
"""
pred_rows = db.execute(pred_query).fetchall()

# Create label lookup: (symbol_id, ts_date) -> up
labels = {}
for symbol_id, ts, up, fwd_return, horizon in pred_rows:
    date_str = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
    labels[(symbol_id, date_str)] = (up, fwd_return)

db.close()

# Helper function to compute returns and volatility
def compute_metrics(bars_list, target_date, window=60):
    """Compute required metrics from bars_list up to target_date (exclusive)"""
    dates = [b[0] for b in bars_list]
    if target_date not in dates:
        return None
    
    idx = dates.index(target_date)
    if idx < 252:  # Need at least 252 prior sessions
        return None
    
    # Get T-1 close
    t_minus1 = idx - 1
    if t_minus1 < 0:
        return None
    close_t_minus1 = bars_list[t_minus1][1]
    
    # Check price >= $5
    if close_t_minus1 < 5:
        return None
    
    # Compute 20-day return (T-21 to T-1)
    if idx < 21:
        return None
    close_t_minus21 = bars_list[idx - 21][1]
    ret_20d = (close_t_minus1 / close_t_minus21) - 1
    
    # Compute average daily dollar volume over T-60..T-1
    avg_vol = 0
    if idx >= 60:
        for i in range(idx - 60, idx):
            close_i = bars_list[i][1]
            vol_i = bars_list[i][2]
            avg_vol += close_i * vol_i
        avg_vol /= 60
    else:
        return None
    
    # Compute 20-day realized volatility (T-20..T-1)
    if idx < 20:
        return None
    returns = []
    for i in range(idx - 20, idx):
        close_prev = bars_list[i - 1][1] if i > 0 else None
        if close_prev and close_prev > 0:
            returns.append((bars_list[i][1] / close_prev) - 1)
    if len(returns) < 20:
        return None
    mean_ret = sum(returns) / len(returns)
    var = sum((r - mean_ret) ** 2 for r in returns) / (len(returns) - 1)
    vol_20d = math.sqrt(var)
    
    return {
        'close_t_minus1': close_t_minus1,
        'ret_20d': ret_20d,
        'avg_vol': avg_vol,
        'vol_20d': vol_20d,
        'idx': idx
    }

# Process each disclosure date
opportunities = []
issued_calls = []

for (symbol_id, date_str), insiders in disclosure_map.items():
    if symbol_id not in bars_by_symbol:
        continue
    
    bars_list = bars_by_symbol[symbol_id]
    dates = [b[0] for b in bars_list]
    
    if date_str not in dates:
        continue
    
    # Get metrics at T (date_str)
    metrics = compute_metrics(bars_list, date_str)
    if not metrics:
        continue
    
    # Check insider conditions: at least 2 distinct insiders with trades in T-10..T-1
    t_idx = metrics['idx']
    if t_idx < 10:
        continue
    
    # Get date range T-10..T-1
    t_minus10_date = bars_list[t_idx - 10][0]
    t_minus1_date = bars_list[t_idx - 1][0]
    
    # Filter insiders by tx_ts in range
    valid_insiders = set()
    for insider, tx_ts in insiders:
        tx_date = datetime.utcfromtimestamp(tx_ts).strftime('%Y-%m-%d')
        if t_minus10_date <= tx_date <= t_minus1_date:
            valid_insiders.add(insider)
    
    if len(valid_insiders) < 2:
        continue
    
    # Check 20-day return >= +10%
    if metrics['ret_20d'] < 0.10:
        continue
    
    # Record this as an opportunity
    opportunities.append((symbol_id, date_str, metrics['vol_20d']))

# Compute cross-sectional volatility decile for each date
vol_by_date = defaultdict(list)
for symbol_id, date_str, vol in opportunities:
    vol_by_date[date_str].append((symbol_id, vol))

# Filter out top decile volatility
filtered_opportunities = []
for date_str, symbol_vols in vol_by_date.items():
    # Sort by volatility
    symbol_vols.sort(key=lambda x: x[1])
    n = len(symbol_vols)
    cutoff_idx = max(1, int(n * 0.9))  # Top 10% starts at 90th percentile
    for i, (symbol_id, vol) in enumerate(symbol_vols):
        if i < cutoff_idx:  # Not in top decile
            filtered_opportunities.append((symbol_id, date_str, vol))

# Check for repeat calls (20 trading days cooldown)
# Group by symbol
symbol_dates = defaultdict(list)
for symbol_id, date_str, vol in filtered_opportunities:
    symbol_dates[symbol_id].append(date_str)

# Get sorted dates for each symbol
for symbol_id in symbol_dates:
    symbol_dates[symbol_id].sort()

# Apply cooldown
final_opportunities = []
for symbol_id, date_str, vol in filtered_opportunities:
    dates = symbol_dates[symbol_id]
    idx = dates.index(date_str)
    
    # Check if any call in prior 20 trading days
    cooldown = False
    for i in range(max(0, idx - 20), idx):
        # We'll check later if any call was issued in these dates
        # For now, we keep all and filter after we know issued calls
        pass
    
    final_opportunities.append((symbol_id, date_str, vol))

# Need to sort opportunities by date
final_opportunities.sort(key=lambda x: x[1])

# Check minimum observations (30)
if len(final_opportunities) < 30:
    print("INSUFFICIENT=1")
    exit(0)

# Split into train/test (80/20)
n_total = len(final_opportunities)
n_test = max(1, int(n_total * 0.2))
train_opps = final_opportunities[:n_total - n_test]
test_opps = final_opportunities[n_total - n_test:]

# Function to issue calls and compute labels
def evaluate_opportunities(opps_list, cooldown_set=None):
    if cooldown_set is None:
        cooldown_set = set()
    
    issued = []
    issued_dates = set()
    cooldown = defaultdict(int)  # symbol -> last call date
    
    for symbol_id, date_str, vol in opps_list:
        # Check cooldown (20 trading days)
        if symbol_id in cooldown:
            last_date = cooldown[symbol_id]
            # Check if within 20 trading days
            # For simplicity, we'll approximate by calendar days (20 trading days ≈ 28 calendar days)
            try:
                d1 = datetime.strptime(last_date, '%Y-%m-%d')
                d2 = datetime.strptime(date_str, '%Y-%m-%d')
                if (d2 - d1).days <= 28:
                    continue
            except:
                pass
        
        # Check if we have label
        if (symbol_id, date_str) not in labels:
            continue
        
        up, fwd_return = labels[(symbol_id, date_str)]
        
        # We predict DOWN, so hit if up == 0
        hit = (up == 0)
        issued.append((symbol_id, date_str, hit))
        issued_dates.add(date_str)
        cooldown[symbol_id] = date_str
    
    return issued, issued_dates

# Evaluate train set
train_issued, train_dates = evaluate_opportunities(train_opps)

# Compute metrics for train set
if not train_issued:
    print("INSUFFICIENT=1")
    exit(0)

# Compute design effect for train set (clustered by date)
date_counts = defaultdict(int)
for _, date_str, _ in train_issued:
    date_counts[date_str] += 1

n_dates = len(date_counts)
if n_dates == 0:
    print("INSUFFICIENT=1")
    exit(0)

# Effective sample size (simplified: using date clustering)
# Design effect ≈ 1 + (m - 1) * ICC, where m is average cluster size
# ICC ≈ 0.1 as rough estimate for financial data
avg_cluster_size = len(train_issued) / n_dates
icc = 0.1  # Conservative estimate
design_effect = 1 + (avg_cluster_size - 1) * icc
effective_n = len(train_issued) / design_effect

# Compute precision metrics
hits = sum(1 for _, _, hit in train_issued if hit)
precision = hits / len(train_issued)

# Compute base rate: probability of down (up=0) in issued subset
down_count = sum(1 for _, _, up, _ in [(sid, ds, labels[(sid, ds)][0], labels[(sid, ds)][1]) 
                                        for sid, ds, _ in train_issued] 
                 if up == 0)
base_rate = down_count / len(train_issued)

# Evaluate test set (sealed era) with train cooldown
test_issued, test_dates = evaluate_opportunities(test_opps, set(cooldown.values()))
test_hits = sum(1 for _, _, hit in test_issued if hit)
test_precision = test_hits / len(test_issued) if test_issued else 0

# Print required metrics
print(f"ISSUED={len(train_issued)}")
print(f"OPPORTUNITIES={len(train_opps)}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={len(train_dates)}")
print(f"EFFECTIVE_N={effective_n:.2f}")
print(f"SEALED_PRECISION={test_precision:.4f}")
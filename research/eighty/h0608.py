# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 607
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'
conn = sqlite3.connect(DB_PATH, uri=True)
conn.row_factory = sqlite3.Row
c = conn.cursor()

# 1. Get all open-market purchases (code='P')
c.execute("""
    SELECT symbol_id, tx_ts, filed_ts
    FROM insider_trades
    WHERE code = 'P'
""")
trades = c.fetchall()

# Early exit if no trades
if not trades:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# 2. Gather unique symbols that have daily bars
c.execute("""
    SELECT DISTINCT symbol_id
    FROM bars
    WHERE tf = '1d'
""")
symbols_with_daily = set(row['symbol_id'] for row in c.fetchall())
if not symbols_with_daily:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# 3. Helper to get daily bars for a symbol up to a timestamp
def get_daily_bars(symbol_id, before_ts):
    """Return list of (ts, close, volume) for daily bars with ts < before_ts, ordered by ts."""
    c.execute("""
        SELECT ts, close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts < ?
        ORDER BY ts
    """, (symbol_id, before_ts))
    return c.fetchall()

# 4. Helper to compute close-to-close return over a range of timestamps
def compute_return(bars, start_ts, end_ts):
    """Given sorted bars, compute return from start_ts to end_ts (both must exist)."""
    start_idx = None
    end_idx = None
    for i, row in enumerate(bars):
        if row['ts'] == start_ts:
            start_idx = i
        if row['ts'] == end_ts:
            end_idx = i
    if start_idx is None or end_idx is None:
        return None
    return bars[end_idx]['close'] / bars[start_idx]['close'] - 1

# 5. Helper to compute median dollar volume over last 63 sessions
def compute_median_dollar_vol(bars, n=63):
    """Take the last n bars, compute close*volume for each, return median."""
    if len(bars) < n:
        return None
    last_n = bars[-n:]
    dollar_vols = [row['close'] * row['volume'] for row in last_n]
    dollar_vols.sort()
    mid = n // 2
    if n % 2 == 1:
        return dollar_vols[mid]
    else:
        return (dollar_vols[mid-1] + dollar_vols[mid]) / 2

# 6. Helper to get forward return 21 trading days after a given ts
def get_forward_return(symbol_id, after_ts):
    """Return close-to-close return from after_ts to the 21st trading day after."""
    c.execute("""
        SELECT ts, close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        ORDER BY ts
        LIMIT 22
    """, (symbol_id, after_ts))
    rows = c.fetchall()
    if len(rows) < 22:
        return None
    return rows[21]['close'] / rows[0]['close'] - 1

# 7. Process each trade to identify opportunities
opportunities = []  # Each entry: (symbol_id, ts(D), forward_return)
last_call = {}  # symbol_id -> last D (timestamp) of call issued

for trade in trades:
    symbol_id = trade['symbol_id']
    tx_ts = trade['tx_ts']
    filed_ts = trade['filed_ts']  # D
    
    # Basic check: T must be before D
    if tx_ts >= filed_ts:
        continue
    
    # Check daily bars exist for symbol
    if symbol_id not in symbols_with_daily:
        continue
    
    # Get daily bars before D
    bars_before_d = get_daily_bars(symbol_id, filed_ts)
    if len(bars_before_d) < 63:
        continue
    
    # Check T is in the bars (should be, but ensure)
    if not any(row['ts'] == tx_ts for row in bars_before_d):
        continue
    
    # Compute return from T to D-1 (last bar before D)
    # Find the bar immediately before D (the last bar in bars_before_d)
    last_bar_before_d = bars_before_d[-1]
    d_minus_1_ts = last_bar_before_d['ts']
    t_to_d_minus_1_return = compute_return(bars_before_d, tx_ts, d_minus_1_ts)
    if t_to_d_minus_1_return is None:
        continue
    
    # Abstain if return > -3%
    if t_to_d_minus_1_return > -0.03:
        continue
    
    # Compute median dollar volume over 63 sessions before D
    median_dollar_vol = compute_median_dollar_vol(bars_before_d, 63)
    if median_dollar_vol is None or median_dollar_vol < 1_000_000:
        continue
    
    # Check if same symbol already called within prior 21 calendar days
    if symbol_id in last_call:
        last_d_ts = last_call[symbol_id]
        diff_days = (datetime.utcfromtimestamp(filed_ts) - datetime.utcfromtimestamp(last_d_ts)).days
        if diff_days <= 21:
            continue
    
    # Compute forward return from D
    forward_return = get_forward_return(symbol_id, filed_ts)
    if forward_return is None:
        continue
    
    # All checks passed: this is an opportunity that issues a call
    opportunities.append((symbol_id, filed_ts, forward_return))
    last_call[symbol_id] = filed_ts

# Early exit if no opportunities
if not opportunities:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# 8. Split into train (80%) and sealed (20%) by time
opportunities.sort(key=lambda x: x[1])  # sort by D
split_idx = int(len(opportunities) * 0.8)
train = opportunities[:split_idx]
sealed = opportunities[split_idx:]

# 9. Compute metrics for train set
def compute_metrics(subset):
    if not subset:
        return None, None, None, None, None, None
    
    n_issued = len(subset)
    hits = sum(1 for _, _, fwd in subset if fwd > 0)
    precision = hits / n_issued
    base_rate = precision  # base rate of up within issued
    
    # Distinct days
    days = set(d for _, d, _ in subset)
    n_days = len(days)
    
    # Design effect: cluster by day
    # Group by day
    day_groups = {}
    for _, d, fwd in subset:
        if d not in day_groups:
            day_groups[d] = []
        day_groups[d].append(fwd)
    
    k = n_days
    n = n_issued
    # Compute ICC for binary outcomes
    # Overall proportion
    p = hits / n
    # Between-cluster variance
    msb = 0.0
    for d, outcomes in day_groups.items():
        n_i = len(outcomes)
        p_i = sum(outcomes) / n_i
        msb += n_i * (p_i - p) ** 2
    if k > 1:
        msb /= (k - 1)
    else:
        msb = 0.0
    # Within-cluster variance
    msw = 0.0
    for d, outcomes in day_groups.items():
        n_i = len(outcomes)
        p_i = sum(outcomes) / n_i
        msw += n_i * p_i * (1 - p_i)
    if n - k > 0:
        msw /= (n - k)
    else:
        msw = 0.0
    # Adjusted average cluster size
    sum_ni_sq = sum(len(outcomes) ** 2 for outcomes in day_groups.values())
    m0 = (n - sum_ni_sq / n) / (k - 1) if k > 1 else n
    # ICC
    if msb > 0 and msw > 0:
        rho = (msb - msw) / (msb + (m0 - 1) * msw)
    else:
        rho = 0.0
    # Design effect
    deff = 1 + (n / k - 1) * rho if k > 0 else 1.0
    effective_n = n / deff if deff > 0 else n
    
    return n_issued, precision, base_rate, n_days, effective_n

# Compute for train
train_metrics = compute_metrics(train)
if train_metrics is None:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

train_issued, train_precision, train_base_rate, train_distinct_days, train_effective_n = train_metrics

# Compute for sealed
sealed_metrics = compute_metrics(sealed)
sealed_precision = sealed_metrics[1] if sealed_metrics else 0.0

# 10. Print results
print(f"ISSUED={train_issued}")
print(f"OPPORTUNITIES={len(opportunities)}")
print(f"PRECISION={train_precision:.4f}")
print(f"BASE_RATE={train_base_rate:.4f}")
print(f"DISTINCT_DAYS={train_distinct_days}")
print(f"EFFECTIVE_N={train_effective_n:.2f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")

conn.close()
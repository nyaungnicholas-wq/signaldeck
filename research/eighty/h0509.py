# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 508
# cycle_index: 38
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

DB_PATH = "file:data/signaldeck.db?mode=ro"
conn = sqlite3.connect(DB_PATH, uri=True)
conn.row_factory = sqlite3.Row
c = conn.cursor()

# Get all symbols that have daily bars and meet the minimum history requirement
c.execute("""
    SELECT symbol_id, COUNT(*) as bar_count
    FROM bars WHERE tf='1d'
    GROUP BY symbol_id
    HAVING COUNT(*) >= 252
""")
valid_symbols = {row[0] for row in c.fetchall()}

if len(valid_symbols) < 20:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Get trade-weighted dollar index (TWEXB) from macro_series
c.execute("""
    SELECT ts, value FROM macro_series
    WHERE series='TWEXB'
    ORDER BY ts
""")
dollar_data = c.fetchall()
if len(dollar_data) < 30:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

dollar_ts_vals = [(row[0], row[1]) for row in dollar_data]
dollar_dict = {ts: val for ts, val in dollar_ts_vals}

# Get all daily bars for valid symbols, sorted by symbol and time
c.execute("""
    SELECT symbol_id, ts, close, volume FROM bars
    WHERE tf='1d' AND symbol_id IN ({})
    ORDER BY symbol_id, ts
""".format(','.join(map(str, valid_symbols))))

bars = []
for row in c.fetchall():
    bars.append((row[0], row[1], row[2], row[3]))

# Group bars by symbol
bars_by_symbol = defaultdict(list)
for s, ts, close, vol in bars:
    bars_by_symbol[s].append((ts, close, vol))

# Get prediction outcomes for labels
c.execute("""
    SELECT symbol_id, horizon, ts, up, fwd_return FROM prediction_outcomes
    WHERE horizon=21
""")
outcomes = {}
for row in c.fetchall():
    outcomes[(row[0], row[2])] = (row[3], row[4])

# Prepare data structures for computation
# We'll process day by day in chronological order
# Get all unique days from dollar data
all_days = sorted(set([ts for ts, _ in dollar_ts_vals]))

# For each symbol, precompute 20-day rolling dollar returns and 60-day beta
# Also track 21-day average dollar volume
symbol_betas = defaultdict(list)  # symbol -> list of (ts, beta)
symbol_avg_vol = defaultdict(list)  # symbol -> list of (ts, avg_vol_21d)

for symbol in valid_symbols:
    if symbol not in bars_by_symbol:
        continue
    sym_bars = bars_by_symbol[symbol]
    if len(sym_bars) < 252:
        continue
    
    # Compute 21-day average dollar volume
    for i in range(252, len(sym_bars)):
        window = sym_bars[i-21:i]
        avg_vol = sum(close * vol for _, close, vol in window) / 21
        symbol_avg_vol[symbol].append((sym_bars[i][0], avg_vol))
    
    # Compute 60-day rolling beta vs dollar index
    for i in range(252, len(sym_bars)):
        if i < 60:
            continue
        ts = sym_bars[i][0]
        # Get dollar index returns over last 60 days
        dollar_window = []
        for j in range(i-59, i+1):
            day_ts = sym_bars[j][0]
            if day_ts in dollar_dict and day_ts - 86400 in dollar_dict:
                r_d = (dollar_dict[day_ts] - dollar_dict[day_ts-86400]) / dollar_dict[day_ts-86400]
                # Get stock return
                stock_ts1 = sym_bars[j][0]
                stock_ts0 = sym_bars[j-1][0]
                if stock_ts1 == day_ts:  # aligned
                    r_s = (sym_bars[j][1] - sym_bars[j-1][1]) / sym_bars[j-1][1]
                    dollar_window.append((r_d, r_s))
        
        if len(dollar_window) >= 40:  # need enough data
            # Compute beta
            mean_d = sum(x for x,_ in dollar_window) / len(dollar_window)
            mean_s = sum(y for _,y in dollar_window) / len(dollar_window)
            cov = sum((x-mean_d)*(y-mean_s) for x,y in dollar_window) / len(dollar_window)
            var_d = sum((x-mean_d)**2 for x,_ in dollar_window) / len(dollar_window)
            if var_d > 0:
                beta = cov / var_d
                symbol_betas[symbol].append((ts, beta))

# Now process each decision day in chronological order
decision_points = []  # list of (symbol, ts, is_call, label)

# Track last call per symbol to enforce 10-day abstention
last_call_ts = {}

for day_ts in all_days:
    # Check if dollar index surged 3% over last 20 days
    lookback_ts = day_ts - 20*86400
    if lookback_ts not in dollar_dict or day_ts not in dollar_dict:
        continue
    d_ret = (dollar_dict[day_ts] - dollar_dict[lookback_ts]) / dollar_dict[lookback_ts]
    if d_ret < 0.03:
        continue
    
    # Get universe: symbols with avg_vol >= 2M at this day
    universe = []
    for symbol in valid_symbols:
        if symbol not in symbol_avg_vol:
            continue
        # Find avg_vol at or before day_ts
        vol_at = None
        for ts, avg_vol in symbol_avg_vol[symbol]:
            if ts <= day_ts:
                vol_at = avg_vol
            else:
                break
        if vol_at is None:
            continue
        if vol_at >= 2e6:
            universe.append((symbol, vol_at))
    
    if len(universe) < 10:
        continue
    
    # Get betas at this day
    betas_at_day = []
    for symbol, _ in universe:
        if symbol not in symbol_betas:
            continue
        beta_at = None
        for ts, beta in symbol_betas[symbol]:
            if ts <= day_ts:
                beta_at = beta
            else:
                break
        if beta_at is not None:
            betas_at_day.append((symbol, beta_at))
    
    if len(betas_at_day) < 10:
        continue
    
    # Quintiles
    betas_sorted = sorted([b for _,b in betas_at_day])
    vols_sorted = sorted([v for _,v in universe])
    top_beta_q = betas_sorted[int(0.8*len(betas_sorted))]
    bottom_vol_q = vols_sorted[int(0.2*len(vols_sorted))]
    
    # Issue calls
    for symbol, vol in universe:
        # Check abstention: fewer than 10 days since last call
        if symbol in last_call_ts:
            if day_ts - last_call_ts[symbol] < 10*86400:
                continue
        
        # Check beta quintile
        beta_at = None
        for ts, beta in symbol_betas[symbol]:
            if ts <= day_ts:
                beta_at = beta
            else:
                break
        if beta_at is None:
            continue
        if beta_at < top_beta_q:
            continue
        
        # Check volume quintile
        if vol > bottom_vol_q:
            continue
        
        # Check label availability
        label_ts = day_ts + 21*86400
        if (symbol, label_ts) not in outcomes:
            continue
        
        up, fwd_return = outcomes[(symbol, label_ts)]
        is_hit = (up == 0)  # SHORT, so down is hit
        
        decision_points.append((symbol, day_ts, True, is_hit))
        last_call_ts[symbol] = day_ts

# Also collect all decision points (including non-issued) for base rate
# But we need to know all opportunities
all_opportunities = []
for day_ts in all_days:
    # Same universe computation as above
    lookback_ts = day_ts - 20*86400
    if lookback_ts not in dollar_dict or day_ts not in dollar_dict:
        continue
    d_ret = (dollar_dict[day_ts] - dollar_dict[lookback_ts]) / dollar_dict[lookback_ts]
    if d_ret < 0.03:
        continue
    
    universe = []
    for symbol in valid_symbols:
        if symbol not in symbol_avg_vol:
            continue
        vol_at = None
        for ts, avg_vol in symbol_avg_vol[symbol]:
            if ts <= day_ts:
                vol_at = avg_vol
            else:
                break
        if vol_at is None:
            continue
        if vol_at >= 2e6:
            universe.append((symbol, vol_at))
    
    if len(universe) < 10:
        continue
    
    betas_at_day = []
    for symbol, _ in universe:
        if symbol not in symbol_betas:
            continue
        beta_at = None
        for ts, beta in symbol_betas[symbol]:
            if ts <= day_ts:
                beta_at = beta
            else:
                break
        if beta_at is not None:
            betas_at_day.append((symbol, beta_at))
    
    if len(betas_at_day) < 10:
        continue
    
    for symbol, vol in universe:
        beta_at = None
        for ts, beta in symbol_betas[symbol]:
            if ts <= day_ts:
                beta_at = beta
            else:
                break
        if beta_at is None:
            continue
        
        label_ts = day_ts + 21*86400
        if (symbol, label_ts) in outcomes:
            up, fwd_return = outcomes[(symbol, label_ts)]
            all_opportunities.append((symbol, day_ts, up == 0))

# Split into train and sealed (last 20%)
sorted_days = sorted(set([ts for _, ts, _ in all_opportunities]))
split_idx = int(0.8 * len(sorted_days))
split_ts = sorted_days[split_idx] if split_idx < len(sorted_days) else float('inf')

train_opps = [(s, ts, h) for s, ts, h in all_opportunities if ts < split_ts]
sealed_opps = [(s, ts, h) for s, ts, h in all_opportunities if ts >= split_ts]

train_calls = [(s, ts, h) for s, ts, _, h in decision_points if ts < split_ts]
sealed_calls = [(s, ts, h) for s, ts, _, h in decision_points if ts >= split_ts]

def compute_metrics(calls, opportunities):
    if not calls:
        return 0, 0, 0, 0, 0, 0
    
    issued = len(calls)
    hits = sum(1 for _, _, h in calls if h)
    precision = hits / issued
    
    # Base rate within issued subset
    base_rate = precision
    
    # Distinct days
    days = set(ts for _, ts, _ in calls)
    distinct_days = len(days)
    
    # Design effect: cluster by day
    day_clusters = defaultdict(list)
    for _, ts, h in calls:
        day_clusters[ts].append(h)
    
    if len(day_clusters) <= 1:
        design_effect = 1
    else:
        # Compute ICC
        p = precision
        # Variance within clusters
        var_within = 0
        total_n = issued
        for day, labels in day_clusters.items():
            m = len(labels)
            p_day = sum(labels) / m
            var_within += m * (p_day * (1 - p_day))
        var_within /= total_n
        
        # Variance between clusters
        var_between = 0
        for day, labels in day_clusters.items():
            m = len(labels)
            p_day = sum(labels) / m
            var_between += m * (p_day - p) ** 2
        var_between /= total_n
        
        # ICC approximation
        if var_within + var_between > 0:
            icc = var_between / (var_within + var_between)
        else:
            icc = 0
        
        m_bar = issued / len(day_clusters)
        design_effect = 1 + (m_bar - 1) * icc
    
    effective_n = issued / design_effect
    
    return issued, precision, base_rate, distinct_days, design_effect, effective_n

train_issued, train_prec, train_br, train_days, train_deff, train_eff = compute_metrics(train_calls, train_opps)
sealed_issued, sealed_prec, sealed_br, sealed_days, sealed_deff, sealed_eff = compute_metrics(sealed_calls, sealed_opps)

if train_issued == 0:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

print(f"ISSUED={train_issued}")
print(f"OPPORTUNITIES={len(train_opps)}")
print(f"PRECISION={train_prec:.4f}")
print(f"BASE_RATE={train_br:.4f}")
print(f"DISTINCT_DAYS={train_days}")
print(f"EFFECTIVE_N={train_eff:.1f}")
print(f"SEALED_PRECISION={sealed_prec:.4f}")

conn.close()
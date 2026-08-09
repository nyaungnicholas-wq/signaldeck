import sqlite3
import math
from collections import defaultdict

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
c = db.cursor()

# Get VIX data from macro_series
c.execute("SELECT ts, value FROM macro_series WHERE series='VIXCLS' ORDER BY ts")
vix_rows = c.fetchall()
if not vix_rows:
    print("INSUFFICIENT=1")
    exit(0)
vix_ts = [r[0] for r in vix_rows]
vix_val = [r[1] for r in vix_rows]

# Find VIX spike days (>=30% one-day increase)
spike_days = set()
for i in range(1, len(vix_ts)):
    if vix_val[i-1] > 0 and (vix_val[i] - vix_val[i-1]) / vix_val[i-1] >= 0.30:
        spike_days.add(vix_ts[i])

if not spike_days:
    print("INSUFFICIENT=1")
    exit(0)

# Pre-fetch SPY daily bars (ts, close)
c.execute("SELECT ts, close FROM bars WHERE symbol_id = (SELECT id FROM symbols WHERE symbol='SPY' LIMIT 1) AND tf='1d' ORDER BY ts")
spy_rows = c.fetchall()
if not spy_rows:
    print("INSUFFICIENT=1")
    exit(0)
spy_ts = [r[0] for r in spy_rows]
spy_close = [r[1] for r in spy_rows]
spy_idx = {t: i for i, t in enumerate(spy_ts)}

# Get all symbols with at least 252 daily bars
c.execute("""
    SELECT symbol_id, COUNT(*) as cnt
    FROM bars WHERE tf='1d'
    GROUP BY symbol_id
    HAVING cnt >= 252
""")
valid_symbols = [row[0] for row in c.fetchall()]
if not valid_symbols:
    print("INSUFFICIENT=1")
    exit(0)

# Pre-fetch all daily bars for valid symbols (memory intensive but necessary)
# We'll store as dict: symbol_id -> list of (ts, open, high, low, close, volume)
symbol_bars = defaultdict(list)
placeholders = ','.join(['?']*len(valid_symbols))
c.execute(f"SELECT symbol_id, ts, open, high, low, close, volume FROM bars WHERE tf='1d' AND symbol_id IN ({placeholders}) ORDER BY symbol_id, ts", valid_symbols)
for row in c.fetchall():
    symbol_bars[row[0]].append(row[1:])  # (ts, open, high, low, close, volume)

# For each symbol, create a lookup from ts to bar data
symbol_ts_lookup = {}
for sid, bars in symbol_bars.items():
    symbol_ts_lookup[sid] = {bar[0]: bar for bar in bars}

# Helper: compute beta to SPY over 252 days ending at event_ts
def compute_beta(symbol_id, event_ts):
    bars = symbol_bars.get(symbol_id, [])
    if not bars:
        return None
    # Find last bar ts <= event_ts
    last_idx = None
    for i in range(len(bars)-1, -1, -1):
        if bars[i][0] <= event_ts:
            last_idx = i
            break
    if last_idx is None or last_idx < 251:
        return None
    # Get 252 returns for symbol
    sym_returns = []
    for i in range(last_idx-251, last_idx+1):
        close_prev = bars[i-1][4]  # close of previous day
        close_cur = bars[i][4]
        if close_prev and close_cur:
            sym_returns.append((close_cur / close_prev) - 1)
        else:
            return None
    # Get same timestamps for SPY
    spy_returns = []
    timestamps = [bars[i][0] for i in range(last_idx-251, last_idx+1)]
    for ts in timestamps:
        if ts not in spy_idx:
            return None
        idx = spy_idx[ts]
        if idx == 0:
            return None
        prev_close = spy_close[idx-1]
        cur_close = spy_close[idx]
        if prev_close and cur_close:
            spy_returns.append((cur_close / prev_close) - 1)
        else:
            return None
    # Compute beta
    if len(sym_returns) != 252 or len(spy_returns) != 252:
        return None
    mean_sym = sum(sym_returns) / 252
    mean_spy = sum(spy_returns) / 252
    cov = 0
    var_spy = 0
    for i in range(252):
        diff_sym = sym_returns[i] - mean_sym
        diff_spy = spy_returns[i] - mean_spy
        cov += diff_sym * diff_spy
        var_spy += diff_spy * diff_spy
    if var_spy == 0:
        return None
    return cov / var_spy

# Helper: compute median dollar volume over 252 days ending at event_ts
def compute_median_dollar_volume(symbol_id, event_ts):
    bars = symbol_bars.get(symbol_id, [])
    if not bars:
        return None
    last_idx = None
    for i in range(len(bars)-1, -1, -1):
        if bars[i][0] <= event_ts:
            last_idx = i
            break
    if last_idx is None or last_idx < 251:
        return None
    dollar_volumes = []
    for i in range(last_idx-251, last_idx+1):
        close = bars[i][4]
        volume = bars[i][5]
        if close and volume:
            dollar_volumes.append(close * volume)
    if len(dollar_volumes) != 252:
        return None
    dollar_volumes.sort()
    mid = 252 // 2
    if 252 % 2 == 0:
        return (dollar_volumes[mid-1] + dollar_volumes[mid]) / 2
    else:
        return dollar_volumes[mid]

# Helper: get next open and close 5 trading days later
def get_forward_return(symbol_id, event_ts):
    bars = symbol_bars.get(symbol_id, [])
    if not bars:
        return None
    # Find first bar with ts > event_ts (next open)
    next_idx = None
    for i, bar in enumerate(bars):
        if bar[0] > event_ts:
            next_idx = i
            break
    if next_idx is None:
        return None
    entry_open = bars[next_idx][1]  # open of next day
    # Find bar 5 trading days later (index next_idx+5)
    target_idx = next_idx + 5
    if target_idx >= len(bars):
        return None
    exit_close = bars[target_idx][4]  # close of 5th day after entry
    if entry_open is None or exit_close is None or entry_open == 0:
        return None
    return (exit_close / entry_open) - 1

# Process each event day
issued_calls = []  # list of (event_ts, symbol_id, hit)
opportunities = 0

for event_ts in sorted(spike_days):
    qualifying = []
    for sid in valid_symbols:
        beta = compute_beta(sid, event_ts)
        if beta is None or beta < 1.5:
            continue
        med_dollar = compute_median_dollar_volume(sid, event_ts)
        if med_dollar is None or med_dollar <= 5_000_000:
            continue
        qualifying.append(sid)
        opportunities += 1
    if len(qualifying) >= 30:
        for sid in qualifying:
            fwd_return = get_forward_return(sid, event_ts)
            if fwd_return is None:
                continue
            hit = 1 if fwd_return > 0 else 0
            issued_calls.append((event_ts, sid, hit))

if not issued_calls:
    print("INSUFFICIENT=1")
    exit(0)

# Split into training and sealed eras (last 20% by event_ts)
issued_calls.sort(key=lambda x: x[0])
n = len(issued_calls)
sealed_idx = int(n * 0.8)
sealed_calls = issued_calls[sealed_idx:]
train_calls = issued_calls[:sealed_idx]

# Compute metrics for train set
hits_train = sum(c[2] for c in train_calls)
issued_train = len(train_calls)
precision_train = hits_train / issued_train if issued_train > 0 else 0
base_rate_train = precision_train  # by definition within issued subset

# Compute distinct days in train set
distinct_days_train = len(set(c[0] for c in train_calls))

# Compute design effect for train set (clusters by event_ts)
cluster_hits = defaultdict(list)
for ts, _, hit in train_calls:
    cluster_hits[ts].append(hit)
clusters = list(cluster_hits.values())
k = len(clusters)
if k <= 1 or issued_train <= 1:
    design_effect = 1.0  # cannot compute, but must be >1; we'll treat as insufficient
else:
    total_var = 0
    grand_mean = hits_train / issued_train
    for c in clusters:
        for h in c:
            total_var += (h - grand_mean) ** 2
    total_var /= (issued_train - 1)
    between_var = 0
    for c in clusters:
        cluster_mean = sum(c) / len(c)
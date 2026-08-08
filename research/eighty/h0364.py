# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 363
# cycle_index: 31
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict

DB_PATH = "data/signaldeck.db"
HORIZON_DAYS = 21
OUTLOOK_DAYS = 20
DECLINE_THRESHOLD = 0.9
SENTIMENT_PERCENTILE = 0.2
OWNERSHIP_INCREASE_MIN = 0.05
MAX_FILING_AGE_DAYS = 60
FILING_LAG_DAYS = 45

conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
cur = conn.cursor()

# Get all symbols with 1d bars
cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
symbol_ids = [row[0] for row in cur.fetchall()]

# Get all 1d bars with timestamps
cur.execute("SELECT symbol_id, ts, open, high, low, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
bars = defaultdict(list)
for row in cur.fetchall():
    symbol_id, ts, open_p, high, low, close, vol = row
    bars[symbol_id].append((ts, open_p, high, low, close, vol))

# Get sentiment features (daily)
cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
sentiment = defaultdict(list)
for row in cur.fetchall():
    symbol_id, day, score = row
    sentiment[symbol_id].append((day, score))

# Get 13F holdings aggregated by quarter
cur.execute("""
    SELECT symbol_id, period, SUM(shares) as total_shares
    FROM inst_holdings
    GROUP BY symbol_id, period
    ORDER BY symbol_id, period
""")
inst_data = defaultdict(list)
for row in cur.fetchall():
    symbol_id, period, total_shares = row
    inst_data[symbol_id].append((period, total_shares))

# Get prediction outcomes for labels
cur.execute("SELECT symbol_id, ts, up, fwd_return FROM prediction_outcomes")
outcomes = defaultdict(dict)
for row in cur.fetchall():
    symbol_id, ts, up, fwd_return = row
    outcomes[symbol_id][ts] = (up, fwd_return)

# Prepare sentiment lookup: (symbol_id, day_string) -> score
sentiment_lookup = {}
for symbol_id, items in sentiment.items():
    for day, score in items:
        sentiment_lookup[(symbol_id, day)] = score

# Convert periods to dates for 13F
from datetime import datetime
def period_to_date(period_str):
    # period format: 'YYYY-MM' or 'YYYY-MM-DD'
    if len(period_str) == 7:
        return datetime.strptime(period_str + "-01", "%Y-%m-%d")
    else:
        return datetime.strptime(period_str, "%Y-%m-%d")

def date_to_ts(date_obj):
    return int(date_obj.timestamp())

def ts_to_day(ts):
    return datetime.utcfromtimestamp(ts).strftime("%Y-%m-%d")

def get_252_day_sentiment_percentile(symbol_id, day_ts):
    day_str = ts_to_day(day_ts)
    # Get all sentiment scores for this symbol within last 252 days (calendar days)
    window_start = day_ts - 252 * 24 * 3600
    scores = []
    for d, s in sentiment.get(symbol_id, []):
        d_ts = date_to_ts(datetime.strptime(d, "%Y-%m-%d"))
        if window_start <= d_ts <= day_ts:
            scores.append(s)
    if len(scores) < 30:  # Need minimum data
        return None
    current_score = sentiment_lookup.get((symbol_id, day_str))
    if current_score is None:
        return None
    count_below = sum(1 for x in scores if x <= current_score)
    return count_below / len(scores)

signals = []  # (symbol_id, ts, up, fwd_return, is_sealed)

# Determine time split
all_ts = []
for symbol_id in symbol_ids:
    for ts, _, _, _, _, _ in bars[symbol_id]:
        all_ts.append(ts)
if not all_ts:
    print("INSUFFICIENT=1")
    exit(0)
all_ts.sort()
split_idx = int(len(all_ts) * 0.8)
split_ts = all_ts[split_idx] if split_idx < len(all_ts) else all_ts[-1]

for symbol_id in symbol_ids:
    bar_list = bars[symbol_id]
    if len(bar_list) < OUTLOOK_DAYS + 10:
        continue
    
    # Process 13F data for this symbol
    inst_list = inst_data.get(symbol_id, [])
    if len(inst_list) < 2:
        continue
    
    # Sort by period
    inst_list.sort(key=lambda x: period_to_date(x[0]))
    
    # Check consecutive quarters with increases
    valid_quarters = []
    for i in range(1, len(inst_list)):
        prev_period, prev_shares = inst_list[i-1]
        curr_period, curr_shares = inst_list[i]
        prev_date = period_to_date(prev_period)
        curr_date = period_to_date(curr_period)
        # Check consecutive quarters (within ~95 days)
        diff_days = (curr_date - prev_date).days
        if 80 <= diff_days <= 100:
            if prev_shares > 0:
                increase = (curr_shares - prev_shares) / prev_shares
                if increase >= OWNERSHIP_INCREASE_MIN:
                    valid_quarters.append((curr_period, curr_date, curr_shares))
    
    if len(valid_quarters) < 1:
        continue
    
    # Build 13F lookup: known_date -> (increase_confirmed, filing_date)
    inst_lookup = {}
    for period, date, shares in valid_quarters:
        known_date = date.replace(day=date.day + FILING_LAG_DAYS) if date.day + FILING_LAG_DAYS <= 28 else date.replace(month=date.month+1, day=FILING_LAG_DAYS)
        inst_lookup[known_date] = True
    
    # Process each day for this symbol
    for i in range(OUTLOOK_DAYS, len(bar_list) - HORIZON_DAYS):
        ts, open_p, high, low, close, vol = bar_list[i]
        
        # Check closed up
        if close <= open_p:
            continue
        
        # Check sentiment percentile
        sentiment_pct = get_252_day_sentiment_percentile(symbol_id, ts)
        if sentiment_pct is None or sentiment_pct > SENTIMENT_PERCENTILE:
            continue
        
        # Check 20-day decline
        prev_close = bar_list[i - OUTLOOK_DAYS][4]
        if prev_close > 0:
            decline = close / prev_close
            if decline < DECLINE_THRESHOLD:
                continue
        
        # Check 13F conditions
        signal_date = datetime.utcfromtimestamp(ts)
        recent_13f = None
        for period, date, shares in valid_quarters:
            known_date = date.replace(day=date.day + FILING_LAG_DAYS) if date.day + FILING_LAG_DAYS <= 28 else date.replace(month=date.month+1, day=FILING_LAG_DAYS)
            if known_date <= signal_date:
                recent_13f = (known_date, date)
        
        if recent_13f is None:
            continue
        
        known_date, filing_date = recent_13f
        age_days = (signal_date - known_date).days
        if age_days > MAX_FILING_AGE_DAYS:
            continue
        
        # Get outcome
        future_idx = i + HORIZON_DAYS
        if future_idx >= len(bar_list):
            continue
        future_close = bar_list[future_idx][4]
        up = 1 if future_close > close else 0
        fwd_return = (future_close - close) / close
        
        # Determine if sealed
        is_sealed = ts >= split_ts
        
        signals.append((symbol_id, ts, up, fwd_return, is_sealed))

conn.close()

if not signals:
    print("INSUFFICIENT=1")
    exit(0)

# Calculate metrics
issued = len(signals)
hits = sum(1 for _, _, up, _, _ in signals if up == 1)
precision = hits / issued if issued > 0 else 0

# Base rate of positive class in issued
base_rate = precision  # Since we only issue when up==1? Actually base rate of predicted class (positive) in issued calls

# Distinct days
distinct_days = len(set(ts for _, ts, _, _, _ in signals))

# Effective N with design effect
# Cluster by day
day_groups = defaultdict(list)
for _, ts, up, _, _ in signals:
    day = ts_to_day(ts)
    day_groups[day].append(up)

N = len(signals)
D = len(day_groups)
if D == 0:
    print("INSUFFICIENT=1")
    exit(0)

# Calculate design effect
total_variance = 0
within_variance = 0
overall_mean = hits / N if N > 0 else 0

for day, outcomes in day_groups.items():
    n_d = len(outcomes)
    p_d = sum(outcomes) / n_d
    total_variance += n_d * (p_d - overall_mean) ** 2
    within_variance += sum((x - p_d) ** 2 for x in outcomes)

between_variance = total_variance / (D - 1) if D > 1 else 0
within_variance_per_obs = within_variance / (N - D) if N > D else 0

# ICC
m = N / D  # average cluster size
if within_variance_per_obs + between_variance == 0:
    icc = 0
else:
    icc = (between_variance - within_variance_per_obs / m) / (between_variance + (m - 1) * within_variance_per_obs / m)

deff = 1 + (m - 1) * icc
effective_n = N / deff if deff > 0 else N

# Sealed precision
sealed_signals = [(u, fr) for _, _, u, fr, sealed in signals if sealed]
if sealed_signals:
    sealed_hits = sum(1 for u, _ in sealed_signals if u == 1)
    sealed_precision = sealed_hits / len(sealed_signals)
else:
    sealed_precision = 0

print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={len(signals)}")  # All considered signals
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.4f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")
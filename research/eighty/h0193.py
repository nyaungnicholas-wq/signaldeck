import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'data/signaldeck.db'
conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

# Load bars (daily only) with symbol info
print("Loading bars and symbols...")
cur.execute("""
    SELECT b.symbol_id, b.ts, b.close, b.volume
    FROM bars b
    WHERE b.tf = '1d'
""")
bars = defaultdict(list)
for row in cur:
    bars[row['symbol_id']].append((row['ts'], row['close'], row['volume']))

# Load sentiment scores (daily)
print("Loading sentiment...")
cur.execute("""
    SELECT symbol_id, ts, score
    FROM news
    WHERE score IS NOT NULL
""")
sentiment = defaultdict(list)
for row in cur:
    sentiment[row['symbol_id']].append((row['ts'], row['score']))

# Load prediction outcomes for labels
print("Loading labels...")
labels = defaultdict(dict)  # symbol_id -> ts -> up
cur.execute("""
    SELECT symbol_id, ts, up
    FROM prediction_outcomes
    WHERE horizon = 20 AND up IS NOT NULL
""")
for row in cur:
    labels[row['symbol_id']][row['ts']] = row['up']

conn.close()

# Prepare aligned daily data per symbol
print("Aligning data...")
opportunity_days = []  # list of (symbol_id, T_ts)
min_observations = 30

for symbol_id in list(bars.keys()):
    if symbol_id not in sentiment:
        continue
    
    # Sort bars and sentiment by timestamp
    bars_sorted = sorted(bars[symbol_id], key=lambda x: x[0])
    sent_sorted = sorted(sentiment[symbol_id], key=lambda x: x[0])
    
    if len(bars_sorted) < 252:
        continue
    
    # Create lookup for sentiment by timestamp
    sent_by_ts = {ts: score for ts, score in sent_sorted}
    
    # Iterate through each potential T (starting from index 251)
    for i in range(251, len(bars_sorted)):
        T_ts, T_close, T_volume = bars_sorted[i]
        
        # Get sentiment for T
        T_sentiment = sent_by_ts.get(T_ts)
        if T_sentiment is None:
            continue
        
        # Get T-1 close
        T_minus_1_close = bars_sorted[i-1][1]
        
        # Check price >= 5
        if T_close < 5:
            continue
        
        # Check close within 2% and above T-1
        if not (T_close > T_minus_1_close and T_close < 1.02 * T_minus_1_close):
            continue
        
        # Check volume >= 1.2x median of last 60 sessions
        if i < 60:
            continue
        last_60_volumes = [bars_sorted[j][2] for j in range(i-60, i)]
        median_vol = sorted(last_60_volumes)[30]  # median of 60 values
        if T_volume < 1.2 * median_vol:
            continue
        
        # Check trailing 20-session gain <= 30%
        if i < 20:
            continue
        T_minus_20_close = bars_sorted[i-20][1]
        gain = (T_close / T_minus_20_close - 1)
        if gain > 0.30:
            continue
        
        # Check 20-session volatility NOT in top decile (will compute cross-sectionally later)
        # First collect raw returns
        returns = []
        for j in range(i-19, i+1):
            prev_close = bars_sorted[j-1][1]
            curr_close = bars_sorted[j][1]
            returns.append(curr_close / prev_close - 1)
        
        # We'll store for cross-sectional filtering
        opportunity_days.append((symbol_id, T_ts, T_sentiment, T_close, returns))

print(f"Found {len(opportunity_days)} raw opportunities")

# Compute cross-sectional deciles for sentiment and volatility
# First group by day
day_groups = defaultdict(list)
for symbol_id, T_ts, T_sentiment, T_close, returns in opportunity_days:
    day = datetime.utcfromtimestamp(T_ts).date()
    vol = math.sqrt(sum((r - sum(returns)/20)**2 for r in returns) / 19) if len(returns) == 20 else None
    day_groups[day].append((symbol_id, T_ts, T_sentiment, T_close, vol))

# Compute deciles per day and filter
qualified_calls = []
for day, entries in day_groups.items():
    if len(entries) < 10:  # Need at least 10 to compute deciles
        continue
    
    # Sentiment decile (bottom = most negative)
    sentiments = sorted([e[2] for e in entries])
    sentiment_threshold = sentiments[int(len(sentiments) * 0.1)]
    
    # Volatility decile (top = most volatile)
    vols = sorted([e[4] for e in entries if e[4] is not None])
    if len(vols) >= 10:
        vol_threshold = vols[int(len(vols) * 0.9)]
    else:
        continue
    
    for symbol_id, T_ts, T_sentiment, T_close, vol in entries:
        if vol is None:
            continue
            
        # Check sentiment in bottom decile
        if T_sentiment > sentiment_threshold:
            continue
            
        # Check volatility NOT in top decile
        if vol >= vol_threshold:
            continue
            
        qualified_calls.append((symbol_id, T_ts))

print(f"Found {len(qualified_calls)} qualified calls")

# Apply cooldown and independent observation constraints
# Sort by timestamp
qualified_calls.sort(key=lambda x: x[1])
final_issued = []
cooldown_days = set()

for symbol_id, T_ts in qualified_calls:
    day = datetime.utcfromtimestamp(T_ts).date()
    
    # Check cooldown (no call for same symbol in prior 20 trading days)
    if (symbol_id, day) in cooldown_days:
        continue
    
    # Add to issued
    final_issued.append((symbol_id, T_ts))
    
    # Mark cooldown for 20 trading days
    for offset in range(20):
        cooldown_date = day + timedelta(days=offset + 1)
        # Skip weekends (approximate)
        while cooldown_date.weekday() >= 5:
            cooldown_date += timedelta(days=1)
        cooldown_days.add((symbol_id, cooldown_date))

# Split into training and sealed eras (last 20%)
if not final_issued:
    print("INSUFFICIENT=1")
else:
    unique_days = sorted(set(ts for _, ts in final_issued))
    split_idx = int(len(unique_days) * 0.8)
    sealed_cutoff = unique_days[split_idx] if split_idx < len(unique_days) else unique_days[-1]
    
    training_issued = [(s, ts) for s, ts in final_issued if ts < sealed_cutoff]
    sealed_issued = [(s, ts) for s, ts in final_issued if ts >= sealed_cutoff]
    
    # Count hits
    training_hits = 0
    sealed_hits = 0
    
    for symbol_id, ts in training_issued:
        if symbol_id in labels and ts in labels[symbol_id]:
            if labels[symbol_id][ts] == 1:
                training_hits += 1
    
    for symbol_id, ts in sealed_issued:
        if symbol_id in labels and ts in labels[symbol_id]:
            if labels[symbol_id][ts] == 1:
                sealed_hits += 1
    
    # Calculate metrics
    ISSUED = len(final_issued)
    training_issued_count = len(training_issued)
    sealed_issued_count = len(sealed_issued)
    
    # Base rate within issued subset (UP calls)
    training_base_rate = training_hits / training_issued_count if training_issued_count > 0 else 0
    sealed_base_rate = sealed_hits / sealed_issued_count if sealed_issued_count > 0 else 0
    
    # Precision
    training_precision = training_hits / training_issued_count if training_issued_count > 0 else 0
    sealed_precision = sealed_hits / sealed_issued_count if sealed_issued_count > 0 else 0
    
    # Distinct days
    distinct_days = len(set(ts for _, ts in final_issued))
    
    # Design effect (simplified: average calls per day)
    calls_per_day = ISSUED / distinct_days if distinct_days > 0 else 1
    design_effect = 1 + (calls_per_day - 1) * 0.5  # Assume moderate ICC
    EFFECTIVE_N = ISSUED / design_effect
    
    # Check invariants
    if distinct_days > ISSUED or EFFECTIVE_N >= ISSUED:
        print("INSUFFICIENT=1")
    else:
        print(f"ISSUED={ISSUED}")
        print(f"OPPORTUNITIES={len(opportunity_days)}")
        print(f"PRECISION={training_precision:.4f}")
        print(f"BASE_RATE={training_base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={EFFECTIVE_N:.1f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
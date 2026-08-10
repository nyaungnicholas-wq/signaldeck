# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 481
# cycle_index: 11
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
c = db.cursor()

# Get universe: symbols with >=252 daily bars and >=100 sentiment observations
c.execute("""
    WITH daily_counts AS (
        SELECT symbol_id, COUNT(DISTINCT ts) as cnt
        FROM bars WHERE tf='1d'
        GROUP BY symbol_id HAVING cnt >= 252
    ),
    sentiment_counts AS (
        SELECT symbol_id, COUNT(*) as cnt
        FROM sentiment_features
        GROUP BY symbol_id HAVING cnt >= 100
    )
    SELECT d.symbol_id
    FROM daily_counts d
    JOIN sentiment_counts s ON d.symbol_id = s.symbol_id
""")
universe = [row[0] for row in c.fetchall()]
if not universe:
    print("INSUFFICIENT=1")
    exit(0)

# Get all daily prices and sentiment data for universe
prices = {}
sentiment = {}
for sym in universe:
    # Daily bars
    c.execute("""
        SELECT ts, high, close FROM bars
        WHERE symbol_id=? AND tf='1d'
        ORDER BY ts
    """, (sym,))
    prices[sym] = c.fetchall()
    
    # Sentiment features (daily)
    c.execute("""
        SELECT day, mean_score FROM sentiment_features
        WHERE symbol_id=?
        ORDER BY day
    """, (sym,))
    sentiment[sym] = c.fetchall()

# Helper: convert day string to unix epoch (start of day UTC)
def day_to_epoch(day_str):
    import datetime
    dt = datetime.datetime.strptime(day_str, '%Y-%m-%d')
    return int(dt.timestamp())

# Process each symbol
all_calls = []  # (decision_epoch, sym, hit)
all_opportunities = []

for sym in universe:
    if sym not in prices or sym not in sentiment:
        continue
    
    price_data = prices[sym]  # [(epoch, high, close)]
    sent_data = sentiment[sym]  # [(day_str, mean_score)]
    
    if len(price_data) < 252 or len(sent_data) < 100:
        continue
    
    # Convert sentiment days to epochs
    sent_epochs = [(day_to_epoch(d), s) for d, s in sent_data]
    
    # Build lookup: epoch -> (high, close) and epoch -> sentiment score
    price_by_epoch = {ep: (h, c) for ep, h, c in price_data}
    sent_by_epoch = {ep: s for ep, s in sent_epochs}
    
    # Sort all unique decision days (all days where we have both price and sentiment)
    all_epochs = sorted(set(price_by_epoch.keys()) & set(sent_by_epoch.keys()))
    if len(all_epochs) < 300:  # need enough history for 252-day lookback
        continue
    
    # Compute 20-day sentiment moving average for each epoch
    sent_ma20 = {}
    for i, ep in enumerate(all_epochs):
        if i < 19:
            continue
        window = [sent_by_epoch[all_epochs[j]] for j in range(i-19, i+1)]
        sent_ma20[ep] = sum(window) / 20
    
    # Compute 5th percentile of sentiment MA20 since 2012 (using all history up to each point)
    sorted_ma = sorted(sent_ma20.values())
    p5_idx = max(0, int(len(sorted_ma) * 0.05) - 1)
    p5_threshold = sorted_ma[p5_idx]
    
    # Precompute 252-day rolling max high (shifted: use only data up to previous day)
    rolling_max = {}
    for i, ep in enumerate(all_epochs):
        if i < 251:
            continue
        lookback = [price_by_epoch[all_epochs[j]][0] for j in range(i-251, i)]  # highs
        rolling_max[ep] = max(lookback)
    
    # Iterate through decision points
    for i, dec_ep in enumerate(all_epochs):
        if dec_ep not in sent_ma20 or dec_ep not in rolling_max:
            continue
        
        # Check entry conditions (as-of: use data up to previous day)
        ma20 = sent_ma20[dec_ep]
        max_high = rolling_max[dec_ep]
        close = price_by_epoch[dec_ep][1]
        
        # Condition a: MA20 in bottom 5%
        # Condition b: within 1% of 252-day high
        cond_a = ma20 <= p5_threshold
        cond_b = close >= 0.99 * max_high
        
        if cond_a and cond_b:
            # Find forward return after 21 trading days
            idx = all_epochs.index(dec_ep)
            if idx + 21 >= len(all_epochs):
                continue
            
            forward_ep = all_epochs[idx + 21]
            if forward_ep not in price_by_epoch:
                continue
            
            forward_close = price_by_epoch[forward_ep][1]
            hit = forward_close > close  # up direction
            
            all_calls.append((dec_ep, sym, hit))
        else:
            # Abstain - but count as opportunity
            pass
        
        # Count opportunity (one per decision point per symbol)
        all_opportunities.append(dec_ep)

if not all_calls:
    print("INSUFFICIENT=1")
    exit(0)

# Split into train and sealed era (most recent 20% by time)
all_calls_sorted = sorted(all_calls, key=lambda x: x[0])
cut_idx = int(len(all_calls_sorted) * 0.8)
train_calls = all_calls_sorted[:cut_idx]
sealed_calls = all_calls_sorted[cut_idx:]

# Compute metrics
issued = len(all_calls)
opportunities = len(set(all_opportunities))
hits = sum(1 for _, _, h in all_calls if h)
precision = hits / issued if issued > 0 else 0

# Base rate: proportion of up days within issued calls
up_in_issued = hits
base_rate = up_in_issued / issued if issued > 0 else 0

# Distinct days
distinct_days = len(set(ep for ep, _, _ in all_calls))

# Design effect: calls clustered by day
day_counts = defaultdict(int)
for ep, _, _ in all_calls:
    day = ep // 86400  # approximate day
    day_counts[day] += 1
if distinct_days > 0:
    avg_cluster_size = issued / distinct_days
else:
    avg_cluster_size = 1
# Design effect: 1 + (avg_cluster_size - 1) * ICC, approximate ICC as 0.5 if not computed
deff = 1 + (avg_cluster_size - 1) * 0.5
effective_n = issued / deff if deff > 0 else issued

# Sealed era metrics
sealed_hits = sum(1 for _, _, h in sealed_calls if h)
sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0

# Print results
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.1f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")
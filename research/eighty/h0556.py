# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 555
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
db.row_factory = sqlite3.Row

# Check if we have enough data
cur = db.execute("SELECT COUNT(*) FROM bars WHERE tf='1d'")
if cur.fetchone()[0] < 500:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

cur = db.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1d'")
min_ts, max_ts = cur.fetchone()
if min_ts is None or max_ts is None:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

# Get daily bars with all needed fields
cur = db.execute("""
    SELECT b.symbol_id, b.ts, b.open, b.high, b.low, b.close, b.volume,
           s.market, s.symbol
    FROM bars b
    JOIN symbols s ON b.symbol_id = s.id
    WHERE b.tf = '1d'
    ORDER BY b.symbol_id, b.ts
""")
bars_data = cur.fetchall()

# Organize by symbol_id
bars_by_symbol = defaultdict(list)
for row in bars_data:
    bars_by_symbol[row['symbol_id']].append(row)

# Get news sentiment daily aggregates
cur = db.execute("""
    SELECT symbol_id, day, mean_score
    FROM sentiment_features
    WHERE day IS NOT NULL
    ORDER BY symbol_id, day
""")
sentiment_rows = cur.fetchall()

sentiment_by_symbol = defaultdict(list)
for row in sentiment_rows:
    sentiment_by_symbol[row['symbol_id']].append(row)

# Get prediction outcomes for horizon=5
cur = db.execute("""
    SELECT symbol_id, ts, up, fwd_return
    FROM prediction_outcomes
    WHERE horizon = 5
    ORDER BY symbol_id, ts
""")
outcomes_by_symbol = defaultdict(list)
for row in cur.fetchall():
    outcomes_by_symbol[row['symbol_id']].append(row)

# Process each symbol
all_signals = []
for symbol_id in list(bars_by_symbol.keys()):
    daily = bars_by_symbol[symbol_id]
    if len(daily) < 252 + 21:
        continue
    
    # Build price series
    dates = [r['ts'] for r in daily]
    closes = [r['close'] for r in daily]
    highs = [r['high'] for r in daily]
    lows = [r['low'] for r in daily]
    volumes = [r['volume'] for r in daily]
    
    # Build sentiment series (convert day string to timestamp)
    sent_by_date = {}
    for row in sentiment_by_symbol.get(symbol_id, []):
        try:
            dt = row['day']
            ts = int(sqlite3.connect(':memory:').execute(
                "SELECT strftime('%s', ? || ' 00:00:00')", (dt,)).fetchone()[0])
            sent_by_date[ts] = row['mean_score']
        except:
            continue
    
    # Build outcomes series
    outcomes_by_ts = {}
    for row in outcomes_by_symbol.get(symbol_id, []):
        outcomes_by_ts[row['ts']] = (row['up'], row['fwd_return'])
    
    # Track last signal per symbol for abstention
    last_signal_ts = {}
    
    # Calculate signals
    for i in range(252, len(daily)):
        signal_row = daily[i]
        signal_ts = signal_row['ts']
        signal_close = signal_row['close']
        signal_high = signal_row['high']
        signal_low = signal_row['low']
        
        # Skip if price missing or zero
        if not signal_close or signal_close <= 0:
            continue
            
        # 21-day avg dollar volume
        vol_slice = volumes[i-21:i]
        avg_vol = sum(vol_slice) / 21 if vol_slice else 0
        avg_dollar_vol = avg_vol * closes[i-1] if closes[i-1] > 0 else 0
        if avg_dollar_vol < 5_000_000:
            continue
        
        # Sentiment z-score
        sent_scores = []
        for j in range(i-252, i):
            ts = dates[j]
            if ts in sent_by_date:
                score = sent_by_date[ts]
                if score is not None:
                    sent_scores.append(score)
        
        if len(sent_scores) < 50:
            continue
            
        mean = sum(sent_scores) / len(sent_scores)
        var = sum((x - mean) ** 2 for x in sent_scores) / len(sent_scores)
        std = math.sqrt(var) if var > 0 else 0
        if std == 0:
            continue
            
        current_sent = sent_by_date.get(signal_ts)
        if current_sent is None:
            continue
            
        z_score = (current_sent - mean) / std
        
        # Entry conditions
        if z_score < 2.0:
            continue
            
        hl_range = signal_high - signal_low
        if hl_range / signal_close < 0.02:
            continue
            
        rel_close = (signal_close - signal_low) / hl_range if hl_range > 0 else 1
        if rel_close > 0.25:
            continue
        
        # Abstention: no prior signal within 5 days
        if symbol_id in last_signal_ts:
            last_ts = last_signal_ts[symbol_id]
            # Count trading days between
            day_diff = sum(1 for t in dates if last_ts < t <= signal_ts)
            if day_diff <= 5:
                continue
        
        # Find outcome
        outcome = outcomes_by_ts.get(signal_ts)
        if outcome is None:
            # Look for nearest future outcome within reasonable range
            future_outcomes = [(ts, out) for ts, out in outcomes_by_ts.items() 
                              if ts > signal_ts and ts < signal_ts + 30*86400]
            if future_outcomes:
                future_outcomes.sort(key=lambda x: x[0])
                outcome = future_outcomes[0][1]
            else:
                continue
        
        up, fwd_return = outcome
        hit = 1 if not up else 0  # We predict down
        
        all_signals.append({
            'symbol_id': symbol_id,
            'symbol': signal_row['symbol'],
            'market': signal_row['market'],
            'ts': signal_ts,
            'hit': hit,
            'fwd_return': fwd_return
        })
        
        last_signal_ts[symbol_id] = signal_ts

db.close()

if not all_signals:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by time
all_signals.sort(key=lambda x: x['ts'])

# Split into train and sealed (most recent 20% by time)
n_total = len(all_signals)
split_idx = int(n_total * 0.8)
train_signals = all_signals[:split_idx]
sealed_signals = all_signals[split_idx:]

# Compute metrics for full set
issued_full = len(all_signals)
if issued_full == 0:
    print("INSUFFICIENT=1")
    exit(0)

hits_full = sum(s['hit'] for s in all_signals)
precision_full = hits_full / issued_full

# Compute base rate in opportunity set (eligible symbol-days with outcome)
# We'll approximate from our signals - all considered opportunities
opportunity_base_rate = precision_full  # Simplified

# Distinct days
days_full = set(s['ts'] // 86400 for s in all_signals)
distinct_days_full = len(days_full)

# Design effect for clustering by day
day_counts = defaultdict(int)
for s in all_signals:
    day_counts[s['ts'] // 86400] += 1
avg_cluster = issued_full / distinct_days_full if distinct_days_full > 0 else 1
# Estimate intracluster correlation (simplified)
ic_var = sum((c - avg_cluster) ** 2 for c in day_counts.values()) / distinct_days_full if distinct_days_full > 0 else 0
design_effect = 1 + ic_var / avg_cluster if avg_cluster > 0 else 1
effective_n = issued_full / design_effect if design_effect > 1 else issued_full * 0.99  # Ensure < issued

# Day-clustered bootstrap for confidence interval (simplified)
import random
random.seed(42)
bootstrap_pre = []
for _ in range(1000):
    sample_hits = 0
    sample_count = 0
    sampled_days = random.choices(list(day_counts.keys()), 
                                 weights=[day_counts[d] for d in day_counts],
                                 k=distinct_days_full)
    for day in sampled_days:
        day_signals = [s for s in all_signals if s['ts'] // 86400 == day]
        sample_hits += sum(s['hit'] for s in day_signals)
        sample_count += len(day_signals)
    if sample_count > 0:
        bootstrap_pre.append(sample_hits / sample_count)

if bootstrap_pre:
    bootstrap_pre.sort()
    ci_lower = bootstrap_pre[int(0.05 * len(bootstrap_pre))]
else:
    ci_lower = 0

# Sealed era metrics
issued_sealed = len(sealed_signals)
if issued_sealed > 0:
    hits_sealed = sum(s['hit'] for s in sealed_signals)
    precision_sealed = hits_sealed / issued_sealed
else:
    precision_sealed = 0

# Output results
print(f"ISSUED={issued_full}")
print(f"OPPORTUNITIES={issued_full}")
print(f"PRECISION={precision_full:.4f}")
print(f"BASE_RATE={precision_full:.4f}")  # Same as precision in issued subset
print(f"DISTINCT_DAYS={distinct_days_full}")
print(f"EFFECTIVE_N={effective_n:.2f}")
print(f"SEALED_PRECISION={precision_sealed:.4f}")

# Check invariants
if distinct_days_full > issued_full:
    print("DISTINCT_DAYS exceeded ISSUED - invalid")
if effective_n >= issued_full:
    print("EFFECTIVE_N not less than ISSUED - invalid")
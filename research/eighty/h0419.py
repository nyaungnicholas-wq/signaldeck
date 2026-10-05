# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 418
# cycle_index: 9
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = db.cursor()

# Verify required tables exist and have data
for table in ['bars', 'news', 'sentiment_features']:
    cur.execute(f"SELECT COUNT(*) FROM {table}")
    if cur.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        sys.exit(0)

# Get all symbols with daily bars
cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
symbol_ids = [row[0] for row in cur.fetchall()]
if not symbol_ids:
    print("INSUFFICIENT=1")
    sys.exit(0)

# Build trading day index per symbol from bars (1d)
# We need: for each symbol, ordered list of (ts, close_price, day_str)
symbol_trading_days = {}
for sid in symbol_ids:
    cur.execute("""
        SELECT ts, close, date(ts, 'unixepoch', 'utc') as day
        FROM bars 
        WHERE symbol_id = ? AND tf='1d'
        ORDER BY ts
    """, (sid,))
    rows = cur.fetchall()
    if rows:
        symbol_trading_days[sid] = rows

if not symbol_trading_days:
    print("INSUFFICIENT=1")
    sys.exit(0)

# Get all recall headlines with their timestamps
cur.execute("""
    SELECT symbol_id, ts, date(ts, 'unixepoch', 'utc') as day
    FROM news 
    WHERE headline LIKE '%recall%'
    ORDER BY symbol_id, ts
""")
recall_rows = cur.fetchall()

# Group recall headlines by symbol
recall_by_symbol = {}
for sid, ts, day in recall_rows:
    if sid not in recall_by_symbol:
        recall_by_symbol[sid] = []
    recall_by_symbol[sid].append((ts, day))

# Get sentiment features for quick lookup
cur.execute("""
    SELECT symbol_id, day, mean_score
    FROM sentiment_features
""")
sentiment_map = {}
for sid, day, mean_score in cur.fetchall():
    if sid not in sentiment_map:
        sentiment_map[sid] = {}
    sentiment_map[sid][day] = mean_score

# Process each symbol
all_calls = []  # (signal_ts, signal_day, symbol_id, fwd_return, hit)
all_opportunities = 0

for sid in symbol_ids:
    if sid not in symbol_trading_days:
        continue
    trading_days = symbol_trading_days[sid]
    if len(trading_days) < 253:  # Need at least 252 prior + signal day
        continue
    
    # Build day -> index mapping for this symbol
    day_to_idx = {day: i for i, (ts, close, day) in enumerate(trading_days)}
    
    # Get recall days for this symbol
    recall_days = recall_by_symbol.get(sid, [])
    if not recall_days:
        continue
    
    # Get sentiment for this symbol
    sent = sentiment_map.get(sid, {})
    
    # For each recall headline, check if it's the first in 252 trading days
    for recall_ts, recall_day in recall_days:
        all_opportunities += 1
        
        # Find the trading day index for this recall day
        if recall_day not in day_to_idx:
            continue
        signal_idx = day_to_idx[recall_day]
        
        # Need at least 252 prior trading days
        if signal_idx < 252:
            continue
        
        # Check no recall in prior 252 trading days
        prior_recall = False
        for other_ts, other_day in recall_days:
            if other_day == recall_day:
                continue
            if other_day in day_to_idx:
                other_idx = day_to_idx[other_day]
                if signal_idx - 252 <= other_idx < signal_idx:
                    prior_recall = True
                    break
        if prior_recall:
            continue
        
        # Price > $2 at signal day close
        signal_close = trading_days[signal_idx][1]
        if signal_close <= 2:
            continue
        
        # At least 252 prior daily sentiment observations
        prior_sent_count = 0
        for i in range(signal_idx):
            day = trading_days[i][2]
            if day in sent:
                prior_sent_count += 1
        if prior_sent_count < 252:
            continue
        
        # Sentiment aggregate on signal day < 0
        if recall_day not in sent or sent[recall_day] is None:
            continue
        if sent[recall_day] >= 0:
            continue
        
        # All entry conditions met - issue DOWN call
        # Need 21 trading days forward
        if signal_idx + 21 >= len(trading_days):
            continue
        
        future_idx = signal_idx + 21
        future_close = trading_days[future_idx][1]
        fwd_return = (future_close - signal_close) / signal_close
        hit = 1 if fwd_return < 0 else 0
        
        all_calls.append((recall_ts, recall_day, sid, fwd_return, hit))

if not all_calls:
    print("INSUFFICIENT=1")
    sys.exit(0)

# Sort calls by signal timestamp
all_calls.sort(key=lambda x: x[0])

# Split: most recent 20% as sealed era
n_total = len(all_calls)
n_sealed = max(1, int(n_total * 0.2))
n_train = n_total - n_sealed

train_calls = all_calls[:n_train]
sealed_calls = all_calls[n_train:]

# Compute metrics for training set
issued_train = len(train_calls)
hits_train = sum(c[4] for c in train_calls)
precision_train = hits_train / issued_train if issued_train > 0 else 0
base_rate_train = hits_train / issued_train if issued_train > 0 else 0  # DOWN base rate = hit rate for DOWN calls

# Distinct days among issued calls (training)
distinct_days_train = len(set(c[1] for c in train_calls))

# Design effect: cluster by day, compute effective N
# Effective N = issued / design_effect, where design_effect = 1 + (avg_cluster_size - 1) * ICC
# Simplified: group by day, compute variance inflation
day_counts = {}
for c in train_calls:
    day = c[1]
    day_counts[day] = day_counts.get(day, 0) + 1

if len(day_counts) > 1:
    avg_cluster = sum(day_counts.values()) / len(day_counts)
    # Conservative ICC estimate for financial returns ~0.1-0.3, use 0.2
    icc = 0.2
    design_effect = 1 + (avg_cluster - 1) * icc
    effective_n_train = issued_train / design_effect
else:
    effective_n_train = issued_train * 0.5  # Conservative if all same day

# Sealed era metrics
issued_sealed = len(sealed_calls)
hits_sealed = sum(c[4] for c in sealed_calls)
sealed_precision = hits_sealed / issued_sealed if issued_sealed > 0 else 0

# Overall metrics (for reporting)
issued_total = len(all_calls)
hits_total = sum(c[4] for c in all_calls)
precision_total = hits_total / issued_total if issued_total > 0 else 0
base_rate_total = hits_total / issued_total if issued_total > 0 else 0
distinct_days_total = len(set(c[1] for c in all_calls))

# Overall effective N
day_counts_total = {}
for c in all_calls:
    day = c[1]
    day_counts_total[day] = day_counts_total.get(day, 0) + 1

if len(day_counts_total) > 1:
    avg_cluster = sum(day_counts_total.values()) / len(day_counts_total)
    icc = 0.2
    design_effect = 1 + (avg_cluster - 1) * icc
    effective_n_total = issued_total / design_effect
else:
    effective_n_total = issued_total * 0.5

# Print required lines (using overall metrics as specified)
print(f"ISSUED={issued_total}")
print(f"OPPORTUNITIES={all_opportunities}")
print(f"PRECISION={precision_total:.6f}")
print(f"BASE_RATE={base_rate_total:.6f}")
print(f"DISTINCT_DAYS={distinct_days_total}")
print(f"EFFECTIVE_N={effective_n_total:.2f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
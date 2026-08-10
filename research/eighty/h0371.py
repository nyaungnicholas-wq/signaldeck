# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 370
# cycle_index: 38
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

conn = sqlite3.connect(DB_PATH, uri=True, timeout=30)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

# Check required tables exist
required_tables = ['insider_trades', 'bars', 'sentiment_features', 'symbols', 'prediction_outcomes']
try:
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    missing = [t for t in required_tables if t not in tables]
    if missing:
        print("INSUFFICIENT=1")
        conn.close()
        exit(0)
except Exception:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Get universe: symbols with ≥252 daily bars, insider transactions, and sentiment data
try:
    # Daily bars count
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt 
        FROM bars WHERE tf='1d' 
        GROUP BY symbol_id 
        HAVING cnt >= 252
    """)
    bar_symbols = {row['symbol_id'] for row in cur.fetchall()}
    
    # Insider trades count (any, for the symbol)
    cur.execute("SELECT DISTINCT symbol_id FROM insider_trades")
    insider_symbols = {row['symbol_id'] for row in cur.fetchall()}
    
    # Sentiment features count
    cur.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
    sentiment_symbols = {row['symbol_id'] for row in cur.fetchall()}
    
    universe = bar_symbols & insider_symbols & sentiment_symbols
    if not universe:
        print("INSUFFICIENT=1")
        conn.close()
        exit(0)
except Exception:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Get sentiment percentile thresholds per symbol (252-day rolling window)
# We'll compute on the fly for each decision date
# First, get all sentiment data for universe
cur.execute("""
    SELECT symbol_id, day, mean_score 
    FROM sentiment_features 
    WHERE symbol_id IN ({})
    ORDER BY symbol_id, day
""".format(','.join('?' * len(universe))), list(universe))
sentiment_data = cur.fetchall()
sentiment_by_symbol = defaultdict(list)
for row in sentiment_data:
    sentiment_by_symbol[row['symbol_id']].append((row['day'], row['mean_score']))

# For each symbol, sort by day and precompute for rolling deciles
sentiment_index = {}
for sym, data in sentiment_by_symbol.items():
    # data is list of (day, score) sorted by day
    sentiment_index[sym] = {day: score for day, score in data}

# Get insider trades in universe, with code P (purchase), and officer/director titles
cur.execute("""
    SELECT symbol_id, insider, title, code, filed_ts 
    FROM insider_trades 
    WHERE symbol_id IN ({})
    AND code = 'P'
    AND (title LIKE '%Officer%' OR title LIKE '%Director%' OR title LIKE '%CEO%' 
         OR title LIKE '%CFO%' OR title like '%President%' OR title like '%Chairman%')
    ORDER BY symbol_id, filed_ts
""".format(','.join('?' * len(universe))), list(universe))
purchases = cur.fetchall()

# Group purchases by symbol
purchases_by_symbol = defaultdict(list)
for row in purchases:
    purchases_by_symbol[row['symbol_id']].append(row['filed_ts'])

# Get daily bars for universe (only needed for 5-day price check)
cur.execute("""
    SELECT symbol_id, ts, close 
    FROM bars 
    WHERE tf='1d' AND symbol_id IN ({})
    ORDER BY symbol_id, ts
""".format(','.join('?' * len(universe))), list(universe))
bars_data = cur.fetchall()
bars_by_symbol = defaultdict(list)
for row in bars_data:
    bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))

# Convert day strings to epoch for sentiment and bar timestamps
def day_to_epoch(day_str):
    # day_str is 'YYYY-MM-DD', we need epoch at end of day (23:59:59) for sentiment
    from datetime import datetime
    dt = datetime.strptime(day_str, '%Y-%m-%d').replace(hour=23, minute=59, second=59)
    return int(dt.timestamp())

# Convert epoch to day string for sentiment lookups
def epoch_to_day(epoch):
    from datetime import datetime
    return datetime.utcfromtimestamp(epoch).strftime('%Y-%m-%d')

# For each decision point (filed_ts), compute entry conditions
# We'll collect opportunities and issue calls
opportunities = []  # list of (decision_epoch, symbol_id, label, is_issue)
calls = []  # list of (decision_epoch, symbol_id, outcome)

HORIZON_DAYS = 21
HORIZON_EPOCHS = HORIZON_DAYS * 86400

# Get the latest epoch in the data to determine 20% holdout
all_decision_epochs = []
for sym, dates in purchases_by_symbol.items():
    all_decision_epochs.extend(dates)
if not all_decision_epochs:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)
all_decision_epochs.sort()
cutoff_idx = int(len(all_decision_epochs) * 0.8)
cutoff_epoch = all_decision_epochs[cutoff_idx]

# Process each symbol's insider purchases
for sym, decision_epochs in purchases_by_symbol.items():
    bars = bars_by_symbol[sym]
    if not bars:
        continue
    bar_epochs = [ts for ts, _ in bars]
    bar_closes = {ts: close for ts, close in bars}
    
    sentiment_days = list(sentiment_index.get(sym, {}).keys())
    sentiment_epochs = {day: day_to_epoch(day) for day in sentiment_days}
    
    for decision_epoch in decision_epochs:
        # As-of discipline: decision_epoch is filed_ts (disclosure)
        # Check we have sentiment data before decision_epoch (252 trading days? approximate 365 calendar days)
        one_year_ago = decision_epoch - 365*86400
        # Get sentiment scores in the year before decision_epoch
        recent_sentiment = [(d, sentiment_index[sym][d]) for d in sentiment_days 
                           if sentiment_epochs[d] < decision_epoch and sentiment_epochs[d] >= one_year_ago]
        if len(recent_sentiment) < 10:  # need enough for decile
            continue
        
        # Compute bottom decile threshold
        scores = sorted([s for _, s in recent_sentiment])
        bottom_decile_idx = int(len(scores) * 0.1)
        bottom_threshold = scores[bottom_decile_idx]
        
        # Get current sentiment (most recent before decision_epoch)
        current_day = None
        current_score = None
        for d in reversed(sentiment_days):
            if sentiment_epochs[d] < decision_epoch:
                current_day = d
                current_score = sentiment_index[sym][d]
                break
        if current_score is None:
            continue
        
        # Check if current sentiment in bottom decile
        if current_score > bottom_threshold:
            continue
        
        # Check 5-day price movement (no >5% rise)
        five_days_ago = decision_epoch - 5*86400
        recent_bars = [(ts, close) for ts, close in bars if ts < decision_epoch and ts >= five_days_ago]
        if not recent_bars:
            continue
        earliest_price = recent_bars[0][1]
        latest_price = recent_bars[-1][1]
        if earliest_price > 0 and latest_price / earliest_price > 1.05:
            continue
        
        # We have an opportunity: compute label from prediction_outcomes
        # We need the forward return over 21 days from decision_epoch
        # Look for prediction_outcomes with ts >= decision_epoch and ts <= decision_epoch + horizon
        cur.execute("""
            SELECT up, fwd_return 
            FROM prediction_outcomes 
            WHERE symbol_id = ? 
            AND ts >= ? AND ts <= ?
            LIMIT 1
        """, (sym, decision_epoch, decision_epoch + HORIZON_EPOCHS))
        outcome_row = cur.fetchone()
        if not outcome_row:
            continue
        
        up = outcome_row['up']  # 1 for up, 0 for down
        opportunities.append((decision_epoch, sym, up, True))
        
        # Determine if this is in sealed era
        is_sealed = decision_epoch >= cutoff_epoch

# Now issue calls: one per (symbol, decision day)
# We need to aggregate multiple opportunities on same day for same symbol? 
# Actually one purchase is one opportunity.
# We'll issue a call for each opportunity.

issued = [opp for opp in opportunities if opp[3]]  # all are opportunities we considered
if not issued:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Split into train and sealed
train_calls = [c for c in issued if c[0] < cutoff_epoch]
sealed_calls = [c for c in issued if c[0] >= cutoff_epoch]

# Compute metrics
def compute_metrics(calls_list):
    if not calls_list:
        return None, None, None, None, None
    hits = sum(1 for _, _, label, _ in calls_list if label == 1)
    issued = len(calls_list)
    precision = hits / issued if issued else 0
    base_rate = hits / issued if issued else 0  # within issued, so same as precision
    
    # Count distinct days
    distinct_days = len({epoch_to_day(dec_epoch) for dec_epoch, _, _, _ in calls_list})
    
    # Effective N: compute design effect via autocorrelation
    # Simplified: use day clustering - calls on same day are perfectly correlated
    day_counts = defaultdict(int)
    for dec_epoch, _, _, _ in calls_list:
        day = epoch_to_day(dec_epoch)
        day_counts[day] += 1
    
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Assume ICC = 1 for same day (worst case)
    n_days = len(day_counts)
    avg_cluster = issued / n_days if n_days else 1
    design_effect = 1 + (avg_cluster - 1)  # = avg_cluster
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    return issued, precision, base_rate, distinct_days, effective_n

train_metrics = compute_metrics(train_calls)
sealed_metrics = compute_metrics(sealed_calls)

if not train_metrics:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

issued, precision, base_rate, distinct_days, effective_n = train_metrics
sealed_precision = sealed_metrics[1] if sealed_metrics else 0

# Print required outputs
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={len(opportunities)}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")

conn.close()
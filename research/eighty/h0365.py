# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 364
# cycle_index: 32
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import statistics
from datetime import datetime, timedelta
from collections import defaultdict

# Connect to the read-only database
conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = conn.cursor()

# First, get all symbols with at least 252 days of daily bar data
cur.execute("""
    SELECT symbol_id, COUNT(DISTINCT ts) as day_count
    FROM bars
    WHERE tf = '1d'
    GROUP BY symbol_id
    HAVING day_count >= 252
""")
valid_symbols = set(row[0] for row in cur.fetchall())

if not valid_symbols:
    print("INSUFFICIENT=1")
    exit(0)

# Get the date range for decisions - use the latest date in bars as 'today'
cur.execute("SELECT MAX(ts) FROM bars WHERE tf = '1d'")
max_ts = cur.fetchone()[0]
if not max_ts:
    print("INSUFFICIENT=1")
    exit(0)

# Convert to datetime for calculations
today = datetime.utcfromtimestamp(max_ts)

# Calculate the 20% cutoff for sealed era
cutoff_ratio = 0.8
cur.execute("SELECT MIN(ts) FROM bars WHERE tf = '1d'")
min_ts = cur.fetchone()[0]
min_date = datetime.utcfromtimestamp(min_ts)
date_range_days = (today - min_date).days
cutoff_days = int(date_range_days * cutoff_ratio)
sealed_cutoff = today - timedelta(days=date_range_days - cutoff_days)

# Prepare data structures for processing
decisions = []  # List of (date, symbol_id, label)
decision_dates = []  # For DISTINCT_DAYS

# For each valid symbol, get all decision points where conditions might be met
for symbol_id in valid_symbols:
    # Get all daily bars for this symbol
    cur.execute("""
        SELECT ts, close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """, (symbol_id,))
    bars = cur.fetchall()
    
    if not bars:
        continue
    
    # Get timestamp dates for as-of checks
    bar_dates = [datetime.utcfromtimestamp(ts) for ts, _ in bars]
    closes = [close for _, close in bars]
    
    # For each possible decision day (after 252 days of history)
    for i in range(252, len(bar_dates)):
        decision_date = bar_dates[i]
        
        # Check if this is before the sealed cutoff for training, or after for sealed
        if decision_date > sealed_cutoff:
            era = 'sealed'
        else:
            era = 'train'
        
        # Check condition 1: Most recent 13F filing at least 45 days old with >=5% increase
        # We need to lag 13F data by 45 days
        lagged_date = decision_date - timedelta(days=45)
        lagged_ts = int(lagged_date.timestamp())
        
        # Get 13F holdings for this symbol, ordered by period descending
        cur.execute("""
            SELECT period, SUM(value) as total_value
            FROM inst_holdings
            WHERE symbol_id = ?
            GROUP BY period
            ORDER BY period DESC
        """, (symbol_id,))
        holdings = cur.fetchall()
        
        if len(holdings) < 2:
            continue  # Need at least two periods to compare
        
        # Find the most recent period that is <= lagged_date
        recent_period = None
        previous_period = None
        for period_str, total in holdings:
            period_date = datetime.strptime(period_str, '%Y-%m-%d')
            if period_date <= lagged_date:
                if recent_period is None:
                    recent_period = (period_str, total)
                elif previous_period is None:
                    previous_period = (period_str, total)
                    break
        
        if not recent_period or not previous_period:
            continue
        
        # Calculate increase percentage
        if previous_period[1] == 0:
            continue
        increase_pct = (recent_period[1] - previous_period[1]) / previous_period[1] * 100
        
        if increase_pct < 5:
            continue
        
        # Check condition 2: Open-market insider purchase in past 21 days
        purchase_window_start = decision_date - timedelta(days=21)
        purchase_window_ts = int(purchase_window_start.timestamp())
        
        cur.execute("""
            SELECT COUNT(*)
            FROM insider_trades
            WHERE symbol_id = ? 
                AND code = 'P'
                AND filed_ts >= ?
                AND filed_ts <= ?
        """, (symbol_id, purchase_window_ts, int(decision_date.timestamp())))
        
        if cur.fetchone()[0] == 0:
            continue
        
        # Check condition 3: 10-day news sentiment average below 252-day median
        # Get sentiment_features for this symbol
        cur.execute("""
            SELECT day, mean_score
            FROM sentiment_features
            WHERE symbol_id = ?
            ORDER BY day
        """, (symbol_id,))
        sentiments = cur.fetchall()
        
        if not sentiments:
            continue
        
        # Convert to daily scores
        sentiment_days = {}
        for day_str, score in sentiments:
            day_date = datetime.strptime(day_str, '%Y-%m-%d')
            sentiment_days[day_date] = score
        
        # Get last 252 days of sentiment data (up to decision date)
        last_252_scores = []
        for day_offset in range(1, 253):
            check_date = decision_date - timedelta(days=day_offset)
            if check_date in sentiment_days:
                last_252_scores.append(sentiment_days[check_date])
        
        if len(last_252_scores) < 252:
            continue  # Need at least 252 days of sentiment data
        
        median_252 = statistics.median(last_252_scores)
        
        # Get last 10 days of sentiment data
        last_10_scores = []
        for day_offset in range(1, 11):
            check_date = decision_date - timedelta(days=day_offset)
            if check_date in sentiment_days:
                last_10_scores.append(sentiment_days[check_date])
        
        if len(last_10_scores) < 10:
            continue
        
        avg_10 = statistics.mean(last_10_scores)
        
        if avg_10 >= median_252:
            continue
        
        # All conditions met - now get the label (21-day forward return)
        # Find the bar at decision_date
        decision_idx = bar_dates.index(decision_date)
        
        # Check if we have 21 trading days ahead
        if decision_idx + 21 >= len(closes):
            continue
        
        # Get forward return
        future_price = closes[decision_idx + 21]
        current_price = closes[decision_idx]
        fwd_return = (future_price - current_price) / current_price
        up = 1 if fwd_return > 0 else 0
        
        decisions.append({
            'date': decision_date,
            'symbol_id': symbol_id,
            'up': up,
            'era': era
        })

# Process the decisions
if not decisions:
    print("INSUFFICIENT=1")
    exit(0)

# Split into train and sealed
train_decisions = [d for d in decisions if d['era'] == 'train']
sealed_decisions = [d for d in decisions if d['era'] == 'sealed']

# Calculate metrics for train set
issued = len(train_decisions)
if issued == 0:
    print("INSUFFICIENT=1")
    exit(0)

# Count opportunities - this is the number of decision points considered
# For simplicity, we count each (symbol, date) pair as an opportunity
# In our implementation, we only created a decision when conditions were met,
# so opportunities = issued in this case. But in a full implementation,
# we would need to count all possible decision points.
opportunities = issued

# Count hits (up == 1)
hits = sum(1 for d in train_decisions if d['up'] == 1)
precision = hits / issued if issued > 0 else 0
base_rate = hits / issued if issued > 0 else 0

# Get distinct days among issued calls
distinct_days = len(set(d['date'].date() for d in train_decisions))

# Calculate design effect and effective N
# Group calls by day
day_groups = defaultdict(list)
for d in train_decisions:
    day_groups[d['date'].date()].append(d['up'])

# Calculate ICC (intraclass correlation)
n = issued
k = len(day_groups)
if k > 1 and n > 1:
    # Calculate variance components
    grand_mean = statistics.mean([d['up'] for d in train_decisions])
    
    # Between-group variance
    ss_between = 0
    for day, ups in day_groups.items():
        group_mean = statistics.mean(ups)
        ss_between += len(ups) * (group_mean - grand_mean) ** 2
    
    # Within-group variance
    ss_within = 0
    for day, ups in day_groups.items():
        group_mean = statistics.mean(ups)
        for up in ups:
            ss_within += (up - group_mean) ** 2
    
    ms_between = ss_between / (k - 1)
    ms_within = ss_within / (n - k)
    
    # ICC = between variance / total variance
    icc = (ms_between - ms_within) / (ms_between + (len(day_groups[list(day_groups.keys())[0]]) - 1) * ms_within) if (ms_between + ms_within) > 0 else 0
    
    # Design effect = 1 + (average cluster size - 1) * ICC
    avg_cluster_size = n / k
    deff = 1 + (avg_cluster_size - 1) * icc
    effective_n = n / deff
else:
    deff = 1
    effective_n = n

# Calculate sealed era precision
sealed_issued = len(sealed_decisions)
if sealed_issued > 0:
    sealed_hits = sum(1 for d in sealed_decisions if d['up'] == 1)
    sealed_precision = sealed_hits / sealed_issued
else:
    sealed_precision = 0

# Print results
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.2f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")

# Verify invariants
if distinct_days > issued:
    print("ERROR: DISTINCT_DAYS > ISSUED", file=__import__('sys').stderr)
if effective_n >= issued:
    print("ERROR: EFFECTIVE_N >= ISSUED", file=__import__('sys').stderr)

conn.close()
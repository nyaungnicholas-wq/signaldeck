# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 521
# cycle_index: 51
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
from datetime import datetime, timedelta
import math

# Open read-only database
db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, isolation_level='DEFERRED')
db.row_factory = sqlite3.Row

# Get symbols with SEC filings
filings_symbols = set(row[0] for row in db.execute("SELECT DISTINCT symbol_id FROM filings"))

# Get 13F data for these symbols
q13f = """
    SELECT symbol_id, period, COUNT(DISTINCT manager) as num_holders
    FROM inst_holdings
    WHERE symbol_id IN ({})
    GROUP BY symbol_id, period
    ORDER BY symbol_id, period
""".format(','.join('?' * len(filings_symbols)))

rows_13f = db.execute(q13f, list(filings_symbols)).fetchall()

# Process quarterly changes
symbol_quarters = defaultdict(list)
for row in rows_13f:
    symbol_quarters[row[0]].append((row[1], row[2]))

# Calculate quarterly changes in distinct holders
quarterly_changes = defaultdict(list)
symbols_with_two_quarters = set()
for symbol, quarters in symbol_quarters.items():
    if len(quarters) >= 2:
        symbols_with_two_quarters.add(symbol)
        for i in range(1, len(quarters)):
            prev_holders = quarters[i-1][1]
            curr_holders = quarters[i][1]
            change = curr_holders - prev_holders
            period_end = quarters[i][0]  # quarter end date string YYYY-MM-DD
            # Store as datetime for easier comparison
            dt_period_end = datetime.strptime(period_end, '%Y-%m-%d')
            quarterly_changes[symbol].append((dt_period_end, change, curr_holders))

# Calculate 75th percentile for each symbol
percentiles_75 = {}
for symbol, changes in quarterly_changes.items():
    vals = [c[1] for c in changes]
    if not vals:
        continue
    sorted_vals = sorted(vals)
    idx = int(math.ceil(0.75 * len(sorted_vals))) - 1
    idx = max(0, min(idx, len(sorted_vals)-1))
    percentiles_75[symbol] = sorted_vals[idx]

# Get all trading days for volume calculation
trading_days_q = """
    SELECT DISTINCT ts/86400 as day_epoch 
    FROM bars 
    WHERE tf='1d' AND symbol_id IN ({})
    ORDER BY day_epoch
""".format(','.join('?' * len(filings_symbols)))

trading_days = set(row[0] for row in db.execute(trading_days_q, list(filings_symbols)).fetchall())

# Helper function to get next trading day
def get_next_trading_day(after_epoch):
    candidates = [d for d in trading_days if d > after_epoch]
    return min(candidates) if candidates else None

# Helper function to get sentiment 5-day MA
def get_sentiment_ma(symbol_id, day_epoch):
    # Get last 5 trading days including current
    q = """
        SELECT AVG(mean_score) FROM (
            SELECT mean_score 
            FROM sentiment_features 
            WHERE symbol_id = ? AND day <= datetime(?, 'unixepoch')
            ORDER BY day DESC 
            LIMIT 5
        )
    """
    result = db.execute(q, (symbol_id, day_epoch*86400)).fetchone()
    return result[0] if result else 0

# Helper function to get 20-day ADV
def get_adv20(symbol_id, day_epoch):
    # Get 20 trading days before (not including) current day
    q = """
        SELECT AVG(close * volume) FROM (
            SELECT close, volume FROM bars 
            WHERE symbol_id = ? AND tf='1d' AND ts/86400 < ? 
            ORDER BY ts DESC LIMIT 20
        )
    """
    result = db.execute(q, (symbol_id, day_epoch)).fetchone()
    return result[0] if result else 0

# Process each decision point
opportunities = []  # (symbol_id, entry_epoch, label)
issues = 0

for symbol in symbols_with_two_quarters:
    for dt_period_end, change, holders in quarterly_changes[symbol]:
        # Calculate 45-day lag deadline
        deadline = dt_period_end + timedelta(days=45)
        deadline_epoch = int(deadline.timestamp() / 86400)
        
        # Get first trading day after deadline
        entry_epoch = get_next_trading_day(deadline_epoch)
        if entry_epoch is None:
            continue
            
        # Check 75th percentile condition
        threshold = percentiles_75.get(symbol, 0)
        if change <= threshold:
            continue
            
        # Check sentiment condition
        sentiment_ma = get_sentiment_ma(symbol, entry_epoch)
        if sentiment_ma <= 0:
            continue
            
        # Check volume condition
        adv20 = get_adv20(symbol, entry_epoch)
        if adv20 < 10_000_000:
            continue
            
        # Get label from prediction_outcomes
        entry_ts = entry_epoch * 86400
        label_q = """
            SELECT up FROM prediction_outcomes 
            WHERE symbol_id = ? AND horizon = 21 
            AND ts = ? AND resolved_at IS NOT NULL
            LIMIT 1
        """
        label_row = db.execute(label_q, (symbol, entry_ts)).fetchone()
        if label_row is None:
            continue
            
        issues += 1
        opportunities.append((symbol, entry_epoch, label_row[0]))

db.close()

if issues < 20:
    print("INSUFFICIENT=1")
else:
    # Sort opportunities by entry date
    opportunities.sort(key=lambda x: x[1])
    
    # Split into train (80%) and sealed (20%)
    split_idx = int(len(opportunities) * 0.8)
    train = opportunities[:split_idx]
    sealed = opportunities[split_idx:]
    
    # Calculate metrics for train set
    issued_train = len(train)
    if issued_train == 0:
        print("INSUFFICIENT=1")
    else:
        # Count hits (label == 1)
        hits_train = sum(1 for _, _, label in train if label == 1)
        base_rate_train = hits_train / issued_train
        
        # Count distinct days
        days_set_train = set(entry for _, entry, _ in train)
        distinct_days_train = len(days_set_train)
        
        # Calculate design effect (average calls per day)
        day_counts_train = defaultdict(int)
        for _, entry, _ in train:
            day_counts_train[entry] += 1
        avg_calls_per_day = sum(day_counts_train.values()) / len(day_counts_train)
        design_effect = avg_calls_per_day  # Assuming ICC=1 for simplicity
        effective_n = issued_train / design_effect
        
        # Calculate sealed metrics
        issued_sealed = len(sealed)
        if issued_sealed > 0:
            hits_sealed = sum(1 for _, _, label in sealed if label == 1)
            precision_sealed = hits_sealed / issued_sealed
        else:
            precision_sealed = 0
        
        # Print required outputs
        print(f"ISSUED={issued_train}")
        print(f"OPPORTUNITIES={issues}")
        print(f"PRECISION={base_rate_train:.6f}")  # hits/issued
        print(f"BASE_RATE={base_rate_train:.6f}")
        print(f"DISTINCT_DAYS={distinct_days_train}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={precision_sealed:.6f}")
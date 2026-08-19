# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 518
# cycle_index: 48
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def compute_rsi(prices, period=14):
    if len(prices) < period + 1:
        return []
    rsi = []
    gains = []
    losses = []
    for i in range(1, len(prices)):
        change = prices[i] - prices[i-1]
        gains.append(max(change, 0))
        losses.append(max(-change, 0))
    
    avg_gain = sum(gains[:period]) / period
    avg_loss = sum(losses[:period]) / period
    
    if avg_loss == 0:
        rsi.append(100.0)
    else:
        rs = avg_gain / avg_loss
        rsi.append(100.0 - (100.0 / (1.0 + rs)))
    
    for i in range(period, len(gains)):
        avg_gain = (avg_gain * (period - 1) + gains[i]) / period
        avg_loss = (avg_loss * (period - 1) + losses[i]) / period
        if avg_loss == 0:
            rsi.append(100.0)
        else:
            rs = avg_gain / avg_loss
            rsi.append(100.0 - (100.0 / (1.0 + rs)))
    return rsi

conn = sqlite3.connect(DB_PATH, uri=True)
conn.row_factory = sqlite3.Row

# Get symbols with sufficient daily bars
cursor = conn.execute("""
    SELECT symbol_id 
    FROM bars 
    WHERE tf = '1d' 
    GROUP BY symbol_id 
    HAVING COUNT(*) >= 252
""")
symbol_ids = [row[0] for row in cursor]
if not symbol_ids:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Get symbols with sufficient sentiment features
cursor = conn.execute("""
    SELECT symbol_id 
    FROM sentiment_features 
    GROUP BY symbol_id 
    HAVING COUNT(DISTINCT day) >= 252
""")
sentiment_symbol_ids = set(row[0] for row in cursor)

# Find intersection
valid_symbols = [s for s in symbol_ids if s in sentiment_symbol_ids]
if not valid_symbols:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

opportunities = []
issued_calls = []
base_dates = {}

for symbol_id in valid_symbols:
    # Get daily bars
    cursor = conn.execute("""
        SELECT ts, close 
        FROM bars 
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """, (symbol_id,))
    bar_data = cursor.fetchall()
    if len(bar_data) < 252:
        continue
    
    timestamps = [row[0] for row in bar_data]
    closes = [row[1] for row in bar_data]
    
    # Get sentiment features
    cursor = conn.execute("""
        SELECT day, mean_score 
        FROM sentiment_features 
        WHERE symbol_id = ?
        ORDER BY day
    """, (symbol_id,))
    sentiment_data = cursor.fetchall()
    if not sentiment_data:
        continue
    
    # Create sentiment lookup by date
    sentiment_by_day = {}
    for row in sentiment_data:
        day_str = row[0]
        try:
            day_ts = int(datetime.strptime(day_str, '%Y-%m-%d').timestamp())
            sentiment_by_day[day_ts] = row[1]
        except ValueError:
            continue
    
    # Align bars with sentiment
    aligned_closes = []
    aligned_sentiments = []
    aligned_timestamps = []
    
    for i, ts in enumerate(timestamps):
        # Find sentiment for this bar's date (or closest before)
        bar_date = datetime.utcfromtimestamp(ts).date()
        sentiment_value = None
        
        # Check same day first
        if ts in sentiment_by_day:
            sentiment_value = sentiment_by_day[ts]
        else:
            # Find most recent sentiment before this bar
            best_ts = None
            for s_ts in sentiment_by_day:
                if s_ts <= ts:
                    if best_ts is None or s_ts > best_ts:
                        best_ts = s_ts
            if best_ts is not None:
                sentiment_value = sentiment_by_day[best_ts]
        
        if sentiment_value is not None:
            aligned_closes.append(closes[i])
            aligned_sentiments.append(sentiment_value)
            aligned_timestamps.append(ts)
    
    if len(aligned_closes) < 252:
        continue
    
    # Compute RSI
    rsi_values = compute_rsi(aligned_closes, 14)
    if not rsi_values:
        continue
    
    # Compute moving averages of sentiment
    sent_5ma = []
    sent_20ma = []
    
    for i in range(len(aligned_sentiments)):
        if i >= 4:
            ma5 = sum(aligned_sentiments[i-4:i+1]) / 5
            sent_5ma.append(ma5)
        else:
            sent_5ma.append(None)
        
        if i >= 19:
            ma20 = sum(aligned_sentiments[i-19:i+1]) / 20
            sent_20ma.append(ma20)
        else:
            sent_20ma.append(None)
    
    # Evaluate entry conditions
    # RSI starts at index 14 (since we need 14 periods to compute first RSI)
    # Sentiment MA20 starts at index 19
    # So we need aligned data from index max(14,19) onwards
    
    for i in range(19, len(aligned_timestamps)):
        if rsi_values[i-14] < 30 and sent_5ma[i] is not None and sent_20ma[i] is not None:
            if sent_5ma[i] > sent_20ma[i]:
                decision_ts = aligned_timestamps[i]
                
                # Get label from prediction_outcomes
                cursor = conn.execute("""
                    SELECT up 
                    FROM prediction_outcomes 
                    WHERE symbol_id = ? AND horizon = 5 AND ts = ?
                    LIMIT 1
                """, (symbol_id, decision_ts))
                result = cursor.fetchone()
                
                if result:
                    opportunities.append({
                        'symbol_id': symbol_id,
                        'ts': decision_ts,
                        'hit': result[0]
                    })
                    
                    issued_calls.append({
                        'symbol_id': symbol_id,
                        'ts': decision_ts,
                        'hit': result[0]
                    })
                    
                    # Track base dates
                    base_date = datetime.utcfromtimestamp(decision_ts).date()
                    if base_date not in base_dates:
                        base_dates[base_date] = 0
                    base_dates[base_date] += 1

conn.close()

if not issued_calls:
    print("INSUFFICIENT=1")
    exit(0)

# Calculate basic metrics
issued_count = len(issued_calls)
opportunity_count = len(opportunities)
hits = sum(1 for call in issued_calls if call['hit'] == 1)
precision = hits / issued_count if issued_count > 0 else 0
base_rate = precision  # Same as precision since we're measuring predicted class in issued set

# Distinct days
distinct_days = len(set(datetime.utcfromtimestamp(call['ts']).date() for call in issued_calls))

# Calculate design effect and effective N
# Group by date
date_groups = {}
for call in issued_calls:
    date = datetime.utcfromtimestamp(call['ts']).date()
    if date not in date_groups:
        date_groups[date] = {'hits': 0, 'total': 0}
    date_groups[date]['total'] += 1
    if call['hit'] == 1:
        date_groups[date]['hits'] += 1

# Overall proportion
p = base_rate
k = len(date_groups)  # Number of clusters (days)
if k > 1:
    # Calculate variance between clusters
    total_n = issued_count
    sum_sq = 0
    for date, data in date_groups.items():
        n_i = data['total']
        p_i = data['hits'] / n_i if n_i > 0 else 0
        sum_sq += n_i * ((p_i - p) ** 2)
    
    s_b_squared = sum_sq / (k - 1)
    
    # Calculate variance within clusters
    sum_within = 0
    for date, data in date_groups.items():
        n_i = data['total']
        hits_i = data['hits']
        if n_i > 1:
            sum_within += hits_i * (1 - hits_i / n_i)
    
    s_w_squared = sum_within / (total_n - k) if (total_n - k) > 0 else 0
    
    # Intraclass correlation
    if (s_b_squared + s_w_squared) > 0:
        rho = s_b_squared / (s_b_squared + s_w_squared)
    else:
        rho = 0
    
    # Average cluster size
    m = total_n / k
    
    # Design effect
    deff = 1 + (m - 1) * rho
else:
    # Only one day, assume design effect of 2 (conservative)
    deff = 2

effective_n = issued_count / deff

# Sealed era (most recent 20% by time)
issued_calls_sorted = sorted(issued_calls, key=lambda x: x['ts'])
sealed_cutoff = int(0.8 * len(issued_calls_sorted))
sealed_calls = issued_calls_sorted[sealed_cutoff:]
sealed_hits = sum(1 for call in sealed_calls if call['hit'] == 1)
sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0

# Output results
print(f"ISSUED={issued_count}")
print(f"OPPORTUNITIES={opportunity_count}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
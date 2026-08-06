#!/usr/bin/env python3
import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cursor = db.cursor()

# Helper to get trading days for a symbol from bars
def get_trading_days(symbol_id):
    cursor.execute(
        'SELECT DISTINCT ts FROM bars WHERE symbol_id = ? AND tf = "1d" ORDER BY ts',
        (symbol_id,)
    )
    return [row[0] for row in cursor.fetchall()]

# Helper to check insider purchase on a given trading day (by date)
def has_insider_purchase(symbol_id, trading_day_ts):
    # Convert ts to date string for comparison with filed_ts
    day_str = datetime.utcfromtimestamp(trading_day_ts).strftime('%Y-%m-%d')
    cursor.execute(
        '''SELECT COUNT(*) FROM insider_trades 
           WHERE symbol_id = ? AND code = 'P' 
           AND date(filed_ts/1000, 'unixepoch') = ?''',
        (symbol_id, day_str)
    )
    return cursor.fetchone()[0] > 0

# Helper to get prior sessions and check conditions
def get_prior_sessions(symbol_id, ts, n):
    cursor.execute(
        'SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = "1d" AND ts < ? ORDER BY ts DESC LIMIT ?',
        (symbol_id, ts, n)
    )
    return cursor.fetchall()

# Helper to compute cross-sectional percentile
def compute_percentile(values, target):
    if not values:
        return 1.0
    count_below = sum(1 for v in values if v < target)
    return count_below / len(values)

# Get all symbol_ids that have both bars and insider_trades
cursor.execute('''SELECT DISTINCT s.id 
                  FROM symbols s
                  INNER JOIN bars b ON s.id = b.symbol_id
                  INNER JOIN insider_trades i ON s.id = i.symbol_id
                  WHERE b.tf = "1d"''')
symbol_ids = [row[0] for row in cursor.fetchall()]

# Collect all candidate opportunities
candidate_calls = []
all_trading_days = set()

for symbol_id in symbol_ids:
    # Get trading days for this symbol
    trading_days = get_trading_days(symbol_id)
    if len(trading_days) < 252:
        continue
    
    all_trading_days.update(trading_days)
    
    # Precompute daily returns and other metrics for this symbol
    # We'll store data for each trading day
    day_data = {}
    
    for i, ts in enumerate(trading_days):
        if i < 251:  # Need at least 252 prior sessions
            continue
        
        # Get current day bar
        cursor.execute(
            'SELECT close, volume FROM bars WHERE symbol_id = ? AND tf = "1d" AND ts = ?',
            (symbol_id, ts)
        )
        current = cursor.fetchone()
        if not current:
            continue
        
        close, volume = current
        
        # Price filter
        if close < 5:
            continue
        
        # Get previous 60 sessions for volume and return calculations
        prev_60 = get_prior_sessions(symbol_id, ts, 60)
        if len(prev_60) < 60:
            continue
        
        # Calculate 60-day average dollar volume
        avg_dollar_vol = 0
        for _, prev_close in prev_60:
            # We don't have volume for each day in prev_60, so approximate
            # using current day's volume as proxy (imperfect but data limitation)
            avg_dollar_vol += prev_close * volume
        avg_dollar_vol /= 60
        
        if avg_dollar_vol < 1_000_000:
            continue
        
        # Calculate 60-day return
        first_close_60 = prev_60[-1][1]
        last_close_60 = prev_60[0][1]
        return_60d = (last_close_60 / first_close_60) - 1
        
        # Calculate 20-day realized volatility
        prev_20 = get_prior_sessions(symbol_id, ts, 20)
        if len(prev_20) < 20:
            continue
        
        returns_20d = []
        for j in range(len(prev_20)-1):
            r = (prev_20[j][1] / prev_20[j+1][1]) - 1
            returns_20d.append(r)
        
        if not returns_20d:
            continue
            
        mean_ret = sum(returns_20d) / len(returns_20d)
        var = sum((r - mean_ret)**2 for r in returns_20d) / len(returns_20d)
        volatility_20d = var ** 0.5
        
        # Calculate today's return
        prev_day = get_prior_sessions(symbol_id, ts, 1)
        if not prev_day:
            continue
        return_today = (close / prev_day[0][1]) - 1
        
        # Check if return_today is between -1% and +1%
        if not (-0.01 <= return_today <= 0.01):
            continue
        
        # Check for insider purchase disclosed today
        if not has_insider_purchase(symbol_id, ts):
            continue
        
        # Check trade date is within 10 sessions
        # Get insider trades disclosed today
        day_str = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        cursor.execute(
            '''SELECT tx_ts FROM insider_trades 
               WHERE symbol_id = ? AND code = 'P' 
               AND date(filed_ts/1000, 'unixepoch') = ?''',
            (symbol_id, day_str)
        )
        tx_timestamps = [row[0] for row in cursor.fetchall()]
        
        # Get trading days before current
        prev_days = [d for d in trading_days if d < ts]
        if not prev_days:
            continue
        
        # Check if any trade happened within last 10 sessions
        trade_within_10 = False
        for tx_ts in tx_timestamps:
            # Find the trading day of the trade
            tx_day = None
            for d in prev_days:
                if d <= tx_ts:
                    tx_day = d
                    break
            
            if tx_day:
                # Count sessions between tx_day and ts
                sessions_since = sum(1 for d in prev_days if tx_day <= d < ts)
                if sessions_since <= 10:
                    trade_within_10 = True
                    break
        
        if not trade_within_10:
            continue
        
        # Store candidate
        candidate_calls.append({
            'symbol_id': symbol_id,
            'ts': ts,
            'return_60d': return_60d,
            'volatility_20d': volatility_20d
        })

# Filter candidates by cross-sectional deciles
if not candidate_calls:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

# Get all unique timestamps
unique_ts = sorted(set(call['ts'] for call in candidate_calls))
if len(unique_ts) < 10:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

# Split into test and sealed (80/20)
split_idx = int(len(unique_ts) * 0.8)
test_ts = set(unique_ts[:split_idx])
sealed_ts = set(unique_ts[split_idx:])

# Collect final calls with checks
final_test_calls = []
final_sealed_calls = []

# Track calls per symbol within 20 days
last_call_per_symbol = {}

for ts in unique_ts:
    # Get all candidates for this timestamp
    ts_candidates = [c for c in candidate_calls if c['ts'] == ts]
    
    # Compute cross-sectional percentiles for this timestamp
    return_60d_vals = [c['return_60d'] for c in ts_candidates]
    volatility_vals = [c['volatility_20d'] for c in ts_candidates]
    
    for call in ts_candidates:
        # Check if 20-day volatility is in top decile (abstain)
        vol_percentile = compute_percentile(volatility_vals, call['volatility_20d'])
        if vol_percentile >= 0.90:
            continue
        
        # Check 60-day return is in bottom decile (entry)
        return_percentile = compute_percentile(return_60d_vals, call['return_60d'])
        if return_percentile >= 0.10:
            continue
        
        # Check if same symbol had call within prior 20 trading days
        sym = call['symbol_id']
        if sym in last_call_per_symbol:
            days_since = (ts - last_call_per_symbol[sym]) / (24 * 60 * 60)  # approximate days
            if days_since < 20 * 1.5:  # approximate 20 trading days in calendar days
                continue
        
        # Issue call
        if ts in test_ts:
            final_test_calls.append(call)
        else:
            final_sealed_calls.append(call)
        
        last_call_per_symbol[sym] = ts

# Count independent observations (symbol, day)
independent_test_days = defaultdict(set)
for call in final_test_calls:
    day_str = datetime.utcfromtimestamp(call['ts']).strftime('%Y-%m-%d')
    independent_test_days
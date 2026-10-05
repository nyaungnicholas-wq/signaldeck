# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 446
# cycle_index: 37
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
from datetime import datetime, timedelta
import math

# Connect to database
conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cursor = conn.cursor()

# Helper: Get all distinct trading days (1d bars) for a symbol
def get_symbol_days(symbol_id, max_date=None):
    query = "SELECT ts FROM bars WHERE symbol_id = ? AND tf = '1d'"
    params = [symbol_id]
    if max_date:
        query += " AND ts <= ?"
        params.append(max_date)
    query += " ORDER BY ts"
    cursor.execute(query, params)
    return [row[0] for row in cursor.fetchall()]

# Helper: Get M2 year-over-year growth (weekly prints)
def get_m2_yoy_growth():
    # Get M2 weekly data
    cursor.execute("""
        SELECT ts, value 
        FROM macro_series 
        WHERE series = 'M2' 
        ORDER BY ts
    """)
    m2_data = cursor.fetchall()
    if not m2_data:
        return []
    
    # Convert to date-keyed structure (week ending)
    # M2 is reported weekly, but ts is unix timestamp
    weekly_data = {}
    for ts, value in m2_data:
        dt = datetime.utcfromtimestamp(ts)
        # Group by ISO week
        year, week, _ = dt.isocalendar()
        key = (year, week)
        if key not in weekly_data or ts > weekly_data[key][0]:
            weekly_data[key] = (ts, value)
    
    # Convert to sorted list by timestamp
    sorted_data = sorted([(ts, val) for ts, val in weekly_data.values()])
    
    # Compute year-over-year growth (compare to same week last year)
    growth_series = []
    for i, (ts, val) in enumerate(sorted_data):
        # Find corresponding week last year
        dt = datetime.utcfromtimestamp(ts)
        prev_year = dt.year - 1
        prev_week = dt.isocalendar()[1]
        
        # Search for matching week in previous year
        prev_ts = None
        prev_val = None
        for j in range(i-1, -1, -1):
            prev_dt = datetime.utcfromtimestamp(sorted_data[j][0])
            if prev_dt.year == prev_year and prev_dt.isocalendar()[1] == prev_week:
                prev_ts, prev_val = sorted_data[j]
                break
        
        if prev_val is not None and prev_val > 0:
            yoy_growth = (val / prev_val - 1) * 100
            growth_series.append((ts, yoy_growth))
    
    return growth_series

# Helper: Find dates when M2 acceleration condition is met
def find_m2_acceleration_dates():
    growth_series = get_m2_yoy_growth()
    if len(growth_series) < 3:
        return []
    
    acceleration_dates = []
    for i in range(2, len(growth_series)):
        # Check three consecutive rising prints
        if (growth_series[i][1] > growth_series[i-1][1] and 
            growth_series[i-1][1] > growth_series[i-2][1]):
            # Return the timestamp of the third print
            acceleration_dates.append(growth_series[i][0])
    
    return acceleration_dates

# Get all symbols with at least 252 trading days of 1d data
cursor.execute("""
    SELECT symbol_id, COUNT(*) as cnt
    FROM bars
    WHERE tf = '1d'
    GROUP BY symbol_id
    HAVING cnt >= 252
""")
symbol_counts = {row[0]: row[1] for row in cursor.fetchall()}

# Get all 1d bar timestamps to find universe definition times
cursor.execute("SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts")
all_1d_timestamps = [row[0] for row in cursor.fetchall()]

# Get all symbols' 1d bars for computation
cursor.execute("""
    SELECT symbol_id, ts, close, volume
    FROM bars
    WHERE tf = '1d'
    ORDER BY symbol_id, ts
""")
all_1d_bars = cursor.fetchall()

# Organize bars by symbol
symbol_bars = defaultdict(list)
for symbol_id, ts, close, volume in all_1d_bars:
    symbol_bars[symbol_id].append((ts, close, volume))

# Get prediction outcomes with horizon=21 for labels
cursor.execute("""
    SELECT symbol_id, ts, up, fwd_return
    FROM prediction_outcomes
    WHERE horizon = 21
""")
labels = {}
for symbol_id, ts, up, fwd_return in cursor.fetchall():
    if symbol_id not in labels:
        labels[symbol_id] = {}
    labels[symbol_id][ts] = (up, fwd_return)

# Get M2 acceleration dates
m2_accel_dates = find_m2_acceleration_dates()
if not m2_accel_dates:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Convert M2 acceleration dates to trading day timestamps
# Find first trading day after each acceleration date
trading_days_set = set(all_1d_timestamps)
trading_days = sorted(trading_days_set)

def next_trading_day(ts):
    """Find first trading day after timestamp ts"""
    for td in trading_days:
        if td > ts:
            return td
    return None

entry_dates = []
for accel_ts in m2_accel_dates:
    td = next_trading_day(accel_ts)
    if td:
        entry_dates.append(td)

if not entry_dates:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Remove duplicates and sort
entry_dates = sorted(set(entry_dates))

# Process each entry date
issued_calls = []
all_opportunities = []

for entry_date in entry_dates:
    # Find symbols in universe at this entry_date
    universe_symbols = []
    for symbol_id in symbol_counts:
        # Check if we have bars up to entry_date
        bars = symbol_bars.get(symbol_id, [])
        if not bars:
            continue
        
        # Get bars up to entry_date
        bars_up_to = [(ts, close, vol) for ts, close, vol in bars if ts <= entry_date]
        if len(bars_up_to) < 252:
            continue
        
        # Compute trailing 20-day median dollar volume
        recent_bars = bars_up_to[-20:]
        dollar_volumes = [close * vol for _, close, vol in recent_bars]
        if not dollar_volumes:
            continue
        
        # Use simple sort for median (no numpy)
        sorted_volumes = sorted(dollar_volumes)
        n = len(sorted_volumes)
        median_vol = (sorted_volumes[n//2] if n % 2 == 1 else 
                     (sorted_volumes[n//2 - 1] + sorted_volumes[n//2]) / 2)
        
        if median_vol > 1_000_000:
            # Compute trailing 20-day return
            if len(bars_up_to) >= 20:
                start_close = bars_up_to[-20][1]
                end_close = bars_up_to[-1][1]
                if start_close > 0:
                    ret_20d = (end_close / start_close - 1) * 100
                    universe_symbols.append((symbol_id, ret_20d))
    
    if len(universe_symbols) < 30:
        continue
    
    # Compute equal-weight universe mean return
    mean_return = sum(ret for _, ret in universe_symbols) / len(universe_symbols)
    
    # Compute relative returns
    relative_returns = [(symbol_id, ret - mean_return) for symbol_id, ret in universe_symbols]
    
    # Sort by relative return
    relative_returns.sort(key=lambda x: x[1])
    
    # Take bottom quintile (lowest 20%)
    quintile_size = max(1, len(relative_returns) // 5)
    bottom_quintile = [symbol_id for symbol_id, _ in relative_returns[:quintile_size]]
    
    # Issue calls for each symbol in bottom quintile
    for symbol_id in bottom_quintile:
        issued_calls.append((symbol_id, entry_date))
        all_opportunities.append(entry_date)

# Split into training and sealed eras (last 20%)
if issued_calls:
    unique_dates = sorted(set([date for _, date in issued_calls]))
    split_idx = int(len(unique_dates) * 0.8)
    sealed_start = unique_dates[split_idx] if split_idx < len(unique_dates) else None
    
    training_calls = []
    sealed_calls = []
    
    for symbol_id, date in issued_calls:
        if sealed_start and date >= sealed_start:
            sealed_calls.append((symbol_id, date))
        else:
            training_calls.append((symbol_id, date))
    
    # Compute metrics for training set
    hits = 0
    base_rate_hits = 0
    
    for symbol_id, date in training_calls:
        if symbol_id in labels and date in labels[symbol_id]:
            up, _ = labels[symbol_id][date]
            if up == 1:
                hits += 1
                base_rate_hits += 1
    
    issued = len(training_calls)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate within issued subset
    base_rate = base_rate_hits / issued if issued > 0 else 0
    
    # Distinct days
    distinct_days = len(set([date for _, date in training_calls]))
    
    # Effective N (assuming perfect within-day correlation)
    avg_calls_per_day = issued / distinct_days if distinct_days > 0 else 1
    effective_n = distinct_days  # Design effect = avg_calls_per_day
    
    # Compute sealed precision
    sealed_hits = 0
    sealed_total = len(sealed_calls)
    
    for symbol_id, date in sealed_calls:
        if symbol_id in labels and date in labels[symbol_id]:
            up, _ = labels[symbol_id][date]
            if up == 1:
                sealed_hits += 1
    
    sealed_precision = sealed_hits / sealed_total if sealed_total > 0 else 0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(set(all_opportunities))}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
else:
    print("INSUFFICIENT=1")

conn.close()
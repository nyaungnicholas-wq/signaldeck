# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 608
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = db.cursor()

# Get all insider open-market purchases (code='P')
cur.execute('''
SELECT it.symbol_id, it.filed_ts, it.price
FROM insider_trades it
WHERE it.code = 'P'
''')
purchases = cur.fetchall()

if not purchases:
    print("INSUFFICIENT=1")
    exit(0)

# Get daily bars for all symbols since 2018-07 (ts >= 1532534400)
cur.execute('''
SELECT symbol_id, ts, close, volume
FROM bars
WHERE tf = '1d' AND ts >= 1532534400
ORDER BY symbol_id, ts
''')
bars = cur.fetchall()

if not bars:
    print("INSUFFICIENT=1")
    exit(0)

# Organize bars by symbol
bars_by_symbol = defaultdict(list)
for symbol_id, ts, close, volume in bars:
    bars_by_symbol[symbol_id].append((ts, close, volume))

# For each symbol, compute rolling 252-session high and 90-day volume percentiles
def compute_indicators(symbol_id, bars_data):
    if len(bars_data) < 252:
        return {}
    
    indicators = {}
    closes = [close for _, close, _ in bars_data]
    
    # Compute 252-session high for each position
    for i in range(len(bars_data)):
        ts, close, _ = bars_data[i]
        # Get last 252 closes including current
        window_start = max(0, i - 251)
        high_252 = max(closes[window_start:i+1])
        indicators[ts] = {'high_252': high_252}
    
    # Compute 90-day volume percentiles
    for i in range(len(bars_data)):
        ts, _, volume = bars_data[i]
        # Get last 90 volumes including current
        window_start = max(0, i - 89)
        volume_window = [bars_data[j][2] for j in range(window_start, i+1)]
        # Calculate percentile rank
        count_below = sum(1 for v in volume_window if v <= volume)
        percentile = (count_below / len(volume_window)) * 100
        indicators[ts]['volume_percentile'] = percentile
    
    return indicators

# Compute indicators for each symbol
symbol_indicators = {}
for symbol_id, bars_data in bars_by_symbol.items():
    symbol_indicators[symbol_id] = compute_indicators(symbol_id, bars_data)

# Process insider purchases and apply entry criteria
signals = []
for symbol_id, filed_ts, price in purchases:
    if symbol_id not in symbol_indicators:
        continue
    
    # Find closest bar timestamp <= filed_ts
    if symbol_id not in bars_by_symbol:
        continue
    
    symbol_bars = bars_by_symbol[symbol_id]
    bar_ts = None
    close_price = None
    volume = None
    
    # Find the bar at or before filed_ts
    for ts, close, vol in symbol_bars:
        if ts <= filed_ts:
            bar_ts = ts
            close_price = close
            volume = vol
        else:
            break
    
    if bar_ts is None:
        continue
    
    # Get indicators for this bar
    if bar_ts not in symbol_indicators[symbol_id]:
        continue
    
    ind = symbol_indicators[symbol_id][bar_ts]
    
    # Entry condition: close within 2% of 252-session high
    high_252 = ind['high_252']
    if close_price < high_252 * 0.98:
        continue
    
    # Abstain condition: volume below 20th percentile of 90-day history
    vol_percentile = ind['volume_percentile']
    if vol_percentile < 20:
        continue
    
    signals.append((symbol_id, bar_ts, close_price))

if not signals:
    print("INSUFFICIENT=1")
    exit(0)

# Get 21-day forward returns using future bars
cur.execute('''
SELECT symbol_id, ts, close
FROM bars
WHERE tf = '1d'
ORDER BY symbol_id, ts
''')
future_bars = cur.fetchall()

# Organize future bars by symbol for fast lookup
future_bars_by_symbol = defaultdict(list)
for symbol_id, ts, close in future_bars:
    future_bars_by_symbol[symbol_id].append((ts, close))

# Calculate forward returns for each signal
signals_with_returns = []
for symbol_id, signal_ts, entry_close in signals:
    if symbol_id not in future_bars_by_symbol:
        continue
    
    symbol_bars = future_bars_by_symbol[symbol_id]
    
    # Find position of signal_ts in future bars
    signal_idx = None
    for i, (ts, _) in enumerate(symbol_bars):
        if ts == signal_ts:
            signal_idx = i
            break
    
    if signal_idx is None:
        continue
    
    # Need at least 21 more bars after signal
    if signal_idx + 21 >= len(symbol_bars):
        continue
    
    # Get close price after 21 trading days
    future_close = symbol_bars[signal_idx + 21][1]
    forward_return = (future_close - entry_close) / entry_close
    
    # Hit is positive return
    hit = 1 if forward_return > 0 else 0
    
    signals_with_returns.append((symbol_id, signal_ts, hit))

if not signals_with_returns:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by timestamp and split into train/test (80/20)
signals_with_returns.sort(key=lambda x: x[1])
split_idx = int(len(signals_with_returns) * 0.8)
train_signals = signals_with_returns[:split_idx]
test_signals = signals_with_returns[split_idx:]

# Calculate metrics for entire dataset
all_hits = sum(hit for _, _, hit in signals_with_returns)
all_issued = len(signals_with_returns)
all_precision = all_hits / all_issued if all_issued > 0 else 0
all_base_rate = all_precision  # base rate equals precision when all are positive class

# Calculate distinct days
distinct_days = len(set(ts for _, ts, _ in signals_with_returns))

# Calculate design effect (clustering by day)
day_counts = defaultdict(int)
for _, ts, _ in signals_with_returns:
    day_counts[ts] += 1

if day_counts:
    design_effect = (all_issued ** 2) / sum(count ** 2 for count in day_counts.values())
else:
    design_effect = 1

effective_n = all_issued / design_effect if design_effect > 0 else 0

# Calculate metrics for sealed era (test set)
test_hits = sum(hit for _, _, hit in test_signals)
test_issued = len(test_signals)
sealed_precision = test_hits / test_issued if test_issued > 0 else 0

# Print required outputs
print(f"ISSUED={all_issued}")
print(f"OPPORTUNITIES={all_issued}")  # All considered signals are opportunities
print(f"PRECISION={all_precision:.6f}")
print(f"BASE_RATE={all_base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
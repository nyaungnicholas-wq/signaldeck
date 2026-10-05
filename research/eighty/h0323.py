# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 322
# cycle_index: 45
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import re
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 21
LOOKBACK = 5
RETURN_THRESHOLD_LONG = 0.10
RETURN_THRESHOLD_SHORT = -0.10
TRAIN_RATIO = 0.8
MIN_HISTORY_DAYS = 252

conn = sqlite3.connect(DB_PATH, uri=True)
conn.row_factory = sqlite3.Row

# Get all trading days for each symbol
trading_days = defaultdict(list)
for row in conn.execute("SELECT symbol_id, ts FROM bars WHERE tf='1d' ORDER BY symbol_id, ts"):
    trading_days[row[0]].append(row[1])

# Get news headlines with guidance signals
guidance_headlines = []
pos_pattern = re.compile(r'(raise[s]?\s+guidance|increas[e]?\s+guidance|guidance\s+above|guided\s+higher|upward\s+guidance|raised\s+guidance|increased\s+guidance|guidance\s+raised|guidance\s+increased|raised\s+outlook|increased\s+outlook)', re.I)
neg_pattern = re.compile(r'(cut[s]?\s+guidance|lower[s]?\s+guidance|guidance\s+below|guided\s+lower|downward\s+guidance|cut\s+guidance|lowered\s+guidance|guidance\s+cut|guidance\s+lowered|lowered\s+outlook|cut\s+outlook)', re.I)
exclude_pattern = re.compile(r'(earnings|revenue|eps|per\s+share|results|quarterly\s+report|fiscal\s+year)', re.I)

for row in conn.execute("SELECT symbol_id, ts, headline FROM news"):
    symbol_id, ts, headline = row
    if not headline:
        continue
    if exclude_pattern.search(headline):
        continue
    
    if pos_pattern.search(headline):
        guidance_headlines.append((symbol_id, ts, 1))
    elif neg_pattern.search(headline):
        guidance_headlines.append((symbol_id, ts, -1))

conn.close()

# Group headlines by symbol
headlines_by_symbol = defaultdict(list)
for symbol_id, ts, direction in guidance_headlines:
    headlines_by_symbol[symbol_id].append((ts, direction))

# Process signals
signals = []  # (entry_ts, direction, correct)

for symbol_id, headlines in headlines_by_symbol.items():
    days = trading_days.get(symbol_id, [])
    if not days:
        continue
    
    # Sort headlines by timestamp
    headlines.sort(key=lambda x: x[0])
    
    # For each headline, check if it qualifies as signal
    for i, (headline_ts, direction) in enumerate(headlines):
        # Find entry day: first trading day after headline
        entry_idx = None
        for j, d in enumerate(days):
            if d > headline_ts:
                entry_idx = j
                break
        
        if entry_idx is None or entry_idx < MIN_HISTORY_DAYS:
            continue
        
        # Check 5-day lookback window
        lookback_start_idx = entry_idx - LOOKBACK - 1
        if lookback_start_idx < 0:
            continue
        
        # Get symbols in the 5-day window
        window_headlines = []
        for other_ts, other_dir in headlines:
            if days[lookback_start_idx] <= other_ts < days[entry_idx]:
                window_headlines.append(other_dir)
        
        # Check for conflicting directions in window
        pos_in_window = sum(1 for d in window_headlines if d == 1)
        neg_in_window = sum(1 for d in window_headlines if d == -1)
        if pos_in_window > 0 and neg_in_window > 0:
            continue
        
        # Calculate 5-day cumulative return
        if entry_idx < 5:
            continue
        
        # Get bars for this symbol
        conn = sqlite3.connect(DB_PATH, uri=True)
        bars = conn.execute(
            "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT ?",
            (symbol_id, days[entry_idx - 1], LOOKBACK + 1)
        ).fetchall()
        conn.close()
        
        if len(bars) < LOOKBACK + 1:
            continue
        
        five_day_return = (bars[0][1] / bars[LOOKBACK][1]) - 1
        
        # Apply return filter
        if direction == 1 and five_day_return >= RETURN_THRESHOLD_LONG:
            continue
        if direction == -1 and five_day_return <= RETURN_THRESHOLD_SHORT:
            continue
        
        # Calculate 21-day forward return
        if entry_idx + HORIZON >= len(days):
            continue
        
        conn = sqlite3.connect(DB_PATH, uri=True)
        forward_bars = conn.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
            (symbol_id, days[entry_idx])
        ).fetchone()
        end_bars = conn.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
            (symbol_id, days[entry_idx + HORIZON])
        ).fetchone()
        conn.close()
        
        if not forward_bars or not end_bars:
            continue
        
        forward_return = (end_bars[0] / forward_bars[0]) - 1
        correct = (direction == 1 and forward_return > 0) or (direction == -1 and forward_return < 0)
        
        signals.append((days[entry_idx], direction, correct))

if not signals:
    print("INSUFFICIENT=1")
    exit(0)

# Split into train and sealed
signals.sort(key=lambda x: x[0])
split_idx = int(len(signals) * TRAIN_RATIO)
train_signals = signals[:split_idx]
sealed_signals = signals[split_idx:]

# Calculate metrics
def calc_metrics(sig_list):
    if not sig_list:
        return None, None, None, None, None, None
    
    issued = len(sig_list)
    hits = sum(1 for s in sig_list if s[2])
    precision = hits / issued if issued > 0 else 0
    
    # Base rate: proportion of correct calls in issued set
    base_rate = precision
    
    # Count distinct days
    distinct_days = len(set(s[0] for s in sig_list))
    
    # Calculate design effect using autocorrelation
    if distinct_days == 0 or issued == 0:
        effective_n = 0
    else:
        # Simple approximation: design effect = 1 + autocorrelation
        # For simplicity, use ratio of issued to distinct days as proxy
        design_effect = issued / distinct_days
        effective_n = issued / design_effect
    
    return issued, precision, base_rate, distinct_days, effective_n

# Calculate sealed metrics
sealed_metrics = calc_metrics(sealed_signals)

if not sealed_metrics or sealed_metrics[0] == 0:
    print("INSUFFICIENT=1")
    exit(0)

issued, precision, base_rate, distinct_days, effective_n = sealed_metrics

# Check claim thresholds
if precision < 0.80 or (precision - base_rate) < 0.10:
    print("INSUFFICIENT=1")
    exit(0)

# Output metrics
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={len(signals)}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.2f}")
print(f"SEALED_PRECISION={precision:.6f}")
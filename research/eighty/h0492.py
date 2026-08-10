# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 491
# cycle_index: 21
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime
from collections import defaultdict

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
c = db.cursor()

# Get all insider purchases (code='P' for open-market purchase)
c.execute("""
    SELECT symbol_id, insider, price, filed_ts
    FROM insider_trades
    WHERE code = 'P'
    ORDER BY symbol_id, insider, filed_ts
""")
purchases = c.fetchall()

# Get trading days for all symbols (to compute trading day differences)
c.execute("SELECT symbol_id, ts FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
all_bars = c.fetchall()
symbol_bars = defaultdict(list)
for symbol_id, ts in all_bars:
    symbol_bars[symbol_id].append(ts)

# Build signal opportunities and issued calls
signals = []
opportunities = 0

# Group purchases by (symbol_id, insider) and sort by filed_ts
grouped = defaultdict(list)
for symbol_id, insider, price, filed_ts in purchases:
    grouped[(symbol_id, insider)].append((price, filed_ts))

for (symbol_id, insider), trades in grouped.items():
    if len(trades) < 2:
        continue
    bars = symbol_bars[symbol_id]
    if not bars:
        continue
    
    last_call_idx = None
    
    for i in range(1, len(trades)):
        current_price, current_ts = trades[i]
        prior_price, prior_ts = trades[i-1]
        
        # Check if current price is strictly below prior price
        if current_price >= prior_price:
            continue
        
        # Find indices of current and prior ts in trading days
        try:
            current_idx = bars.index(current_ts)
        except ValueError:
            continue
        try:
            prior_idx = bars.index(prior_ts)
        except ValueError:
            continue
        
        # Check if prior was at least 20 trading days before current
        if current_idx - prior_idx < 20:
            continue
        
        opportunities += 1
        
        # Check if call already issued for this symbol within last 20 trading days
        if last_call_idx is not None and current_idx - last_call_idx < 20:
            continue
        
        # Issue call
        signals.append((symbol_id, current_ts, current_idx))
        last_call_idx = current_idx

if not signals:
    print("INSUFFICIENT=1")
    db.close()
    exit(0)

# Sort signals by time for temporal split
signals.sort(key=lambda x: x[1])
split = int(len(signals) * 0.8)
train = signals[:split]
sealed = signals[split:]

def compute_metrics(signal_list):
    if not signal_list:
        return 0, 0, 0.0, 0.0, 0, 0.0
    
    hits = 0
    days = set()
    day_counts = defaultdict(int)
    
    for symbol_id, signal_ts, signal_idx in signal_list:
        bars = symbol_bars[symbol_id]
        future_idx = signal_idx + 21
        if future_idx >= len(bars):
            continue
        
        # Get close prices
        c.execute("SELECT close FROM bars WHERE symbol_id=? AND ts=? AND tf='1d'",
                  (symbol_id, bars[signal_idx]))
        close_now = c.fetchone()
        c.execute("SELECT close FROM bars WHERE symbol_id=? AND ts=? AND tf='1d'",
                  (symbol_id, bars[future_idx]))
        close_future = c.fetchone()
        
        if not close_now or not close_future:
            continue
        
        if close_future[0] > close_now[0]:
            hits += 1
        
        day = datetime.utcfromtimestamp(signal_ts).strftime('%Y-%m-%d')
        days.add(day)
        day_counts[day] += 1
    
    issued = len(signal_list)
    if issued == 0:
        return 0, 0, 0.0, 0.0, 0, 0.0
    
    precision = hits / issued
    base_rate = precision  # Within issued subset
    
    # Design effect for day clustering
    J = len(day_counts)
    if J <= 1:
        design_effect = issued  # Prevent division by zero; EFFECTIVE_N would be 1
    else:
        m0 = issued / J
        # Approximate ICC as variance of day means / variance of all signals
        day_means = [c / day_counts[d] for d, c in 
                    ((d, sum(1 for s in signal_list if 
                             datetime.utcfromtimestamp(s[1]).strftime('%Y-%m-%d') == d))
                     for d in day_counts)]
        overall_mean = precision
        var_between = sum((m - overall_mean)**2 for m in day_means) / (J-1)
        var_within = sum((c/issued)*(1 - c/issued) for c in day_counts.values()) / J
        if var_within == 0:
            design_effect = 1
        else:
            icc = var_between / (var_between + var_within)
            design_effect = 1 + (m0 - 1) * icc
    
    effective_n = issued / design_effect if design_effect > 0 else 0
    
    return issued, opportunities, precision, base_rate, len(days), effective_n

# Compute training metrics
issued, opps, precision, base_rate, distinct_days, effective_n = compute_metrics(train)

# Compute sealed precision
sealed_hits = 0
sealed_count = 0
for symbol_id, signal_ts, signal_idx in sealed:
    bars = symbol_bars[symbol_id]
    future_idx = signal_idx + 21
    if future_idx >= len(bars):
        continue
    c.execute("SELECT close FROM bars WHERE symbol_id=? AND ts=? AND tf='1d'",
              (symbol_id, bars[signal_idx]))
    close_now = c.fetchone()
    c.execute("SELECT close FROM bars WHERE symbol_id=? AND ts=? AND tf='1d'",
              (symbol_id, bars[future_idx]))
    close_future = c.fetchone()
    if close_now and close_future and close_future[0] > close_now[0]:
        sealed_hits += 1
    sealed_count += 1

sealed_precision = sealed_hits / sealed_count if sealed_count > 0 else 0.0

db.close()

# Output metrics
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opps}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.4f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 442
# cycle_index: 33
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
import statistics

# Connect read-only
conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = conn.cursor()

# Get all daily bars
cur.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d'")
bars = cur.fetchall()

# Organize by symbol
symbol_bars = defaultdict(list)
for sid, ts, close, vol in bars:
    symbol_bars[sid].append((ts, close, vol))

# Sort each symbol's bars by ts
for sid in symbol_bars:
    symbol_bars[sid].sort(key=lambda x: x[0])

# Get all trading days (unique ts)
all_days = sorted({ts for sid in symbol_bars for ts, _, _ in symbol_bars[sid]})

# Process each day in chronological order
calls = []  # (decision_day, symbol_id, direction, hit)
opportunities = []

# Track high-ADV group average returns for decile calculation
high_adv_history = []

for day_idx, day in enumerate(all_days):
    # Collect symbols with sufficient history and price >= $5
    valid = []
    for sid, bars_list in symbol_bars.items():
        # Find index of current day in this symbol's bars
        idx = None
        for i, (ts, _, _) in enumerate(bars_list):
            if ts == day:
                idx = i
                break
        if idx is None:
            continue
        
        # Need at least 63 days of history before today
        if idx < 62:
            continue
        
        # Price >= $5 at decision time
        _, close_today, _ = bars_list[idx]
        if close_today < 5:
            continue
        
        # Compute trailing 63-day median dollar volume
        window = bars_list[max(0, idx-63):idx]
        dollar_vols = [c * v for _, c, v in window]
        if len(dollar_vols) < 30:
            continue
        
        median_dvol = statistics.median(dollar_vols)
        valid.append((sid, median_dvol, idx))
    
    if len(valid) < 50:  # Need enough for quintiles
        continue
    
    # Sort by median dollar volume to assign quintiles
    valid.sort(key=lambda x: x[1])
    n = len(valid)
    quintile_size = n // 5
    
    # Bottom quintile (targets) and top quintile (high-ADV)
    target_indices = set(range(quintile_size))
    high_adv_indices = set(range(n - quintile_size, n))
    
    # Compute high-ADV group's prior-day return (equal-weighted)
    high_adv_returns = []
    for idx in high_adv_indices:
        sid, _, bars_idx = valid[idx]
        bars_list = symbol_bars[sid]
        # Prior day is at bars_idx - 1
        if bars_idx < 1:
            continue
        _, prev_close, _ = bars_list[bars_idx - 1]
        _, today_close, _ = bars_list[bars_idx]
        if prev_close > 0:
            high_adv_returns.append((today_close - prev_close) / prev_close)
    
    if len(high_adv_returns) < 10:
        continue
    
    avg_high_adv_return = sum(high_adv_returns) / len(high_adv_returns)
    
    # Add to history for decile calculation
    high_adv_history.append(avg_high_adv_return)
    if len(high_adv_history) > 252:
        high_adv_history = high_adv_history[-252:]
    
    # Check if current average is in top or bottom decile
    if len(high_adv_history) < 20:
        continue
    
    current_rank = sum(1 for x in high_adv_history if x <= avg_high_adv_return) / len(high_adv_history)
    signal = None
    if current_rank >= 0.9:  # Top decile
        signal = 1  # Issue LONG on bottom-ADV
    elif current_rank <= 0.1:  # Bottom decile
        signal = -1  # Issue SHORT on bottom-ADV
    
    if signal is None:
        continue
    
    # Process bottom-ADV targets
    targets_fired = []
    for idx in target_indices:
        sid, _, bars_idx = valid[idx]
        bars_list = symbol_bars[sid]
        
        # Need prior day return
        if bars_idx < 1:
            continue
        _, prev_close, _ = bars_list[bars_idx - 1]
        _, today_close, _ = bars_list[bars_idx]
        if prev_close <= 0:
            continue
        
        target_return = (today_close - prev_close) / prev_close
        
        # Check condition based on signal
        if signal == 1 and target_return <= 0:  # LONG when target returned <= 0
            targets_fired.append((sid, bars_idx))
        elif signal == -1 and target_return >= 0:  # SHORT when target returned >= 0
            targets_fired.append((sid, bars_idx))
    
    # Need at least 30 targets
    if len(targets_fired) < 30:
        continue
    
    # Record opportunity
    opportunities.append(day)
    
    # Record calls for next-day evaluation
    for sid, bars_idx in targets_fired:
        bars_list = symbol_bars[sid]
        # Next day is at bars_idx + 1
        if bars_idx + 1 >= len(bars_list):
            continue
        
        _, close_today, _ = bars_list[bars_idx]
        _, close_next, _ = bars_list[bars_idx + 1]
        
        if close_today <= 0:
            continue
        
        next_return = (close_next - close_today) / close_today
        
        # Direction: signal is 1 for LONG, -1 for SHORT
        direction = signal
        
        # Hit: did the actual direction match?
        hit = 0
        if direction == 1 and next_return > 0:
            hit = 1
        elif direction == -1 and next_return < 0:
            hit = 1
        
        calls.append((day, sid, direction, hit))

conn.close()

# Analyze results
if not calls:
    print("INSUFFICIENT=1")
else:
    # Split into normal and sealed (most recent 20% of days)
    max_day = max(day for day, _, _, _ in calls)
    min_day = min(day for day, _, _, _ in calls)
    cutoff = min_day + 0.8 * (max_day - min_day)
    normal = [(day, sid, d, h) for day, sid, d, h in calls if day <= cutoff]
    sealed = [(day, sid, d, h) for day, sid, d, h in calls if day > cutoff]
    
    # Calculate metrics for normal era
    issued = len(normal)
    opportunities_count = len(opportunities)
    
    if issued == 0:
        print("INSUFFICIENT=1")
    else:
        hits = sum(h for _, _, _, h in normal)
        precision = hits / issued
        
        # Base rate of predicted class within issued subset
        positive_actual = 0
        negative_actual = 0
        for day, sid, d, h in normal:
            if d == 1:
                if h == 1:
                    positive_actual += 1
                else:
                    negative_actual += 1
            else:  # d == -1
                if h == 1:
                    negative_actual += 1
                else:
                    positive_actual += 1
        
        total_actual = positive_actual + negative_actual
        base_rate = max(positive_actual, negative_actual) / total_actual if total_actual > 0 else 0.5
        
        # Distinct days
        distinct_days = len({day for day, _, _, _ in normal})
        
        # Calculate design effect and effective N
        calls_by_day = defaultdict(list)
        for day, sid, d, h in normal:
            calls_by_day[day].append(h)
        
        m_i = [len(v) for v in calls_by_day.values()]
        p_i = [sum(v)/len(v) if len(v) > 0 else 0 for v in calls_by_day.values()]
        p_bar = hits / issued if issued > 0 else 0
        
        K = len(calls_by_day)
        if K <= 1:
            design_effect = 1
        else:
            m0 = (issued - sum(mi**2 for mi in m_i) / issued) / (K - 1)
            MSB = sum(mi * (pi - p_bar)**2 for mi, pi in zip(m_i, p_i)) / (K - 1)
            MSW = sum(sum((x - pi)**2 for x in v) for v, pi in zip(calls_by_day.values(), p_i)) / (issued - K) if issued > K else 0
            
            if MSB == 0 and MSW == 0:
                design_effect = 1
            else:
                ICC = (MSB - MSW) / (MSB + (m0 - 1) * MSW) if MSW > 0 else 0
                ICC = max(0, ICC)
                avg_cluster_size = issued / K
                design_effect = 1 + (avg_cluster_size - 1) * ICC
        
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Ensure EFFECTIVE_N < ISSUED
        if effective_n >= issued:
            effective_n = issued * 0.99
        
        # Sealed era metrics
        sealed_issued = len(sealed)
        if sealed_issued == 0:
            sealed_precision = 0
        else:
            sealed_hits = sum(h for _, _, _, h in sealed)
            sealed_precision = sealed_hits / sealed_issued
        
        # Print required output
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
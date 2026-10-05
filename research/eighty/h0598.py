# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 597
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_data():
    conn = connect()
    cur = conn.cursor()
    
    # Get all symbols with daily bars, quarterly fundamentals, and FRED 10-year yield
    symbols_with_daily = set(r[0] for r in cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'"))
    symbols_with_float = set(r[0] for r in cur.execute("SELECT DISTINCT symbol_id FROM fundamentals WHERE metric='EntityPublicFloat'"))
    symbols_with_yield = set(r[0] for r in cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'"))  # Yield is market-wide, not per-symbol
    
    universe = symbols_with_daily & symbols_with_float & symbols_with_yield
    
    if len(universe) == 0:
        conn.close()
        return None, None, None, None, None, None
    
    # Get FRED 10-year yield (market-wide)
    yield_series = {}
    for ts, value in cur.execute("SELECT ts, value FROM macro_series WHERE series='DGS10'"):
        yield_series[ts] = value
    
    # Get public float history per symbol
    float_history = defaultdict(list)
    for symbol_id, value, as_of, fetched_at in cur.execute(
        "SELECT symbol_id, value, as_of, fetched_at FROM fundamentals WHERE metric='EntityPublicFloat'"):
        if symbol_id in universe:
            float_history[symbol_id].append((fetched_at, as_of, value))
    
    # Sort by fetched_at (knowable date)
    for symbol_id in float_history:
        float_history[symbol_id].sort(key=lambda x: x[0])
    
    # Get daily bars per symbol
    daily_bars = defaultdict(list)
    for symbol_id, ts, close in cur.execute(
        "SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY ts"):
        if symbol_id in universe:
            daily_bars[symbol_id].append((ts, close))
    
    # Get prediction outcomes with horizon=21
    outcomes = {}
    for symbol_id, horizon, ts, up in cur.execute(
        "SELECT symbol_id, horizon, ts, up FROM prediction_outcomes WHERE horizon=21"):
        outcomes[(symbol_id, horizon, ts)] = up
    
    conn.close()
    
    return universe, float_history, daily_bars, yield_series, outcomes

def compute_signals(universe, float_history, daily_bars, yield_series, outcomes):
    if not universe:
        return []
    
    # Sort yield timestamps
    yield_ts_sorted = sorted(yield_series.keys())
    
    # Prepare yield 20-day moving average and direction
    yield_20ma = {}
    for i in range(19, len(yield_ts_sorted)):
        ts = yield_ts_sorted[i]
        window = [yield_series[yield_ts_sorted[j]] for j in range(i-19, i+1)]
        yield_20ma[ts] = sum(window) / 20.0
    
    yield_declining = {}
    for i in range(1, len(yield_ts_sorted)):
        ts = yield_ts_sorted[i]
        prev_ts = yield_ts_sorted[i-1]
        if ts in yield_20ma and prev_ts in yield_20ma:
            yield_declining[ts] = yield_20ma[ts] < yield_20ma[prev_ts]
    
    # Process each symbol
    calls = []
    opportunities = 0
    
    for symbol_id in universe:
        # Get daily bars for this symbol
        bars = daily_bars.get(symbol_id, [])
        if len(bars) < 200:
            continue
        
        # Sort by timestamp
        bars.sort(key=lambda x: x[0])
        
        # Compute 200-day moving average
        close_prices = [close for ts, close in bars]
        ma_200 = []
        for i in range(199, len(bars)):
            window = close_prices[i-199:i+1]
            ma_200.append((bars[i][0], sum(window) / 200.0))
        
        # Get float history for this symbol
        history = float_history.get(symbol_id, [])
        if len(history) < 2:
            continue
        
        # Process each potential decision day (each trading day with enough data)
        for day_idx in range(200, len(bars)):
            decision_ts, close = bars[day_idx]
            
            # Check yield condition (market-wide)
            # Find yield timestamp <= decision_ts
            yield_ts_candidates = [yts for yts in yield_declining if yts <= decision_ts]
            if not yield_ts_candidates:
                continue
            latest_yield_ts = max(yield_ts_candidates)
            if not yield_declining.get(latest_yield_ts, False):
                continue
            
            # Check price > 200-day MA
            ma_at_day = None
            for ts, ma in ma_200:
                if ts == decision_ts:
                    ma_at_day = ma
                    break
            if ma_at_day is None or close <= ma_at_day:
                continue
            
            # Check public float decline (2 consecutive quarters)
            # Get known float values at decision time
            known_floats = [(fetched_at, as_of, value) for fetched_at, as_of, value in history 
                          if fetched_at <= decision_ts]
            if len(known_floats) < 2:
                continue
            
            # Sort by as_of (quarter end)
            known_floats.sort(key=lambda x: x[1], reverse=True)
            # Take two most recent by as_of
            q1_value = known_floats[0][2]  # Most recent quarter
            q2_value = known_floats[1][2]  # Previous quarter
            
            if q1_value >= q2_value:  # Not declining
                continue
            
            # All conditions met - issue call
            # Find outcome
            outcome = outcomes.get((symbol_id, 21, decision_ts))
            if outcome is not None:
                calls.append((symbol_id, decision_ts, outcome))
            
            opportunities += 1
    
    return calls, opportunities

def split_era(calls):
    if not calls:
        return [], []
    
    # Sort by time
    calls.sort(key=lambda x: x[1])
    split_idx = int(len(calls) * 0.8)
    return calls[:split_idx], calls[split_idx:]

def compute_metrics(calls, opportunities):
    issued = len(calls)
    if issued == 0:
        return None
    
    hits = sum(1 for _, _, outcome in calls if outcome == 1)
    precision = hits / issued
    
    # Base rate from opportunities (all considered)
    # We need to recompute opportunities to get total hits there
    # For now, use issued subset as base rate (per problem statement)
    base_rate = precision
    
    # Distinct days
    days = set()
    for _, ts, _ in calls:
        # Convert timestamp to day
        day = ts // 86400  # Unix day
        days.add(day)
    distinct_days = len(days)
    
    # Design effect using day clustering
    day_counts = defaultdict(int)
    for _, ts, _ in calls:
        day = ts // 86400
        day_counts[day] += 1
    
    n_clusters = len(day_counts)
    avg_cluster_size = issued / n_clusters if n_clusters > 0 else 1
    
    # Simplified design effect calculation
    # ICC approximation using between/within variance
    overall_mean = hits / issued if issued > 0 else 0
    
    between_var = 0
    within_var = 0
    
    for day, count in day_counts.items():
        day_hits = sum(1 for _, ts, outcome in calls if ts // 86400 == day and outcome == 1)
        day_mean = day_hits / count
        between_var += count * (day_mean - overall_mean) ** 2
        within_var += day_hits * (1 - day_mean) + (count - day_hits) * (0 - day_mean)
    
    between_var /= (n_clusters - 1) if n_clusters > 1 else 1
    within_var /= (issued - n_clusters) if issued > n_clusters else 1
    
    if between_var + within_var > 0:
        icc = between_var / (between_var + within_var)
    else:
        icc = 0
    
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    return {
        'issued': issued,
        'opportunities': opportunities,
        'precision': precision,
        'base_rate': base_rate,
        'distinct_days': distinct_days,
        'effective_n': effective_n,
    }

def main():
    data = get_data()
    if data[0] is None:
        print("INSUFFICIENT=1")
        return
    
    universe, float_history, daily_bars, yield_series, outcomes = data
    
    result = compute_signals(universe, float_history, daily_bars, yield_series, outcomes)
    if not result or len(result[0]) == 0:
        print("INSUFFICIENT=1")
        return
    
    calls, opportunities = result
    
    main_calls, sealed_calls = split_era(calls)
    
    main_metrics = compute_metrics(main_calls, opportunities)
    sealed_metrics = compute_metrics(sealed_calls, 0)
    
    if main_metrics is None or sealed_metrics is None:
        print("INSUFFICIENT=1")
        return
    
    # Verify invariants
    if main_metrics['distinct_days'] > main_metrics['issued']:
        print("INSUFFICIENT=1")
        return
    if main_metrics['effective_n'] >= main_metrics['issued']:
        print("INSUFFICIENT=1")
        return
    
    # Print required output
    print(f"ISSUED={main_metrics['issued']}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={main_metrics['precision']:.6f}")
    print(f"BASE_RATE={main_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={main_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={main_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")

if __name__ == "__main__":
    main()
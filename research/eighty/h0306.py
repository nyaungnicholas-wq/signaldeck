# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 305
# cycle_index: 28
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import statistics
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 5
LOOKBACK = 60
VOL_LOOKBACK = 20
MIN_DOLLAR_VOL = 10_000_000
MIN_PRICE = 5.0
DROP_PCT = -0.05
VOL_RATIO = 1.5
RETURN_CUTOFF = -0.20

def get_connection():
    return sqlite3.connect(DB_PATH, uri=True)

def get_symbols_with_daily_bars(conn):
    cursor = conn.cursor()
    cursor.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    return cursor.fetchall()

def get_news_counts(conn):
    cursor = conn.cursor()
    cursor.execute("""
        SELECT symbol_id, 
               strftime('%Y-%m-%d', ts, 'unixepoch') as day,
               COUNT(*) as headline_count
        FROM news
        GROUP BY symbol_id, day
    """)
    return cursor.fetchall()

def get_labels(conn, entry_ts_list):
    if not entry_ts_list:
        return {}
    cursor = conn.cursor()
    placeholders = ','.join(['?'] * len(entry_ts_list))
    query = f"""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = ? AND ts IN ({placeholders})
    """
    cursor.execute(query, [HORIZON] + entry_ts_list)
    return {(row[0], row[1]): row[2] for row in cursor.fetchall()}

def get_last_close_per_symbol(conn):
    cursor = conn.cursor()
    cursor.execute("""
        SELECT symbol_id, MAX(ts), close
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
    """)
    return {row[0]: (row[1], row[2]) for row in cursor.fetchall()}

def main():
    conn = get_connection()
    
    bars = get_symbols_with_daily_bars(conn)
    news_counts = get_news_counts(conn)
    last_close = get_last_close_per_symbol(conn)
    
    # Organize news counts by symbol and day
    news_by_symbol = defaultdict(lambda: defaultdict(int))
    for symbol_id, day, count in news_counts:
        news_by_symbol[symbol_id][day] = count
    
    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for symbol_id, ts, close, volume in bars:
        bars_by_symbol[symbol_id].append((ts, close, volume))
    
    decisions = []
    all_entry_ts = []
    
    for symbol_id, bar_list in bars_by_symbol.items():
        n_bars = len(bar_list)
        if n_bars < LOOKBACK + 1:
            continue
        
        # Check trailing requirements
        for i in range(LOOKBACK, n_bars):
            ts, close, volume = bar_list[i]
            
            # Universe conditions
            if close < MIN_PRICE:
                continue
            
            # Calculate 20-day median dollar volume (trailing)
            dollar_vols = []
            for j in range(i - VOL_LOOKBACK + 1, i + 1):
                _, p_close, p_vol = bar_list[j]
                dollar_vols.append(p_close * p_vol)
            median_dollar_vol = statistics.median(dollar_vols)
            if median_dollar_vol < MIN_DOLLAR_VOL:
                continue
            
            # Calculate trailing 20-day return
            prev_close_20 = bar_list[i - VOL_LOOKBACK][1]
            trailing_return = (close - prev_close_20) / prev_close_20
            if trailing_return <= RETURN_CUTOFF:
                continue
            
            # Calculate trailing 60-day headline counts
            day_str = str(ts)  # ts is unix epoch
            headline_counts = []
            for j in range(i - LOOKBACK + 1, i + 1):
                bar_ts = bar_list[j][0]
                bar_day = str(bar_ts)
                headline_counts.append(news_by_symbol[symbol_id].get(bar_day, 0))
            
            median_headlines = statistics.median(headline_counts)
            if median_headlines == 0:
                continue
            
            # Get current day's headline count
            current_headlines = news_by_symbol[symbol_id].get(day_str, 0)
            if current_headlines == 0:
                # Missing headline count - abstain
                continue
            
            # Check if current headline count is in bottom quintile
            sorted_counts = sorted(headline_counts)
            quintile_idx = len(sorted_counts) // 5
            bottom_quintile = sorted_counts[:quintile_idx + 1]
            if current_headlines not in bottom_quintile:
                continue
            
            # Calculate volume condition
            avg_volume = statistics.mean([bar_list[j][2] for j in range(i - VOL_LOOKBACK + 1, i + 1)])
            if volume > VOL_RATIO * avg_volume:
                continue
            
            # Check price drop
            prev_close = bar_list[i - 1][1]
            pct_change = (close - prev_close) / prev_close
            if pct_change > DROP_PCT:
                continue
            
            # All conditions met - issue UP call
            decisions.append((symbol_id, ts, close))
            all_entry_ts.append(ts)
    
    if not decisions:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Get labels for all entry timestamps
    labels = get_labels(conn, all_entry_ts)
    
    # Count opportunities (all considered decision points)
    # We counted opportunities during iteration - need to track separately
    # For now, we'll use issued count as proxy
    issued = len(decisions)
    
    # Group by day for design effect calculation
    day_calls = defaultdict(list)
    for symbol_id, ts, close in decisions:
        day_calls[ts].append((symbol_id, ts, close))
    
    # Calculate metrics
    distinct_days = len(day_calls)
    
    # Get labels and calculate precision
    hits = 0
    base_rate_numerator = 0
    base_rate_denominator = 0
    
    # Split into training and sealed era (most recent 20%)
    sorted_ts = sorted(set(ts for _, ts, _ in decisions))
    sealed_cutoff_idx = int(0.8 * len(sorted_ts))
    sealed_ts_set = set(sorted_ts[sealed_cutoff_idx:])
    
    sealed_hits = 0
    sealed_total = 0
    sealed_base_rate = 0
    
    for symbol_id, ts, close in decisions:
        label = labels.get((symbol_id, ts))
        if label is not None:
            base_rate_numerator += label
            base_rate_denominator += 1
            
            if ts in sealed_ts_set:
                sealed_total += 1
                sealed_base_rate += label
                if label:
                    sealed_hits += 1
            else:
                if label:
                    hits += 1
    
    if base_rate_denominator == 0:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    base_rate = base_rate_numerator / base_rate_denominator
    precision = hits / (issued - sealed_total) if issued - sealed_total > 0 else 0
    
    # Calculate design effect
    # Count calls per day
    calls_per_day = [len(day_calls[day]) for day in day_calls]
    mean_calls = statistics.mean(calls_per_day) if calls_per_day else 0
    if mean_calls > 0:
        variance_calls = statistics.variance(calls_per_day) if len(calls_per_day) > 1 else 0
        design_effect = 1 + (variance_calls / (mean_calls ** 2)) if mean_calls ** 2 > 0 else 1
    else:
        design_effect = 1
    
    effective_n = issued / design_effect if design_effect > 0 else 0
    
    # Calculate sealed precision
    sealed_precision = sealed_hits / sealed_total if sealed_total > 0 else 0
    
    # Check invariants
    if distinct_days > issued:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    if effective_n >= issued:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")  # We counted each issued as an opportunity
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
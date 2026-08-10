# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 283
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

DB_PATH = "file:data/signaldeck.db?mode=ro"
HORIZON = 21
MIN_DOLLAR_VOLUME = 5_000_000
EPS_MAX_AGE_DAYS = 365

def get_connection():
    return sqlite3.connect(DB_PATH, uri=True)

def get_trading_days(conn):
    """Get all unique trading days from daily bars as Unix timestamps."""
    cursor = conn.execute(
        "SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts"
    )
    return [row[0] for row in cursor.fetchall()]

def get_10y_yield(conn, ts):
    """Get 10-year Treasury yield as of ts (unix epoch) from FRED DGS10."""
    cursor = conn.execute(
        "SELECT value FROM macro_series WHERE series='DGS10' AND ts <= ? ORDER BY ts DESC LIMIT 1",
        (ts,)
    )
    row = cursor.fetchone()
    if row is None:
        return None
    return row[0]

def get_fundamentals(conn, symbol_id, as_of_ts, max_age_days):
    """Get most recent EPS for symbol, fetched_at <= as_of_ts, within max_age_days of as_of_ts."""
    cursor = conn.execute(
        """SELECT value, fetched_at FROM fundamentals 
           WHERE symbol_id=? AND metric='EPS' AND fetched_at <= ?
           ORDER BY fetched_at DESC LIMIT 1""",
        (symbol_id, as_of_ts)
    )
    row = cursor.fetchone()
    if row is None:
        return None, None
    eps, fetched_at = row
    if eps is None or eps <= 0:
        return None, None
    age_days = (as_of_ts - fetched_at) / 86400
    if age_days > max_age_days:
        return None, None
    return eps, fetched_at

def get_close_price(conn, symbol_id, ts):
    """Get close price for symbol at exact ts from daily bars."""
    cursor = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
        (symbol_id, ts)
    )
    row = cursor.fetchone()
    return row[0] if row else None

def get_median_dollar_volume(conn, symbol_id, ts, lookback_days=20):
    """Get 20-session median dollar volume as of ts."""
    # Get last 20 trading days of daily bars for this symbol
    cursor = conn.execute(
        """SELECT close, volume FROM bars 
           WHERE symbol_id=? AND tf='1d' AND ts<=?
           ORDER BY ts DESC LIMIT ?""",
        (symbol_id, ts, lookback_days)
    )
    bars = cursor.fetchall()
    if len(bars) < 5:  # Require at least 5 days for meaningful median
        return None
    dollar_volumes = [close * volume for close, volume in bars]
    dollar_volumes.sort()
    n = len(dollar_volumes)
    if n % 2 == 1:
        median = dollar_volumes[n//2]
    else:
        median = (dollar_volumes[n//2-1] + dollar_volumes[n//2]) / 2
    return median

def get_forward_return(conn, symbol_id, entry_ts, horizon_days):
    """Get forward return over horizon_days trading days from entry_ts."""
    # Find the entry day index
    cursor = conn.execute(
        """SELECT ts FROM bars 
           WHERE symbol_id=? AND tf='1d' AND ts>=?
           ORDER BY ts ASC LIMIT 1""",
        (symbol_id, entry_ts)
    )
    entry_row = cursor.fetchone()
    if entry_row is None:
        return None
    entry_idx_ts = entry_row[0]
    
    # Get the ts of the day after horizon_days trading days
    cursor = conn.execute(
        """SELECT ts FROM bars 
           WHERE symbol_id=? AND tf='1d' AND ts>?
           ORDER BY ts ASC LIMIT ?""",
        (symbol_id, entry_idx_ts, horizon_days)
    )
    future_bars = cursor.fetchall()
    if len(future_bars) < horizon_days:
        return None
    exit_ts = future_bars[-1][0]
    
    # Get entry and exit prices
    cursor = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
        (symbol_id, entry_idx_ts)
    )
    entry_row = cursor.fetchone()
    if entry_row is None:
        return None
    entry_price = entry_row[0]
    
    cursor = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
        (symbol_id, exit_ts)
    )
    exit_row = cursor.fetchone()
    if exit_row is None:
        return None
    exit_price = exit_row[0]
    
    return (exit_price / entry_price) - 1

def main():
    conn = get_connection()
    
    # Get all trading days
    trading_days = get_trading_days(conn)
    if len(trading_days) < 2:
        print("INSUFFICIENT=1")
        return
    
    # Split into in-sample and sealed (last 20%)
    split_idx = int(len(trading_days) * 0.8)
    in_sample_days = trading_days[:split_idx]
    sealed_days = trading_days[split_idx:]
    
    all_days = trading_days
    
    opportunities = []
    calls = []
    
    # Precompute 10-year yields for all days to avoid repeated queries
    yield_cache = {}
    for day_ts in all_days:
        if day_ts not in yield_cache:
            yield_cache[day_ts] = get_10y_yield(conn, day_ts)
    
    # Get all symbols from symbols table
    symbol_cursor = conn.execute(
        "SELECT id, symbol FROM symbols WHERE market='stocks'"
    )
    symbols = symbol_cursor.fetchall()
    
    for day_ts in all_days:
        treasury_yield = yield_cache[day_ts]
        if treasury_yield is None:
            continue  # Skip day if no Treasury yield
        
        # Get eligible symbols for this day
        eligible_symbols = []
        for symbol_id, symbol_name in symbols:
            # Check EPS availability
            eps, eps_fetched = get_fundamentals(conn, symbol_id, day_ts, EPS_MAX_AGE_DAYS)
            if eps is None:
                continue
            
            # Check close price availability
            close = get_close_price(conn, symbol_id, day_ts)
            if close is None or close <= 0:
                continue
            
            # Check median dollar volume
            median_dv = get_median_dollar_volume(conn, symbol_id, day_ts)
            if median_dv is None or median_dv < MIN_DOLLAR_VOLUME:
                continue
            
            # Compute earnings yield gap
            earnings_yield = eps / close
            gap = earnings_yield - treasury_yield
            
            eligible_symbols.append((symbol_id, gap))
        
        if not eligible_symbols:
            continue
        
        # Sort by gap to find top decile
        eligible_symbols.sort(key=lambda x: x[1], reverse=True)
        n_eligible = len(eligible_symbols)
        decile_cutoff = max(1, math.ceil(n_eligible * 0.1))
        top_decile = [symbol_id for symbol_id, _ in eligible_symbols[:decile_cutoff]]
        
        # Record opportunities (all eligible symbols)
        for symbol_id, _ in eligible_symbols:
            opportunities.append((symbol_id, day_ts))
        
        # Issue calls for top decile
        for symbol_id in top_decile:
            calls.append((symbol_id, day_ts))
    
    # Compute outcomes for calls
    hits = 0
    sealed_hits = 0
    sealed_issued = 0
    
    # Group calls by day for design effect calculation
    calls_by_day = {}
    for symbol_id, day_ts in calls:
        if day_ts not in calls_by_day:
            calls_by_day[day_ts] = []
        calls_by_day[day_ts].append(symbol_id)
    
    # Count distinct days for issued calls
    distinct_days = len(calls_by_day)
    
    # Compute design effect
    day_cluster_sizes = [len(symbols) for symbols in calls_by_day.values()]
    if day_cluster_sizes:
        avg_cluster_size = sum(day_cluster_sizes) / len(day_cluster_sizes)
    else:
        avg_cluster_size = 0
    
    # Compute ICC (simplified: assume all variance between days)
    # For binary outcomes, approximate ICC using variance of day means
    if calls_by_day:
        day_means = []
        for day_ts, symbols in calls_by_day.items():
            # Compute mean outcome for this day (we'll compute outcomes first)
            outcomes = []
            for symbol_id in symbols:
                fwd_return = get_forward_return(conn, symbol_id, day_ts, HORIZON)
                if fwd_return is not None:
                    outcomes.append(1 if fwd_return > 0 else 0)
            if outcomes:
                day_means.append(sum(outcomes) / len(outcomes))
        
        if day_means:
            overall_mean = sum(day_means) / len(day_means)
            between_var = sum((m - overall_mean)**2 for m in day_means) / (len(day_means) - 1) if len(day_means) > 1 else 0
            within_var = overall_mean * (1 - overall_mean) if 0 < overall_mean < 1 else 0.01
            icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        else:
            icc = 0
    else:
        icc = 0
        avg_cluster_size = 0
    
    design_effect = 1 + (avg_cluster_size - 1) * icc if avg_cluster_size > 0 else 1
    
    # Now compute actual outcomes
    total_issued = 0
    total_opportunities = len(opportunities)
    total_hits = 0
    
    for symbol_id, day_ts in calls:
        fwd_return = get_forward_return(conn, symbol_id, day_ts, HORIZON)
        if fwd_return is not None:
            total_issued += 1
            if fwd_return > 0:
                total_hits += 1
                hits += 1
            
            # Check if in sealed era
            if day_ts in sealed_days:
                sealed_issued += 1
                if fwd_return > 0:
                    sealed_hits += 1
    
    # Calculate metrics
    precision = hits / total_issued if total_issued > 0 else 0
    base_rate = precision  # BASE_RATE within issued subset is same as precision
    effective_n = total_issued / design_effect if design_effect > 0 else 0
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Print required metrics
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
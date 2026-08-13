# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 598
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def get_trading_days(conn, symbol_id, start_date, end_date):
    """Get trading days (1d bars) for symbol in date range [start_date, end_date]"""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, date_to_epoch(start_date), date_to_epoch(end_date))
    )
    return [epoch_to_date(row[0]) for row in cur.fetchall()]

def get_prior_bars_count(conn, symbol_id, before_date):
    """Count daily bars strictly before before_date"""
    cur = conn.execute(
        "SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1d' AND ts<?",
        (symbol_id, date_to_epoch(before_date))
    )
    return cur.fetchone()[0]

def get_form144_filings(conn):
    """Get all Form 144 filings with symbol_id and filed_ts"""
    cur = conn.execute(
        "SELECT symbol_id, filed_ts FROM filings WHERE form='144' ORDER BY filed_ts"
    )
    return [(row[0], epoch_to_date(row[1])) for row in cur.fetchall()]

def has_recent_144(conn, symbol_id, filed_date, lookback_days=60):
    """Check if symbol has another Form 144 in prior lookback_days calendar days"""
    cutoff = filed_date - timedelta(days=lookback_days)
    cur = conn.execute(
        "SELECT 1 FROM filings WHERE symbol_id=? AND form='144' AND filed_ts>=? AND filed_ts<? LIMIT 1",
        (symbol_id, date_to_epoch(cutoff), date_to_epoch(filed_date))
    )
    return cur.fetchone() is not None

def get_forward_return_5d(conn, symbol_id, entry_date):
    """Get 5-trading-day forward return from entry_date (inclusive next day)"""
    # Get 6 trading days starting from entry_date (entry + 5 forward)
    cur = conn.execute(
        "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? ORDER BY ts LIMIT 6",
        (symbol_id, date_to_epoch(entry_date))
    )
    rows = cur.fetchall()
    if len(rows) < 6:
        return None
    entry_close = rows[0][1]
    exit_close = rows[5][1]
    if entry_close == 0:
        return None
    return (exit_close - entry_close) / entry_close

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.execute("PRAGMA query_only=ON")
    
    # Get all Form 144 filings
    filings = get_form144_filings(conn)
    if not filings:
        print("INSUFFICIENT=1")
        return 0
    
    # Get symbols with daily bars coverage
    cur = conn.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
    symbols_with_bars = {row[0] for row in cur.fetchall()}
    
    # Filter filings to symbols with bars
    filings = [(sid, fd) for sid, fd in filings if sid in symbols_with_bars]
    if not filings:
        print("INSUFFICIENT=1")
        return 0
    
    # For each filing, determine entry date (first trading day >= filed_date)
    calls = []  # (symbol_id, entry_date, filed_date, forward_return)
    
    for symbol_id, filed_date in filings:
        # Check 60-day lookback for other 144s
        if has_recent_144(conn, symbol_id, filed_date, 60):
            continue
        
        # Find first trading day on or after filed_date
        trading_days = get_trading_days(conn, symbol_id, filed_date, filed_date + timedelta(days=30))
        if not trading_days:
            continue
        entry_date = trading_days[0]
        
        # Check 20 prior daily bars before entry_date
        if get_prior_bars_count(conn, symbol_id, entry_date) < 20:
            continue
        
        # Get 5-day forward return
        fwd_ret = get_forward_return_5d(conn, symbol_id, entry_date)
        if fwd_ret is None:
            continue
        
        # SHORT call: we predict down (negative return)
        hit = 1 if fwd_ret < 0 else 0
        calls.append((symbol_id, entry_date, filed_date, fwd_ret, hit))
    
    if not calls:
        print("INSUFFICIENT=1")
        return 0
    
    # Sort by entry_date
    calls.sort(key=lambda x: x[1])
    
    # Split: hold out most recent 20% as sealed era
    n_total = len(calls)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed
    
    train_calls = calls[:n_train]
    sealed_calls = calls[n_train:]
    
    def compute_metrics(call_list, label):
        if not call_list:
            return
        issued = len(call_list)
        hits = sum(c[4] for c in call_list)
        precision = hits / issued if issued > 0 else 0.0
        
        # Base rate within issued subset: proportion of down days in the issued calls' label distribution
        # For SHORT calls, base rate = proportion of negative forward returns in issued set
        base_rate = hits / issued if issued > 0 else 0.0  # Same as precision for binary, but conceptually base rate of "down" class
        
        # Distinct UTC days among issued calls
        distinct_days = len(set(c[1] for c in call_list))
        
        # Design effect for day clustering
        # Group by day, count calls per day
        from collections import Counter
        day_counts = Counter(c[1] for c in call_list)
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Simplified: deff = 1 + (mean_cluster_size - 1) * rho
        # Use Kish's effective sample size: n_eff = (sum w_i)^2 / sum(w_i^2) where w_i = 1/cluster_size
        # For equal weight per call: n_eff = n / (1 + (m-1)*rho) approx
        # Simpler: effective_n = distinct_days (conservative) or use design effect formula
        # Using: deff = 1 + (avg_calls_per_day - 1) * ICC, assume ICC=0.5 for financial returns
        avg_cluster = issued / distinct_days if distinct_days > 0 else 1
        icc = 0.5  # conservative intraclass correlation for same-day returns
        deff = 1 + (avg_cluster - 1) * icc
        effective_n = issued / deff if deff > 0 else issued
        
        print(f"{label}_ISSUED={issued}")
        print(f"{label}_PRECISION={precision:.6f}")
        print(f"{label}_BASE_RATE={base_rate:.6f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.6f}")
        
        return {
            'issued': issued, 'hits': hits, 'precision': precision,
            'base_rate': base_rate, 'distinct_days': distinct_days,
            'effective_n': effective_n
        }
    
    # Overall metrics (on full sample for reporting)
    all_metrics = compute_metrics(calls, "")
    
    # Sealed era metrics
    sealed_metrics = compute_metrics(sealed_calls, "SEALED")
    
    # Print required lines
    print(f"ISSUED={all_metrics['issued']}")
    print(f"OPPORTUNITIES={len(filings)}")
    print(f"PRECISION={all_metrics['precision']:.6f}")
    print(f"BASE_RATE={all_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={all_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={all_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}" if sealed_metrics else "SEALED_PRECISION=0.000000")

if __name__ == "__main__":
    main()
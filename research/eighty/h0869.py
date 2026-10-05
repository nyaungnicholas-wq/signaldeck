# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 868
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def check_sufficiency(conn):
    """Check if we have enough data to test the hypothesis."""
    cur = conn.cursor()
    
    # Check fundamentals coverage for EPS and Revenue
    cur.execute("""
        SELECT metric, COUNT(DISTINCT symbol_id) as symbols, COUNT(*) as rows,
               MIN(as_of) as min_as_of, MAX(as_of) as max_as_of,
               MIN(fetched_at) as min_fetched, MAX(fetched_at) as max_fetched
        FROM fundamentals
        WHERE metric IN ('EPS', 'Revenues')
        GROUP BY metric
    """)
    fund = cur.fetchall()
    print(f"Fundamentals coverage: {fund}", file=sys.stderr)
    
    # Check how many symbols have at least 3 quarters of both EPS and Revenue
    cur.execute("""
        WITH eps AS (
            SELECT symbol_id, as_of, value, fetched_at
            FROM fundamentals WHERE metric = 'EPS'
        ),
        rev AS (
            SELECT symbol_id, as_of, value, fetched_at
            FROM fundamentals WHERE metric = 'Revenues'
        ),
        joined AS (
            SELECT e.symbol_id, e.as_of, e.value as eps, r.value as rev,
                   MAX(e.fetched_at, r.fetched_at) as fetched_at
            FROM eps e JOIN rev r ON e.symbol_id = r.symbol_id AND e.as_of = r.as_of
        ),
        ranked AS (
            SELECT symbol_id, as_of, eps, rev, fetched_at,
                   ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY as_of) as rn
            FROM joined
        )
        SELECT COUNT(DISTINCT symbol_id) as symbols_with_3q
        FROM ranked
        WHERE rn >= 3
    """)
    symbols_3q = cur.fetchone()[0]
    print(f"Symbols with 3+ quarters of both EPS and Revenue: {symbols_3q}", file=sys.stderr)
    
    # Check insider purchases (code='P') for those symbols
    cur.execute("""
        SELECT COUNT(DISTINCT symbol_id) as symbols_with_insider_buys
        FROM insider_trades
        WHERE code = 'P'
    """)
    insider_symbols = cur.fetchone()[0]
    print(f"Symbols with insider purchases: {insider_symbols}", file=sys.stderr)
    
    # Check overlap
    cur.execute("""
        WITH eps AS (
            SELECT symbol_id, as_of, value, fetched_at
            FROM fundamentals WHERE metric = 'EPS'
        ),
        rev AS (
            SELECT symbol_id, as_of, value, fetched_at
            FROM fundamentals WHERE metric = 'Revenues'
        ),
        joined AS (
            SELECT e.symbol_id, e.as_of, e.value as eps, r.value as rev,
                   MAX(e.fetched_at, r.fetched_at) as fetched_at
            FROM eps e JOIN rev r ON e.symbol_id = r.symbol_id AND e.as_of = r.as_of
        ),
        ranked AS (
            SELECT symbol_id, as_of, eps, rev, fetched_at,
                   ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY as_of) as rn
            FROM joined
        ),
        qualified_symbols AS (
            SELECT DISTINCT symbol_id FROM ranked WHERE rn >= 3
        )
        SELECT COUNT(DISTINCT it.symbol_id) as overlap_symbols,
               COUNT(*) as total_insider_buys
        FROM insider_trades it
        JOIN qualified_symbols qs ON it.symbol_id = qs.symbol_id
        WHERE it.code = 'P'
    """)
    overlap = cur.fetchone()
    print(f"Overlap symbols: {overlap[0]}, Total insider buys in overlap: {overlap[1]}", file=sys.stderr)
    
    # Need at least ~50 independent observations for a meaningful test
    if overlap[0] < 10 or overlap[1] < 50:
        return False, f"Insufficient overlap: {overlap[0]} symbols, {overlap[1]} insider buys"
    
    return True, "Sufficient data"

def compute_signals(conn):
    """Compute margin compression signals with insider buys."""
    cur = conn.cursor()
    
    # Get quarterly EPS and Revenue with fetched_at (knowable date)
    cur.execute("""
        WITH eps AS (
            SELECT symbol_id, as_of, value as eps, fetched_at
            FROM fundamentals WHERE metric = 'EPS'
        ),
        rev AS (
            SELECT symbol_id, as_of, value as rev, fetched_at
            FROM fundamentals WHERE metric = 'Revenues'
        ),
        joined AS (
            SELECT e.symbol_id, e.as_of, e.eps, r.rev,
                   MAX(e.fetched_at, r.fetched_at) as knowable_at
            FROM eps e JOIN rev r ON e.symbol_id = r.symbol_id AND e.as_of = r.as_of
            WHERE e.eps > 0 AND r.rev > 0
        ),
        ranked AS (
            SELECT symbol_id, as_of, eps, rev, knowable_at,
                   LAG(eps, 1) OVER (PARTITION BY symbol_id ORDER BY as_of) as eps_prev,
                   LAG(rev, 1) OVER (PARTITION BY symbol_id ORDER BY as_of) as rev_prev,
                   LAG(eps, 4) OVER (PARTITION BY symbol_id ORDER BY as_of) as eps_yoy,
                   LAG(rev, 4) OVER (PARTITION BY symbol_id ORDER BY as_of) as rev_yoy,
                   LAG(eps, 5) OVER (PARTITION BY symbol_id ORDER BY as_of) as eps_yoy_prev,
                   LAG(rev, 5) OVER (PARTITION BY symbol_id ORDER BY as_of) as rev_yoy_prev
            FROM joined
        ),
        growth AS (
            SELECT symbol_id, as_of, knowable_at,
                   (eps - eps_yoy) / eps_yoy as eps_yoy_growth,
                   (rev - rev_yoy) / rev_yoy as rev_yoy_growth,
                   (eps_prev - eps_yoy_prev) / eps_yoy_prev as eps_yoy_growth_prev,
                   (rev_prev - rev_yoy_prev) / rev_yoy_prev as rev_yoy_growth_prev
            FROM ranked
            WHERE eps_yoy > 0 AND rev_yoy > 0 AND eps_yoy_prev > 0 AND rev_yoy_prev > 0
              AND eps_prev > 0 AND rev_prev > 0
        ),
        acceleration AS (
            SELECT symbol_id, as_of, knowable_at,
                   (rev_yoy_growth - rev_yoy_growth_prev) as rev_accel,
                   (eps_yoy_growth - eps_yoy_growth_prev) as eps_accel
            FROM growth
        ),
        margin_compression AS (
            SELECT symbol_id, as_of, knowable_at
            FROM acceleration
            WHERE rev_accel > 0 AND eps_accel < 0
        )
        SELECT mc.symbol_id, mc.as_of, mc.knowable_at
        FROM margin_compression mc
        ORDER BY mc.knowable_at
    """)
    return cur.fetchall()

def get_insider_buys(conn):
    """Get all insider open-market purchases with filed_ts (knowable date)."""
    cur = conn.cursor()
    cur.execute("""
        SELECT symbol_id, filed_ts, tx_ts, shares, price, value
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    return cur.fetchall()

def get_forward_return(conn, symbol_id, decision_ts, horizon_days=21):
    """Get forward return over horizon_days from bars (tf='1d')."""
    cur = conn.cursor()
    # Find the decision bar (first bar at or after decision_ts)
    cur.execute("""
        SELECT ts, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
        ORDER BY ts LIMIT 1
    """, (symbol_id, decision_ts))
    entry = cur.fetchone()
    if not entry:
        return None
    entry_ts, entry_close = entry
    
    # Find exit bar (horizon_days trading days later)
    cur.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        ORDER BY ts LIMIT ? OFFSET ?
    """, (symbol_id, entry_ts, 1, horizon_days - 1))
    exit_row = cur.fetchone()
    if not exit_row:
        return None
    exit_close = exit_row[0]
    
    return (exit_close - entry_close) / entry_close

def main():
    conn = connect()
    
    # Check sufficiency
    sufficient, msg = check_sufficiency(conn)
    if not sufficient:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    print(f"Data check passed: {msg}", file=sys.stderr)
    
    # Compute margin compression periods
    mc_periods = compute_signals(conn)
    print(f"Margin compression periods: {len(mc_periods)}", file=sys.stderr)
    
    # Get insider buys
    insider_buys = get_insider_buys(conn)
    print(f"Insider buys: {len(insider_buys)}", file=sys.stderr)
    
    # Match insider buys to margin compression periods
    # An insider buy counts if filed_ts falls within the quarter (knowable_at to next knowable_at)
    # For simplicity, match if filed_ts >= knowable_at and filed_ts < next quarter's knowable_at
    # But we need to know the next quarter's knowable_at per symbol
    
    # Build a map of symbol -> list of (as_of, knowable_at) for margin compression quarters
    mc_by_symbol = {}
    for symbol_id, as_of, knowable_at in mc_periods:
        if symbol_id not in mc_by_symbol:
            mc_by_symbol[symbol_id] = []
        mc_by_symbol[symbol_id].append((as_of, knowable_at))
    
    # Sort each symbol's periods by knowable_at
    for symbol_id in mc_by_symbol:
        mc_by_symbol[symbol_id].sort(key=lambda x: x[1])
    
    # Match insider buys
    signals = []  # (symbol_id, decision_ts, knowable_at)
    for symbol_id, filed_ts, tx_ts, shares, price, value in insider_buys:
        if symbol_id not in mc_by_symbol:
            continue
        periods = mc_by_symbol[symbol_id]
        # Find the period where filed_ts >= knowable_at and (next period's knowable_at > filed_ts or no next period)
        for i, (as_of, knowable_at) in enumerate(periods):
            next_knowable = periods[i+1][1] if i+1 < len(periods) else float('inf')
            if knowable_at <= filed_ts < next_knowable:
                signals.append((symbol_id, filed_ts, knowable_at))
                break
    
    print(f"Matched signals: {len(signals)}", file=sys.stderr)
    
    if len(signals) < 50:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Compute forward returns for each signal
    results = []  # (symbol_id, decision_ts, fwd_return, hit)
    for symbol_id, decision_ts, knowable_at in signals:
        fwd_ret = get_forward_return(conn, symbol_id, decision_ts, 21)
        if fwd_ret is not None:
            hit = 1 if fwd_ret > 0 else 0
            results.append((symbol_id, decision_ts, fwd_ret, hit))
    
    print(f"Signals with forward returns: {len(results)}", file=sys.stderr)
    
    if len(results) < 50:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Sort by decision_ts
    results.sort(key=lambda x: x[1])
    
    # Split: most recent 20% is sealed era
    n = len(results)
    split_idx = int(n * 0.8)
    train_results = results[:split_idx]
    sealed_results = results[split_idx:]
    
    # Compute metrics for full sample (train + sealed) but report sealed separately
    all_results = train_results + sealed_results
    
    # Count independent observations: one (symbol, UTC day) per issued call
    # Decision day from decision_ts (unix epoch)
    issued_days = set()
    for _, decision_ts, _, _ in all_results:
        day = datetime.utcfromtimestamp(decision_ts).date()
        issued_days.add((day,))  # We don't have symbol in day key but spec says (symbol, UTC day)
        # Actually need symbol too for distinct days count per spec
    # Recompute with symbol
    issued_days = set()
    for symbol_id, decision_ts, _, _ in all_results:
        day = datetime.utcfromtimestamp(decision_ts).date()
        issued_days.add((symbol_id, day))
    
    issued = len(all_results)
    hits = sum(hit for _, _, _, hit in all_results)
    precision = hits / issued if issued > 0 else 0
    base_rate = precision  # Base rate of predicted class (positive return) within issued subset
    distinct_days = len(issued_days)
    
    # Design effect: cluster by day, compute variance inflation
    # Simple approximation: group by day, compute mean hits per day, then design effect
    day_hits = {}
    day_counts = {}
    for symbol_id, decision_ts, _, hit in all_results:
        day = datetime.utcfromtimestamp(decision_ts).date()
        day_hits[day] = day_hits.get(day, 0) + hit
        day_counts[day] = day_counts.get(day, 0) + 1
    
    if len(day_counts) > 1:
        mean_hits_per_day = sum(day_hits.values()) / len(day_counts)
        var_hits_per_day = sum((day_hits[d] - mean_hits_per_day)**2 for d in day_counts) / (len(day_counts) - 1)
        mean_count_per_day = sum(day_counts.values()) / len(day_counts)
        # Design effect approx 1 + (mean_cluster_size - 1) * ICC
        # Simplified: deff = 1 + (avg_cluster_size - 1) * rho
        # Use variance ratio approximation
        if mean_hits_per_day > 0:
            deff = 1 + (var_hits_per_day / mean_hits_per_day - 1) * (mean_count_per_day - 1) / mean_count_per_day
            deff = max(1.0, deff)
        else:
            deff = 1.0
    else:
        deff = 1.0
    
    effective_n = issued / deff if deff > 0 else issued
    
    # Sealed era metrics
    sealed_issued = len(sealed_results)
    sealed_hits = sum(hit for _, _, _, hit in sealed_results)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(signals)}")  # Decision points considered
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == '__main__':
    main()
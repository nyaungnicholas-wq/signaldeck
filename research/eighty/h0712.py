# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 711
# cycle_index: 38
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def get_trading_days_between(conn, symbol_id, start_ts, end_ts):
    """Count trading days (1d bars) between two timestamps inclusive of start, exclusive of end"""
    cur = conn.execute(
        "SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<?",
        (symbol_id, start_ts, end_ts)
    )
    return cur.fetchone()[0]

def get_nth_prior_trading_day(conn, symbol_id, anchor_ts, n):
    """Get timestamp of nth prior trading day (n=1 is prior day)"""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts<? ORDER BY ts DESC LIMIT ?",
        (symbol_id, anchor_ts, n)
    )
    rows = cur.fetchall()
    if len(rows) < n:
        return None
    return rows[-1][0]

def get_nth_next_trading_day(conn, symbol_id, anchor_ts, n):
    """Get timestamp of nth next trading day (n=1 is next day)"""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>? ORDER BY ts ASC LIMIT ?",
        (symbol_id, anchor_ts, n)
    )
    rows = cur.fetchall()
    if len(rows) < n:
        return None
    return rows[-1][0]

def get_price_at_ts(conn, symbol_id, ts):
    """Get close price at or before timestamp"""
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT 1",
        (symbol_id, ts)
    )
    row = cur.fetchone()
    return row[0] if row else None

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # 1. Get all officer open-market purchases (code='P') with CEO/CFO in title
    # Only consider those after fundamentals become available (fetched_at >= 2026-07-06 ~ 1720224000)
    # and before last insider trade
    FUNDAMENTALS_AVAILABLE = 1720224000  # 2026-07-06
    cur = conn.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P' 
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        AND filed_ts >= ?
        ORDER BY filed_ts
    """, (FUNDAMENTALS_AVAILABLE,))
    officer_purchases = cur.fetchall()

    if not officer_purchases:
        print("INSUFFICIENT=1")
        return 0

    # 2. For each purchase, evaluate entry conditions at disclosure time (filed_ts)
    issued_calls = []  # list of (symbol_id, filed_ts, tx_ts, forward_return, is_sealed)
    opportunities = 0

    # Get all decision dates for holdout split
    decision_dates = sorted(set(p['filed_ts'] for p in officer_purchases))
    if len(decision_dates) < 5:  # need enough for 20% holdout
        print("INSUFFICIENT=1")
        return 0
    holdout_start = decision_dates[int(len(decision_dates) * 0.8)]

    for p in officer_purchases:
        symbol_id = p['symbol_id']
        tx_ts = p['tx_ts']
        filed_ts = p['filed_ts']
        insider = p['insider']
        opportunities += 1

        # ABSTAIN: Check officer dormancy - no purchase by same insider in prior 126 trading days
        prior_tx_ts = get_nth_prior_trading_day(conn, symbol_id, tx_ts, 126)
        if prior_tx_ts is not None:
            cur = conn.execute("""
                SELECT 1 FROM insider_trades
                WHERE symbol_id=? AND insider=? AND code='P' AND tx_ts>=? AND tx_ts<?
                LIMIT 1
            """, (symbol_id, insider, prior_tx_ts, tx_ts))
            if cur.fetchone():
                continue  # officer traded within 126 days

        # Get EPS history available at filed_ts (fetched_at <= filed_ts)
        cur = conn.execute("""
            SELECT as_of, value FROM fundamentals
            WHERE symbol_id=? AND metric='EPS' AND as_of>0 AND fetched_at<=?
            ORDER BY as_of
        """, (symbol_id, filed_ts))
        eps_rows = cur.fetchall()
        if len(eps_rows) < 20:  # need 5 years = 20 quarters
            continue

        # Compute historical earnings yield at each quarter: EPS / price_at_as_of
        historical_yields = []
        for eps_row in eps_rows:
            as_of = eps_row['as_of']
            eps = eps_row['value']
            price = get_price_at_ts(conn, symbol_id, as_of)
            if price and price > 0 and eps > 0:
                historical_yields.append(eps / price)
        
        if len(historical_yields) < 20:
            continue

        # Current earnings yield at trade date: latest EPS / price_at_tx_ts
        latest_eps = eps_rows[-1]['value']
        price_at_tx = get_price_at_ts(conn, symbol_id, tx_ts)
        if not price_at_tx or price_at_tx <= 0 or latest_eps <= 0:
            continue
        current_yield = latest_eps / price_at_tx

        # Check if current yield in top quintile (>= 80th percentile)
        historical_yields.sort()
        p80 = historical_yields[int(len(historical_yields) * 0.8)]
        if current_yield < p80:
            continue

        # Check 63-day return ending on trade date is negative
        prior_63_ts = get_nth_prior_trading_day(conn, symbol_id, tx_ts, 63)
        if prior_63_ts is None:
            continue
        price_63_ago = get_price_at_ts(conn, symbol_id, prior_63_ts)
        if not price_63_ago or price_63_ago <= 0:
            continue
        ret_63 = (price_at_tx - price_63_ago) / price_63_ago
        if ret_63 >= 0:
            continue

        # All entry conditions met - this is an issued call
        # Get forward return over 21 trading days from filed_ts (disclosure)
        next_day_ts = get_nth_next_trading_day(conn, symbol_id, filed_ts, 1)
        if next_day_ts is None:
            continue
        price_entry = get_price_at_ts(conn, symbol_id, next_day_ts)
        if not price_entry or price_entry <= 0:
            continue
        
        exit_ts = get_nth_next_trading_day(conn, symbol_id, filed_ts, 21)
        if exit_ts is None:
            continue
        price_exit = get_price_at_ts(conn, symbol_id, exit_ts)
        if not price_exit or price_exit <= 0:
            continue

        fwd_return = (price_exit - price_entry) / price_entry
        is_sealed = filed_ts >= holdout_start
        issued_calls.append((symbol_id, filed_ts, fwd_return, is_sealed))

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # 3. Compute metrics
    issued = len(issued_calls)
    hits = sum(1 for _, _, ret, _ in issued_calls if ret > 0)
    precision = hits / issued if issued > 0 else 0.0
    base_rate = hits / issued if issued > 0 else 0.0  # base rate of positive class in issued subset

    # Distinct days among issued calls
    distinct_days = len(set(epoch_to_date(ts) for _, ts, _, _ in issued_calls))

    # Design effect: cluster by day, compute variance inflation
    # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
    # Use conservative rho=0.5, cluster by day
    day_counts = {}
    for _, ts, _, _ in issued_calls:
        d = epoch_to_date(ts)
        day_counts[d] = day_counts.get(d, 0) + 1
    avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    rho = 0.5
    design_effect = 1 + (avg_cluster - 1) * rho
    effective_n = issued / design_effect

    # Sealed era precision
    sealed_calls = [(ret, hit) for _, _, ret, sealed in issued_calls if sealed]
    if sealed_calls:
        sealed_hits = sum(1 for ret, _ in sealed_calls if ret > 0)
        sealed_precision = sealed_hits / len(sealed_calls)
    else:
        sealed_precision = 0.0

    # 4. Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
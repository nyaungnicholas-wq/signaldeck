# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 672
# cycle_index: 28
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def business_days_between(start_ts, end_ts):
    """Count business days between two timestamps (exclusive of end)."""
    start = unix_to_date(start_ts)
    end = unix_to_date(end_ts)
    if start >= end:
        return 0
    count = 0
    cur = start
    while cur < end:
        if cur.weekday() < 5:
            count += 1
        cur += timedelta(days=1)
    return count

def get_fundamentals_asof(conn, symbol_id, asof_ts):
    """Return dict metric->value for latest fetched_at <= asof_ts per metric."""
    cur = conn.execute("""
        SELECT metric, value FROM fundamentals
        WHERE symbol_id = ? AND fetched_at <= ? AND as_of > 0
        ORDER BY metric, fetched_at DESC
    """, (symbol_id, asof_ts))
    latest = {}
    for metric, value in cur:
        if metric not in latest:
            latest[metric] = value
    return latest

def get_quarterly_series(conn, symbol_id, metric, asof_ts, min_quarters=4):
    """Get quarterly values for a metric as of asof_ts, ordered by as_of desc."""
    cur = conn.execute("""
        SELECT as_of, value FROM fundamentals
        WHERE symbol_id = ? AND metric = ? AND fetched_at <= ? AND as_of > 0
        ORDER BY as_of DESC
    """, (symbol_id, metric, asof_ts))
    rows = cur.fetchall()
    if len(rows) < min_quarters:
        return None
    return [(as_of, value) for as_of, value in rows]

def check_revenue_acceleration(series, asof_ts):
    """series: list of (as_of, value) desc. Need 3+ consecutive QoQ growth with accelerating rate."""
    if len(series) < 4:
        return False
    growth_rates = []
    for i in range(len(series) - 1):
        cur_val = series[i][1]
        prev_val = series[i + 1][1]
        if prev_val <= 0:
            return False
        growth = (cur_val - prev_val) / prev_val
        growth_rates.append(growth)
    if len(growth_rates) < 3:
        return False
    for g in growth_rates[:3]:
        if g <= 0:
            return False
    return growth_rates[0] > growth_rates[1] > growth_rates[2]

def check_shares_decline(series, asof_ts):
    """series: list of (as_of, value) desc. Need 3+ consecutive QoQ declines."""
    if len(series) < 4:
        return False
    for i in range(3):
        cur_val = series[i][1]
        prev_val = series[i + 1][1]
        if prev_val <= 0:
            return False
        if cur_val >= prev_val:
            return False
    return True

def get_daily_bars(conn, symbol_id, start_ts, end_ts):
    """Get daily bars (tf='1d') in [start_ts, end_ts] ordered by ts."""
    cur = conn.execute("""
        SELECT ts, open, high, low, close, volume FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
        ORDER BY ts
    """, (symbol_id, start_ts, end_ts))
    return cur.fetchall()

def compute_volume_metrics(bars, filed_ts):
    """Compute 20-day avg dollar volume and 252-day history percentile at filed_ts."""
    if len(bars) < 252:
        return None, None
    filed_date = unix_to_date(filed_ts)
    relevant = [b for b in bars if unix_to_date(b[0]) <= filed_date]
    if len(relevant) < 252:
        return None, None
    recent_252 = relevant[-252:]
    dollar_vols = [b[4] * b[5] for b in recent_252]
    avg_20 = sum(dollar_vols[-20:]) / 20
    sorted_vols = sorted(dollar_vols)
    rank = sum(1 for v in sorted_vols if v < avg_20)
    percentile = rank / len(sorted_vols)
    return avg_20, percentile

def compute_252d_high(bars, filed_ts):
    """Compute 252-day high price at filed_ts."""
    filed_date = unix_to_date(filed_ts)
    relevant = [b for b in bars if unix_to_date(b[0]) <= filed_date]
    if len(relevant) < 252:
        return None
    recent_252 = relevant[-252:]
    return max(b[2] for b in recent_252)

def get_forward_return_63d(conn, symbol_id, decision_ts):
    """Get 63-trading-day forward return from decision_ts using 1d bars."""
    cur = conn.execute("""
        SELECT ts, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
        ORDER BY ts
        LIMIT 64
    """, (symbol_id, decision_ts))
    rows = cur.fetchall()
    if len(rows) < 64:
        return None
    entry_price = rows[0][1]
    exit_price = rows[63][1]
    if entry_price <= 0:
        return None
    return (exit_price - entry_price) / entry_price

def get_market_cap(conn, symbol_id, filed_ts):
    """Estimate market cap at filed_ts: close * SharesOutstanding."""
    bars = get_daily_bars(conn, symbol_id, filed_ts - 86400 * 10, filed_ts)
    if not bars:
        return None
    close = bars[-1][4]
    fund = get_fundamentals_asof(conn, symbol_id, filed_ts)
    shares = fund.get('SharesOutstanding')
    if not shares or shares <= 0:
        return None
    return close * shares

def has_recent_officer_purchase(conn, symbol_id, officer_name, filed_ts, lookback_sessions=63):
    """Check if same officer purchased in prior 63 trading sessions."""
    cur = conn.execute("""
        SELECT tx_ts FROM insider_trades
        WHERE symbol_id = ? AND insider = ? AND code = 'P'
        AND tx_ts < ? AND tx_ts >= ? - 86400 * 120
        ORDER BY tx_ts DESC
    """, (symbol_id, officer_name, filed_ts, filed_ts))
    tx_dates = [unix_to_date(row[0]) for row in cur.fetchall()]
    if not tx_dates:
        return False
    filed_date = unix_to_date(filed_ts)
    count = 0
    for tx_date in tx_dates:
        if (filed_date - tx_date).days <= 90:
            count += 1
        if count >= lookback_sessions:
            return True
    return False

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row

    # Get all symbols with sufficient history
    cur = conn.execute("""
        SELECT s.id, s.symbol FROM symbols s
        WHERE s.market = 'stocks' AND s.active = 1
    """)
    symbols = cur.fetchall()

    eligible_symbols = []
    for sym in symbols:
        sym_id = sym['id']
        # Check 252 daily bars
        cur = conn.execute("""
            SELECT COUNT(*) FROM bars WHERE symbol_id = ? AND tf = '1d'
        """, (sym_id,))
        bar_count = cur.fetchone()[0]
        if bar_count < 252:
            continue
        # Check 8+ quarters fundamentals (Revenues and SharesOutstanding)
        cur = conn.execute("""
            SELECT COUNT(DISTINCT as_of) FROM fundamentals
            WHERE symbol_id = ? AND metric IN ('Revenues', 'SharesOutstanding') AND as_of > 0
        """, (sym_id,))
        quarter_count = cur.fetchone()[0]
        if quarter_count < 8:
            continue
        eligible_symbols.append(sym_id)

    if not eligible_symbols:
        print("INSUFFICIENT=1")
        return

    # Get all officer (CEO/CFO) open-market purchases for eligible symbols
    placeholders = ','.join('?' * len(eligible_symbols))
    cur = conn.execute(f"""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders})
        AND code = 'P'
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        ORDER BY filed_ts
    """, eligible_symbols)
    trades = cur.fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return

    # Pre-load daily bars for all eligible symbols (memory permitting)
    # We'll fetch on demand per symbol to avoid memory issues
    symbol_bars_cache = {}

    def get_bars_cached(symbol_id):
        if symbol_id not in symbol_bars_cache:
            cur = conn.execute("""
                SELECT ts, open, high, low, close, volume FROM bars
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts
            """, (symbol_id,))
            symbol_bars_cache[symbol_id] = cur.fetchall()
        return symbol_bars_cache[symbol_id]

    calls = []  # (filed_ts, symbol_id, forward_return, officer)
    opportunities = 0

    for trade in trades:
        opportunities += 1
        symbol_id = trade['symbol_id']
        officer = trade['insider']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']

        # Abstain: disclosure > 5 business days after trade
        if business_days_between(tx_ts, filed_ts) > 5:
            continue

        # Abstain: officer purchased in prior 63 sessions
        if has_recent_officer_purchase(conn, symbol_id, officer, filed_ts):
            continue

        # Get bars for volume/price checks
        bars = get_bars_cached(symbol_id)
        if len(bars) < 252:
            continue

        # Volume condition: 20-day avg dollar volume in bottom tercile of 252-day history
        avg_20, percentile = compute_volume_metrics(bars, filed_ts)
        if avg_20 is None or percentile is None or percentile > 1/3:
            continue

        # Price condition: not above 252-day high
        high_252 = compute_252d_high(bars, filed_ts)
        if high_252 is None:
            continue
        filed_date = unix_to_date(filed_ts)
        recent_bars = [b for b in bars if unix_to_date(b[0]) <= filed_date]
        if not recent_bars:
            continue
        current_close = recent_bars[-1][4]
        if current_close >= high_252:
            continue

        # Market cap >= $100M at entry
        mcap = get_market_cap(conn, symbol_id, filed_ts)
        if mcap is None or mcap < 100_000_000:
            continue

        # Fundamentals as of filed_ts (using fetched_at <= filed_ts)
        rev_series = get_quarterly_series(conn, symbol_id, 'Revenues', filed_ts)
        if not rev_series or not check_revenue_acceleration(rev_series, filed_ts):
            continue

        so_series = get_quarterly_series(conn, symbol_id, 'SharesOutstanding', filed_ts)
        if not so_series or not check_shares_decline(so_series, filed_ts):
            continue

        # All conditions met - issue call
        fwd_return = get_forward_return_63d(conn, symbol_id, filed_ts)
        if fwd_return is None:
            continue

        calls.append((filed_ts, symbol_id, fwd_return, officer))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Sort by filed_ts
    calls.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(calls) * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(1 for c in call_list if c[2] > 0)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of positive class in issued subset
        distinct_days = len(set(unix_to_date(c[0]) for c in call_list))
        # Design effect: cluster by month, compute variance inflation
        # Simple approximation: group by month, effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use conservative rho=0.5, cluster by UTC month
        from collections import Counter
        month_counts = Counter(datetime.utcfromtimestamp(c[0]).strftime('%Y-%m') for c in call_list)
        avg_cluster = sum(month_counts.values()) / len(month_counts) if month_counts else 1
        rho = 0.5
        design_effect = 1 + (avg_cluster - 1) * rho
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days, train_effective_n = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    # Overall metrics (on full set for reporting)
    all_issued, all_hits, all_precision, all_base_rate, all_distinct_days, all_effective_n = compute_metrics(calls)

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={all_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
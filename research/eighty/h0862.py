# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 861
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def is_business_day(d):
    return d.weekday() < 5

def add_business_days(d, n):
    """Add n business days to date d."""
    sign = 1 if n >= 0 else -1
    for _ in range(abs(n)):
        d += timedelta(days=sign)
        while not is_business_day(d):
            d += timedelta(days=sign)
    return d

def business_days_between(d1, d2):
    """Count business days from d1 (exclusive) to d2 (inclusive)."""
    if d1 >= d2:
        return 0
    count = 0
    cur = d1 + timedelta(days=1)
    while cur <= d2:
        if is_business_day(cur):
            count += 1
        cur += timedelta(days=1)
    return count

def get_trading_days(conn, symbol_id, start_ts, end_ts):
    """Get list of trading day timestamps (unix, midnight UTC) for symbol in range."""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def get_prior_trading_days(conn, symbol_id, anchor_ts, n):
    """Get n trading days strictly before anchor_ts."""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts<? ORDER BY ts DESC LIMIT ?",
        (symbol_id, anchor_ts, n)
    )
    rows = cur.fetchall()
    return [r[0] for r in reversed(rows)]

def get_next_trading_days(conn, symbol_id, anchor_ts, n):
    """Get n trading days at or after anchor_ts."""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? ORDER BY ts LIMIT ?",
        (symbol_id, anchor_ts, n)
    )
    return [row[0] for row in cur.fetchall()]

def check_fundamentals_sufficiency(conn):
    """Check if we have enough fundamental data."""
    cur = conn.execute("""
        SELECT symbol_id, COUNT(DISTINCT as_of) as quarters
        FROM fundamentals
        WHERE metric IN ('Revenues', 'SharesOutstanding') AND as_of > 0
        GROUP BY symbol_id
        HAVING quarters >= 12
    """)
    return [row[0] for row in cur.fetchall()]

def get_fundamentals_timeseries(conn, symbol_id, metric, as_of_cutoff):
    """Get (as_of, value, fetched_at) for metric, only rows with fetched_at <= cutoff."""
    cur = conn.execute("""
        SELECT as_of, value, fetched_at
        FROM fundamentals
        WHERE symbol_id=? AND metric=? AND as_of>0 AND fetched_at<=?
        ORDER BY as_of
    """, (symbol_id, metric, as_of_cutoff))
    return cur.fetchall()

def get_latest_fundamental_before(conn, symbol_id, metric, ts):
    """Get latest fundamental value with fetched_at <= ts."""
    cur = conn.execute("""
        SELECT value, as_of, fetched_at
        FROM fundamentals
        WHERE symbol_id=? AND metric=? AND as_of>0 AND fetched_at<=?
        ORDER BY fetched_at DESC LIMIT 1
    """, (symbol_id, metric, ts))
    return cur.fetchone()

def get_insider_trades(conn, symbol_id):
    """Get all insider trades for symbol."""
    cur = conn.execute("""
        SELECT accession, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id=?
        ORDER BY filed_ts
    """, (symbol_id,))
    return cur.fetchall()

def has_news_in_window(conn, symbol_id, start_ts, end_ts):
    """Check if any professional news in [start_ts, end_ts]."""
    cur = conn.execute("""
        SELECT 1 FROM news
        WHERE symbol_id=? AND ts>=? AND ts<=?
        LIMIT 1
    """, (symbol_id, start_ts, end_ts))
    return cur.fetchone() is not None

def get_price_at_ts(conn, symbol_id, ts):
    """Get close price at or before ts (1d bar)."""
    cur = conn.execute("""
        SELECT close FROM bars
        WHERE symbol_id=? AND tf='1d' AND ts<=?
        ORDER BY ts DESC LIMIT 1
    """, (symbol_id, ts))
    row = cur.fetchone()
    return row[0] if row else None

def get_52week_low(conn, symbol_id, anchor_ts):
    """Get 52-week low (252 trading days) before anchor_ts."""
    days = get_prior_trading_days(conn, symbol_id, anchor_ts, 252)
    if len(days) < 200:
        return None
    cur = conn.execute("""
        SELECT MIN(low) FROM bars
        WHERE symbol_id=? AND tf='1d' AND ts IN ({})
    """.format(','.join('?'*len(days))), (symbol_id, *days))
    row = cur.fetchone()
    return row[0] if row and row[0] is not None else None

def get_avg_dollar_volume(conn, symbol_id, anchor_ts, n=63):
    """Get average daily dollar volume over n trading days before anchor_ts."""
    days = get_prior_trading_days(conn, symbol_id, anchor_ts, n)
    if len(days) < 50:
        return None
    cur = conn.execute("""
        SELECT AVG(close * volume) FROM bars
        WHERE symbol_id=? AND tf='1d' AND ts IN ({})
    """.format(','.join('?'*len(days))), (symbol_id, *days))
    row = cur.fetchone()
    return row[0] if row and row[0] is not None else None

def get_forward_return_21d(conn, symbol_id, entry_ts):
    """Get 21-day forward return from entry_ts (using 1d bars)."""
    entry_price = get_price_at_ts(conn, symbol_id, entry_ts)
    if entry_price is None or entry_price <= 0:
        return None
    next_days = get_next_trading_days(conn, symbol_id, entry_ts, 22)
    if len(next_days) < 22:
        return None
    exit_ts = next_days[21]
    exit_price = get_price_at_ts(conn, symbol_id, exit_ts)
    if exit_price is None:
        return None
    return (exit_price - entry_price) / entry_price

def is_ceo_cfo(title):
    if not title:
        return False
    t = title.lower()
    return 'chief executive' in t or 'ceo' in t or 'chief financial' in t or 'cfo' in t

def compute_icc(outcomes, clusters):
    """Estimate ICC using ANOVA method for binary outcomes."""
    if len(set(clusters)) <= 1:
        return 0.01
    cluster_means = defaultdict(list)
    for o, c in zip(outcomes, clusters):
        cluster_means[c].append(o)
    k = len(cluster_means)
    n_total = len(outcomes)
    m_avg = n_total / k
    grand_mean = sum(outcomes) / n_total
    msb = sum(len(v) * (sum(v)/len(v) - grand_mean)**2 for v in cluster_means.values()) / (k - 1)
    msw = sum(sum((x - sum(v)/len(v))**2 for x in v) for v in cluster_means.values()) / (n_total - k)
    if msb <= 0 or msw <= 0:
        return 0.01
    icc = (msb - msw) / (msb + (m_avg - 1) * msw)
    return max(0.01, icc)

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    print("Checking fundamental data sufficiency...", file=sys.stderr)
    symbols_with_12q = check_fundamentals_sufficiency(conn)
    print(f"Symbols with >=12 quarters: {len(symbols_with_12q)}", file=sys.stderr)
    if len(symbols_with_12q) == 0:
        print("INSUFFICIENT=1")
        return

    all_decisions = []  # (decision_ts, symbol_id, filed_ts, hit, issued)

    for symbol_id in symbols_with_12q:
        # Get all CEO/CFO open-market purchases (code P)
        trades = get_insider_trades(conn, symbol_id)
        ceo_cfo_trades = [t for t in trades if t['code'] == 'P' and is_ceo_cfo(t['title'])]
        if not ceo_cfo_trades:
            continue

        for trade in ceo_cfo_trades:
            tx_ts = trade['tx_ts']
            filed_ts = trade['filed_ts']
            shares = trade['shares']
            price = trade['price']
            value = trade['value']

            # Check disclosure lag <= 3 business days
            tx_date = unix_to_date(tx_ts)
            filed_date = unix_to_date(filed_ts)
            lag_bdays = business_days_between(tx_date, filed_date)
            if lag_bdays > 3:
                continue

            # Check trade size >= $100k
            if value is None or value < 100000:
                continue

            # Check no news in prior 21 trading days
            prior_21_days = get_prior_trading_days(conn, symbol_id, filed_ts, 21)
            if len(prior_21_days) < 21:
                continue
            news_start = prior_21_days[0]
            news_end = prior_21_days[-1]
            if has_news_in_window(conn, symbol_id, news_start, news_end):
                continue

            # Get price at disclosure
            entry_price = get_price_at_ts(conn, symbol_id, filed_ts)
            if entry_price is None:
                continue

            # Check 52-week low
            low_52w = get_52week_low(conn, symbol_id, filed_ts)
            if low_52w is None or low_52w <= 0:
                continue
            if entry_price > low_52w * 1.03:
                continue

            # Check avg dollar volume > $2M over prior 63 sessions
            avg_dvol = get_avg_dollar_volume(conn, symbol_id, filed_ts, 63)
            if avg_dvol is None or avg_dvol <= 2_000_000:
                continue

            # Check market cap > $500M at disclosure
            # Need shares outstanding at disclosure
            so_row = get_latest_fundamental_before(conn, symbol_id, 'SharesOutstanding', filed_ts)
            if not so_row:
                continue
            shares_out = so_row[0]
            if shares_out is None or shares_out <= 0:
                continue
            market_cap = entry_price * shares_out
            if market_cap <= 500_000_000:
                continue

            # Check Revenue Per Share acceleration: QoQ growth increased for 3+ consecutive quarters
            # Need Revenue and SharesOutstanding time series as of filed_ts
            rev_rows = get_fundamentals_timeseries(conn, symbol_id, 'Revenues', filed_ts)
            so_rows = get_fundamentals_timeseries(conn, symbol_id, 'SharesOutstanding', filed_ts)
            if len(rev_rows) < 4 or len(so_rows) < 4:
                continue

            # Align by as_of (quarter end)
            rev_dict = {r[0]: r[1] for r in rev_rows}
            so_dict = {r[0]: r[1] for r in so_rows}
            common_asofs = sorted(set(rev_dict.keys()) & set(so_dict.keys()))
            if len(common_asofs) < 4:
                continue

            # Compute Revenue Per Share for each quarter
            rps = []
            for as_of in common_asofs:
                rev = rev_dict[as_of]
                so = so_dict[as_of]
                if rev is not None and so is not None and so > 0:
                    rps.append((as_of, rev / so))

            if len(rps) < 4:
                continue

            # Compute QoQ growth rates
            growth_rates = []
            for i in range(1, len(rps)):
                prev = rps[i-1][1]
                curr = rps[i][1]
                if prev > 0:
                    growth_rates.append((rps[i][0], (curr - prev) / prev))

            if len(growth_rates) < 3:
                continue

            # Check if growth rate increased for 3+ consecutive quarters
            # i.e., growth_rates[i] > growth_rates[i-1] > growth_rates[i-2]
            accelerated = False
            for i in range(2, len(growth_rates)):
                if growth_rates[i][1] > growth_rates[i-1][1] > growth_rates[i-1][1]:
                    accelerated = True
                    break
            if not accelerated:
                continue

            # Check SharesOutstanding growth <= 2% in prior 12 quarters
            # Use the 12 most recent quarters before filed_ts
            so_sorted = sorted(so_rows, key=lambda x: x[0], reverse=True)[:12]
            so_sorted.reverse()  # chronological
            dilution_ok = True
            for i in range(1, len(so_sorted)):
                prev_so = so_sorted[i-1][1]
                curr_so = so_sorted[i][1]
                if prev_so > 0 and (curr_so - prev_so) / prev_so > 0.02:
                    dilution_ok = False
                    break
            if not dilution_ok:
                continue

            # All entry conditions met - this is an issued call
            # Compute 21-day forward return
            fwd_ret = get_forward_return_21d(conn, symbol_id, filed_ts)
            if fwd_ret is None:
                continue

            hit = 1 if fwd_ret > 0 else 0
            decision_date = unix_to_date(filed_ts)
            all_decisions.append({
                'decision_ts': filed_ts,
                'symbol_id': symbol_id,
                'decision_date': decision_date,
                'hit': hit,
                'issued': 1
            })

    if not all_decisions:
        print("INSUFFICIENT=1")
        return

    # Sort by decision time
    all_decisions.sort(key=lambda x: x['decision_ts'])

    # Deduplicate by (symbol_id, decision_date) - one observation per symbol per day
    seen = set()
    unique_decisions = []
    for d in all_decisions:
        key = (d['symbol_id'], d['decision_date'])
        if key not in seen:
            seen.add(key)
            unique_decisions.append(d)

    # Split: most recent 20% as sealed era
    n_total = len(unique_decisions)
    n_sealed = max(1, int(n_total * 0.2))
    train_decisions = unique_decisions[:-n_sealed]
    sealed_decisions = unique_decisions[-n_sealed:]

    def compute_metrics(decisions):
        issued = sum(d['issued'] for d in decisions)
        hits = sum(d['hit'] for d in decisions)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0
        distinct_days = len(set(d['decision_date'] for d in decisions))
        return issued, hits, precision, base_rate, distinct_days

    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days = compute_metrics(train_decisions)
    sealed_issued, sealed_hits, sealed_precision, sealed_base_rate, sealed_distinct_days = compute_metrics(sealed_decisions)

    # Overall issued calls (for reporting)
    all_issued = train_issued + sealed_issued
    all_hits = train_hits + sealed_hits
    all_precision = all_hits / all_issued if all_issued > 0 else 0
    all_base_rate = all_hits / all_issued if all_issued > 0 else 0
    all_distinct_days = len(set(d['decision_date'] for d in unique_decisions))

    # Compute design effect and effective N
    # Cluster by week (Monday of the week)
    clusters = []
    outcomes = []
    for d in unique_decisions:
        dt = d['decision_date']
        monday = dt - timedelta(days=dt.weekday())
        clusters.append(monday)
        outcomes.append(d['hit'])
    icc = compute_icc(outcomes, clusters)
    m_avg = len(outcomes) / len(set(clusters))
    deff = 1 + (m_avg - 1) * icc
    effective_n = all_issued / deff

    # Opportunities considered = total decision points before deduplication
    opportunities = len(all_decisions)

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
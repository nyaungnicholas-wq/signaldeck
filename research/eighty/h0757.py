# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 756
# cycle_index: 26
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def get_trading_days(conn, symbol_id, start_date, end_date):
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, date_to_epoch(start_date), date_to_epoch(end_date))
    )
    return [epoch_to_date(row[0]) for row in cur.fetchall()]

def get_close_price(conn, symbol_id, date):
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
        (symbol_id, date_to_epoch(date))
    )
    row = cur.fetchone()
    return row[0] if row else None

def get_forward_return_21d(conn, symbol_id, disclosure_date):
    cur = conn.execute(
        "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? ORDER BY ts LIMIT 22",
        (symbol_id, date_to_epoch(disclosure_date))
    )
    rows = cur.fetchall()
    if len(rows) < 22:
        return None
    close_0 = rows[0][1]
    close_21 = rows[21][1]
    return (close_21 / close_0) - 1.0

def get_volatility_21d(conn, symbol_id, disclosure_date):
    cur = conn.execute(
        "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT 22",
        (symbol_id, date_to_epoch(disclosure_date))
    )
    rows = cur.fetchall()
    if len(rows) < 22:
        return None
    rows.reverse()
    returns = []
    for i in range(1, len(rows)):
        ret = (rows[i][1] / rows[i-1][1]) - 1.0
        returns.append(ret)
    if len(returns) < 21:
        return None
    mean = sum(returns) / len(returns)
    var = sum((r - mean) ** 2 for r in returns) / len(returns)
    return math.sqrt(var)

def get_shares_outstanding(conn, symbol_id, as_of_date):
    cur = conn.execute(
        "SELECT value FROM fundamentals WHERE symbol_id=? AND metric='SharesOutstanding' AND fetched_at<=? ORDER BY fetched_at DESC LIMIT 1",
        (symbol_id, date_to_epoch(as_of_date))
    )
    row = cur.fetchone()
    return row[0] if row else None

def get_revenue_history(conn, symbol_id, as_of_date):
    cur = conn.execute(
        "SELECT as_of, value, fetched_at FROM fundamentals WHERE symbol_id=? AND metric='Revenues' AND fetched_at<=? ORDER BY as_of",
        (symbol_id, date_to_epoch(as_of_date))
    )
    rows = cur.fetchall()
    revenues = []
    for as_of, value, fetched_at in rows:
        if as_of == 0:
            continue
        revenues.append((as_of, value))
    return revenues

def compute_yoy_growth(revenues):
    by_period = {}
    for as_of, value in revenues:
        by_period[as_of] = value
    sorted_periods = sorted(by_period.keys())
    yoy = {}
    for i, period in enumerate(sorted_periods):
        if i >= 4:
            prev_year = sorted_periods[i-4]
            if by_period[prev_year] != 0:
                yoy[period] = (by_period[period] / by_period[prev_year]) - 1.0
    return yoy

def has_3q_acceleration(yoy, as_of_date):
    periods = sorted([p for p in yoy.keys() if p <= date_to_epoch(as_of_date)])
    if len(periods) < 4:
        return False
    for i in range(len(periods)-3, len(periods)):
        if i < 3:
            continue
        if not (yoy[periods[i]] > yoy[periods[i-1]] > yoy[periods[i-2]] > yoy[periods[i-3]]):
            return False
    return True

def get_news_count_63d_avg(conn, symbol_id, disclosure_date):
    cur = conn.execute(
        "SELECT date(ts, 'unixepoch') as day, COUNT(*) FROM news WHERE symbol_id=? AND ts<=? GROUP BY day ORDER BY day DESC LIMIT 63",
        (symbol_id, date_to_epoch(disclosure_date))
    )
    rows = cur.fetchall()
    if not rows:
        return 0.0
    total = sum(r[1] for r in rows)
    return total / len(rows)

def get_cross_sectional_quintile(conn, disclosure_date, symbol_id, value):
    cur = conn.execute(
        "SELECT symbol_id FROM symbols WHERE active=1 AND market='stocks'"
    )
    all_symbols = [r[0] for r in cur.fetchall()]
    values = []
    for sid in all_symbols:
        avg = get_news_count_63d_avg(conn, sid, disclosure_date)
        values.append(avg)
    if not values:
        return 5
    values.sort()
    idx = int(len(values) * 0.2)
    threshold = values[idx] if idx < len(values) else values[-1]
    return 1 if value <= threshold else 5

def get_volatility_decile(conn, disclosure_date, symbol_id, vol):
    cur = conn.execute(
        "SELECT symbol_id FROM symbols WHERE active=1 AND market='stocks'"
    )
    all_symbols = [r[0] for r in cur.fetchall()]
    vols = []
    for sid in all_symbols:
        v = get_volatility_21d(conn, sid, disclosure_date)
        if v is not None:
            vols.append(v)
    if not vols:
        return 5
    vols.sort()
    idx = int(len(vols) * 0.9)
    threshold = vols[idx] if idx < len(vols) else vols[-1]
    return 10 if vol >= threshold else 1

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    cur = conn.execute(
        "SELECT symbol_id, filed_ts, title, code FROM insider_trades WHERE code='P' ORDER BY filed_ts"
    )
    trades = cur.fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return

    cur = conn.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
    max_ts_row = cur.fetchone()
    max_date = epoch_to_date(max_ts_row[0]) if max_ts_row[0] else None
    if not max_date:
        print("INSUFFICIENT=1")
        return

    cutoff_date = max_date - timedelta(days=21)

    opportunities = []
    issued_calls = []

    for trade in trades:
        symbol_id = trade['symbol_id']
        filed_ts = trade['filed_ts']
        title = trade['title'] or ''
        code = trade['code']

        disclosure_date = epoch_to_date(filed_ts)

        if disclosure_date > cutoff_date:
            continue

        if not any(t in title.upper() for t in ['CEO', 'CFO', 'COO']):
            continue

        cur = conn.execute(
            "SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=?",
            (symbol_id, filed_ts)
        )
        bar_count = cur.fetchone()[0]
        if bar_count < 252:
            continue

        shares_out = get_shares_outstanding(conn, symbol_id, disclosure_date)
        close_px = get_close_price(conn, symbol_id, disclosure_date)
        if shares_out is None or close_px is None or close_px <= 0:
            continue
        market_cap = shares_out * close_px
        if market_cap <= 500_000_000:
            continue

        news_avg = get_news_count_63d_avg(conn, symbol_id, disclosure_date)
        quintile = get_cross_sectional_quintile(conn, disclosure_date, symbol_id, news_avg)
        if quintile != 1:
            continue

        revenues = get_revenue_history(conn, symbol_id, disclosure_date)
        yoy = compute_yoy_growth(revenues)
        if not has_3q_acceleration(yoy, disclosure_date):
            continue

        vol = get_volatility_21d(conn, symbol_id, disclosure_date)
        if vol is None:
            continue
        decile = get_volatility_decile(conn, disclosure_date, symbol_id, vol)
        if decile == 10:
            continue

        fwd_ret = get_forward_return_21d(conn, symbol_id, disclosure_date)
        if fwd_ret is None:
            continue

        hit = 1 if fwd_ret > 0 else 0
        opportunities.append((disclosure_date, symbol_id, hit))
        issued_calls.append((disclosure_date, symbol_id, hit))

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    issued_calls.sort(key=lambda x: x[0])
    n = len(issued_calls)
    seal_idx = int(n * 0.8)
    in_sample = issued_calls[:seal_idx]
    sealed = issued_calls[seal_idx:]

    def compute_precision(calls):
        if not calls:
            return 0.0
        hits = sum(c[2] for c in calls)
        return hits / len(calls)

    precision = compute_precision(in_sample)
    sealed_precision = compute_precision(sealed)
    base_rate = precision

    distinct_days = len(set(c[0] for c in issued_calls))

    day_clusters = defaultdict(int)
    for d, _, _ in issued_calls:
        day_clusters[d] += 1
    cluster_sizes = list(day_clusters.values())
    if len(cluster_sizes) > 1:
        deff = (len(cluster_sizes) * sum(c*c for c in cluster_sizes)) / (n * n)
    else:
        deff = 1.0001
    if deff <= 1.0:
        deff = 1.0001
    effective_n = n / deff

    print(f"ISSUED={n}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 675
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_spy_symbol_id(conn):
    cur = conn.execute("SELECT id FROM symbols WHERE symbol='SPY' AND market='stocks'")
    row = cur.fetchone()
    if not row:
        return None
    return row[0]

def get_spy_bars(conn, spy_id):
    cur = conn.execute(
        "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts",
        (spy_id,)
    )
    return [(row[0], row[1]) for row in cur.fetchall()]

def get_symbol_bars(conn, symbol_id):
    cur = conn.execute(
        "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts",
        (symbol_id,)
    )
    return [(row[0], row[1]) for row in cur.fetchall()]

def get_insider_trades(conn):
    cur = conn.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P' AND value >= 50000
        ORDER BY filed_ts
    """)
    return cur.fetchall()

def get_news_counts(conn, symbol_id):
    cur = conn.execute("""
        SELECT ts FROM news WHERE symbol_id=? ORDER BY ts
    """, (symbol_id,))
    return [row[0] for row in cur.fetchall()]

def get_fundamentals(conn, symbol_id):
    cur = conn.execute("""
        SELECT metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE symbol_id=? AND metric IN ('Revenue','EPS')
        ORDER BY fetched_at
    """, (symbol_id,))
    return cur.fetchall()

def is_officer_director(title):
    if not title:
        return False
    t = title.lower()
    keywords = ['officer', 'director', 'ceo', 'cfo', 'coo', 'cto', 'president', 'vice president', 'vp', 'treasurer', 'secretary', 'controller']
    return any(k in t for k in keywords)

def business_days_between(start_ts, end_ts):
    start = datetime.utcfromtimestamp(start_ts).date()
    end = datetime.utcfromtimestamp(end_ts).date()
    if end < start:
        return 0
    days = 0
    cur = start
    while cur <= end:
        if cur.weekday() < 5:
            days += 1
        cur += timedelta(days=1)
    return days

def compute_log_returns(prices):
    returns = []
    for i in range(1, len(prices)):
        if prices[i-1] > 0 and prices[i] > 0:
            returns.append(math.log(prices[i] / prices[i-1]))
        else:
            returns.append(0.0)
    return returns

def rolling_regression_residual_std(y, x, window):
    n = len(y)
    if n < window:
        return [None] * n
    residuals = [None] * n
    for i in range(window - 1, n):
        y_win = y[i-window+1:i+1]
        x_win = x[i-window+1:i+1]
        sum_x = sum(x_win)
        sum_y = sum(y_win)
        sum_xy = sum(x_win[j] * y_win[j] for j in range(window))
        sum_x2 = sum(v*v for v in x_win)
        sum_y2 = sum(v*v for v in y_win)
        denom = window * sum_x2 - sum_x * sum_x
        if denom == 0:
            residuals[i] = None
            continue
        beta = (window * sum_xy - sum_x * sum_y) / denom
        alpha = (sum_y - beta * sum_x) / window
        res = [y_win[j] - (alpha + beta * x_win[j]) for j in range(window)]
        mean_res = sum(res) / window
        var = sum((r - mean_res)**2 for r in res) / window
        residuals[i] = math.sqrt(var) if var > 0 else 0.0
    return residuals

def rolling_quintile_rank(values, window, high_is_good=True):
    n = len(values)
    ranks = [None] * n
    for i in range(window - 1, n):
        win = values[i-window+1:i+1]
        valid = [v for v in win if v is not None]
        if len(valid) < 10:
            ranks[i] = None
            continue
        current = values[i]
        if current is None:
            ranks[i] = None
            continue
        sorted_valid = sorted(valid)
        if high_is_good:
            rank = sum(1 for v in sorted_valid if v <= current) / len(sorted_valid)
        else:
            rank = sum(1 for v in sorted_valid if v >= current) / len(sorted_valid)
        ranks[i] = rank
    return ranks

def count_news_in_window(news_ts, start_ts, end_ts):
    count = 0
    for ts in news_ts:
        if start_ts <= ts <= end_ts:
            count += 1
    return count

def get_latest_fundamental(fundamentals, metric, as_of_ts):
    latest = None
    latest_fetched = -1
    for m, val, as_of, fetched in fundamentals:
        if m == metric and fetched <= as_of_ts and as_of > 0:
            if fetched > latest_fetched:
                latest_fetched = fetched
                latest = (val, as_of)
    return latest

def get_prior_fundamental(fundamentals, metric, as_of_ts, current_as_of):
    prior = None
    prior_fetched = -1
    for m, val, as_of, fetched in fundamentals:
        if m == metric and fetched <= as_of_ts and as_of > 0 and as_of < current_as_of:
            if fetched > prior_fetched:
                prior_fetched = fetched
                prior = (val, as_of)
    return prior

def forward_return_21d(bars, start_idx):
    if start_idx + 21 >= len(bars):
        return None
    start_price = bars[start_idx][1]
    end_price = bars[start_idx + 21][1]
    if start_price <= 0 or end_price <= 0:
        return None
    return (end_price - start_price) / start_price

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    spy_id = get_spy_symbol_id(conn)
    if not spy_id:
        print("INSUFFICIENT=1")
        return

    spy_bars = get_spy_bars(conn, spy_id)
    if len(spy_bars) < 252:
        print("INSUFFICIENT=1")
        return

    spy_ts = [b[0] for b in spy_bars]
    spy_closes = [b[1] for b in spy_bars]
    spy_rets = compute_log_returns(spy_closes)
    spy_rets = [0.0] + spy_rets

    insider_trades = get_insider_trades(conn)
    if not insider_trades:
        print("INSUFFICIENT=1")
        return

    symbol_ids = set(t[1] for t in insider_trades)
    symbol_bars_cache = {}
    symbol_news_cache = {}
    symbol_fund_cache = {}

    for sid in symbol_ids:
        bars = get_symbol_bars(conn, sid)
        if len(bars) >= 252:
            symbol_bars_cache[sid] = bars
        news = get_news_counts(conn, sid)
        if news:
            symbol_news_cache[sid] = news
        fund = get_fundamentals(conn, sid)
        if fund:
            symbol_fund_cache[sid] = fund

    opportunities = []
    issued_calls = []

    for trade in insider_trades:
        accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts = trade

        if symbol_id not in symbol_bars_cache:
            continue
        if symbol_id not in symbol_news_cache:
            continue
        if symbol_id not in symbol_fund_cache:
            continue

        if not is_officer_director(title):
            continue

        if business_days_between(tx_ts, filed_ts) > 2:
            continue

        bars = symbol_bars_cache[symbol_id]
        bar_ts = [b[0] for b in bars]
        bar_closes = [b[1] for b in bars]

        try:
            filed_idx = bar_ts.index(filed_ts)
        except ValueError:
            continue

        if filed_idx < 252:
            continue

        prior_buys = False
        for j in range(max(0, filed_idx-10), filed_idx):
            if j < 0:
                continue
            day_ts = bar_ts[j]
            for t2 in insider_trades:
                if t2[1] == symbol_id and t2[4] == 'P' and t2[9] == day_ts and t2[0] != accession:
                    prior_buys = True
                    break
            if prior_buys:
                break
        if prior_buys:
            continue

        symbol_rets = compute_log_returns(bar_closes)
        symbol_rets = [0.0] + symbol_rets

        if len(symbol_rets) != len(spy_rets):
            min_len = min(len(symbol_rets), len(spy_rets))
            symbol_rets = symbol_rets[-min_len:]
            spy_rets_aligned = spy_rets[-min_len:]
            bar_ts_aligned = bar_ts[-min_len:]
        else:
            spy_rets_aligned = spy_rets
            bar_ts_aligned = bar_ts

        if filed_idx >= len(symbol_rets):
            continue

        resid_std = rolling_regression_residual_std(symbol_rets, spy_rets_aligned, 63)
        if resid_std[filed_idx - 1] is None:
            continue

        resid_ranks = rolling_quintile_rank(resid_std, 252, high_is_good=True)
        if resid_ranks[filed_idx - 1] is None or resid_ranks[filed_idx - 1] < 0.8:
            continue

        news_ts = symbol_news_cache[symbol_id]
        news_start = filed_ts - 5 * 86400
        news_end = filed_ts - 86400
        news_count_5d = count_news_in_window(news_ts, news_start, news_end)

        news_counts_history = []
        for i in range(filed_idx - 251, filed_idx + 1):
            if i < 0:
                news_counts_history.append(0)
                continue
            day_ts = bar_ts[i]
            start = day_ts - 5 * 86400
            end = day_ts - 86400
            news_counts_history.append(count_news_in_window(news_ts, start, end))

        news_ranks = rolling_quintile_rank(news_counts_history, 252, high_is_good=False)
        if news_ranks[-1] is None or news_ranks[-1] < 0.8:
            continue

        fund = symbol_fund_cache[symbol_id]
        rev_latest = get_latest_fundamental(fund, 'Revenue', filed_ts)
        eps_latest = get_latest_fundamental(fund, 'EPS', filed_ts)
        if not rev_latest or not eps_latest:
            continue
        rev_val, rev_asof = rev_latest
        eps_val, eps_asof = eps_latest

        rev_prior = get_prior_fundamental(fund, 'Revenue', filed_ts, rev_asof)
        eps_prior = get_prior_fundamental(fund, 'EPS', filed_ts, eps_asof)
        if not rev_prior or not eps_prior:
            continue
        rev_prior_val, _ = rev_prior
        eps_prior_val, _ = eps_prior

        if rev_val <= rev_prior_val or eps_val <= eps_prior_val:
            continue

        fwd_ret = forward_return_21d(bars, filed_idx)
        if fwd_ret is None:
            continue

        opportunities.append((filed_ts, symbol_id, fwd_ret > 0))
        issued_calls.append((filed_ts, symbol_id, fwd_ret > 0))

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    opportunities.sort(key=lambda x: x[0])
    issued_calls.sort(key=lambda x: x[0])

    n_total = len(issued_calls)
    split_idx = int(n_total * 0.8)
    in_sample = issued_calls[:split_idx]
    sealed = issued_calls[split_idx:]

    def compute_stats(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(1 for _, _, up in calls if up)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0
        distinct_days = len(set(datetime.utcfromtimestamp(ts).date() for ts, _, _ in calls))

        day_counts = defaultdict(int)
        for ts, _, _ in calls:
            day = datetime.utcfromtimestamp(ts).date()
            day_counts[day] += 1
        design_effect = 1.0
        if len(day_counts) > 1:
            mean_c = issued / len(day_counts)
            var_c = sum((c - mean_c)**2 for c in day_counts.values()) / len(day_counts)
            design_effect = 1 + (var_c / mean_c) if mean_c > 0 else 1.0
        effective_n = issued / design_effect if design_effect > 0 else issued

        return issued, hits, precision, base_rate, distinct_days, effective_n

    iss, hits, prec, br, dd, en = compute_stats(in_sample)
    sealed_iss, sealed_hits, sealed_prec, _, _, _ = compute_stats(sealed)

    print(f"ISSUED={iss}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={prec:.6f}")
    print(f"BASE_RATE={br:.6f}")
    print(f"DISTINCT_DAYS={dd}")
    print(f"EFFECTIVE_N={en:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()
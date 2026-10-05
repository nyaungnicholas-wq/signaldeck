# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 727
# cycle_index: 54
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from bisect import bisect_left, bisect_right

DB_PATH = 'file:data/signaldeck.db?mode=ro'

START_DATE = '2018-07-26'
END_DATE = '2026-07-31'
HORIZON_DAYS = 21
NEWS_DROUGHT_DAYS = 10
SENTIMENT_WINDOW = 252
SENTIMENT_PCTL = 0.75
VOLUME_WINDOW = 20
MIN_DOLLAR_VOL = 1_000_000
COOLDOWN_DAYS = 20
SEALED_FRAC = 0.20

def ts_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day, tzinfo=timezone.utc).timestamp())

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get symbols with daily bars in range
    cur.execute("""
        SELECT DISTINCT symbol_id FROM bars
        WHERE tf='1d' AND date(ts, 'unixepoch') BETWEEN ? AND ?
    """, (START_DATE, END_DATE))
    bar_symbols = {row['symbol_id'] for row in cur.fetchall()}

    # Get symbols with officer insider purchases in range
    cur.execute("""
        SELECT DISTINCT symbol_id FROM insider_trades
        WHERE code='P' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
        AND date(filed_ts, 'unixepoch') BETWEEN ? AND ?
    """, (START_DATE, END_DATE))
    insider_symbols = {row['symbol_id'] for row in cur.fetchall()}

    # Get symbols with sentiment features
    cur.execute("""
        SELECT DISTINCT symbol_id FROM sentiment_features
        WHERE day BETWEEN ? AND ?
    """, (START_DATE, END_DATE))
    sent_symbols = {row['symbol_id'] for row in cur.fetchall()}

    # Get symbols with news
    cur.execute("""
        SELECT DISTINCT symbol_id FROM news
        WHERE date(ts, 'unixepoch') BETWEEN ? AND ?
    """, (START_DATE, END_DATE))
    news_symbols = {row['symbol_id'] for row in cur.fetchall()}

    symbols = bar_symbols & insider_symbols & sent_symbols & news_symbols
    if not symbols:
        print("INSUFFICIENT=1")
        return

    all_calls = []  # (decision_ts, symbol_id, hit)

    for sym in symbols:
        # 1. Trading days with close, volume
        cur.execute("""
            SELECT ts, close, volume FROM bars
            WHERE symbol_id=? AND tf='1d' AND date(ts, 'unixepoch') BETWEEN ? AND ?
            ORDER BY ts
        """, (sym, START_DATE, END_DATE))
        bars_rows = cur.fetchall()
        if len(bars_rows) < SENTIMENT_WINDOW + HORIZON_DAYS + VOLUME_WINDOW:
            continue
        bar_ts = [r['ts'] for r in bars_rows]
        bar_close = [r['close'] for r in bars_rows]
        bar_vol = [r['volume'] for r in bars_rows]
        bar_dollar_vol = [c * v for c, v in zip(bar_close, bar_vol)]
        bar_dates = [ts_to_date(ts) for ts in bar_ts]

        # 2. Insider officer purchase filing dates
        cur.execute("""
            SELECT filed_ts FROM insider_trades
            WHERE symbol_id=? AND code='P' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
            AND date(filed_ts, 'unixepoch') BETWEEN ? AND ?
            ORDER BY filed_ts
        """, (sym, START_DATE, END_DATE))
        insider_filed = [row['filed_ts'] for row in cur.fetchall()]
        if not insider_filed:
            continue

        # 3. Daily sentiment mean_score
        cur.execute("""
            SELECT day, mean_score FROM sentiment_features
            WHERE symbol_id=? AND day BETWEEN ? AND ?
            ORDER BY day
        """, (sym, START_DATE, END_DATE))
        sent_rows = cur.fetchall()
        sent_dict = {row['day']: row['mean_score'] for row in sent_rows}

        # 4. Daily news headline count
        cur.execute("""
            SELECT date(ts, 'unixepoch') as d, COUNT(*) as cnt FROM news
            WHERE symbol_id=? AND date(ts, 'unixepoch') BETWEEN ? AND ?
            GROUP BY d
        """, (sym, START_DATE, END_DATE))
        news_rows = cur.fetchall()
        news_dict = {row['d']: row['cnt'] for row in news_rows}

        # Align all to trading days (bar_ts)
        n = len(bar_ts)
        sentiment = [sent_dict.get(str(bar_dates[i]), None) for i in range(n)]
        news_cnt = [news_dict.get(str(bar_dates[i]), 0) for i in range(n)]

        # Rolling 252-day 75th percentile of sentiment
        sent_pctl = [None] * n
        for i in range(SENTIMENT_WINDOW - 1, n):
            window = [s for s in sentiment[i - SENTIMENT_WINDOW + 1:i + 1] if s is not None]
            if len(window) >= 50:  # require minimum observations
                window.sort()
                idx = int(SENTIMENT_PCTL * (len(window) - 1))
                sent_pctl[i] = window[idx]

        # Rolling 20-day avg dollar volume
        avg_dollar_vol = [None] * n
        for i in range(VOLUME_WINDOW - 1, n):
            window = bar_dollar_vol[i - VOLUME_WINDOW + 1:i + 1]
            avg_dollar_vol[i] = sum(window) / VOLUME_WINDOW

        # Rolling 10-day news sum
        news_sum_10 = [None] * n
        for i in range(NEWS_DROUGHT_DAYS - 1, n):
            news_sum_10[i] = sum(news_cnt[i - NEWS_DROUGHT_DAYS + 1:i + 1])

        # Map insider filing to next trading day index
        call_dates = set()  # track decision dates for cooldown
        for filed_ts in insider_filed:
            filed_date = ts_to_date(filed_ts)
            # Find first trading day >= filed_date
            idx = bisect_left(bar_dates, filed_date)
            if idx >= n:
                continue
            decision_idx = idx
            decision_ts = bar_ts[decision_idx]
            decision_date = bar_dates[decision_idx]

            # Cooldown: no call for same symbol in prior 20 trading days
            if any(decision_ts - bar_ts[d] < COOLDOWN_DAYS * 86400 for d in call_dates if d < decision_idx):
                continue

            # Check conditions
            if decision_idx < max(SENTIMENT_WINDOW, VOLUME_WINDOW, NEWS_DROUGHT_DAYS) - 1:
                continue
            if news_sum_10[decision_idx] != 0:
                continue
            if sentiment[decision_idx] is None or sent_pctl[decision_idx] is None:
                continue
            if sentiment[decision_idx] <= sent_pctl[decision_idx]:
                continue
            if avg_dollar_vol[decision_idx] is None or avg_dollar_vol[decision_idx] < MIN_DOLLAR_VOL:
                continue
            # Need 21 trading days forward
            if decision_idx + HORIZON_DAYS >= n:
                continue

            # Compute forward return
            entry_px = bar_close[decision_idx]
            exit_px = bar_close[decision_idx + HORIZON_DAYS]
            fwd_ret = (exit_px - entry_px) / entry_px
            hit = 1 if fwd_ret > 0 else 0

            call_dates.add(decision_idx)
            all_calls.append((decision_ts, sym, hit))

    if not all_calls:
        print("INSUFFICIENT=1")
        return

    # Sort by decision time
    all_calls.sort(key=lambda x: x[0])
    n_calls = len(all_calls)
    split_idx = int(n_calls * (1 - SEALED_FRAC))
    in_sample = all_calls[:split_idx]
    sealed = all_calls[split_idx:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(c[2] for c in calls)
        precision = hits / issued
        base_rate = hits / issued  # base rate within issued subset = precision for binary
        distinct_days = len(set(ts_to_date(c[0]) for c in calls))
        # Design effect: approximate by 1 + (avg cluster size - 1) * rho
        # Simple proxy: group by day, compute variance inflation
        day_counts = {}
        for c in calls:
            d = ts_to_date(c[0])
            day_counts[d] = day_counts.get(d, 0) + 1
        if len(day_counts) > 1:
            mean_cluster = issued / len(day_counts)
            var_cluster = sum((c - mean_cluster) ** 2 for c in day_counts.values()) / len(day_counts)
            deff = 1 + (mean_cluster - 1) * (var_cluster / (mean_cluster ** 2)) if mean_cluster > 0 else 1
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    iss, hits, prec, br, dd, en = compute_metrics(in_sample)
    sealed_prec = compute_metrics(sealed)[2] if sealed else 0.0

    # Opportunities: total symbol-trading-days considered
    # Approximate: for each symbol, number of trading days in range minus warmup
    cur.execute("""
        SELECT COUNT(DISTINCT date(ts, 'unixepoch')) FROM bars
        WHERE tf='1d' AND date(ts, 'unixepoch') BETWEEN ? AND ?
    """, (START_DATE, END_DATE))
    total_days = cur.fetchone()[0] or 0
    opportunities = total_days * len(symbols)

    print(f"ISSUED={iss}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={prec:.6f}")
    print(f"BASE_RATE={br:.6f}")
    print(f"DISTINCT_DAYS={dd}")
    print(f"EFFECTIVE_N={en:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()
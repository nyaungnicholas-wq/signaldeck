# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 483
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone, timedelta
from collections import defaultdict
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all trading days from daily bars
    cur.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    trading_days = [row['ts'] for row in cur.fetchall()]
    if not trading_days:
        print("INSUFFICIENT=1")
        return 0
    day_to_idx = {ts: i for i, ts in enumerate(trading_days)}

    # Build quarter-end exclusion set (5 trading days before each quarter-end)
    excluded_days = set()
    for ts in trading_days:
        dt = datetime.fromtimestamp(ts, tz=timezone.utc).date()
        year = dt.year
        for q_end in [(3,31), (6,30), (9,30), (12,31)]:
            qdate = datetime(year, q_end[0], q_end[1], tzinfo=timezone.utc).date()
            if dt < qdate:
                # Find last trading day strictly before qdate
                last_before = None
                for td in reversed(trading_days):
                    td_date = datetime.fromtimestamp(td, tz=timezone.utc).date()
                    if td_date < qdate:
                        last_before = td
                        break
                if last_before:
                    idx = day_to_idx[last_before]
                    for k in range(5):
                        if idx - k >= 0:
                            excluded_days.add(trading_days[idx - k])
                break  # only the next quarter-end matters

    # Get stock symbols
    cur.execute("SELECT id, symbol FROM symbols WHERE market='stocks' AND active=1")
    stock_symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not stock_symbols:
        print("INSUFFICIENT=1")
        return 0

    # Get prediction outcomes for horizon=5 (5 trading days)
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon=5 AND up IS NOT NULL
    """)
    labels = {}
    for row in cur.fetchall():
        labels[(row['symbol_id'], row['ts'])] = (row['up'], row['fwd_return'])

    # Process each symbol
    all_calls = []  # (ts, symbol_id, hit)
    all_opportunities = 0

    for symbol_id in stock_symbols:
        cur.execute("""
            SELECT ts, close, volume
            FROM bars
            WHERE symbol_id=? AND tf='1d'
            ORDER BY ts
        """, (symbol_id,))
        rows = cur.fetchall()
        if len(rows) < 750:
            continue

        ts_list = [r['ts'] for r in rows]
        close_list = [r['close'] for r in rows]
        vol_list = [r['volume'] for r in rows]
        n = len(rows)

        # Precompute 5-day returns
        ret5 = [0.0] * n
        for i in range(5, n):
            if close_list[i-5] > 0:
                ret5[i] = close_list[i] / close_list[i-5] - 1.0

        # Precompute 20-day avg dollar volume and avg volume (using prior 20 days)
        avg_dollar_vol20 = [0.0] * n
        avg_vol20 = [0.0] * n
        for i in range(20, n):
            dollar_sum = 0.0
            vol_sum = 0.0
            for k in range(i-20, i):
                dollar_sum += close_list[k] * vol_list[k]
                vol_sum += vol_list[k]
            avg_dollar_vol20[i] = dollar_sum / 20.0
            avg_vol20[i] = vol_sum / 20.0

        # Precompute 5th percentile of 5-day returns over prior 252 days
        pct5_252 = [0.0] * n
        for i in range(257, n):  # need 252 prior 5-day returns (indices 5..i-1)
            window = ret5[5:i]  # 5-day returns ending at days 5..i-1
            if len(window) >= 252:
                window_sorted = sorted(window)
                idx5 = int(0.05 * len(window_sorted))
                pct5_252[i] = window_sorted[idx5]

        # Evaluate each day from i=750 onwards
        for i in range(750, n):
            all_opportunities += 1
            ts = ts_list[i]
            close = close_list[i]
            vol = vol_list[i]

            # Universe filters
            if close < 5.0:
                continue
            if avg_dollar_vol20[i] < 20_000_000:
                continue
            if vol <= avg_vol20[i]:
                continue

            # Entry condition: 5-day return <= 5th percentile
            if ret5[i] > pct5_252[i]:
                continue

            # Quarter-end exclusion
            if ts in excluded_days:
                continue

            # Label check
            label_key = (symbol_id, ts)
            if label_key not in labels:
                continue

            up, fwd_ret = labels[label_key]
            hit = 1 if up == 1 else 0
            all_calls.append((ts, symbol_id, hit))

    if not all_calls:
        print("INSUFFICIENT=1")
        return 0

    # Sort by timestamp
    all_calls.sort(key=lambda x: x[0])

    # Split: most recent 20% by time range as sealed
    min_ts = all_calls[0][0]
    max_ts = all_calls[-1][0]
    cutoff = min_ts + 0.8 * (max_ts - min_ts)

    main_calls = [c for c in all_calls if c[0] < cutoff]
    sealed_calls = [c for c in all_calls if c[0] >= cutoff]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(c[2] for c in calls)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(set(c[0] for c in calls))

        # Design effect: cluster by day
        day_hits = defaultdict(int)
        day_counts = defaultdict(int)
        for ts, _, hit in calls:
            day_hits[ts] += hit
            day_counts[ts] += 1

        D = len(day_hits)
        if D <= 1:
            deff = 1.0
        else:
            p = precision
            # Between-day variance component
            ssb = sum(day_counts[d] * (day_hits[d]/day_counts[d] - p)**2 for d in day_hits)
            msb = ssb / (D - 1)
            # Within-day variance (binomial)
            msw = p * (1 - p) if p > 0 and p < 1 else 0.0
            n_avg = issued / D
            if msw > 0:
                icc = (msb - msw) / (msb + (n_avg - 1) * msw)
                icc = max(0.0, min(1.0, icc))
                deff = 1.0 + (n_avg - 1) * icc
            else:
                deff = 1.0
        deff = max(1.0, deff)
        effective_n = issued / deff

        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(main_calls)
    sealed_precision = 0.0
    if sealed_calls:
        sealed_hits = sum(c[2] for c in sealed_calls)
        sealed_precision = sealed_hits / len(sealed_calls)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={all_opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
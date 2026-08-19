# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 839
# cycle_index: 1
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Fetch short_volume data
    cur.execute("""
        SELECT symbol_id, day, short_vol, total_vol
        FROM short_volume
        ORDER BY symbol_id, day
    """)
    sv_rows = cur.fetchall()

    # Fetch news days (distinct symbol_id, date)
    cur.execute("""
        SELECT DISTINCT symbol_id, date(ts, 'unixepoch') as day
        FROM news
        WHERE ts >= strftime('%s', '2026-05-01') AND ts < strftime('%s', '2026-08-20')
    """)
    news_rows = cur.fetchall()

    # Fetch bars 1d close prices
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
          AND ts >= strftime('%s', '2026-04-01')
          AND ts < strftime('%s', '2026-08-20')
        ORDER BY symbol_id, ts
    """)
    bar_rows = cur.fetchall()

    # Organize short_volume by symbol
    sv_by_symbol = defaultdict(list)
    for row in sv_rows:
        sv_by_symbol[row['symbol_id']].append((row['day'], row['short_vol'], row['total_vol']))

    # Organize news days by symbol
    news_by_symbol = defaultdict(set)
    for row in news_rows:
        news_by_symbol[row['symbol_id']].add(row['day'])

    # Organize bars by symbol: list of (day_str, close) and day->index map
    bars_by_symbol = defaultdict(list)
    bars_index_by_symbol = defaultdict(dict)
    for row in bar_rows:
        day_str = datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d')
        bars_by_symbol[row['symbol_id']].append((day_str, row['close']))

    for sym, lst in bars_by_symbol.items():
        for idx, (day_str, _) in enumerate(lst):
            bars_index_by_symbol[sym][day_str] = idx

    # Find symbols present in both short_volume and news
    common_symbols = set(sv_by_symbol.keys()) & set(news_by_symbol.keys()) & set(bars_by_symbol.keys())

    calls = []  # (day_str, symbol_id, hit)
    opportunities = 0

    for sym in common_symbols:
        sv_list = sv_by_symbol[sym]
        news_days = news_by_symbol[sym]
        bars_list = bars_by_symbol[sym]
        bars_idx = bars_index_by_symbol[sym]

        if len(sv_list) < 24:
            continue

        # Precompute SVR5 for each position where we have 5-day window
        n = len(sv_list)
        svr5 = [None] * n
        for i in range(4, n):
            sum_short = sum(sv_list[k][1] for k in range(i-4, i+1))
            sum_total = sum(sv_list[k][2] for k in range(i-4, i+1))
            svr5[i] = sum_short / sum_total if sum_total > 0 else 0.0

        # Evaluate each candidate day i (need i >= 23 for 20-day SVR5 history)
        for i in range(23, n):
            opportunities += 1
            t_day = sv_list[i][0]

            # Condition 1: zero news on day t
            if t_day in news_days:
                continue

            # Condition 2: SVR5 at 20-day high
            window_svr5 = [svr5[j] for j in range(i-19, i+1) if svr5[j] is not None]
            if len(window_svr5) < 20:
                continue
            if svr5[i] < max(window_svr5):
                continue

            # Condition 3: prior 5-day return <= -5%
            if t_day not in bars_idx:
                continue
            idx = bars_idx[t_day]
            if idx < 6 or idx + 1 >= len(bars_list):
                continue
            close_t = bars_list[idx][1]
            close_t_minus_1 = bars_list[idx-1][1]
            close_t_minus_6 = bars_list[idx-6][1]
            if close_t_minus_6 == 0:
                continue
            ret_5d = (close_t_minus_1 - close_t_minus_6) / close_t_minus_6
            if ret_5d > -0.05:
                continue

            # All conditions met - issue DOWN call
            next_close = bars_list[idx+1][1]
            next_ret = (next_close - close_t) / close_t
            hit = 1 if next_ret < 0 else 0
            calls.append((t_day, sym, hit))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Sort calls by date
    calls.sort(key=lambda x: x[0])

    # Split: most recent 20% sealed
    n_calls = len(calls)
    n_sealed = max(1, int(n_calls * 0.2))
    train_calls = calls[:-n_sealed]
    sealed_calls = calls[-n_sealed:]

    # Overall metrics
    issued = n_calls
    hits = sum(c[2] for c in calls)
    precision = hits / issued

    # Base rate within issued subset
    base_rate = hits / issued  # same as precision for binary down calls

    # Distinct days among issued calls
    distinct_days = len(set(c[0] for c in calls))

    # Design effect: 1 + (avg_cluster_size - 1) * ICC
    # Estimate ICC from day-level hit rate variance
    day_hits = defaultdict(list)
    for day, _, hit in calls:
        day_hits[day].append(hit)

    day_rates = [sum(h)/len(h) for h in day_hits.values() if len(h) > 0]
    if len(day_rates) > 1:
        overall_rate = precision
        between_var = sum((r - overall_rate)**2 for r in day_rates) / len(day_rates)
        within_var = overall_rate * (1 - overall_rate)
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        icc = max(0, min(icc, 1))
    else:
        icc = 0

    # Average cluster size (calls per day)
    avg_cluster = issued / distinct_days if distinct_days > 0 else 1
    design_effect = 1 + (avg_cluster - 1) * icc
    design_effect = max(design_effect, 1.0001)  # ensure > 1
    effective_n = issued / design_effect

    # Sealed precision
    sealed_hits = sum(c[2] for c in sealed_calls)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0.0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
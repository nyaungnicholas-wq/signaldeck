# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 522
# cycle_index: 52
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import bisect
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load all 1d bars
    cur.execute("SELECT symbol_id, ts, open, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    bars_by_sym = defaultdict(list)
    all_bar_ts = []
    for r in cur.fetchall():
        bars_by_sym[r['symbol_id']].append((r['ts'], r['open'], r['close'], r['volume']))
        all_bar_ts.append(r['ts'])

    # Load news timestamps
    cur.execute("SELECT symbol_id, ts FROM news")
    news_by_sym = defaultdict(list)
    for r in cur.fetchall():
        news_by_sym[r['symbol_id']].append(r['ts'])

    conn.close()

    if not all_bar_ts:
        print("INSUFFICIENT=1")
        return

    # Compute sealed era cutoff
    min_ts = min(all_bar_ts)
    max_ts = max(all_bar_ts)
    cutoff_ts = min_ts + 0.8 * (max_ts - min_ts)

    opportunities = 0
    issued = 0
    issued_correct = 0
    base_rate_count = 0  # count of negative open-to-close returns in issued calls
    distinct_days = set()
    sealed_issued = 0
    sealed_correct = 0
    # For effective N: store (day_ts, correct) per issued call
    issued_calls = []

    for sym, bars in bars_by_sym.items():
        n = len(bars)
        if n < 253:
            continue

        # Sort news timestamps for binary search
        news_ts_list = sorted(news_by_sym.get(sym, []))

        for i in range(252, n):
            day_ts, open_px, close_px, _ = bars[i]
            prev_close = bars[i-1][2]  # close of prior day

            # Universe filter: median dollar volume over prior 63 days
            if i - 63 < 0:
                continue
            window = bars[i-63:i]
            dvs = [row[3] * row[2] for row in window]  # volume * close
            dvs_sorted = sorted(dvs)
            mid = len(dvs_sorted) // 2
            if len(dvs_sorted) % 2 == 0:
                median_dvol = (dvs_sorted[mid-1] + dvs_sorted[mid]) / 2
            else:
                median_dvol = dvs_sorted[mid]
            if median_dvol <= 5_000_000:
                continue

            # Universe filter: at least 20 news headlines in prior 90 days
            cutoff_90 = day_ts - 90 * 86400
            start_idx = bisect.bisect_left(news_ts_list, cutoff_90)
            end_idx = bisect.bisect_left(news_ts_list, day_ts)
            if (end_idx - start_idx) < 20:
                continue

            # This day is an opportunity
            opportunities += 1

            # Entry condition: gap up between 7% and 15%
            gap = open_px / prev_close - 1
            if gap < 0.07 or gap >= 0.15:
                continue

            # Prior 5-day close-to-close return
            if i - 5 < 0:
                continue
            return_5d = bars[i-5][2] / prev_close - 1
            if return_5d >= 0:
                continue

            # No news in 24 hours before open
            cutoff_24 = day_ts - 24 * 3600
            recent_news_idx = bisect.bisect_right(news_ts_list, cutoff_24)
            if recent_news_idx < end_idx:
                continue  # there is news in the 24h window

            # Issue DOWN call
            issued += 1
            distinct_days.add(day_ts)
            correct = 1 if close_px < open_px else 0
            issued_correct += correct
            base_rate_count += (1 - correct)  # negative return counts as 1 for base rate
            issued_calls.append((day_ts, correct))

            if day_ts >= cutoff_ts:
                sealed_issued += 1
                sealed_correct += correct

    if issued == 0 or sealed_issued < 10:
        print("INSUFFICIENT=1")
        return

    precision = issued_correct / issued
    base_rate = base_rate_count / issued
    sealed_precision = sealed_correct / sealed_issued

    # Effective N: cluster by day
    # Group calls by day
    calls_by_day = defaultdict(list)
    for ts, correct in issued_calls:
        calls_by_day[ts].append(correct)
    days = list(calls_by_day.keys())
    D = len(days)
    N = issued
    if D == 0:
        effective_n = 0
    else:
        m = N / D  # average calls per day
        p = precision  # overall proportion correct
        # Compute variance between days
        sigma_b_sq = 0.0
        for day in days:
            n_d = len(calls_by_day[day])
            c_d = sum(calls_by_day[day])
            p_d = c_d / n_d
            sigma_b_sq += n_d * (p_d - p) ** 2
        if N > 1:
            sigma_b_sq /= (N - 1)
        sigma_w_sq = p * (1 - p)
        if sigma_b_sq + sigma_w_sq == 0:
            icc = 0
        else:
            icc = sigma_b_sq / (sigma_b_sq + sigma_w_sq)
        design_effect = 1 + (m - 1) * icc
        effective_n = N / design_effect

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={len(distinct_days)}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
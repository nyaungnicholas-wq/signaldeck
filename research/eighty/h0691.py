# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 690
# cycle_index: 17
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from bisect import bisect_left
from collections import defaultdict

def utc_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    cur.execute("""
        SELECT DISTINCT symbol_id FROM insider_trades
        WHERE code = 'P' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
    """)
    candidate_symbols = [row['symbol_id'] for row in cur.fetchall()]
    if not candidate_symbols:
        print("INSUFFICIENT=1")
        return 0

    news_by_symbol = defaultdict(set)
    cur.execute("SELECT symbol_id, ts FROM news")
    for row in cur.fetchall():
        news_by_symbol[row['symbol_id']].add(utc_date(row['ts']))

    insider_by_symbol = defaultdict(list)
    cur.execute("""
        SELECT symbol_id, filed_ts FROM insider_trades
        WHERE code = 'P' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
        ORDER BY symbol_id, filed_ts
    """)
    for row in cur.fetchall():
        insider_by_symbol[row['symbol_id']].append(row['filed_ts'])

    opportunities = []
    issued = []

    for sym in candidate_symbols:
        cur.execute("""
            SELECT ts, close, volume FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym,))
        bars = cur.fetchall()
        if len(bars) < 92:
            continue

        dates = [utc_date(r['ts']) for r in bars]
        closes = [r['close'] for r in bars]
        vols = [r['volume'] for r in bars]
        dollar_vols = [c * v for c, v in zip(closes, vols)]

        returns = [0.0] * len(bars)
        for i in range(1, len(bars)):
            if closes[i-1] != 0:
                returns[i] = (closes[i] - closes[i-1]) / closes[i-1]

        std_60 = [0.0] * len(bars)
        avg_vol_20 = [0.0] * len(bars)
        avg_dollar_60 = [0.0] * len(bars)

        for i in range(60, len(bars)):
            ret_window = returns[i-60:i]
            mean_ret = sum(ret_window) / 60
            var = sum((r - mean_ret) ** 2 for r in ret_window) / 60
            std_60[i] = var ** 0.5
            avg_vol_20[i] = sum(vols[i-20:i]) / 20
            avg_dollar_60[i] = sum(dollar_vols[i-60:i]) / 60

        news_dates = news_by_symbol.get(sym, set())
        filings = insider_by_symbol.get(sym, [])

        for i in range(60, len(bars) - 31):
            if returns[i] >= -2 * std_60[i]:
                continue
            if vols[i] <= 2 * avg_vol_20[i]:
                continue
            if avg_dollar_60[i] <= 1_000_000:
                continue
            if dates[i] in news_dates:
                continue

            event_ts = bars[i]['ts']
            opportunities.append((event_ts, sym, dates[i]))

            for filed_ts in filings:
                filed_date = utc_date(filed_ts)
                j = bisect_left(dates, filed_date)
                if j >= len(bars):
                    continue
                if j <= i or j - i > 10:
                    continue
                if j + 21 >= len(bars):
                    continue
                if avg_dollar_60[j] <= 1_000_000:
                    continue

                entry_px = closes[j]
                exit_px = closes[j + 21]
                fwd_ret = (exit_px - entry_px) / entry_px
                up = 1 if fwd_ret > 0 else 0
                issued.append((filed_ts, sym, dates[j], up))
                break

    if not issued:
        print("INSUFFICIENT=1")
        return 0

    issued.sort(key=lambda x: x[0])
    opportunities.sort(key=lambda x: x[0])

    n = len(issued)
    seal_start = int(n * 0.8)
    in_sample = issued[:seal_start]
    sealed = issued[seal_start:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0
        by_day = defaultdict(list)
        for _, sym, day, up in calls:
            by_day[(sym, day)].append(up)
        obs = [(sym, day, max(ups)) for (sym, day), ups in by_day.items()]
        issued_cnt = len(obs)
        hits = sum(up for _, _, up in obs)
        precision = hits / issued_cnt if issued_cnt else 0
        base_rate = precision
        distinct_days = len(set(day for _, day, _ in obs))

        day_groups = defaultdict(list)
        for _, day, up in obs:
            day_groups[day].append(up)
        G = len(day_groups)
        N = issued_cnt
        if G <= 1:
            deff = 1.01
        else:
            p = hits / N
            n_bar = N / G
            msb = sum(len(v) * (sum(v)/len(v) - p) ** 2 for v in day_groups.values()) / (G - 1)
            msw = sum(sum((u - sum(v)/len(v)) ** 2 for u in v) for v in day_groups.values()) / (N - G)
            if msb + (n_bar - 1) * msw == 0:
                icc = 0
            else:
                icc = (msb - msw) / (msb + (n_bar - 1) * msw)
            icc = max(0, icc)
            deff = 1 + (n_bar - 1) * icc
            if deff <= 1:
                deff = 1.01
        effective_n = N / deff
        return issued_cnt, precision, base_rate, distinct_days, effective_n

    iss_in, prec_in, base_in, days_in, eff_in = compute_metrics(in_sample)
    iss_seal, prec_seal, _, _, _ = compute_metrics(sealed)

    print(f"ISSUED={iss_in}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={prec_in:.6f}")
    print(f"BASE_RATE={base_in:.6f}")
    print(f"DISTINCT_DAYS={days_in}")
    print(f"EFFECTIVE_N={eff_in:.2f}")
    print(f"SEALED_PRECISION={prec_seal:.6f}")
    return 0

if __name__ == "__main__":
    sys.exit(main())
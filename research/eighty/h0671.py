# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 670
# cycle_index: 26
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get symbols with insider purchases (code='P') and sentiment_features
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN insider_trades it ON it.symbol_id = s.id AND it.code = 'P'
        JOIN sentiment_features sf ON sf.symbol_id = s.id
        WHERE s.active = 1
    """)
    symbols = cur.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    all_calls = []  # (decision_date, symbol_id, label, forward_return)

    for sym in symbols:
        sym_id = sym['id']
        # Load daily bars (tf='1d')
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym_id,))
        bars = cur.fetchall()
        if len(bars) < 250:
            continue
        # trading_days: list of (date, close, ts)
        trading_days = []
        for b in bars:
            dt = datetime.utcfromtimestamp(b['ts']).date()
            trading_days.append((dt, b['close'], b['ts']))
        if len(trading_days) < 252:
            continue

        # Load sentiment_features
        cur.execute("""
            SELECT day, mean_score FROM sentiment_features
            WHERE symbol_id = ?
            ORDER BY day
        """, (sym_id,))
        sent_rows = cur.fetchall()
        sent_map = {}
        for r in sent_rows:
            try:
                d = datetime.strptime(r['day'], '%Y-%m-%d').date()
                sent_map[d] = r['mean_score']
            except:
                pass
        if len(sent_map) < 252:
            continue

        # Align sentiment to trading days
        sentiment_series = []
        for dt, close, ts in trading_days:
            val = sent_map.get(dt)
            sentiment_series.append(val)

        # Compute 14-day rolling std of sentiment (trading days)
        n = len(sentiment_series)
        sent_14d_std = [None] * n
        for i in range(13, n):
            window = [sentiment_series[j] for j in range(i-13, i+1) if sentiment_series[j] is not None]
            if len(window) == 14:
                mean = sum(window) / 14
                var = sum((x - mean) ** 2 for x in window) / 14
                sent_14d_std[i] = math.sqrt(var)

        # Compute 252-day rolling 10th percentile of sent_14d_std
        sent_10pct = [None] * n
        for i in range(251, n):
            window = [sent_14d_std[j] for j in range(i-251, i+1) if sent_14d_std[j] is not None]
            if len(window) >= 200:  # require sufficient data
                window.sort()
                idx = int(0.10 * len(window))
                sent_10pct[i] = window[idx]

        # Drought flag and streak
        drought_flag = [False] * n
        drought_streak = [0] * n
        for i in range(n):
            if sent_14d_std[i] is not None and sent_10pct[i] is not None:
                if sent_14d_std[i] <= sent_10pct[i]:
                    drought_flag[i] = True
            if drought_flag[i]:
                drought_streak[i] = (drought_streak[i-1] if i > 0 else 0) + 1
            else:
                drought_streak[i] = 0
        drought_condition = [ds >= 10 for ds in drought_streak]

        # Compute 200-day MA (using close up to that day)
        ma200 = [None] * n
        for i in range(199, n):
            window = [trading_days[j][1] for j in range(i-199, i+1)]
            ma200[i] = sum(window) / 200

        # Map date to index
        date_to_idx = {trading_days[i][0]: i for i in range(n)}

        # Load insider purchases
        cur.execute("""
            SELECT filed_ts FROM insider_trades
            WHERE symbol_id = ? AND code = 'P'
            ORDER BY filed_ts
        """, (sym_id,))
        insider_rows = cur.fetchall()
        for ir in insider_rows:
            filed_ts = ir['filed_ts']
            filing_dt = datetime.utcfromtimestamp(filed_ts)
            filing_date = filing_dt.date()

            # Find day0: first trading day >= filing_date
            day0_idx = None
            for i in range(n):
                if trading_days[i][0] >= filing_date:
                    day0_idx = i
                    break
            if day0_idx is None:
                continue
            # day_minus1 is previous trading day
            if day0_idx == 0:
                continue
            day_minus1_idx = day0_idx - 1

            # Check drought condition on day_minus1
            if not drought_condition[day_minus1_idx]:
                continue

            # Check price condition: close[day_minus1] within 5% of MA200[day_minus1]
            close_dm1 = trading_days[day_minus1_idx][1]
            ma_dm1 = ma200[day_minus1_idx]
            if ma_dm1 is None:
                continue
            if abs(close_dm1 - ma_dm1) / ma_dm1 > 0.05:
                continue

            # Need entry day (day0) and exit day (day0 + 21)
            exit_idx = day0_idx + 21
            if exit_idx >= n:
                continue
            entry_price = trading_days[day0_idx][1]
            exit_price = trading_days[exit_idx][1]
            fwd_return = (exit_price - entry_price) / entry_price
            label = 1 if fwd_return > 0 else 0

            decision_date = trading_days[day0_idx][0]
            all_calls.append((decision_date, sym_id, label, fwd_return))

    if not all_calls:
        print("INSUFFICIENT=1")
        return 0

    # Deduplicate: one call per symbol per decision_date
    calls_dict = {}
    for dec_date, sym_id, label, fwd in all_calls:
        key = (sym_id, dec_date)
        if key not in calls_dict:
            calls_dict[key] = (dec_date, sym_id, label, fwd)
    calls = list(calls_dict.values())
    calls.sort(key=lambda x: x[0])  # sort by decision_date

    # Split: most recent 20% as sealed
    total = len(calls)
    split_idx = int(total * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(c[2] for c in call_list)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # base rate of predicted class within issued subset (all predicted up)
        distinct_days = len(set(c[0] for c in call_list))
        # Design effect: lag-1 autocorrelation of daily call count
        day_counts = defaultdict(int)
        for c in call_list:
            day_counts[c[0]] += 1
        dates_sorted = sorted(day_counts.keys())
        counts = [day_counts[d] for d in dates_sorted]
        if len(counts) > 1:
            mean_c = sum(counts) / len(counts)
            cov = sum((counts[i] - mean_c) * (counts[i-1] - mean_c) for i in range(1, len(counts)))
            var = sum((c - mean_c) ** 2 for c in counts)
            rho1 = cov / var if var > 0 else 0
            if rho1 > 0:
                deff = (1 + rho1) / (1 - rho1)
            else:
                deff = 1.0001
        else:
            deff = 1.0001
        effective_n = issued / deff
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_train, hits_train, prec_train, base_train, distinct_train, eff_train = compute_metrics(train_calls)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, distinct_sealed, eff_sealed = compute_metrics(sealed_calls)

    # Overall issued includes both train and sealed
    total_issued = issued_train + issued_sealed
    total_hits = hits_train + hits_sealed
    total_precision = total_hits / total_issued if total_issued > 0 else 0.0
    total_base = total_precision
    total_distinct = len(set(c[0] for c in calls))
    # Effective N for total
    day_counts_all = defaultdict(int)
    for c in calls:
        day_counts_all[c[0]] += 1
    dates_all = sorted(day_counts_all.keys())
    counts_all = [day_counts_all[d] for d in dates_all]
    if len(counts_all) > 1:
        mean_c = sum(counts_all) / len(counts_all)
        cov = sum((counts_all[i] - mean_c) * (counts_all[i-1] - mean_c) for i in range(1, len(counts_all)))
        var = sum((c - mean_c) ** 2 for c in counts_all)
        rho1 = cov / var if var > 0 else 0
        if rho1 > 0:
            deff_all = (1 + rho1) / (1 - rho1)
        else:
            deff_all = 1.0001
    else:
        deff_all = 1.0001
    effective_n_all = total_issued / deff_all

    # Opportunities: number of insider purchase filings considered (before filters)
    # We need to count all insider purchase filings for symbols in universe
    cur.execute("""
        SELECT COUNT(*) FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P' AND s.active = 1
    """)
    opportunities = cur.fetchone()[0]

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base:.6f}")
    print(f"DISTINCT_DAYS={total_distinct}")
    print(f"EFFECTIVE_N={effective_n_all:.2f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
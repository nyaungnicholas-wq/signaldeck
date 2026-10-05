# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 821
# cycle_index: 17
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, date
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. StockTwits date range and per-symbol bearish 90th percentile
    cur.execute("""
        SELECT symbol_id, ts, bearish
        FROM stocktwits_sentiment
        ORDER BY symbol_id, ts
    """)
    st_rows = cur.fetchall()
    if not st_rows:
        print("INSUFFICIENT=1")
        return

    # Group by symbol_id, compute 90th percentile of bearish
    by_sym = defaultdict(list)
    for r in st_rows:
        by_sym[r['symbol_id']].append(r['bearish'])

    p90 = {}
    for sym, vals in by_sym.items():
        if len(vals) < 10:
            continue
        sorted_vals = sorted(vals)
        idx = int(0.9 * (len(sorted_vals) - 1))
        p90[sym] = sorted_vals[idx]

    if not p90:
        print("INSUFFICIENT=1")
        return

    # 2. CEO/CFO open-market purchases (code='P') with trade date in StockTwits range
    st_min_ts = min(r['ts'] for r in st_rows)
    st_max_ts = max(r['ts'] for r in st_rows)

    cur.execute("""
        SELECT accession, symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' 
               OR title LIKE '%CHIEF EXECUTIVE%' OR title LIKE '%CHIEF FINANCIAL%')
          AND tx_ts >= ? AND tx_ts <= ?
    """, (st_min_ts, st_max_ts))
    insider_rows = cur.fetchall()
    if not insider_rows:
        print("INSUFFICIENT=1")
        return

    # 3. For each insider trade, check StockTwits bearish on tx_ts date >= p90
    #    and news sentiment neutral/improving on tx_ts date
    #    Need to convert tx_ts (epoch) to date string for stocktwits_sentiment.ts (epoch) and sentiment_features.day (YYYY-MM-DD)
    qualifying = []
    for r in insider_rows:
        sym = r['symbol_id']
        if sym not in p90:
            continue
        tx_date = datetime.utcfromtimestamp(r['tx_ts']).date()
        # Find StockTwits row for this symbol on tx_date
        cur.execute("""
            SELECT bearish FROM stocktwits_sentiment
            WHERE symbol_id = ? AND date(ts, 'unixepoch') = ?
        """, (sym, tx_date.isoformat()))
        st_row = cur.fetchone()
        if not st_row or st_row['bearish'] < p90[sym]:
            continue

        # News sentiment: use sentiment_features for tx_date (day string)
        # Neutral/improving: mean_score >= 0 (neutral) OR mean_score > prior_5d_avg (improving)
        cur.execute("""
            SELECT mean_score FROM sentiment_features
            WHERE symbol_id = ? AND day = ?
        """, (sym, tx_date.isoformat()))
        sf_row = cur.fetchone()
        if not sf_row:
            continue
        mean_score = sf_row['mean_score']
        # Check improving: mean_score > average of prior 5 days
        cur.execute("""
            SELECT AVG(mean_score) FROM sentiment_features
            WHERE symbol_id = ? AND day < ? AND day >= date(?, '-5 days')
        """, (sym, tx_date.isoformat(), tx_date.isoformat()))
        prior_avg_row = cur.fetchone()
        prior_avg = prior_avg_row[0] if prior_avg_row and prior_avg_row[0] is not None else -999
        if not (mean_score >= 0 or mean_score > prior_avg):
            continue

        qualifying.append((sym, r['filed_ts'], r['tx_ts']))

    if not qualifying:
        print("INSUFFICIENT=1")
        return

    # 4. Get prediction_outcomes for 1w horizon at filed_ts (decision timestamp)
    #    prediction_outcomes.ts is epoch. We need exact match on symbol_id, horizon='1w', ts=filed_ts
    calls = []  # (symbol_id, filed_ts, up)
    for sym, filed_ts, tx_ts in qualifying:
        cur.execute("""
            SELECT up FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = '1w' AND ts = ?
        """, (sym, filed_ts))
        po = cur.fetchone()
        if po:
            calls.append((sym, filed_ts, po['up']))

    if len(calls) < 20:
        print("INSUFFICIENT=1")
        return

    # 5. Hold out most recent 20% by filed_ts date
    calls.sort(key=lambda x: x[1])
    n = len(calls)
    split_idx = int(n * 0.8)
    train_calls = calls[:split_idx]
    test_calls = calls[split_idx:]

    def compute_metrics(call_list, label):
        if not call_list:
            return
        issued = len(call_list)
        hits = sum(1 for _, _, up in call_list if up == 1)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate within issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(ft).date() for _, ft, _ in call_list))
        # Design effect: 1 + (avg_cluster_size - 1) * intraclass_corr
        # Approximate: group by day, compute variance of daily counts
        day_counts = defaultdict(int)
        for _, ft, _ in call_list:
            day_counts[datetime.utcfromtimestamp(ft).date()] += 1
        counts = list(day_counts.values())
        if len(counts) > 1:
            mean_c = sum(counts) / len(counts)
            var_c = sum((c - mean_c) ** 2 for c in counts) / len(counts)
            deff = 1 + (var_c / mean_c) if mean_c > 0 else 1
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        print(f"{label}_ISSUED={issued}")
        print(f"{label}_PRECISION={precision:.6f}")
        print(f"{label}_BASE_RATE={base_rate:.6f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.6f}")

    # Overall opportunities: distinct (symbol, filed_ts_date) from qualifying that had prediction_outcomes
    # But opportunities considered = all qualifying trades with prediction_outcomes available
    opportunities = len(calls)

    print(f"ISSUED={n}")
    print(f"OPPORTUNITIES={opportunities}")
    compute_metrics(train_calls, "TRAIN")
    compute_metrics(test_calls, "SEALED")

    # SEALED_PRECISION is the test precision
    if test_calls:
        sealed_hits = sum(1 for _, _, up in test_calls if up == 1)
        sealed_precision = sealed_hits / len(test_calls)
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
    else:
        print("SEALED_PRECISION=0.000000")

if __name__ == '__main__':
    main()
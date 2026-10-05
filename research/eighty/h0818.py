# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 817
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import statistics

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    con = connect_ro()
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # 1. Get officer open-market purchases (code='P')
    cur.execute("""
        SELECT accession, symbol_id, tx_ts, filed_ts, shares, price, value, title
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
          AND tx_ts IS NOT NULL AND filed_ts IS NOT NULL
        ORDER BY tx_ts
    """)
    purchases = cur.fetchall()
    if not purchases:
        print("INSUFFICIENT=1")
        return

    # Filter fast disclosure (<= 2 days = 172800 seconds)
    fast_purchases = [p for p in purchases if p['filed_ts'] - p['tx_ts'] <= 172800]
    if not fast_purchases:
        print("INSUFFICIENT=1")
        return

    # Group by symbol
    by_symbol = defaultdict(list)
    for p in fast_purchases:
        by_symbol[p['symbol_id']].append(p)

    # 2. For each symbol, load sentiment_features and bars (tf='1d')
    calls = []  # (trade_date, symbol_id, predicted_up, actual_fwd_return)
    opportunities = 0

    for sym_id, sym_purchases in by_symbol.items():
        # Load sentiment_features for this symbol
        cur.execute("""
            SELECT day, mean_score FROM sentiment_features
            WHERE symbol_id = ? ORDER BY day
        """, (sym_id,))
        sent_rows = cur.fetchall()
        if len(sent_rows) < 63:
            continue
        sent_dates = [str_to_date(r['day']) for r in sent_rows]
        sent_scores = [r['mean_score'] for r in sent_rows]

        # Load daily bars for this symbol
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d' ORDER BY ts
        """, (sym_id,))
        bar_rows = cur.fetchall()
        if len(bar_rows) < 84:  # need 63 for lookback + 21 for forward
            continue
        bar_dates = [epoch_to_date(r['ts']) for r in bar_rows]
        bar_closes = [r['close'] for r in bar_rows]

        # Build date -> index maps
        sent_date_to_idx = {d: i for i, d in enumerate(sent_dates)}
        bar_date_to_idx = {d: i for i, d in enumerate(bar_dates)}

        # Precompute 63-day rolling mean sentiment and 63-day price return for each date
        # We'll compute on the fly for each purchase trade date

        for p in sym_purchases:
            opportunities += 1
            trade_ts = p['tx_ts']
            trade_date = epoch_to_date(trade_ts)
            trade_date_str = date_to_str(trade_date)

            # Need sentiment up to day BEFORE trade date (to avoid lookahead)
            prev_date = trade_date - timedelta(days=1)
            prev_date_str = date_to_str(prev_date)

            # Find sentiment index for prev_date
            if prev_date_str not in sent_date_to_idx:
                continue
            sent_idx = sent_date_to_idx[prev_date_str]
            if sent_idx < 62:  # need 63 days ending at sent_idx
                continue

            # Compute 63-day mean sentiment ending at sent_idx
            window_scores = sent_scores[sent_idx-62:sent_idx+1]
            mean_sent_63 = statistics.mean(window_scores)

            # Compute historical distribution of 63-day mean sentiment up to this point
            hist_means = []
            for i in range(62, sent_idx + 1):
                hist_means.append(statistics.mean(sent_scores[i-62:i+1]))
            if len(hist_means) < 20:  # need some history for quartile
                continue
            bottom_quartile = statistics.quantiles(hist_means, n=4)[0]  # 25th percentile

            # Check if current mean sentiment is in bottom quartile
            if mean_sent_63 > bottom_quartile:
                continue

            # Check price resilience: 63-day return up to day before trade date
            if prev_date not in bar_date_to_idx:
                continue
            bar_idx = bar_date_to_idx[prev_date]
            if bar_idx < 62:
                continue
            close_now = bar_closes[bar_idx]
            close_63_ago = bar_closes[bar_idx - 62]
            ret_63 = (close_now / close_63_ago) - 1.0
            if ret_63 <= 0:
                continue

            # All conditions met - issue call
            # Label: 21-day forward return from trade date
            # Find trade date in bars (or next available)
            if trade_date not in bar_date_to_idx:
                # find next trading day
                future_dates = [d for d in bar_dates if d >= trade_date]
                if not future_dates:
                    continue
                entry_date = future_dates[0]
                entry_idx = bar_date_to_idx[entry_date]
            else:
                entry_idx = bar_date_to_idx[trade_date]

            # Need 21 trading days forward
            if entry_idx + 21 >= len(bar_closes):
                continue
            entry_close = bar_closes[entry_idx]
            exit_close = bar_closes[entry_idx + 21]
            fwd_return = (exit_close / entry_close) - 1.0
            actual_up = 1 if fwd_return > 0 else 0

            calls.append((trade_ts, sym_id, 1, actual_up, fwd_return))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Sort by trade timestamp
    calls.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    n_total = len(calls)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed

    train_calls = calls[:n_train]
    sealed_calls = calls[n_train:]

    def compute_metrics(call_list, label):
        if not call_list:
            return
        issued = len(call_list)
        hits = sum(1 for c in call_list if c[3] == 1)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate of predicted class (up) within issued
        distinct_days = len(set(epoch_to_date(c[0]) for c in call_list))
        # Design effect: 1 + (avg_cluster_size - 1) * intracluster_corr
        # Approximate: group by day, compute variance of daily counts
        day_counts = defaultdict(int)
        for c in call_list:
            day_counts[epoch_to_date(c[0])] += 1
        counts = list(day_counts.values())
        if len(counts) > 1:
            mean_c = statistics.mean(counts)
            var_c = statistics.variance(counts)
            deff = 1 + (var_c / mean_c) if mean_c > 0 else 1.0
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        print(f"{label}_ISSUED={issued}")
        print(f"{label}_PRECISION={precision:.6f}")
        print(f"{label}_BASE_RATE={base_rate:.6f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.2f}")

    compute_metrics(train_calls, "TRAIN")
    compute_metrics(sealed_calls, "SEALED")

    # Overall required output
    all_issued = len(calls)
    all_hits = sum(1 for c in calls if c[3] == 1)
    all_precision = all_hits / all_issued if all_issued else 0.0
    all_base_rate = all_hits / all_issued if all_issued else 0.0
    all_distinct_days = len(set(epoch_to_date(c[0]) for c in calls))
    day_counts_all = defaultdict(int)
    for c in calls:
        day_counts_all[epoch_to_date(c[0])] += 1
    counts_all = list(day_counts_all.values())
    if len(counts_all) > 1:
        mean_c = statistics.mean(counts_all)
        var_c = statistics.variance(counts_all)
        deff = 1 + (var_c / mean_c) if mean_c > 0 else 1.0
    else:
        deff = 1.0
    all_effective_n = all_issued / deff if deff > 0 else all_issued
    sealed_precision = sum(1 for c in sealed_calls if c[3] == 1) / len(sealed_calls) if sealed_calls else 0.0

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={all_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
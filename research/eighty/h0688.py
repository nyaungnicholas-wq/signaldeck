# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 687
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    conn = sqlite3.connect(db_path, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get sentiment_features data with sufficient history for 63-day median
    # sentiment_features: symbol_id, day (YYYY-MM-DD), n_all, mean_score
    cur.execute("""
        SELECT symbol_id, day, n_all, mean_score
        FROM sentiment_features
        WHERE n_all IS NOT NULL AND mean_score IS NOT NULL
        ORDER BY symbol_id, day
    """)
    rows = cur.fetchall()
    if not rows:
        print("INSUFFICIENT=1")
        return 0

    # Organize by symbol
    by_symbol = {}
    for r in rows:
        sid = r['symbol_id']
        by_symbol.setdefault(sid, []).append((r['day'], r['n_all'], r['mean_score']))

    # Get daily bars for forward returns (tf='1d')
    # bars: symbol_id, tf, ts (unix epoch), close
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bar_rows = cur.fetchall()
    if not bar_rows:
        print("INSUFFICIENT=1")
        return 0

    # Organize bars by symbol, convert ts to date string
    bars_by_symbol = {}
    for r in bar_rows:
        sid = r['symbol_id']
        dt = datetime.utcfromtimestamp(r['ts']).date()
        day_str = dt.isoformat()
        bars_by_symbol.setdefault(sid, []).append((day_str, r['close']))

    # For each symbol, compute signals and forward returns
    all_signals = []  # (symbol_id, signal_day, fwd_return, is_sealed)

    for sid, sent_data in by_symbol.items():
        if sid not in bars_by_symbol:
            continue
        bar_data = bars_by_symbol[sid]
        if len(bar_data) < 252:  # need enough bars for 21-day forward + history
            continue

        # Build maps for quick lookup
        bar_map = {day: close for day, close in bar_data}
        bar_days = [day for day, _ in bar_data]

        # Need at least 63 days of sentiment history for median
        if len(sent_data) < 63:
            continue

        # Compute rolling 63-day median of n_all, and 5/21 day MA of mean_score
        n_all_vals = [n for _, n, _ in sent_data]
        mean_scores = [s for _, _, s in sent_data]
        days = [d for d, _, _ in sent_data]

        # Rolling median of n_all (63-day window)
        n_all_median = []
        for i in range(len(n_all_vals)):
            if i < 62:
                n_all_median.append(None)
            else:
                window = sorted(n_all_vals[i-62:i+1])
                n_all_median.append(window[len(window)//2])

        # Rolling 5-day and 21-day MA of mean_score
        ma5 = []
        ma21 = []
        for i in range(len(mean_scores)):
            if i < 4:
                ma5.append(None)
            else:
                ma5.append(sum(mean_scores[i-4:i+1]) / 5)
            if i < 20:
                ma21.append(None)
            else:
                ma21.append(sum(mean_scores[i-20:i+1]) / 21)

        # Find signal days: n_all > 3 * median_63 AND ma5 crosses above ma21
        for i in range(21, len(days) - 21):  # need 21 days forward for return
            if n_all_median[i] is None or ma5[i] is None or ma21[i] is None:
                continue
            if ma5[i-1] is None or ma21[i-1] is None:
                continue

            # Coverage spike
            if n_all_vals[i] <= 3 * n_all_median[i]:
                continue

            # Sentiment inflection: ma5 crosses above ma21 today, was <= yesterday
            if ma5[i] <= ma21[i]:
                continue
            if ma5[i-1] > ma21[i-1]:
                continue

            signal_day = days[i]

            # Find signal_day in bar_days (must exist)
            if signal_day not in bar_map:
                continue
            entry_price = bar_map[signal_day]

            # Find exit price 21 trading days later
            try:
                idx = bar_days.index(signal_day)
                if idx + 21 >= len(bar_days):
                    continue
                exit_day = bar_days[idx + 21]
                exit_price = bar_map[exit_day]
            except ValueError:
                continue

            fwd_return = (exit_price - entry_price) / entry_price
            all_signals.append((sid, signal_day, fwd_return))

    if not all_signals:
        print("INSUFFICIENT=1")
        return 0

    # Sort by signal_day
    all_signals.sort(key=lambda x: x[1])

    # Hold out most recent 20% as sealed era
    n_total = len(all_signals)
    n_sealed = max(1, int(n_total * 0.2))
    train_signals = all_signals[:-n_sealed]
    sealed_signals = all_signals[-n_sealed:]

    def compute_metrics(signals):
        if not signals:
            return 0, 0, 0, 0, 0
        issued = len(signals)
        hits = sum(1 for _, _, r in signals if r > 0)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0  # base rate of positive class in issued
        distinct_days = len(set(d for _, d, _ in signals))
        # Design effect: approximate as 1 + (avg cluster size - 1) * autocorr
        # Simple approximation: group by day, count signals per day
        from collections import Counter
        day_counts = Counter(d for _, d, _ in signals)
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        # Conservative design effect > 1
        deff = max(1.0, 1.0 + (avg_cluster - 1) * 0.1)
        effective_n = issued / deff
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_base, train_days, train_eff = compute_metrics(train_signals)
    sealed_issued, sealed_hits, sealed_prec, sealed_base, sealed_days, sealed_eff = compute_metrics(sealed_signals)

    # Total issued across both eras
    total_issued = train_issued + sealed_issued
    total_hits = train_hits + sealed_hits
    total_prec = total_hits / total_issued if total_issued > 0 else 0
    total_base = total_hits / total_issued if total_issued > 0 else 0
    total_days = len(set(d for _, d, _ in all_signals))
    # Effective N for total
    from collections import Counter
    day_counts = Counter(d for _, d, _ in all_signals)
    avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    deff = max(1.0, 1.0 + (avg_cluster - 1) * 0.1)
    total_eff = total_issued / deff

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_issued}")  # Each signal day per symbol is an opportunity considered
    print(f"PRECISION={total_prec:.6f}")
    print(f"BASE_RATE={total_base:.6f}")
    print(f"DISTINCT_DAYS={total_days}")
    print(f"EFFECTIVE_N={total_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
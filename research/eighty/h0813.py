# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 812
# cycle_index: 8
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def trading_days_between(start, end, trading_days_set):
    count = 0
    current = start
    while current <= end:
        if current in trading_days_set:
            count += 1
        current += timedelta(days=1)
    return count

def next_trading_day(d, trading_days_set, max_lookforward=10):
    for i in range(1, max_lookforward + 1):
        nd = d + timedelta(days=i)
        if nd in trading_days_set:
            return nd
    return None

def nth_trading_day_after(start, n, trading_days_list, trading_days_set):
    try:
        idx = trading_days_list.index(start)
        if idx + n < len(trading_days_list):
            return trading_days_list[idx + n]
    except ValueError:
        pass
    return None

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load all trading days from bars (tf='1d')
    cur.execute("SELECT DISTINCT date(ts, 'unixepoch') as d FROM bars WHERE tf='1d' ORDER BY d")
    trading_days = [str_to_date(row['d']) for row in cur.fetchall()]
    trading_days_set = set(trading_days)
    if not trading_days:
        print("INSUFFICIENT=1")
        return 0

    # Split: hold out most recent 20% as sealed era
    split_idx = int(len(trading_days) * 0.8)
    train_days = trading_days[:split_idx]
    test_days = trading_days[split_idx:]
    train_set = set(train_days)
    test_set = set(test_days)

    # Load sentiment_features, compute hedged_ratio and rolling p90
    cur.execute("""
        SELECT symbol_id, day, hedged, n_polar
        FROM sentiment_features
        WHERE n_polar > 0
        ORDER BY symbol_id, day
    """)
    sf_rows = cur.fetchall()

    # Group by symbol_id
    from collections import defaultdict
    sf_by_symbol = defaultdict(list)
    for row in sf_rows:
        d = str_to_date(row['day'])
        ratio = row['hedged'] / row['n_polar']
        sf_by_symbol[row['symbol_id']].append((d, ratio))

    # Compute rolling 252-day 90th percentile for each symbol
    high_hedge_days = set()  # (symbol_id, day)
    for sym_id, series in sf_by_symbol.items():
        if len(series) < 50:
            continue
        series.sort()
        dates = [d for d, _ in series]
        ratios = [r for _, r in series]
        for i in range(50, len(series)):
            window = ratios[max(0, i-252):i]
            if len(window) < 20:
                continue
            window_sorted = sorted(window)
            p90_idx = int(0.9 * (len(window_sorted) - 1))
            p90 = window_sorted[p90_idx]
            if ratios[i] > p90:
                high_hedge_days.add((sym_id, dates[i]))

    # Load insider trades: code='P', title contains CEO or CFO
    cur.execute("""
        SELECT symbol_id, filed_ts, title, code
        FROM insider_trades
        WHERE code = 'P'
          AND (lower(title) LIKE '%ceo%' OR lower(title) LIKE '%cfo%')
        ORDER BY symbol_id, filed_ts
    """)
    insider_rows = cur.fetchall()

    # For each insider trade, check if filed within 5 trading days after a high-hedge day
    signals = []  # (symbol_id, decision_day, horizon_end_day)
    for row in insider_rows:
        sym_id = row['symbol_id']
        filed_date = epoch_to_date(row['filed_ts'])
        # Find high-hedge days for this symbol within 5 trading days before filed_date
        # We need the high-hedge day to be known at decision time (filed_date)
        # sentiment_features day is a date string; assume it's available next trading day
        # So high-hedge day must be <= filed_date - 1 trading day
        # And within 5 trading days prior
        for lookback in range(1, 6):
            hd = nth_trading_day_after(filed_date, -lookback, trading_days, trading_days_set)
            if hd and (sym_id, hd) in high_hedge_days:
                # Check as-of: hedged_ratio for day hd must be computable before filed_date
                # sentiment_features for day hd is available at hd+1 trading day earliest
                # So we need hd+1 <= filed_date
                hd_next = next_trading_day(hd, trading_days_set)
                if hd_next and hd_next <= filed_date:
                    # Valid signal
                    horizon_end = nth_trading_day_after(filed_date, 21, trading_days, trading_days_set)
                    if horizon_end:
                        signals.append((sym_id, filed_date, horizon_end))
                    break  # only first matching high-hedge day

    if not signals:
        print("INSUFFICIENT=1")
        return 0

    # Assign each signal to train or test based on decision_day
    train_signals = [s for s in signals if s[1] in train_set]
    test_signals = [s for s in signals if s[1] in test_set]

    # Load daily bars for forward returns
    # We need close price at decision_day and at horizon_end_day
    # Build a price lookup: (symbol_id, date) -> close
    cur.execute("""
        SELECT symbol_id, date(ts, 'unixepoch') as d, close
        FROM bars
        WHERE tf='1d'
    """)
    price_lookup = {}
    for row in cur.fetchall():
        price_lookup[(row['symbol_id'], str_to_date(row['d']))] = row['close']

    def evaluate(signal_list, era_name):
        issued = 0
        hits = 0
        decision_days = set()
        for sym_id, decision_day, horizon_end in signal_list:
            p0 = price_lookup.get((sym_id, decision_day))
            p1 = price_lookup.get((sym_id, horizon_end))
            if p0 is None or p1 is None or p0 <= 0:
                continue
            issued += 1
            decision_days.add(decision_day)
            fwd_ret = (p1 - p0) / p0
            if fwd_ret > 0:
                hits += 1
        precision = hits / issued if issued else 0.0
        base_rate = precision  # base rate of positive class within issued subset
        distinct_days = len(decision_days)
        # Design effect: cluster by month
        from collections import Counter
        month_counts = Counter(d.strftime('%Y-%m') for d in decision_days)
        if month_counts:
            avg_cluster = sum(month_counts.values()) / len(month_counts)
            design_effect = 1 + (avg_cluster - 1) * 0.5  # conservative ICC=0.5
            effective_n = issued / design_effect
        else:
            effective_n = 0
        print(f"{era_name}_ISSUED={issued}")
        print(f"{era_name}_PRECISION={precision:.6f}")
        print(f"{era_name}_BASE_RATE={base_rate:.6f}")
        print(f"{era_name}_DISTINCT_DAYS={distinct_days}")
        print(f"{era_name}_EFFECTIVE_N={effective_n:.2f}")
        return issued, hits, distinct_days, effective_n

    # Evaluate train and test (sealed)
    train_issued, train_hits, train_days, train_eff = evaluate(train_signals, "TRAIN")
    test_issued, test_hits, test_days, test_eff = evaluate(test_signals, "SEALED")

    # Overall
    all_issued = train_issued + test_issued
    all_hits = train_hits + test_hits
    all_precision = all_hits / all_issued if all_issued else 0.0
    all_base_rate = all_precision
    all_distinct_days = train_days + test_days  # they're disjoint by construction
    all_effective_n = train_eff + test_eff

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={len(signals)}")
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={all_effective_n:.2f}")
    print(f"SEALED_PRECISION={test_hits / test_issued if test_issued else 0.0:.6f}")

    # Check invariants
    if all_distinct_days > all_issued:
        print("ERROR: DISTINCT_DAYS > ISSUED", file=sys.stderr)
        sys.exit(1)
    if all_effective_n >= all_issued:
        print("ERROR: EFFECTIVE_N >= ISSUED", file=sys.stderr)
        sys.exit(1)

    return 0

if __name__ == '__main__':
    sys.exit(main())
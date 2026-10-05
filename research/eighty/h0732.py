# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 731
# cycle_index: 1
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, date
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'
EMBARGO_START = date(2025, 1, 1)
UNIVERSE_START = date(2018, 7, 1)
UNIVERSE_END = date(2024, 12, 31)
HORIZON = 21
LOOKBACK_HIGH = 252
LOOKBACK_RETURN = 20
LOOKBACK_SENTIMENT_CONSEC = 20
SENTIMENT_MA_WINDOW = 5
SENTIMENT_THRESHOLD = 0.3
RETURN_THRESHOLD = 0.05
HIGH_PROXIMITY = 0.9
INSIDER_LOOKBACK = 63
MIN_INSIDER_COUNT = 3
SEALED_FRACTION = 0.20

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get symbols with daily bars, sentiment_features, and insider purchases in universe period
    cur.execute("""
        SELECT DISTINCT b.symbol_id
        FROM bars b
        JOIN sentiment_features sf ON sf.symbol_id = b.symbol_id
        JOIN insider_trades it ON it.symbol_id = b.symbol_id
        WHERE b.tf = '1d'
          AND date(datetime(b.ts, 'unixepoch')) BETWEEN ? AND ?
          AND it.code = 'P'
          AND date(datetime(it.filed_ts, 'unixepoch')) BETWEEN ? AND ?
    """, (UNIVERSE_START.isoformat(), UNIVERSE_END.isoformat(),
          UNIVERSE_START.isoformat(), UNIVERSE_END.isoformat()))
    symbol_ids = [row['symbol_id'] for row in cur.fetchall()]

    if not symbol_ids:
        print("INSUFFICIENT=1")
        return 0

    all_calls = []  # (decision_date, symbol_id, hit)

    for sym_id in symbol_ids:
        # Load daily bars
        cur.execute("""
            SELECT ts, close, high FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym_id,))
        bars = [(epoch_to_date(row['ts']), row['close'], row['high']) for row in cur.fetchall()]
        if len(bars) < LOOKBACK_HIGH + HORIZON + 1:
            continue

        # Load sentiment features
        cur.execute("""
            SELECT day, mean_score FROM sentiment_features
            WHERE symbol_id = ?
            ORDER BY day
        """, (sym_id,))
        sentiment = {str_to_date(row['day']): row['mean_score'] for row in cur.fetchall()}

        # Load insider purchases (code='P')
        cur.execute("""
            SELECT filed_ts FROM insider_trades
            WHERE symbol_id = ? AND code = 'P'
            ORDER BY filed_ts
        """, (sym_id,))
        insider_dates = set(epoch_to_date(row['filed_ts']) for row in cur.fetchall())

        # Map date to bar index
        date_to_idx = {d: i for i, (d, _, _) in enumerate(bars)}

        # Precompute 5-day MA of sentiment for each day where we have enough data
        # We need sentiment for day d-4 to d
        sentiment_ma5 = {}
        sorted_sent_dates = sorted(sentiment.keys())
        for i, d in enumerate(sorted_sent_dates):
            if i >= SENTIMENT_MA_WINDOW - 1:
                window_dates = sorted_sent_dates[i - SENTIMENT_MA_WINDOW + 1:i + 1]
                if len(window_dates) == SENTIMENT_MA_WINDOW:
                    ma = sum(sentiment[wd] for wd in window_dates) / SENTIMENT_MA_WINDOW
                    sentiment_ma5[d] = ma

        # Iterate decision points: bar indices where insider purchase disclosed
        for i in range(LOOKBACK_HIGH, len(bars) - HORIZON):
            decision_date = bars[i][0]
            if decision_date < UNIVERSE_START or decision_date > UNIVERSE_END:
                continue
            if decision_date >= EMBARGO_START:
                continue
            if decision_date not in insider_dates:
                continue

            # Check sentiment condition: 5-day MA > 0.3 for at least 20 consecutive sessions up to decision_date
            # Need sentiment_ma5 for decision_date - 19 through decision_date
            ok = True
            for offset in range(LOOKBACK_SENTIMENT_CONSEC):
                check_date = date.fromordinal(decision_date.toordinal() - (LOOKBACK_SENTIMENT_CONSEC - 1) + offset)
                if check_date not in sentiment_ma5 or sentiment_ma5[check_date] <= SENTIMENT_THRESHOLD:
                    ok = False
                    break
            if not ok:
                continue

            # Check 20-day absolute return < 5%
            close_now = bars[i][1]
            close_20d_ago = bars[i - LOOKBACK_RETURN][1]
            ret_20d = abs(close_now / close_20d_ago - 1)
            if ret_20d >= RETURN_THRESHOLD:
                continue

            # Check within 10% of 252-session high
            high_252 = max(bars[j][2] for j in range(i - LOOKBACK_HIGH + 1, i + 1))
            if close_now < HIGH_PROXIMITY * high_252:
                continue

            # Check at least 3 insider purchases in prior 63 sessions
            insider_count = 0
            for j in range(max(0, i - INSIDER_LOOKBACK), i):
                if bars[j][0] in insider_dates:
                    insider_count += 1
            if insider_count < MIN_INSIDER_COUNT:
                continue

            # All conditions met - issue call
            # Label: 21-day forward return > 0
            fwd_return = bars[i + HORIZON][1] / close_now - 1
            hit = 1 if fwd_return > 0 else 0
            all_calls.append((decision_date, sym_id, hit))

    if not all_calls:
        print("INSUFFICIENT=1")
        return 0

    # Sort by decision date
    all_calls.sort(key=lambda x: x[0])

    # Split: most recent 20% sealed
    n_total = len(all_calls)
    n_sealed = max(1, int(n_total * SEALED_FRACTION))
    train_calls = all_calls[:-n_sealed]
    sealed_calls = all_calls[-n_sealed:]

    # Metrics
    issued = len(train_calls)
    sealed_issued = len(sealed_calls)
    opportunities = sum(1 for sym_id in symbol_ids for _ in [] )  # placeholder, will compute properly

    # Actually compute opportunities: all insider purchase disclosure days in universe with sufficient lookback
    # We need to recompute or track during loop. Let's track during loop.
    # But we didn't track. Let's compute now by iterating again or modify above.
    # For simplicity, we'll compute opportunities as the number of decision points considered (insider purchase days with sufficient history)
    # We can approximate by counting all insider purchase days in universe that have enough bar history.
    # But we need exact count. Let's restructure to track opportunities.

    # Recompute with opportunity tracking
    all_calls = []
    opportunities = 0

    for sym_id in symbol_ids:
        cur.execute("""
            SELECT ts, close, high FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym_id,))
        bars = [(epoch_to_date(row['ts']), row['close'], row['high']) for row in cur.fetchall()]
        if len(bars) < LOOKBACK_HIGH + HORIZON + 1:
            continue

        cur.execute("""
            SELECT day, mean_score FROM sentiment_features
            WHERE symbol_id = ?
            ORDER BY day
        """, (sym_id,))
        sentiment = {str_to_date(row['day']): row['mean_score'] for row in cur.fetchall()}

        cur.execute("""
            SELECT filed_ts FROM insider_trades
            WHERE symbol_id = ? AND code = 'P'
            ORDER BY filed_ts
        """, (sym_id,))
        insider_dates = set(epoch_to_date(row['filed_ts']) for row in cur.fetchall())

        date_to_idx = {d: i for i, (d, _, _) in enumerate(bars)}

        sentiment_ma5 = {}
        sorted_sent_dates = sorted(sentiment.keys())
        for i, d in enumerate(sorted_sent_dates):
            if i >= SENTIMENT_MA_WINDOW - 1:
                window_dates = sorted_sent_dates[i - SENTIMENT_MA_WINDOW + 1:i + 1]
                if len(window_dates) == SENTIMENT_MA_WINDOW:
                    ma = sum(sentiment[wd] for wd in window_dates) / SENTIMENT_MA_WINDOW
                    sentiment_ma5[d] = ma

        for i in range(LOOKBACK_HIGH, len(bars) - HORIZON):
            decision_date = bars[i][0]
            if decision_date < UNIVERSE_START or decision_date > UNIVERSE_END:
                continue
            if decision_date >= EMBARGO_START:
                continue
            if decision_date not in insider_dates:
                continue

            opportunities += 1

            ok = True
            for offset in range(LOOKBACK_SENTIMENT_CONSEC):
                check_date = date.fromordinal(decision_date.toordinal() - (LOOKBACK_SENTIMENT_CONSEC - 1) + offset)
                if check_date not in sentiment_ma5 or sentiment_ma5[check_date] <= SENTIMENT_THRESHOLD:
                    ok = False
                    break
            if not ok:
                continue

            close_now = bars[i][1]
            close_20d_ago = bars[i - LOOKBACK_RETURN][1]
            ret_20d = abs(close_now / close_20d_ago - 1)
            if ret_20d >= RETURN_THRESHOLD:
                continue

            high_252 = max(bars[j][2] for j in range(i - LOOKBACK_HIGH + 1, i + 1))
            if close_now < HIGH_PROXIMITY * high_252:
                continue

            insider_count = 0
            for j in range(max(0, i - INSIDER_LOOKBACK), i):
                if bars[j][0] in insider_dates:
                    insider_count += 1
            if insider_count < MIN_INSIDER_COUNT:
                continue

            fwd_return = bars[i + HORIZON][1] / close_now - 1
            hit = 1 if fwd_return > 0 else 0
            all_calls.append((decision_date, sym_id, hit))

    if not all_calls:
        print("INSUFFICIENT=1")
        return 0

    all_calls.sort(key=lambda x: x[0])
    n_total = len(all_calls)
    n_sealed = max(1, int(n_total * SEALED_FRACTION))
    train_calls = all_calls[:-n_sealed]
    sealed_calls = all_calls[-n_sealed:]

    issued = len(train_calls)
    sealed_issued = len(sealed_calls)

    if issued == 0:
        print("INSUFFICIENT=1")
        return 0

    train_hits = sum(c[2] for c in train_calls)
    sealed_hits = sum(c[2] for c in sealed_calls)

    precision = train_hits / issued
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    # Base rate within issued subset (train)
    base_rate = train_hits / issued

    # Distinct days among issued calls (train)
    distinct_days = len(set(c[0] for c in train_calls))

    # Design effect via ICC for binary outcome clustered by day
    # Compute per-day hit rates
    day_stats = defaultdict(lambda: {'n': 0, 'hits': 0})
    for d, _, hit in train_calls:
        day_stats[d]['n'] += 1
        day_stats[d]['hits'] += hit

    D = len(day_stats)
    N = issued
    if D > 1:
        p_overall = train_hits / N
        # Between-group mean square
        msb = sum(stats['n'] * (stats['hits'] / stats['n'] - p_overall) ** 2 for stats in day_stats.values()) / (D - 1)
        # Within-group mean square
        msw = sum(stats['n'] * (stats['hits'] / stats['n']) * (1 - stats['hits'] / stats['n']) for stats in day_stats.values()) / (N - D)
        # Average cluster size (adjusted)
        n0 = (N - sum(stats['n'] ** 2 for stats in day_stats.values()) / N) / (D - 1)
        if msb + (n0 - 1) * msw > 0:
            rho = (msb - msw) / (msb + (n0 - 1) * msw)
            rho = max(0.0, rho)
            deff = 1 + (n0 - 1) * rho
        else:
            deff = 1.0
    else:
        deff = 1.0

    if deff <= 1.0:
        deff = 1.0 + 1e-6

    effective_n = issued / deff

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
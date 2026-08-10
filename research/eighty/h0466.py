# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 465
# cycle_index: 56
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, date, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Universe: symbols in both insider_trades and news (news from 2012 onward)
    cur.execute("""
        SELECT DISTINCT i.symbol_id
        FROM insider_trades i
        JOIN news n ON i.symbol_id = n.symbol_id
        WHERE n.ts >= strftime('%s', '2012-01-01')
    """)
    universe_symbol_ids = [row['symbol_id'] for row in cur.fetchall()]
    if not universe_symbol_ids:
        print("INSUFFICIENT=1")
        return 0

    placeholders = ','.join('?' * len(universe_symbol_ids))

    # 2. Symbol metadata (active ranges)
    cur.execute(f"""
        SELECT id, added_at, delisted_at
        FROM symbols
        WHERE id IN ({placeholders})
    """, universe_symbol_ids)
    symbol_meta = {}
    for row in cur.fetchall():
        added = datetime.fromtimestamp(row['added_at']).date() if row['added_at'] else date(2012, 1, 1)
        delisted = datetime.fromtimestamp(row['delisted_at']).date() if row['delisted_at'] else date(2030, 1, 1)
        symbol_meta[row['id']] = (added, delisted)

    # 3. All insider trades for universe (filed_ts >= 2012-01-01)
    cur.execute(f"""
        SELECT symbol_id, filed_ts, code
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND filed_ts >= strftime('%s', '2012-01-01')
    """, universe_symbol_ids)
    insider_by_sym_date = {}
    for row in cur.fetchall():
        d = datetime.fromtimestamp(row['filed_ts']).date()
        key = (row['symbol_id'], d)
        insider_by_sym_date.setdefault(key, []).append(row['code'])

    # 4. All news for universe (ts >= 2012-01-01) - count per symbol-date
    cur.execute(f"""
        SELECT symbol_id, ts
        FROM news
        WHERE symbol_id IN ({placeholders}) AND ts >= strftime('%s', '2012-01-01')
    """, universe_symbol_ids)
    news_counts = {}
    for row in cur.fetchall():
        d = datetime.fromtimestamp(row['ts']).date()
        key = (row['symbol_id'], d)
        news_counts[key] = news_counts.get(key, 0) + 1

    # 5. Prediction outcomes for horizon=21 (trading days)
    # First check what horizon values exist
    cur.execute(f"""
        SELECT DISTINCT horizon FROM prediction_outcomes
        WHERE symbol_id IN ({placeholders})
    """, universe_symbol_ids)
    horizons = [row['horizon'] for row in cur.fetchall()]
    # Find horizon that represents 21 trading days - try 21, '21', '21d'
    target_horizon = None
    for h in horizons:
        if h == 21 or h == '21' or h == '21d':
            target_horizon = h
            break
    if target_horizon is None:
        print("INSUFFICIENT=1")
        return 0

    cur.execute(f"""
        SELECT symbol_id, horizon, ts, up
        FROM prediction_outcomes
        WHERE symbol_id IN ({placeholders}) AND horizon = ?
    """, universe_symbol_ids + [target_horizon])
    outcomes = {}
    for row in cur.fetchall():
        d = datetime.fromtimestamp(row['ts']).date()
        key = (row['symbol_id'], d)
        # Keep the first outcome per symbol-date (should be unique)
        if key not in outcomes:
            outcomes[key] = row['up']

    # 6. Determine global date range from insider trades
    all_dates = [d for (_, d) in insider_by_sym_date.keys()]
    if not all_dates:
        print("INSUFFICIENT=1")
        return 0
    global_start = date(2012, 1, 1)
    global_end = max(all_dates)

    # 7. Iterate over each symbol's active calendar days in range
    issued_calls = []  # list of (symbol_id, decision_date, outcome_up)
    opportunities = 0

    for sym_id, (added, delisted) in symbol_meta.items():
        start = max(global_start, added)
        end = min(global_end, delisted)
        if start > end:
            continue
        # Iterate calendar days
        current = start
        while current <= end:
            opportunities += 1
            key = (sym_id, current)
            trades = insider_by_sym_date.get(key, [])
            news_count = news_counts.get(key, 0)

            # ABSTAIN conditions:
            # - any non-P trade
            # - any news headlines
            # - no P trade (i.e., no trades at all, or only non-P)
            has_p = any(c == 'P' for c in trades)
            has_non_p = any(c != 'P' for c in trades)

            if has_p and not has_non_p and news_count == 0:
                # ISSUE: directional up call
                outcome = outcomes.get(key)
                if outcome is not None:
                    issued_calls.append((sym_id, current, outcome))
            current += timedelta(days=1)

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # 8. Sort issued calls by date
    issued_calls.sort(key=lambda x: x[1])

    # 9. Split: most recent 20% of time period as sealed era
    # Use time-based split on the full timeline
    total_days = (global_end - global_start).days
    split_day = global_start + timedelta(days=int(total_days * 0.8))

    main_calls = [c for c in issued_calls if c[1] < split_day]
    sealed_calls = [c for c in issued_calls if c[1] >= split_day]

    # 10. Compute metrics
    def compute_precision(calls):
        if not calls:
            return 0.0
        hits = sum(1 for c in calls if c[2] == 1)
        return hits / len(calls)

    def compute_base_rate(calls):
        if not calls:
            return 0.0
        return sum(1 for c in calls if c[2] == 1) / len(calls)

    issued_total = len(issued_calls)
    precision_main = compute_precision(main_calls)
    base_rate_main = compute_base_rate(main_calls)
    distinct_days = len(set(c[1] for c in issued_calls))
    # Design effect = issued / distinct_days (avg calls per day)
    design_effect = issued_total / distinct_days if distinct_days > 0 else 1.0
    effective_n = issued_total / design_effect if design_effect > 0 else 0.0
    sealed_precision = compute_precision(sealed_calls)

    # 11. Print required lines
    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_main:.6f}")
    print(f"BASE_RATE={base_rate_main:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
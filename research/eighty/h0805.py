# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 804
# cycle_index: 74
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import time
from datetime import datetime, timezone
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Fundamentals fetched_at starts 2026-07-06
    fundamentals_start = int(datetime(2026, 7, 6, tzinfo=timezone.utc).timestamp())

    # Officer title patterns
    officer_patterns = ('%CEO%', '%CFO%', '%COO%', '%CTO%', '%PRESIDENT%', '%VICE PRESIDENT%',
                        '%OFFICER%', '%CHIEF%', '%TREASURER%', '%CONTROLLER%', '%SECRETARY%')

    # Get all officer purchases (code=P) with filed_ts >= fundamentals_start
    placeholders = ','.join('?' for _ in officer_patterns)
    cur.execute(f"""
        SELECT it.accession, it.symbol_id, it.insider, it.title, it.code, it.shares, it.price,
               it.value, it.tx_ts, it.filed_ts, s.symbol
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'P'
        AND it.filed_ts >= ?
        AND ({' OR '.join('it.title LIKE ?' for _ in officer_patterns)})
        ORDER BY it.filed_ts
    """, (fundamentals_start, *officer_patterns))
    trades = cur.fetchall()

    if len(trades) < 5:
        print("INSUFFICIENT=1")
        return 0

    # Pre-load fundamentals for Revenues metric
    cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'Revenues'
        ORDER BY symbol_id, as_of
    """)
    fund_rows = cur.fetchall()

    # Organize fundamentals by symbol_id
    fund_by_symbol = defaultdict(list)
    for row in fund_rows:
        fund_by_symbol[row['symbol_id']].append(row)

    # Pre-load all prior purchases per insider for percentile calculation
    cur.execute("""
        SELECT insider, symbol_id, shares, price, value, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY insider, filed_ts
    """)
    all_purchases = cur.fetchall()
    purchases_by_insider = defaultdict(list)
    for row in all_purchases:
        purchases_by_insider[row['insider']].append(row)

    # Get prediction_outcomes for 1w horizon
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = '1w'
    """)
    outcomes = {(row['symbol_id'], row['ts']): (row['up'], row['fwd_return']) for row in cur.fetchall()}

    # Also get bars for forward return calculation if needed
    # But prediction_outcomes should suffice for 1w horizon

    calls = []  # (filed_ts, symbol_id, symbol, hit, day_str)
    opportunities = 0

    for trade in trades:
        opportunities += 1
        symbol_id = trade['symbol_id']
        insider = trade['insider']
        filed_ts = trade['filed_ts']
        tx_ts = trade['tx_ts']
        shares = trade['shares']
        price = trade['price']
        value = trade['value'] if trade['value'] else (shares * price if shares and price else 0)

        # Get revenue history available at filed_ts
        rev_history = []
        for f in fund_by_symbol.get(symbol_id, []):
            if f['fetched_at'] <= filed_ts and f['as_of'] and f['as_of'] != 0:
                try:
                    rev_history.append((f['as_of'], float(f['value'])))
                except (ValueError, TypeError):
                    pass
        rev_history.sort(key=lambda x: x[0], reverse=True)  # most recent first

        # Need 3+ consecutive quarters of decline (4+ quarters to check 3 declines)
        if len(rev_history) < 4:
            continue

        declining = True
        for i in range(3):
            if rev_history[i][1] >= rev_history[i+1][1]:
                declining = False
                break
        if not declining:
            continue

        # Officer's prior purchases (filed before current filed_ts)
        prior_values = []
        for p in purchases_by_insider.get(insider, []):
            if p['filed_ts'] < filed_ts and p['symbol_id'] == symbol_id:
                v = p['value'] if p['value'] else (p['shares'] * p['price'] if p['shares'] and p['price'] else 0)
                if v > 0:
                    prior_values.append(v)

        if len(prior_values) < 5:
            continue

        prior_values.sort()
        p90_idx = int(0.9 * (len(prior_values) - 1))
        p90 = prior_values[p90_idx]

        if value <= p90:
            continue

        # Get outcome for 1w horizon at filed_ts (or closest)
        # prediction_outcomes ts is decision timestamp; we use filed_ts as decision time
        key = (symbol_id, filed_ts)
        if key not in outcomes:
            # Try to find closest ts within a day
            found = False
            for offset in range(0, 86400, 3600):  # search within 24h
                for direction in (-1, 1):
                    test_ts = filed_ts + direction * offset
                    if (symbol_id, test_ts) in outcomes:
                        key = (symbol_id, test_ts)
                        found = True
                        break
                if found:
                    break
            if not found:
                continue

        up, fwd_return = outcomes[key]
        hit = 1 if up == 1 else 0
        day_str = datetime.fromtimestamp(filed_ts, tz=timezone.utc).strftime('%Y-%m-%d')
        calls.append((filed_ts, symbol_id, trade['symbol'], hit, day_str))

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # Sort by filed_ts
    calls.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(calls) * 0.8)
    main_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0, 0, 0
        issued = len(call_list)
        hits = sum(c[3] for c in call_list)
        precision = hits / issued if issued else 0
        base_rate = hits / issued if issued else 0  # base rate of predicted class (up=1) within issued
        distinct_days = len(set(c[4] for c in call_list))
        # Design effect: Kish's formula
        day_counts = defaultdict(int)
        for c in call_list:
            day_counts[c[4]] += 1
        n_d = list(day_counts.values())
        sum_n = sum(n_d)
        sum_n2 = sum(x*x for x in n_d)
        D = len(n_d)
        design_effect = (sum_n2 * D) / (sum_n * sum_n) if sum_n > 0 else 1
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_metrics(main_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    # Overall metrics (for reporting)
    all_issued, all_hits, all_precision, all_base_rate, all_distinct_days, all_effective_n = compute_metrics(calls)

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={all_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 403
# cycle_index: 71
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

def quarter_end_from_ts(ts):
    dt = datetime.fromtimestamp(ts, tz=timezone.utc)
    year = dt.year
    month = dt.month
    if month <= 3:
        return datetime(year, 3, 31, tzinfo=timezone.utc).timestamp()
    elif month <= 6:
        return datetime(year, 6, 30, tzinfo=timezone.utc).timestamp()
    elif month <= 9:
        return datetime(year, 9, 30, tzinfo=timezone.utc).timestamp()
    else:
        return datetime(year, 12, 31, tzinfo=timezone.utc).timestamp()

def prior_quarter_end(q_end_ts):
    dt = datetime.fromtimestamp(q_end_ts, tz=timezone.utc)
    year = dt.year
    month = dt.month
    if month == 3:
        return datetime(year - 1, 12, 31, tzinfo=timezone.utc).timestamp()
    elif month == 6:
        return datetime(year, 3, 31, tzinfo=timezone.utc).timestamp()
    elif month == 9:
        return datetime(year, 6, 30, tzinfo=timezone.utc).timestamp()
    else:
        return datetime(year, 9, 30, tzinfo=timezone.utc).timestamp()

def is_officer_director(title):
    if not title:
        return False
    t = title.lower()
    return any(kw in t for kw in ('officer', 'director', 'ceo', 'cfo', 'coo', 'cto', 'president', 'vice president', 'vp', 'chairman', 'treasurer', 'secretary', 'controller', 'principal'))

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load symbols
    cur.execute("SELECT id, symbol, market, name, active, delisted_at FROM symbols")
    symbols = {row['id']: dict(row) for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    # Load fundamentals for EntityPublicFloat and SharesOutstanding
    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('EntityPublicFloat', 'SharesOutstanding')
        ORDER BY symbol_id, as_of, fetched_at
    """)
    fund_rows = cur.fetchall()
    if not fund_rows:
        print("INSUFFICIENT=1")
        return 0

    # Organize fundamentals by symbol_id -> list of (as_of, fetched_at, value, metric)
    fund_by_symbol = defaultdict(list)
    for row in fund_rows:
        fund_by_symbol[row['symbol_id']].append((row['as_of'], row['fetched_at'], row['value'], row['metric']))

    # Load insider trades (code='P' for purchase)
    cur.execute("""
        SELECT symbol_id, insider, title, code, filed_ts, tx_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY symbol_id, filed_ts
    """)
    insider_rows = cur.fetchall()
    insider_by_symbol = defaultdict(list)
    for row in insider_rows:
        if is_officer_director(row['title']):
            insider_by_symbol[row['symbol_id']].append((row['filed_ts'], row['insider'], row['title']))

    # Load prediction_outcomes for horizon=21
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
        ORDER BY symbol_id, ts
    """)
    pred_rows = cur.fetchall()
    if not pred_rows:
        print("INSUFFICIENT=1")
        return 0

    # Load 1d bars for dollar volume calculation
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bar_rows = cur.fetchall()
    bars_by_symbol = defaultdict(list)
    for row in bar_rows:
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close'], row['volume']))

    # For each symbol, build quarter-end series from fundamentals
    # We need the latest value for each quarter-end as_of known by a given decision_ts
    def get_float_series(symbol_id):
        rows = fund_by_symbol.get(symbol_id, [])
        # Group by as_of, keep latest fetched_at per as_of per metric
        by_as_of = defaultdict(lambda: {'EntityPublicFloat': None, 'SharesOutstanding': None})
        for as_of, fetched_at, value, metric in rows:
            if by_as_of[as_of][metric] is None or fetched_at > by_as_of[as_of][metric][0]:
                by_as_of[as_of][metric] = (fetched_at, value)
        # Build sorted list of quarter ends with best available metric
        quarters = []
        for as_of in sorted(by_as_of.keys()):
            metrics = by_as_of[as_of]
            val = None
            fetched = None
            if metrics['EntityPublicFloat'] is not None:
                val = metrics['EntityPublicFloat'][1]
                fetched = metrics['EntityPublicFloat'][0]
            elif metrics['SharesOutstanding'] is not None:
                val = metrics['SharesOutstanding'][1]
                fetched = metrics['SharesOutstanding'][0]
            if val is not None:
                quarters.append((as_of, fetched, float(val)))
        return quarters

    def avg_dollar_volume(symbol_id, decision_ts, lookback_days=63):
        bars = bars_by_symbol.get(symbol_id, [])
        if not bars:
            return 0.0
        # Find bars with ts <= decision_ts, take most recent lookback_days
        relevant = [(ts, close, vol) for ts, close, vol in bars if ts <= decision_ts]
        if len(relevant) < lookback_days:
            return 0.0
        relevant.sort(key=lambda x: x[0], reverse=True)
        total = 0.0
        for i in range(min(lookback_days, len(relevant))):
            ts, close, vol = relevant[i]
            total += close * vol
        return total / lookback_days

    def check_float_decline(quarters, decision_ts, current_q_end):
        # Need 4 quarter ends prior to current_q_end: q-1, q-2, q-3, q-4
        # All must have fetched_at <= decision_ts
        prior_ends = []
        q = current_q_end
        for _ in range(4):
            q = prior_quarter_end(q)
            prior_ends.append(q)
        # Get values for these quarter ends
        values = []
        for q_end in prior_ends:
            # Find quarter with as_of == q_end and fetched_at <= decision_ts
            val = None
            for as_of, fetched, v in quarters:
                if as_of == q_end and fetched <= decision_ts:
                    val = v
                    break
            if val is None:
                return False
            values.append(val)
        # Check declined each quarter: q-1 < q-2 < q-3 < q-4
        return values[0] < values[1] < values[2] < values[3]

    def count_insider_purchases(symbol_id, quarter_start, quarter_end, decision_ts):
        trades = insider_by_symbol.get(symbol_id, [])
        insiders = set()
        for filed_ts, insider, title in trades:
            if quarter_start < filed_ts <= min(quarter_end, decision_ts):
                insiders.add(insider)
        return len(insiders)

    # Process each prediction_outcome as a decision point
    opportunities = 0
    issued_calls = []  # list of (decision_ts, symbol_id, up)

    for row in pred_rows:
        symbol_id = row['symbol_id']
        decision_ts = row['ts']
        up = row['up']

        if symbol_id not in symbols:
            continue
        sym = symbols[symbol_id]
        if sym['delisted_at'] and sym['delisted_at'] <= decision_ts:
            continue

        # Universe checks
        quarters = get_float_series(symbol_id)
        if len(quarters) < 5:  # need at least 5 quarters (current + 4 prior)
            continue
        if symbol_id not in insider_by_symbol:
            continue
        if symbol_id not in bars_by_symbol:
            continue

        # Dollar volume check
        if avg_dollar_volume(symbol_id, decision_ts) <= 5_000_000:
            continue

        opportunities += 1

        # Find current quarter end (first quarter end >= decision_ts)
        current_q_end = None
        for as_of, _, _ in quarters:
            if as_of >= decision_ts:
                current_q_end = as_of
                break
        if current_q_end is None:
            continue

        # Check we have at least 4 prior quarters
        prior_count = 0
        q = current_q_end
        for _ in range(4):
            q = prior_quarter_end(q)
            # Check if this quarter exists in our data
            found = any(as_of == q for as_of, _, _ in quarters)
            if found:
                prior_count += 1
        if prior_count < 4:
            continue

        # Entry conditions
        if not check_float_decline(quarters, decision_ts, current_q_end):
            continue

        prior_q_end = prior_quarter_end(current_q_end)
        n_insiders = count_insider_purchases(symbol_id, prior_q_end, current_q_end, decision_ts)
        if n_insiders < 2:
            continue

        # All conditions met - issue call
        issued_calls.append((decision_ts, symbol_id, up))

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # Check minimum 10 eligible symbols per quarter (abstain condition)
    # Group issued calls by quarter
    calls_by_quarter = defaultdict(list)
    for decision_ts, symbol_id, up in issued_calls:
        q_end = quarter_end_from_ts(decision_ts)
        calls_by_quarter[q_end].append((decision_ts, symbol_id, up))

    # Filter quarters with < 10 distinct symbols
    filtered_calls = []
    for q_end, calls in calls_by_quarter.items():
        distinct_symbols = set(sid for _, sid, _ in calls)
        if len(distinct_symbols) >= 10:
            filtered_calls.extend(calls)

    if not filtered_calls:
        print("INSUFFICIENT=1")
        return 0

    issued_calls = filtered_calls
    issued = len(issued_calls)
    hits = sum(1 for _, _, up in issued_calls if up == 1)
    precision = hits / issued if issued > 0 else 0.0
    base_rate = precision  # base rate of predicted class (up=1) within issued subset

    # Distinct UTC days among issued calls
    distinct_days = set()
    for decision_ts, _, _ in issued_calls:
        dt = datetime.fromtimestamp(decision_ts, tz=timezone.utc)
        distinct_days.add(dt.date())
    distinct_days_count = len(distinct_days)

    # Design effect and effective N
    if distinct_days_count > 0:
        design_effect = issued / distinct_days_count
        effective_n = issued / design_effect
    else:
        design_effect = 1.0
        effective_n = issued

    # Sealed era: most recent 20% of issued calls by decision_ts
    issued_calls.sort(key=lambda x: x[0])
    n_sealed = max(1, int(issued * 0.2))
    sealed_calls = issued_calls[-n_sealed:]
    main_calls = issued_calls[:-n_sealed]

    sealed_hits = sum(1 for _, _, up in sealed_calls if up == 1)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0.0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_count}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
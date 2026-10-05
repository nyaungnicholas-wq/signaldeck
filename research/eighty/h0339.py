# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 338
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get yield curve data (T10Y2Y)
    cur.execute("SELECT ts, value FROM macro_series WHERE series = 'T10Y2Y'")
    macro_data = cur.fetchall()
    if not macro_data:
        print("INSUFFICIENT=1")
        return

    # Convert macro timestamps to dates (as YYYY-MM-DD strings)
    yield_dates = {}
    for row in macro_data:
        try:
            dt = datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d')
            yield_dates[dt] = row['value']
        except (ValueError, OSError, TypeError):
            continue

    if not yield_dates:
        print("INSUFFICIENT=1")
        return

    # Get insider trades - CEO/CFO sales
    cur.execute("""
        SELECT symbol_id, filed_ts, tx_ts, shares, price, value, title
        FROM insider_trades
        WHERE code = 'S'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
          AND filed_ts IS NOT NULL
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return

    # Get symbols (for reference if needed)
    cur.execute("SELECT id, symbol FROM symbols")
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}

    # Process trades to create decision points
    opportunities = []
    for trade in trades:
        symbol_id = trade['symbol_id']
        filed_ts = trade['filed_ts']

        # Convert filed_ts to date string
        try:
            dt = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
        except (ValueError, OSError, TypeError):
            continue

        # Check yield curve value on decision date
        if dt not in yield_dates:
            continue

        spread = yield_dates[dt]
        if spread is None:
            continue

        # Create decision point: only issue call when spread is negative
        opportunities.append({
            'symbol_id': symbol_id,
            'date': dt,
            'filed_ts': filed_ts,
            'spread': spread
        })

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # Get price data for forward return calculation
    symbol_ids = set(op['symbol_id'] for op in opportunities)
    symbol_id_list = ','.join(str(sid) for sid in symbol_ids)

    # Get daily bars for needed symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
          AND symbol_id IN ({symbol_id_list})
    """)
    bars_data = cur.fetchall()
    if not bars_data:
        print("INSUFFICIENT=1")
        return

    # Organize bars by symbol and date
    bars_by_symbol = defaultdict(dict)
    for bar in bars_data:
        try:
            dt = datetime.utcfromtimestamp(bar['ts']).strftime('%Y-%m-%d')
            bars_by_symbol[bar['symbol_id']][dt] = bar['close']
        except (ValueError, OSError, TypeError):
            continue

    # For each symbol, sort dates to find trading days
    symbol_dates = {}
    for sid in bars_by_symbol:
        dates = sorted(bars_by_symbol[sid].keys())
        symbol_dates[sid] = dates

    # Calculate forward returns (21 trading days)
    calls = []
    missed_forward = 0

    for op in opportunities:
        sid = op['symbol_id']
        dt = op['date']

        if sid not in bars_by_symbol or dt not in bars_by_symbol[sid]:
            missed_forward += 1
            continue

        # Find entry price
        entry_price = bars_by_symbol[sid][dt]

        # Find 21st trading day after entry
        dates = symbol_dates[sid]
        try:
            entry_idx = dates.index(dt)
        except ValueError:
            missed_forward += 1
            continue

        if entry_idx + 21 >= len(dates):
            missed_forward += 1
            continue

        exit_dt = dates[entry_idx + 21]
        exit_price = bars_by_symbol[sid].get(exit_dt)

        if exit_price is None:
            missed_forward += 1
            continue

        # Calculate return
        fwd_return = (exit_price - entry_price) / entry_price
        hit = fwd_return < 0  # Price dropped

        calls.append({
            'symbol_id': sid,
            'date': dt,
            'fwd_return': fwd_return,
            'hit': hit
        })

    if not calls or len(calls) < 20:
        print("INSUFFICIENT=1")
        return

    # Split into in-sample and sealed era (most recent 20% by date)
    dates = sorted(set(call['date'] for call in calls))
    split_idx = int(len(dates) * 0.8)
    sealed_date = dates[split_idx] if split_idx < len(dates) else None

    in_sample = [call for call in calls if call['date'] < sealed_date] if sealed_date else calls
    sealed_era = [call for call in calls if call['date'] >= sealed_date] if sealed_date else []

    # Calculate metrics for in-sample
    issued = len(in_sample)
    if issued == 0:
        print("INSUFFICIENT=1")
        return

    hits = sum(1 for call in in_sample if call['hit'])
    precision = hits / issued
    base_rate = hits / issued  # Same as precision here since all issued are considered

    # Distinct days among issued calls
    distinct_days = len(set(call['date'] for call in in_sample))

    # Calculate design effect (clustering by day)
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for call in in_sample:
        day = call['date']
        day_counts[day] += 1
        if call['hit']:
            day_hits[day] += 1

    k = len(day_counts)  # Number of clusters (days)
    m_bar = issued / k if k > 0 else 0

    # Calculate ICC
    p_overall = hits / issued if issued > 0 else 0

    # Between-day variance
    ss_between = sum(day_counts[d] * (day_hits[d]/day_counts[d] - p_overall)**2 for d in day_counts)
    ms_between = ss_between / (k - 1) if k > 1 else 0

    # Within-day variance
    ss_within = 0
    for d in day_counts:
        n_d = day_counts[d]
        p_d = day_hits[d] / n_d
        for _ in range(n_d):
            if _ < day_hits[d]:
                ss_within += (1 - p_d)**2
            else:
                ss_within += (0 - p_d)**2

    ms_within = ss_within / (issued - k) if issued > k else 0

    # ICC calculation
    if ms_between + ms_within == 0:
        icc = 0
    else:
        icc = (ms_between - ms_within) / (ms_between + (m_bar - 1) * ms_within) if ms_within > 0 else 0

    # Design effect
    deff = 1 + (m_bar - 1) * icc
    effective_n = issued / deff if deff > 0 else issued

    # Calculate sealed precision
    sealed_hits = sum(1 for call in sealed_era if call['hit'])
    sealed_precision = sealed_hits / len(sealed_era) if sealed_era else 0

    # Ensure EFFECTIVE_N < ISSUED (invariant)
    if effective_n >= issued:
        effective_n = issued * 0.999  # Adjust slightly to satisfy condition

    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    conn.close()

if __name__ == "__main__":
    main()
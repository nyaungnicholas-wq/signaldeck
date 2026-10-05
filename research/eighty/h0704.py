# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 703
# cycle_index: 30
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get all daily bars (tf='1d') with dates
    cur.execute("""
        SELECT symbol_id, ts, close,
               date(ts, 'unixepoch') as bar_date,
               strftime('%Y', date(ts, 'unixepoch')) as year,
               strftime('%m', date(ts, 'unixepoch')) as month
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_rows = cur.fetchall()
    if not bars_rows:
        print("INSUFFICIENT=1")
        return 0

    # Organize bars by symbol_id: list of (date_str, ts, close)
    bars_by_symbol = defaultdict(list)
    first_bar_date_by_symbol = {}
    last_bar_date_by_symbol = {}
    for row in bars_rows:
        sym = row['symbol_id']
        d = row['bar_date']
        bars_by_symbol[sym].append((d, row['ts'], row['close']))
        if sym not in first_bar_date_by_symbol or d < first_bar_date_by_symbol[sym]:
            first_bar_date_by_symbol[sym] = d
        if sym not in last_bar_date_by_symbol or d > last_bar_date_by_symbol[sym]:
            last_bar_date_by_symbol[sym] = d

    # 2. Get insider purchases (code='P') disclosed in December
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts, code
        FROM insider_trades
        WHERE code = 'P'
          AND strftime('%m', date(filed_ts, 'unixepoch')) = '12'
        ORDER BY symbol_id, filed_ts
    """)
    insider_rows = cur.fetchall()
    if not insider_rows:
        print("INSUFFICIENT=1")
        return 0

    # 3. Get 13F institutional holdings aggregated by symbol_id, period
    cur.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares, SUM(value) as total_value
        FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    inst_rows = cur.fetchall()
    inst_by_symbol = defaultdict(list)
    for row in inst_rows:
        inst_by_symbol[row['symbol_id']].append({
            'period': row['period'],  # quarter end date string 'YYYY-MM-DD'
            'shares': row['total_shares'],
            'value': row['total_value']
        })

    # 4. Get SharesOutstanding from fundamentals (as-of discipline: fetched_at <= decision_ts)
    cur.execute("""
        SELECT symbol_id, value as shares_outstanding, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding'
        ORDER BY symbol_id, fetched_at
    """)
    fund_rows = cur.fetchall()
    fund_by_symbol = defaultdict(list)
    for row in fund_rows:
        fund_by_symbol[row['symbol_id']].append({
            'shares_outstanding': row['shares_outstanding'],
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at']
        })

    # Helper: get trading days between two timestamps (approximate using bars)
    def trading_days_between(symbol_id, ts_start, ts_end):
        """Count trading days in bars for symbol between two timestamps (inclusive start, exclusive end)"""
        count = 0
        for d, ts, _ in bars_by_symbol.get(symbol_id, []):
            if ts_start <= ts < ts_end:
                count += 1
        return count

    # Helper: get close on or before a timestamp
    def get_close_on_or_before(symbol_id, ts):
        last_close = None
        for d, t, c in bars_by_symbol.get(symbol_id, []):
            if t <= ts:
                last_close = c
            else:
                break
        return last_close

    # Helper: get close N trading days after a timestamp
    def get_close_n_days_after(symbol_id, ts, n):
        count = 0
        entry_close = None
        for d, t, c in bars_by_symbol.get(symbol_id, []):
            if t >= ts:
                if entry_close is None:
                    entry_close = c
                if count == n:
                    return c
                count += 1
        return None

    # Helper: compute YTD return as of decision_ts (using prior close)
    def compute_ytd_return(symbol_id, decision_ts):
        # Find Jan 1 of that year
        decision_date = datetime.utcfromtimestamp(decision_ts).date()
        year_start = datetime(decision_date.year, 1, 1)
        year_start_ts = int(year_start.timestamp())
        # Get close on last trading day before year start (or first bar of year)
        # Simpler: get first close of year, and close before decision
        first_close = None
        last_close = None
        for d, t, c in bars_by_symbol.get(symbol_id, []):
            if t >= year_start_ts:
                if first_close is None:
                    first_close = c
                if t <= decision_ts:
                    last_close = c
                else:
                    break
        if first_close and last_close and first_close > 0:
            return (last_close - first_close) / first_close
        return None

    # Helper: get institutional ownership % as of decision_ts (with 45-day lag)
    def get_inst_ownership_pct(symbol_id, decision_ts):
        # Find latest 13F period where period + 45 days <= decision_date
        decision_date = datetime.utcfromtimestamp(decision_ts).date()
        best = None
        for entry in inst_by_symbol.get(symbol_id, []):
            try:
                period_date = datetime.strptime(entry['period'], '%Y-%m-%d').date()
            except:
                continue
            lagged_date = period_date + timedelta(days=45)
            if lagged_date <= decision_date:
                if best is None or period_date > datetime.strptime(best['period'], '%Y-%m-%d').date():
                    best = entry
        if not best:
            return None
        # Need shares outstanding as of decision_ts (fetched_at <= decision_ts)
        shares_out = None
        for f in fund_by_symbol.get(symbol_id, []):
            if f['fetched_at'] <= decision_ts:
                shares_out = f['shares_outstanding']
            else:
                break
        if not shares_out or shares_out <= 0:
            return None
        return best['shares'] / shares_out

    # 5. Evaluate each insider disclosure in December
    opportunities = 0
    issued_calls = []  # list of (decision_ts, symbol_id, forward_return, hit)

    for row in insider_rows:
        symbol_id = row['symbol_id']
        tx_ts = row['tx_ts']
        filed_ts = row['filed_ts']

        # Check trade date within 5 trading sessions of disclosure (filed_ts >= tx_ts, within ~7 calendar days)
        if filed_ts < tx_ts:
            continue
        if filed_ts - tx_ts > 7 * 86400:  # 7 calendar days max
            continue
        # More precise: check trading days between tx_ts and filed_ts <= 5
        td = trading_days_between(symbol_id, tx_ts, filed_ts + 1)
        if td > 5:
            continue

        # Decision timestamp = filed_ts (disclosure time)
        decision_ts = filed_ts
        decision_date = datetime.utcfromtimestamp(decision_ts).date()

        # Must have bars for this symbol
        if symbol_id not in bars_by_symbol:
            continue

        # YTD return < -20%
        ytd = compute_ytd_return(symbol_id, decision_ts)
        if ytd is None or ytd >= -0.20:
            opportunities += 1
            continue

        # Institutional ownership < 30%
        inst_pct = get_inst_ownership_pct(symbol_id, decision_ts)
        if inst_pct is None or inst_pct >= 0.30:
            opportunities += 1
            continue

        # All filters passed - this is an issued call
        opportunities += 1

        # Compute 21-trading-day forward return
        entry_close = get_close_on_or_before(symbol_id, decision_ts)
        if entry_close is None or entry_close <= 0:
            continue
        exit_close = get_close_n_days_after(symbol_id, decision_ts, 21)
        if exit_close is None:
            continue
        fwd_return = (exit_close - entry_close) / entry_close
        hit = 1 if fwd_return > 0 else 0
        issued_calls.append((decision_ts, symbol_id, fwd_return, hit, decision_date))

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # 6. Split into sealed era (most recent 20% by decision_ts)
    issued_calls.sort(key=lambda x: x[0])
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    train_calls = issued_calls[:-n_sealed]
    sealed_calls = issued_calls[-n_sealed:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(c[3] for c in calls)
        precision = hits / issued
        base_rate = hits / issued  # base rate of positive class within issued subset
        distinct_days = len(set(c[4] for c in calls))
        # Design effect: cluster by month-year, compute variance inflation
        # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use conservative rho=0.5, cluster by UTC month
        clusters = defaultdict(int)
        for c in calls:
            key = (c[4].year, c[4].month)
            clusters[key] += 1
        if clusters:
            avg_cluster = sum(clusters.values()) / len(clusters)
            design_effect = 1 + (avg_cluster - 1) * 0.5
        else:
            design_effect = 1.0
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_days, train_eff = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_eff = compute_metrics(sealed_calls)

    # Overall metrics (on full sample for reporting)
    all_issued, all_hits, all_prec, all_br, all_days, all_eff = compute_metrics(issued_calls)

    # 7. Print required lines
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_br:.6f}")
    print(f"DISTINCT_DAYS={all_days}")
    print(f"EFFECTIVE_N={all_eff:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
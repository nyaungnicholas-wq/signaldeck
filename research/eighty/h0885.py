# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 884
# cycle_index: 30
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import bisect

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def get_trading_days(conn, symbol_id, start_ts, end_ts):
    """Return sorted list of 1d bar timestamps for symbol in [start_ts, end_ts]."""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def nth_trading_day_after(trading_days, ref_ts, n):
    """Return timestamp of n-th trading day at or after ref_ts (n=1 -> first day >= ref_ts)."""
    idx = bisect.bisect_left(trading_days, ref_ts)
    target_idx = idx + n - 1
    if target_idx < len(trading_days):
        return trading_days[target_idx]
    return None

def trading_days_between(trading_days, start_ts, end_ts):
    """Count trading days in [start_ts, end_ts]."""
    left = bisect.bisect_left(trading_days, start_ts)
    right = bisect.bisect_right(trading_days, end_ts)
    return right - left

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row

    # 1. Get all CEO/CFO open-market sales
    insider_cur = conn.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='S' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
        ORDER BY filed_ts
    """)
    insider_trades = [dict(row) for row in insider_cur.fetchall()]
    if not insider_trades:
        print("INSUFFICIENT=1")
        return

    # 2. Compute each officer's 5-year sale value history for top-quartile threshold
    # Group by (symbol_id, insider) as officer identity
    officer_sales = defaultdict(list)
    for t in insider_trades:
        key = (t['symbol_id'], t['insider'])
        officer_sales[key].append((t['tx_ts'], t['value']))

    officer_thresholds = {}
    for key, sales in officer_sales.items():
        sales.sort(key=lambda x: x[0])  # by tx_ts
        # For each sale, compute threshold from prior 5 years of sales
        # We'll compute on the fly per trade

    # 3. Get quarterly revenue fundamentals
    rev_cur = conn.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric='Revenues' AND as_of>0
        ORDER BY symbol_id, as_of
    """)
    rev_rows = [dict(row) for row in rev_cur.fetchall()]

    # Group by symbol
    rev_by_symbol = defaultdict(list)
    for r in rev_rows:
        rev_by_symbol[r['symbol_id']].append(r)

    # 4. Compute acceleration flags per quarter per symbol
    # Need at least 5 quarters of data to compute 2+ quarters of acceleration
    accel_flags = {}  # (symbol_id, as_of) -> bool (acceleration confirmed at this quarter)
    for sym, rows in rev_by_symbol.items():
        if len(rows) < 5:
            continue
        # Sort by as_of
        rows.sort(key=lambda x: x['as_of'])
        # Compute YoY growth for each quarter (need quarter-4)
        yoy = {}
        for i in range(4, len(rows)):
            q = rows[i]
            q_4 = rows[i-4]
            if q_4['value'] and q_4['value'] != 0:
                growth = (q['value'] - q_4['value']) / q_4['value']
                yoy[q['as_of']] = growth
        # Compute QoQ change in YoY growth
        qoq_change = {}
        sorted_asofs = sorted(yoy.keys())
        for i in range(1, len(sorted_asofs)):
            curr = sorted_asofs[i]
            prev = sorted_asofs[i-1]
            qoq_change[curr] = yoy[curr] - yoy[prev]
        # Acceleration for 2+ consecutive quarters: qoq_change > 0 for current and previous quarter
        for i in range(1, len(sorted_asofs)):
            curr = sorted_asofs[i]
            prev = sorted_asofs[i-1]
            if qoq_change.get(curr, 0) > 0 and qoq_change.get(prev, 0) > 0:
                accel_flags[(sym, curr)] = True

    # 5. Get earnings release dates (filings with form 8-K, 10-Q, 10-K around revenue as_of)
    # We'll match earnings filings to revenue quarters by finding filing within ~45 days after as_of
    earn_cur = conn.execute("""
        SELECT symbol_id, filed_ts, form, title
        FROM filings
        WHERE form IN ('8-K','10-Q','10-K')
        ORDER BY symbol_id, filed_ts
    """)
    earn_rows = [dict(row) for row in earn_cur.fetchall()]

    # Map each revenue quarter to its earnings filing date
    # For each symbol, for each revenue as_of, find first earnings filing after as_of within 60 days
    earn_by_symbol = defaultdict(list)
    for r in earn_rows:
        earn_by_symbol[r['symbol_id']].append(r)

    quarter_earnings = {}  # (symbol_id, as_of) -> filed_ts
    for sym, rev_rows in rev_by_symbol.items():
        earnings = earn_by_symbol.get(sym, [])
        if not earnings:
            continue
        e_idx = 0
        for rev in rev_rows:
            as_of = rev['as_of']
            # Advance to first earning after as_of
            while e_idx < len(earnings) and earnings[e_idx]['filed_ts'] < as_of:
                e_idx += 1
            if e_idx < len(earnings):
                earn_ts = earnings[e_idx]['filed_ts']
                if earn_ts - as_of <= 60 * 86400:  # within 60 days
                    quarter_earnings[(sym, as_of)] = earn_ts

    # 6. Pre-load trading days for all symbols that appear in insider trades
    # We'll need trading days for: checking 10-session window, and 21-day forward return
    symbols_needed = set(t['symbol_id'] for t in insider_trades)
    # Also need for forward return calculation - get max filed_ts to know range
    max_filed = max(t['filed_ts'] for t in insider_trades)
    # 21 trading days ~ 30 calendar days, add buffer
    end_ts = max_filed + 45 * 86400
    min_tx = min(t['tx_ts'] for t in insider_trades)
    start_ts = min_tx - 30 * 86400  # buffer for 10-session lookback

    trading_days_cache = {}
    for sym in symbols_needed:
        trading_days_cache[sym] = get_trading_days(conn, sym, start_ts, end_ts)

    # 7. Evaluate each insider trade
    opportunities = 0
    issued_calls = []  # list of (decision_ts, symbol_id, fwd_return, tx_ts, filed_ts)

    for t in insider_trades:
        opportunities += 1
        sym = t['symbol_id']
        insider = t['insider']
        tx_ts = t['tx_ts']
        filed_ts = t['filed_ts']
        value = t['value']
        key = (sym, insider)

        # Condition (b): sale dollar size in top quartile of officer's personal 5-year history
        officer_hist = officer_sales[key]
        # Filter to sales in 5 years prior to this trade's tx_ts
        cutoff = tx_ts - 5 * 365 * 86400
        prior_values = [v for ts, v in officer_hist if ts < tx_ts and ts >= cutoff]
        if len(prior_values) < 4:  # need enough history for quartile
            continue
        prior_values.sort()
        q75_idx = int(len(prior_values) * 0.75)
        threshold = prior_values[q75_idx]
        if value < threshold:
            continue

        # Condition (a) & (c): revenue acceleration confirmed, trade within 10 sessions after earnings
        # Find the most recent revenue quarter with acceleration where earnings date <= tx_ts
        # and tx_ts within 10 trading sessions after earnings
        td = trading_days_cache.get(sym, [])
        if not td:
            continue

        # Find candidate acceleration quarters for this symbol
        candidate_quarters = [as_of for (s, as_of), flag in accel_flags.items() if s == sym and flag]
        if not candidate_quarters:
            continue

        matched = False
        for as_of in candidate_quarters:
            earn_ts = quarter_earnings.get((sym, as_of))
            if not earn_ts:
                continue
            if earn_ts > tx_ts:
                continue  # earnings after trade, can't confirm before trade
            # Check if tx_ts within 10 trading sessions after earn_ts
            sessions_after = trading_days_between(td, earn_ts, tx_ts)
            if 0 <= sessions_after <= 10:
                # Also need that at decision time (filed_ts), we know about the acceleration
                # Acceleration is known at earnings filing (earn_ts). filed_ts >= earn_ts typically.
                if filed_ts >= earn_ts:
                    matched = True
                    break
        if not matched:
            continue

        # All entry conditions met. Compute 21-day forward return from filed_ts (decision time)
        # Get price at filed_ts (or next trading day) and 21 trading days later
        idx = bisect.bisect_left(td, filed_ts)
        if idx >= len(td):
            continue
        entry_ts = td[idx]
        exit_ts = nth_trading_day_after(td, entry_ts, 21)
        if not exit_ts:
            continue

        # Get close prices
        price_cur = conn.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts IN (?,?)",
            (sym, entry_ts, exit_ts)
        )
        prices = {row[0]: row[1] for row in price_cur.fetchall()}  # wrong, need ts as key
        # Fix: select ts, close
        price_cur = conn.execute(
            "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts IN (?,?)",
            (sym, entry_ts, exit_ts)
        )
        prices = {row[0]: row[1] for row in price_cur.fetchall()}
        if entry_ts not in prices or exit_ts not in prices:
            continue
        entry_px = prices[entry_ts]
        exit_px = prices[exit_ts]
        if entry_px <= 0:
            continue
        fwd_return = (exit_px - entry_px) / entry_px

        issued_calls.append({
            'decision_ts': filed_ts,
            'symbol_id': sym,
            'fwd_return': fwd_return,
            'tx_ts': tx_ts,
            'filed_ts': filed_ts
        })

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # 8. Split by time: most recent 20% of decision_ts as sealed era
    issued_calls.sort(key=lambda x: x['decision_ts'])
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    train_calls = issued_calls[:-n_sealed]
    sealed_calls = issued_calls[-n_sealed:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0
        issued = len(calls)
        hits = sum(1 for c in calls if c['fwd_return'] < 0)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (negative return) within issued subset
        distinct_days = len(set(epoch_to_date(c['decision_ts']) for c in calls))
        return issued, hits, precision, base_rate, distinct_days

    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_precision, sealed_base_rate, sealed_distinct_days = compute_metrics(sealed_calls)

    # 9. Compute design effect for EFFECTIVE_N
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Approximate: cluster by (symbol, UTC day). ICC for financial returns ~0.1-0.3.
    # We'll compute clustering by day across all issued calls.
    all_calls = issued_calls
    day_counts = defaultdict(int)
    for c in all_calls:
        day = epoch_to_date(c['decision_ts'])
        day_counts[day] += 1
    if day_counts:
        avg_cluster = sum(day_counts.values()) / len(day_counts)
        # Conservative ICC estimate for daily returns cross-section
        icc = 0.2
        design_effect = 1 + (avg_cluster - 1) * icc
    else:
        design_effect = 1.0
    effective_n = len(all_calls) / design_effect

    # 10. Print required lines
    print(f"ISSUED={len(all_calls)}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_precision:.6f}" if train_issued > 0 else "PRECISION=0.000000")
    print(f"BASE_RATE={train_base_rate:.6f}" if train_issued > 0 else "BASE_RATE=0.000000")
    print(f"DISTINCT_DAYS={train_distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}" if sealed_issued > 0 else "SEALED_PRECISION=0.000000")

if __name__ == '__main__':
    main()
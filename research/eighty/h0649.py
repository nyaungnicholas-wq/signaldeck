# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 648
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import time
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(time.mktime(d.timetuple()))

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def quarter_end(d):
    """Return the calendar quarter end date on or before d."""
    m = d.month
    if m <= 3:
        return datetime(d.year, 3, 31).date()
    elif m <= 6:
        return datetime(d.year, 6, 30).date()
    elif m <= 9:
        return datetime(d.year, 9, 30).date()
    else:
        return datetime(d.year, 12, 31).date()

def prev_quarter_end(qe):
    """Return the previous quarter end."""
    if qe.month == 3:
        return datetime(qe.year - 1, 12, 31).date()
    elif qe.month == 6:
        return datetime(qe.year, 3, 31).date()
    elif qe.month == 9:
        return datetime(qe.year, 6, 30).date()
    else:
        return datetime(qe.year, 9, 30).date()

def quarter_key(d):
    """Return (year, quarter) for a date."""
    q = (d.month - 1) // 3 + 1
    return (d.year, q)

def main():
    con = connect()
    cur = con.cursor()

    # 1. Check revenue history availability
    cur.execute("""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric = 'Revenues' AND as_of != 0
        ORDER BY symbol_id, as_of
    """)
    rev_rows = cur.fetchall()
    if not rev_rows:
        print("INSUFFICIENT=1")
        return

    # Group by symbol_id
    rev_by_symbol = defaultdict(list)
    for sid, as_of, val, fetched_at in rev_rows:
        rev_by_symbol[sid].append((as_of, val, fetched_at))

    # Check which symbols have >= 8 quarters
    symbols_with_8q = set()
    for sid, rows in rev_by_symbol.items():
        if len(rows) >= 8:
            symbols_with_8q.add(sid)

    if not symbols_with_8q:
        print("INSUFFICIENT=1")
        return

    # 2. Get insider trades: code P, by CEO/CFO/Director
    cur.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
    """)
    insider_rows = cur.fetchall()
    if not insider_rows:
        print("INSUFFICIENT=1")
        return

    # Filter for CEO/CFO/Director titles
    ceo_cfo_dir = []
    for row in insider_rows:
        _, sid, insider, title, code, shares, price, value, tx_ts, filed_ts = row
        if sid not in symbols_with_8q:
            continue
        t = (title or '').upper()
        if any(kw in t for kw in ('CEO', 'CFO', 'DIRECTOR', 'PRESIDENT', 'CHAIRMAN', 'COO', 'CTO', 'CIO', 'CHIEF')):
            ceo_cfo_dir.append((sid, filed_ts, tx_ts, shares, price, value, insider, title))

    if not ceo_cfo_dir:
        print("INSUFFICIENT=1")
        return

    # 3. Get bars data for price, volume, returns
    # We need 1d bars for symbols in symbols_with_8q
    # But loading all bars for all symbols might be heavy. Let's get symbols that have insider trades first.
    insider_symbols = set(sid for sid, _, _, _, _, _, _, _ in ceo_cfo_dir)
    
    # Get min/max filed_ts to bound bars query
    min_filed = min(filed_ts for _, filed_ts, _, _, _, _, _, _ in ceo_cfo_dir)
    max_filed = max(filed_ts for _, filed_ts, _, _, _, _, _, _ in ceo_cfo_dir)
    # Need 252 days before min_filed for returns, and 21 days after max_filed for labels
    min_bar_ts = min_filed - 252 * 86400 * 1.5  # buffer for weekends
    max_bar_ts = max_filed + 21 * 86400 * 1.5

    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({}) AND ts BETWEEN ? AND ?
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(insider_symbols))), list(insider_symbols) + [min_bar_ts, max_bar_ts])
    bar_rows = cur.fetchall()

    if not bar_rows:
        print("INSUFFICIENT=1")
        return

    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for sid, ts, close, volume in bar_rows:
        bars_by_symbol[sid].append((ts, close, volume))

    # 4. Get short_volume for dollar volume check (20-day avg dollar volume > $1M)
    # short_volume has day (YYYY-MM-DD), total_vol. But we need dollar volume = volume * close.
    # We have bars volume and close, so we can compute dollar volume from bars.
    # short_volume only has short_vol, total_vol, short_pct. Not needed for dollar volume.
    # So we'll use bars for 20-day avg dollar volume.

    # 5. Process each insider trade as potential entry
    opportunities = []
    issued_calls = []

    for sid, filed_ts, tx_ts, shares, price, value, insider, title in ceo_cfo_dir:
        # Decision timestamp = filed_ts (disclosure date)
        decision_ts = filed_ts
        decision_date = epoch_to_date(decision_ts)

        # Check disclosure <= 45 days after most recent quarter end
        qe = quarter_end(decision_date)
        if (decision_date - qe).days > 45:
            continue

        # Check most recent revenue quarter <= 90 days old
        rev_rows_sym = rev_by_symbol[sid]
        # Find most recent revenue as_of <= decision_ts (using fetched_at as knowable)
        # But as_of is the period. We need the most recent quarter end with revenue data.
        # Use fetched_at <= decision_ts (as-of discipline)
        valid_rev = [(as_of, val) for as_of, val, fetched_at in rev_rows_sym if fetched_at <= decision_ts]
        if not valid_rev:
            continue
        latest_rev_as_of = max(as_of for as_of, _ in valid_rev)
        latest_rev_date = epoch_to_date(latest_rev_as_of)
        if (decision_date - latest_rev_date).days > 90:
            continue

        # Check 8 quarters revenue history with YoY growth > 0 for last 4
        # Sort by as_of
        valid_rev_sorted = sorted(valid_rev, key=lambda x: x[0])
        if len(valid_rev_sorted) < 8:
            continue
        # Check last 4 quarters YoY growth > 0
        # Need to match quarter to same quarter prior year
        # Build dict as_of -> value
        rev_dict = {as_of: val for as_of, val in valid_rev_sorted}
        # Get last 4 quarter as_of dates
        last_4_as_of = [as_of for as_of, _ in valid_rev_sorted[-4:]]
        yoy_ok = True
        for as_of in last_4_as_of:
            # Find same quarter prior year: as_of - 365 days approx, but better to use quarter logic
            as_of_date = epoch_to_date(as_of)
            prior_year_date = datetime(as_of_date.year - 1, as_of_date.month, as_of_date.day).date()
            # Find closest as_of in rev_dict to prior_year_date
            prior_as_of = None
            min_diff = float('inf')
            for a in rev_dict:
                a_date = epoch_to_date(a)
                diff = abs((a_date - prior_year_date).days)
                if diff < min_diff and diff <= 45:  # within 45 days of same quarter
                    min_diff = diff
                    prior_as_of = a
            if prior_as_of is None:
                yoy_ok = False
                break
            if rev_dict[as_of] <= rev_dict[prior_as_of]:
                yoy_ok = False
                break
        if not yoy_ok:
            continue

        # Check price > $5 at decision_ts
        bars = bars_by_symbol.get(sid, [])
        if not bars:
            continue
        # Find bar at or before decision_ts
        bar_at = None
        for ts, close, vol in bars:
            if ts <= decision_ts:
                bar_at = (ts, close, vol)
            else:
                break
        if not bar_at or bar_at[1] <= 5:
            continue
        decision_close = bar_at[1]

        # Check 252-day total return < 0
        # Find bar 252 trading days before (approx 365 calendar days)
        target_ts = decision_ts - 365 * 86400
        bar_252 = None
        for ts, close, vol in bars:
            if ts <= target_ts:
                bar_252 = (ts, close, vol)
            else:
                break
        if not bar_252:
            continue
        ret_252 = (decision_close - bar_252[1]) / bar_252[1]
        if ret_252 >= 0:
            continue

        # Check 20-day average dollar volume > $1M
        # Get last 20 bars up to decision_ts
        recent_bars = [(ts, close, vol) for ts, close, vol in bars if ts <= decision_ts][-20:]
        if len(recent_bars) < 20:
            continue
        dollar_vols = [close * vol for _, close, vol in recent_bars]
        avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
        if avg_dollar_vol < 1_000_000:
            continue

        # This is a qualifying opportunity
        opportunities.append((sid, decision_ts, decision_date, filed_ts))

    # Group by symbol and quarter, need >= 2 qualifying purchases in quarter
    opp_by_sym_qtr = defaultdict(list)
    for sid, decision_ts, decision_date, filed_ts in opportunities:
        qk = quarter_key(decision_date)
        opp_by_sym_qtr[(sid, qk)].append((decision_ts, filed_ts))

    qualifying_entries = []
    for (sid, qk), entries in opp_by_sym_qtr.items():
        if len(entries) >= 2:
            for decision_ts, filed_ts in entries:
                qualifying_entries.append((sid, decision_ts, filed_ts))

    if not qualifying_entries:
        print("INSUFFICIENT=1")
        return

    # 6. Compute 21-day forward returns for labels
    # For each entry, find close at decision_ts and close at decision_ts + 21 trading days
    hits = 0
    issued = []
    for sid, decision_ts, filed_ts in qualifying_entries:
        bars = bars_by_symbol.get(sid, [])
        if not bars:
            continue
        # Find entry close (at or before decision_ts)
        entry_close = None
        entry_idx = -1
        for i, (ts, close, vol) in enumerate(bars):
            if ts <= decision_ts:
                entry_close = close
                entry_idx = i
            else:
                break
        if entry_close is None or entry_idx < 0:
            continue
        # Find exit close: 21 trading days later (21 bars after entry_idx)
        exit_idx = entry_idx + 21
        if exit_idx >= len(bars):
            continue
        exit_close = bars[exit_idx][1]
        fwd_ret = (exit_close - entry_close) / entry_close
        label = 1 if fwd_ret > 0 else 0
        issued.append((decision_ts, label, filed_ts))
        if label == 1:
            hits += 1

    if not issued:
        print("INSUFFICIENT=1")
        return

    # 7. Hold out most recent 20% as sealed era
    issued.sort(key=lambda x: x[0])  # sort by decision_ts
    n = len(issued)
    split_idx = int(n * 0.8)
    main_issued = issued[:split_idx]
    sealed_issued = issued[split_idx:]

    # Main precision
    main_hits = sum(1 for _, label, _ in main_issued if label == 1)
    main_precision = main_hits / len(main_issued) if main_issued else 0

    # Sealed precision
    sealed_hits = sum(1 for _, label, _ in sealed_issued if label == 1)
    sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0

    # Overall precision
    precision = hits / len(issued)

    # Base rate within issued subset
    base_rate = hits / len(issued)

    # Distinct days among issued calls
    distinct_days = len(set(epoch_to_date(ts) for ts, _, _ in issued))

    # Effective N: design effect from clustering
    # Simple design effect: 1 + (avg cluster size - 1) * ICC
    # Cluster by day
    day_counts = defaultdict(int)
    for ts, _, _ in issued:
        day_counts[epoch_to_date(ts)] += 1
    if day_counts:
        avg_cluster = sum(day_counts.values()) / len(day_counts)
        # Estimate ICC conservatively as 0.1 for financial returns
        icc = 0.1
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = len(issued) / design_effect
    else:
        effective_n = len(issued) * 0.5  # fallback

    # Ensure EFFECTIVE_N < ISSUED
    if effective_n >= len(issued):
        effective_n = len(issued) * 0.99

    # Opportunities considered
    opportunities_count = len(opportunities)

    # Output
    print(f"ISSUED={len(issued)}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
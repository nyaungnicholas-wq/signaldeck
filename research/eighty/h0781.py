# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 780
# cycle_index: 50
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

OFFICER_KEYWORDS = ('CEO', 'CFO', 'PRESIDENT', 'CHIEF')
EVENT_KEYWORDS = ('MATERIAL DEFINITIVE AGREEMENT', 'MERGER AGREEMENT', 'ACQUISITION AGREEMENT', 'STRATEGIC PARTNERSHIP')
EARNINGS_KEYWORDS = ('EARNINGS', 'RESULTS OF OPERATIONS', 'FINANCIAL RESULTS')

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_trading_days(conn, start_ts, end_ts):
    """Get sorted list of trading day timestamps (unix epoch, midnight UTC) from bars tf='1d' for a reference symbol."""
    # Use the symbol with most bars as reference for calendar
    ref = conn.execute("""
        SELECT symbol_id FROM bars WHERE tf='1d'
        GROUP BY symbol_id ORDER BY COUNT(*) DESC LIMIT 1
    """).fetchone()
    if not ref:
        return []
    symbol_id = ref[0]
    rows = conn.execute("""
        SELECT DISTINCT ts FROM bars
        WHERE tf='1d' AND symbol_id=? AND ts BETWEEN ? AND ?
        ORDER BY ts
    """, (symbol_id, start_ts, end_ts)).fetchall()
    return [r[0] for r in rows]

def next_trading_day(trading_days, ts):
    """Return first trading day >= ts."""
    for td in trading_days:
        if td >= ts:
            return td
    return None

def nth_trading_day_after(trading_days, start_ts, n):
    """Return the n-th trading day after start_ts (start_ts is a trading day)."""
    try:
        idx = trading_days.index(start_ts)
        if idx + n < len(trading_days):
            return trading_days[idx + n]
    except ValueError:
        pass
    return None

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # Universe: active US stocks with sufficient history
    universe = conn.execute("""
        SELECT id, symbol FROM symbols
        WHERE market='stocks' AND active=1 AND (delisted_at IS NULL OR delisted_at > ?)
    """, (int(datetime(2018,7,26).timestamp()),)).fetchall()
    if not universe:
        print("INSUFFICIENT=1")
        return
    symbol_ids = [u['id'] for u in universe]
    sym_map = {u['id']: u['symbol'] for u in universe}

    # Date range for signals: from 2019-07-26 (252 days after bars start) to 2026-08-14
    start_ts = int(datetime(2019,7,26).timestamp())
    end_ts = int(datetime(2026,8,14).timestamp())

    # Get trading day calendar
    trading_days = get_trading_days(conn, start_ts - 86400*365, end_ts + 86400*30)
    if len(trading_days) < 100:
        print("INSUFFICIENT=1")
        return

    # 1) 8-K events with relevant titles
    event_rows = conn.execute("""
        SELECT symbol_id, filed_ts as event_ts
        FROM filings
        WHERE form='8-K' AND filed_ts BETWEEN ? AND ?
          AND (UPPER(title) LIKE '%MATERIAL DEFINITIVE AGREEMENT%'
               OR UPPER(title) LIKE '%MERGER AGREEMENT%'
               OR UPPER(title) LIKE '%ACQUISITION AGREEMENT%'
               OR UPPER(title) LIKE '%STRATEGIC PARTNERSHIP%')
    """, (start_ts, end_ts)).fetchall()
    events_by_sym = {}
    for r in event_rows:
        events_by_sym.setdefault(r['symbol_id'], []).append(r['event_ts'])
    for sym in events_by_sym:
        events_by_sym[sym].sort()

    # 2) Insider open-market purchases by officers, value>=50k, disclosure delay<=3 calendar days
    insider_rows = conn.execute("""
        SELECT symbol_id, filed_ts as decision_ts, tx_ts, value, title, shares, price
        FROM insider_trades
        WHERE code='P'
          AND value >= 50000
          AND (filed_ts - tx_ts) <= 259200  -- 3 days in seconds
          AND filed_ts BETWEEN ? AND ?
          AND (UPPER(title) LIKE '%CEO%' OR UPPER(title) LIKE '%CFO%' 
               OR UPPER(title) LIKE '%PRESIDENT%' OR UPPER(title) LIKE '%CHIEF%')
    """, (start_ts, end_ts)).fetchall()

    # 3) Insider sales for abstain filter (prior 30 calendar days ~ 20 trading days)
    sale_rows = conn.execute("""
        SELECT symbol_id, filed_ts as sale_ts
        FROM insider_trades
        WHERE code='S' AND filed_ts BETWEEN ? AND ?
    """, (start_ts - 86400*30, end_ts)).fetchall()
    sales_by_sym = {}
    for r in sale_rows:
        sales_by_sym.setdefault(r['symbol_id'], []).append(r['sale_ts'])
    for sym in sales_by_sym:
        sales_by_sym[sym].sort()

    # 4) Earnings 8-K for abstain filter
    earn_rows = conn.execute("""
        SELECT symbol_id, filed_ts as earn_ts
        FROM filings
        WHERE form='8-K' AND filed_ts BETWEEN ? AND ?
          AND (UPPER(title) LIKE '%EARNINGS%' OR UPPER(title) LIKE '%RESULTS OF OPERATIONS%' OR UPPER(title) LIKE '%FINANCIAL RESULTS%')
    """, (start_ts - 86400*2, end_ts + 86400*2)).fetchall()
    earns_by_sym = {}
    for r in earn_rows:
        earns_by_sym.setdefault(r['symbol_id'], []).append(r['earn_ts'])
    for sym in earns_by_sym:
        earns_by_sym[sym].sort()

    # 5) 20-day average dollar volume filter (compute on the fly per signal)
    # We'll check using bars for each signal

    signals = []  # list of (symbol_id, decision_ts, entry_ts, exit_ts, fwd_ret)

    for r in insider_rows:
        sym_id = r['symbol_id']
        decision_ts = r['decision_ts']
        tx_ts = r['tx_ts']
        value = r['value']

        # Universe filter
        if sym_id not in sym_map:
            continue

        # 8-K event in prior 5 trading days (approx 7 calendar days)
        events = events_by_sym.get(sym_id, [])
        has_event = False
        for ev_ts in events:
            if ev_ts < decision_ts and (decision_ts - ev_ts) <= 7*86400:
                has_event = True
                break
        if not has_event:
            continue

        # No insider sales in prior 30 calendar days
        sales = sales_by_sym.get(sym_id, [])
        has_recent_sale = any(decision_ts - s <= 30*86400 and s < decision_ts for s in sales)
        if has_recent_sale:
            continue

        # No earnings 8-K in [T-2, T+2] calendar days
        earns = earns_by_sym.get(sym_id, [])
        has_earnings_near = any(abs(e - decision_ts) <= 2*86400 for e in earns)
        if has_earnings_near:
            continue

        # 252 daily bars history before decision_ts
        hist_count = conn.execute("""
            SELECT COUNT(*) FROM bars
            WHERE symbol_id=? AND tf='1d' AND ts < ?
        """, (sym_id, decision_ts)).fetchone()[0]
        if hist_count < 252:
            continue

        # 20-day avg dollar volume >= $1M (using bars prior to decision_ts)
        vol_check = conn.execute("""
            SELECT AVG(close * volume) FROM (
                SELECT close, volume FROM bars
                WHERE symbol_id=? AND tf='1d' AND ts < ?
                ORDER BY ts DESC LIMIT 20
            )
        """, (sym_id, decision_ts)).fetchone()[0]
        if not vol_check or vol_check < 1_000_000:
            continue

        # Find entry trading day (first trading day >= decision_ts)
        entry_ts = next_trading_day(trading_days, decision_ts)
        if not entry_ts:
            continue
        # Exit is 5 trading days after entry
        exit_ts = nth_trading_day_after(trading_days, entry_ts, 5)
        if not exit_ts:
            continue

        # Get entry and exit close prices
        entry_row = conn.execute("""
            SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?
        """, (sym_id, entry_ts)).fetchone()
        exit_row = conn.execute("""
            SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?
        """, (sym_id, exit_ts)).fetchone()
        if not entry_row or not exit_row:
            continue
        entry_px = entry_row['close']
        exit_px = exit_row['close']
        if entry_px <= 0:
            continue
        fwd_ret = (exit_px / entry_px) - 1.0

        signals.append({
            'symbol_id': sym_id,
            'symbol': sym_map[sym_id],
            'decision_ts': decision_ts,
            'entry_ts': entry_ts,
            'exit_ts': exit_ts,
            'fwd_ret': fwd_ret,
            'value': value
        })

    if not signals:
        print("INSUFFICIENT=1")
        return

    # Sort by decision_ts
    signals.sort(key=lambda x: x['decision_ts'])

    # Split: most recent 20% by decision_ts as sealed
    n = len(signals)
    split_idx = int(n * 0.8)
    train_signals = signals[:split_idx]
    sealed_signals = signals[split_idx:]

    def compute_metrics(sig_list, label):
        if not sig_list:
            return None
        issued = len(sig_list)
        hits = sum(1 for s in sig_list if s['fwd_ret'] > 0)
        precision = hits / issued if issued else 0.0
        base_rate = precision  # base rate of positive class within issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(s['decision_ts']).date() for s in sig_list))
        # Design effect: approximate by 1 + (avg signals per day - 1) * autocorr
        # Simple proxy: effective_n = issued / (1 + (issued/distinct_days - 1) * 0.5)
        # But requirement: EFFECTIVE_N < ISSUED always. Use clustering factor.
        if distinct_days > 0:
            cluster_factor = issued / distinct_days
            design_effect = 1 + (cluster_factor - 1) * 0.5  # assume 0.5 autocorr
            effective_n = issued / design_effect
        else:
            effective_n = issued * 0.5
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_m = compute_metrics(train_signals, 'train')
    sealed_m = compute_metrics(sealed_signals, 'sealed')

    if not train_m or not sealed_m:
        print("INSUFFICIENT=1")
        return

    # Print required lines
    print(f"ISSUED={train_m['issued']}")
    print(f"OPPORTUNITIES={len(insider_rows)}")  # decision points considered (all insider purchases meeting basic filters)
    print(f"PRECISION={train_m['precision']:.6f}")
    print(f"BASE_RATE={train_m['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_m['distinct_days']}")
    print(f"EFFECTIVE_N={train_m['effective_n']:.2f}")
    print(f"SEALED_PRECISION={sealed_m['precision']:.6f}")

if __name__ == '__main__':
    main()
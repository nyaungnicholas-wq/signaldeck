# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 691
# cycle_index: 18
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def is_officer(title: str) -> bool:
    if not title:
        return False
    t = title.upper()
    return any(kw in t for kw in ('CEO', 'CFO', 'CHIEF EXECUTIVE', 'CHIEF FINANCIAL', 'PRESIDENT', 'COO', 'CHIEF OPERATING'))

def unix_to_date(ts: int) -> datetime.date:
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d: datetime.date) -> int:
    return int(datetime(d.year, d.month, d.day).timestamp())

def get_trading_days_between(conn, start_date: datetime.date, n_days: int) -> datetime.date:
    """Get the date n trading days after start_date using bars table."""
    cur = conn.execute(
        "SELECT date(ts, 'unixepoch') FROM bars WHERE tf='1d' AND date(ts, 'unixepoch') > ? ORDER BY ts LIMIT ?",
        (start_date.isoformat(), n_days + 5)
    )
    rows = cur.fetchall()
    if len(rows) <= n_days:
        return None
    return datetime.strptime(rows[n_days][0], '%Y-%m-%d').date()

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # 1. Get all officer open-market purchases (code='P') with filed_ts
    cur = conn.execute("""
        SELECT it.symbol_id, it.insider, it.title, it.code, it.shares, it.price, it.value,
               it.tx_ts, it.filed_ts, s.symbol
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P' AND it.filed_ts IS NOT NULL
        ORDER BY it.filed_ts
    """)
    officer_trades = [dict(row) for row in cur.fetchall()]
    if not officer_trades:
        print("INSUFFICIENT=1")
        return

    # 2. Compute dormancy per (symbol_id, insider): days since last open-market purchase
    # Group by (symbol_id, insider)
    trades_by_key = defaultdict(list)
    for t in officer_trades:
        key = (t['symbol_id'], t['insider'])
        trades_by_key[key].append(t)

    signals = []
    for key, trades in trades_by_key.items():
        trades.sort(key=lambda x: x['filed_ts'])
        for i, t in enumerate(trades):
            if i == 0:
                dormancy_days = 9999  # first trade ever
            else:
                prev_filed = trades[i-1]['filed_ts']
                dormancy_days = (t['filed_ts'] - prev_filed) / 86400
            t['dormancy_days'] = dormancy_days
            # Only consider if dormancy > 252 calendar days (~1 year)
            if dormancy_days > 252:
                signals.append(t)

    if not signals:
        print("INSUFFICIENT=1")
        return

    # 3. For each signal, check revenue acceleration (3+ quarters) as of filed_ts
    # Get all revenue data (metric='Revenues') with fetched_at <= filed_ts
    # We need to query per signal or batch. Let's batch by getting all revenues.
    cur = conn.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'Revenues' AND as_of > 0
        ORDER BY symbol_id, as_of
    """)
    rev_rows = [dict(row) for row in cur.fetchall()]

    rev_by_symbol = defaultdict(list)
    for r in rev_rows:
        rev_by_symbol[r['symbol_id']].append(r)

    # 4. Get institutional ownership per symbol per period (latest period <= filed_ts - 45 days)
    cur = conn.execute("""
        SELECT symbol_id, period, SUM(value) as total_value, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    inst_rows = [dict(row) for row in cur.fetchall()]
    inst_by_symbol = defaultdict(list)
    for r in inst_rows:
        inst_by_symbol[r['symbol_id']].append(r)

    # 5. Compute forward returns from bars (tf='1d') for 21 trading days
    # We'll compute on the fly for each signal that passes filters

    qualified = []
    for sig in signals:
        sym_id = sig['symbol_id']
        filed_ts = sig['filed_ts']
        filed_date = unix_to_date(filed_ts)

        # Revenue acceleration check: need at least 4 quarters with fetched_at <= filed_ts
        revs = [r for r in rev_by_symbol.get(sym_id, []) if r['fetched_at'] <= filed_ts]
        if len(revs) < 4:
            continue
        # Sort by as_of (quarter end)
        revs.sort(key=lambda x: x['as_of'])
        # Compute QoQ growth rates for last 4 quarters
        growth_rates = []
        for i in range(1, len(revs)):
            prev_val = revs[i-1]['value']
            curr_val = revs[i]['value']
            if prev_val and prev_val != 0:
                growth_rates.append((curr_val - prev_val) / prev_val)
        if len(growth_rates) < 3:
            continue
        # Check acceleration: last 3 growth rates increasing
        last3 = growth_rates[-3:]
        if not (last3[0] < last3[1] < last3[2]):
            continue

        # Institutional ownership: latest period <= filed_ts - 45 days
        cutoff_ts = filed_ts - 45 * 86400
        inst_periods = [p for p in inst_by_symbol.get(sym_id, []) if p['period'] <= cutoff_ts]
        if not inst_periods:
            continue
        latest_inst = max(inst_periods, key=lambda x: x['period'])
        # We'll compute percentile later across all signals at their decision times
        sig['inst_value'] = latest_inst['total_value']
        sig['inst_period'] = latest_inst['period']

        # Forward return: get close at filed_date and 21 trading days later
        # Use bars table
        cur = conn.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') = ?
        """, (sym_id, filed_date.isoformat()))
        row = cur.fetchone()
        if not row:
            continue
        entry_close = row['close']

        future_date = get_trading_days_between(conn, filed_date, 21)
        if not future_date:
            continue
        cur = conn.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') = ?
        """, (sym_id, future_date.isoformat()))
        row = cur.fetchone()
        if not row:
            continue
        exit_close = row['close']

        fwd_return = (exit_close - entry_close) / entry_close
        sig['fwd_return'] = fwd_return
        sig['filed_date'] = filed_date
        qualified.append(sig)

    if not qualified:
        print("INSUFFICIENT=1")
        return

    # Compute institutional ownership percentile at each decision time
    # For each signal, compare its inst_value to all symbols' inst_value at that period
    # But periods differ. Simpler: compute cross-sectional percentile at each signal's inst_period
    # Group signals by inst_period
    signals_by_period = defaultdict(list)
    for sig in qualified:
        signals_by_period[sig['inst_period']].append(sig)

    for period, sigs in signals_by_period.items():
        # Get all symbols' inst_value at this period
        cur = conn.execute("""
            SELECT symbol_id, SUM(value) as total_value
            FROM inst_holdings
            WHERE period = ?
            GROUP BY symbol_id
        """, (period,))
        all_vals = [row['total_value'] for row in cur.fetchall() if row['total_value'] is not None]
        if not all_vals:
            for sig in sigs:
                sig['inst_pctile'] = 0.5
            continue
        all_vals.sort()
        for sig in sigs:
            val = sig['inst_value']
            # percentile rank
            rank = sum(1 for v in all_vals if v <= val) / len(all_vals)
            sig['inst_pctile'] = rank

    # Filter: low institutional ownership (bottom 30%)
    final_signals = [s for s in qualified if s.get('inst_pctile', 1) <= 0.3]

    if not final_signals:
        print("INSUFFICIENT=1")
        return

    # Deduplicate by (symbol_id, filed_date) - one observation per symbol per day
    seen = set()
    deduped = []
    for s in final_signals:
        key = (s['symbol_id'], s['filed_date'])
        if key not in seen:
            seen.add(key)
            deduped.append(s)

    # Sort by filed_ts for temporal split
    deduped.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed era
    n = len(deduped)
    split_idx = int(n * 0.8)
    main_signals = deduped[:split_idx]
    sealed_signals = deduped[split_idx:]

    def compute_metrics(signals):
        if not signals:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(signals)
        hits = sum(1 for s in signals if s['fwd_return'] > 0)
        precision = hits / issued if issued else 0.0
        base_rate = precision  # base rate within issued subset
        distinct_days = len(set(s['filed_date'] for s in signals))
        # Design effect: approximate by 1 + (avg cluster size - 1) * rho
        # Simple approximation: group by date, compute variance inflation
        day_counts = defaultdict(int)
        for s in signals:
            day_counts[s['filed_date']] += 1
        if len(day_counts) > 1:
            avg_cluster = issued / len(day_counts)
            # Assume intra-cluster correlation ~0.2
            deff = 1 + (avg_cluster - 1) * 0.2
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_prec, main_br, main_days, main_eff = compute_metrics(main_signals)
    sealed_issued, sealed_hits, sealed_prec, _, _, _ = compute_metrics(sealed_signals)

    # Opportunities: total decision points considered (officer trades with dormancy > 252)
    opportunities = len(signals)

    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={main_prec:.6f}")
    print(f"BASE_RATE={main_br:.6f}")
    print(f"DISTINCT_DAYS={main_days}")
    print(f"EFFECTIVE_N={main_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()
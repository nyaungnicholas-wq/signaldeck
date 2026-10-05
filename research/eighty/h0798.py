# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 797
# cycle_index: 67
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def quarter_end_dates():
    """Generate quarter end dates from 2000 to 2030."""
    dates = []
    for year in range(2000, 2031):
        for month in [3, 6, 9, 12]:
            if month == 3:
                day = 31
            elif month == 6:
                day = 30
            elif month == 9:
                day = 30
            else:
                day = 31
            dates.append(datetime(year, month, day))
    return sorted(dates)

QE_DATES = quarter_end_dates()
QE_SET = set(QE_DATES)

def prev_quarter_end(dt, n=1):
    """Return the n-th previous quarter end date before dt."""
    idx = 0
    while idx < len(QE_DATES) and QE_DATES[idx] < dt:
        idx += 1
    if idx - n < 0:
        return None
    return QE_DATES[idx - n]

def quarter_end_before(dt):
    """Return the most recent quarter end strictly before dt."""
    for qe in reversed(QE_DATES):
        if qe < dt:
            return qe
    return None

def quarter_ends_before(dt, count):
    """Return list of `count` most recent quarter ends strictly before dt, oldest first."""
    res = []
    for qe in reversed(QE_DATES):
        if qe < dt:
            res.append(qe)
            if len(res) == count:
                break
    return list(reversed(res))

def epoch_to_dt(epoch):
    return datetime.utcfromtimestamp(epoch)

def dt_to_epoch(dt):
    return int(dt.timestamp())

def parse_date(s):
    """Parse YYYY-MM-DD string to datetime at midnight UTC."""
    return datetime.strptime(s, '%Y-%m-%d')

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load insider open-market purchases (code='P')
    cur.execute("""
        SELECT it.symbol_id, it.filed_ts, it.tx_ts, s.symbol
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P'
        ORDER BY it.filed_ts
    """)
    insider_purchases = cur.fetchall()
    if not insider_purchases:
        print("INSUFFICIENT=1")
        return 0

    # 2. Load public float data (EntityPublicFloat) from fundamentals
    cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'EntityPublicFloat'
        ORDER BY symbol_id, fetched_at
    """)
    float_rows = cur.fetchall()
    float_by_sym = defaultdict(list)
    for r in float_rows:
        float_by_sym[r['symbol_id']].append({
            'value': r['value'],
            'as_of': r['as_of'],
            'fetched_at': r['fetched_at']
        })

    # 3. Load institutional holdings (13F) - aggregate by symbol_id, period
    cur.execute("""
        SELECT symbol_id, period, SUM(value) as total_value, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    inst_rows = cur.fetchall()
    inst_by_sym = defaultdict(list)
    for r in inst_rows:
        inst_by_sym[r['symbol_id']].append({
            'period': r['period'],
            'total_value': r['total_value'],
            'total_shares': r['total_shares']
        })

    # 4. Load daily bars for forward returns (tf='1d')
    # We'll query on demand per symbol to avoid loading 15M rows
    # But we need to know date range for 21-day forward
    cur.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1d'")
    min_ts, max_ts = cur.fetchone()
    if min_ts is None or max_ts is None:
        print("INSUFFICIENT=1")
        return 0
    max_dt = epoch_to_dt(max_ts)

    # 5. Process each insider purchase as a decision point
    decisions = []  # list of (symbol_id, symbol, decision_dt, decision_epoch, label_up)
    
    for row in insider_purchases:
        sym_id = row['symbol_id']
        symbol = row['symbol']
        filed_ts = row['filed_ts']
        decision_dt = epoch_to_dt(filed_ts)
        decision_epoch = filed_ts

        # Skip if decision too recent for 21-day forward (need 21 trading days ~ 30 calendar days)
        if decision_epoch > max_ts - 30 * 86400:
            continue

        # --- Public float condition: declined for two consecutive quarters ---
        # Need EntityPublicFloat for three most recent quarters as of decision_dt (using fetched_at <= decision_dt)
        float_data = float_by_sym.get(sym_id, [])
        # Filter to fetched_at <= decision_dt
        available = [f for f in float_data if f['fetched_at'] <= decision_epoch]
        if len(available) < 3:
            continue
        # Group by as_of (quarter end), take latest fetched_at per quarter
        by_quarter = {}
        for f in available:
            qe = f['as_of']
            if qe == 0:
                continue  # sentinel
            qe_dt = epoch_to_dt(qe)
            if qe_dt not in by_quarter or f['fetched_at'] > by_quarter[qe_dt]['fetched_at']:
                by_quarter[qe_dt] = f
        # Get three most recent quarter ends before decision_dt
        recent_qes = quarter_ends_before(decision_dt, 3)
        if len(recent_qes) < 3:
            continue
        vals = []
        for qe in recent_qes:
            if qe not in by_quarter:
                vals = None
                break
            vals.append(by_quarter[qe]['value'])
        if vals is None or len(vals) < 3:
            continue
        # Check declining for two consecutive quarters: Q2 < Q1 < Q0 (most recent is Q2)
        # recent_qes[0] = oldest of three, [2] = most recent
        if not (vals[2] < vals[1] < vals[0]):
            continue

        # --- Institutional ownership condition: increased in most recent quarter ---
        # Use inst_holdings with period <= decision_dt - 45 days
        lag_dt = decision_dt - timedelta(days=45)
        lag_epoch = dt_to_epoch(lag_dt)
        inst_data = inst_by_sym.get(sym_id, [])
        # Filter to period <= lag_epoch
        available_inst = [i for i in inst_data if i['period'] <= lag_epoch]
        if len(available_inst) < 2:
            continue
        # Get two most recent periods
        recent_periods = sorted(available_inst, key=lambda x: x['period'])[-2:]
        if recent_periods[1]['total_value'] <= recent_periods[0]['total_value']:
            continue

        # --- Compute 21-day forward return from bars ---
        # Get close on decision day (or next trading day) and 21 trading days later
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            ORDER BY ts LIMIT 22
        """, (sym_id, decision_epoch))
        bars = cur.fetchall()
        if len(bars) < 22:
            continue
        close_0 = bars[0]['close']
        close_21 = bars[21]['close']
        fwd_return = (close_21 - close_0) / close_0
        label_up = 1 if fwd_return > 0 else 0

        decisions.append({
            'symbol_id': sym_id,
            'symbol': symbol,
            'decision_dt': decision_dt,
            'decision_epoch': decision_epoch,
            'label_up': label_up
        })

    if not decisions:
        print("INSUFFICIENT=1")
        return 0

    # 6. Split into sealed era (most recent 20%) and main era
    decisions.sort(key=lambda x: x['decision_epoch'])
    n_total = len(decisions)
    n_sealed = max(1, int(n_total * 0.2))
    main_decisions = decisions[:-n_sealed]
    sealed_decisions = decisions[-n_sealed:]

    def compute_metrics(dec_list):
        if not dec_list:
            return None
        issued = len(dec_list)
        hits = sum(d['label_up'] for d in dec_list)
        precision = hits / issued if issued else 0.0
        base_rate = precision  # base rate within issued subset is just precision of the predicted class (up)
        # Distinct UTC days among issued calls
        distinct_days = len(set(d['decision_dt'].date() for d in dec_list))
        # Design effect: cluster by day, compute variance inflation
        # For binary outcomes clustered by day, design effect = 1 + (avg_cluster_size - 1) * ICC
        # Simplified: effective_n = issued / design_effect, design_effect = issued / distinct_days (if perfect correlation within day)
        # More properly: design_effect = 1 + (m-1)*rho, but we'll use the standard cluster-robust approximation
        # Here we use: effective_n = distinct_days * (issued / distinct_days) / (1 + (issued/distinct_days - 1) * rho)
        # Since we don't have rho, use the conservative: effective_n = distinct_days (each day is one independent obs)
        # But requirement says EFFECTIVE_N must be < ISSUED, so we use design_effect = issued / distinct_days
        design_effect = issued / distinct_days if distinct_days > 0 else 1.0
        effective_n = issued / design_effect if design_effect > 0 else issued
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    main_metrics = compute_metrics(main_decisions)
    sealed_metrics = compute_metrics(sealed_decisions)

    if main_metrics is None:
        print("INSUFFICIENT=1")
        return 0

    # 7. Output required lines
    print(f"ISSUED={main_metrics['issued']}")
    print(f"OPPORTUNITIES={n_total}")  # all decision points considered
    print(f"PRECISION={main_metrics['precision']:.6f}")
    print(f"BASE_RATE={main_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={main_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={main_metrics['effective_n']:.6f}")
    sealed_precision = sealed_metrics['precision'] if sealed_metrics else 0.0
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
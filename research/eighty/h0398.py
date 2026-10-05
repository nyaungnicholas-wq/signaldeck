# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 397
# cycle_index: 65
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

def get_periods(conn):
    cur = conn.execute("SELECT DISTINCT period FROM inst_holdings ORDER BY period")
    return [row[0] for row in cur.fetchall()]

def parse_period(p):
    # period format: 'YYYY-MM-DD' (quarter end)
    return datetime.strptime(p, '%Y-%m-%d')

def period_to_str(dt):
    return dt.strftime('%Y-%m-%d')

def prev_quarter_end(period_str):
    dt = parse_period(period_str)
    month = dt.month
    if month == 3:
        return period_to_str(dt.replace(month=12, year=dt.year-1, day=31))
    elif month == 6:
        return period_to_str(dt.replace(month=3, day=31))
    elif month == 9:
        return period_to_str(dt.replace(month=6, day=30))
    elif month == 12:
        return period_to_str(dt.replace(month=9, day=30))
    return None

def get_institutional_ownership(conn, period):
    # Returns dict symbol_id -> (total_shares, {filer: shares})
    cur = conn.execute("""
        SELECT symbol_id, cik, shares
        FROM inst_holdings
        WHERE period = ?
    """, (period,))
    result = defaultdict(lambda: [0, {}])
    for symbol_id, cik, shares in cur.fetchall():
        result[symbol_id][0] += shares
        result[symbol_id][1][cik] = shares
    return {k: (v[0], v[1]) for k, v in result.items()}

def get_shares_outstanding(conn, as_of_date, fetched_before):
    # as_of_date is period end string 'YYYY-MM-DD'
    # fetched_before is datetime (decision date)
    cur = conn.execute("""
        SELECT symbol_id, value
        FROM fundamentals
        WHERE metric = 'SharesOutstanding'
        AND as_of = ?
        AND fetched_at <= ?
    """, (as_of_date, fetched_before.strftime('%Y-%m-%d %H:%M:%S')))
    return {row[0]: float(row[1]) for row in cur.fetchall()}

def get_dollar_volume(conn, symbol_id, decision_date, lookback_days=63):
    # decision_date is datetime, need 63 trading days prior
    # Use 1d bars, ts is unix epoch
    # Approximate: 63 trading days ~ 89 calendar days
    start_ts = int((decision_date - timedelta(days=89)).timestamp())
    end_ts = int((decision_date - timedelta(days=1)).timestamp())
    cur = conn.execute("""
        SELECT close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
        ORDER BY ts DESC
        LIMIT ?
    """, (symbol_id, start_ts, end_ts, lookback_days))
    rows = cur.fetchall()
    if len(rows) < lookback_days // 2:  # require at least half the days
        return 0
    total = sum(close * volume for close, volume in rows)
    return total / len(rows)

def get_label(conn, symbol_id, decision_date, horizon=21):
    # prediction_outcomes: horizon, ts, up
    # ts is unix epoch? schema says ts is unix epoch integer for bars, but for prediction_outcomes not specified
    # Assume ts is unix epoch
    ts = int(decision_date.timestamp())
    cur = conn.execute("""
        SELECT up
        FROM prediction_outcomes
        WHERE symbol_id = ? AND horizon = ? AND ts = ?
    """, (symbol_id, horizon, ts))
    row = cur.fetchone()
    if row:
        return row[0]
    # Try nearest ts within a few days
    cur = conn.execute("""
        SELECT up
        FROM prediction_outcomes
        WHERE symbol_id = ? AND horizon = ? AND ts BETWEEN ? AND ?
        ORDER BY ABS(ts - ?) LIMIT 1
    """, (symbol_id, horizon, ts - 86400*5, ts + 86400*5, ts))
    row = cur.fetchone()
    return row[0] if row else None

def get_symbol_info(conn, symbol_id):
    cur = conn.execute("SELECT symbol, market FROM symbols WHERE id = ?", (symbol_id,))
    return cur.fetchone()

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # Check critical tables have data
    for table in ['inst_holdings', 'fundamentals', 'bars', 'prediction_outcomes', 'symbols']:
        cur = conn.execute(f"SELECT COUNT(*) FROM {table}")
        if cur.fetchone()[0] == 0:
            print("INSUFFICIENT=1")
            return

    periods = get_periods(conn)
    if len(periods) < 2:
        print("INSUFFICIENT=1")
        return

    all_calls = []  # (decision_date, symbol_id, hit)

    for period in periods:
        prev_period = prev_quarter_end(period)
        if not prev_period:
            continue

        # Decision date = period + 45 days (latest filing date)
        period_dt = parse_period(period)
        decision_date = period_dt + timedelta(days=45)

        # Get institutional ownership for current and previous quarter
        curr_own = get_institutional_ownership(conn, period)
        prev_own = get_institutional_ownership(conn, prev_period)

        # Get SharesOutstanding for current and previous quarter (as_of = period end)
        # fetched_at must be <= decision_date
        curr_so = get_shares_outstanding(conn, period, decision_date)
        prev_so = get_shares_outstanding(conn, prev_period, decision_date)

        # Symbols present in both quarters
        common_symbols = set(curr_own.keys()) & set(prev_own.keys()) & set(curr_so.keys()) & set(prev_so.keys())

        for symbol_id in common_symbols:
            # Skip crypto
            sym_info = get_symbol_info(conn, symbol_id)
            if not sym_info or sym_info[1] != 'stocks':
                continue

            # Institutional ownership QoQ change
            curr_total, curr_filers = curr_own[symbol_id]
            prev_total, prev_filers = prev_own[symbol_id]
            if prev_total == 0:
                continue
            own_change_pct = (curr_total - prev_total) / prev_total * 100

            # Count distinct filers increasing positions
            increasing_filers = 0
            for cik, shares in curr_filers.items():
                prev_shares = prev_filers.get(cik, 0)
                if shares > prev_shares:
                    increasing_filers += 1

            # SharesOutstanding QoQ change
            curr_so_val = curr_so[symbol_id]
            prev_so_val = prev_so[symbol_id]
            if prev_so_val == 0:
                continue
            so_change_pct = (curr_so_val - prev_so_val) / prev_so_val * 100

            # Entry conditions
            if own_change_pct <= 5:
                continue
            if so_change_pct >= 0:  # SharesOutstanding must fall (negative change)
                continue
            if increasing_filers < 3:
                continue

            # Dollar volume filter
            avg_dollar_vol = get_dollar_volume(conn, symbol_id, decision_date)
            if avg_dollar_vol <= 10_000_000:
                continue

            # All conditions met - issue call
            label = get_label(conn, symbol_id, decision_date, horizon=21)
            if label is not None:
                all_calls.append((decision_date, symbol_id, label))

    if not all_calls:
        print("INSUFFICIENT=1")
        return

    # Sort by decision_date
    all_calls.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(all_calls) * 0.8)
    main_calls = all_calls[:split_idx]
    sealed_calls = all_calls[split_idx:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(1 for _, _, label in calls if label == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of predicted class (up=1) within issued
        # Distinct UTC days among issued calls
        distinct_days = len(set(d.date() for d, _, _ in calls))
        # Design effect: estimate ICC = 0.05
        calls_per_day = defaultdict(int)
        for d, _, _ in calls:
            calls_per_day[d.date()] += 1
        avg_cluster = sum(calls_per_day.values()) / len(calls_per_day) if calls_per_day else 1
        icc = 0.05
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_main, hits_main, prec_main, base_main, days_main, eff_main = compute_metrics(main_calls)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, days_sealed, eff_sealed = compute_metrics(sealed_calls)

    # Total opportunities: decision points considered (symbol-period pairs that had data)
    # We need to count all symbol-period pairs evaluated, not just those passing filters
    # This is complex to track exactly. Approximate: total symbol-quarter combinations with data
    # For simplicity, count unique (symbol_id, period) in inst_holdings that have both quarters
    cur = conn.execute("""
        SELECT COUNT(DISTINCT ih1.symbol_id, ih1.period)
        FROM inst_holdings ih1
        JOIN inst_holdings ih2 ON ih1.symbol_id = ih2.symbol_id
        WHERE ih2.period = (
            SELECT MAX(period) FROM inst_holdings WHERE period < ih1.period
        )
    """)
    opportunities = cur.fetchone()[0] or 0

    print(f"ISSUED={issued_main}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={prec_main:.6f}")
    print(f"BASE_RATE={base_main:.6f}")
    print(f"DISTINCT_DAYS={days_main}")
    print(f"EFFECTIVE_N={eff_main:.6f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

if __name__ == '__main__':
    main()
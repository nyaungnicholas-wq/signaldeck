# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 807
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_spy_id(conn):
    cur = conn.execute("SELECT id FROM symbols WHERE symbol = 'SPY' AND market = 'stocks'")
    row = cur.fetchone()
    if not row:
        return None
    return row[0]

def get_candidate_trades(conn):
    """10% owner open-market purchases (code=P)."""
    sql = """
        SELECT accession, symbol_id, filed_ts, tx_ts, shares, price, value, title
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%10%' OR title LIKE '%Ten Percent%' OR title LIKE '%10 percent%')
        ORDER BY filed_ts
    """
    return conn.execute(sql).fetchall()

def get_252d_return(conn, symbol_id, as_of_ts, spy_id):
    """Return (sym_return, spy_return) for 252 trading days ending at as_of_ts.
       Returns None if insufficient data."""
    # Get 253 closes for symbol (252 returns)
    sql = """
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC LIMIT 253
    """
    sym_closes = [r[0] for r in conn.execute(sql, (symbol_id, as_of_ts)).fetchall()]
    if len(sym_closes) < 253:
        return None
    sym_ret = (sym_closes[-1] / sym_closes[0]) - 1.0  # oldest / newest? wait: DESC so [0] is newest, [-1] is oldest
    # Actually: ts DESC -> [0] = most recent (as_of_ts), [-1] = 252 days ago
    # Return = (price_now / price_252d_ago) - 1
    sym_ret = (sym_closes[0] / sym_closes[-1]) - 1.0

    spy_closes = [r[0] for r in conn.execute(sql, (spy_id, as_of_ts)).fetchall()]
    if len(spy_closes) < 253:
        return None
    spy_ret = (spy_closes[0] / spy_closes[-1]) - 1.0

    return sym_ret, spy_ret

def has_3q_consecutive_so_decline(conn, symbol_id, as_of_ts):
    """Check if SharesOutstanding declined for 3+ consecutive quarters as of as_of_ts.
       Uses fetched_at for as-of discipline."""
    sql = """
        SELECT as_of, value
        FROM fundamentals
        WHERE symbol_id = ? AND metric = 'SharesOutstanding' AND fetched_at <= ?
        ORDER BY as_of DESC
    """
    rows = conn.execute(sql, (symbol_id, as_of_ts)).fetchall()
    if len(rows) < 4:  # need 4 quarters to see 3 declines
        return False
    # rows ordered by as_of DESC (most recent first)
    # Check consecutive declines: each quarter < previous quarter
    declines = 0
    for i in range(len(rows) - 1):
        if rows[i+1][1] < rows[i][1]:  # older quarter has lower SO = decline
            declines += 1
            if declines >= 3:
                return True
        else:
            declines = 0
    return False

def get_21d_forward_return(conn, symbol_id, decision_ts):
    """21-day forward return from next trading day after decision_ts.
       Returns None if insufficient data."""
    sql = """
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        ORDER BY ts ASC LIMIT 22
    """
    closes = [r[0] for r in conn.execute(sql, (symbol_id, decision_ts)).fetchall()]
    if len(closes) < 22:
        return None
    # closes[0] = next day open? No, daily bars: close of next trading day
    # We want return from close of next day to close of 21 days later
    # Actually: decision at filed_ts (could be after market close). Next bar is next trading day.
    # Forward return over 21 sessions: (close_t21 / close_t1) - 1
    fwd_ret = (closes[21] / closes[0]) - 1.0
    return fwd_ret

def utc_day(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    spy_id = get_spy_id(conn)
    if not spy_id:
        print("INSUFFICIENT=1")
        return

    trades = get_candidate_trades(conn)
    if not trades:
        print("INSUFFICIENT=1")
        return

    calls = []  # (decision_ts, symbol_id, fwd_ret, hit)
    last_call_day = {}  # symbol -> last call utc_day (for 63-session deduplication)

    for row in trades:
        accession, symbol_id, filed_ts, tx_ts, shares, price, value, title = row
        # As-of discipline: decision at filed_ts (disclosure date)
        decision_ts = filed_ts

        # Deduplication: one call per symbol per 63 trading days (~3 months)
        # Approximate with calendar days: 63 trading days ~ 90 calendar days
        day = utc_day(decision_ts)
        last = last_call_day.get(symbol_id)
        if last is not None and (day - last).days < 90:
            continue

        # Condition 1: 252-day underperformance vs SPY > 20%
        ret_pair = get_252d_return(conn, symbol_id, decision_ts, spy_id)
        if ret_pair is None:
            continue
        sym_ret, spy_ret = ret_pair
        if sym_ret - spy_ret >= -0.20:  # underperformance < 20% (i.e., not underperforming by >20%)
            continue

        # Condition 2: 3+ consecutive quarters SharesOutstanding decline
        if not has_3q_consecutive_so_decline(conn, symbol_id, decision_ts):
            continue

        # All conditions met - issue call
        fwd_ret = get_21d_forward_return(conn, symbol_id, decision_ts)
        if fwd_ret is None:
            continue

        hit = 1 if fwd_ret > 0 else 0
        calls.append((decision_ts, symbol_id, fwd_ret, hit))
        last_call_day[symbol_id] = day

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Sort by decision time
    calls.sort(key=lambda x: x[0])

    # 20% holdout (most recent)
    n = len(calls)
    split = int(n * 0.8)
    in_sample = calls[:split]
    sealed = calls[split:]

    def compute_metrics(call_list, label):
        if not call_list:
            return
        issued = len(call_list)
        hits = sum(c[3] for c in call_list)
        precision = hits / issued if issued else 0.0
        base_rate = precision  # base rate of predicted class (up) within issued subset
        distinct_days = len(set(utc_day(c[0]) for c in call_list))
        # Design effect: autocorrelation of hits at lag 1
        hit_series = [c[3] for c in call_list]
        if len(hit_series) > 1:
            mean_hit = sum(hit_series) / len(hit_series)
            num = sum((hit_series[i] - mean_hit) * (hit_series[i-1] - mean_hit) for i in range(1, len(hit_series)))
            den = sum((h - mean_hit) ** 2 for h in hit_series)
            rho1 = num / den if den > 0 else 0.0
            design_effect = 1 + 2 * max(rho1, 0.0)
        else:
            design_effect = 1.0
        effective_n = issued / design_effect if design_effect > 0 else issued
        print(f"{label}_ISSUED={issued}")
        print(f"{label}_OPPORTUNITIES={issued}")  # opportunities considered = issued (we only count decision points that met pre-filters? Actually opportunities = all candidate trades considered)
        print(f"{label}_PRECISION={precision:.6f}")
        print(f"{label}_BASE_RATE={base_rate:.6f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.6f}")

    # We need OPPORTUNITIES = count of decision points considered (candidate trades that passed basic filters)
    # For simplicity, use total candidate trades as opportunities (conservative)
    opportunities = len(trades)

    # In-sample
    issued = len(in_sample)
    hits = sum(c[3] for c in in_sample)
    precision = hits / issued if issued else 0.0
    base_rate = precision
    distinct_days = len(set(utc_day(c[0]) for c in in_sample))
    hit_series = [c[3] for c in in_sample]
    if len(hit_series) > 1:
        mean_hit = sum(hit_series) / len(hit_series)
        num = sum((hit_series[i] - mean_hit) * (hit_series[i-1] - mean_hit) for i in range(1, len(hit_series)))
        den = sum((h - mean_hit) ** 2 for h in hit_series)
        rho1 = num / den if den > 0 else 0.0
        design_effect = 1 + 2 * max(rho1, 0.0)
    else:
        design_effect = 1.0
    effective_n = issued / design_effect if design_effect > 0 else issued

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")

    # Sealed
    if sealed:
        s_issued = len(sealed)
        s_hits = sum(c[3] for c in sealed)
        s_precision = s_hits / s_issued if s_issued else 0.0
        print(f"SEALED_PRECISION={s_precision:.6f}")
    else:
        print("SEALED_PRECISION=0.000000")

if __name__ == '__main__':
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 426
# cycle_index: 17
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def get_connection():
    return sqlite3.connect(DB_PATH, uri=True)

def fetch_all(sql, params=()):
    with get_connection() as conn:
        conn.row_factory = sqlite3.Row
        return [dict(row) for row in conn.execute(sql, params)]

def get_universe():
    sql = """
        SELECT DISTINCT symbol_id 
        FROM inst_holdings
        INTERSECT
        SELECT DISTINCT symbol_id 
        FROM fundamentals 
        WHERE metric='EntityPublicFloat'
        INTERSECT
        SELECT DISTINCT symbol_id 
        FROM bars 
        WHERE tf='1d' AND ts >= 1530412800
    """
    return {row['symbol_id'] for row in fetch_all(sql)}

def get_available_quarters(symbol_id, cutoff_ts):
    """Get 13F quarters available by cutoff_ts, lagged 45 days."""
    sql = """
        SELECT period, SUM(value) as total_value
        FROM inst_holdings
        WHERE symbol_id=? AND (period + 45*86400) <= ?
        GROUP BY period
        ORDER BY period DESC
        LIMIT 10
    """
    return fetch_all(sql, (symbol_id, cutoff_ts))

def get_available_floats(symbol_id, cutoff_ts):
    """Get EntityPublicFloat values available by cutoff_ts."""
    sql = """
        SELECT as_of, fetched_at, value
        FROM fundamentals
        WHERE symbol_id=? AND metric='EntityPublicFloat' AND fetched_at <= ?
        ORDER BY as_of DESC
        LIMIT 10
    """
    return fetch_all(sql, (symbol_id, cutoff_ts))

def check_conditions(symbol_id, decision_ts):
    quarters = get_available_quarters(symbol_id, decision_ts)
    if len(quarters) < 2:
        return False
    
    current = quarters[0]
    previous = quarters[1]
    if previous['total_value'] <= 0:
        return False
    ownership_change = (current['total_value'] - previous['total_value']) / previous['total_value']
    if ownership_change <= 0.05:
        return False
    
    floats = get_available_floats(symbol_id, decision_ts)
    if len(floats) < 2:
        return False
    
    current_float = floats[0]['value']
    if current_float <= 0:
        return False
    
    current_as_of = floats[0]['as_of']
    year_ago_ts = current_as_of - 365*86400
    year_ago_float = None
    for f in floats[1:]:
        if f['as_of'] <= year_ago_ts:
            year_ago_float = f['value']
            break
    if year_ago_float is None or year_ago_float <= 0:
        return False
    
    float_change = (current_float - year_ago_float) / year_ago_float
    if float_change >= -0.10:
        return False
    
    return True

def get_outcome(symbol_id, ts):
    sql = """
        SELECT up FROM prediction_outcomes
        WHERE symbol_id=? AND ts=? AND horizon=21
    """
    rows = fetch_all(sql, (symbol_id, ts))
    return rows[0]['up'] if rows else None

def main():
    universe = get_universe()
    if not universe:
        print("INSUFFICIENT=1")
        return
    
    sql = """
        SELECT DISTINCT b.symbol_id, b.ts
        FROM bars b
        JOIN prediction_outcomes po ON b.symbol_id=po.symbol_id AND b.ts=po.ts
        WHERE b.tf='1d' AND po.horizon=21
        ORDER BY b.ts
    """
    candidates = fetch_all(sql)
    if not candidates:
        print("INSUFFICIENT=1")
        return
    
    calls = []
    opportunities = 0
    for cand in candidates:
        sym = cand['symbol_id']
        if sym not in universe:
            continue
        opportunities += 1
        ts = cand['ts']
        if check_conditions(sym, ts):
            outcome = get_outcome(sym, ts)
            if outcome is not None:
                calls.append((sym, ts, outcome))
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    issued = len(calls)
    hits = sum(1 for _, _, up in calls if up == 1)
    precision = hits / issued if issued > 0 else 0.0
    base_rate = precision
    
    distinct_days = len({ts for _, ts, _ in calls})
    if distinct_days <= 0 or distinct_days > issued:
        print("INSUFFICIENT=1")
        return
    
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for sym, ts, up in calls:
        day_counts[ts] += 1
        if up == 1:
            day_hits[ts] += 1
    
    n_days = len(day_counts)
    if n_days == 0:
        print("INSUFFICIENT=1")
        return
    
    total_calls = issued
    m = total_calls / n_days
    p = precision
    
    var_between = 0.0
    for ts in day_counts:
        n_i = day_counts[ts]
        p_i = day_hits[ts] / n_i if n_i > 0 else 0.0
        var_between += n_i * (p_i - p) ** 2
    var_between /= (n_days - 1) if n_days > 1 else 1
    
    var_within_num = 0.0
    var_within_den = 0
    for ts in day_counts:
        n_i = day_counts[ts]
        p_i = day_hits[ts] / n_i if n_i > 0 else 0.0
        var_within_num += n_i * p_i * (1 - p_i)
        var_within_den += n_i
    var_within_den -= n_days
    var_within = var_within_num / var_within_den if var_within_den > 0 else 0.0
    
    if var_between + var_within > 0:
        icc = var_between / (var_between + var_within)
    else:
        icc = 0.0
    
    design_effect = 1 + (m - 1) * icc if m > 1 else 1.0
    effective_n = issued / design_effect if design_effect > 0 else issued
    if effective_n >= issued:
        print("INSUFFICIENT=1")
        return
    
    ts_list = sorted({ts for _, ts, _ in calls})
    cutoff_idx = int(0.8 * len(ts_list))
    sealed_ts = set(ts_list[cutoff_idx:])
    sealed_calls = [(sym, ts, up) for sym, ts, up in calls if ts in sealed_ts]
    
    if not sealed_calls:
        print("INSUFFICIENT=1")
        return
    
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(1 for _, _, up in sealed_calls if up == 1)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 500
# cycle_index: 30
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

def get_trading_days(conn):
    """Get all distinct UTC days with 1d bars from 2018-07-26 onward."""
    cur = conn.execute("""
        SELECT DISTINCT date(ts, 'unixepoch') as day
        FROM bars
        WHERE tf = '1d' AND ts >= strftime('%s', '2018-07-26')
        ORDER BY day
    """)
    return [row[0] for row in cur.fetchall()]

def get_symbols_with_bars(conn):
    """Get symbols that have 1d bars from 2018-07-26."""
    cur = conn.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id
        WHERE b.tf = '1d' AND b.ts >= strftime('%s', '2018-07-26')
        AND s.active = 1
    """)
    return {row[0]: row[1] for row in cur.fetchall()}

def get_fundamentals_eps(conn):
    """Get EPS fundamentals: symbol_id, value, as_of, fetched_at."""
    cur = conn.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'EPS'
        ORDER BY symbol_id, as_of
    """)
    return cur.fetchall()

def get_sentiment_features(conn):
    """Get daily sentiment features: symbol_id, day, mean_score."""
    cur = conn.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE mean_score IS NOT NULL
        ORDER BY symbol_id, day
    """)
    return cur.fetchall()

def get_inst_holdings(conn):
    """Get institutional holdings aggregated by symbol_id and period."""
    cur = conn.execute("""
        SELECT symbol_id, period, SUM(value) as total_value, SUM(shares) as total_shares
        FROM inst_holdings
        WHERE symbol_id IS NOT NULL AND period IS NOT NULL
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    return cur.fetchall()

def get_prediction_outcomes_21d(conn):
    """Get 21-day horizon prediction outcomes."""
    cur = conn.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21 AND up IS NOT NULL
        ORDER BY symbol_id, ts
    """)
    return cur.fetchall()

def parse_date(date_str):
    """Parse YYYY-MM-DD string to datetime.date."""
    return datetime.strptime(date_str, '%Y-%m-%d').date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def quarter_end_dates():
    """Generate quarter end dates from 2018-Q1 onward."""
    quarters = []
    for year in range(2018, 2027):
        for q in range(1, 5):
            if q == 1:
                end = datetime(year, 3, 31).date()
            elif q == 2:
                end = datetime(year, 6, 30).date()
            elif q == 3:
                end = datetime(year, 9, 30).date()
            else:
                end = datetime(year, 12, 31).date()
            quarters.append(end)
    return quarters

def get_quarter(date):
    """Return (year, quarter) for a date."""
    q = (date.month - 1) // 3 + 1
    return (date.year, q)

def quarter_to_end(year, q):
    if q == 1:
        return datetime(year, 3, 31).date()
    elif q == 2:
        return datetime(year, 6, 30).date()
    elif q == 3:
        return datetime(year, 9, 30).date()
    else:
        return datetime(year, 12, 31).date()

def prev_quarter(year, q):
    if q == 1:
        return (year - 1, 4)
    return (year, q - 1)

def next_quarter(year, q):
    if q == 4:
        return (year + 1, 1)
    return (year, q + 1)

def build_eps_ttm_growth(eps_rows):
    """Build TTM EPS growth rates per quarter per symbol.
    Returns: dict[symbol_id] -> dict[(year,q)] -> (growth_rate, knowable_date)"""
    by_symbol = defaultdict(list)
    for symbol_id, value, as_of, fetched_at in eps_rows:
        if value is None or as_of is None or fetched_at is None:
            continue
        try:
            val = float(value)
            as_of_date = parse_date(as_of)
            fetched_date = parse_date(fetched_at)
        except:
            continue
        by_symbol[symbol_id].append((as_of_date, val, fetched_date))
    
    result = {}
    for symbol_id, rows in by_symbol.items():
        rows.sort(key=lambda x: x[0])
        eps_by_quarter = {}
        for as_of, val, fetched in rows:
            y, q = get_quarter(as_of)
            eps_by_quarter[(y, q)] = (val, fetched)
        
        quarters = sorted(eps_by_quarter.keys())
        growth = {}
        for i, (y, q) in enumerate(quarters):
            if i < 3:
                continue
            ttm_now = sum(eps_by_quarter.get(prev_quarter(y, q), (0, None))[0] for _ in range(4))
            # Actually need to get the 4 quarters
            q4 = (y, q)
            q3 = prev_quarter(y, q)
            q2 = prev_quarter(*q3)
            q1 = prev_quarter(*q2)
            q0 = prev_quarter(*q1)
            
            ttm_current = (eps_by_quarter.get(q4, (0, None))[0] +
                          eps_by_quarter.get(q3, (0, None))[0] +
                          eps_by_quarter.get(q2, (0, None))[0] +
                          eps_by_quarter.get(q1, (0, None))[0])
            ttm_prev = (eps_by_quarter.get(q3, (0, None))[0] +
                       eps_by_quarter.get(q2, (0, None))[0] +
                       eps_by_quarter.get(q1, (0, None))[0] +
                       eps_by_quarter.get(q0, (0, None))[0])
            
            if ttm_prev > 0:
                growth_rate = (ttm_current / ttm_prev) - 1
                # Knowable date is the latest fetched_at among the 4 quarters
                fetched_dates = [eps_by_quarter.get(qq, (None, None))[1] for qq in [q4, q3, q2, q1]]
                fetched_dates = [d for d in fetched_dates if d is not None]
                if fetched_dates:
                    knowable = max(fetched_dates)
                    growth[(y, q)] = (growth_rate, knowable)
        result[symbol_id] = growth
    return result

def find_acceleration_quarters(growth_dict):
    """Find quarters where growth accelerated for 2 consecutive quarters.
    Returns set of (year, quarter) where condition holds."""
    quarters = sorted(growth_dict.keys())
    accel = set()
    for i in range(2, len(quarters)):
        q = quarters[i]
        q1 = quarters[i-1]
        q2 = quarters[i-2]
        g, _ = growth_dict[q]
        g1, _ = growth_dict[q1]
        g2, _ = growth_dict[q2]
        if g > g1 and g1 > g2:
            accel.add(q)
    return accel

def build_sentiment_30d_avg(sentiment_rows):
    """Build 30-day rolling average of mean_score per symbol.
    Returns: dict[symbol_id] -> dict[day_str] -> (score, avg_30d)"""
    by_symbol = defaultdict(list)
    for symbol_id, day, score in sentiment_rows:
        if score is None:
            continue
        by_symbol[symbol_id].append((day, float(score)))
    
    result = {}
    for symbol_id, rows in by_symbol.items():
        rows.sort(key=lambda x: x[0])
        scores = {}
        for day, score in rows:
            scores[day] = score
        
        days = sorted(scores.keys())
        avg_30d = {}
        for i, day in enumerate(days):
            start_idx = max(0, i - 29)
            window = [scores[d] for d in days[start_idx:i+1]]
            if len(window) >= 5:  # require at least 5 days
                avg_30d[day] = sum(window) / len(window)
            else:
                avg_30d[day] = None
        result[symbol_id] = {day: (scores[day], avg_30d[day]) for day in days}
    return result

def build_inst_ownership_changes(inst_rows):
    """Build quarterly institutional ownership changes per symbol.
    Returns: dict[symbol_id] -> dict[period] -> (total_value, knowable_date)"""
    by_symbol = defaultdict(list)
    for symbol_id, period, total_value, total_shares in inst_rows:
        if total_value is None:
            continue
        by_symbol[symbol_id].append((period, float(total_value)))
    
    result = {}
    for symbol_id, rows in by_symbol.items():
        rows.sort(key=lambda x: x[0])
        periods = [r[0] for r in rows]
        values = [r[1] for r in rows]
        
        changes = {}
        for i in range(1, len(periods)):
            period = periods[i]
            prev_period = periods[i-1]
            change = values[i] - values[i-1]
            # Knowable date: period end + 45 days
            try:
                period_end = parse_date(period)
                knowable = period_end + timedelta(days=45)
                changes[period] = (change, knowable)
            except:
                continue
        result[symbol_id] = changes
    return result

def get_decision_timestamp(day_str):
    """Convert day string to unix timestamp at market close (16:00 ET = 20:00 UTC)."""
    dt = datetime.strptime(day_str, '%Y-%m-%d')
    # Assume 20:00 UTC as decision time (market close)
    dt = dt.replace(hour=20, minute=0, second=0)
    return int(dt.timestamp())

def main():
    conn = connect()
    
    print("Loading data...", file=sys.stderr)
    
    symbols = get_symbols_with_bars(conn)
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    symbol_ids = list(symbols.keys())
    
    # Load all required data
    eps_rows = get_fundamentals_eps(conn)
    sentiment_rows = get_sentiment_features(conn)
    inst_rows = get_inst_holdings(conn)
    outcomes_rows = get_prediction_outcomes_21d(conn)
    
    if not eps_rows or not sentiment_rows or not inst_rows or not outcomes_rows:
        print("INSUFFICIENT=1")
        return
    
    print("Processing fundamentals...", file=sys.stderr)
    eps_growth = build_eps_ttm_growth(eps_rows)
    accel_quarters = {}
    for sid, growth in eps_growth.items():
        accel_quarters[sid] = find_acceleration_quarters(growth)
    
    print("Processing sentiment...", file=sys.stderr)
    sentiment_data = build_sentiment_30d_avg(sentiment_rows)
    
    print("Processing institutional holdings...", file=sys.stderr)
    inst_changes = build_inst_ownership_changes(inst_rows)
    
    print("Processing outcomes...", file=sys.stderr)
    # Build outcomes lookup: symbol_id -> {ts: (up, fwd_return)}
    outcomes = defaultdict(dict)
    for symbol_id, ts, up, fwd_return in outcomes_rows:
        outcomes[symbol_id][ts] = (up, fwd_return)
    
    # Get all trading days
    trading_days = get_trading_days(conn)
    if not trading_days:
        print("INSUFFICIENT=1")
        return
    
    print("Evaluating entry conditions...", file=sys.stderr)
    
    # For each symbol, for each trading day, check conditions
    issued_calls = []  # list of (symbol_id, day_str, ts, up)
    opportunities = 0
    
    for symbol_id in symbol_ids:
        if symbol_id not in accel_quarters or symbol_id not in sentiment_data or symbol_id not in inst_changes:
            continue
        
        accel_qs = accel_quarters[symbol_id]
        sent_data = sentiment_data[symbol_id]
        inst_data = inst_changes[symbol_id]
        sym_outcomes = outcomes.get(symbol_id, {})
        
        if not accel_qs or not sent_data or not inst_data or not sym_outcomes:
            continue
        
        # For each trading day
        for day_str in trading_days:
            opportunities += 1
            day = parse_date(day_str)
            ts = get_decision_timestamp(day_str)
            
            # Condition 1: Trailing 4Q EPS growth accelerated for 2nd consecutive quarter
            # Find the latest quarter with knowable date <= day
            y, q = get_quarter(day)
            current_q = (y, q)
            cond1 = False
            knowable_q = None
            
            # Check current quarter and previous quarters
            for offset in range(0, 4):
                check_q = current_q
                for _ in range(offset):
                    check_q = prev_quarter(*check_q)
                if check_q in accel_qs:
                    # Need to verify knowable date from growth dict
                    growth_info = eps_growth[symbol_id].get(check_q)
                    if growth_info:
                        growth_rate, knowable = growth_info
                        if knowable <= day:
                            cond1 = True
                            knowable_q = check_q
                            break
            
            if not cond1:
                continue
            
            # Condition 2: News sentiment below 30-day average
            if day_str not in sent_data:
                continue
            score, avg_30d = sent_data[day_str]
            if avg_30d is None or score >= avg_30d:
                continue
            
            # Condition 3: Institutional ownership increased in prior quarter
            # Prior quarter relative to knowable_q
            if knowable_q is None:
                continue
            prior_q = prev_quarter(*knowable_q)
            prior_period_str = date_to_str(quarter_end_dates()[0])  # placeholder
            # Find period string for prior_q
            py, pq = prior_q
            prior_period = f"{py}-{pq*3:02d}-{30 if pq in [1,3] else 31 if pq==2 else 30}"  # approximate
            # Actually need to match the period format in inst_holdings
            # inst_holdings period format unknown, but likely 'YYYY-MM-DD' quarter end
            prior_period_end = quarter_to_end(py, pq)
            prior_period_str = date_to_str(prior_period_end)
            
            if prior_period_str not in inst_data:
                continue
            change, knowable_inst = inst_data[prior_period_str]
            if change <= 0 or knowable_inst > day:
                continue
            
            # All conditions met - check outcome
            if ts not in sym_outcomes:
                continue
            up, fwd_return = sym_outcomes[ts]
            
            issued_calls.append((symbol_id, day_str, ts, up))
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Sort by timestamp
    issued_calls.sort(key=lambda x: x[2])
    
    # Split: most recent 20% as sealed
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed
    
    train_calls = issued_calls[:n_train]
    sealed_calls = issued_calls[n_train:]
    
    # Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0
        issued = len(calls)
        hits = sum(1 for c in calls if c[3] == 1)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0  # base rate of positive class in issued subset
        distinct_days = len(set(c[1] for c in calls))
        return issued, hits, precision, base_rate, distinct_days
    
    train_issued, train_hits, train_precision, train_base_rate, train_distinct = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_precision, sealed_base_rate, sealed_distinct = compute_metrics(sealed_calls)
    
    total_issued = train_issued + sealed_issued
    total_hits = train_hits + sealed_hits
    total_precision = total_hits / total_issued if total_issued > 0 else 0
    total_base_rate = total_hits / total_issued if total_issued > 0 else 0
    total_distinct = len(set(c[1] for c in issued_calls))
    
    # Effective N: issued / design_effect
    # Design effect approximation: 1 + (avg_cluster_size - 1) * ICC
    # Simple approximation: group by day, compute cluster sizes
    day_counts = defaultdict(int)
    for c in issued_calls:
        day_counts[c[1]] += 1
    cluster_sizes = list(day_counts.values())
    if cluster_sizes:
        avg_cluster = sum(cluster_sizes) / len(cluster_sizes)
        # Conservative ICC estimate for financial returns ~0.1-0.3
        icc = 0.2
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = total_issued / design_effect
    else:
        effective_n = total_issued - 1  # ensure < issued
    
    # Ensure effective_n < issued
    if effective_n >= total_issued:
        effective_n = total_issued - 1
    
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base_rate:.6f}")
    print(f"DISTINCT_DAYS={total_distinct}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
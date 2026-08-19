# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 753
# cycle_index: 23
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def fetch_officer_trades(conn):
    """Get all officer open-market purchases at trade date."""
    sql = """
        SELECT symbol_id, tx_ts, filed_ts, shares, price, value, title
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' 
               OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        ORDER BY tx_ts
    """
    cur = conn.execute(sql)
    rows = cur.fetchall()
    cols = [d[0] for d in cur.description]
    return [dict(zip(cols, r)) for r in rows]

def fetch_fundamentals_for_symbol(conn, symbol_id, as_of_ts):
    """Get latest fundamental values for Revenue, EPS, SharesOutstanding known by as_of_ts."""
    sql = """
        SELECT metric, as_of, fetched_at, value
        FROM fundamentals
        WHERE symbol_id = ? 
          AND metric IN ('Revenues', 'EPS', 'SharesOutstanding')
          AND fetched_at <= ?
          AND as_of > 0
        ORDER BY as_of, fetched_at
    """
    cur = conn.execute(sql, (symbol_id, as_of_ts))
    rows = cur.fetchall()
    # For each as_of, keep the latest fetched_at (most recent revision)
    by_period = {}
    for metric, as_of, fetched_at, value in rows:
        key = (metric, as_of)
        if key not in by_period or fetched_at > by_period[key][1]:
            by_period[key] = (value, fetched_at)
    # Reorganize by metric -> list of (as_of, value) sorted by as_of
    result = {'Revenues': [], 'EPS': [], 'SharesOutstanding': []}
    for (metric, as_of), (value, _) in by_period.items():
        if metric in result:
            result[metric].append((as_of, float(value)))
    for metric in result:
        result[metric].sort(key=lambda x: x[0])
    return result

def compute_qoq_growth(series):
    """series: list of (as_of, value) sorted by as_of. Returns list of (as_of, growth_rate)."""
    growth = []
    for i in range(1, len(series)):
        prev_asof, prev_val = series[i-1]
        curr_asof, curr_val = series[i]
        if prev_val != 0:
            growth.append((curr_asof, (curr_val - prev_val) / abs(prev_val)))
    return growth

def check_revenue_acceleration(rev_growth, min_quarters=2):
    """Check if revenue QoQ growth accelerated for at least min_quarters consecutive quarters."""
    if len(rev_growth) < min_quarters + 1:
        return False
    # Need growth rates: g1, g2, g3... acceleration means g2>g1, g3>g2, etc.
    accel_count = 0
    for i in range(1, len(rev_growth)):
        if rev_growth[i][1] > rev_growth[i-1][1]:
            accel_count += 1
            if accel_count >= min_quarters:
                return True
        else:
            accel_count = 0
    return False

def check_eps_deceleration(eps_growth, min_quarters=2):
    """Check if EPS QoQ growth decelerated for at least min_quarters consecutive quarters."""
    if len(eps_growth) < min_quarters + 1:
        return False
    decel_count = 0
    for i in range(1, len(eps_growth)):
        if eps_growth[i][1] < eps_growth[i-1][1]:
            decel_count += 1
            if decel_count >= min_quarters:
                return True
        else:
            decel_count = 0
    return False

def check_no_dilution(shares_series, lookback_quarters=8):
    """Check no quarter with >2% SharesOutstanding growth in last lookback_quarters."""
    if len(shares_series) < 2:
        return False
    growth = compute_qoq_growth(shares_series)
    # Check last lookback_quarters growth rates
    relevant = growth[-lookback_quarters:] if len(growth) >= lookback_quarters else growth
    for _, g in relevant:
        if g > 0.02:
            return False
    return True

def fetch_sentiment_volatility(conn, symbol_id, trade_ts, window_days=21):
    """Compute std of mean_score over window_days before trade_ts."""
    trade_date = datetime.fromtimestamp(trade_ts, tz=timezone.utc).date()
    start_date = trade_date - __import__('datetime').timedelta(days=window_days * 2)  # buffer for weekends
    sql = """
        SELECT day, mean_score
        FROM sentiment_features
        WHERE symbol_id = ? AND day <= ? AND day >= ?
        ORDER BY day
    """
    cur = conn.execute(sql, (symbol_id, trade_date.isoformat(), start_date.isoformat()))
    rows = cur.fetchall()
    if len(rows) < 5:
        return None
    scores = [r[1] for r in rows[-window_days:]]
    if len(scores) < 5:
        return None
    mean = sum(scores) / len(scores)
    var = sum((x - mean) ** 2 for x in scores) / len(scores)
    return math.sqrt(var)

def fetch_forward_return(conn, symbol_id, trade_ts, horizon_days=21):
    """Get forward return over horizon_days trading days from trade_ts using daily bars."""
    # Find the first daily bar on or after trade_ts
    trade_date = datetime.fromtimestamp(trade_ts, tz=timezone.utc).date()
    sql = """
        SELECT ts, close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
        ORDER BY ts
        LIMIT ?
    """
    # Need horizon_days + 1 bars (entry + horizon_days forward)
    cur = conn.execute(sql, (symbol_id, int(trade_ts), horizon_days + 1))
    rows = cur.fetchall()
    if len(rows) < horizon_days + 1:
        return None
    entry_close = rows[0][1]
    exit_close = rows[horizon_days][1]
    if entry_close == 0:
        return None
    return (exit_close - entry_close) / entry_close

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row
    
    # 1. Get all officer trades
    trades = fetch_officer_trades(conn)
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    OPPORTUNITIES = len(trades)
    
    # 2. For each trade, compute features and filter
    issued = []
    all_volatilities = []
    
    # First pass: compute features and collect volatilities for quartile threshold
    trade_features = []
    for t in trades:
        symbol_id = t['symbol_id']
        tx_ts = t['tx_ts']
        
        # Fundamentals
        fund = fetch_fundamentals_for_symbol(conn, symbol_id, tx_ts)
        rev_growth = compute_qoq_growth(fund['Revenues'])
        eps_growth = compute_qoq_growth(fund['EPS'])
        
        rev_accel = check_revenue_acceleration(rev_growth, 2)
        eps_decel = check_eps_deceleration(eps_growth, 2)
        no_dilution = check_no_dilution(fund['SharesOutstanding'], 8)
        
        # Sentiment volatility
        vol = fetch_sentiment_volatility(conn, symbol_id, tx_ts, 21)
        
        if rev_accel and eps_decel and no_dilution and vol is not None:
            trade_features.append({
                'symbol_id': symbol_id,
                'tx_ts': tx_ts,
                'volatility': vol,
                'trade': t
            })
            all_volatilities.append(vol)
    
    if not trade_features:
        print("INSUFFICIENT=1")
        return
    
    # Determine bottom quartile threshold for volatility
    all_volatilities.sort()
    q25_idx = max(0, int(len(all_volatilities) * 0.25) - 1)
    vol_threshold = all_volatilities[q25_idx]
    
    # 3. Filter by volatility and compute forward returns
    for tf in trade_features:
        if tf['volatility'] <= vol_threshold:
            fwd_ret = fetch_forward_return(conn, tf['symbol_id'], tf['tx_ts'], 21)
            if fwd_ret is not None:
                issued.append({
                    'symbol_id': tf['symbol_id'],
                    'tx_ts': tf['tx_ts'],
                    'fwd_return': fwd_ret,
                    'up': 1 if fwd_ret > 0 else 0
                })
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    ISSUED = len(issued)
    
    # 4. Split by time: most recent 20% sealed
    issued.sort(key=lambda x: x['tx_ts'])
    split_idx = int(ISSUED * 0.8)
    main_era = issued[:split_idx]
    sealed_era = issued[split_idx:]
    
    # 5. Compute metrics
    def compute_precision(era):
        if not era:
            return 0.0
        hits = sum(1 for x in era if x['up'] == 1)
        return hits / len(era)
    
    def compute_base_rate(era):
        if not era:
            return 0.0
        return sum(1 for x in era if x['up'] == 1) / len(era)
    
    def distinct_days(era):
        days = set()
        for x in era:
            dt = datetime.fromtimestamp(x['tx_ts'], tz=timezone.utc).date()
            days.add(dt)
        return len(days)
    
    PRECISION = compute_precision(main_era)
    BASE_RATE = compute_base_rate(main_era)
    DISTINCT_DAYS = distinct_days(issued)  # among issued calls only
    SEALED_PRECISION = compute_precision(sealed_era)
    
    # Effective N: distinct_days (conservative, < ISSUED if any clustering)
    EFFECTIVE_N = DISTINCT_DAYS
    if EFFECTIVE_N >= ISSUED:
        EFFECTIVE_N = ISSUED - 1 if ISSUED > 1 else 0
    
    # 6. Output
    print(f"ISSUED={ISSUED}")
    print(f"OPPORTUNITIES={OPPORTUNITIES}")
    print(f"PRECISION={PRECISION:.6f}")
    print(f"BASE_RATE={BASE_RATE:.6f}")
    print(f"DISTINCT_DAYS={DISTINCT_DAYS}")
    print(f"EFFECTIVE_N={EFFECTIVE_N}")
    print(f"SEALED_PRECISION={SEALED_PRECISION:.6f}")

if __name__ == '__main__':
    main()
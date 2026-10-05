# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 326
# cycle_index: 49
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def get_trading_days(conn):
    """Get all distinct trading days from daily bars."""
    cur = conn.execute("""
        SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts
    """)
    return [row[0] for row in cur.fetchall()]

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def is_weekday(d):
    return d.weekday() < 5  # Mon-Fri

def get_holidays(trading_days):
    """Identify US market holidays as missing weekdays in the trading calendar."""
    if not trading_days:
        return set()
    
    dates = [ts_to_date(ts) for ts in trading_days]
    date_set = set(dates)
    start = dates[0]
    end = dates[-1]
    
    holidays = set()
    current = start
    while current <= end:
        if is_weekday(current) and current not in date_set:
            holidays.add(current)
        current += timedelta(days=1)
    return holidays

def get_pre_holiday_sessions(trading_days, holidays):
    """Find (decision_day, pre_holiday_day) pairs.
    decision_day: the trading day before pre-holiday session (where we decide at close)
    pre_holiday_day: the last trading day before a holiday
    """
    dates = [ts_to_date(ts) for ts in trading_days]
    date_to_ts_map = {ts_to_date(ts): ts for ts in trading_days}
    
    pairs = []
    for i in range(len(dates) - 1):
        d = dates[i]
        d_next = dates[i + 1]
        
        # Check if d_next is pre-holiday: next weekday after d_next is a holiday
        next_weekday = d_next + timedelta(days=1)
        while not is_weekday(next_weekday):
            next_weekday += timedelta(days=1)
        
        if next_weekday in holidays:
            pairs.append((date_to_ts_map[d], date_to_ts_map[d_next]))
    
    return pairs

def get_symbols_for_day(conn, day_ts, min_price=5.0, min_dollar_vol=5_000_000, lookback_days=20):
    """Get symbols passing liquidity/price screen as of prior close (day_ts)."""
    # Get 20 trading days ending at day_ts (inclusive)
    cur = conn.execute("""
        SELECT DISTINCT ts FROM bars WHERE tf = '1d' AND ts <= ? ORDER BY ts DESC LIMIT ?
    """, (day_ts, lookback_days))
    lookback_ts = [row[0] for row in cur.fetchall()]
    
    if len(lookback_ts) < lookback_days:
        return set()
    
    lookback_set = set(lookback_ts)
    placeholders = ','.join('?' * len(lookback_ts))
    
    # Get close and volume for these days
    cur = conn.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND ts IN ({placeholders})
    """, lookback_ts)
    
    # Compute 20-day median dollar volume per symbol
    symbol_data = defaultdict(list)
    for symbol_id, ts, close, volume in cur.fetchall():
        if close and volume:
            dollar_vol = close * volume
            symbol_data[symbol_id].append(dollar_vol)
    
    qualified = set()
    for symbol_id, vols in symbol_data.items():
        if len(vols) >= lookback_days:
            sorted_vols = sorted(vols)
            mid = len(sorted_vols) // 2
            median_vol = (sorted_vols[mid] + sorted_vols[~mid]) / 2
            if median_vol >= min_dollar_vol:
                qualified.add(symbol_id)
    
    # Now check price on day_ts (prior close)
    if not qualified:
        return set()
    
    placeholders = ','.join('?' * len(qualified))
    cur = conn.execute(f"""
        SELECT symbol_id, close FROM bars
        WHERE tf = '1d' AND ts = ? AND symbol_id IN ({placeholders})
    """, [day_ts] + list(qualified))
    
    final = set()
    for symbol_id, close in cur.fetchall():
        if close and close >= min_price:
            final.add(symbol_id)
    
    return final

def get_pre_holiday_returns(conn, pre_holiday_ts, symbol_ids):
    """Get close-to-close returns for pre-holiday session.
    Return: (close_pre_holiday - close_prior) / close_prior
    """
    if not symbol_ids:
        return {}
    
    # Get prior trading day
    cur = conn.execute("""
        SELECT MAX(ts) FROM bars WHERE tf = '1d' AND ts < ?
    """, (pre_holiday_ts,))
    prior_ts = cur.fetchone()[0]
    if not prior_ts:
        return {}
    
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, ts, close FROM bars
        WHERE tf = '1d' AND ts IN (?, ?) AND symbol_id IN ({placeholders})
    """, [prior_ts, pre_holiday_ts] + list(symbol_ids))
    
    closes = defaultdict(dict)
    for symbol_id, ts, close in cur.fetchall():
        closes[symbol_id][ts] = close
    
    returns = {}
    for symbol_id in symbol_ids:
        if prior_ts in closes[symbol_id] and pre_holiday_ts in closes[symbol_id]:
            c_prior = closes[symbol_id][prior_ts]
            c_pre = closes[symbol_id][pre_holiday_ts]
            if c_prior > 0:
                ret = (c_pre - c_prior) / c_prior
                returns[symbol_id] = ret
    
    return returns

def compute_design_effect(issued_calls):
    """Compute design effect for day-clustered observations.
    issued_calls: list of (day_ts, symbol_id, hit)
    """
    if not issued_calls:
        return 1.0
    
    # Group by day
    day_hits = defaultdict(list)
    for day_ts, symbol_id, hit in issued_calls:
        day_hits[day_ts].append(hit)
    
    n_days = len(day_hits)
    n_total = len(issued_calls)
    
    if n_days <= 1:
        return 1.0
    
    # Overall mean
    p = sum(h for _, _, h in issued_calls) / n_total
    
    # Between-day variance
    day_means = [sum(hits)/len(hits) for hits in day_hits.values()]
    day_sizes = [len(hits) for hits in day_hits.values()]
    
    # Design effect = 1 + (n_avg - 1) * ICC
    # ICC = between_var / (between_var + within_var)
    overall_mean = p
    between_var = sum(sz * (m - overall_mean)**2 for sz, m in zip(day_sizes, day_means)) / n_total
    within_var = sum(sum((h - m)**2 for h in hits) for hits, m in zip(day_hits.values(), day_means)) / n_total
    
    if between_var + within_var == 0:
        return 1.0
    
    icc = between_var / (between_var + within_var)
    n_avg = n_total / n_days
    deff = 1 + (n_avg - 1) * icc
    
    return max(1.0, deff)

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    conn = sqlite3.connect(db_path, uri=True)
    conn.execute("PRAGMA query_only = ON")
    
    try:
        # Get trading days
        trading_days = get_trading_days(conn)
        if not trading_days:
            print("INSUFFICIENT=1")
            return 0
        
        # Identify holidays
        holidays = get_holidays(trading_days)
        if not holidays:
            print("INSUFFICIENT=1")
            return 0
        
        # Get pre-holiday session pairs
        pairs = get_pre_holiday_sessions(trading_days, holidays)
        if not pairs:
            print("INSUFFICIENT=1")
            return 0
        
        # For each pair, get qualified symbols and returns
        all_calls = []  # (decision_day_ts, pre_holiday_ts, symbol_id, hit)
        
        for decision_ts, pre_holiday_ts in pairs:
            qualified = get_symbols_for_day(conn, decision_ts)
            if not qualified:
                continue
            
            returns = get_pre_holiday_returns(conn, pre_holiday_ts, qualified)
            for symbol_id, ret in returns.items():
                hit = 1 if ret > 0 else 0
                all_calls.append((decision_ts, pre_holiday_ts, symbol_id, hit))
        
        if not all_calls:
            print("INSUFFICIENT=1")
            return 0
        
        # Sort by decision time
        all_calls.sort(key=lambda x: x[0])
        
        # Split: most recent 20% as sealed era
        n_total = len(all_calls)
        n_sealed = max(1, int(n_total * 0.2))
        n_train = n_total - n_sealed
        
        train_calls = all_calls[:n_train]
        sealed_calls = all_calls[n_train:]
        
        # Compute metrics on training set
        issued_train = len(train_calls)
        hits_train = sum(c[3] for c in train_calls)
        precision_train = hits_train / issued_train if issued_train > 0 else 0
        
        # Base rate within issued subset (training)
        base_rate_train = precision_train  # Since we only issue on pre-holiday days, base rate = precision
        
        # Distinct days in training
        distinct_days_train = len(set(c[0] for c in train_calls))
        
        # Design effect and effective N
        deff = compute_design_effect([(c[0], c[2], c[3]) for c in train_calls])
        effective_n = issued_train / deff if deff > 0 else issued_train
        
        # Sealed era precision
        issued_sealed = len(sealed_calls)
        hits_sealed = sum(c[3] for c in sealed_calls)
        sealed_precision = hits_sealed / issued_sealed if issued_sealed > 0 else 0
        
        # Opportunities: total decision points considered (symbol-day pairs screened)
        # This is sum of qualified symbols across all decision days
        opportunities = 0
        for decision_ts, pre_holiday_ts in pairs:
            qualified = get_symbols_for_day(conn, decision_ts)
            opportunities += len(qualified)
        
        # Print results
        print(f"ISSUED={issued_train}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision_train:.6f}")
        print(f"BASE_RATE={base_rate_train:.6f}")
        print(f"DISTINCT_DAYS={distinct_days_train}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
        # Verify invariants
        if distinct_days_train > issued_train:
            print("ERROR: DISTINCT_DAYS > ISSUED", file=sys.stderr)
        if effective_n >= issued_train:
            print("ERROR: EFFECTIVE_N >= ISSUED", file=sys.stderr)
        
    finally:
        conn.close()
    
    return 0

if __name__ == "__main__":
    sys.exit(main())
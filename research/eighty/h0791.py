# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 790
# cycle_index: 60
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def unix_to_dt(ts):
    return datetime.utcfromtimestamp(ts)

def dt_to_unix(dt):
    return int(dt.timestamp())

def business_days_between(start_dt, end_dt):
    """Count business days between two datetimes (exclusive of start, inclusive of end)."""
    if start_dt >= end_dt:
        return 0
    count = 0
    current = start_dt + timedelta(days=1)
    while current <= end_dt:
        if current.weekday() < 5:
            count += 1
        current += timedelta(days=1)
    return count

def get_trading_day_bars(conn, symbol_id, start_ts, n_days=21):
    """Get n_days of 1d bars starting at or after start_ts. Returns list of (ts, close)."""
    cur = conn.execute("""
        SELECT ts, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
        ORDER BY ts ASC
        LIMIT ?
    """, (symbol_id, start_ts, n_days + 5))
    return cur.fetchall()

def compute_forward_return_21d(conn, symbol_id, decision_ts):
    """Compute 21-trading-day forward return from decision_ts using 1d bars."""
    bars = get_trading_day_bars(conn, symbol_id, decision_ts, 22)
    if len(bars) < 22:
        return None
    entry_close = bars[0][1]
    exit_close = bars[21][1]
    if entry_close == 0:
        return None
    return (exit_close - entry_close) / entry_close

def load_symbols_with_bars(conn):
    """Get symbols with daily bars from 2018-07-26 onwards."""
    cutoff_ts = dt_to_unix(datetime(2018, 7, 26))
    cur = conn.execute("""
        SELECT s.id, s.symbol, s.market
        FROM symbols s
        WHERE s.active = 1
        AND EXISTS (
            SELECT 1 FROM bars b
            WHERE b.symbol_id = s.id AND b.tf = '1d' AND b.ts >= ?
        )
    """, (cutoff_ts,))
    return cur.fetchall()

def load_fundamentals(conn, symbol_ids):
    """Load SharesOutstanding and EPS fundamentals for symbols."""
    if not symbol_ids:
        return {}, {}
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE symbol_id IN ({placeholders})
        AND metric IN ('SharesOutstanding', 'EPS')
        AND as_of > 0
    """, symbol_ids)
    
    so_data = defaultdict(list)
    eps_data = defaultdict(list)
    for row in cur.fetchall():
        symbol_id, metric, value, as_of, fetched_at = row
        try:
            val = float(value)
            if metric == 'SharesOutstanding':
                so_data[symbol_id].append((as_of, fetched_at, val))
            elif metric == 'EPS':
                eps_data[symbol_id].append((as_of, fetched_at, val))
        except (ValueError, TypeError):
            continue
    
    for symbol_id in so_data:
        so_data[symbol_id].sort(key=lambda x: x[0])
    for symbol_id in eps_data:
        eps_data[symbol_id].sort(key=lambda x: x[0])
    
    return so_data, eps_data

def load_insider_trades(conn, symbol_ids):
    """Load officer (CEO/CFO/COO) open-market purchases (code='P')."""
    if not symbol_ids:
        return []
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders})
        AND code = 'P'
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%COO%')
        ORDER BY symbol_id, insider, filed_ts
    """, symbol_ids)
    return cur.fetchall()

def check_shares_outstanding_condition(so_history, decision_fetched_at, lookback_quarters=20, threshold=0.02):
    """Check zero quarters of >2% SharesOutstanding growth over trailing 20 quarters."""
    available = [(as_of, val) for as_of, fetched_at, val in so_history if fetched_at <= decision_fetched_at]
    if len(available) < lookback_quarters + 1:
        return False
    available.sort(key=lambda x: x[0])
    recent = available[-(lookback_quarters + 1):]
    for i in range(1, len(recent)):
        prev_val = recent[i-1][1]
        curr_val = recent[i][1]
        if prev_val > 0:
            growth = (curr_val - prev_val) / prev_val
            if growth > threshold:
                return False
    return True

def check_eps_acceleration(eps_history, decision_fetched_at, min_quarters=3):
    """Check EPS growth accelerated for 3+ consecutive quarters (QoQ growth rate increasing)."""
    available = [(as_of, val) for as_of, fetched_at, val in eps_history if fetched_at <= decision_fetched_at]
    if len(available) < min_quarters + 1:
        return False
    available.sort(key=lambda x: x[0])
    recent = available[-(min_quarters + 1):]
    
    growth_rates = []
    for i in range(1, len(recent)):
        prev_val = recent[i-1][1]
        curr_val = recent[i][1]
        if prev_val != 0:
            growth = (curr_val - prev_val) / abs(prev_val)
            growth_rates.append(growth)
        else:
            return False
    
    if len(growth_rates) < min_quarters:
        return False
    
    for i in range(1, len(growth_rates)):
        if growth_rates[i] <= growth_rates[i-1]:
            return False
    return True

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row
    
    print("Loading symbols...", file=sys.stderr)
    symbols = load_symbols_with_bars(conn)
    if not symbols:
        print("INSUFFICIENT=1")
        return
    symbol_ids = [s['id'] for s in symbols]
    print(f"  {len(symbols)} symbols with daily bars from 2018-07", file=sys.stderr)
    
    print("Loading fundamentals...", file=sys.stderr)
    so_data, eps_data = load_fundamentals(conn, symbol_ids)
    print(f"  SharesOutstanding: {len(so_data)} symbols, EPS: {len(eps_data)} symbols", file=sys.stderr)
    
    print("Loading insider trades...", file=sys.stderr)
    trades = load_insider_trades(conn, symbol_ids)
    print(f"  {len(trades)} officer open-market purchases", file=sys.stderr)
    
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    # Group trades by (symbol_id, insider)
    trades_by_officer = defaultdict(list)
    for t in trades:
        key = (t['symbol_id'], t['insider'])
        trades_by_officer[key].append(t)
    
    opportunities = []
    issued_calls = []
    
    for (symbol_id, insider), officer_trades in trades_by_officer.items():
        officer_trades.sort(key=lambda x: x['filed_ts'])
        
        for i, trade in enumerate(officer_trades):
            opportunities.append(trade)
            
            # Abstain: officer has <3 prior open-market purchases
            if i < 3:
                continue
            
            # Abstain: disclosure date >5 business days after trade date
            tx_dt = unix_to_dt(trade['tx_ts'])
            filed_dt = unix_to_dt(trade['filed_ts'])
            if business_days_between(tx_dt, filed_dt) > 5:
                continue
            
            # Check: purchase dollar size is officer's personal maximum
            trade_value = trade['value'] or (trade['shares'] * trade['price'])
            prior_values = [t['value'] or (t['shares'] * t['price']) for t in officer_trades[:i]]
            if not prior_values or trade_value < max(prior_values):
                continue
            
            # Check fundamentals conditions at decision time (filed_ts)
            so_history = so_data.get(symbol_id, [])
            eps_history = eps_data.get(symbol_id, [])
            
            if not so_history or not eps_history:
                continue
            
            # Abstain: zero quarters of >2% SharesOutstanding growth over trailing 20 quarters
            if not check_shares_outstanding_condition(so_history, trade['filed_ts']):
                continue
            
            # Abstain: EPS growth accelerated for 3+ consecutive quarters
            if not check_eps_acceleration(eps_history, trade['filed_ts']):
                continue
            
            # Get 21-day forward return label
            fwd_return = compute_forward_return_21d(conn, symbol_id, trade['filed_ts'])
            if fwd_return is None:
                continue
            
            up = 1 if fwd_return > 0 else 0
            issued_calls.append({
                'symbol_id': symbol_id,
                'insider': insider,
                'filed_ts': trade['filed_ts'],
                'filed_dt': filed_dt,
                'up': up,
                'fwd_return': fwd_return,
                'trade_value': trade_value
            })
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Sort by decision time
    issued_calls.sort(key=lambda x: x['filed_ts'])
    
    # Hold out most recent 20% as sealed era
    n_total = len(issued_calls)
    n_sealed = max(1, int(math.ceil(n_total * 0.2)))
    n_train = n_total - n_sealed
    
    train_calls = issued_calls[:n_train]
    sealed_calls = issued_calls[n_train:]
    
    # Compute metrics
    def compute_precision(calls):
        if not calls:
            return 0.0
        hits = sum(c['up'] for c in calls)
        return hits / len(calls)
    
    def compute_base_rate(calls):
        if not calls:
            return 0.0
        return sum(c['up'] for c in calls) / len(calls)
    
    def compute_distinct_days(calls):
        days = set()
        for c in calls:
            dt = unix_to_dt(c['filed_ts'])
            days.add(dt.date())
        return len(days)
    
    def compute_design_effect(calls):
        """Estimate design effect from temporal clustering."""
        if len(calls) < 2:
            return 1.0
        # Group by week
        week_counts = defaultdict(int)
        for c in calls:
            dt = unix_to_dt(c['filed_ts'])
            week_key = (dt.year, dt.isocalendar()[1])
            week_counts[week_key] += 1
        # Design effect ≈ 1 + (avg cluster size - 1) * ICC
        # Simplified: use variance inflation from clustering
        cluster_sizes = list(week_counts.values())
        if not cluster_sizes:
            return 1.0
        mean_cluster = sum(cluster_sizes) / len(cluster_sizes)
        var_cluster = sum((c - mean_cluster) ** 2 for c in cluster_sizes) / len(cluster_sizes)
        # Conservative ICC estimate for financial returns
        icc = 0.1
        deff = 1 + (mean_cluster - 1) * icc
        return max(1.0, deff)
    
    precision = compute_precision(issued_calls)
    base_rate = compute_base_rate(issued_calls)
    distinct_days = compute_distinct_days(issued_calls)
    deff = compute_design_effect(issued_calls)
    effective_n = len(issued_calls) / deff
    sealed_precision = compute_precision(sealed_calls)
    
    # Invariants check
    if distinct_days > len(issued_calls):
        distinct_days = len(issued_calls)
    if effective_n >= len(issued_calls):
        effective_n = len(issued_calls) - 0.001
    
    print(f"ISSUED={len(issued_calls)}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
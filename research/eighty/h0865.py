# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 864
# cycle_index: 10
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def get_trading_days(conn):
    """Get all distinct 1d bar timestamps as sorted list of unix epochs (start of UTC day)."""
    cur = conn.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    return [row[0] for row in cur.fetchall()]

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def build_session_map(trading_days):
    """Map each trading day timestamp to its session index (0-based)."""
    return {ts: i for i, ts in enumerate(trading_days)}

def get_session_for_ts(ts, session_map, trading_days):
    """Get session index for a timestamp (finds the trading day <= ts)."""
    # Binary search for the last trading day <= ts
    lo, hi = 0, len(trading_days) - 1
    ans = -1
    while lo <= hi:
        mid = (lo + hi) // 2
        if trading_days[mid] <= ts:
            ans = mid
            lo = mid + 1
        else:
            hi = mid - 1
    return ans

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.execute("PRAGMA query_only = ON")
    
    # Get trading days and session map
    trading_days = get_trading_days(conn)
    if not trading_days:
        print("INSUFFICIENT=1")
        return
    session_map = build_session_map(trading_days)
    n_sessions = len(trading_days)
    
    # Get officer (CEO/CFO) open-market sales (code='S')
    # title contains CEO or CFO (case-insensitive)
    cur = conn.execute("""
        SELECT symbol_id, tx_ts, filed_ts, title, code
        FROM insider_trades
        WHERE code = 'S'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
        ORDER BY symbol_id, filed_ts, tx_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    # Map trades to sessions
    trade_records = []
    for symbol_id, tx_ts, filed_ts, title, code in trades:
        tx_session = get_session_for_ts(tx_ts, session_map, trading_days)
        filed_session = get_session_for_ts(filed_ts, session_map, trading_days)
        if tx_session >= 0 and filed_session >= 0:
            trade_records.append({
                'symbol_id': symbol_id,
                'tx_session': tx_session,
                'filed_session': filed_session,
                'tx_ts': tx_ts,
                'filed_ts': filed_ts,
                'title': title
            })
    
    if not trade_records:
        print("INSUFFICIENT=1")
        return
    
    # Group by symbol_id and filed_session (disclosure date)
    by_symbol_filed = defaultdict(list)
    for tr in trade_records:
        by_symbol_filed[(tr['symbol_id'], tr['filed_session'])].append(tr)
    
    # Universe: symbols with at least one officer transaction in prior 252 sessions
    # First, get all symbols that ever have officer trades
    symbol_officer_sessions = defaultdict(list)
    for tr in trade_records:
        symbol_officer_sessions[tr['symbol_id']].append(tr['tx_session'])
    
    for sym in symbol_officer_sessions:
        symbol_officer_sessions[sym].sort()
    
    # For each disclosure, check if symbol has officer trade in prior 252 sessions
    opportunities = []
    issued_calls = []  # (symbol_id, filed_session, filed_ts, predicted_down, actual_down)
    
    for (symbol_id, filed_session), trades_list in by_symbol_filed.items():
        # Check universe: at least one officer trade in prior 252 sessions (before this disclosure)
        prior_sessions = [s for s in symbol_officer_sessions.get(symbol_id, []) if s < filed_session]
        if not prior_sessions:
            continue
        if filed_session - prior_sessions[-1] > 252:
            continue
        
        # This is a decision point (opportunity)
        opportunities.append((symbol_id, filed_session))
        
        # ENTRY conditions:
        # 1. Two or more officer sales
        if len(trades_list) < 2:
            continue
        
        # 2. Trade dates within a 5-session window
        tx_sessions = [t['tx_session'] for t in trades_list]
        tx_min, tx_max = min(tx_sessions), max(tx_sessions)
        if tx_max - tx_min > 5:
            continue
        
        # 3. Max disclosure delay <= 10 sessions
        delays = [t['filed_session'] - t['tx_session'] for t in trades_list]
        if max(delays) > 10:
            continue
        
        # 4. No trade date > 20 sessions before disclosure (stale cluster)
        if any(d > 20 for d in delays):
            continue
        
        # All entry conditions met - issue a call
        # Predict DOWN (negative return) because sales signal negative info
        # Need 21-day forward return from bars
        label_session = filed_session + 21
        if label_session >= n_sessions:
            continue  # Not enough future data
        
        # Get close prices at filed_session and label_session
        filed_ts = trading_days[filed_session]
        label_ts = trading_days[label_session]
        
        cur = conn.execute("""
            SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?
        """, (symbol_id, filed_ts))
        row = cur.fetchone()
        if not row:
            continue
        entry_close = row[0]
        
        cur = conn.execute("""
            SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?
        """, (symbol_id, label_ts))
        row = cur.fetchone()
        if not row:
            continue
        exit_close = row[0]
        
        fwd_return = (exit_close - entry_close) / entry_close
        actual_down = 1 if fwd_return < 0 else 0
        predicted_down = 1  # We predict down
        
        issued_calls.append({
            'symbol_id': symbol_id,
            'filed_session': filed_session,
            'filed_ts': filed_ts,
            'predicted_down': predicted_down,
            'actual_down': actual_down,
            'fwd_return': fwd_return
        })
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    if not issued_calls:
        # No calls issued, but opportunities exist
        print("ISSUED=0")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print("PRECISION=0.0")
        print("BASE_RATE=0.0")
        print("DISTINCT_DAYS=0")
        print("EFFECTIVE_N=0.0")
        print("SEALED_PRECISION=0.0")
        return
    
    # Hold out most recent 20% as sealed era
    # Sort by filed_session (time)
    issued_calls.sort(key=lambda x: x['filed_session'])
    n_issued = len(issued_calls)
    split_idx = int(n_issued * 0.8)
    main_calls = issued_calls[:split_idx]
    sealed_calls = issued_calls[split_idx:]
    
    # Compute metrics on main era
    main_hits = sum(1 for c in main_calls if c['predicted_down'] == c['actual_down'] and c['predicted_down'] == 1)
    main_issued = len(main_calls)
    main_precision = main_hits / main_issued if main_issued > 0 else 0.0
    
    # Base rate of predicted class (down) within issued subset
    main_down_count = sum(1 for c in main_calls if c['actual_down'] == 1)
    main_base_rate = main_down_count / main_issued if main_issued > 0 else 0.0
    
    # Distinct UTC days among issued calls
    main_days = set()
    for c in main_calls:
        d = datetime.utcfromtimestamp(c['filed_ts']).date()
        main_days.add(d)
    distinct_days = len(main_days)
    
    # Design effect: cluster by day, compute variance inflation
    # Group calls by day
    day_counts = defaultdict(int)
    for c in main_calls:
        d = datetime.utcfromtimestamp(c['filed_ts']).date()
        day_counts[d] += 1
    
    if len(day_counts) > 1:
        mean_cluster = sum(day_counts.values()) / len(day_counts)
        var_cluster = sum((c - mean_cluster) ** 2 for c in day_counts.values()) / len(day_counts)
        design_effect = 1 + (mean_cluster - 1) * (var_cluster / (mean_cluster ** 2)) if mean_cluster > 0 else 1.0
        # Simplified: design_effect = 1 + (avg_cluster_size - 1) * ICC
        # Using Kish's approximation: deff = 1 + (n_bar - 1) * rho
        # Estimate rho from cluster sizes
        if mean_cluster > 1:
            design_effect = 1 + (mean_cluster - 1) * (var_cluster / (mean_cluster * (mean_cluster - 1))) if var_cluster > 0 else 1.0
        else:
            design_effect = 1.0
    else:
        design_effect = 1.0
    
    # Ensure design effect > 1 (calls clustered in time are not independent)
    if design_effect <= 1.0:
        design_effect = 1.0 + 1e-9
    
    effective_n = main_issued / design_effect
    
    # Sealed era precision
    sealed_hits = sum(1 for c in sealed_calls if c['predicted_down'] == c['actual_down'] and c['predicted_down'] == 1)
    sealed_issued = len(sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    
    # Output
    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={main_precision:.6f}")
    print(f"BASE_RATE={main_base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
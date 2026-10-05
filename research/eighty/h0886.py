# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 885
# cycle_index: 31
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

"""MECHANISM: Officers and directors possess private fundamental conviction; when they commit personal capital via open-market purchases during Federal Reserve balance sheet contraction (WALCL declining over 3 months), they signal micro strength overcoming macro headwinds, and the market underreacts due to macro attribution bias.
HORIZON: 5d (5 trading days, built from bars tf='1d')
UNIVERSE: Active common stocks with >=500 daily bars (tf='1d') and >=5 historical officer/director open-market purchases in insider_trades
ENTRY: On each filed_ts where an officer (title like '%CEO%' or '%CFO%') or director (title like '%Director%') executes an open-market purchase (code='P'), and the 3-month change in FRED:WALCL (latest value <= filed_ts vs value ~63 trading days prior) is negative, issue a long call for the next 5 trading sessions.
ABSTAIN: If WALCL data unavailable within 10 days of filed_ts, or if any Form 4/8-K/144 filing for the symbol exists in the prior 5 trading days, or if multiple insiders issue conflicting signals (purchases and sales) on the same filed_ts.
CLAIM: Precision exceeds the base rate within the issued subset by >=10 percentage points in both the main era and the sealed 20% holdout era."""

import sqlite3
import sys
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_universe_symbols(conn):
    """Symbols with >=500 daily bars and >=5 officer/director purchases."""
    cur = conn.cursor()
    # Symbols with sufficient daily bars
    cur.execute("""
        SELECT symbol_id, COUNT(*) as bar_count
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING bar_count >= 500
    """)
    bar_symbols = {row[0] for row in cur.fetchall()}
    
    # Symbols with >=5 officer/director open-market purchases
    cur.execute("""
        SELECT symbol_id, COUNT(*) as purchase_count
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Director%')
        GROUP BY symbol_id
        HAVING purchase_count >= 5
    """)
    insider_symbols = {row[0] for row in cur.fetchall()}
    
    # Active symbols
    cur.execute("SELECT id FROM symbols WHERE active = 1 AND market = 'stocks'")
    active_symbols = {row[0] for row in cur.fetchall()}
    
    return bar_symbols & insider_symbols & active_symbols

def load_walcl(conn):
    """Load WALCL series as list of (ts, value) sorted by ts."""
    cur = conn.cursor()
    cur.execute("SELECT ts, value FROM macro_series WHERE series = 'WALCL' ORDER BY ts")
    rows = cur.fetchall()
    if not rows:
        # Try alternative series names
        for alt in ['FED_ASSETS', 'WALCL_LEVEL', 'BOGZ1FL880061705A']:
            cur.execute("SELECT ts, value FROM macro_series WHERE series = ? ORDER BY ts", (alt,))
            rows = cur.fetchall()
            if rows:
                break
    return rows

def load_bars_for_symbols(conn, symbol_ids):
    """Load daily bars for symbols, return dict symbol_id -> list of (ts, close)."""
    if not symbol_ids:
        return {}
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.cursor()
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, list(symbol_ids))
    
    bars = defaultdict(list)
    for symbol_id, ts, close in cur.fetchall():
        bars[symbol_id].append((ts, close))
    return bars

def load_insider_purchases(conn, symbol_ids):
    """Load officer/director open-market purchases with filed_ts."""
    if not symbol_ids:
        return []
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.cursor()
    cur.execute(f"""
        SELECT symbol_id, filed_ts, title
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Director%')
          AND symbol_id IN ({placeholders})
        ORDER BY filed_ts
    """, list(symbol_ids))
    return cur.fetchall()

def load_filings(conn, symbol_ids):
    """Load recent filings for abstain check."""
    if not symbol_ids:
        return defaultdict(list)
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.cursor()
    cur.execute(f"""
        SELECT symbol_id, filed_ts, form
        FROM filings
        WHERE form IN ('4', '8-K', '144')
          AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, filed_ts
    """, list(symbol_ids))
    
    filings = defaultdict(list)
    for symbol_id, filed_ts, form in cur.fetchall():
        filings[symbol_id].append((filed_ts, form))
    return filings

def load_conflicting_signals(conn, symbol_ids):
    """Load days with both purchases and sales by insiders."""
    if not symbol_ids:
        return defaultdict(set)
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.cursor()
    cur.execute(f"""
        SELECT symbol_id, filed_ts, code
        FROM insider_trades
        WHERE symbol_id IN ({placeholders})
          AND code IN ('P', 'S')
        ORDER BY symbol_id, filed_ts
    """, list(symbol_ids))
    
    signals = defaultdict(lambda: {'P': 0, 'S': 0})
    for symbol_id, filed_ts, code in cur.fetchall():
        # Group by date (filed_ts is unix epoch, convert to UTC date)
        import datetime
        dt = datetime.datetime.utcfromtimestamp(filed_ts).date()
        day_key = (symbol_id, dt)
        signals[day_key][code] += 1
    
    conflict_days = defaultdict(set)
    for (symbol_id, dt), counts in signals.items():
        if counts['P'] > 0 and counts['S'] > 0:
            conflict_days[symbol_id].add(dt)
    return conflict_days

def find_walcl_value(walcl_data, target_ts, max_lag_days=10):
    """Find WALCL value at or before target_ts within max_lag_days."""
    # walcl_data is list of (ts, value) sorted by ts
    # target_ts is unix epoch
    max_lag_secs = max_lag_days * 86400
    best = None
    for ts, value in walcl_data:
        if ts <= target_ts and (target_ts - ts) <= max_lag_secs:
            best = (ts, value)
        elif ts > target_ts:
            break
    return best

def get_trading_days(bars_dict, symbol_id):
    """Get sorted list of trading day timestamps for a symbol."""
    return [ts for ts, _ in bars_dict.get(symbol_id, [])]

def find_forward_return(bars_dict, symbol_id, decision_ts, horizon_days=5):
    """Compute forward return over horizon_days trading days from decision_ts."""
    bars = bars_dict.get(symbol_id, [])
    if not bars:
        return None
    
    # Find first bar on or after decision_ts
    start_idx = None
    for i, (ts, _) in enumerate(bars):
        if ts >= decision_ts:
            start_idx = i
            break
    
    if start_idx is None:
        return None
    
    end_idx = start_idx + horizon_days
    if end_idx >= len(bars):
        return None
    
    entry_price = bars[start_idx][1]
    exit_price = bars[end_idx][1]
    
    if entry_price <= 0:
        return None
    
    return (exit_price - entry_price) / entry_price

def walcl_3m_change(walcl_data, decision_ts):
    """Compute 3-month (approx 63 trading days) change in WALCL."""
    current = find_walcl_value(walcl_data, decision_ts, max_lag_days=10)
    if not current:
        return None
    
    # 3 months ~ 63 trading days ~ 90 calendar days
    past_ts = decision_ts - 90 * 86400
    past = find_walcl_value(walcl_data, past_ts, max_lag_days=10)
    if not past:
        return None
    
    current_val = current[1]
    past_val = past[1]
    if past_val == 0:
        return None
    
    return (current_val - past_val) / past_val

def has_recent_filing(filings_dict, symbol_id, decision_ts, lookback_days=5):
    """Check if any filing in prior lookback_days."""
    if symbol_id not in filings_dict:
        return False
    lookback_secs = lookback_days * 86400
    for filed_ts, _ in filings_dict[symbol_id]:
        if filed_ts < decision_ts and (decision_ts - filed_ts) <= lookback_secs:
            return True
    return False

def main():
    conn = connect()
    
    try:
        # Load data
        universe = get_universe_symbols(conn)
        if not universe:
            print("INSUFFICIENT=1")
            return
        
        walcl_data = load_walcl(conn)
        if not walcl_data:
            print("INSUFFICIENT=1")
            return
        
        bars_dict = load_bars_for_symbols(conn, universe)
        if not bars_dict:
            print("INSUFFICIENT=1")
            return
        
        purchases = load_insider_purchases(conn, universe)
        if not purchases:
            print("INSUFFICIENT=1")
            return
        
        filings_dict = load_filings(conn, universe)
        conflict_days = load_conflicting_signals(conn, universe)
        
        # Generate signals
        signals = []  # list of (symbol_id, decision_dt, decision_ts, forward_return)
        
        for symbol_id, filed_ts, title in purchases:
            # Check WALCL 3-month change
            change = walcl_3m_change(walcl_data, filed_ts)
            if change is None or change >= 0:
                continue
            
            # Check recent filings
            if has_recent_filing(filings_dict, symbol_id, filed_ts):
                continue
            
            # Check conflicting signals
            import datetime
            decision_dt = datetime.datetime.utcfromtimestamp(filed_ts).date()
            if decision_dt in conflict_days.get(symbol_id, set()):
                continue
            
            # Compute forward return
            fwd_return = find_forward_return(bars_dict, symbol_id, filed_ts, horizon_days=5)
            if fwd_return is None:
                continue
            
            signals.append((symbol_id, decision_dt, filed_ts, fwd_return))
        
        if not signals:
            print("INSUFFICIENT=1")
            return
        
        # Aggregate to one observation per (symbol, UTC day)
        obs_dict = {}  # (symbol_id, decision_dt) -> fwd_return (first signal of the day)
        for symbol_id, decision_dt, filed_ts, fwd_return in signals:
            key = (symbol_id, decision_dt)
            if key not in obs_dict:
                obs_dict[key] = fwd_return
        
        observations = [(symbol_id, dt, ret) for (symbol_id, dt), ret in obs_dict.items()]
        observations.sort(key=lambda x: x[1])  # sort by date
        
        # Split: last 20% by time as sealed
        n = len(observations)
        split_idx = int(n * 0.8)
        main_obs = observations[:split_idx]
        sealed_obs = observations[split_idx:]
        
        def compute_metrics(obs_list):
            if not obs_list:
                return 0, 0, 0, 0, 0
            issued = len(obs_list)
            hits = sum(1 for _, _, ret in obs_list if ret > 0)
            precision = hits / issued if issued > 0 else 0
            base_rate = precision  # base rate within issued subset
            distinct_days = len(set(dt for _, dt, _ in obs_list))
            # Design effect: at least 1.01
            design_effect = max(1.01, issued / distinct_days) if distinct_days > 0 else 1.01
            effective_n = issued / design_effect
            return issued, hits, precision, base_rate, distinct_days, effective_n
        
        main_issued, main_hits, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_metrics(main_obs)
        sealed_issued, sealed_hits, sealed_precision, sealed_base_rate, sealed_distinct_days, sealed_effective_n = compute_metrics(sealed_obs)
        
        total_issued = main_issued + sealed_issued
        total_hits = main_hits + sealed_hits
        total_precision = total_hits / total_issued if total_issued > 0 else 0
        
        # Overall base rate in issued subset
        overall_base_rate = total_precision
        
        # Distinct days overall
        all_distinct_days = len(set(dt for _, dt, _ in observations))
        
        # Overall effective N
        overall_design_effect = max(1.01, total_issued / all_distinct_days) if all_distinct_days > 0 else 1.01
        overall_effective_n = total_issued / overall_design_effect
        
        print(f"ISSUED={total_issued}")
        print(f"OPPORTUNITIES={len(purchases)}")
        print(f"PRECISION={total_precision:.6f}")
        print(f"BASE_RATE={overall_base_rate:.6f}")
        print(f"DISTINCT_DAYS={all_distinct_days}")
        print(f"EFFECTIVE_N={overall_effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    finally:
        conn.close()

if __name__ == '__main__':
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 775
# cycle_index: 45
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_trading_days(bars_by_symbol):
    """Return sorted list of (ts, close, volume, dollar_vol) for each symbol."""
    result = {}
    for sym_id, rows in bars_by_symbol.items():
        rows.sort(key=lambda x: x[0])
        result[sym_id] = [(r[0], r[3], r[4], r[3] * r[4]) for r in rows]  # ts, close, volume, dollar_vol
    return result

def compute_rolling_stats(trading_days, window_vol=63, window_ret=252, window_dvol=20):
    """Compute rolling stats for each trading day index."""
    n = len(trading_days)
    if n < max(window_vol, window_ret, window_dvol):
        return None, None, None
    
    closes = [d[1] for d in trading_days]
    dollar_vols = [d[3] for d in trading_days]
    
    # Daily log returns
    log_rets = [0.0] * n
    for i in range(1, n):
        if closes[i-1] > 0:
            log_rets[i] = math.log(closes[i] / closes[i-1])
    
    # Rolling 63-day realized vol (std of log returns)
    vol_63 = [None] * n
    for i in range(window_vol - 1, n):
        window = log_rets[i - window_vol + 1:i + 1]
        mean = sum(window) / window_vol
        var = sum((x - mean) ** 2 for x in window) / window_vol
        vol_63[i] = math.sqrt(var) * math.sqrt(252)  # annualized
    
    # Rolling 252-day return
    ret_252 = [None] * n
    for i in range(window_ret - 1, n):
        if closes[i - window_ret + 1] > 0:
            ret_252[i] = (closes[i] / closes[i - window_ret + 1]) - 1
    
    # Rolling 20-day avg dollar volume
    avg_dvol_20 = [None] * n
    for i in range(window_dvol - 1, n):
        window = dollar_vols[i - window_dvol + 1:i + 1]
        avg_dvol_20[i] = sum(window) / window_dvol
    
    return vol_63, ret_252, avg_dvol_20

def find_bar_index(trading_days, target_ts, direction='floor'):
    """Find index of bar at or before (floor) / at or after (ceil) target_ts."""
    ts_list = [d[0] for d in trading_days]
    lo, hi = 0, len(ts_list) - 1
    ans = -1
    while lo <= hi:
        mid = (lo + hi) // 2
        if ts_list[mid] <= target_ts:
            ans = mid
            lo = mid + 1
        else:
            hi = mid - 1
    if direction == 'floor':
        return ans
    # ceil
    if ans == -1:
        return 0
    if ts_list[ans] == target_ts:
        return ans
    if ans + 1 < len(ts_list):
        return ans + 1
    return -1

def is_officer_title(title):
    if not title:
        return False
    t = title.upper()
    return ('CEO' in t or 'CFO' in t or 'CHIEF EXECUTIVE' in t or 'CHIEF FINANCIAL' in t)

def main():
    conn = connect()
    cur = conn.cursor()
    
    # Get stock symbols
    cur.execute("SELECT id FROM symbols WHERE market='stocks'")
    stock_symbols = [row[0] for row in cur.fetchall()]
    if not stock_symbols:
        print("INSUFFICIENT=1")
        return 0
    sym_set = set(stock_symbols)
    
    # Get daily bars for stock symbols
    placeholders = ','.join('?' * len(stock_symbols))
    cur.execute(f"SELECT symbol_id, ts, open, high, low, close, volume FROM bars WHERE tf='1d' AND symbol_id IN ({placeholders})", stock_symbols)
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row[0]].append(row[1:])  # ts, open, high, low, close, volume
    
    # Filter symbols with >= 756 daily bars (3 years)
    valid_symbols = {sid for sid, rows in bars_by_symbol.items() if len(rows) >= 756}
    if not valid_symbols:
        print("INSUFFICIENT=1")
        return 0
    
    # Get officer insider purchases
    cur.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P' AND symbol_id IN ({})
    """.format(','.join('?' * len(valid_symbols))), list(valid_symbols))
    
    insider_trades = []
    for row in cur.fetchall():
        if is_officer_title(row[3]):
            insider_trades.append(row)
    
    if not insider_trades:
        print("INSUFFICIENT=1")
        return 0
    
    # Precompute rolling stats for each valid symbol
    trading_days = get_trading_days({sid: bars_by_symbol[sid] for sid in valid_symbols})
    rolling_stats = {}
    for sid, days in trading_days.items():
        vol_63, ret_252, avg_dvol_20 = compute_rolling_stats(days)
        if vol_63 is not None:
            rolling_stats[sid] = (days, vol_63, ret_252, avg_dvol_20)
    
    # For percentile calculations, we need rolling distributions
    # For each symbol, at each index, compute 25th percentile of vol_63 over past 252 days
    vol_63_pct25 = {}
    for sid, (days, vol_63, ret_252, avg_dvol_20) in rolling_stats.items():
        n = len(days)
        pct25 = [None] * n
        for i in range(252 - 1, n):
            window = [v for v in vol_63[i - 251:i + 1] if v is not None]
            if len(window) >= 63:  # need sufficient data
                window.sort()
                pct25[i] = window[len(window) // 4]
        vol_63_pct25[sid] = pct25
    
    # Evaluate each insider trade
    calls = []  # (filed_ts, symbol_id, tx_ts, entry_ts, forward_return)
    opportunities = 0
    
    for trade in insider_trades:
        accession, sym_id, insider, title, code, shares, price, value, tx_ts, filed_ts = trade
        if sym_id not in rolling_stats:
            continue
        
        days, vol_63, ret_252, avg_dvol_20 = rolling_stats[sym_id]
        pct25 = vol_63_pct25[sym_id]
        
        # Find trade date index (floor)
        tx_idx = find_bar_index(days, tx_ts, 'floor')
        if tx_idx < 252:  # need 252-day history for percentiles and return
            continue
        
        # Check conditions at tx_ts
        v63 = vol_63[tx_idx]
        r252 = ret_252[tx_idx]
        dvol20 = avg_dvol_20[tx_idx]
        v_pct25 = pct25[tx_idx]
        
        if v63 is None or r252 is None or dvol20 is None or v_pct25 is None:
            continue
        if dvol20 <= 0:
            continue
        
        opportunities += 1
        
        # Condition 1: 63-day realized vol < 25th pct of 252-day history
        if not (v63 < v_pct25):
            continue
        
        # Condition 2: 252-day return < 0
        if not (r252 < 0):
            continue
        
        # Condition 3: trade-to-disclosure delay <= 2 days
        delay_days = (filed_ts - tx_ts) / 86400.0
        if not (0 < delay_days <= 2):
            continue
        
        # Condition 4: purchase size > 1% of 20-day avg dollar volume
        trade_dollar = shares * price
        if not (trade_dollar > 0.01 * dvol20):
            continue
        
        # Check for multiple officer purchases on same tx_ts (abstain)
        # We'll handle this after collecting all candidates
        
        # Find entry index: first bar at or after filed_ts
        entry_idx = find_bar_index(days, filed_ts, 'ceil')
        if entry_idx == -1 or entry_idx + 21 >= len(days):
            continue  # not enough forward data
        
        entry_ts = days[entry_idx][0]
        entry_close = days[entry_idx][1]
        exit_close = days[entry_idx + 21][1]
        
        if entry_close <= 0:
            continue
        
        fwd_return = (exit_close / entry_close) - 1
        hit = 1 if fwd_return > 0 else 0
        
        calls.append({
            'filed_ts': filed_ts,
            'entry_ts': entry_ts,
            'symbol_id': sym_id,
            'tx_ts': tx_ts,
            'hit': hit,
            'fwd_return': fwd_return
        })
    
    # Abstain: remove calls where multiple officer purchases share same tx_ts
    tx_ts_counts = defaultdict(int)
    for c in calls:
        tx_ts_counts[c['tx_ts']] += 1
    
    filtered_calls = [c for c in calls if tx_ts_counts[c['tx_ts']] == 1]
    
    if not filtered_calls:
        print("INSUFFICIENT=1")
        return 0
    
    # Sort by filed_ts
    filtered_calls.sort(key=lambda x: x['filed_ts'])
    
    # Hold out most recent 20% as sealed era
    n_total = len(filtered_calls)
    n_sealed = max(1, int(n_total * 0.2))
    n_main = n_total - n_sealed
    
    main_calls = filtered_calls[:n_main]
    sealed_calls = filtered_calls[n_main:]
    
    # Compute metrics
    issued = len(main_calls)
    if issued == 0:
        print("INSUFFICIENT=1")
        return 0
    
    hits = sum(c['hit'] for c in main_calls)
    precision = hits / issued
    
    # Base rate within issued subset
    base_rate = hits / issued  # same as precision for binary, but base rate is prevalence of positive class
    # Actually base rate = proportion of positive labels in issued set
    base_rate = hits / issued
    
    # Distinct days among issued calls (UTC days from entry_ts)
    distinct_days = len(set(datetime.utcfromtimestamp(c['entry_ts']).date() for c in main_calls))
    
    # Effective N: issued / design_effect
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Approximate: group by UTC day, compute cluster sizes
    day_counts = defaultdict(int)
    for c in main_calls:
        day = datetime.utcfromtimestamp(c['entry_ts']).date()
        day_counts[day] += 1
    cluster_sizes = list(day_counts.values())
    avg_cluster = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
    # Conservative ICC estimate for financial returns ~0.1-0.3, use 0.2
    icc = 0.2
    design_effect = 1 + (avg_cluster - 1) * icc
    effective_n = issued / design_effect
    
    # Sealed precision
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(c['hit'] for c in sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    
    # Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())
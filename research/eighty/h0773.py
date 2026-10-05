# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 772
# cycle_index: 42
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from bisect import bisect_right, bisect_left
from statistics import median

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def date_to_ts(date_str):
    return int(datetime.strptime(date_str, '%Y-%m-%d').timestamp())

def load_walcl(conn):
    cur = conn.execute("SELECT ts, value FROM macro_series WHERE series='WALCL' ORDER BY ts")
    rows = cur.fetchall()
    return [(int(ts), float(val)) for ts, val in rows]

def load_news_dates(conn):
    cur = conn.execute("SELECT symbol_id, ts FROM news")
    news_set = set()
    for symbol_id, ts in cur.fetchall():
        news_set.add((symbol_id, ts_to_date(int(ts))))
    return news_set

def load_symbols(conn):
    cur = conn.execute("SELECT id, symbol FROM symbols WHERE market='stocks'")
    return {row[0]: row[1] for row in cur.fetchall()}

def load_daily_bars(conn):
    cur = conn.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    bars_by_symbol = {}
    for symbol_id, ts, close, volume in cur.fetchall():
        symbol_id = int(symbol_id)
        ts = int(ts)
        close = float(close)
        volume = float(volume)
        if symbol_id not in bars_by_symbol:
            bars_by_symbol[symbol_id] = []
        bars_by_symbol[symbol_id].append((ts, close, volume))
    return bars_by_symbol

def get_trading_days(bars_by_symbol):
    all_ts = set()
    for bars in bars_by_symbol.values():
        for ts, _, _ in bars:
            all_ts.add(ts)
    return sorted(all_ts)

def find_walcl_index(walcl_ts_list, target_ts):
    """Return index of latest WALCL <= target_ts, or -1 if none."""
    idx = bisect_right(walcl_ts_list, target_ts) - 1
    return idx if idx >= 0 else -1

def compute_metrics(issued_calls, opportunities, sealed_start_idx):
    if not issued_calls:
        return None
    
    issued = len(issued_calls)
    hits = sum(1 for _, _, up in issued_calls if up)
    precision = hits / issued if issued else 0.0
    
    opp_hits = sum(1 for _, _, up in opportunities if up)
    opp_total = len(opportunities)
    base_rate = opp_hits / opp_total if opp_total else 0.0
    
    distinct_days = len(set(ts for ts, _, _ in issued_calls))
    
    design_effect = issued / distinct_days if distinct_days else 1.0
    effective_n = issued / design_effect if design_effect > 0 else 0.0
    
    sealed_calls = issued_calls[sealed_start_idx:]
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(1 for _, _, up in sealed_calls if up)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0.0
    
    return {
        'ISSUED': issued,
        'OPPORTUNITIES': opp_total,
        'PRECISION': precision,
        'BASE_RATE': base_rate,
        'DISTINCT_DAYS': distinct_days,
        'EFFECTIVE_N': effective_n,
        'SEALED_PRECISION': sealed_precision
    }

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        conn.execute("PRAGMA query_only = ON")
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

    print("Loading data...", file=sys.stderr)
    
    walcl_data = load_walcl(conn)
    if not walcl_data:
        print("INSUFFICIENT=1")
        return 0
    walcl_ts = [ts for ts, _ in walcl_data]
    walcl_val = [val for _, val in walcl_data]
    
    news_dates = load_news_dates(conn)
    symbols = load_symbols(conn)
    bars_by_symbol = load_daily_bars(conn)
    trading_days = get_trading_days(bars_by_symbol)
    
    if not trading_days:
        print("INSUFFICIENT=1")
        return 0
    
    print(f"Loaded {len(walcl_data)} WALCL points, {len(news_dates)} news items, {len(symbols)} symbols, {len(bars_by_symbol)} symbols with bars, {len(trading_days)} trading days", file=sys.stderr)
    
    symbol_indices = {sym: {ts: i for i, (ts, _, _) in enumerate(bars)} for sym, bars in bars_by_symbol.items()}
    
    all_opportunities = []
    all_issued = []
    
    for day_ts in trading_days:
        walcl_idx = find_walcl_index(walcl_ts, day_ts)
        if walcl_idx == -1:
            continue
        if day_ts - walcl_ts[walcl_idx] > 7 * 86400:
            continue
        
        old_target = walcl_ts[walcl_idx] - 90 * 86400
        old_idx = find_walcl_index(walcl_ts, old_target)
        if old_idx == -1:
            continue
        
        walcl_recent = walcl_val[walcl_idx]
        walcl_old = walcl_val[old_idx]
        if walcl_old <= 0:
            continue
        pct_change = (walcl_recent - walcl_old) / walcl_old * 100
        if pct_change <= 1.0:
            continue
        
        day_date = ts_to_date(day_ts)
        symbol_medians = {}
        symbol_returns_20 = {}
        symbol_forward = {}
        
        for symbol_id, bars in bars_by_symbol.items():
            if symbol_id not in symbol_indices:
                continue
            idx_map = symbol_indices[symbol_id]
            if day_ts not in idx_map:
                continue
            i = idx_map[day_ts]
            
            if i < 252:
                continue
            if i < 20:
                continue
            if i + 21 >= len(bars):
                continue
            
            dollar_vols = [bars[j][1] * bars[j][2] for j in range(i - 252, i)]
            med_dv = median(dollar_vols)
            symbol_medians[symbol_id] = med_dv
            
            close_now = bars[i][1]
            close_20 = bars[i - 20][1]
            if close_20 <= 0:
                continue
            ret_20 = (close_now / close_20) - 1
            symbol_returns_20[symbol_id] = ret_20
            
            close_fwd = bars[i + 21][1]
            if close_now <= 0:
                continue
            fwd_ret = (close_fwd / close_now) - 1
            symbol_forward[symbol_id] = fwd_ret
        
        if not symbol_medians:
            continue
        
        medians_sorted = sorted(symbol_medians.values())
        decile_idx = max(0, int(len(medians_sorted) * 0.1) - 1)
        threshold = medians_sorted[decile_idx]
        
        for symbol_id, med_dv in symbol_medians.items():
            if med_dv > threshold:
                continue
            
            ret_20 = symbol_returns_20.get(symbol_id)
            if ret_20 is None or ret_20 >= 0:
                continue
            
            if (symbol_id, day_date) in news_dates:
                continue
            
            fwd_ret = symbol_forward.get(symbol_id)
            if fwd_ret is None:
                continue
            
            up = 1 if fwd_ret > 0 else 0
            all_opportunities.append((day_ts, symbol_id, up))
            
            all_issued.append((day_ts, symbol_id, up))
    
    if not all_issued:
        print("INSUFFICIENT=1")
        return 0
    
    all_issued.sort(key=lambda x: x[0])
    all_opportunities.sort(key=lambda x: x[0])
    
    sealed_start = int(len(all_issued) * 0.8)
    
    metrics = compute_metrics(all_issued, all_opportunities, sealed_start)
    if metrics is None:
        print("INSUFFICIENT=1")
        return 0
    
    print(f"ISSUED={metrics['ISSUED']}")
    print(f"OPPORTUNITIES={metrics['OPPORTUNITIES']}")
    print(f"PRECISION={metrics['PRECISION']:.6f}")
    print(f"BASE_RATE={metrics['BASE_RATE']:.6f}")
    print(f"DISTINCT_DAYS={metrics['DISTINCT_DAYS']}")
    print(f"EFFECTIVE_N={metrics['EFFECTIVE_N']:.6f}")
    print(f"SEALED_PRECISION={metrics['SEALED_PRECISION']:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())
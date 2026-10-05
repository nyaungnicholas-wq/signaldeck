# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 709
# cycle_index: 36
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict
from datetime import datetime, timezone

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def load_insider_purchases(conn):
    cur = conn.execute("""
        SELECT symbol_id, filed_ts, shares, price, value, insider, title
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    return cur.fetchall()

def ts_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def get_symbol_ids_with_insider_buys(conn):
    cur = conn.execute("SELECT DISTINCT symbol_id FROM insider_trades WHERE code = 'P'")
    return [row[0] for row in cur.fetchall()]

def load_bars_for_symbols(conn, symbol_ids):
    if not symbol_ids:
        return {}
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_by_sym = defaultdict(list)
    for sym_id, ts, close in cur:
        bars_by_sym[sym_id].append((ts, close))
    return bars_by_sym

def compute_market_returns(bars_by_sym):
    ts_to_rets = defaultdict(list)
    for sym_id, bars in bars_by_sym.items():
        if len(bars) < 2:
            continue
        for i in range(1, len(bars)):
            prev_ts, prev_close = bars[i-1]
            curr_ts, curr_close = bars[i]
            if prev_close > 0:
                ret = math.log(curr_close / prev_close)
                ts_to_rets[curr_ts].append(ret)
    
    market_ret = {}
    for ts, rets in ts_to_rets.items():
        market_ret[ts] = sum(rets) / len(rets)
    return market_ret

def compute_market_index(market_ret):
    sorted_ts = sorted(market_ret.keys())
    if not sorted_ts:
        return {}
    idx = {sorted_ts[0]: 100.0}
    for i in range(1, len(sorted_ts)):
        idx[sorted_ts[i]] = idx[sorted_ts[i-1]] * (1 + market_ret[sorted_ts[i]])
    return idx

def process_symbol(sym_id, bars, market_idx, market_ret_ts_sorted):
    n = len(bars)
    if n < 253 + 21:
        return []
    
    ts_list = [b[0] for b in bars]
    close_list = [b[1] for b in bars]
    
    # Compute daily log returns
    rets = [0.0] * n
    for i in range(1, n):
        if close_list[i-1] > 0:
            rets[i] = math.log(close_list[i] / close_list[i-1])
    
    # Rolling 252-day volatility and return
    # Use Welford's online algorithm for variance
    vol_252 = [0.0] * n
    ret_252 = [0.0] * n
    
    # Initialize first window
    window_rets = rets[1:253]  # indices 1..252 (252 returns)
    mean = sum(window_rets) / 252
    m2 = sum((r - mean) ** 2 for r in window_rets)
    vol_252[252] = math.sqrt(m2 / 251) if m2 > 0 else 0.0
    ret_252[252] = close_list[252] / close_list[0] - 1
    
    # Roll forward
    for i in range(253, n - 21):
        # Remove rets[i-252], add rets[i-1]
        old_r = rets[i-252]
        new_r = rets[i-1]
        
        delta = new_r - old_r
        mean += delta / 252
        
        # Update m2: m2_new = m2_old + (new_r - mean_new)^2 - (old_r - mean_old)^2
        # But mean changed, so use the formula: m2 += (new_r - old_r) * (new_r - mean_new + old_r - mean_old)
        # Simpler: recompute for stability (252 is small)
        window_rets = rets[i-251:i+1]  # 252 returns ending at i-1
        mean = sum(window_rets) / 252
        m2 = sum((r - mean) ** 2 for r in window_rets)
        vol_252[i] = math.sqrt(m2 / 251) if m2 > 0 else 0.0
        ret_252[i] = close_list[i] / close_list[i-252] - 1
    
    # Market 252-day return for each ts
    mkt_ret_252 = [None] * n
    ts_to_idx = {ts: i for i, ts in enumerate(market_ret_ts_sorted)}
    for i in range(252, n):
        ts = ts_list[i]
        if ts not in ts_to_idx:
            continue
        idx = ts_to_idx[ts]
        if idx < 252:
            continue
        ts_252 = market_ret_ts_sorted[idx - 252]
        mkt_ret_252[i] = market_idx[ts] / market_idx[ts_252] - 1
    
    # Build volatility history for percentile (expanding window)
    # For each day i, we need percentile of vol_252[252:i] 
    vol_history = []
    results = []  # (ts, vol, ret, mkt_ret, vol_pct)
    
    for i in range(252, n - 21):
        v = vol_252[i]
        r = ret_252[i]
        m = mkt_ret_252[i]
        if v == 0 or m is None:
            vol_history.append(v)
            continue
        
        # Compute percentile in expanding history (excluding current)
        if len(vol_history) >= 50:
            count_le = sum(1 for hv in vol_history if hv <= v)
            pct = count_le / len(vol_history)
        else:
            pct = 1.0
        
        results.append((ts_list[i], v, r, m, pct))
        vol_history.append(v)
    
    return results

def find_decision_bar(ts_list, filed_ts):
    """Binary search for latest bar ts <= filed_ts"""
    lo, hi = 0, len(ts_list) - 1
    decision_ts = None
    decision_idx = -1
    while lo <= hi:
        mid = (lo + hi) // 2
        if ts_list[mid] <= filed_ts:
            decision_ts = ts_list[mid]
            decision_idx = mid
            lo = mid + 1
        else:
            hi = mid - 1
    return decision_ts, decision_idx

def main():
    conn = connect_ro()
    
    print("Loading insider purchases...", file=sys.stderr)
    insider_buys = load_insider_purchases(conn)
    if not insider_buys:
        print("INSUFFICIENT=1")
        return
    
    symbol_ids = get_symbol_ids_with_insider_buys(conn)
    print(f"  {len(symbol_ids)} symbols with insider buys", file=sys.stderr)
    
    print("Loading bars for relevant symbols...", file=sys.stderr)
    bars_by_sym = load_bars_for_symbols(conn, symbol_ids)
    print(f"  Loaded bars for {len(bars_by_sym)} symbols", file=sys.stderr)
    
    if not bars_by_sym:
        print("INSUFFICIENT=1")
        return
    
    print("Computing market returns...", file=sys.stderr)
    market_ret = compute_market_returns(bars_by_sym)
    market_idx = compute_market_index(market_ret)
    market_ret_ts_sorted = sorted(market_ret.keys())
    
    print("Processing symbols...", file=sys.stderr)
    # Precompute per-symbol metrics
    sym_metrics = {}
    for sym_id, bars in bars_by_sym.items():
        metrics = process_symbol(sym_id, bars, market_idx, market_ret_ts_sorted)
        if metrics:
            sym_metrics[sym_id] = {
                'metrics': {ts: (vol, ret, mkt, pct) for ts, vol, ret, mkt, pct in metrics},
                'ts_list': [b[0] for b in bars],
                'close_map': {b[0]: b[1] for b in bars}
            }
    
    print(f"  {len(sym_metrics)} symbols with sufficient history", file=sys.stderr)
    
    # Process each insider purchase
    calls = []
    opportunities = 0
    
    for sym_id, filed_ts, shares, price, value, insider, title in insider_buys:
        if sym_id not in sym_metrics:
            continue
        
        opportunities += 1
        data = sym_metrics[sym_id]
        ts_list = data['ts_list']
        close_map = data['close_map']
        metrics = data['metrics']
        
        # Find decision bar (on or before filed_ts)
        decision_ts, decision_idx = find_decision_bar(ts_list, filed_ts)
        if decision_ts is None or decision_idx < 253:  # need at least 252 prior + signal day
            continue
        
        # Signal uses previous day's metrics (avoid lookahead)
        signal_idx = decision_idx - 1
        signal_ts = ts_list[signal_idx]
        
        if signal_ts not in metrics:
            continue
        
        vol, ret_252, mkt_ret_252, vol_pct = metrics[signal_ts]
        
        # Check conditions
        # 1. Vol in bottom quartile (pct <= 0.25)
        if vol_pct > 0.25:
            continue
        
        # 2. Stock underperformed market by >= 10pp over 252 days
        if ret_252 - mkt_ret_252 > -0.10:  # underperformance <= -10%
            continue
        
        # 3. Price >= $5 at signal_ts
        entry_price = close_map.get(signal_ts)
        if entry_price is None or entry_price < 5.0:
            continue
        
        # 4. Need 21 forward trading days from decision_idx
        if decision_idx + 21 >= len(ts_list):
            continue
        exit_ts = ts_list[decision_idx + 21]
        exit_price = close_map.get(exit_ts)
        if exit_price is None:
            continue
        
        fwd_ret = exit_price / entry_price - 1
        hit = 1 if fwd_ret > 0 else 0
        
        calls.append((filed_ts, sym_id, fwd_ret, hit))
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    calls.sort(key=lambda x: x[0])
    
    n = len(calls)
    split_idx = int(n * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]
    
    def compute_stats(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(c[3] for c in call_list)
        precision = hits / issued
        base_rate = precision
        distinct_days = len(set(ts_to_date(c[0]) for c in call_list))
        if distinct_days > 0:
            design_effect = max(1.0, issued / distinct_days)
            effective_n = issued / design_effect
        else:
            effective_n = 0.0
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    all_issued, all_hits, all_prec, all_br, all_days, all_eff = compute_stats(calls)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_eff = compute_stats(sealed_calls)
    
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_br:.6f}")
    print(f"DISTINCT_DAYS={all_days}")
    print(f"EFFECTIVE_N={all_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()
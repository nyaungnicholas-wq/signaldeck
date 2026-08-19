# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 833
# cycle_index: 29
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

def get_active_stock_symbols(conn):
    cur = conn.execute("SELECT id, symbol FROM symbols WHERE market='stocks' AND active=1")
    return {row[0]: row[1] for row in cur.fetchall()}

def get_daily_bars(conn, symbol_ids):
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, ts, close FROM bars
        WHERE tf='1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, list(symbol_ids))
    bars_by_symbol = defaultdict(list)
    for sid, ts, close in cur.fetchall():
        bars_by_symbol[sid].append((ts, close))
    return bars_by_symbol

def get_officer_purchases(conn, symbol_ids):
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, filed_ts, title FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code='P'
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%President%')
        ORDER BY symbol_id, filed_ts
    """, list(symbol_ids))
    purchases_by_symbol = defaultdict(list)
    for sid, filed_ts, title in cur.fetchall():
        purchases_by_symbol[sid].append((filed_ts, title))
    return purchases_by_symbol

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def compute_rolling_low(closes, window):
    lows = []
    for i in range(len(closes)):
        if i < window - 1:
            lows.append(None)
        else:
            lows.append(min(closes[i-window+1:i+1]))
    return lows

def compute_realized_vol(closes, window):
    vols = []
    for i in range(len(closes)):
        if i < window:
            vols.append(None)
        else:
            rets = []
            for j in range(i-window+1, i+1):
                if closes[j-1] > 0:
                    rets.append((closes[j] - closes[j-1]) / closes[j-1])
            if len(rets) >= 2:
                mean_ret = sum(rets) / len(rets)
                var = sum((r - mean_ret)**2 for r in rets) / (len(rets) - 1)
                vols.append(var**0.5)
            else:
                vols.append(None)
    return vols

def compute_rolling_percentile(values, window, percentile):
    result = []
    for i in range(len(values)):
        if i < window - 1:
            result.append(None)
        else:
            window_vals = [v for v in values[i-window+1:i+1] if v is not None]
            if len(window_vals) >= 10:
                window_vals.sort()
                idx = int(len(window_vals) * percentile / 100)
                idx = min(idx, len(window_vals) - 1)
                result.append(window_vals[idx])
            else:
                result.append(None)
    return result

def find_bar_index(bars, target_ts):
    for i, (ts, _) in enumerate(bars):
        if ts >= target_ts:
            return i
    return -1

def main():
    conn = connect()
    
    symbols = get_active_stock_symbols(conn)
    symbol_ids = list(symbols.keys())
    
    bars_by_symbol = get_daily_bars(conn, symbol_ids)
    purchases_by_symbol = get_officer_purchases(conn, symbol_ids)
    
    signals = []
    
    for sid in symbol_ids:
        bars = bars_by_symbol.get(sid, [])
        purchases = purchases_by_symbol.get(sid, [])
        
        if len(bars) < 756 or not purchases:
            continue
        
        closes = [c for _, c in bars]
        timestamps = [ts for ts, _ in bars]
        
        low_252 = compute_rolling_low(closes, 252)
        vol_20 = compute_realized_vol(closes, 20)
        vol_756_p5 = compute_rolling_percentile(vol_20, 756, 5)
        
        for filed_ts, title in purchases:
            signal_date = ts_to_date(filed_ts)
            signal_ts = int(datetime.combine(signal_date, datetime.min.time()).timestamp())
            
            idx = find_bar_index(bars, signal_ts)
            if idx < 0 or idx >= len(bars):
                continue
            
            if low_252[idx] is None or vol_20[idx] is None or vol_756_p5[idx] is None:
                continue
            
            close = closes[idx]
            if close > 1.10 * low_252[idx]:
                continue
            if vol_20[idx] > vol_756_p5[idx]:
                continue
            
            if idx + 63 >= len(bars):
                continue
            
            fwd_close = closes[idx + 63]
            fwd_return = (fwd_close - close) / close
            hit = 1 if fwd_return > 0 else 0
            
            signals.append((signal_date, sid, hit, fwd_return))
    
    if not signals:
        print("INSUFFICIENT=1")
        return
    
    signals.sort(key=lambda x: x[0])
    
    n = len(signals)
    split_idx = int(n * 0.8)
    in_sample = signals[:split_idx]
    sealed = signals[split_idx:]
    
    def compute_metrics(signal_list):
        if not signal_list:
            return 0, 0, 0, 0, 0, 0
        issued = len(signal_list)
        hits = sum(s[2] for s in signal_list)
        precision = hits / issued
        base_rate = precision
        distinct_days = len(set(s[0] for s in signal_list))
        
        y = [s[2] for s in signal_list]
        p = precision
        n_obs = issued
        iid_var = p * (1 - p) / n_obs if n_obs > 1 else 0
        
        day_sums = defaultdict(float)
        for s in signal_list:
            day_sums[s[0]] += s[2] - p
        
        G = distinct_days
        if G > 1 and n_obs > 1:
            cluster_var = sum(v*v for v in day_sums.values()) / (n_obs * n_obs)
            cluster_var *= (n_obs / (n_obs - 1)) * (G / (G - 1))
            design_effect = cluster_var / iid_var if iid_var > 0 else 1
        else:
            design_effect = 1
        
        effective_n = n_obs / design_effect if design_effect > 0 else n_obs
        
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    iss_in, hits_in, prec_in, br_in, dd_in, en_in = compute_metrics(in_sample)
    iss_se, hits_se, prec_se, br_se, dd_se, en_se = compute_metrics(sealed)
    
    if iss_in < 30 or dd_in < 10:
        print("INSUFFICIENT=1")
        return
    
    print(f"ISSUED={iss_in}")
    print(f"OPPORTUNITIES={iss_in}")
    print(f"PRECISION={prec_in:.6f}")
    print(f"BASE_RATE={br_in:.6f}")
    print(f"DISTINCT_DAYS={dd_in}")
    print(f"EFFECTIVE_N={en_in:.2f}")
    print(f"SEALED_PRECISION={prec_se:.6f}")

if __name__ == '__main__':
    main()
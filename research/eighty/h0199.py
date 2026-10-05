#!/usr/bin/env python3
import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = "file:data/signaldeck.db?mode=ro"

def get_connection():
    return sqlite3.connect(DB_PATH, uri=True)

def days_between(ts1, ts2):
    dt1 = datetime.utcfromtimestamp(ts1)
    dt2 = datetime.utcfromtimestamp(ts2)
    return (dt2 - dt1).days

def get_date_str(ts):
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def main():
    conn = get_connection()
    c = conn.cursor()
    
    # Get all symbols with insider trades and daily bars
    c.execute("""
        SELECT DISTINCT it.symbol_id 
        FROM insider_trades it
        JOIN bars b ON it.symbol_id = b.symbol_id AND b.tf = '1d'
    """)
    symbol_ids = [row[0] for row in c.fetchall()]
    
    if len(symbol_ids) == 0:
        print("INSUFFICIENT=1")
        return
    
    # Preload all daily bars for relevant symbols
    c.execute("""
        SELECT symbol_id, ts, close, volume 
        FROM bars 
        WHERE tf = '1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?' * len(symbol_ids))), symbol_ids)
    all_bars = c.fetchall()
    bars_by_symbol = defaultdict(list)
    for sid, ts, close, vol in all_bars:
        bars_by_symbol[sid].append((ts, close, vol))
    
    # Preload insider trades (sales and purchases)
    c.execute("""
        SELECT symbol_id, code, shares, filed_ts 
        FROM insider_trades 
        WHERE code IN ('S', 'P')
    """)
    insider_data = defaultdict(lambda: {'S': [], 'P': []})
    for sid, code, shares, filed_ts in c.fetchall():
        insider_data[sid][code].append((filed_ts, shares))
    
    # Preload prediction outcomes for horizon 20
    c.execute("""
        SELECT symbol_id, ts, up, fwd_return 
        FROM prediction_outcomes 
        WHERE horizon = 20
    """)
    labels = defaultdict(dict)
    for sid, ts, up, fwd_return in c.fetchall():
        labels[sid][ts] = (up, fwd_return)
    
    # Collect all valid decision points (opportunities)
    opportunities = []  # (symbol_id, ts, date_str)
    all_decisions_by_date = defaultdict(list)  # date_str -> [(symbol_id, ts)]
    
    for sid in symbol_ids:
        bars = bars_by_symbol.get(sid, [])
        if len(bars) < 253:  # Need at least 252 prior sessions
            continue
            
        insider_sales = insider_data.get(sid, {}).get('S', [])
        insider_purchases = insider_data.get(sid, {}).get('P', [])
        
        # Sort insider transactions by filed_ts
        insider_sales.sort(key=lambda x: x[0])
        insider_purchases.sort(key=lambda x: x[0])
        
        # Process each potential T (starting from index 252)
        for i in range(252, len(bars)):
            t_ts, t_close, t_vol = bars[i]
            date_str = get_date_str(t_ts)
            
            # Universe criteria: close >= $5
            if t_close < 5:
                continue
            
            # ADTV >= $10M over prior 60 sessions
            start_idx = max(0, i - 60)
            recent_bars = bars[start_idx:i]
            if len(recent_bars) < 60:
                continue
            
            adtv = sum(close * vol for _, close, vol in recent_bars) / len(recent_bars)
            if adtv < 10_000_000:
                continue
            
            # Check 20-session volatility (for abstain condition later)
            if i >= 20:
                vol_window = bars[i-20:i]
                returns = [vol_window[j][1] / vol_window[j-1][1] - 1 
                          for j in range(1, len(vol_window))]
                if len(returns) > 1:
                    mean_ret = sum(returns) / len(returns)
                    vol_20 = (sum((r - mean_ret) ** 2 for r in returns) / (len(returns) - 1)) ** 0.5
                else:
                    vol_20 = 0
            else:
                vol_20 = 0
            
            opportunities.append((sid, t_ts, date_str, vol_20, i))
            all_decisions_by_date[date_str].append((sid, t_ts, vol_20, i))
    
    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Compute volatility deciles cross-sectionally for each date
    vol_deciles = {}
    for date_str, decisions in all_decisions_by_date.items():
        if len(decisions) < 10:
            continue
        vols = [d[2] for d in decisions]  # vol_20
        vols_sorted = sorted(vols)
        idx_90 = int(len(vols_sorted) * 0.9)
        vol_90 = vols_sorted[idx_90] if idx_90 < len(vols_sorted) else vols_sorted[-1]
        vol_deciles[date_str] = vol_90
    
    # Sort opportunities by date
    opportunities.sort(key=lambda x: x[1])  # sort by ts
    
    # Split into sealed era (most recent 20%)
    n_total = len(opportunities)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed
    sealed_cutoff_ts = opportunities[n_train][1] if n_train < n_total else opportunities[-1][1]
    
    # Issue calls
    issued_calls = []  # (symbol_id, ts, hit, is_sealed)
    base_rate_counts = []  # outcomes for base rate calculation
    last_call_idx = defaultdict(lambda: -1000)  # symbol_id -> last issued call index
    
    for idx, (sid, t_ts, date_str, vol_20, bar_idx) in enumerate(opportunities):
        is_sealed = t_ts >= sealed_cutoff_ts
        
        # Check abstain conditions first
        abstain = False
        
        # Volatility in top cross-sectional decile
        vol_90 = vol_deciles.get(date_str, 1e9)
        if vol_20 > vol_90:
            abstain = True
        
        # Call issued for same symbol in prior 20 trading days
        if bar_idx - last_call_idx[sid] <= 20:
            abstain = True
        
        if abstain:
            continue
        
        # Check entry conditions
        # Insider sales in 10 calendar days ending day before T
        t_date = datetime.utcfromtimestamp(t_ts).date()
        window_start = t_date - timedelta(days=10)
        window_end = t_date - timedelta(days=1)
        
        sales_in_window = []
        for filed_ts, shares in insider_data[sid]['S']:
            filed_date = datetime.utcfromtimestamp(filed_ts).date()
            if window_start <= filed_date <= window_end:
                sales_in_window.append((filed_ts, shares))
        
        if len(sales_in_window) < 2:
            continue
        
        # Aggregate value: shares * T close >= $1M
        total_value = sum(shares * t_close for _, shares in sales_in_window)
        if total_value < 1_000_000:
            continue
        
        # 20-session return between 10% and 30%
        if bar_idx >= 20:
            prev_close = bars_by_symbol[sid][bar_idx - 20][1]
            ret_20 = (t_close / prev_close) - 1
            if not (0.10 <= ret_20 <= 0.30):
                continue
        else:
            continue
        
        # T's close above 50-session SMA
        sma_window = bars_by_symbol[sid][max(0, bar_idx-49):bar_idx+1]
        if len(sma_window) < 50:
            continue
        sma_50 = sum(close for _, close, _ in sma_window) / len(sma_window)
        if t_close <= sma_50:
            continue
        
        # T's volume >= 60-session median
        vol_window = bars_by_symbol[sid][max(0, bar_idx-60):bar_idx]
        if len(vol_window) < 60:
            continue
        volumes = [vol for _, _, vol in vol_window]
        volumes.sort()
        median_vol = volumes[len(volumes)//2] if len(volumes) % 2 == 1 else (volumes[len(volumes)//2 - 1] + volumes[len(volumes)//2]) / 2
        if t_vol < median_vol:
            continue
        
        # Check abstain: open-market purchase >= $100k in same window
        purchases_in_window = []
        for filed_ts, shares in insider_data[sid]['P']:
            filed_date = datetime.utcfromtimestamp(filed_ts).date()
            if window_start <= filed_date <= window_end:
                purchases_in_window.append((filed_ts, shares))
        
        for _, shares in purchases_in_window:
            if shares * t_close >= 100_000:
                abstain = True
                break
        
        if abstain:
            continue
        
        # Issue DOWN call
        # Get label
        label_info = labels[sid].get(t_ts)
        if label_info is None:
            continue  # No label available
        
        up, fwd_return = label_info
        hit = (up == 0) or (fwd_return is not None and fwd_return < 0)
        
        issued_calls.append((sid, t_ts, hit, is_sealed))
        last_call_idx[sid] = bar_idx
    
    # Calculate metrics
    if len(issued_calls) == 0:
        print("INSUFFICIENT=1")
        return
    
    # Filter issued calls that have labels
    labeled_calls = [(sid, ts, hit, sealed) for sid, ts, hit, sealed in issued_calls]
    
    total_issued = len(labeled_calls)
    total_hits = sum(1 for _, _, hit, _ in labeled_calls if hit)
    
    # Distinct days in issued calls
    distinct_days = len(set(ts for _, ts, _, _ in labeled_calls))
    
    # Base rate within issued subset
    base_rate = total_hits / total_issued
    
    # Design effect calculation
    day_counts = defaultdict(int)
    for _, ts, _, _ in labeled_calls:
        day_str = get_date_str(ts)
        day_counts[day_str] += 1
    
    sum_sq_counts = sum(cnt ** 2 for cnt in day_counts.values())
    design_effect = sum_sq_counts / total_issued if total_issued > 0 else 1.0
    effective_n = total_issued / design_effect if design_effect > 0 else total_issued
    
    # Sealed era precision
    sealed_calls = [(sid, ts, hit) for sid, ts, hit, sealed in labeled_calls if sealed]
    if sealed_calls:
        sealed_hits = sum(1 for _, _, hit in sealed_calls if hit)
        sealed_precision = sealed_hits / len(sealed_calls)
    else:
        sealed_precision = 0.0
    
    # Print results
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={total_hits/total_issued:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 489
# cycle_index: 19
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
import math
from collections import defaultdict

def main():
    db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
    cur = db.cursor()
    
    # Get insider trades (Form 4 open-market purchases) within short volume date range
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code='P'
        AND filed_ts >= strftime('%s', '2026-05-20')
        AND filed_ts <= strftime('%s', '2026-07-31')
        ORDER BY filed_ts
    """)
    insider_trades = cur.fetchall()
    
    if not insider_trades:
        print("INSUFFICIENT=1")
        return
    
    # Get short volume data for all relevant symbols
    cur.execute("""
        SELECT symbol_id, day, short_vol, total_vol, short_pct
        FROM short_volume
        WHERE symbol_id IN (SELECT DISTINCT symbol_id FROM insider_trades)
        ORDER BY symbol_id, day
    """)
    short_vol_data = cur.fetchall()
    
    if not short_vol_data:
        print("INSUFFICIENT=1")
        return
    
    # Organize short volume by symbol and date
    short_by_symbol = defaultdict(dict)
    for symbol_id, day, short_vol, total_vol, short_pct in short_vol_data:
        short_by_symbol[symbol_id][day] = (short_vol, total_vol, short_pct)
    
    # Get bars (1d) for relevant symbols for 20-day volume calculation and forward return
    # We need bars up to T+5 days for each trade, so get until 2026-08-05
    cur.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf='1d'
        AND symbol_id IN (SELECT DISTINCT symbol_id FROM insider_trades)
        AND ts >= strftime('%s', '2026-05-01')
        AND ts <= strftime('%s', '2026-08-05')
        ORDER BY symbol_id, ts
    """)
    bars_data = cur.fetchall()
    
    if not bars_data:
        print("INSUFFICIENT=1")
        return
    
    # Organize bars by symbol and date
    bars_by_symbol = defaultdict(dict)
    for symbol_id, ts, open_, high, low, close, volume in bars_data:
        dt = datetime.datetime.utcfromtimestamp(ts)
        day_str = dt.strftime('%Y-%m-%d')
        bars_by_symbol[symbol_id][day_str] = (open_, high, low, close, volume)
    
    # Process each insider trade
    calls = []
    
    for symbol_id, filed_ts in insider_trades:
        filed_dt = datetime.datetime.utcfromtimestamp(filed_ts)
        t_date = filed_dt.strftime('%Y-%m-%d')
        
        # Calculate T-1 (previous trading day)
        t_minus_1 = (filed_dt - datetime.timedelta(days=1)).strftime('%Y-%m-%d')
        
        # Skip if no short volume data for T-1
        if symbol_id not in short_by_symbol or t_minus_1 not in short_by_symbol[symbol_id]:
            continue
        
        # Check short volume history (need at least 20 days)
        symbol_short = short_by_symbol[symbol_id]
        short_dates = sorted(symbol_short.keys())
        
        # Find index of T-1 in sorted dates
        if t_minus_1 not in short_dates:
            continue
            
        t_minus_1_idx = short_dates.index(t_minus_1)
        if t_minus_1_idx < 19:  # Need at least 20 days before T-1
            continue
        
        # Calculate 20-day rolling distribution for short volume
        window = [symbol_short[short_dates[i]][0] for i in range(t_minus_1_idx-19, t_minus_1_idx+1)]
        t_minus_1_short_vol = symbol_short[t_minus_1][0]
        
        # Check if in top 10%
        threshold = sorted(window)[int(math.floor(0.9 * len(window)))]
        if t_minus_1_short_vol < threshold:
            continue
        
        # Check 20-day average daily volume from bars
        if symbol_id not in bars_by_symbol:
            continue
            
        symbol_bars = bars_by_symbol[symbol_id]
        bar_dates = sorted(symbol_bars.keys())
        
        # Find bars up to T date
        bar_dates_up_to_T = [d for d in bar_dates if d <= t_date]
        if len(bar_dates_up_to_T) < 20:
            continue
            
        # Get last 20 bars up to T
        last_20_volumes = [symbol_bars[d][4] for d in bar_dates_up_to_T[-20:]]
        avg_vol = sum(last_20_volumes) / len(last_20_volumes)
        
        if avg_vol < 100000:
            continue
        
        # Calculate forward 5-day return
        bar_dates_after_T = [d for d in bar_dates if d > t_date]
        if len(bar_dates_after_T) < 5:
            continue
        
        close_T = symbol_bars[t_date][3]  # close on T
        close_T_plus_5 = symbol_bars[bar_dates_after_T[4]][3]  # close on T+5 (5 trading days later)
        
        forward_return = (close_T_plus_5 - close_T) / close_T
        hit = 1 if forward_return > 0 else 0
        
        calls.append((filed_dt, symbol_id, hit, forward_return))
    
    # Sort by time
    calls.sort(key=lambda x: x[0])
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Split into 80/20 by time
    n_total = len(calls)
    n_split = int(math.floor(n_total * 0.8))
    in_sample = calls[:n_split]
    sealed = calls[n_split:]
    
    # Calculate metrics for in-sample
    issued = len(in_sample)
    opportunities = len(calls)  # All decision points considered
    hits = sum(hit for _, _, hit, _ in in_sample)
    precision = hits / issued if issued > 0 else 0
    base_rate = hits / issued if issued > 0 else 0  # Base rate within issued subset
    
    # Calculate distinct days
    issued_days = set()
    for dt, _, _, _ in in_sample:
        issued_days.add(dt.date())
    distinct_days = len(issued_days)
    
    # Calculate design effect (cluster by day)
    day_counts = defaultdict(int)
    for dt, _, _, _ in in_sample:
        day_counts[dt.date()] += 1
    
    # ICC approximation: variance of cluster sizes / mean cluster size
    mean_cluster_size = issued / distinct_days if distinct_days > 0 else 0
    variance_cluster = sum((count - mean_cluster_size)**2 for count in day_counts.values()) / len(day_counts) if len(day_counts) > 0 else 0
    design_effect = 1 + (mean_cluster_size - 1) * (variance_cluster / (mean_cluster_size**2)) if mean_cluster_size > 0 else 1
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Calculate sealed metrics
    sealed_issued = len(sealed)
    sealed_hits = sum(hit for _, _, hit, _ in sealed)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
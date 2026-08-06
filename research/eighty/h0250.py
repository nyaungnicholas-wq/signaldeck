import sqlite3

def main():
    db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    db.row_factory = sqlite3.Row
    
    # Get all daily bars with enough history
    cursor = db.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    all_bars = cursor.fetchall()
    
    # Group by symbol
    symbols_bars = {}
    for row in all_bars:
        sym = row['symbol_id']
        if sym not in symbols_bars:
            symbols_bars[sym] = []
        symbols_bars[sym].append(row)
    
    # Get all prediction outcomes for horizon=20
    cursor = db.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 20
    """)
    outcomes = {}
    for row in cursor.fetchall():
        key = (row['symbol_id'], row['ts'])
        outcomes[key] = row['up']  # 1 for up, 0 for down
    
    # Get all symbols with market info
    cursor = db.execute("SELECT id, symbol, market FROM symbols")
    symbol_info = {row['id']: (row['symbol'], row['market']) for row in cursor.fetchall()}
    
    # Prepare evaluation periods
    all_ts = sorted(set(row['ts'] for row in all_bars))
    n_days = len(all_ts)
    cutoff_idx = int(n_days * 0.8)
    sealed_ts = set(all_ts[cutoff_idx:])
    
    opportunities = []
    issued_calls = []
    
    # Process each symbol
    for sym_id, bars in symbols_bars.items():
        if len(bars) < 253:  # Need at least 252 prior + T
            continue
        
        # Convert to list of dicts for easier access
        bar_list = [dict(row) for row in bars]
        
        # Check for minimum volume requirement: avg daily dollar vol >= $5M over T-60..T-1
        for i in range(252, len(bar_list)):  # Start from day 253 (index 252)
            T = bar_list[i]
            T_ts = T['ts']
            
            # Check if we have T-1
            if i < 1:
                continue
            T_minus_1 = bar_list[i-1]
            
            # Check close >= $5 at T
            if T['close'] < 5.0:
                continue
            
            # Check we have enough prior data
            if i < 251:  # Need 252 prior sessions
                continue
            
            # Check volume requirement: average over T-60..T-1
            if i < 60:
                continue
            vol_window = bar_list[i-60:i]
            avg_vol = sum(bar['volume'] for bar in vol_window) / 60
            avg_dollar_vol = avg_vol * T_minus_1['close']  # Approximate
            if avg_dollar_vol < 5_000_000:
                continue
            
            # Calculate gap up
            gap_pct = (T['open'] - T_minus_1['close']) / T_minus_1['close']
            
            # Check gap >= 4.0%
            if gap_pct < 0.04:
                continue
            
            # Check close below prior close
            if T['close'] >= T_minus_1['close']:
                continue
            
            # Check close in bottom half of high-low range
            mid_range = (T['high'] + T['low']) / 2.0
            if T['close'] >= mid_range:
                continue
            
            # Check 20-day realized volatility not in top decile
            # Calculate 20-day realized volatility
            if i < 19:
                continue
            window = bar_list[i-19:i+1]  # 20 days including T
            closes = [bar['close'] for bar in window]
            returns = []
            for j in range(1, len(closes)):
                returns.append((closes[j] - closes[j-1]) / closes[j-1])
            
            # Simple volatility measure: std dev of returns
            mean_ret = sum(returns) / len(returns)
            var = sum((r - mean_ret)**2 for r in returns) / (len(returns) - 1)
            vol_20 = var ** 0.5
            
            # We'll collect all vols for cross-sectional comparison later
            # For now, mark as candidate
            candidate = {
                'sym_id': sym_id,
                'ts': T_ts,
                'vol_20': vol_20,
                'in_sealed': T_ts in sealed_ts,
                'T_idx': i
            }
            opportunities.append(candidate)
    
    # Need cross-sectional volatility ranking for each day
    # Group opportunities by ts
    by_ts = {}
    for cand in opportunities:
        ts = cand['ts']
        if ts not in by_ts:
            by_ts[ts] = []
        by_ts[ts].append(cand)
    
    # Calculate cross-sectional 80th percentile volatility for each day
    for ts, cands in by_ts.items():
        vols = [c['vol_20'] for c in cands]
        vols.sort()
        p80_idx = int(len(vols) * 0.8)
        if p80_idx < len(vols):
            p80_vol = vols[p80_idx]
        else:
            p80_vol = float('inf')
        
        # Filter out top 10% volatility
        for c in cands:
            c['vol_ok'] = c['vol_20'] <= p80_vol
    
    # Now filter candidates based on volatility condition
    filtered = [c for c in opportunities if c.get('vol_ok', False)]
    
    # Sort by timestamp
    filtered.sort(key=lambda x: x['ts'])
    
    # Check for repeated calls within 20 trading days
    final_issued = []
    last_call_ts = {}  # sym_id -> last call timestamp
    
    for cand in filtered:
        sym_id = cand['sym_id']
        ts = cand['ts']
        
        # Check if same symbol had call in prior 20 trading days
        if sym_id in last_call_ts:
            last_ts = last_call_ts[sym_id]
            # Find index of ts and last_ts in all_ts
            idx_current = all_ts.index(ts)
            idx_last = all_ts.index(last_ts)
            if idx_current - idx_last <= 20:
                continue
        
        # Check outcome
        key = (sym_id, ts)
        if key not in outcomes:
            continue
        
        up = outcomes[key]
        hit = (up == 0)  # DOWN call, so hit if down
        
        final_issued.append({
            'sym_id': sym_id,
            'ts': ts,
            'hit': hit,
            'in_sealed': cand['in_sealed']
        })
        last_call_ts[sym_id] = ts
    
    # If insufficient observations
    if len(final_issued) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Split into sealed and non-sealed
    sealed_issued = [c for c in final_issued if c['in_sealed']]
    non_sealed_issued = [c for c in final_issued if not c['in_sealed']]
    
    # Count distinct days for non-sealed
    distinct_days_non_sealed = len(set(c['ts'] for c in non_sealed_issued))
    distinct_days_sealed = len(set(c['ts'] for c in sealed_issued))
    
    # Calculate metrics for non-sealed
    issued_non_sealed = len(non_sealed_issued)
    hits_non_sealed = sum(1 for c in non_sealed_issued if c['hit'])
    precision_non_sealed = hits_non_sealed / issued_non_sealed if issued_non_sealed > 0 else 0
    
    # Base rate of DOWN within issued subset
    base_rate_non_sealed = precision_non_sealed  # Since DOWN is our predicted class
    
    # Design effect calculation (simplified: cluster by day)
    # Group by day
    day_groups = {}
    for c in non_sealed_issued:
        day = c['ts']
        if day not in day_groups:
            day_groups[day] = []
        day_groups[day].append(c['hit'])
    
    # Calculate intracluster correlation
    n_clusters = len(day_groups)
    if n_clusters > 1:
        cluster_means = [sum(hits)/len(hits) for hits in day_groups.values()]
        cluster_sizes = [len(hits) for hits in day_groups.values()]
        
        overall_mean = sum(cluster_means) / n_clusters
        m = sum(cluster_sizes) / n_clusters  # Average cluster size
        
        # Variance between clusters
        var_between = sum(s * (m - overall_mean)**2 for s, m in zip(cluster_means, cluster_means)) / (n_clusters - 1)
        # Variance within clusters (average of p*(1-p) for each cluster)
        var_within = sum(sum((1 if h else 0) - m for h in hits) ** 2 / len(hits) 
                       for hits, m in zip(day_groups.values(), cluster_means)) / (sum(cluster_sizes) - n_clusters)
        
        icc = var_between / (var_between + var_within) if (var_between + var_within) > 0 else 0
        design_effect = 1 + (m - 1) * icc
    else:
        design_effect = 1
    
    effective_n = issued_non_sealed / design_effect
    
    # SEALED metrics
    issued_sealed = len(sealed_issued)
    hits_sealed = sum(1 for c in sealed_issued if c['hit'])
    precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0
    
    # Print required output
    print(f"ISSUED={issued_non_sealed}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_non_sealed:.6f}")
    print(f"BASE_RATE={base_rate_non_sealed:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_non_sealed}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={precision_sealed:.6f}")
    
    db.close()

if __name__ == "__main__":
    main()
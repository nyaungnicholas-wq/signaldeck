import sqlite3
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get all daily bars
    cur.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars WHERE tf='1d'
        ORDER BY symbol_id, ts
    """)
    bars_data = cur.fetchall()
    
    # Get StockTwits data
    cur.execute("""
        SELECT symbol_id, ts, bullish, bearish
        FROM stocktwits_sentiment
        ORDER BY symbol_id, ts
    """)
    st_data = cur.fetchall()
    
    # Get prediction outcomes for horizon 20
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon=20
    """)
    outcomes = cur.fetchall()
    
    # Index outcomes by (symbol_id, ts)
    outcome_map = {}
    for sid, ts, up in outcomes:
        outcome_map[(sid, ts)] = up
    
    # Process StockTwits by symbol and day (unix day)
    st_by_sym_day = defaultdict(dict)
    for sid, ts, bull, bear in st_data:
        day = ts // 86400  # Convert to day
        if sid not in st_by_sym_day or ts > st_by_sym_day[sid].get(day, (0, 0, 0))[0]:
            # Keep latest entry for the day
            st_by_sym_day[sid][day] = (ts, bull, bear)
        else:
            # Sum for same day
            if day in st_by_sym_day[sid]:
                _, b, br = st_by_sym_day[sid][day]
                st_by_sym_day[sid][day] = (ts, b + bull, br + bear)
            else:
                st_by_sym_day[sid][day] = (ts, bull, bear)
    
    # Process bars by symbol
    bars_by_sym = defaultdict(list)
    for sid, ts, o, h, l, c, v in bars_data:
        bars_by_sym[sid].append((ts, o, h, l, c, v))
    
    # Process each symbol
    opportunities = []
    
    for sid, bar_list in bars_by_sym.items():
        if sid not in st_by_sym_day or len(bar_list) < 505:
            continue
            
        # Get StockTwits days for this symbol
        st_days = sorted(st_by_sym_day[sid].keys())
        st_set = set(st_days)
        
        # Process each potential decision point T (index i)
        closes = [b[4] for b in bar_list]
        volumes = [b[5] for b in bar_list]
        ts_list = [b[0] for b in bar_list]
        
        # Need 504 prior sessions, so start at index 504
        for i in range(504, len(bar_list)):
            T = bar_list[i]
            ts_T, _, _, _, close_T, vol_T = T
            
            # Check close >= 5
            if close_T < 5:
                continue
            
            # Check average daily dollar volume >= 5M over prior 60 sessions
            if i >= 60:
                dollar_vols = [closes[j] * volumes[j] for j in range(i-59, i+1)]
                avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
                if avg_dollar_vol < 5e6:
                    continue
            else:
                continue
            
            # Check StockTwits data for T-4 to T
            T_day = ts_T // 86400
            missing_st = False
            ratios = []
            for offset in range(-4, 1):
                day = T_day + offset
                if day not in st_set:
                    missing_st = True
                    break
                _, bull, bear = st_by_sym_day[sid][day]
                if bear > 0:
                    ratios.append(bull / bear)
                else:
                    ratios.append(0)  # or handle division by zero
            
            if missing_st:
                continue
            
            # 5-session average ratio
            if len(ratios) != 5:
                continue
            avg_ratio = sum(ratios) / len(ratios)
            
            # 200-day SMA
            if i < 199:
                continue
            sma200 = sum(closes[i-199:i+1]) / 200
            
            # Prior day return
            if i == 0:
                continue
            prev_close = closes[i-1]
            ret_1d = (close_T - prev_close) / prev_close
            
            # 20-day realized volatility
            if i < 19:
                continue
            returns_20 = []
            for j in range(i-19, i+1):
                if j > 0:
                    r = (closes[j] - closes[j-1]) / closes[j-1]
                    returns_20.append(r)
            if len(returns_20) != 20:
                continue
            mean_r = sum(returns_20) / 20
            var_r = sum((r - mean_r) ** 2 for r in returns_20) / 19
            vol_20 = var_r ** 0.5
            
            # Label
            label = outcome_map.get((sid, ts_T))
            if label is None:
                continue
            
            opportunities.append({
                'sid': sid,
                'ts': ts_T,
                'dt': T_day,
                'close': close_T,
                'ret_1d': ret_1d,
                'sma200': sma200,
                'avg_ratio': avg_ratio,
                'vol_20': vol_20,
                'label': label  # 0=up, 1=down
            })
    
    # Sort opportunities by timestamp
    opportunities.sort(key=lambda x: x['ts'])
    
    # Split into training and sealed (last 20%)
    n_total = len(opportunities)
    if n_total == 0:
        print("INSUFFICIENT=1")
        return
    
    split_idx = int(n_total * 0.8)
    training = opportunities[:split_idx]
    sealed = opportunities[split_idx:]
    
    # Process function
    def process_era(era_opps):
        if not era_opps:
            return None, None, None, None
        
        # Group by day
        by_day = defaultdict(list)
        for opp in era_opps:
            by_day[opp['dt']].append(opp)
        
        # Sort days
        days = sorted(by_day.keys())
        
        issued = []
        last_call_per_sym = {}  # symbol -> last call timestamp
        
        for day in days:
            day_opps = by_day[day]
            
            # Check if at least 30 observations
            if len(day_opps) < 30:
                continue
            
            # Compute cross-sectional percentiles for ratio and vol
            ratios = [opp['avg_ratio'] for opp in day_opps]
            vols = [opp['vol_20'] for opp in day_opps]
            
            # Top decile = >= 90th percentile
            sorted_ratios = sorted(ratios)
            sorted_vols = sorted(vols)
            
            n = len(day_opps)
            ratio_p90 = sorted_ratios[int(0.9 * n)] if n > 10 else float('inf')
            vol_p90 = sorted_vols[int(0.9 * n)] if n > 10 else float('inf')
            
            for opp in day_opps:
                # Abstain conditions
                if opp['ret_1d'] < -0.01 or opp['ret_1d'] > 0.01:
                    continue
                if opp['vol_20'] >= vol_p90:
                    continue
                if opp['sid'] in last_call_per_sym and opp['ts'] - last_call_per_sym[opp['sid']] <= 20 * 86400:
                    continue
                
                # Entry conditions
                if opp['avg_ratio'] < ratio_p90:
                    continue
                if opp['close'] <= opp['sma200']:
                    continue
                
                # Issue DOWN call
                issued.append(opp)
                last_call_per_sym[opp['sid']] = opp['ts']
        
        return issued, n_total, len(era_opps), days
    
    # Process training era
    training_issued, _, training_total, training_days = process_era(training)
    if not training_issued:
        print("INSUFFICIENT=1")
        return
    
    # Process sealed era
    sealed_issued, _, sealed_total, sealed_days = process_era(sealed)
    
    # Compute metrics
    issued = training_issued
    n_issued = len(issued)
    n_opportunities = len(training)
    
    if n_issued == 0:
        print("INSUFFICIENT=1")
        return
    
    hits = sum(1 for opp in issued if opp['label'] == 1)  # label=1 means down
    precision = hits / n_issued
    base_rate = hits / n_issued  # Base rate within issued subset
    
    # Distinct days
    distinct_days = len(set(opp['dt'] for opp in issued))
    
    # Design effect (assume intra-day clustering)
    day_counts = defaultdict(int)
    for opp in issued:
        day_counts[opp['dt']] += 1
    
    # Effective sample size: N / (1 + (sum over days of (n_k - 1)) / (N - 1))
    # Simplified: N / (1 + (sum(n_k^2) / N - 1) * (N / (N-1)))
    # Or use formula: N_eff = N / (1 + (M-1) * ICC), where M = avg cluster size, ICC = intra-class correlation
    # Approximate ICC from day proportions
    
    n_days = len(day_counts)
    if n_days > 1 and n_issued > 1:
        # Cluster sizes
        cluster_sizes = list(day_counts.values())
        # Sum of squared cluster sizes
        sum_sq = sum(k*k for k in cluster_sizes)
        # Design effect = 1 + (mean_cluster_size - 1) * ICC
        # ICC ≈ (sum_sq / N - 1) / (mean_cluster_size - 1)
        mean_cluster = n_issued / n_days
        icc_approx = (sum_sq / n_issued - 1) / (mean_cluster - 1) if mean_cluster > 1 else 0
        design_effect = 1 + (mean_cluster - 1) * icc_approx
        effective_n = n_issued / design_effect
    else:
        effective_n = n_issued
    
    # Sealed metrics
    sealed_issued_count = len(sealed_issued) if sealed_issued else 0
    if sealed_issued_count > 0:
        sealed_hits = sum(1 for opp in sealed_issued if opp['label'] == 1)
        sealed_precision = sealed_hits / sealed_issued_count
    else:
        sealed_precision = 0.0
    
    # Abstention rate
    abstention_rate = 1 - n_issued / n_opportunities if n_opportunities > 0 else 1
    
    # Print results
    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={n_opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
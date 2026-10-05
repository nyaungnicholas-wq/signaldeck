import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Get all symbols with daily bars
    c.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
    symbols = [row[0] for row in c.fetchall()]
    
    # Get all 13F data grouped by symbol and quarter
    c.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    inst_data = c.fetchall()
    
    # Build per-symbol 13F history
    symbol_inst = defaultdict(list)
    for symbol_id, period, total_shares in inst_data:
        symbol_inst[symbol_id].append((period, total_shares))
    
    # For each symbol, compute ownership changes and generate signals
    signals = []
    for symbol_id in symbols:
        if symbol_id not in symbol_inst:
            continue
        quarters = symbol_inst[symbol_id]
        for i in range(1, len(quarters)):
            prev_period, prev_shares = quarters[i-1]
            curr_period, curr_shares = quarters[i]
            
            if prev_shares > 0:
                change_pct = (curr_shares - prev_shares) / prev_shares
            else:
                change_pct = 1.0 if curr_shares > 0 else 0
            
            if change_pct > 0.10:  # >10% increase in institutional ownership
                # Compute decision date: 45 days after quarter end
                quarter_end = datetime.strptime(prev_period, '%Y-%m-%d')
                decision_date = quarter_end + timedelta(days=45)
                signals.append((symbol_id, decision_date, change_pct))
    
    if not signals:
        print("INSUFFICIENT=1")
        return
    
    # Get daily bars for all symbols
    c.execute("""
        SELECT symbol_id, ts, close 
        FROM bars 
        WHERE tf='1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join(['?']*len(symbols))), symbols)
    bars = c.fetchall()
    
    # Group bars by symbol
    symbol_bars = defaultdict(list)
    for symbol_id, ts, close in bars:
        symbol_bars[symbol_id].append((ts, close))
    
    # Compute forward returns
    observations = []
    for symbol_id, decision_date, change_pct in signals:
        if symbol_id not in symbol_bars:
            continue
        # Convert decision date to unix timestamp
        decision_ts = int(decision_date.timestamp())
        
        # Find first bar after decision date
        symbol_bars_list = symbol_bars[symbol_id]
        start_idx = None
        for idx, (ts, close) in enumerate(symbol_bars_list):
            if ts >= decision_ts:
                start_idx = idx
                break
        
        if start_idx is None or start_idx + 20 >= len(symbol_bars_list):
            continue
        
        # Get T+0 and T+20 prices
        t0_price = symbol_bars_list[start_idx][1]
        t20_price = symbol_bars_list[start_idx + 20][1]
        fwd_return = (t20_price - t0_price) / t0_price
        
        # Label: positive return = 1
        label = 1 if fwd_return > 0 else 0
        
        observations.append((decision_date, label, symbol_id))
    
    conn.close()
    
    if len(observations) < 20:
        print("INSUFFICIENT=1")
        return
    
    # Sort by date and split into train/sealed (80/20)
    observations.sort(key=lambda x: x[0])
    split_idx = int(len(observations) * 0.8)
    train = observations[:split_idx]
    sealed = observations[split_idx:]
    
    # Compute metrics
    issued = len(train)
    hits = sum(1 for _, label, _ in train if label == 1)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate within issued subset
    base_rate = precision  # Since we're predicting all positives
    
    # Distinct days in issued calls
    distinct_days = len(set(date for date, _, _ in train))
    
    # Effective sample size (accounts for clustering by day)
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for date, label, _ in train:
        day_str = date.strftime('%Y-%m-%d')
        day_counts[day_str] += 1
        if label == 1:
            day_hits[day_str] += 1
    
    avg_cluster_size = issued / len(day_counts) if day_counts else 1
    
    # Intraclass correlation
    if issued > 0 and len(day_counts) > 1:
        overall_p = precision
        # Variance of day-level hit rates
        day_hit_rates = [day_hits[d]/day_counts[d] for d in day_counts]
        day_var = sum((p - overall_p)**2 for p in day_hit_rates) / len(day_counts)
        # ICC approximation
        p_var = overall_p * (1 - overall_p) if overall_p > 0 and overall_p < 1 else 0.01
        icc = day_var / p_var if p_var > 0 else 0
        design_effect = 1 + (avg_cluster_size - 1) * icc
    else:
        design_effect = 1.5  # Conservative default
    
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Sealed era precision
    sealed_hits = sum(1 for _, label, _ in sealed if label == 1)
    sealed_precision = sealed_hits / len(sealed) if sealed else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
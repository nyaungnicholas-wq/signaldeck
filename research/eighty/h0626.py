import sqlite3
import statistics
from collections import defaultdict

def main():
    # Connect to read-only database
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cursor = conn.cursor()
    
    # Get all daily bars
    cursor.execute("""
        SELECT symbol_id, ts, close, volume 
        FROM bars 
        WHERE tf = '1d' 
        ORDER BY symbol_id, ts
    """)
    bars = cursor.fetchall()
    
    if not bars:
        print("INSUFFICIENT=1")
        return
    
    # Group bars by symbol
    symbol_bars = defaultdict(list)
    for symbol_id, ts, close, volume in bars:
        symbol_bars[symbol_id].append((ts, close, volume))
    
    # Compute trailing 252-day volatility and 20-day avg dollar volume for each symbol-day
    candidate_calls = []
    
    for symbol_id, sym_bars in symbol_bars.items():
        if len(sym_bars) < 252:
            continue
            
        # Extract close prices
        closes = [bar[1] for bar in sym_bars]
        
        # Compute daily returns
        returns = []
        for i in range(1, len(closes)):
            returns.append((closes[i] / closes[i-1]) - 1)
        
        # Compute dollar volumes
        dollar_volumes = [bar[1] * bar[3] for bar in sym_bars]
        
        # Rolling calculations
        for i in range(251, len(sym_bars)):
            ts = sym_bars[i][0]
            current_close = closes[i]
            
            # Price filter
            if current_close < 5:
                continue
            
            # 20-day average dollar volume
            dv_slice = dollar_volumes[i-19:i+1]
            avg_dollar_vol = sum(dv_slice) / len(dv_slice)
            if avg_dollar_vol < 5_000_000:
                continue
            
            # 252-day volatility
            ret_slice = returns[i-251:i+1]
            vol_252 = statistics.stdev(ret_slice)
            
            candidate_calls.append((symbol_id, ts, current_close, vol_252))
    
    if not candidate_calls:
        print("INSUFFICIENT=1")
        return
    
    # Group by day to compute cross-sectional decile
    daily_data = defaultdict(list)
    for symbol_id, ts, close, vol_252 in candidate_calls:
        daily_data[ts].append((symbol_id, vol_252))
    
    # Get all decision dates
    all_dates = sorted(daily_data.keys())
    total_dates = len(all_dates)
    sealed_cutoff_idx = int(total_dates * 0.8)
    sealed_dates = set(all_dates[sealed_cutoff_idx:])
    
    # Issue calls: bottom decile of volatility on each day
    issued_calls = []
    
    for ts, symbols in daily_data.items():
        # Need at least 10 symbols to compute decile meaningfully
        if len(symbols) < 10:
            continue
            
        # Get volatilities and compute 10th percentile
        vols = [vol for _, vol in symbols]
        vols_sorted = sorted(vols)
        decile_idx = max(0, int(len(vols_sorted) * 0.1) - 1)
        threshold = vols_sorted[decile_idx]
        
        # Issue LONG for symbols in bottom decile
        for symbol_id, vol in symbols:
            if vol <= threshold:
                issued_calls.append((symbol_id, ts))
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Get labels from prediction_outcomes
    issued_with_labels = []
    
    for symbol_id, ts in issued_calls:
        cursor.execute("""
            SELECT up 
            FROM prediction_outcomes 
            WHERE symbol_id = ? 
              AND ts = ? 
              AND horizon = 21
        """, (symbol_id, ts))
        
        result = cursor.fetchone()
        if result:
            up = result[0]
            issued_with_labels.append((symbol_id, ts, up))
    
    conn.close()
    
    if not issued_with_labels:
        print("INSUFFICIENT=1")
        return
    
    # Split into training and sealed eras
    training_calls = []
    sealed_calls = []
    
    for symbol_id, ts, up in issued_with_labels:
        if ts in sealed_dates:
            sealed_calls.append((symbol_id, ts, up))
        else:
            training_calls.append((symbol_id, ts, up))
    
    all_calls = training_calls + sealed_calls
    
    # Compute metrics for all calls
    issued = len(all_calls)
    if issued == 0:
        print("INSUFFICIENT=1")
        return
    
    # Count hits
    hits = sum(1 for _, _, up in all_calls if up == 1)
    precision = hits / issued
    base_rate = hits / issued  # Same as precision in this case
    
    # Distinct days
    distinct_days = len(set(ts for _, ts, _ in all_calls))
    
    # Design effect and effective N
    # Group calls by day
    daily_calls = defaultdict(int)
    for _, ts, up in all_calls:
        daily_calls[ts] += 1
    
    total_days = len(daily_calls)
    avg_calls_per_day = issued / total_days
    
    # Intra-class correlation for binary outcome
    # Compute daily proportions of 'up'
    daily_counts = defaultdict(lambda: [0, 0])  # [total, ups]
    for _, ts, up in all_calls:
        daily_counts[ts][0] += 1
        if up == 1:
            daily_counts[ts][1] += 1
    
    # Between-cluster variance
    between_var_num = 0
    for ts, (n_i, ups_i) in daily_counts.items():
        p_i = ups_i / n_i
        between_var_num += n_i * ((p_i - precision) ** 2)
    between_var = between_var_num / (issued - 1) if issued > 1 else 0
    
    # Total variance
    total_var = precision * (1 - precision)
    
    # ICC
    if total_var > 0:
        icc = between_var / total_var
    else:
        icc = 0
    
    # Design effect
    deff = 1 + (avg_calls_per_day - 1) * icc
    effective_n = issued / deff if deff > 0 else issued
    
    # Sealed era metrics
    sealed_issued = len(sealed_calls)
    if sealed_issued > 0:
        sealed_hits = sum(1 for _, _, up in sealed_calls if up == 1)
        sealed_precision = sealed_hits / sealed_issued
    else:
        sealed_precision = 0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
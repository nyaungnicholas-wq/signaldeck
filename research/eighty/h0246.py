import sqlite3
import datetime
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cursor = conn.cursor()
    
    # Get symbols with both bars and inst_holdings
    cursor.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN bars b ON s.id = b.symbol_id
        WHERE b.tf = '1d'
        INTERSECT
        SELECT DISTINCT symbol_id, NULL
        FROM inst_holdings
    """)
    symbols = [(row[0], row[1]) for row in cursor.fetchall() if row[0] is not None]
    
    # Get all 13F holdings aggregated by symbol and period
    cursor.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
    """)
    holdings = {}
    for symbol_id, period, total_shares in cursor.fetchall():
        if symbol_id not in holdings:
            holdings[symbol_id] = {}
        holdings[symbol_id][period] = total_shares
    
    # Get all bars
    cursor.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    all_bars = cursor.fetchall()
    conn.close()
    
    # Organize bars by symbol
    bars_by_symbol = {}
    for symbol_id, ts, close, volume in all_bars:
        if symbol_id not in bars_by_symbol:
            bars_by_symbol[symbol_id] = []
        bars_by_symbol[symbol_id].append((ts, close, volume))
    
    # Build index for 20-day lookback
    opportunities = []
    calls = []
    last_call_day = {}
    
    for symbol_id, symbol in symbols:
        if symbol_id not in bars_by_symbol or symbol_id not in holdings:
            continue
        
        bars = bars_by_symbol[symbol_id]
        if len(bars) < 252:
            continue
        
        periods = sorted(holdings[symbol_id].keys(), reverse=True)
        if len(periods) < 2:
            continue
        
        # For each possible disclosure date (period + 45 days)
        for i, period in enumerate(periods):
            # Find the next older period
            if i == len(periods) - 1:
                continue
            prior_period = periods[i + 1]
            
            disclosure_date = datetime.date.fromtimestamp(period) + datetime.timedelta(days=45)
            disclosure_ts = int(disclosure_date.strftime('%s'))
            
            # Find the first bar at or after disclosure_ts
            T_idx = None
            for j, (ts, close, volume) in enumerate(bars):
                if ts >= disclosure_ts and j >= 252 and j >= 60:
                    T_idx = j
                    break
            
            if T_idx is None:
                continue
            
            T_ts, T_close, T_volume = bars[T_idx]
            
            # Check T+20 exists
            if T_idx + 20 >= len(bars):
                continue
            
            # Get close at T+20
            T20_close = bars[T_idx + 20][1]
            
            # Check price and volume requirements
            if T_close < 5:
                continue
            
            # Check 60-day average dollar volume
            volume_sum = 0
            for k in range(T_idx - 60, T_idx):
                vol_price = bars[k][1] * bars[k][2]
                volume_sum += vol_price
            if volume_sum / 60 < 5_000_000:
                continue
            
            # Check 20-day realized volatility
            if T_idx < 20:
                continue
            closes = [bars[k][1] for k in range(T_idx - 20, T_idx)]
            returns = [(closes[j] - closes[j-1]) / closes[j-1] for j in range(1, len(closes))]
            volatility = math.sqrt(sum(r**2 for r in returns) / len(returns))
            
            # Check 13F holdings ratio
            current_shares = holdings[symbol_id].get(period, 0)
            prior_shares = holdings[symbol_id].get(prior_period, 0)
            if prior_shares == 0 or current_shares < 1.5 * prior_shares:
                continue
            
            # Check no call in last 20 trading days for this symbol
            T_date = datetime.date.fromtimestamp(T_ts)
            if symbol_id in last_call_day:
                days_since_last = (T_date - last_call_day[symbol_id]).days
                if days_since_last < 20:
                    continue
            
            # Record opportunity
            outcome = 1 if T20_close > T_close else 0
            opportunities.append((symbol_id, T_ts, T_date, outcome, volatility))
        
    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Sort by time and split
    opportunities.sort(key=lambda x: x[1])
    split_idx = int(len(opportunities) * 0.8)
    non_sealed = opportunities[:split_idx]
    sealed = opportunities[split_idx:]
    
    # Compute volatility decile threshold for non-sealed period
    volatilities = [x[4] for x in non_sealed]
    volatilities.sort()
    decile_idx = int(len(volatilities) * 0.9)
    vol_threshold = volatilities[decile_idx] if volatilities else 0
    
    # Issue calls for non-sealed period
    issued = 0
    last_call_day.clear()
    for symbol_id, T_ts, T_date, outcome, volatility in non_sealed:
        # Check volatility condition
        if volatility > vol_threshold:
            continue
        
        # Check no call in last 20 trading days for this symbol
        if symbol_id in last_call_day:
            days_since_last = (T_date - last_call_day[symbol_id]).days
            if days_since_last < 20:
                continue
        
        # Issue UP call
        issued += 1
        last_call_day[symbol_id] = T_date
        calls.append((symbol_id, T_ts, T_date, outcome))
    
    if issued < 30:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics for non-sealed period
    hits = sum(x[3] for x in calls)
    precision = hits / issued
    base_rate = hits / issued  # Same as precision for UP-only calls
    
    # Count distinct days
    distinct_days = len(set(x[2] for x in calls))
    
    # Calculate design effect (simplified)
    # Count calls per symbol
    symbol_counts = {}
    for symbol_id, _, _, _ in calls:
        symbol_counts[symbol_id] = symbol_counts.get(symbol_id, 0) + 1
    avg_cluster_size = sum(symbol_counts.values()) / len(symbol_counts) if symbol_counts else 1
    design_effect = 1 + (avg_cluster_size - 1) * 0.5  # Assume ICC=0.5
    effective_n = issued / design_effect
    
    # Calculate sealed period metrics
    sealed_issued = 0
    sealed_hits = 0
    sealed_last_call_day = {}
    for symbol_id, T_ts, T_date, outcome, volatility in sealed:
        if volatility > vol_threshold:
            continue
        if symbol_id in sealed_last_call_day:
            days_since_last = (T_date - sealed_last_call_day[symbol_id]).days
            if days_since_last < 20:
                continue
        sealed_issued += 1
        sealed_last_call_day[symbol_id] = T_date
        if outcome == 1:
            sealed_hits += 1
    
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(non_sealed)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
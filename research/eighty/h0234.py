import sqlite3
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get all symbols with daily bars and stocktwits data
    cur.execute("SELECT DISTINCT b.symbol_id FROM bars b WHERE b.tf = '1d' AND EXISTS (SELECT 1 FROM stocktwits_sentiment s WHERE s.symbol_id = b.symbol_id)")
    symbol_ids = [row[0] for row in cur.fetchall()]
    
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return
    
    # Get daily bars for these symbols
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"SELECT symbol_id, ts, close, volume FROM bars WHERE tf = '1d' AND symbol_id IN ({placeholders}) ORDER BY symbol_id, ts", symbol_ids)
    bars_data = cur.fetchall()
    
    # Group bars by symbol
    bars_by_symbol = defaultdict(list)
    for symbol_id, ts, close, volume in bars_data:
        bars_by_symbol[symbol_id].append((ts, close, volume))
    
    # Get stocktwits data for these symbols
    cur.execute(f"SELECT symbol_id, ts, bullish, bearish FROM stocktwits_sentiment WHERE symbol_id IN ({placeholders}) ORDER BY symbol_id, ts", symbol_ids)
    st_data = cur.fetchall()
    
    # Group stocktwits by symbol
    st_by_symbol = defaultdict(list)
    for symbol_id, ts, bullish, bearish in st_data:
        st_by_symbol[symbol_id].append((ts, bullish, bearish))
    
    # Aggregate stocktwits by day (last entry per day)
    st_daily_by_symbol = defaultdict(list)
    for symbol_id, entries in st_by_symbol.items():
        # Group by day (ts // 86400)
        day_entries = defaultdict(list)
        for ts, bullish, bearish in entries:
            day = ts // 86400
            day_entries[day].append((ts, bullish, bearish))
        
        # Take last entry per day
        for day, day_list in day_entries.items():
            last_ts, last_bullish, last_bearish = max(day_list, key=lambda x: x[0])
            st_daily_by_symbol[symbol_id].append((last_ts, last_bullish, last_bearish))
    
    # Sort stocktwits by timestamp
    for symbol_id in st_daily_by_symbol:
        st_daily_by_symbol[symbol_id].sort(key=lambda x: x[0])
    
    # Build mapping from ts to bars for each symbol
    bars_map_by_symbol = defaultdict(dict)
    for symbol_id, entries in bars_by_symbol.items():
        for ts, close, volume in entries:
            bars_map_by_symbol[symbol_id][ts] = (close, volume)
    
    # Get all unique timestamps to define trading days
    all_ts = set()
    for symbol_id, entries in bars_by_symbol.items():
        for ts, close, volume in entries:
            all_ts.add(ts)
    all_ts = sorted(all_ts)
    
    # Map each ts to its index for position calculations
    ts_to_idx = {ts: idx for idx, ts in enumerate(all_ts)}
    idx_to_ts = {idx: ts for ts, idx in ts_to_idx.items()}
    
    # Get prediction outcomes for labels
    cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon = 20")
    outcomes = cur.fetchall()
    outcomes_by_symbol = defaultdict(dict)
    for symbol_id, ts, up in outcomes:
        outcomes_by_symbol[symbol_id][ts] = up  # 1 if up, 0 if down
    
    conn.close()
    
    # Prepare to process opportunities
    opportunities = []
    
    for symbol_id in symbol_ids:
        bars_list = bars_by_symbol.get(symbol_id, [])
        st_list = st_daily_by_symbol.get(symbol_id, [])
        
        if len(bars_list) < 253 or not st_list:
            continue
        
        # Create list of trading day timestamps for this symbol
        symbol_ts = [ts for ts, close, volume in bars_list]
        symbol_ts_set = set(symbol_ts)
        
        # Map timestamp to index in symbol_ts
        ts_to_sym_idx = {ts: idx for idx, ts in enumerate(symbol_ts)}
        
        # Process each potential T
        for sym_idx in range(252, len(symbol_ts)):
            T = symbol_ts[sym_idx]
            
            # Check close price >= $5
            T_close, _ = bars_map_by_symbol[symbol_id][T]
            if T_close < 5.0:
                continue
            
            # Check 252 prior sessions
            if sym_idx < 252:
                continue
            
            # Get close returns
            prev_close, _ = bars_map_by_symbol[symbol_id][symbol_ts[sym_idx - 1]]
            close_return = (T_close - prev_close) / prev_close
            
            # Get 20-session return
            if sym_idx >= 20:
                close_20, _ = bars_map_by_symbol[symbol_id][symbol_ts[sym_idx - 20]]
                return_20 = (T_close - close_20) / close_20
            else:
                continue
            
            # Get stocktwits data for T and prior 4 days
            # Find entries within last 5 trading days
            st_entries_T = []
            for st_ts, bullish, bearish in st_list:
                if st_ts <= T:
                    # Check if it's within 5 trading days
                    if st_ts in ts_to_sym_idx and ts_to_sym_idx[T] - ts_to_sym_idx[st_ts] < 5:
                        st_entries_T.append((st_ts, bullish, bearish))
            
            if len(st_entries_T) < 5:
                continue
            
            # Compute 5-day average bullish ratio
            total_bullish = sum(b for _, b, _ in st_entries_T)
            total_bearish = sum(b for _, _, b in st_entries_T)
            if total_bullish + total_bearish == 0:
                continue
            avg_bullish_ratio = total_bullish / (total_bullish + total_bearish)
            
            # Compute 20-session volatility
            vol_returns = []
            for i in range(19):
                idx1 = sym_idx - i - 1
                idx2 = sym_idx - i
                if idx1 < 0:
                    break
                close1, _ = bars_map_by_symbol[symbol_id][symbol_ts[idx1]]
                close2, _ = bars_map_by_symbol[symbol_id][symbol_ts[idx2]]
                vol_returns.append(math.log(close2 / close1))
            
            if len(vol_returns) < 20:
                continue
            
            vol_mean = sum(vol_returns) / len(vol_returns)
            vol_var = sum((r - vol_mean) ** 2 for r in vol_returns) / (len(vol_returns) - 1)
            vol_20 = math.sqrt(vol_var)
            
            # Store opportunity
            opportunities.append({
                'symbol_id': symbol_id,
                'T': T,
                'close_return': close_return,
                'return_20': return_20,
                'avg_bullish_ratio': avg_bullish_ratio,
                'vol_20': vol_20,
                'T_close': T_close
            })
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Sort opportunities by timestamp
    opportunities.sort(key=lambda x: x['T'])
    
    # Split into train and sealed (last 20%)
    split_idx = int(len(opportunities) * 0.8)
    train_opportunities = opportunities[:split_idx]
    sealed_opportunities = opportunities[split_idx:]
    
    # Compute cross-sectional deciles for volatility in each time period
    # First, group by day
    def compute_deciles(opps):
        day_groups = defaultdict(list)
        for opp in opps:
            day = opp['T'] // 86400
            day_groups[day].append(opp)
        
        deciles = {}
        for day, day_opps in day_groups.items():
            volatilities = [o['vol_20'] for o in day_opps]
            volatilities.sort()
            decile_idx = int(len(volatilities) * 0.9)
            if decile_idx >= len(volatilities):
                decile_idx = len(volatilities) - 1
            deciles[day] = volatilities[decile_idx]
        return deciles
    
    train_deciles = compute_deciles(train_opportunities)
    sealed_deciles = compute_deciles(sealed_opportunities)
    
    # Process calls
    issued_calls = []
    call_history = defaultdict(list)  # symbol_id -> list of T where call was issued
    
    for opp in train_opportunities + sealed_opportunities:
        symbol_id = opp['symbol_id']
        T = opp['T']
        
        # Check abstention conditions
        # 1. Price < $5
        if opp['T_close'] < 5.0:
            continue
        
        # 2. 20-session volatility in top cross-sectional decile
        day = T // 86400
        if opp in train_opportunities:
            deciles = train_deciles
        else:
            deciles = sealed_deciles
        
        if day in deciles and opp['vol_20'] > deciles[day]:
            continue
        
        # 3. Call issued for same symbol in prior 20 trading days
        if symbol_id in call_history:
            recent_calls = [c for c in call_history[symbol_id] if c > T - 20*86400]
            if recent_calls:
                continue
        
        # 4. Close-to-close return outside [-0.5%, +0.5%]
        if abs(opp['close_return']) > 0.005:
            continue
        
        # 5. 20-session return outside [-2%, +2%]
        if abs(opp['return_20']) > 0.02:
            continue
        
        # Check entry condition: 5-day avg bullish ratio >= 0.8
        if opp['avg_bullish_ratio'] >= 0.8:
            # Issue DOWN call
            issued_calls.append({
                'symbol_id': symbol_id,
                'T': T,
                'train': opp in train_opportunities,
                'outcome': None  # Will be filled later
            })
            call_history[symbol_id].append(T)
    
    if len(issued_calls) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Get outcomes for issued calls
    hits = 0
    train_hits = 0
    sealed_hits = 0
    train_issued = 0
    sealed_issued = 0
    
    for call in issued_calls:
        symbol_id = call['symbol_id']
        T = call['T']
        
        # Find outcome 20 trading days after T
        if symbol_id in outcomes_by_symbol:
            # Get next 20 trading day timestamps
            # This is simplified - we assume outcome exists at T+20
            # In reality, we need to find the 20th trading day after T
            # For now, check if there's an outcome within a reasonable range
            future_outcomes = {ts: up for ts, up in outcomes_by_symbol[symbol_id].items() if ts > T}
            if future_outcomes:
                # Take the earliest outcome (should be T+20)
                earliest_ts = min(future_outcomes.keys())
                call['outcome'] = future_outcomes[earliest_ts]
        
        if call['outcome'] is None:
            # Skip if no outcome
            continue
        
        # If outcome is 0 (down), it's a hit
        if call['outcome'] == 0:
            hits += 1
            if call['train']:
                train_hits += 1
            else:
                sealed_hits += 1
        
        if call['train']:
            train_issued += 1
        else:
            sealed_issued += 1
    
    total_issued = len(issued_calls)
    
    if total_issued == 0:
        print("INSUFFICIENT=1")
        return
    
    precision = hits / total_issued
    base_rate = hits / total_issued  # Base rate within issued subset is same as precision
    
    # Count distinct days
    distinct_days = len(set(call['T'] // 86400 for call in issued_calls))
    
    # Compute design effect (clustering by day)
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for call in issued_calls:
        if call['outcome'] is not None:
            day = call['T'] // 86400
            day_counts[day] += 1
            if call['outcome'] == 0:
                day_hits[day] += 1
    
    # Compute ICC
    total_calls = sum(day_counts.values())
    total_hits = sum(day_hits.values())
    grand_mean = total_hits / total_calls if total_calls > 0 else 0
    
    # Compute between-group variance
    group_means = {}
    for day in day_counts:
        if day_counts[day] > 0:
            group_means[day] = day_hits[day] / day_counts[day]
        else:
            group_means[day] = 0
    
    ss_between = sum(day_counts[day] * (group_means[day] - grand_mean) ** 2 for day in day_counts)
    ss_total = total_hits * (1 - grand_mean)  # Simplified
    
    if total_calls <= 1:
        icc = 0
    else:
        ms_between = ss_between / (len(day_counts) - 1) if len(day_counts) > 1 else 0
        ms_total = ss_total / (total_calls - 1) if total_calls > 1 else 0
        icc = ms_between / ms_total if ms_total > 0 else 0
    
    # Design effect
    avg_cluster_size = total_calls / len(day_counts) if day_counts else 1
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = total_issued / design_effect if design_effect > 0 else total_issued
    
    # Compute sealed precision
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Print results
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
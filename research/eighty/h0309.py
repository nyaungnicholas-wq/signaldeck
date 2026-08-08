# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 308
# cycle_index: 31
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

def get_trading_days(conn, symbol_id, start_ts, end_ts):
    """Get list of trading day timestamps for a symbol between start and end timestamps."""
    cursor = conn.cursor()
    cursor.execute("""
        SELECT ts FROM bars 
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
        ORDER BY ts
    """, (symbol_id, start_ts, end_ts))
    return [row[0] for row in cursor.fetchall()]

def get_price_on_date(conn, symbol_id, target_ts, tolerance_days=5):
    """Get close price on or after target_ts within tolerance_days."""
    cursor = conn.cursor()
    end_ts = target_ts + tolerance_days * 86400  # Approximate days to seconds
    cursor.execute("""
        SELECT close FROM bars 
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
        ORDER BY ts LIMIT 1
    """, (symbol_id, target_ts, end_ts))
    result = cursor.fetchone()
    return result[0] if result else None

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except sqlite3.Error as e:
        print("INSUFFICIENT=1")
        return

    # Get all institutional holdings aggregated by symbol and quarter
    cursor = conn.cursor()
    
    # Step 1: Get all 13F data with proper as-of discipline
    # We need: symbol_id, quarter (period), total_shares for that quarter
    cursor.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    inst_data = cursor.fetchall()
    
    # Step 2: Get shares outstanding for each symbol (key/value)
    cursor.execute("""
        SELECT symbol_id, value as shares_outstanding
        FROM fundamentals
        WHERE metric = 'SharesOutstanding'
    """)
    shares_outstanding = {row[0]: row[1] for row in cursor.fetchall()}
    
    # Step 3: Process data to identify opportunities
    opportunities = []
    symbol_history = defaultdict(list)  # symbol_id -> [(period, total_shares, ownership_ratio)]
    
    for symbol_id, period, total_shares in inst_data:
        # Get shares outstanding for this symbol
        if symbol_id not in shares_outstanding:
            continue
        
        shares_out = shares_outstanding[symbol_id]
        if shares_out <= 0:
            continue
            
        ownership_ratio = total_shares / shares_out
        
        # Store in history for this symbol
        symbol_history[symbol_id].append((period, total_shares, ownership_ratio))
    
    # Now process each symbol's history to find top-decile ownership opportunities
    for symbol_id, history in symbol_history.items():
        if len(history) < 21:  # Need at least 20 prior quarters + current
            continue
            
        # Sort by period (quarter end date)
        history.sort(key=lambda x: x[0])
        
        # Check if we have at least 20 prior quarters before each point
        for i in range(20, len(history)):
            current_period = history[i][0]
            current_ownership = history[i][2]
            
            # Get trailing 20 quarters of ownership ratios (excluding current)
            trailing_20 = [h[2] for h in history[i-20:i]]
            
            # Calculate 90th percentile (top decile threshold)
            sorted_trailing = sorted(trailing_20)
            idx_90 = int(len(sorted_trailing) * 0.9)
            threshold_90 = sorted_trailing[idx_90]
            
            # Check if current ownership is in top decile
            if current_ownership < threshold_90:
                continue
            
            # Calculate ownership increase from previous quarter
            if i >= 1:
                prev_ownership = history[i-1][2]
                increase = current_ownership - prev_ownership
                
                # Get trailing 20 increases (if available)
                increases = []
                for j in range(max(0, i-20), i):
                    if j > 0:
                        increases.append(history[j][2] - history[j-1][2])
                
                if len(increases) >= 20:
                    sorted_increases = sorted(increases)
                    idx_90_inc = int(len(sorted_increases) * 0.9)
                    threshold_90_inc = sorted_increases[idx_90_inc]
                    
                    # Check if increase is also in top decile
                    if increase >= threshold_90_inc:
                        continue
            
            # Calculate decision date (period + 45 days)
            period_date = datetime.fromtimestamp(current_period)
            decision_date = period_date + timedelta(days=45)
            decision_ts = int(decision_date.timestamp())
            
            # Price must be >= $5 at entry (use next available trading day)
            price = get_price_on_date(conn, symbol_id, decision_ts)
            if price is None or price < 5:
                continue
            
            # Get forward price 63 trading days later
            trading_days = get_trading_days(conn, symbol_id, decision_ts, decision_ts + 252*86400)
            
            # Find decision day in trading days (first day >= decision_ts)
            decision_idx = None
            for idx, ts in enumerate(trading_days):
                if ts >= decision_ts:
                    decision_idx = idx
                    break
            
            if decision_idx is None:
                continue
                
            # Need at least 63 more trading days
            if decision_idx + 63 >= len(trading_days):
                continue
            
            forward_idx = decision_idx + 63
            forward_ts = trading_days[forward_idx]
            
            # Get forward price
            forward_price = get_price_on_date(conn, symbol_id, forward_ts)
            if forward_price is None:
                continue
            
            # Calculate forward return
            fwd_return = (forward_price - price) / price
            
            opportunities.append({
                'symbol_id': symbol_id,
                'decision_date': decision_date,
                'decision_ts': decision_ts,
                'ownership_ratio': current_ownership,
                'fwd_return': fwd_return,
                'is_down': fwd_return < 0
            })
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Sort opportunities by decision date
    opportunities.sort(key=lambda x: x['decision_ts'])
    
    # Split into training and sealed (most recent 20%)
    n_opportunities = len(opportunities)
    split_idx = int(n_opportunities * 0.8)
    
    training_set = opportunities[:split_idx]
    sealed_set = opportunities[split_idx:]
    
    # Count metrics for sealed era
    if not sealed_set:
        print("INSUFFICIENT=1")
        return
    
    issued = len(sealed_set)
    hits = sum(1 for opp in sealed_set if opp['is_down'])
    precision = hits / issued if issued > 0 else 0
    
    # Base rate of down moves in issued set
    down_count = sum(1 for opp in sealed_set if opp['is_down'])
    base_rate = down_count / issued if issued > 0 else 0
    
    # Count distinct days
    distinct_days = len(set(opp['decision_date'].strftime('%Y-%m-%d') for opp in sealed_set))
    
    # Calculate design effect (effective sample size)
    # Group by day
    day_groups = defaultdict(list)
    for opp in sealed_set:
        day_key = opp['decision_date'].strftime('%Y-%m-%d')
        day_groups[day_key].append(opp['is_down'])
    
    # Calculate intra-class correlation (ICC) for binary outcomes
    total_variance = 0
    within_variance = 0
    
    # Calculate overall mean
    all_outcomes = [1 if opp['is_down'] else 0 for opp in sealed_set]
    overall_mean = sum(all_outcomes) / len(all_outcomes)
    
    # Calculate between-day and within-day variance
    for day, outcomes in day_groups.items():
        n_day = len(outcomes)
        if n_day < 2:
            continue
        
        day_mean = sum(outcomes) / n_day
        
        # Add to between-day variance
        total_variance += n_day * (day_mean - overall_mean) ** 2
        
        # Add to within-day variance
        for outcome in outcomes:
            within_variance += (outcome - day_mean) ** 2
    
    # Calculate ICC
    if len(day_groups) > 0 and n_opportunities > len(day_groups):
        between_variance = total_variance / (len(day_groups) - 1)
        within_variance = within_variance / (n_opportunities - len(day_groups))
        
        if within_variance > 0:
            icc = between_variance / (between_variance + within_variance)
            
            # Calculate average cluster size
            avg_cluster_size = n_opportunities / len(day_groups)
            
            # Design effect
            design_effect = 1 + (avg_cluster_size - 1) * icc
            effective_n = issued / design_effect
        else:
            design_effect = 1
            effective_n = issued
    else:
        design_effect = 1
        effective_n = issued
    
    # Validate invariants
    if distinct_days > issued:
        print("INSUFFICIENT=1")
        return
    
    if effective_n >= issued:
        print("INSUFFICIENT=1")
        return
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={n_opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 514
# cycle_index: 44
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    # Connect to database in read-only mode
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get all 13F holdings with symbol and period
    holdings = conn.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """).fetchall()
    
    if not holdings:
        print("INSUFFICIENT=1")
        return
    
    # Group by symbol
    symbol_holdings = defaultdict(list)
    for row in holdings:
        symbol_holdings[row['symbol_id']].append((row['period'], row['total_shares']))
    
    # Filter symbols with at least 8 quarters of data
    eligible_symbols = {sid: data for sid, data in symbol_holdings.items() if len(data) >= 8}
    
    if not eligible_symbols:
        print("INSUFFICIENT=1")
        return
    
    # For each eligible symbol, compute quarter-over-quarter changes and decision points
    decision_points = []
    
    for symbol_id, periods in eligible_symbols.items():
        periods_sorted = sorted(periods, key=lambda x: x[0])
        
        for i in range(8, len(periods_sorted)):  # Start from 8th quarter to ensure 8 quarters of history
            prev_period, prev_shares = periods_sorted[i-1]
            curr_period, curr_shares = periods_sorted[i]
            
            # Compute percentage change
            if prev_shares == 0:
                continue
            pct_change = (curr_shares - prev_shares) / prev_shares
            
            if pct_change < 0.10:  # Need at least 10% increase
                continue
            
            # Decision date: 45 days after quarter end (latest possible 13F filing date)
            quarter_end = datetime.strptime(curr_period, '%Y-%m-%d')
            decision_date = quarter_end + timedelta(days=45)
            
            # Check if we have daily bars for at least 20 days in the quarter
            quarter_start = quarter_end - timedelta(days=90)  # Approximate quarter start
            bar_count = conn.execute("""
                SELECT COUNT(*)
                FROM bars
                WHERE symbol_id = ? 
                  AND tf = '1d'
                  AND ts >= ? 
                  AND ts <= ?
            """, (symbol_id, int(quarter_start.timestamp()), int(quarter_end.timestamp()))).fetchone()[0]
            
            if bar_count < 20:
                continue
            
            decision_points.append({
                'symbol_id': symbol_id,
                'decision_date': decision_date,
                'decision_ts': int(decision_date.timestamp()),
                'pct_change': pct_change
            })
    
    if not decision_points:
        print("INSUFFICIENT=1")
        return
    
    # Sort decision points by date
    decision_points.sort(key=lambda x: x['decision_date'])
    
    # Split into train and sealed (most recent 20%)
    split_idx = int(len(decision_points) * 0.8)
    sealed_points = decision_points[split_idx:]
    train_points = decision_points[:split_idx]
    
    # Process all points (train + sealed)
    all_points = train_points + sealed_points
    
    issued_calls = []
    opportunities = 0
    
    for point in all_points:
        symbol_id = point['symbol_id']
        decision_date = point['decision_date']
        decision_ts = point['decision_ts']
        
        # Get 20-day realized volatility at decision date
        volatility_query = """
            SELECT close
            FROM bars
            WHERE symbol_id = ? 
              AND tf = '1d'
              AND ts <= ?
            ORDER BY ts DESC
            LIMIT 20
        """
        vol_closes = [row['close'] for row in conn.execute(volatility_query, (symbol_id, decision_ts)).fetchall()]
        
        if len(vol_closes) < 20:
            continue
        
        # Compute realized volatility (standard deviation of log returns)
        log_returns = [math.log(vol_closes[i] / vol_closes[i-1]) for i in range(1, len(vol_closes))]
        mean_return = sum(log_returns) / len(log_returns)
        variance = sum((r - mean_return) ** 2 for r in log_returns) / (len(log_returns) - 1)
        volatility = math.sqrt(variance)
        
        # Get cross-sectional median volatility for this decision date
        median_query = """
            SELECT b.symbol_id, 
                   (SELECT close FROM bars WHERE symbol_id = b.symbol_id AND tf = '1d' AND ts <= ? ORDER BY ts DESC LIMIT 1) as last_close
            FROM symbols b
            WHERE b.active = 1
              AND b.market = 'stocks'
              AND EXISTS (SELECT 1 FROM bars WHERE symbol_id = b.symbol_id AND tf = '1d' AND ts <= ?)
            GROUP BY b.symbol_id
        """
        all_volatilities = []
        for row in conn.execute(median_query, (decision_ts, decision_ts)).fetchall():
            sym = row['symbol_id']
            sym_closes = [r['close'] for r in conn.execute("""
                SELECT close FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
                ORDER BY ts DESC LIMIT 20
            """, (sym, decision_ts)).fetchall()]
            
            if len(sym_closes) >= 20:
                sym_returns = [math.log(sym_closes[i] / sym_closes[i-1]) for i in range(1, len(sym_closes))]
                sym_mean = sum(sym_returns) / len(sym_returns)
                sym_var = sum((r - sym_mean) ** 2 for r in sym_returns) / (len(sym_returns) - 1)
                all_volatilities.append(math.sqrt(sym_var))
        
        if not all_volatilities:
            continue
        
        median_volatility = sorted(all_volatilities)[len(all_volatilities) // 2]
        
        # Entry condition: volatility below cross-sectional median
        if volatility >= median_volatility:
            continue
        
        # Check forward return 21 trading days after decision date
        forward_query = """
            SELECT close
            FROM bars
            WHERE symbol_id = ? 
              AND tf = '1d'
              AND ts > ?
            ORDER BY ts ASC
            LIMIT 21
        """
        forward_closes = [row['close'] for row in conn.execute(forward_query, (symbol_id, decision_ts)).fetchall()]
        
        if len(forward_closes) < 21:
            continue
        
        current_price = conn.execute("""
            SELECT close FROM bars 
            WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
            ORDER BY ts DESC LIMIT 1
        """, (symbol_id, decision_ts)).fetchone()
        
        if not current_price:
            continue
        
        current_price = current_price['close']
        forward_price = forward_closes[-1]
        forward_return = (forward_price - current_price) / current_price
        
        opportunities += 1
        
        # Issue call if we predict upward movement
        issued_calls.append({
            'symbol_id': symbol_id,
            'decision_date': decision_date,
            'up': 1 if forward_return > 0 else 0
        })
    
    conn.close()
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics
    issued = len(issued_calls)
    hits = sum(1 for call in issued_calls if call['up'] == 1)
    precision = hits / issued if issued > 0 else 0
    base_rate = hits / issued  # Base rate of predicted class (up) within issued calls
    
    # Distinct days among issued calls only
    distinct_days = len(set(call['decision_date'].strftime('%Y-%m-%d') for call in issued_calls))
    
    # Calculate design effect for effective N
    # Group by day
    day_groups = defaultdict(list)
    for call in issued_calls:
        day_groups[call['decision_date'].strftime('%Y-%m-%d')].append(call)
    
    # Design effect = 1 + (average cluster size - 1) * ICC
    # For simplicity, we use ICC = 0.5 (conservative estimate)
    icc = 0.5
    cluster_sizes = [len(group) for group in day_groups.values()]
    avg_cluster_size = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = issued / design_effect
    
    # Sealed era precision
    sealed_calls = [call for call in issued_calls if call['decision_date'] >= decision_points[split_idx]['decision_date']]
    sealed_hits = sum(1 for call in sealed_calls if call['up'] == 1)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
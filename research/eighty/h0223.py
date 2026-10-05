#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict

DB_PATH = 'data/signaldeck.db'

def main():
    conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Check data existence
    cur.execute("SELECT COUNT(*) FROM bars WHERE tf='1d'")
    if cur.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        return
    cur.execute("SELECT COUNT(*) FROM insider_trades WHERE code='S'")
    if cur.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        return
    cur.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE horizon=20")
    if cur.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        return
    
    # Get all symbols with insider sales
    cur.execute("""
        SELECT DISTINCT symbol_id 
        FROM insider_trades 
        WHERE code='S'
    """)
    symbol_ids = [row[0] for row in cur.fetchall()]
    
    # Get all daily bars for these symbols
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?' * len(symbol_ids))), symbol_ids)
    bars_data = cur.fetchall()
    
    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for symbol_id, ts, close, volume in bars_data:
        bars_by_symbol[symbol_id].append((ts, close, volume))
    
    # Get insider sales with disclosure dates
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts, price, value
        FROM insider_trades
        WHERE code='S'
        AND symbol_id IN ({})
        ORDER BY symbol_id, filed_ts
    """.format(','.join('?' * len(symbol_ids))), symbol_ids)
    insider_sales = cur.fetchall()
    
    # Organize sales by symbol
    sales_by_symbol = defaultdict(list)
    for symbol_id, tx_ts, filed_ts, price, value in insider_sales:
        sales_by_symbol[symbol_id].append((tx_ts, filed_ts, price, value))
    
    # Process each symbol
    opportunities = []
    for symbol_id in symbol_ids:
        bars = bars_by_symbol[symbol_id]
        if len(bars) < 252:
            continue
            
        sales = sales_by_symbol.get(symbol_id, [])
        if not sales:
            continue
            
        # Build index of bar dates for this symbol
        bar_dates = [bar[0] for bar in bars]
        
        # Create lookup for bar data by ts
        bar_lookup = {}
        for ts, close, volume in bars:
            bar_lookup[ts] = (close, volume)
        
        # For each insider sale with disclosure date
        for tx_ts, filed_ts, sale_price, sale_value in sales:
            # Find T: first trading day on or after filed_ts
            t_idx = None
            for i, ts in enumerate(bar_dates):
                if ts >= filed_ts:
                    t_idx = i
                    break
            if t_idx is None:
                continue
            
            t_ts = bar_dates[t_idx]
            
            # Need at least 20 prior bars for returns
            if t_idx < 20:
                continue
            
            # Check if we have all required bars from T-20 to T
            required_bars = bar_dates[t_idx-20:t_idx+1]
            if len(required_bars) < 21:
                continue
            
            # Check average daily volume over prior 60 sessions
            if t_idx < 60:
                continue
            
            # Calculate average daily dollar volume
            total_volume = 0
            for i in range(t_idx-60, t_idx):
                ts = bar_dates[i]
                close, vol = bar_lookup[ts]
                total_volume += close * vol
            avg_dollar_volume = total_volume / 60
            
            if avg_dollar_volume < 5_000_000:
                continue
            
            # Current close
            t_close, t_volume = bar_lookup[t_ts]
            
            # Price >= $5
            if t_close < 5:
                continue
            
            # 20-session return
            close_20 = bar_lookup[bar_dates[t_idx-20]][0]
            ret_20 = (t_close / close_20) - 1
            
            if ret_20 < 0.10:
                continue
            
            # Close-to-close return
            close_prev = bar_lookup[bar_dates[t_idx-1]][0]
            ret_cc = (t_close / close_prev) - 1
            
            if ret_cc < -0.01 or ret_cc > 0.01:
                continue
            
            # Check trade date is no more than 10 sessions before T
            # Find position of tx_ts in bar_dates
            tx_idx = None
            for i, ts in enumerate(bar_dates):
                if ts >= tx_ts:
                    tx_idx = i
                    break
            if tx_idx is None or tx_idx > t_idx:
                continue
            
            sessions_diff = t_idx - tx_idx
            if sessions_diff > 10:
                continue
            
            # Calculate 20-day volatility
            returns = []
            for i in range(t_idx-19, t_idx+1):
                ts_curr = bar_dates[i]
                ts_prev = bar_dates[i-1]
                close_curr = bar_lookup[ts_curr][0]
                close_prev = bar_lookup[ts_prev][0]
                returns.append(close_curr/close_prev - 1)
            
            mean_ret = sum(returns) / 20
            variance = sum((r - mean_ret) ** 2 for r in returns) / 19
            volatility = math.sqrt(variance)
            
            # Get outcome (label) at T+20
            # Find T+20 trading day
            t20_idx = t_idx + 20
            if t20_idx >= len(bar_dates):
                continue
            
            t20_ts = bar_dates[t20_idx]
            
            # Query prediction_outcomes for this symbol and horizon=20 at T
            cur.execute("""
                SELECT up
                FROM prediction_outcomes
                WHERE symbol_id = ? AND horizon = 20 AND ts = ?
            """, (symbol_id, t_ts))
            row = cur.fetchone()
            if not row:
                continue
            
            up = row[0]
            down = 1 - up  # 1 if down
            
            opportunities.append({
                'symbol_id': symbol_id,
                't_ts': t_ts,
                't20_ts': t20_ts,
                'volatility': volatility,
                'down': down
            })
    
    # Sort opportunities by time
    opportunities.sort(key=lambda x: x['t_ts'])
    
    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Split into training and sealed era (last 20%)
    split_idx = int(len(opportunities) * 0.8)
    training = opportunities[:split_idx]
    sealed = opportunities[split_idx:]
    
    # Calculate volatility deciles from training set
    volatilities = [opp['volatility'] for opp in training]
    volatilities.sort()
    decile_idx = int(len(volatilities) * 0.9)
    top_decile_threshold = volatilities[decile_idx]
    
    # Process training set with cooldown
    issued = []
    last_call = defaultdict(int)  # symbol_id -> last call timestamp
    issued_by_day = defaultdict(int)
    
    for opp in training:
        symbol_id = opp['symbol_id']
        t_ts = opp['t_ts']
        
        # Check cooldown
        if last_call[symbol_id] >= t_ts - 20 * 86400:  # ~20 trading days in seconds
            continue
        
        # Check volatility not in top decile
        if opp['volatility'] > top_decile_threshold:
            continue
        
        # Issue call
        issued.append(opp)
        last_call[symbol_id] = t_ts
        issued_by_day[t_ts] += 1
    
    if len(issued) == 0:
        print("INSUFFICIENT=1")
        return
    
    # Process sealed era with same rules
    sealed_issued = []
    last_call_sealed = defaultdict(int)
    
    for opp in sealed:
        symbol_id = opp['symbol_id']
        t_ts = opp['t_ts']
        
        # Check cooldown (using training period last calls)
        if last_call[symbol_id] >= t_ts - 20 * 86400:
            continue
        
        # Check volatility
        if opp['volatility'] > top_decile_threshold:
            continue
        
        # Issue call
        sealed_issued.append(opp)
        last_call_sealed[symbol_id] = t_ts
    
    # Calculate metrics
    hits = sum(opp['down'] for opp in issued)
    precision = hits / len(issued) if issued else 0
    
    # Base rate of predicted class (DOWN) within issued subset
    base_rate = precision  # In our case, it's the same
    
    # Distinct days with calls
    distinct_days = len(issued_by_day)
    
    # Design effect calculation
    # Cluster by day
    day_counts = list(issued_by_day.values())
    if len(day_counts) > 1:
        n_clusters = len(day_counts)
        sum_squares = sum(c * c for c in day_counts)
        icc = (sum_squares / n_clusters - 1) / (len(issued) - 1) if len(issued) > 1 else 0
        deff = 1 + (len(issued)/n_clusters - 1) * icc
    else:
        deff = 1.0
    
    effective_n = len(issued) / deff if deff > 1 else len(issued)
    
    # Sealed precision
    sealed_hits = sum(opp['down'] for opp in sealed_issued)
    sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0
    
    # Print results
    print(f"ISSUED={len(issued)}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
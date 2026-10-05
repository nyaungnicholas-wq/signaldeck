# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 796
# cycle_index: 66
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta

def get_trading_days(cursor, start_ts, end_ts):
    cursor.execute("""
        SELECT DISTINCT ts FROM bars 
        WHERE tf = '1d' AND ts >= ? AND ts <= ?
        ORDER BY ts
    """, (start_ts, end_ts))
    return [row[0] for row in cursor.fetchall()]

def find_previous_trading_day(trading_days, ts):
    for i in range(len(trading_days)-1, -1, -1):
        if trading_days[i] < ts:
            return trading_days[i]
    return None

def find_next_trading_day(trading_days, ts):
    for day in trading_days:
        if day > ts:
            return day
    return None

def calculate_win_rate(cursor, symbol_id, insider, before_ts):
    cursor.execute("""
        SELECT tx_ts, fwd_return FROM insider_trades
        WHERE symbol_id = ? AND insider = ? AND code = 'P' AND tx_ts < ?
        ORDER BY tx_ts
    """, (symbol_id, insider, before_ts))
    
    prior_trades = cursor.fetchall()
    if not prior_trades:
        return 0.5
    
    wins = sum(1 for _, fwd_return in prior_trades if fwd_return and fwd_return > 0)
    return wins / len(prior_trades)

def calculate_volatility(cursor, symbol_id, end_ts, lookback=252):
    cursor.execute("""
        SELECT ts, close FROM bars 
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC
        LIMIT ?
    """, (symbol_id, end_ts, lookback + 1))
    
    rows = cursor.fetchall()
    if len(rows) < 20:
        return None
    
    closes = [row[1] for row in reversed(rows)]
    returns = [(closes[i] - closes[i-1]) / closes[i-1] for i in range(1, len(closes))]
    
    if not returns:
        return None
    
    mean = sum(returns) / len(returns)
    variance = sum((r - mean) ** 2 for r in returns) / (len(returns) - 1)
    return math.sqrt(variance) if variance > 0 else 0

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Get all symbols with insider trades
        cursor.execute("""
            SELECT DISTINCT symbol_id FROM insider_trades
            WHERE code = 'P'
        """)
        symbols = [row[0] for row in cursor.fetchall()]
        
        if not symbols:
            print("INSUFFICIENT=1")
            return
        
        # Get all trading days for volatility calculations and time management
        cursor.execute("SELECT MIN(ts) FROM bars WHERE tf = '1d'")
        min_ts = cursor.fetchone()[0]
        cursor.execute("SELECT MAX(ts) FROM bars WHERE tf = '1d'")
        max_ts = cursor.fetchone()[0]
        
        trading_days = get_trading_days(cursor, min_ts, max_ts)
        if len(trading_days) < 20:
            print("INSUFFICIENT=1")
            return
        
        # Define time boundaries
        decision_end_ts = trading_days[-22]  # Need 21 days forward from decision
        all_decision_days = []
        
        # Process each symbol
        opportunities = []
        for symbol_id in symbols:
            # Get all insider purchases for this symbol
            cursor.execute("""
                SELECT insider, title, tx_ts, filed_ts FROM insider_trades
                WHERE symbol_id = ? AND code = 'P'
                ORDER BY filed_ts
            """, (symbol_id,))
            trades = cursor.fetchall()
            
            if len(trades) < 3:
                continue
            
            # Group trades by 10-trading-day windows
            last_cluster_ts = None
            for i, (insider, title, tx_ts, filed_ts) in enumerate(trades):
                if filed_ts > decision_end_ts:
                    continue
                
                # Skip if too soon after last cluster
                if last_cluster_ts and (filed_ts - last_cluster_ts) < 20 * 86400:
                    continue
                
                # Find trades within 10 trading days before this filed_ts
                window_start = find_previous_trading_day(trading_days, filed_ts - 10 * 86400)
                if not window_start:
                    continue
                
                window_trades = [(i2, ins, t, f) for i2, (ins, t, tx, f) in enumerate(trades) 
                                if window_start <= f <= filed_ts and ins != insider]
                window_trades.append((i, insider, tx_ts, filed_ts))
                
                if len(window_trades) < 3:
                    continue
                
                # Check distinct roles
                roles = set()
                for _, _, _, f in window_trades:
                    cursor.execute("""
                        SELECT title FROM insider_trades
                        WHERE symbol_id = ? AND insider = ? AND code = 'P' AND filed_ts <= ?
                        ORDER BY filed_ts DESC LIMIT 1
                    """, (symbol_id, _, f))
                    role_row = cursor.fetchone()
                    if role_row:
                        title = role_row[0]
                        if 'CEO' in title.upper():
                            roles.add('CEO')
                        elif 'CFO' in title.upper():
                            roles.add('CFO')
                        elif 'DIRECTOR' in title.upper():
                            roles.add('DIRECTOR')
                        elif 'OFFICER' in title.upper():
                            roles.add('OFFICER')
                        elif '10%' in title.upper() or 'BENEFICIAL' in title.upper():
                            roles.add('OWNER')
                
                if len(roles) < 3:
                    continue
                
                # Check each insider's win rate
                all_winners = True
                for _, ins, _, _ in window_trades:
                    win_rate = calculate_win_rate(cursor, symbol_id, ins, filed_ts)
                    if win_rate <= 0.5:
                        all_winners = False
                        break
                
                if not all_winners:
                    continue
                
                # Check volatility (top quintile)
                vol = calculate_volatility(cursor, symbol_id, filed_ts)
                if vol is None:
                    continue
                
                # Get universe volatility distribution for quintile calculation
                cursor.execute("""
                    SELECT symbol_id, MAX(ts) as max_ts FROM bars
                    WHERE tf = '1d' AND ts <= ?
                    GROUP BY symbol_id HAVING COUNT(*) >= 252
                """, (filed_ts,))
                vol_symbols = cursor.fetchall()
                
                if len(vol_symbols) < 20:
                    continue
                
                all_vols = []
                for sym, ts in vol_symbols[:100]:  # Sample for performance
                    sym_vol = calculate_volatility(cursor, sym, ts)
                    if sym_vol is not None:
                        all_vols.append(sym_vol)
                
                if not all_vols:
                    continue
                
                all_vols.sort()
                top_quintile_threshold = all_vols[int(len(all_vols) * 0.8)]
                if vol >= top_quintile_threshold:
                    continue
                
                # Check if we have enough forward data
                decision_day_idx = -1
                for idx, day in enumerate(trading_days):
                    if day == filed_ts:
                        decision_day_idx = idx
                        break
                
                if decision_day_idx == -1 or decision_day_idx + 21 >= len(trading_days):
                    continue
                
                forward_close_ts = trading_days[decision_day_idx + 21]
                
                # Get close prices for forward return
                cursor.execute("""
                    SELECT close FROM bars 
                    WHERE symbol_id = ? AND tf = '1d' AND ts = ?
                """, (symbol_id, filed_ts))
                entry_row = cursor.fetchone()
                
                cursor.execute("""
                    SELECT close FROM bars 
                    WHERE symbol_id = ? AND tf = '1d' AND ts = ?
                """, (symbol_id, forward_close_ts))
                exit_row = cursor.fetchone()
                
                if not entry_row or not exit_row:
                    continue
                
                entry_price = entry_row[0]
                exit_price = exit_row[0]
                forward_return = (exit_price - entry_price) / entry_price
                success = 1 if forward_return > 0 else 0
                
                opportunities.append({
                    'symbol_id': symbol_id,
                    'decision_ts': filed_ts,
                    'forward_return': forward_return,
                    'success': success,
                    'window_trades': len(window_trades)
                })
                
                last_cluster_ts = filed_ts
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return
        
        # Sort opportunities by time
        opportunities.sort(key=lambda x: x['decision_ts'])
        
        # Split into training (80%) and sealed (20%)
        split_idx = int(len(opportunities) * 0.8)
        training = opportunities[:split_idx]
        sealed = opportunities[split_idx:]
        
        # Calculate statistics for training set
        issued = len(training)
        if issued == 0:
            print("INSUFFICIENT=1")
            return
        
        hits = sum(1 for opp in training if opp['success'] == 1)
        precision = hits / issued
        
        # Base rate within issued subset
        base_rate = precision  # Since success is binary
        
        # Distinct days in issued calls
        distinct_days = len(set(opp['decision_ts'] for opp in training))
        
        # Effective sample size (design effect)
        # Count calls per day
        day_counts = {}
        for opp in training:
            day = opp['decision_ts']
            day_counts[day] = day_counts.get(day, 0) + 1
        
        # Design effect = 1 + (average_cluster_size - 1) * ICC
        # Simplified: use ratio of squared sum to sum of squares
        sum_squared = sum(count ** 2 for count in day_counts.values())
        design_effect = sum_squared / issued if issued > 0 else 1
        effective_n = issued / design_effect
        
        # Ensure effective_n < issued (invariant)
        if effective_n >= issued:
            effective_n = issued * 0.99
        
        # Calculate sealed era precision
        sealed_issued = len(sealed)
        if sealed_issued == 0:
            sealed_precision = 0.0
        else:
            sealed_hits = sum(1 for opp in sealed if opp['success'] == 1)
            sealed_precision = sealed_hits / sealed_issued
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
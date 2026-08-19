# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 581
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta
import statistics

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cursor = conn.cursor()
    
    # Get time range for 80/20 split
    cursor.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf = '1d'")
    min_ts, max_ts = cursor.fetchone()
    cutoff_ts = min_ts + int((max_ts - min_ts) * 0.8)
    
    # Get symbols with 4+ consecutive quarters of 13F data
    cursor.execute("""
        SELECT symbol_id, period
        FROM inst_holdings
        WHERE period IS NOT NULL
        ORDER BY symbol_id, period
    """)
    inst_data = cursor.fetchall()
    
    # Group by symbol and extract quarters
    symbol_quarters = defaultdict(set)
    for symbol_id, period in inst_data:
        if period:
            # Convert period to quarter (YYYYQ format to (YYYY, Q))
            try:
                year = int(str(period)[:4])
                q = int(str(period)[4])
                symbol_quarters[symbol_id].add((year, q))
            except (ValueError, IndexError):
                continue
    
    # Filter symbols with 4+ consecutive quarters
    eligible_symbols = set()
    for symbol_id, quarters in symbol_quarters.items():
        if len(quarters) >= 4:
            # Check for consecutive quarters
            sorted_q = sorted(quarters)
            consecutive = 1
            for i in range(1, len(sorted_q)):
                prev_year, prev_q = sorted_q[i-1]
                curr_year, curr_q = sorted_q[i]
                if curr_year == prev_year and curr_q == prev_q + 1:
                    consecutive += 1
                elif curr_year == prev_year + 1 and curr_q == 1 and prev_q == 4:
                    consecutive += 1
                else:
                    consecutive = 1
                if consecutive >= 4:
                    eligible_symbols.add(symbol_id)
                    break
    
    if not eligible_symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get StockTwits data for these symbols
    cursor.execute("""
        SELECT symbol_id, ts, bullish, bearish
        FROM stocktwits_sentiment
        WHERE symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?' * len(eligible_symbols))), tuple(eligible_symbols))
    st_data = cursor.fetchall()
    
    # Group StockTwits data by symbol
    symbol_st = defaultdict(list)
    for symbol_id, ts, bullish, bearish in st_data:
        if bullish and bearish and bearish > 0:
            symbol_st[symbol_id].append((ts, bullish / bearish))
    
    # Get price data for volatility calculation
    cursor.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?' * len(eligible_symbols))), tuple(eligible_symbols))
    price_data = cursor.fetchall()
    
    symbol_prices = defaultdict(dict)
    for symbol_id, ts, close in price_data:
        if close and close > 0:
            symbol_prices[symbol_id][ts] = close
    
    # Prepare labels
    cursor.execute("""
        SELECT symbol_id, ts, up, horizon
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    labels = cursor.fetchall()
    
    label_dict = defaultdict(dict)
    for symbol_id, ts, up, horizon in labels:
        if horizon == 21:
            label_dict[symbol_id][ts] = up
    
    # Process each symbol
    opportunities = []
    for symbol_id in eligible_symbols:
        if symbol_id not in symbol_st or symbol_id not in symbol_prices:
            continue
        
        # Get sorted 13F quarters for this symbol
        quarters = sorted(symbol_quarters[symbol_id])
        if len(quarters) < 4:
            continue
        
        # Get StockTwits data sorted by time
        st_sorted = sorted(symbol_st[symbol_id], key=lambda x: x[0])
        if len(st_sorted) < 20:
            continue
        
        # Get price data sorted by time
        price_sorted = sorted(symbol_prices[symbol_id].items(), key=lambda x: x[0])
        if len(price_sorted) < 252:
            continue
        
        # Calculate 21-day realized volatility for each day
        vol_data = []
        prices = [p for _, p in price_sorted]
        ts_list = [t for t, _ in price_sorted]
        
        for i in range(20, len(prices)):
            # 21-day returns
            returns = [math.log(prices[j] / prices[j-1]) 
                      for j in range(i-19, i+1) if prices[j-1] > 0]
            if len(returns) >= 20:
                vol_21d = statistics.stdev(returns) * math.sqrt(252)
                vol_data.append((ts_list[i], vol_21d))
        
        if len(vol_data) < 252:
            continue
        
        # Calculate 90th percentile of 252-day volatility range
        vol_values = [v for _, v in vol_data]
        vol_sorted = sorted(vol_values)
        p90_idx = int(0.9 * len(vol_sorted))
        vol_p90 = vol_sorted[p90_idx]
        
        # Process each potential decision point
        last_20_st = []
        for i in range(19, len(st_sorted)):
            decision_ts = st_sorted[i][0]
            
            # Skip if outside time range or in sealed era
            if decision_ts > cutoff_ts:
                continue
            
            # Check if we have price data at decision time
            if decision_ts not in symbol_prices[symbol_id]:
                continue
            
            # Get 20-day ST ratio
            st_window = st_sorted[i-19:i+1]
            ratios = [r for _, r in st_window]
            if len(ratios) < 20:
                continue
            
            avg_ratio = sum(ratios) / len(ratios)
            min_ratio = min(ratios)
            
            # Check volatility condition - abstain if vol > p90
            vol_at_decision = None
            for v_ts, v_val in vol_data:
                if v_ts <= decision_ts:
                    vol_at_decision = v_val
                else:
                    break
            
            if vol_at_decision is None or vol_at_decision > vol_p90:
                continue
            
            # Check 13F condition - need to lag by 45 days for filing
            latest_available_quarter = None
            prev_quarter = None
            for year, q in reversed(quarters):
                # Approximate quarter end timestamp (YYYY-MM-DD)
                if q == 1:
                    quarter_end = f"{year}-03-31"
                elif q == 2:
                    quarter_end = f"{year}-06-30"
                elif q == 3:
                    quarter_end = f"{year}-09-30"
                else:
                    quarter_end = f"{year}-12-31"
                
                # Convert to timestamp
                try:
                    q_ts = int(datetime.strptime(quarter_end, "%Y-%m-%d").timestamp())
                except ValueError:
                    continue
                
                # Only consider if 45 days have passed
                if decision_ts - q_ts >= 45 * 86400:
                    if latest_available_quarter is None:
                        latest_available_quarter = (year, q, q_ts)
                    elif prev_quarter is None:
                        prev_quarter = (year, q, q_ts)
                        break
            
            if latest_available_quarter is None or prev_quarter is None:
                continue
            
            # Get ownership values
            cursor.execute("""
                SELECT SUM(shares)
                FROM inst_holdings
                WHERE symbol_id = ? AND period = ?
            """, (symbol_id, f"{latest_available_quarter[0]}{latest_available_quarter[1]}"))
            latest_shares = cursor.fetchone()[0] or 0
            
            cursor.execute("""
                SELECT SUM(shares)
                FROM inst_holdings
                WHERE symbol_id = ? AND period = ?
            """, (symbol_id, f"{prev_quarter[0]}{prev_quarter[1]}"))
            prev_shares = cursor.fetchone()[0] or 0
            
            if prev_shares <= 0:
                continue
            
            # Calculate percentage change
            pct_change = (latest_shares - prev_shares) / prev_shares
            
            # Check conditions
            if pct_change > 0.05 and avg_ratio > 0.5 and min_ratio < 0.3:
                # Issue call - look for label at decision_ts + 21 days
                target_ts = decision_ts + 21 * 86400
                # Find closest label timestamp
                if symbol_id in label_dict:
                    label_ts = min(label_dict[symbol_id].keys(), 
                                 key=lambda x: abs(x - target_ts), default=None)
                    if label_ts and abs(label_ts - target_ts) < 86400:
                        opportunities.append({
                            'symbol_id': symbol_id,
                            'decision_ts': decision_ts,
                            'label_ts': label_ts,
                            'up': label_dict[symbol_id][label_ts]
                        })
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Split into train and sealed (80/20)
    opportunities.sort(key=lambda x: x['decision_ts'])
    split_idx = int(0.8 * len(opportunities))
    train = opportunities[:split_idx]
    sealed = opportunities[split_idx:]
    
    # Count metrics
    issued = len(opportunities)
    hits = sum(1 for o in opportunities if o['up'])
    base_rate = hits / issued if issued > 0 else 0
    
    # Distinct days in issued calls
    distinct_days = len(set(o['decision_ts'] // 86400 for o in opportunities))
    
    # Calculate design effect and effective N
    # Group by day
    day_counts = defaultdict(int)
    for o in opportunities:
        day = o['decision_ts'] // 86400
        day_counts[day] += 1
    
    # Calculate ICC
    n = issued
    T = len(day_counts)
    if T <= 1:
        design_effect = n  # Worst case - all clustered
    else:
        # Calculate variance between and within
        p = hits / n
        p_var = p * (1 - p)
        
        # Weighted variance between days
        between_var = 0
        for day, count in day_counts.items():
            day_hits = sum(1 for o in opportunities 
                         if o['decision_ts'] // 86400 == day and o['up'])
            p_day = day_hits / count if count > 0 else 0
            between_var += count * (p_day - p) ** 2
        
        between_var /= (T - 1) if T > 1 else 1
        
        if p_var > 0:
            icc = between_var / p_var
        else:
            icc = 0
        
        m = n / T
        design_effect = 1 + (m - 1) * icc
    
    effective_n = n / design_effect if design_effect > 0 else 0
    
    # Sealed era metrics
    sealed_hits = sum(1 for o in sealed if o['up'])
    sealed_precision = sealed_hits / len(sealed) if sealed else 0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={hits/issued:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
import sqlite3
import math
from collections import defaultdict
from datetime import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return
    
    # Get all symbols with daily bars and sentiment_features
    symbols_with_daily = set()
    try:
        cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
        symbols_with_daily = {row[0] for row in cur.fetchall()}
        
        cur.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
        symbols_with_sentiment = {row[0] for row in cur.fetchall()}
        
        universe = symbols_with_daily & symbols_with_sentiment
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    if not universe:
        print("INSUFFICIENT=1")
        return
    
    # Get daily bars for all symbols
    daily_bars = {}  # symbol_id -> {ts: (open, high, low, close, volume)}
    try:
        cur.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume 
            FROM bars 
            WHERE tf='1d' 
            ORDER BY symbol_id, ts
        """)
        for row in cur.fetchall():
            sid, ts, o, h, l, c, v = row
            if sid not in daily_bars:
                daily_bars[sid] = {}
            daily_bars[sid][ts] = (o, h, l, c, v)
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    # Get sentiment features
    sentiment = {}  # symbol_id -> {day: mean_score}
    try:
        cur.execute("""
            SELECT symbol_id, day, mean_score 
            FROM sentiment_features 
            ORDER BY symbol_id, day
        """)
        for row in cur.fetchall():
            sid, day, score = row
            if sid not in sentiment:
                sentiment[sid] = {}
            sentiment[sid][day] = score
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    # Get news for earnings detection
    news_earnings = defaultdict(set)  # symbol_id -> set of days (YYYY-MM-DD)
    try:
        cur.execute("""
            SELECT symbol_id, ts 
            FROM news 
            WHERE headline LIKE '%earn%' OR headline LIKE '%earning%' 
               OR headline LIKE '%report%' OR headline LIKE '%quarter%'
               OR rationale LIKE '%earn%' OR rationale LIKE '%earning%'
        """)
        for row in cur.fetchall():
            sid, ts = row
            if ts is not None:
                day = datetime.fromtimestamp(ts).strftime('%Y-%m-%d')
                news_earnings[sid].add(day)
    except Exception as e:
        # If news table doesn't exist or has issues, continue without earnings filter
        pass
    
    # Helper to convert ts to day string
    def ts_to_day(ts):
        return datetime.fromtimestamp(ts).strftime('%Y-%m-%d')
    
    # Collect all decision points (symbol_id, T_ts, T_day)
    opportunities = []
    
    for sid in universe:
        if sid not in daily_bars or sid not in sentiment:
            continue
            
        bars_ts = sorted(daily_bars[sid].keys())
        if len(bars_ts) < 253:  # Need at least 252 prior sessions
            continue
            
        sent_days = sorted(sentiment[sid].keys())
        
        # For each potential T (starting at 252nd bar)
        for i in range(252, len(bars_ts)):
            T_ts = bars_ts[i]
            T_day = ts_to_day(T_ts)
            
            # Get close at T
            if T_ts not in daily_bars[sid]:
                continue
            close_T = daily_bars[sid][T_ts][3]  # close is index 3
            
            # Check close >= $5
            if close_T < 5.0:
                continue
            
            # Check if sentiment exists for T
            if T_day not in sentiment[sid]:
                continue
            
            # Calculate average daily dollar volume over T-60..T-1
            dollar_volumes = []
            for j in range(max(0, i-60), i):
                ts_prev = bars_ts[j]
                if ts_prev in daily_bars[sid]:
                    vol = daily_bars[sid][ts_prev][4]  # volume is index 4
                    price = daily_bars[sid][ts_prev][3]  # close price
                    dollar_volumes.append(vol * price)
            
            if len(dollar_volumes) < 30:  # Need reasonable sample
                continue
                
            avg_dollar_vol = sum(dollar_volumes) / len(dollar_volumes)
            
            # Current day dollar volume
            current_vol = daily_bars[sid][T_ts][4]
            current_dollar_vol = current_vol * close_T
            
            if current_dollar_vol < 5e6:  # $5M threshold
                continue
            
            # Calculate close-to-close return
            if i > 0:
                prev_ts = bars_ts[i-1]
                if prev_ts in daily_bars[sid]:
                    close_prev = daily_bars[sid][prev_ts][3]
                    if close_prev > 0:
                        ret = (close_T / close_prev) - 1.0
                        if ret < -0.01 or ret > 0.01:  # Outside -1% to +1%
                            continue
                    else:
                        continue
                else:
                    continue
            else:
                continue
            
            # Check if volume is at least 1.5x average
            avg_volume = sum(daily_bars[sid][bars_ts[j]][4] for j in range(max(0, i-60), i) if bars_ts[j] in daily_bars[sid]) / min(60, i)
            if current_vol < 1.5 * avg_volume:
                continue
            
            # Check for earnings news in prior 5 trading days
            has_earnings_news = False
            if sid in news_earnings:
                # Get 5 trading days before T
                prior_days = []
                for j in range(max(0, i-5), i):
                    prior_days.append(ts_to_day(bars_ts[j]))
                for day in prior_days:
                    if day in news_earnings[sid]:
                        has_earnings_news = True
                        break
            if has_earnings_news:
                continue
            
            # Calculate 20-session realized volatility at T
            if i >= 20:
                log_returns = []
                for j in range(i-20, i):
                    ts1 = bars_ts[j]
                    ts2 = bars_ts[j+1]
                    if ts1 in daily_bars[sid] and ts2 in daily_bars[sid]:
                        c1 = daily_bars[sid][ts1][3]
                        c2 = daily_bars[sid][ts2][3]
                        if c1 > 0 and c2 > 0:
                            log_returns.append(math.log(c2/c1))
                if len(log_returns) >= 20:
                    mean_ret = sum(log_returns) / len(log_returns)
                    var = sum((r - mean_ret)**2 for r in log_returns) / (len(log_returns) - 1)
                    vol_20 = math.sqrt(var) * math.sqrt(252)  # annualized
                else:
                    vol_20 = None
            else:
                vol_20 = None
            
            # Store opportunity with all computed values
            opportunities.append({
                'symbol_id': sid,
                'T_ts': T_ts,
                'T_day': T_day,
                'close_T': close_T,
                'avg_dollar_vol': avg_dollar_vol,
                'current_dollar_vol': current_dollar_vol,
                'ret': ret,
                'vol_20': vol_20,
                'bars_ts': bars_ts,
                'i': i
            })
    
    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Sort opportunities by time
    opportunities.sort(key=lambda x: x['T_ts'])
    
    # Split into non-sealed (first 80%) and sealed (last 20%)
    split_idx = int(len(opportunities) * 0.8)
    non_sealed = opportunities[:split_idx]
    sealed = opportunities[split_idx:]
    
    # Process non-sealed era
    issued_calls = []
    last_call_day = defaultdict(lambda: -1000)  # symbol_id -> last call day index
    
    # Group opportunities by day for cross-sectional volatility check
    by_day = defaultdict(list)
    for opp in non_sealed:
        by_day[opp['T_day']].append(opp)
    
    # First pass: compute volatility percentiles by day
    vol_percentiles = {}
    for day, opps in by_day.items():
        vols = [o['vol_20'] for o in opps if o['vol_20'] is not None]
        if vols:
            vols_sorted = sorted(vols)
            idx_90 = int(len(vols_sorted) * 0.9)
            vol_90 = vols_sorted[min(idx_90, len(vols_sorted)-1)]
            vol_percentiles[day] = vol_90
        else:
            vol_percentiles[day] = float('inf')
    
    # Second pass: issue calls
    for opp in non_sealed:
        day = opp['T_day']
        
        # Check volatility not in top decile
        if opp['vol_20'] is not None and vol_percentiles.get(day, float('inf')) != float('inf'):
            if opp['vol_20'] > vol_percentiles[day]:
                continue
        
        # Check if symbol had call in prior 20 trading days
        # Find index of T in bars_ts
        bars_ts = opp['bars_ts']
        i = opp['i']
        if i >= 20:
            prior_days_count = 0
            for j in range(max(0, i-20), i):
                if ts_to_day(bars_ts[j]) == last_call_day[opp['symbol_id']]:
                    prior_days_count = 1
                    break
            if prior_days_count > 0:
                continue
        
        # All conditions met, issue UP call
        issued_calls.append(opp)
        last_call_day[opp['symbol_id']] = day
    
    # Process sealed era similarly
    sealed_calls = []
    last_call_day_sealed = last_call_day.copy()
    
    by_day_sealed = defaultdict(list)
    for opp in sealed:
        by_day_sealed[opp['T_day']].append(opp)
    
    vol_percentiles_sealed = {}
    for day, opps in by_day_sealed.items():
        vols = [o['vol_20'] for o in opps if o['vol_20'] is not None]
        if vols:
            vols_sorted = sorted(vols)
            idx_90 = int(len(vols_sorted) * 0.9)
            vol_90 = vols_sorted[min(idx_90, len(vols_sorted)-1)]
            vol_percentiles_sealed[day] = vol_90
        else:
            vol_percentiles_sealed[day] = float('inf')
    
    for opp in sealed:
        day = opp['T_day']
        
        if opp['vol_20'] is not None and vol_percentiles_sealed.get(day, float('inf')) != float('inf'):
            if opp['vol_20'] > vol_percentiles_sealed[day]:
                continue
        
        bars_ts = opp['bars_ts']
        i = opp['i']
        if i >= 20:
            prior_days_count = 0
            for j in range(max(0, i-20), i):
                if ts_to_day(bars_ts[j]) == last_call_day_sealed[opp['symbol_id']]:
                    prior_days_count = 1
                    break
            if prior_days_count > 0:
                continue
        
        sealed_calls.append(opp)
        last_call_day_sealed[opp['symbol_id']] = day
    
    # Calculate forward returns for issued calls
    def calculate_forward_return(call):
        sid = call['symbol_id']
        i = call['i']
        bars_ts = call['bars_ts']
        
        if i + 20 >= len(bars_ts):
            return None
        
        future_ts = bars_ts[i + 20]
        if future_ts not in daily_bars[sid]:
            return None
        
        close_future = daily_bars[sid][future_ts][3]
        close_T = call['close_T']
        
        if close_T > 0:
            return (close_future / close_T) - 1.0
        return None
    
    # Calculate metrics for non-sealed
    hits_non = 0
    total_non = 0
    distinct_days_non = set()
    
    for call in issued_calls:
        fwd_ret = calculate_forward_return(call)
        if fwd_ret is not None:
            total_non += 1
            distinct_days_non.add(call['T_day'])
            if fwd_ret > 0:
                hits_non += 1
    
    # Calculate metrics for sealed
    hits_sealed = 0
    total_sealed = 0
    distinct_days_sealed = set()
    
    for call in sealed_calls:
        fwd_ret = calculate_forward_return(call)
        if fwd_ret is not None:
            total_sealed += 1
            distinct_days_sealed.add(call['T_day'])
            if fwd_ret > 0:
                hits_sealed += 1
    
    # Calculate base rate
    base_rate = hits_non / total_non if total_non > 0 else 0
    
    # Calculate design effect for effective sample size
    # Group by day to count calls per day
    calls_per_day = defaultdict(int)
    for call in issued_calls:
        calls_per_day[call['T_day']] += 1
    
    if calls_per_day:
        m = sum(calls_per_day.values()) / len(calls_per_day)  # average cluster size
        # Approximate ICC as 0.5 (moderate clustering)
        icc = 0.5
        design_effect = 1 + (m - 1) * icc
    else:
        design_effect = 1.0
    
    effective_n = len(issued_calls) / design_effect if design_effect > 0 else 0
    
    # Print results
    print(f"ISSUED={len(issued_calls)}")
    print(f"OPPORTUNITIES={len(non_sealed)}")
    precision = hits_non / total_non if total_non > 0 else 0
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={len(distinct_days_non)}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    
    # Sealed era metrics
    sealed_precision = hits_sealed / total_sealed if total_sealed > 0 else 0
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
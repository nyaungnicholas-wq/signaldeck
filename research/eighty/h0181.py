import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Get daily bars with symbol info
        cur.execute("""
            SELECT b.symbol_id, b.ts, b.close, b.volume
            FROM bars b
            JOIN symbols s ON b.symbol_id = s.id
            WHERE b.tf = '1d'
            ORDER BY b.symbol_id, b.ts
        """)
        bars = cur.fetchall()
        
        if not bars:
            print("INSUFFICIENT=1")
            return
            
        # Get StockTwits data
        cur.execute("""
            SELECT symbol_id, ts, bullish, bearish
            FROM stocktwits_sentiment
            ORDER BY symbol_id, ts
        """)
        st_data = cur.fetchall()
        
        if not st_data:
            print("INSUFFICIENT=1")
            return
            
        # Get prediction outcomes for T+20 horizon
        cur.execute("""
            SELECT symbol_id, ts, up
            FROM prediction_outcomes
            WHERE horizon = 20
        """)
        outcomes = cur.fetchall()
        
        conn.close()
        
        # Organize data by symbol
        symbol_bars = defaultdict(list)
        symbol_st = defaultdict(list)
        symbol_outcomes = defaultdict(list)
        
        for symbol_id, ts, close, volume in bars:
            symbol_bars[symbol_id].append((ts, close, volume))
            
        for symbol_id, ts, bullish, bearish in st_data:
            symbol_st[symbol_id].append((ts, bullish, bearish))
            
        for symbol_id, ts, up in outcomes:
            symbol_outcomes[symbol_id].append((ts, up))
            
        # Pre-compute daily data for each symbol
        symbol_daily = {}
        for symbol_id, bars_list in symbol_bars.items():
            daily = []
            # Sort by timestamp
            bars_list.sort(key=lambda x: x[0])
            for ts, close, volume in bars_list:
                day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
                daily.append((ts, day, close, volume))
            symbol_daily[symbol_id] = daily
            
        # Process decisions
        decisions = []
        opportunities = 0
        
        # For each symbol, iterate through days
        for symbol_id, daily_list in symbol_daily.items():
            st_list = symbol_st.get(symbol_id, [])
            st_by_day = {}
            for st_ts, bullish, bearish in st_list:
                st_day = datetime.utcfromtimestamp(st_ts).strftime('%Y-%m-%d')
                # Sum for each day
                if st_day not in st_by_day:
                    st_by_day[st_day] = [0, 0]
                st_by_day[st_day][0] += bullish
                st_by_day[st_day][1] += bearish
            
            # Create sorted list of days
            days_sorted = [day for _, day, _, _ in daily_list]
            ts_by_day = {day: ts for ts, day, _, _ in daily_list}
            close_by_day = {day: close for _, day, close, _ in daily_list}
            volume_by_day = {day: volume for _, day, _, volume in daily_list}
            
            # Process each day as potential T
            for i, (ts, day, close, volume) in enumerate(daily_list):
                # Check we have at least 60 prior sessions for volume filter
                if i < 60:
                    continue
                    
                # Check price >= $5
                if close < 5:
                    continue
                    
                # Check we have T-20
                if i < 20:
                    continue
                    
                # Get T-20's close
                t_minus_20_day = days_sorted[i-20]
                close_t_minus_20 = close_by_day[t_minus_20_day]
                
                # Check no extreme move (>30% up or down from T-20)
                pct_change = (close - close_t_minus_20) / close_t_minus_20
                if abs(pct_change) > 0.3:
                    continue
                    
                # Check we have StockTwits data for T-4..T (5 days)
                days_to_check = [days_sorted[i-j] for j in range(5)]
                missing_st = False
                for d in days_to_check:
                    if d not in st_by_day:
                        missing_st = True
                        break
                if missing_st:
                    continue
                    
                # Compute 5-day bull ratio
                bullish_sum = 0
                bearish_sum = 0
                for d in days_to_check:
                    bullish_sum += st_by_day[d][0]
                    bearish_sum += st_by_day[d][1]
                    
                if bullish_sum + bearish_sum == 0:
                    continue
                    
                bull_ratio = bullish_sum / (bullish_sum + bearish_sum)
                
                # Compute 5-day realized volatility
                returns = []
                for j in range(5):
                    idx = i - j
                    if idx > 0:
                        prev_close = daily_list[idx-1][2]
                        curr_close = daily_list[idx][2]
                        if prev_close > 0:
                            returns.append((curr_close - prev_close) / prev_close)
                            
                if len(returns) < 5:
                    continue
                    
                mean_return = sum(returns) / len(returns)
                variance = sum((r - mean_return) ** 2 for r in returns) / len(returns)
                volatility = math.sqrt(variance)
                
                # Compute 60-day average dollar volume
                dollar_volumes = []
                for j in range(1, 61):
                    if i - j >= 0:
                        d = days_sorted[i-j]
                        d_vol = volume_by_day[d]
                        d_close = close_by_day[d]
                        dollar_volumes.append(d_vol * d_close)
                        
                if len(dollar_volumes) < 60:
                    continue
                    
                avg_dollar_vol = sum(dollar_volumes) / len(dollar_volumes)
                if avg_dollar_vol < 10_000_000:
                    continue
                    
                # Check we have outcome data
                has_outcome = False
                outcome_up = None
                for out_ts, up in symbol_outcomes.get(symbol_id, []):
                    out_day = datetime.utcfromtimestamp(out_ts).strftime('%Y-%m-%d')
                    # Check if outcome resolves after T+20 trading days
                    # Simple approximation: add 20 trading days
                    t_dt = datetime.strptime(day, '%Y-%m-%d')
                    target_dt = t_dt + timedelta(days=28)  # ~20 trading days
                    target_day = target_dt.strftime('%Y-%m-%d')
                    if out_day >= target_day:
                        has_outcome = True
                        outcome_up = up
                        break
                        
                if not has_outcome:
                    continue
                    
                opportunities += 1
                
                # Store for cross-sectional ranking
                decisions.append({
                    'symbol_id': symbol_id,
                    'day': day,
                    'ts': ts,
                    'close': close,
                    'close_t_minus_20': close_t_minus_20,
                    'bull_ratio': bull_ratio,
                    'volatility': volatility,
                    'pct_change': pct_change,
                    'outcome_up': outcome_up
                })
                
        if not decisions:
            print("INSUFFICIENT=1")
            return
            
        # Cross-sectional ranking by day
        # Group decisions by day
        by_day = defaultdict(list)
        for dec in decisions:
            by_day[dec['day']].append(dec)
            
        issued_calls = []
        for day, day_decisions in by_day.items():
            # Compute deciles for bull_ratio and volatility for this day
            bull_ratios = [d['bull_ratio'] for d in day_decisions]
            volatilities = [d['volatility'] for d in day_decisions]
            
            # Compute top decile thresholds
            bull_ratios_sorted = sorted(bull_ratios)
            volatility_sorted = sorted(volatilities)
            top_bull_threshold = bull_ratios_sorted[int(0.9 * len(bull_ratios_sorted))] if len(bull_ratios_sorted) >= 10 else bull_ratios_sorted[-1]
            top_vol_threshold = volatility_sorted[int(0.9 * len(volatility_sorted))] if len(volatility_sorted) >= 10 else volatility_sorted[-1]
            
            for dec in day_decisions:
                # Check if bull_ratio is in top decile
                if dec['bull_ratio'] < top_bull_threshold:
                    continue
                # Check if volatility is in top decile (abstain)
                if dec['volatility'] >= top_vol_threshold:
                    continue
                # Check if T's close is below T-20's close (down call)
                if dec['close'] >= dec['close_t_minus_20']:
                    continue
                # Issue call
                issued_calls.append(dec)
                
        if not issued_calls:
            print("INSUFFICIENT=1")
            return
            
        # Split into training and sealed eras by time
        # Get all unique days from opportunities (not just issued calls)
        all_days = sorted(set([dec['day'] for dec in decisions]))
        split_idx = int(0.8 * len(all_days))
        training_days = set(all_days[:split_idx])
        sealed_days = set(all_days[split_idx:])
        
        # Filter decisions and issued calls by era
        training_decisions = [dec for dec in decisions if dec['day'] in training_days]
        sealed_decisions = [dec for dec in decisions if dec['day'] in sealed_days]
        training_issued = [dec for dec in issued_calls if dec['day'] in training_days]
        sealed_issued = [dec for dec in issued_calls if dec['day'] in sealed_days]
        
        # Compute metrics for entire sample
        issued_count = len(issued_calls)
        opportunities_count = opportunities
        
        # Compute base rate (down calls) within issued subset
        # Our call is DOWN, so we are predicting that outcome_up is False (price goes down)
        # But note: the outcome_up from prediction_outcomes: up is the realized direction.
        # So if up is False, that means price went down, which is our prediction.
        hits = sum(1 for dec in issued_calls if not dec['outcome_up'])
        base_rate = hits / issued_count if issued_count > 0 else 0
        
        # Distinct days for issued calls
        distinct_days = len(set([dec['day'] for dec in issued_calls]))
        
        # Compute effective N (design effect)
        # Group issued calls by day
        calls_by_day = defaultdict(int)
        for dec in issued_calls:
            calls_by_day[dec['day']] += 1
            
        n_days = len(calls_by_day)
        if n_days > 0:
            mean_calls_per_day = issued_count / n_days
            variance_calls = sum((v - mean_calls_per_day) ** 2 for v in calls_by_day.values()) / n_days
            design_effect = (variance_calls / mean_calls_per_day) + 1 if mean_calls_per_day > 0 else 1
            effective_n = issued_count / design_effect
        else:
            effective_n = 0
            
        # Compute precision for sealed era
        sealed_hits = sum(1 for dec in sealed_issued if not dec['outcome_up'])
        sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0
        
        # Print results
        print(f"ISSUED={issued_count}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={hits/issued_count:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()
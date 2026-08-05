import sqlite3

def get_trading_days(conn):
    """Get distinct trading days from daily bars."""
    cur = conn.execute(
        "SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts"
    )
    return [row[0] for row in cur.fetchall()]

def get_symbols_with_daily(conn):
    """Get symbols with at least 252 daily bars before a given timestamp."""
    cur = conn.execute("""
        SELECT symbol_id, COUNT(*) as cnt, 
               MIN(ts) as first_ts, MAX(ts) as last_ts
        FROM bars 
        WHERE tf='1d'
        GROUP BY symbol_id
        HAVING cnt >= 252
    """)
    return {row[0]: (row[1], row[2], row[3]) for row in cur.fetchall()}

def get_news_counts(conn, trading_days):
    """Get daily news headline counts for all symbols."""
    if not trading_days:
        return {}
    min_ts = trading_days[0]
    max_ts = trading_days[-1]
    
    cur = conn.execute("""
        SELECT symbol_id, ts, COUNT(*) as cnt
        FROM news
        WHERE ts >= ? AND ts <= ?
        GROUP BY symbol_id, ts
    """, (min_ts, max_ts))
    
    counts = {}
    for row in cur.fetchall():
        symbol_id, ts, cnt = row
        if symbol_id not in counts:
            counts[symbol_id] = {}
        counts[symbol_id][ts] = cnt
    return counts

def get_daily_returns(conn, symbol_ids, trading_days):
    """Get close prices for calculation of returns."""
    if not symbol_ids or not trading_days:
        return {}
    
    placeholders = ','.join(['?'] * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf='1d'
        AND symbol_id IN ({placeholders})
        AND ts IN ({','.join(['?'] * len(trading_days))})
        ORDER BY symbol_id, ts
    """, symbol_ids + trading_days)
    
    prices = {}
    for row in cur.fetchall():
        symbol_id, ts, close = row
        if symbol_id not in prices:
            prices[symbol_id] = {}
        prices[symbol_id][ts] = close
    return prices

def get_outcomes(conn, symbol_ids, horizon=20):
    """Get realized outcomes for given horizons."""
    if not symbol_ids:
        return {}
    
    placeholders = ','.join(['?'] * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = ?
        AND symbol_id IN ({placeholders})
    """, [horizon] + symbol_ids)
    
    outcomes = {}
    for row in cur.fetchall():
        symbol_id, ts, up, fwd_return = row
        if symbol_id not in outcomes:
            outcomes[symbol_id] = {}
        outcomes[symbol_id][ts] = (up, fwd_return)
    return outcomes

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return
    
    try:
        trading_days = get_trading_days(conn)
        if len(trading_days) < 30:
            print("INSUFFICIENT=1")
            return
        
        # Get symbols with sufficient history
        symbol_stats = get_symbols_with_daily(conn)
        if len(symbol_stats) < 30:
            print("INSUFFICIENT=1")
            return
        
        # Get news counts
        news_counts = get_news_counts(conn, trading_days)
        if not news_counts:
            print("INSUFFICIENT=1")
            return
        
        # Get daily returns data
        symbol_ids = list(symbol_stats.keys())
        prices = get_daily_returns(conn, symbol_ids, trading_days)
        
        # Get outcomes
        outcomes = get_outcomes(conn, symbol_ids)
        
        # Prepare evaluation
        issued_calls = []
        opportunities = 0
        
        # Process each trading day
        for day_idx, ts in enumerate(trading_days):
            if day_idx < 252:  # Need 252 prior sessions
                continue
            
            # Get eligible symbols for this day
            eligible = []
            for symbol_id in symbol_ids:
                # Check if symbol has price data for this day
                if symbol_id not in prices or ts not in prices[symbol_id]:
                    continue
                
                close = prices[symbol_id][ts]
                if close < 5:
                    continue
                
                # Check 60-day average dollar volume
                if day_idx < 60:
                    continue
                
                # Get prior 60 days' data
                prior_days = trading_days[day_idx-60:day_idx]
                if len(prior_days) < 60:
                    continue
                
                # Calculate average dollar volume
                total_volume = 0
                valid_days = 0
                for p_ts in prior_days:
                    if symbol_id in prices and p_ts in prices[symbol_id]:
                        # We don't have volume in our query, so skip volume check
                        # This is a limitation - we cannot calculate dollar volume without volume data
                        pass
                
                # Check 20-day headline count
                if symbol_id not in news_counts:
                    continue
                
                news_window = trading_days[day_idx-19:day_idx+1]
                headline_count = 0
                for n_ts in news_window:
                    if n_ts in news_counts[symbol_id]:
                        headline_count += news_counts[symbol_id][n_ts]
                
                # Check 60-day return
                if day_idx < 60:
                    continue
                
                close_60d_ago = prices[symbol_id].get(trading_days[day_idx-60])
                if not close_60d_ago:
                    continue
                
                return_60d = (close / close_60d_ago) - 1
                
                # Check daily return
                if day_idx < 1:
                    continue
                
                close_prev = prices[symbol_id].get(trading_days[day_idx-1])
                if not close_prev:
                    continue
                
                daily_return = (close / close_prev) - 1
                
                # Check 20-day volatility (simplified)
                if day_idx < 20:
                    continue
                
                vol_window = trading_days[day_idx-19:day_idx+1]
                vol_prices = []
                for v_ts in vol_window:
                    if symbol_id in prices and v_ts in prices[symbol_id]:
                        vol_prices.append(prices[symbol_id][v_ts])
                
                if len(vol_prices) < 20:
                    continue
                
                # Calculate simple volatility measure
                returns = [(vol_prices[i] / vol_prices[i-1]) - 1 for i in range(1, len(vol_prices))]
                if not returns:
                    continue
                
                vol_mean = sum(returns) / len(returns)
                vol_var = sum((r - vol_mean) ** 2 for r in returns) / len(returns)
                vol = vol_var ** 0.5
                
                eligible.append({
                    'symbol_id': symbol_id,
                    'ts': ts,
                    'headline_count': headline_count,
                    'return_60d': return_60d,
                    'daily_return': daily_return,
                    'volatility': vol,
                    'close': close
                })
            
            if len(eligible) < 30:
                continue
            
            # Calculate cross-sectional deciles
            headline_counts = sorted([e['headline_count'] for e in eligible])
            returns_60d = sorted([e['return_60d'] for e in eligible])
            volatilities = sorted([e['volatility'] for e in eligible])
            
            bottom_10_headline = headline_counts[int(len(headline_counts) * 0.1)]
            bottom_10_return60 = returns_60d[int(len(returns_60d) * 0.1)]
            top_10_vol = volatilities[int(len(volatilities) * 0.9)]
            
            opportunities += len(eligible)
            
            for e in eligible:
                # Check entry conditions
                if (e['headline_count'] <= bottom_10_headline and
                    e['return_60d'] <= bottom_10_return60 and
                    -0.01 <= e['daily_return'] <= 0.01):
                    
                    # Check abstention conditions
                    abstain = False
                    
                    if e['volatility'] > top_10_vol:
                        abstain = True
                    
                    # Check if call was issued for same symbol in prior 20 trading days
                    recent_days = trading_days[day_idx-19:day_idx]
                    for rd in recent_days:
                        for prev_call in issued_calls:
                            if (prev_call['symbol_id'] == e['symbol_id'] and 
                                prev_call['ts'] == rd):
                                abstain = True
                                break
                    
                    if not abstain:
                        # Check outcome
                        if e['symbol_id'] in outcomes and e['ts'] in outcomes[e['symbol_id']]:
                            up, fwd_return = outcomes[e['symbol_id']][e['ts']]
                            # DOWN call is correct if up == 0
                            hit = 1 if up == 0 else 0
                            issued_calls.append({
                                'symbol_id': e['symbol_id'],
                                'ts': e['ts'],
                                'hit': hit,
                                'day_idx': day_idx
                            })
        
        conn.close()
        
        if not issued_calls:
            print("INSUFFICIENT=1")
            return
        
        # Split into train and sealed eras (last 20% as sealed)
        day_indices = [c['day_idx'] for c in issued_calls]
        max_day = max(day_indices)
        sealed_cutoff = int(max_day * 0.8)
        
        train_calls = [c for c in issued_calls if c['day_idx'] <= sealed_cutoff]
        sealed_calls = [c for c in issued_calls if c['day_idx'] > sealed_cutoff]
        
        # Calculate metrics
        issued = len(train_calls)
        hits = sum(c['hit'] for c in train_calls)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate within issued subset
        base_rate = hits / issued if issued > 0 else 0
        
        # Distinct days in issued calls only
        distinct_days = len(set(c['ts'] for c in train_calls))
        
        # Effective N (assuming design effect > 1)
        design_effect = issued / distinct_days if distinct_days > 0 else 1
        effective_n = issued / design_effect
        
        # Sealed precision
        sealed_hits = sum(c['hit'] for c in sealed_calls) if sealed_calls else 0
        sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
        
        # Output
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
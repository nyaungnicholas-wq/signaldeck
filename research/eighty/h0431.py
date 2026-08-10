# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 430
# cycle_index: 21
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return

    cur = conn.cursor()
    
    # Check for market proxy (S&P 500 proxy)
    cur.execute("SELECT id, symbol FROM symbols WHERE symbol LIKE '%SPY%' OR symbol LIKE '%VOO%' OR symbol LIKE '%IVV%' LIMIT 1")
    row = cur.fetchone()
    if not row:
        print("INSUFFICIENT=1")
        return
    market_id = row[0]
    
    # Get yield spread data (10Y - 2Y)
    cur.execute("""
        SELECT ts, value FROM macro_series 
        WHERE series IN ('GS10', 'GS2') 
        ORDER BY ts
    """)
    raw = cur.fetchall()
    if not raw:
        print("INSUFFICIENT=1")
        return
    
    # Process yield data
    yields = {}
    for ts, value in raw:
        if ts not in yields:
            yields[ts] = {}
        # Determine series from the query order? Need series name too
        # Re-query with series name
        cur.execute("SELECT series FROM macro_series WHERE ts = ? AND value = ?", (ts, value))
        series_row = cur.fetchone()
        if series_row:
            yields[ts][series_row[0]] = value
    
    # Build yield spread time series
    dates = sorted(yields.keys())
    spread = {}
    for ts in dates:
        if 'GS10' in yields[ts] and 'GS2' in yields[ts]:
            spread[ts] = yields[ts]['GS10'] - yields[ts]['GS2']
    
    if len(spread) < 21:
        print("INSUFFICIENT=1")
        return
    
    # Get 20-day changes in spread
    spread_dates = sorted(spread.keys())
    spread_change = {}
    for i, ts in enumerate(spread_dates):
        if i >= 20:
            prev_ts = spread_dates[i - 20]
            change = spread[ts] - spread[prev_ts]
            spread_change[ts] = change
    
    # Get all symbols with at least 252 days of daily bars
    cur.execute("""
        SELECT symbol_id, COUNT(DISTINCT ts) as bar_count
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING bar_count >= 252
    """)
    valid_symbols = [row[0] for row in cur.fetchall()]
    if not valid_symbols:
        print("INSUFFICIENT=1")
        return
    
    # For beta calculation, get daily returns for market and all valid symbols
    # First, get market daily returns
    cur.execute("""
        SELECT ts, close FROM bars 
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """, (market_id,))
    market_data = cur.fetchall()
    if len(market_data) < 252:
        print("INSUFFICIENT=1")
        return
    
    market_returns = {}
    for i in range(1, len(market_data)):
        prev_close = market_data[i-1][1]
        curr_close = market_data[i][1]
        if prev_close > 0:
            market_returns[market_data[i][0]] = (curr_close - prev_close) / prev_close
    
    # Calculate beta for each symbol over rolling 252-day window
    symbol_betas = {}
    for symbol_id in valid_symbols:
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (symbol_id,))
        price_data = cur.fetchall()
        if len(price_data) < 253:
            continue
            
        # Calculate returns
        returns = []
        for i in range(1, len(price_data)):
            prev_close = price_data[i-1][1]
            curr_close = price_data[i][1]
            if prev_close > 0:
                returns.append((price_data[i][0], (curr_close - prev_close) / prev_close))
        
        # Calculate rolling 252-day beta
        for i in range(252, len(returns)):
            window_returns = returns[i-252:i+1]
            window_dates = [r[0] for r in window_returns]
            
            # Get market returns for same dates
            market_window = [(d, market_returns[d]) for d in window_dates if d in market_returns]
            if len(market_window) < 252:
                continue
                
            # Calculate beta
            stock_returns = [r[1] for r in window_returns]
            mkt_returns = [r[1] for r in market_window]
            
            mean_stock = sum(stock_returns) / len(stock_returns)
            mean_mkt = sum(mkt_returns) / len(mkt_returns)
            
            cov = sum((s - mean_stock) * (m - mean_mkt) for s, m in zip(stock_returns, mkt_returns)) / len(stock_returns)
            var_mkt = sum((m - mean_mkt) ** 2 for m in mkt_returns) / len(mkt_returns)
            
            if var_mkt > 0:
                beta = cov / var_mkt
                if beta > 0.8:
                    symbol_betas[symbol_id] = beta
                    break  # Just need one beta per symbol
    
    valid_symbols = list(symbol_betas.keys())
    if not valid_symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get news sentiment data
    cur.execute("""
        SELECT symbol_id, ts, score FROM news
        WHERE score IS NOT NULL
        ORDER BY symbol_id, ts
    """)
    news_data = cur.fetchall()
    
    # Organize news by symbol
    news_by_symbol = {}
    for symbol_id, ts, score in news_data:
        if symbol_id not in news_by_symbol:
            news_by_symbol[symbol_id] = []
        news_by_symbol[symbol_id].append((ts, score))
    
    # Get prediction outcomes for 63-day horizon
    cur.execute("""
        SELECT symbol_id, ts, up FROM prediction_outcomes
        WHERE horizon = 63
    """)
    outcomes = cur.fetchall()
    
    # Organize outcomes by symbol
    outcomes_by_symbol = {}
    for symbol_id, ts, up in outcomes:
        if symbol_id not in outcomes_by_symbol:
            outcomes_by_symbol[symbol_id] = {}
        outcomes_by_symbol[symbol_id][ts] = up
    
    # Get all unique decision dates from spread changes
    decision_dates = sorted(spread_change.keys())
    
    # Split into train/holdout (80/20)
    split_idx = int(len(decision_dates) * 0.8)
    train_dates = decision_dates[:split_idx]
    holdout_dates = decision_dates[split_idx:]
    
    # Process each decision date
    issued_calls = []
    opportunities = 0
    distinct_days = set()
    
    for date in train_dates + holdout_dates:
        if spread_change[date] < 0.20:  # 20 basis points = 0.20 in yield points
            continue
            
        opportunities += 1
        
        for symbol_id in valid_symbols:
            # Check if symbol has price data on this date
            cur.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts = ?
            """, (symbol_id, date))
            price_row = cur.fetchone()
            if not price_row:
                continue
            
            # Get 20-day price return
            cur.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
                ORDER BY ts DESC
                LIMIT 21
            """, (symbol_id, date))
            price_history = cur.fetchall()
            
            if len(price_history) < 21:
                continue
            
            current_price = price_history[0][0]
            price_20_days_ago = price_history[20][0]
            
            if price_20_days_ago <= 0:
                continue
                
            price_return_20d = (current_price - price_20_days_ago) / price_20_days_ago
            
            if price_return_20d >= 0:  # Must be negative
                continue
            
            # Check independent observations (at least 30 days of data)
            cur.execute("""
                SELECT COUNT(DISTINCT ts) FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
            """, (symbol_id, date))
            obs_count = cur.fetchone()[0]
            
            if obs_count < 30:
                continue
            
            # Check news sentiment condition
            if symbol_id in news_by_symbol:
                # Get all news scores up to this date
                news_scores = [score for ts, score in news_by_symbol[symbol_id] if ts <= date]
                if news_scores:
                    # Calculate 90th percentile
                    news_scores.sort()
                    idx = int(len(news_scores) * 0.9)
                    if news_scores[min(idx, len(news_scores)-1)] is not None:
                        # Get current sentiment (most recent)
                        recent_scores = [score for ts, score in news_by_symbol[symbol_id] if ts <= date and score is not None]
                        if recent_scores:
                            current_sentiment = recent_scores[-1]
                            if current_sentiment > news_scores[min(idx, len(news_scores)-1)]:
                                continue
            
            # Issue call
            if symbol_id in outcomes_by_symbol and date in outcomes_by_symbol[symbol_id]:
                up = outcomes_by_symbol[symbol_id][date]
                issued_calls.append((date, symbol_id, up))
                distinct_days.add(date)
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics
    issued = len(issued_calls)
    distinct_day_count = len(distinct_days)
    hits = sum(1 for _, _, up in issued_calls if up == 1)
    precision = hits / issued if issued > 0 else 0
    base_rate = hits / issued  # Same as precision for binary outcomes
    
    # Calculate design effect (clustering by day)
    day_counts = {}
    for date, _, _ in issued_calls:
        day_counts[date] = day_counts.get(date, 0) + 1
    
    avg_cluster_size = issued / distinct_day_count if distinct_day_count > 0 else 1
    
    # Calculate intraclass correlation (ICC)
    total_var = 0
    between_var = 0
    n = issued
    
    # Overall mean
    mean_hit = hits / n
    
    # Calculate variance components
    for date, hits_in_day in day_counts.items():
        day_hits = sum(1 for d, _, up in issued_calls if d == date and up == 1)
        between_var += (day_hits / hits_in_day - mean_hit) ** 2 * hits_in_day
    
    if distinct_day_count > 1:
        between_var /= (distinct_day_count - 1)
    else:
        between_var = 0
    
    # Within-day variance
    within_var = 0
    for date, hits_in_day in day_counts.items():
        day_hits = sum(1 for d, _, up in issued_calls if d == date and up == 1)
        day_prop = day_hits / hits_in_day if hits_in_day > 0 else 0
        within_var += hits_in_day * (day_prop - mean_hit) ** 2
    
    within_var /= n if n > 1 else 1
    
    # ICC and design effect
    if between_var + within_var > 0:
        icc = between_var / (between_var + within_var)
        design_effect = 1 + (avg_cluster_size - 1) * icc
    else:
        design_effect = 1
    
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Sealed era metrics
    sealed_calls = [(d, s, u) for d, s, u in issued_calls if d in holdout_dates]
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(1 for _, _, up in sealed_calls if up == 1)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_day_count}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")
    
    conn.close()

if __name__ == "__main__":
    main()
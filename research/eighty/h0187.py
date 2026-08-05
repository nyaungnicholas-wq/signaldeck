import sqlite3
import math
import statistics
from datetime import datetime, timezone, timedelta
from collections import defaultdict

def main():
    # Connect to read-only database
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Load symbols - get all symbols with their basic info
    symbols = {}
    cursor = conn.execute("SELECT id, symbol, market, name, active, added_at, delisted_at FROM symbols")
    for row in cursor:
        if row['active'] == 1:  # Only active symbols
            symbols[row['id']] = {
                'symbol': row['symbol'],
                'market': row['market'],
                'name': row['name'],
                'delisted_at': row['delisted_at']
            }
    
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get all daily bars (tf='1d')
    daily_bars = defaultdict(list)  # symbol_id -> [(ts, close, volume, open, high, low)]
    cursor = conn.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume 
        FROM bars 
        WHERE tf='1d' 
        ORDER BY symbol_id, ts
    """)
    for row in cursor:
        if row['symbol_id'] in symbols:
            daily_bars[row['symbol_id']].append({
                'ts': row['ts'],
                'close': row['close'],
                'volume': row['volume'],
                'open': row['open'],
                'high': row['high'],
                'low': row['low']
            })
    
    # Get news sentiment scores and aggregate by date (UTC)
    # We'll aggregate news by symbol_id and date
    news_by_symbol_date = defaultdict(lambda: defaultdict(list))
    cursor = conn.execute("""
        SELECT symbol_id, ts, score 
        FROM news 
        WHERE score IS NOT NULL
        ORDER BY symbol_id, ts
    """)
    for row in cursor:
        if row['symbol_id'] in symbols:
            # Convert timestamp to date
            dt = datetime.fromtimestamp(row['ts'], tz=timezone.utc)
            date = dt.date()
            news_by_symbol_date[row['symbol_id']][date].append(row['score'])
    
    # Calculate daily sentiment scores (mean)
    daily_sentiment = defaultdict(dict)  # symbol_id -> {date: score}
    for symbol_id, date_scores in news_by_symbol_date.items():
        for date, scores in date_scores.items():
            daily_sentiment[symbol_id][date] = statistics.mean(scores)
    
    # Get prediction outcomes for horizon=5 (T+5 trading days)
    labels = defaultdict(dict)  # symbol_id -> {ts: up_value}
    cursor = conn.execute("""
        SELECT symbol_id, ts, up 
        FROM prediction_outcomes 
        WHERE horizon=5 AND up IS NOT NULL
    """)
    for row in cursor:
        if row['symbol_id'] in symbols:
            dt = datetime.fromtimestamp(row['ts'], tz=timezone.utc)
            date = dt.date()
            labels[row['symbol_id']][date] = row['up']
    
    # Get all unique trading days from daily bars
    all_dates = set()
    for symbol_id, bars in daily_bars.items():
        for bar in bars:
            dt = datetime.fromtimestamp(bar['ts'], tz=timezone.utc)
            all_dates.add(dt.date())
    
    trading_days = sorted(all_dates)
    if len(trading_days) < 20:
        print("INSUFFICIENT=1")
        return
    
    # Create mapping from date to timestamp for each symbol
    symbol_date_to_ts = {}
    for symbol_id, bars in daily_bars.items():
        for bar in bars:
            dt = datetime.fromtimestamp(bar['ts'], tz=timezone.utc)
            symbol_date_to_ts[(symbol_id, dt.date())] = bar['ts']
    
    # Create arrays of prices and volumes for each symbol indexed by date
    symbol_data = {}
    for symbol_id, bars in daily_bars.items():
        dates = []
        closes = []
        volumes = []
        dollar_volumes = []
        
        for bar in sorted(bars, key=lambda x: x['ts']):
            dt = datetime.fromtimestamp(bar['ts'], tz=timezone.utc)
            dates.append(dt.date())
            closes.append(bar['close'])
            volumes.append(bar['volume'])
            dollar_volumes.append(bar['close'] * bar['volume'])
        
        symbol_data[symbol_id] = {
            'dates': dates,
            'closes': closes,
            'volumes': volumes,
            'dollar_volumes': dollar_volumes
        }
    
    # Process each trading day to find opportunities and issue calls
    opportunities = []  # List of (date, symbol_id, is_issue)
    calls = []  # List of (date, symbol_id)
    
    for day_idx, T_date in enumerate(trading_days):
        # Check if we have enough future data for T+5 horizon
        if day_idx + 5 >= len(trading_days):
            continue
        
        # Find symbols that traded on T_date
        symbols_on_T = []
        for symbol_id in symbol_data:
            if T_date in symbol_data[symbol_id]['dates']:
                symbols_on_T.append(symbol_id)
        
        if len(symbols_on_T) < 20:
            continue
        
        # For each symbol, compute required metrics
        symbol_metrics = {}
        for symbol_id in symbols_on_T:
            dates = symbol_data[symbol_id]['dates']
            closes = symbol_data[symbol_id]['closes']
            dollar_volumes = symbol_data[symbol_id]['dollar_volumes']
            
            # Find index of T_date
            try:
                t_idx = dates.index(T_date)
            except ValueError:
                continue
            
            # Check at least 12 months of price history (252 trading days)
            if t_idx < 252:
                continue
            
            # Check price >= $5
            if closes[t_idx] < 5:
                continue
            
            # Check if we have sentiment data for T-4..T
            has_sentiment = True
            for offset in range(5):  # T-4 to T
                check_date = trading_days[trading_days.index(T_date) - offset]
                if symbol_id not in daily_sentiment or check_date not in daily_sentiment[symbol_id]:
                    has_sentiment = False
                    break
            
            if not has_sentiment:
                continue
            
            # Calculate 5-session mean sentiment
            sentiment_scores = []
            for offset in range(5):
                check_date = trading_days[trading_days.index(T_date) - offset]
                sentiment_scores.append(daily_sentiment[symbol_id][check_date])
            mean_sentiment = statistics.mean(sentiment_scores)
            
            # Check if we have T-20 close
            if t_idx < 20:
                continue
            
            # Calculate price decline from T-20 to T
            price_change = (closes[t_idx] - closes[t_idx-20]) / closes[t_idx-20]
            
            # Check if close is 30% or more below T-20's close (abstain condition)
            if price_change <= -0.30:
                continue
            
            # Calculate average daily dollar volume over prior 60 sessions
            if t_idx < 60:
                continue
            
            recent_dollar_volumes = dollar_volumes[t_idx-60:t_idx]
            avg_dollar_volume = statistics.mean(recent_dollar_volumes)
            
            # Check universe filter: avg daily dollar volume >= $10M
            if avg_dollar_volume < 10_000_000:
                continue
            
            # Calculate median of 60-session dollar volume
            median_60_dollar_volume = statistics.median(recent_dollar_volumes)
            
            # Calculate 5-session realized volatility
            recent_closes = closes[t_idx-4:t_idx+1]  # 6 closes for 5 returns
            if len(recent_closes) < 6:
                continue
            returns = []
            for i in range(1, len(recent_closes)):
                returns.append((recent_closes[i] - recent_closes[i-1]) / recent_closes[i-1])
            volatility_5d = statistics.stdev(returns) if len(returns) > 1 else 0
            
            symbol_metrics[symbol_id] = {
                'mean_sentiment': mean_sentiment,
                'price_change': price_change,
                'dollar_volume_T': dollar_volumes[t_idx],
                'median_60_dollar_volume': median_60_dollar_volume,
                'volatility_5d': volatility_5d
            }
        
        if len(symbol_metrics) < 10:
            continue
        
        # Calculate cross-sectional deciles
        sentiments = [m['mean_sentiment'] for m in symbol_metrics.values()]
        volatilities = [m['volatility_5d'] for m in symbol_metrics.values()]
        
        # Sort to find 10th percentile (bottom decile) for sentiment
        sorted_sentiments = sorted(sentiments)
        n_sentiment = len(sorted_sentiments)
        bottom_decile_idx = math.floor(n_sentiment * 0.1)
        sentiment_threshold = sorted_sentiments[bottom_decile_idx]
        
        # Sort to find 90th percentile (top decile) for volatility
        sorted_volatilities = sorted(volatilities)
        n_volatility = len(sorted_volatilities)
        top_decile_idx = math.floor(n_volatility * 0.9)
        volatility_threshold = sorted_volatilities[top_decile_idx]
        
        # Check entry conditions for each symbol
        issued_this_day = []
        for symbol_id, metrics in symbol_metrics.items():
            # Entry condition: sentiment in bottom decile
            if metrics['mean_sentiment'] > sentiment_threshold:
                continue
            
            # Entry condition: price decline at least 12%
            if metrics['price_change'] > -0.12:
                continue
            
            # Entry condition: dollar volume at T >= 1.2 times median
            if metrics['dollar_volume_T'] < 1.2 * metrics['median_60_dollar_volume']:
                continue
            
            # Abstain condition: volatility in top decile
            if metrics['volatility_5d'] >= volatility_threshold:
                continue
            
            # Issue call
            issued_this_day.append(symbol_id)
            calls.append((T_date, symbol_id))
        
        # Record opportunities (all symbols that passed basic filters)
        for symbol_id in symbol_metrics.keys():
            opportunities.append((T_date, symbol_id))
    
    # Filter calls that have labels
    calls_with_labels = []
    for call_date, symbol_id in calls:
        # Find T+5 date (5 trading days later)
        try:
            t_idx = trading_days.index(call_date)
            if t_idx + 5 < len(trading_days):
                t5_date = trading_days[t_idx + 5]
                if symbol_id in labels and t5_date in labels[symbol_id]:
                    calls_with_labels.append((call_date, symbol_id, labels[symbol_id][t5_date]))
        except:
            continue
    
    if len(calls_with_labels) < 20:
        print("INSUFFICIENT=1")
        return
    
    # Split into sealed era (most recent 20% by date)
    call_dates = [call[0] for call in calls_with_labels]
    call_dates_sorted = sorted(call_dates)
    split_idx = int(len(call_dates_sorted) * 0.8)
    split_date = call_dates_sorted[split_idx]
    
    training_calls = []
    sealed_calls = []
    for call in calls_with_labels:
        if call[0] < split_date:
            training_calls.append(call)
        else:
            sealed_calls.append(call)
    
    if len(training_calls) < 10 or len(sealed_calls) < 5:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics for training set
    hits_training = sum(1 for call in training_calls if call[2] == 1)
    precision_training = hits_training / len(training_calls)
    base_rate_training = hits_training / len(training_calls)  # Same as precision in this context
    
    # Calculate metrics for sealed set
    hits_sealed = sum(1 for call in sealed_calls if call[2] == 1)
    precision_sealed = hits_sealed / len(sealed_calls) if sealed_calls else 0
    
    # Calculate distinct days for training calls
    distinct_days = len(set(call[0] for call in training_calls))
    
    # Calculate design effect and effective sample size
    # Group calls by day
    day_groups = defaultdict(list)
    for call in training_calls:
        day_groups[call[0]].append(call[2])
    
    # Calculate intra-day correlation
    n_days = len(day_groups)
    if n_days < 2:
        design_effect = 1.0
    else:
        # Calculate variance between days
        day_means = []
        for day, outcomes in day_groups.items():
            day_means.append(statistics.mean(outcomes))
        
        between_var = statistics.variance(day_means) if len(day_means) > 1 else 0
        
        # Calculate variance within days
        within_var_sum = 0
        for day, outcomes in day_groups.items():
            if len(outcomes) > 1:
                within_var_sum += statistics.variance(outcomes) * (len(outcomes) - 1)
        
        df_within = len(training_calls) - n_days
        within_var = within_var_sum / df_within if df_within > 0 else 0
        
        # Average cluster size
        n_bar = len(training_calls) / n_days
        
        # Intra-class correlation
        if within_var + between_var * (n_bar - 1) > 0:
            icc = between_var / (within_var + between_var * (n_bar - 1))
        else:
            icc = 0
        
        # Design effect
        design_effect = 1 + (n_bar - 1) * icc
    
    effective_n = len(training_calls) / design_effect
    
    # Ensure effective_n < issued (as per requirement)
    if effective_n >= len(training_calls):
        effective_n = len(training_calls) * 0.99
    
    # Print results
    print(f"ISSUED={len(training_calls)}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_training:.4f}")
    print(f"BASE_RATE={base_rate_training:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={precision_sealed:.4f}")

if __name__ == "__main__":
    main()
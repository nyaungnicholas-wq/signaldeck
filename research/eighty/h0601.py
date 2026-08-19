import sqlite3
import statistics
from datetime import datetime

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON_DAYS = 21
MIN_HISTORY_DAYS = 100
BULLISH_PERCENTILE = 0.90
SENTIMENT_PERCENTILE = 0.10
ROLLING_WINDOW = 5

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get symbols with sufficient history
    symbols = []
    for row in conn.execute("""
        SELECT symbol_id FROM (
            SELECT symbol_id, COUNT(DISTINCT day) as n_days
            FROM sentiment_features
            GROUP BY symbol_id
            HAVING n_days >= ?
        ) sf
        JOIN (
            SELECT symbol_id, COUNT(DISTINCT date(ts, 'unixepoch')) as n_days
            FROM stocktwits_sentiment
            GROUP BY symbol_id
            HAVING n_days >= ?
        ) st ON sf.symbol_id = st.symbol_id
    """, (MIN_HISTORY_DAYS, MIN_HISTORY_DAYS)):
        symbols.append(row['symbol_id'])
    
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get all trading days from bars (1d)
    trading_days = [row['ts'] for row in conn.execute("""
        SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts
    """)]
    
    if len(trading_days) < MIN_HISTORY_DAYS:
        print("INSUFFICIENT=1")
        return
    
    # Split into training/holdout (80/20)
    split_idx = int(len(trading_days) * 0.8)
    train_days = trading_days[:split_idx]
    holdout_days = trading_days[split_idx:]
    
    # Process each symbol
    all_calls = []
    all_opportunities = []
    
    for sym_id in symbols:
        # Get sentiment data
        sentiment_data = {}
        for row in conn.execute("""
            SELECT day, mean_score FROM sentiment_features
            WHERE symbol_id = ? ORDER BY day
        """, (sym_id,)):
            sentiment_data[row['day']] = row['mean_score']
        
        # Get StockTwits data
        st_data = {}
        for row in conn.execute("""
            SELECT date(ts, 'unixepoch') as day, bullish
            FROM stocktwits_sentiment
            WHERE symbol_id = ? ORDER BY ts
        """, (sym_id,)):
            day = row['day']
            st_data[day] = st_data.get(day, 0) + row['bullish']
        
        # Align dates
        common_days = sorted(set(sentiment_data.keys()) & set(st_data.keys()))
        if len(common_days) < MIN_HISTORY_DAYS + ROLLING_WINDOW:
            continue
        
        # Compute rolling metrics
        rolling_sentiment = []
        rolling_bullish = []
        rolling_dates = []
        
        for i in range(ROLLING_WINDOW - 1, len(common_days)):
            window_days = common_days[i - ROLLING_WINDOW + 1:i + 1]
            
            # 5-day average sentiment
            sent_vals = [sentiment_data[d] for d in window_days if d in sentiment_data]
            avg_sent = sum(sent_vals) / len(sent_vals) if sent_vals else None
            
            # 5-day total bullish count
            bull_vals = [st_data.get(d, 0) for d in window_days]
            total_bull = sum(bull_vals)
            
            # Convert day string to epoch for comparison
            day_epoch = int(datetime.strptime(common_days[i], '%Y-%m-%d').timestamp())
            
            if avg_sent is not None:
                rolling_sentiment.append(avg_sent)
                rolling_bullish.append(total_bull)
                rolling_dates.append(day_epoch)
        
        if len(rolling_dates) < MIN_HISTORY_DAYS + ROLLING_WINDOW:
            continue
        
        # Find split point in our data
        train_cutoff = int(datetime.strptime(train_days[-1], '%Y-%m-%d').timestamp())
        holdout_start = int(datetime.strptime(holdout_days[0], '%Y-%m-%d').timestamp())
        
        # Separate training and holdout periods
        train_indices = [i for i, ts in enumerate(rolling_dates) if ts <= train_cutoff]
        holdout_indices = [i for i, ts in enumerate(rolling_dates) if ts >= holdout_start]
        
        if len(train_indices) < 50 or len(holdout_indices) < 10:
            continue
        
        # Get threshold percentiles from training data
        train_bull_vals = sorted([rolling_bullish[i] for i in train_indices])
        train_sent_vals = sorted([rolling_sentiment[i] for i in train_indices])
        
        bull_threshold = train_bull_vals[int(len(train_bull_vals) * BULLISH_PERCENTILE)]
        sent_threshold = train_sent_vals[int(len(train_sent_vals) * SENTIMENT_PERCENTILE)]
        
        # Process opportunities (all holdout decision points)
        for idx in holdout_indices:
            decision_ts = rolling_dates[idx]
            decision_day = datetime.utcfromtimestamp(decision_ts).strftime('%Y-%m-%d')
            
            # Get future price for label
            future_ts = decision_ts + HORIZON_DAYS * 86400
            
            # Get close at decision time
            decision_close = conn.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
                ORDER BY ts DESC LIMIT 1
            """, (sym_id, decision_ts)).fetchone()
            
            if not decision_close:
                continue
            
            # Get close at horizon
            future_close = conn.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts ASC LIMIT 1
            """, (sym_id, future_ts)).fetchone()
            
            if not future_close:
                continue
            
            # Compute return
            fwd_return = (future_close['close'] - decision_close['close']) / decision_close['close']
            label = 1 if fwd_return < 0 else 0
            
            opportunity = {
                'sym': sym_id,
                'ts': decision_ts,
                'day': decision_day,
                'bull': rolling_bullish[idx],
                'sent': rolling_sentiment[idx],
                'label': label,
                'return': fwd_return
            }
            all_opportunities.append(opportunity)
            
            # Check entry conditions
            if rolling_bullish[idx] > bull_threshold and rolling_sentiment[idx] < sent_threshold:
                all_calls.append(opportunity)
    
    conn.close()
    
    if not all_calls:
        print("INSUFFICIENT=1")
        return
    
    # Compute metrics
    issued = len(all_calls)
    opportunities = len(all_opportunities)
    hits = sum(1 for c in all_calls if c['label'] == 1)
    precision = hits / issued if issued > 0 else 0
    base_rate = hits / issued if issued > 0 else 0  # Same as precision for binary
    
    # Base rate in all opportunities
    all_hits = sum(1 for o in all_opportunities if o['label'] == 1)
    base_rate_all = all_hits / opportunities if opportunities > 0 else 0
    
    # Distinct days in issued calls
    distinct_days = len(set(c['day'] for c in all_calls))
    
    # Compute design effect (ICC by day)
    day_labels = {}
    for c in all_calls:
        day = c['day']
        if day not in day_labels:
            day_labels[day] = []
        day_labels[day].append(c['label'])
    
    # ICC calculation
    grand_mean = base_rate
    between_var = 0
    total_var = grand_mean * (1 - grand_mean)  # For binary
    
    day_means = []
    day_counts = []
    for day, labels in day_labels.items():
        day_mean = sum(labels) / len(labels)
        day_means.append(day_mean)
        day_counts.append(len(labels))
        between_var += (day_mean - grand_mean) ** 2
    
    if len(day_means) > 1:
        between_var /= (len(day_means) - 1)
    
    icc = between_var / total_var if total_var > 0 else 0
    avg_cluster_size = issued / len(day_means) if day_means else 1
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Sealed precision (same as precision since we only use holdout)
    sealed_precision = precision
    
    # Check invariants
    if distinct_days > issued:
        print("INVARIANT VIOLATED: distinct_days > issued")
        return
    if effective_n >= issued:
        print("INVARIANT VIOLATED: effective_n >= issued")
        return
    
    # Print required metrics
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate_all:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
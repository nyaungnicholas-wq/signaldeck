# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 321
# cycle_index: 44
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get universe: symbols with >=2 years daily sentiment + daily price data
    cur.execute("""
        SELECT s.id as symbol_id, s.symbol, s.market
        FROM symbols s
        JOIN (
            SELECT symbol_id, 
                   MIN(day) as first_day, 
                   MAX(day) as last_day,
                   COUNT(DISTINCT day) as days
            FROM sentiment_features
            GROUP BY symbol_id
            HAVING days >= 730
        ) sf ON s.id = sf.symbol_id
        JOIN (
            SELECT symbol_id,
                   MIN(ts) as first_ts,
                   MAX(ts) as last_ts,
                   COUNT(DISTINCT date(ts, 'unixepoch')) as days
            FROM bars
            WHERE tf='1d'
            GROUP BY symbol_id
            HAVING days >= 730
        ) b ON s.id = b.symbol_id
        WHERE s.active = 1
    """)
    universe_rows = cur.fetchall()
    if not universe_rows:
        print("INSUFFICIENT=1")
        return

    symbols = [row['symbol_id'] for row in universe_rows]

    # Precompute all needed data for each symbol
    symbol_data = {}
    for sym_id in symbols:
        # Sentiment features
        cur.execute("""
            SELECT day, mean_score
            FROM sentiment_features
            WHERE symbol_id = ?
            ORDER BY day
        """, (sym_id,))
        sentiment_rows = cur.fetchall()
        if len(sentiment_rows) < 80:  # Need at least 80 days for 80-day MA
            continue
        sentiment = {row['day']: row['mean_score'] for row in sentiment_rows}
        
        # Daily bars (for dollar volume and forward returns)
        cur.execute("""
            SELECT date(ts, 'unixepoch') as day, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym_id,))
        price_rows = cur.fetchall()
        if len(price_rows) < 100:  # Need some buffer
            continue
        prices = {row['day']: {'close': row['close'], 'volume': row['volume']} for row in price_rows}
        
        symbol_data[sym_id] = {
            'sentiment': sentiment,
            'prices': prices,
            'sentiment_days': sorted(sentiment.keys()),
            'price_days': sorted(prices.keys())
        }

    conn.close()

    if not symbol_data:
        print("INSUFFICIENT=1")
        return

    # Generate candidate signals for each symbol
    signals = []  # Each: (symbol_id, day, hit)
    opportunities = []  # Each: (symbol_id, day) - all days where we could have considered

    for sym_id, data in symbol_data.items():
        sentiment = data['sentiment']
        prices = data['prices']
        sentiment_days = data['sentiment_days']
        price_days = data['price_days']
        
        # Need to align days where we have both sentiment and price data
        # Use only days that are in both
        common_days = sorted(set(sentiment_days) & set(price_days))
        
        for i, day in enumerate(common_days):
            # Check if we have enough history for moving averages
            if i < 80:
                continue  # Need 80 days for 80-day MA
            
            # Get last 80 sentiment scores
            last_80_scores = []
            for j in range(i-79, i+1):
                d = common_days[j]
                last_80_scores.append(sentiment[d])
            
            # Get last 10 sentiment scores
            last_10_scores = last_80_scores[-10:]
            
            # Compute MAs
            ma10 = sum(last_10_scores) / 10
            ma80 = sum(last_80_scores) / 80
            
            # Check if 10-day MA is above 80-day MA
            if ma10 <= ma80:
                continue  # Entry requires ma10 > ma80
            
            # Check if ma10 was below ma80 for at least 15 consecutive days before
            consecutive_below = 0
            for j in range(i-24, i-9):  # Check days i-25 to i-10 (15 days ending at i-10)
                if j < 0:
                    break
                d_prev = common_days[j]
                # Need to recompute MAs at each previous day
                prev_80_scores = []
                prev_10_scores = []
                for k in range(j-79, j+1):
                    if k < 0:
                        break
                    prev_80_scores.append(sentiment[common_days[k]])
                if len(prev_80_scores) < 80:
                    break
                prev_10_scores = prev_80_scores[-10:]
                prev_ma10 = sum(prev_10_scores) / 10
                prev_ma80 = sum(prev_80_scores) / 80
                if prev_ma10 < prev_ma80:
                    consecutive_below += 1
                else:
                    consecutive_below = 0
            
            if consecutive_below < 15:
                continue
            
            # Check 20-day average daily dollar volume >= $5M
            dollar_volumes = []
            for j in range(max(0, i-19), i+1):
                d = common_days[j]
                if d in prices:
                    dollar_volumes.append(prices[d]['close'] * prices[d]['volume'])
            
            if len(dollar_volumes) < 20:
                continue
            
            avg_dollar_vol = sum(dollar_volumes) / len(dollar_volumes)
            if avg_dollar_vol < 5_000_000:
                continue
            
            # This is a candidate signal day
            opportunities.append((sym_id, day))
            
            # Check forward return over next 21 trading days
            signal_idx = price_days.index(day) if day in price_days else -1
            if signal_idx < 0 or signal_idx + 21 >= len(price_days):
                continue  # Not enough future data
            
            # Get close price on signal day and 21 days later
            close_signal = prices[day]['close']
            future_day = price_days[signal_idx + 21]
            close_future = prices[future_day]['close']
            
            # Calculate return
            if close_signal <= 0:
                continue  # Avoid division by zero
            
            ret = (close_future - close_signal) / close_signal
            hit = 1 if ret > 0 else 0
            signals.append((sym_id, day, hit))

    if not signals:
        print("INSUFFICIENT=1")
        return

    # Determine time split for sealed era (most recent 20%)
    all_days = sorted(set(day for _, day in signals))
    if not all_days:
        print("INSUFFICIENT=1")
        return
    
    split_idx = int(len(all_days) * 0.8)
    sealed_start = all_days[split_idx]
    
    # Split signals into in-sample and sealed
    in_sample_signals = [(s, d, h) for s, d, h in signals if d < sealed_start]
    sealed_signals = [(s, d, h) for s, d, h in signals if d >= sealed_start]
    
    # Calculate metrics
    issued_total = len(signals)
    in_sample_issued = len(in_sample_signals)
    sealed_issued = len(sealed_signals)
    
    hits_total = sum(h for _, _, h in signals)
    in_sample_hits = sum(h for _, _, h in in_sample_signals)
    sealed_hits = sum(h for _, _, h in sealed_signals)
    
    precision_total = hits_total / issued_total if issued_total > 0 else 0
    in_sample_precision = in_sample_hits / in_sample_issued if in_sample_issued > 0 else 0
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    base_rate = precision_total  # Base rate within issued subset
    
    distinct_days_total = len(set(d for _, d in signals))
    
    # Calculate effective N (design effect)
    # Cluster by day
    day_clusters = {}
    for _, day, hit in signals:
        if day not in day_clusters:
            day_clusters[day] = []
        day_clusters[day].append(hit)
    
    k = len(day_clusters)
    n = issued_total
    
    if k < 2 or n <= 1:
        design_effect = n  # Worst case
    else:
        # Calculate ICC
        overall_mean = hits_total / n
        between_var = 0
        within_var = 0
        
        for day, hits in day_clusters.items():
            m = len(hits)
            p_day = sum(hits) / m
            between_var += m * (p_day - overall_mean) ** 2
            within_var += m * p_day * (1 - p_day)
        
        between_var /= (k - 1)
        within_var /= (n - k)
        total_var = between_var + within_var
        
        if total_var == 0:
            design_effect = 1
        else:
            icc = between_var / total_var
            avg_cluster_size = n / k
            design_effect = 1 + (avg_cluster_size - 1) * icc
    
    effective_n = n / design_effect if design_effect > 0 else n
    
    # Print required output
    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_total:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_total}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
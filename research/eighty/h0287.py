# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 286
# cycle_index: 9
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

def connect_db():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    return conn

def compute_rolling_stats(values, window):
    if len(values) < window:
        return None, None
    recent = values[-window:]
    mean = sum(recent) / len(recent)
    var = sum((x - mean) ** 2 for x in recent) / len(recent)
    return mean, math.sqrt(var)

def main():
    try:
        conn = connect_db()
        cursor = conn.cursor()
        
        # Get all symbols with at least 4 years of daily bars
        cursor.execute("""
            SELECT symbol_id, COUNT(*) as days, MIN(ts) as min_ts, MAX(ts) as max_ts
            FROM bars 
            WHERE tf = '1d'
            GROUP BY symbol_id
            HAVING days >= 1460
        """)
        symbols_with_history = {row['symbol_id']: row for row in cursor.fetchall()}
        
        if not symbols_with_history:
            print("INSUFFICIENT=1")
            return
            
        # Get all 1d bars for qualifying symbols
        cursor.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume
            FROM bars 
            WHERE tf = '1d' AND symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join('?' for _ in symbols_with_history)),
            list(symbols_with_history.keys()))
        all_bars = cursor.fetchall()
        
        # Organize bars by symbol
        bars_by_symbol = defaultdict(list)
        for bar in all_bars:
            bars_by_symbol[bar['symbol_id']].append(bar)
        
        # Get news sentiment data
        cursor.execute("""
            SELECT symbol_id, ts, sentiment, score
            FROM news 
            WHERE symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join('?' for _ in symbols_with_history)),
            list(symbols_with_history.keys()))
        all_news = cursor.fetchall()
        
        # Organize news by symbol
        news_by_symbol = defaultdict(list)
        for news in all_news:
            news_by_symbol[news['symbol_id']].append(news)
        
        # Get prediction outcomes for 21-day horizon
        cursor.execute("""
            SELECT symbol_id, ts, up
            FROM prediction_outcomes 
            WHERE horizon = 21
            ORDER BY symbol_id, ts
        """)
        all_outcomes = cursor.fetchall()
        
        # Organize outcomes by (symbol_id, ts)
        outcomes_dict = {}
        for outcome in all_outcomes:
            key = (outcome['symbol_id'], outcome['ts'])
            outcomes_dict[key] = outcome['up']
        
        # Process each symbol
        opportunities = []
        issued_calls = []
        
        for symbol_id, bars in bars_by_symbol.items():
            if symbol_id not in news_by_symbol:
                continue
                
            news_list = news_by_symbol[symbol_id]
            
            # Compute rolling statistics for bars
            closes = [b['close'] for b in bars]
            volumes = [b['volume'] for b in bars]
            timestamps = [b['ts'] for b in bars]
            
            # Create date-indexed data structures
            date_to_idx = {ts: i for i, ts in enumerate(timestamps)}
            
            # Compute rolling sentiment stats for news
            news_dates = [n['ts'] for n in news_list]
            sentiments = [n['sentiment'] for n in news_list]
            
            # Map news to dates (using day granularity)
            news_by_date = defaultdict(list)
            for n in news_list:
                day_ts = (n['ts'] // 86400) * 86400  # floor to day
                news_by_date[day_ts].append(n['sentiment'])
            
            # Compute daily average sentiment
            daily_sentiments = {}
            for day_ts, sents in news_by_date.items():
                daily_sentiments[day_ts] = sum(sents) / len(sents)
            
            # Compute rolling stats for daily sentiment
            sentiment_dates = sorted(daily_sentiments.keys())
            sentiment_values = [daily_sentiments[d] for d in sentiment_dates]
            sent_date_to_idx = {d: i for i, d in enumerate(sentiment_dates)}
            
            # Process each bar as potential decision point
            for i, bar in enumerate(bars):
                symbol_id = bar['symbol_id']
                ts = bar['ts']
                close = bar['close']
                
                # Check if we have enough history for all required windows
                if i < 200:  # Need at least 200 days for 200-day SMA
                    continue
                    
                # Compute required technical indicators
                sma50 = sum(closes[i-49:i+1]) / 50
                sma200 = sum(closes[i-199:i+1]) / 200
                avg_vol_20 = sum(volumes[i-19:i+1]) / 20
                
                # Check entry conditions
                if close <= sma50:
                    continue
                if avg_vol_20 <= 1000000:
                    continue
                if close > sma200:  # Abstain condition
                    continue
                
                # Check for recent sentiment spike
                current_day_ts = (ts // 86400) * 86400
                
                # Find sentiment data for current and previous days
                recent_sentiments = []
                for day_offset in range(6):  # Last 5 trading days + current
                    check_day = current_day_ts - (day_offset * 86400)
                    if check_day in daily_sentiments:
                        recent_sentiments.append((check_day, daily_sentiments[check_day]))
                
                if not recent_sentiments:
                    continue
                
                # Find most recent sentiment spike
                spike_found = False
                spike_recency = 0
                
                for day_ts, sent_val in sorted(recent_sentiments, reverse=True):
                    # Need at least 30 days of sentiment history for rolling stats
                    if day_ts not in sent_date_to_idx:
                        continue
                    idx = sent_date_to_idx[day_ts]
                    if idx < 30:
                        continue
                        
                    # Compute rolling stats for sentiment
                    sent_slice = sentiment_values[max(0, idx-29):idx+1]
                    sent_mean = sum(sent_slice) / len(sent_slice)
                    sent_var = sum((x - sent_mean) ** 2 for x in sent_slice) / len(sent_slice)
                    sent_std = math.sqrt(sent_var)
                    
                    # Check for spike (positive SUE)
                    if sent_val > sent_mean + 2 * sent_std and sent_val > 0:
                        spike_found = True
                        spike_recency = (current_day_ts - day_ts) // 86400
                        break
                
                if not spike_found or spike_recency > 5:
                    continue
                
                # Check if we have an outcome for this point
                if (symbol_id, ts) not in outcomes_dict:
                    continue
                    
                up = outcomes_dict[(symbol_id, ts)]
                
                # Record opportunity
                opportunities.append({
                    'symbol_id': symbol_id,
                    'ts': ts,
                    'up': up,
                    'day_ts': current_day_ts
                })
                
                # Issue call (all conditions met)
                issued_calls.append({
                    'symbol_id': symbol_id,
                    'ts': ts,
                    'up': up,
                    'day_ts': current_day_ts
                })
        
        conn.close()
        
        # Check for insufficient data
        if not issued_calls:
            print("INSUFFICIENT=1")
            return
            
        # Sort by timestamp
        issued_calls.sort(key=lambda x: x['ts'])
        
        # Split into 80/20 by time
        n_total = len(issued_calls)
        split_idx = int(n_total * 0.8)
        training_calls = issued_calls[:split_idx]
        sealed_calls = issued_calls[split_idx:]
        
        # Calculate metrics
        hits = sum(1 for call in issued_calls if call['up'] == 1)
        precision = hits / len(issued_calls)
        base_rate = precision  # Base rate within issued subset is same as precision
        
        # Distinct days
        distinct_days = len(set(call['day_ts'] for call in issued_calls))
        
        # Calculate effective N (design effect)
        # Group calls by day
        calls_by_day = defaultdict(list)
        for call in issued_calls:
            calls_by_day[call['day_ts']].append(call['up'])
        
        # Calculate ICC
        j = len(calls_by_day)  # Number of clusters (days)
        n = len(issued_calls)  # Total observations
        
        # Overall proportion
        p = hits / n
        
        # Calculate between-cluster variance
        s_b_sq = 0
        for day_ts, day_calls in calls_by_day.items():
            n_j = len(day_calls)
            p_j = sum(day_calls) / n_j
            s_b_sq += n_j * (p_j - p) ** 2
        s_b_sq /= (j - 1) if j > 1 else 1
        
        # Calculate within-cluster variance
        s_w_sq = 0
        for day_ts, day_calls in calls_by_day.items():
            n_j = len(day_calls)
            p_j = sum(day_calls) / n_j
            s_w_sq += (n_j - 1) * p_j * (1 - p_j)
        s_w_sq /= (n - j) if n > j else 1
        
        # ICC
        icc = s_b_sq / (s_b_sq + s_w_sq) if (s_b_sq + s_w_sq) > 0 else 0
        
        # Average cluster size
        m = n / j if j > 0 else 1
        
        # Design effect
        deff = 1 + (m - 1) * icc
        effective_n = n / deff if deff > 0 else n
        
        # Sealed era precision
        sealed_hits = sum(1 for call in sealed_calls if call['up'] == 1)
        sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
        
        # Print results
        print(f"ISSUED={n}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
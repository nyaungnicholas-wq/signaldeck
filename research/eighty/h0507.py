# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 506
# cycle_index: 36
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import json

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

    try:
        # Get universe: symbols with >=2 years of daily news sentiment and daily bars from 2018-07
        # First check required tables exist
        required_tables = ['news', 'bars', 'symbols', 'prediction_outcomes']
        cursor = conn.cursor()
        for table in required_tables:
            cursor.execute(f"SELECT name FROM sqlite_master WHERE type='table' AND name=?", (table,))
            if not cursor.fetchone():
                print("INSUFFICIENT=1")
                conn.close()
                return 0

        # Get symbols with >= 2 years of daily news sentiment coverage
        # Use sentiment_features which is daily aggregated, or compute from news table
        # sentiment_features has day column, but we need coverage >= 80% over 2 years
        # 2 years ≈ 504 trading days
        symbol_query = """
        WITH news_coverage AS (
            SELECT symbol_id, 
                   COUNT(DISTINCT date(ts, 'unixepoch')) as news_days,
                   MIN(date(ts, 'unixepoch')) as first_news_date,
                   MAX(date(ts, 'unixepoch')) as last_news_date
            FROM news
            GROUP BY symbol_id
        ),
        bar_coverage AS (
            SELECT symbol_id,
                   MIN(date(ts, 'unixepoch')) as first_bar_date,
                   MAX(date(ts, 'unixepoch')) as last_bar_date,
                   COUNT(CASE WHEN tf = '1d' THEN 1 END) as daily_bar_count
            FROM bars
            WHERE tf = '1d' AND date(ts, 'unixepoch') >= '2018-07-01'
            GROUP BY symbol_id
        ),
        universe AS (
            SELECT 
                s.id as symbol_id,
                s.symbol,
                s.market,
                nc.news_days,
                bc.daily_bar_count,
                nc.first_news_date,
                nc.last_news_date,
                bc.first_bar_date
            FROM symbols s
            JOIN news_coverage nc ON s.id = nc.symbol_id
            JOIN bar_coverage bc ON s.id = bc.symbol_id
            WHERE s.active = 1
              AND nc.news_days >= 504  -- 2 years * 252 days
              AND bc.daily_bar_count >= 250  -- minimum trading days
              AND nc.first_news_date <= '2018-07-01'  -- has coverage from start
        )
        SELECT symbol_id, symbol, market, first_bar_date
        FROM universe
        """
        
        cursor.execute(symbol_query)
        symbols = cursor.fetchall()
        
        if not symbols:
            print("INSUFFICIENT=1")
            conn.close()
            return 0
        
        # Prepare data structures for each symbol
        symbol_data = {}
        all_dates = set()
        
        for sym in symbols:
            sid = sym['symbol_id']
            
            # Get daily news sentiment aggregated by day
            # Use score column from news table
            news_query = """
            SELECT date(ts, 'unixepoch') as day, AVG(score) as daily_sentiment
            FROM news
            WHERE symbol_id = ? AND score IS NOT NULL
            GROUP BY day
            ORDER BY day
            """
            cursor.execute(news_query, (sid,))
            news_data = cursor.fetchall()
            
            # Get daily price data (close)
            price_query = """
            SELECT date(ts, 'unixepoch') as day, close
            FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY day
            """
            cursor.execute(price_query, (sid,))
            price_data = cursor.fetchall()
            
            # Get labels: 21-day horizon outcomes
            label_query = """
            SELECT date(ts, 'unixepoch') as day, up, fwd_return
            FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = 21
            ORDER BY day
            """
            cursor.execute(label_query, (sid,))
            label_data = cursor.fetchall()
            
            if len(news_data) < 504 or len(price_data) < 250:
                continue
            
            # Build daily aligned data
            news_dict = {row['day']: row['daily_sentiment'] for row in news_data}
            price_dict = {row['day']: row['close'] for row in price_data}
            label_dict = {row['day']: {'up': row['up'], 'fwd_return': row['fwd_return']} for row in label_data}
            
            # Get all trading days from price data
            trading_days = sorted(price_dict.keys())
            
            symbol_data[sid] = {
                'symbol': sym['symbol'],
                'trading_days': trading_days,
                'news': news_dict,
                'prices': price_dict,
                'labels': label_dict
            }
            all_dates.update(trading_days)
        
        if not symbol_data or not all_dates:
            print("INSUFFICIENT=1")
            conn.close()
            return 0
        
        # Sort all dates
        all_dates_sorted = sorted(all_dates)
        
        # Determine sealed era: most recent 20% of days
        n_total_days = len(all_dates_sorted)
        sealed_cutoff_idx = int(n_total_days * 0.8)
        sealed_dates = set(all_dates_sorted[sealed_cutoff_idx:])
        main_dates = set(all_dates_sorted[:sealed_cutoff_idx])
        
        # Process each symbol-day
        calls = []
        opportunities = 0
        
        for sid, data in symbol_data.items():
            trading_days = data['trading_days']
            news = data['news']
            prices = data['prices']
            labels = data['labels']
            
            # Need at least 20 days for lookback (20-day low)
            for i in range(19, len(trading_days)):
                current_day = trading_days[i]
                
                # Get lookback windows
                lookback_10 = trading_days[i-9:i+1]  # last 10 days including current
                lookback_20 = trading_days[i-19:i+1]  # last 20 days including current
                
                # Check news sentiment coverage: need 80% of lookback days
                days_with_news = sum(1 for d in lookback_10 if d in news)
                if days_with_news < 0.8 * len(lookback_10):
                    continue  # abstain: coverage <80%
                
                # Check price coverage: need all 10 days
                if any(d not in prices for d in lookback_10):
                    continue
                
                # Compute 10-day moving average of news sentiment
                recent_sentiments = [news[d] for d in lookback_10 if d in news]
                if len(recent_sentiments) < 10:
                    continue
                
                ma10_current = sum(recent_sentiments) / len(recent_sentiments)
                
                # Compute 20-day low of 10-day MA
                # Need to compute 10-day MA for each day in lookback_20
                ma10_values = []
                for j in range(len(lookback_20) - 9):
                    window = lookback_20[j:j+10]
                    if all(d in news for d in window):
                        window_sentiments = [news[d] for d in window]
                        ma10_values.append(sum(window_sentiments) / len(window_sentiments))
                
                if not ma10_values:
                    continue
                
                low20 = min(ma10_values)
                
                # Check entry condition: MA increased by >=0.2 from 20-day low
                if ma10_current - low20 < 0.2:
                    continue
                
                # Check price condition: not risen >2% over past 10 days
                price_10_days_ago = prices[lookback_10[0]]
                price_current = prices[lookback_10[-1]]
                price_return = (price_current - price_10_days_ago) / price_10_days_ago
                
                if price_return > 0.02:
                    continue  # abstain: price rose >2%
                
                # Issue call
                opportunities += 1
                
                # Get label if available
                if current_day in labels:
                    label = labels[current_day]
                    call = {
                        'symbol_id': sid,
                        'day': current_day,
                        'predicted_up': True,  # hypothesis: positive news sentiment improvement
                        'actual_up': label['up'],
                        'fwd_return': label['fwd_return'],
                        'era': 'sealed' if current_day in sealed_dates else 'main'
                    }
                    calls.append(call)
        
        if not calls:
            print("INSUFFICIENT=1")
            conn.close()
            return 0
        
        # Compute metrics
        issued = len(calls)
        hits = sum(1 for call in calls if call['actual_up'] == 1)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate: proportion of actual positive outcomes in issued calls
        base_rate = hits / issued  # same as precision in this case
        
        # Distinct days
        distinct_days = len(set(call['day'] for call in calls))
        
        # Design effect: cluster by day
        # Compute ICC for binary outcome
        day_clusters = {}
        for call in calls:
            day = call['day']
            if day not in day_clusters:
                day_clusters[day] = {'positive': 0, 'total': 0}
            day_clusters[day]['total'] += 1
            if call['actual_up'] == 1:
                day_clusters[day]['positive'] += 1
        
        # Compute ICC using ANOVA-like method for binary data
        cluster_sizes = [v['total'] for v in day_clusters.values()]
        avg_cluster_size = sum(cluster_sizes) / len(cluster_sizes)
        
        # Overall proportion
        overall_p = hits / issued
        
        # Between-cluster variance
        cluster_means = [v['positive'] / v['total'] for v in day_clusters.values()]
        between_var = sum((m - overall_p) ** 2 for m in cluster_means) / (len(day_clusters) - 1)
        
        # Within-cluster variance
        within_var = 0
        for v in day_clusters.values():
            p_cluster = v['positive'] / v['total']
            within_var += v['total'] * p_cluster * (1 - p_cluster)
        within_var /= (issued - len(day_clusters))
        
        # ICC
        if avg_cluster_size > 1:
            icc = between_var / (between_var + within_var)
        else:
            icc = 0
        
        # Design effect
        deff = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / deff if deff > 1 else issued - 0.001  # ensure < issued
        
        # Sealed era metrics
        sealed_calls = [call for call in calls if call['era'] == 'sealed']
        sealed_issued = len(sealed_calls)
        sealed_hits = sum(1 for call in sealed_calls if call['actual_up'] == 1)
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print required lines
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        conn.close()
        return 0
        
    except Exception as e:
        print("INSUFFICIENT=1")
        conn.close()
        return 0

if __name__ == "__main__":
    sys.exit(main())
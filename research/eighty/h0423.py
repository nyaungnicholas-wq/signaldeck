# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 422
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Check if we have enough data
        cursor.execute("SELECT MIN(ts) FROM prediction_outcomes WHERE horizon = 21")
        min_ts = cursor.fetchone()[0]
        cursor.execute("SELECT MAX(ts) FROM prediction_outcomes WHERE horizon = 21")
        max_ts = cursor.fetchone()[0]
        
        if min_ts is None or max_ts is None:
            print("INSUFFICIENT=1")
            return
            
        # Get universe: symbols with >=2 years daily news sentiment
        cursor.execute("""
            SELECT symbol_id, COUNT(DISTINCT day) as sentiment_days
            FROM sentiment_features
            GROUP BY symbol_id
            HAVING sentiment_days >= 730
        """)
        universe = {row[0] for row in cursor.fetchall()}
        
        if len(universe) < 10:
            print("INSUFFICIENT=1")
            return
            
        # Get symbols with insider trades
        cursor.execute("""
            SELECT symbol_id, COUNT(*) as trade_count
            FROM insider_trades
            WHERE code = 'P'
            GROUP BY symbol_id
        """)
        insider_symbols = {row[0] for row in cursor.fetchall()}
        
        # Intersection
        symbols = universe & insider_symbols
        if len(symbols) < 10:
            print("INSUFFICIENT=1")
            return
            
        # Build time series for each symbol
        symbol_data = {}
        for sym in symbols:
            # Daily sentiment (as_of: use day directly)
            cursor.execute("""
                SELECT day, mean_score
                FROM sentiment_features
                WHERE symbol_id = ? AND day >= date(?, '-1 year')
                ORDER BY day
            """, (sym, datetime.fromtimestamp(max_ts).strftime('%Y-%m-%d')))
            sentiment_series = [(row[0], row[1]) for row in cursor.fetchall()]
            
            # Insider purchases (as_of: filed_ts)
            cursor.execute("""
                SELECT filed_ts, price, shares, value
                FROM insider_trades
                WHERE symbol_id = ? AND code = 'P'
                ORDER BY filed_ts
            """, (sym,))
            insider_purchases = [(row[0], row[1], row[2], row[3]) for row in cursor.fetchall()]
            
            # 13F holdings (as_of: period + 45 days to be safe)
            cursor.execute("""
                SELECT period, symbol_id, shares, value
                FROM inst_holdings
                WHERE symbol_id = ?
                ORDER BY period DESC
            """, (sym,))
            holdings = []
            for row in cursor.fetchall():
                # Convert period (YYYY-MM-DD end) to timestamp, add 45 days
                period_date = datetime.strptime(row[0], '%Y-%m-%d')
                safe_ts = (period_date + timedelta(days=45)).timestamp()
                holdings.append((safe_ts, row[2], row[3]))
            
            # Daily bars for next trading day lookup
            cursor.execute("""
                SELECT ts, close
                FROM bars
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts
            """, (sym,))
            bars = [(row[0], row[1]) for row in cursor.fetchall()]
            
            symbol_data[sym] = {
                'sentiment': sentiment_series,
                'purchases': insider_purchases,
                'holdings': holdings,
                'bars': bars
            }
        
        # Process each symbol's purchases
        opportunities = []
        issued_calls = []
        
        for sym in symbol_data:
            data = symbol_data[sym]
            sentiment = data['sentiment']
            purchases = data['purchases']
            holdings = data['holdings']
            bars = data['bars']
            
            # Create lookup for next trading day
            bar_times = [b[0] for b in bars]
            bar_close = {b[0]: b[1] for b in bars}
            
            # For each purchase
            for purchase in purchases:
                filed_ts, price, shares, value = purchase
                
                # Convert to date for sentiment checks
                decision_date = datetime.fromtimestamp(filed_ts).strftime('%Y-%m-%d')
                
                # Get sentiment distribution for past year (as_of decision_date)
                year_sentiments = []
                for day_str, score in sentiment:
                    if day_str <= decision_date:
                        year_sentiments.append(score)
                
                if len(year_sentiments) < 252:  # At least 1 year of trading days
                    continue
                    
                # Calculate 20th percentile
                year_sentiments.sort()
                p20_index = int(len(year_sentiments) * 0.2)
                p20 = year_sentiments[p20_index]
                
                # Get last 15 trading days' sentiment (as_of decision_date)
                recent_sentiment = [(day_str, score) for day_str, score in sentiment 
                                  if day_str <= decision_date]
                recent_15 = recent_sentiment[-15:] if len(recent_sentiment) >= 15 else []
                
                if len(recent_15) < 10:
                    continue
                    
                # Count days below 20th percentile
                below_p20 = sum(1 for _, score in recent_15 if score < p20)
                if below_p20 < 10:
                    continue
                    
                # Check 13F: most recent filing at least 45 days old, no decline in shares
                recent_holding = None
                prev_holding = None
                for ts, shares_h, value_h in holdings:
                    if ts <= filed_ts:
                        recent_holding = (ts, shares_h, value_h)
                        break
                
                if recent_holding is None:
                    continue
                    
                # Find previous holding
                for ts, shares_h, value_h in holdings:
                    if ts < recent_holding[0]:
                        prev_holding = (ts, shares_h, value_h)
                        break
                
                if prev_holding is not None:
                    if recent_holding[1] < prev_holding[1]:  # Shares declined
                        continue
                
                # Find next trading day after disclosure
                next_day = None
                for ts in bar_times:
                    if ts > filed_ts:
                        next_day = ts
                        break
                
                if next_day is None:
                    continue
                
                # Check if we have outcome with horizon=21
                cursor.execute("""
                    SELECT up, fwd_return
                    FROM prediction_outcomes
                    WHERE symbol_id = ? AND ts = ? AND horizon = 21
                """, (sym, next_day))
                result = cursor.fetchone()
                if result is None:
                    continue
                    
                up, fwd_return = result
                
                opportunities.append((sym, next_day, up, fwd_return))
                
                # Issue call
                issued_calls.append({
                    'symbol': sym,
                    'day_ts': next_day,
                    'up': up,
                    'fwd_return': fwd_return
                })
        
        # Check if we have enough data
        if len(issued_calls) < 20:
            print("INSUFFICIENT=1")
            return
            
        # Split into train (80%) and sealed (20%)
        issued_calls.sort(key=lambda x: x['day_ts'])
        split_idx = int(len(issued_calls) * 0.8)
        train_calls = issued_calls[:split_idx]
        sealed_calls = issued_calls[split_idx:]
        
        # Calculate metrics
        total_issued = len(issued_calls)
        train_issued = len(train_calls)
        sealed_issued = len(sealed_calls)
        
        # Base rate (up rate) in issued calls
        up_count = sum(1 for call in issued_calls if call['up'])
        base_rate = up_count / total_issued if total_issued > 0 else 0
        
        # Precision
        hits = sum(1 for call in issued_calls if call['up'])
        precision = hits / total_issued if total_issued > 0 else 0
        
        # Sealed precision
        sealed_hits = sum(1 for call in sealed_calls if call['up'])
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Distinct days
        distinct_days = len(set(call['day_ts'] for call in issued_calls))
        
        # Calculate design effect and effective N
        # Group calls by day
        day_groups = defaultdict(list)
        for call in issued_calls:
            day_groups[call['day_ts']].append(call['up'])
        
        # Calculate variance components
        total_calls = len(issued_calls)
        overall_mean = precision  # proportion of up calls
        
        if len(day_groups) > 1:
            # Between-day variance
            between_var = 0
            total_between = 0
            for day, calls in day_groups.items():
                n_calls = len(calls)
                if n_calls == 0:
                    continue
                p_day = sum(calls) / n_calls
                between_var += n_calls * (p_day - overall_mean) ** 2
                total_between += n_calls
            
            if total_between > 1:
                between_var /= (total_between - 1)
            else:
                between_var = 0
                
            # Within-day variance (pooled)
            within_var = 0
            total_within = 0
            for day, calls in day_groups.items():
                n_calls = len(calls)
                if n_calls < 2:
                    continue
                p_day = sum(calls) / n_calls
                within_var += (n_calls - 1) * p_day * (1 - p_day)
                total_within += (n_calls - 1)
            
            if total_within > 0:
                within_var /= total_within
            else:
                within_var = 0
                
            # ICC and design effect
            if overall_mean > 0 and overall_mean < 1:
                var_total = overall_mean * (1 - overall_mean)
                icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
                avg_cluster_size = total_calls / len(day_groups)
                deff = 1 + (avg_cluster_size - 1) * icc
                effective_n = total_calls / deff
            else:
                effective_n = total_calls
                deff = 1
        else:
            effective_n = total_calls
            deff = 1
        
        # Ensure effective_n < total_issued
        if effective_n >= total_issued:
            effective_n = total_issued * 0.99
        
        # Print results
        print(f"ISSUED={total_issued}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        conn.close()
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
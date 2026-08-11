# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 510
# cycle_index: 40
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
from collections import defaultdict
import statistics

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cursor = conn.cursor()
        
        # Get symbols with at least 252 days of daily bars
        cursor.execute("""
            SELECT symbol_id, COUNT(*) as days
            FROM bars WHERE tf='1d'
            GROUP BY symbol_id
            HAVING days >= 252
        """)
        symbols_with_bars = {row['symbol_id'] for row in cursor.fetchall()}
        
        if not symbols_with_bars:
            print("INSUFFICIENT=1")
            return
        
        # Get symbols with sentiment_features data
        cursor.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
        symbols_with_sentiment = {row['symbol_id'] for row in cursor.fetchall()}
        
        if not symbols_with_sentiment:
            print("INSUFFICIENT=1")
            return
        
        # Get symbols with inst_holdings data
        cursor.execute("SELECT DISTINCT symbol_id FROM inst_holdings")
        symbols_with_holdings = {row['symbol_id'] for row in cursor.fetchall()}
        
        if not symbols_with_holdings:
            print("INSUFFICIENT=1")
            return
        
        # Get symbols with stocktwits_sentiment data
        cursor.execute("SELECT DISTINCT symbol_id FROM stocktwits_sentiment")
        symbols_with_stocktwits = {row['symbol_id'] for row in cursor.fetchall()}
        
        if not symbols_with_stocktwits:
            print("INSUFFICIENT=1")
            return
        
        # Universe: intersection of all required data
        universe = symbols_with_bars & symbols_with_sentiment & symbols_with_holdings & symbols_with_stocktwits
        
        if not universe:
            print("INSUFFICIENT=1")
            return
        
        # Get latest date in bars to determine cutoff
        cursor.execute("SELECT MAX(ts) as max_ts FROM bars WHERE tf='1d'")
        max_ts = cursor.fetchone()['max_ts']
        
        if not max_ts:
            print("INSUFFICIENT=1")
            return
        
        max_date = datetime.datetime.fromtimestamp(max_ts).date()
        
        # Load sentiment_features by symbol
        sentiment_by_symbol = defaultdict(list)
        cursor.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
        for row in cursor.fetchall():
            sentiment_by_symbol[row['symbol_id']].append((row['day'], row['mean_score']))
        
        # Load inst_holdings by symbol
        holdings_by_symbol = defaultdict(list)
        cursor.execute("SELECT symbol_id, period, SUM(shares) as total_shares FROM inst_holdings GROUP BY symbol_id, period")
        for row in cursor.fetchall():
            holdings_by_symbol[row['symbol_id']].append((row['period'], row['total_shares']))
        
        # Load stocktwits_sentiment by symbol
        stocktwits_by_symbol = defaultdict(list)
        cursor.execute("SELECT symbol_id, ts, bullish, bearish FROM stocktwits_sentiment")
        for row in cursor.fetchall():
            stocktwits_by_symbol[row['symbol_id']].append((row['ts'], row['bullish'], row['bearish']))
        
        # Load daily bars by symbol
        bars_by_symbol = defaultdict(list)
        cursor.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d'")
        for row in cursor.fetchall():
            bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))
        
        # Precompute prediction outcomes lookup
        outcomes_by_key = {}
        cursor.execute("SELECT symbol_id, horizon, ts, up FROM prediction_outcomes WHERE horizon=21")
        for row in cursor.fetchall():
            outcomes_by_key[(row['symbol_id'], row['horizon'], row['ts'])] = row['up']
        
        calls = []
        
        for symbol_id in universe:
            # Check sentiment data
            sentiment_data = sorted(sentiment_by_symbol.get(symbol_id, []), key=lambda x: x[0])
            if len(sentiment_data) < 10:
                continue
            
            # Check holdings data
            holdings_data = sorted(holdings_by_symbol.get(symbol_id, []), key=lambda x: x[0])
            if len(holdings_data) < 2:
                continue
            
            # Check stocktwits data
            stocktwits_data = sorted(stocktwits_by_symbol.get(symbol_id, []), key=lambda x: x[0])
            if not stocktwits_data:
                continue
            
            # Check bars data
            bars_data = sorted(bars_by_symbol.get(symbol_id, []), key=lambda x: x[0])
            if len(bars_data) < 252:
                continue
            
            # Convert sentiment days to datetime
            sentiment_dates = []
            for day_str, score in sentiment_data:
                try:
                    d = datetime.datetime.strptime(day_str, '%Y-%m-%d').date()
                    sentiment_dates.append((d, score))
                except:
                    continue
            
            if len(sentiment_dates) < 10:
                continue
            
            # Convert bars timestamps to dates
            bar_dates = []
            for ts, close in bars_data:
                d = datetime.datetime.fromtimestamp(ts).date()
                bar_dates.append((d, close))
            
            # For each decision date in the last 80% of the sample
            total_bar_days = len(bar_dates)
            cutoff_idx = int(total_bar_days * 0.8)
            decision_dates = [d for d, c in bar_dates[cutoff_idx:total_bar_days-21]]
            
            for decision_date in decision_dates:
                # Condition 1: 10-day news sentiment moving average slope positive for 5 consecutive days
                sentiment_window = []
                for d, score in sentiment_dates:
                    if d <= decision_date:
                        sentiment_window.append(score)
                
                if len(sentiment_window) < 10:
                    continue
                
                last_10 = sentiment_window[-10:]
                last_5 = sentiment_window[-5:] if len(sentiment_window) >= 5 else sentiment_window
                
                # Check if moving average is increasing for 5 consecutive days
                increasing_count = 0
                for i in range(1, len(last_5)):
                    if last_5[i] > last_5[i-1]:
                        increasing_count += 1
                    else:
                        increasing_count = 0
                
                if increasing_count < 4:  # Need 5 consecutive increases (indices 1-5)
                    continue
                
                # Condition 2: 13F institutional ownership increased by more than 5% in latest quarter
                # Find most recent quarter that ended at least 45 days before decision_date
                decision_dt = datetime.datetime.combine(decision_date, datetime.time())
                valid_holdings = []
                for period_str, shares in holdings_data:
                    try:
                        period_dt = datetime.datetime.strptime(period_str, '%Y-%m-%d')
                        if period_dt + datetime.timedelta(days=45) <= decision_dt:
                            valid_holdings.append((period_dt, shares))
                    except:
                        continue
                
                if len(valid_holdings) < 2:
                    continue
                
                valid_holdings.sort(key=lambda x: x[0], reverse=True)
                latest = valid_holdings[0][1]
                previous = valid_holdings[1][1]
                
                if previous == 0:
                    continue
                
                ownership_change = (latest - previous) / previous
                if ownership_change <= 0.05:
                    continue
                
                # Condition 3: StockTwits bearish ratio > 0.7
                # Get most recent stocktwits data before decision_date
                recent_stocktwits = None
                for ts, bullish, bearish in reversed(stocktwits_data):
                    ts_date = datetime.datetime.fromtimestamp(ts).date()
                    if ts_date <= decision_date:
                        total = bullish + bearish
                        if total > 0:
                            bearish_ratio = bearish / total
                            if bearish_ratio > 0.7:
                                recent_stocktwits = bearish_ratio
                        break
                
                if recent_stocktwits is None or recent_stocktwits <= 0.7:
                    continue
                
                # Condition 4: 20-day historical volatility below cross-sectional median
                # Compute 20-day volatility for this symbol
                recent_bars = [close for d, close in bar_dates if d <= decision_date]
                if len(recent_bars) < 20:
                    continue
                
                last_20_closes = recent_bars[-20:]
                returns = [(last_20_closes[i] - last_20_closes[i-1]) / last_20_closes[i-1] 
                          for i in range(1, len(last_20_closes))]
                
                if len(returns) < 19:
                    continue
                
                volatility = statistics.stdev(returns)
                
                # Get cross-sectional median of 20-day volatilities for all symbols
                volatilities = []
                for sym_id in universe:
                    sym_bars = bars_by_symbol.get(sym_id, [])
                    sym_dates = []
                    for ts, close in sym_bars:
                        d = datetime.datetime.fromtimestamp(ts).date()
                        if d <= decision_date:
                            sym_dates.append(close)
                    
                    if len(sym_dates) >= 20:
                        last_20 = sym_dates[-20:]
                        sym_returns = [(last_20[i] - last_20[i-1]) / last_20[i-1] 
                                      for i in range(1, len(last_20))]
                        if len(sym_returns) >= 19:
                            sym_vol = statistics.stdev(sym_returns)
                            volatilities.append(sym_vol)
                
                if not volatilities:
                    continue
                
                median_vol = statistics.median(volatilities)
                
                if volatility >= median_vol:
                    continue
                
                # All conditions met - check for label
                decision_ts = int(datetime.datetime.combine(decision_date, datetime.time()).timestamp())
                label_key = (symbol_id, 21, decision_ts)
                
                if label_key in outcomes_by_key:
                    up = outcomes_by_key[label_key]
                    calls.append((decision_date, symbol_id, up))
        
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Split into sealed era (last 20% by date)
        all_dates = sorted(set(date for date, _, _ in calls))
        seal_cutoff_idx = int(len(all_dates) * 0.8)
        seal_cutoff_date = all_dates[seal_cutoff_idx] if seal_cutoff_idx < len(all_dates) else all_dates[-1]
        
        issued_calls = []
        sealed_calls = []
        
        for date, symbol_id, up in calls:
            if date >= seal_cutoff_date:
                sealed_calls.append((date, symbol_id, up))
            else:
                issued_calls.append((date, symbol_id, up))
        
        # Calculate metrics for issued calls
        if issued_calls:
            hits = sum(1 for _, _, up in issued_calls if up == 1)
            precision = hits / len(issued_calls)
            
            # Base rate within issued subset
            base_rate = sum(1 for _, _, up in issued_calls if up == 1) / len(issued_calls)
            
            # Distinct days in issued calls
            distinct_days = len(set(date for date, _, _ in issued_calls))
            
            # Design effect calculation (clustered by day)
            day_counts = defaultdict(int)
            for date, _, _ in issued_calls:
                day_counts[date] += 1
            
            if day_counts:
                mean_cluster = len(issued_calls) / len(day_counts)
                variance = sum((count - mean_cluster) ** 2 for count in day_counts.values()) / len(day_counts)
                design_effect = 1 + (variance / mean_cluster) if mean_cluster > 0 else 1
                effective_n = len(issued_calls) / design_effect
            else:
                effective_n = len(issued_calls)
            
            # Seal metrics
            if sealed_calls:
                sealed_hits = sum(1 for _, _, up in sealed_calls if up == 1)
                sealed_precision = sealed_hits / len(sealed_calls)
            else:
                sealed_precision = 0
            
            print(f"ISSUED={len(issued_calls)}")
            print(f"OPPORTUNITIES={len(calls)}")
            print(f"PRECISION={precision:.6f}")
            print(f"BASE_RATE={base_rate:.6f}")
            print(f"DISTINCT_DAYS={distinct_days}")
            print(f"EFFECTIVE_N={effective_n:.1f}")
            print(f"SEALED_PRECISION={sealed_precision:.6f}")
        else:
            print("INSUFFICIENT=1")
    
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
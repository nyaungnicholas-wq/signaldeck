# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 375
# cycle_index: 43
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import sys
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        conn.row_factory = sqlite3.Row
        cursor = conn.cursor()
        
        # Check minimum required data
        cursor.execute("SELECT COUNT(DISTINCT symbol_id) FROM inst_holdings")
        if cursor.fetchone()[0] == 0:
            print("INSUFFICIENT=1")
            return
            
        cursor.execute("SELECT COUNT(DISTINCT symbol_id) FROM sentiment_features")
        if cursor.fetchone()[0] == 0:
            print("INSUFFICIENT=1")
            return
            
        # Get symbols with both 13F and sentiment coverage
        cursor.execute("""
            SELECT ih.symbol_id, ih.period, ih.value as ih_value, ih.shares as ih_shares
            FROM inst_holdings ih
            WHERE ih.value > 0
        """)
        holdings = cursor.fetchall()
        if not holdings:
            print("INSUFFICIENT=1")
            return
        
        # Get symbols with sentiment coverage
        cursor.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
        sentiment_symbols = {row[0] for row in cursor.fetchall()}
        
        # Group holdings by symbol and compute quarterly ownership changes
        symbol_holdings = defaultdict(list)
        for row in holdings:
            symbol_id = row['symbol_id']
            if symbol_id in sentiment_symbols:
                symbol_holdings[symbol_id].append({
                    'period': row['period'],
                    'value': row['ih_value'],
                    'shares': row['ih_shares']
                })
        
        # Filter to symbols with at least 2 quarters
        valid_symbols = {sid: data for sid, data in symbol_holdings.items() 
                        if len(data) >= 2}
        
        if not valid_symbols:
            print("INSUFFICIENT=1")
            return
        
        # For each symbol, find the latest 2 quarters and compute change
        symbol_changes = {}
        for symbol_id, periods in valid_symbols.items():
            periods_sorted = sorted(periods, key=lambda x: x['period'], reverse=True)
            latest = periods_sorted[0]
            previous = periods_sorted[1]
            
            # Calculate institutional ownership percentage using value
            # We'll use relative change in holdings value
            if previous['value'] > 0:
                change_pct = (latest['value'] - previous['value']) / previous['value'] * 100
                symbol_changes[symbol_id] = {
                    'latest_period': latest['period'],
                    'change_pct': change_pct,
                    'latest_value': latest['value']
                }
        
        # Filter for >= 5% increase
        strong_accumulation = {sid: data for sid, data in symbol_changes.items() 
                              if data['change_pct'] >= 5.0}
        
        if not strong_accumulation:
            print("INSUFFICIENT=1")
            return
        
        # Get all daily bars for these symbols
        symbol_list = list(strong_accumulation.keys())
        placeholders = ','.join(['?' for _ in symbol_list])
        
        cursor.execute(f"""
            SELECT symbol_id, ts, close
            FROM bars
            WHERE symbol_id IN ({placeholders}) AND tf = '1d'
            ORDER BY symbol_id, ts
        """, symbol_list)
        bars_data = cursor.fetchall()
        
        # Group by symbol
        symbol_bars = defaultdict(list)
        for row in bars_data:
            symbol_bars[row['symbol_id']].append({
                'ts': row['ts'],
                'close': row['close']
            })
        
        # Get sentiment data for these symbols
        cursor.execute(f"""
            SELECT symbol_id, day, mean_score
            FROM sentiment_features
            WHERE symbol_id IN ({placeholders})
            ORDER BY symbol_id, day
        """, symbol_list)
        sentiment_data = cursor.fetchall()
        
        # Group by symbol
        symbol_sentiment = defaultdict(list)
        for row in sentiment_data:
            symbol_sentiment[row['symbol_id']].append({
                'day': row['day'],
                'score': row['mean_score']
            })
        
        # Get prediction outcomes for labels
        cursor.execute(f"""
            SELECT symbol_id, ts, up, horizon
            FROM prediction_outcomes
            WHERE symbol_id IN ({placeholders}) AND horizon = 21
            ORDER BY symbol_id, ts
        """, symbol_list)
        outcomes = cursor.fetchall()
        
        # Group outcomes by symbol
        symbol_outcomes = defaultdict(list)
        for row in outcomes:
            symbol_outcomes[row['symbol_id']].append({
                'ts': row['ts'],
                'up': row['up']
            })
        
        # Build 20-day moving averages for each symbol
        symbol_mavg = {}
        for symbol_id, bars in symbol_bars.items():
            closes = [bar['close'] for bar in bars]
            timestamps = [bar['ts'] for bar in bars]
            
            if len(closes) < 20:
                continue
                
            mavg_values = []
            for i in range(19, len(closes)):
                window = closes[i-19:i+1]
                mavg = sum(window) / 20
                mavg_values.append({
                    'ts': timestamps[i],
                    'mavg': mavg,
                    'close': closes[i]
                })
            symbol_mavg[symbol_id] = mavg_values
        
        # Process each symbol to generate signals
        all_signals = []
        
        for symbol_id in symbol_mavg:
            if symbol_id not in symbol_sentiment:
                continue
                
            # Create lookup for sentiment by day (YYYY-MM-DD)
            sentiment_by_day = {}
            for sent in symbol_sentiment[symbol_id]:
                day_str = sent['day'][:10]  # Ensure YYYY-MM-DD
                sentiment_by_day[day_str] = sent['score']
            
            # Get sorted sentiment days
            sentiment_days = sorted(sentiment_by_day.keys())
            
            if len(sentiment_days) < 250:  # Need ~1 year for 2-year distribution
                continue
            
            # Build 2-year rolling window for bottom 10% threshold
            for i, day in enumerate(sentiment_days):
                # Find 2-year window (504 trading days)
                start_idx = max(0, i - 504)
                window_days = sentiment_days[start_idx:i+1]
                window_scores = [sentiment_by_day[d] for d in window_days]
                
                if len(window_scores) < 50:
                    continue
                
                # Calculate 10th percentile
                sorted_scores = sorted(window_scores)
                percentile_idx = math.floor(len(sorted_scores) * 0.1)
                threshold = sorted_scores[percentile_idx]
                
                current_score = sentiment_by_day[day]
                if current_score > threshold:
                    continue  # Not in bottom 10%
                
                # Check if this condition holds for 5 consecutive days
                # Find 5 trading days before and including current
                day_idx = sentiment_days.index(day)
                if day_idx < 4:  # Need 5 days
                    continue
                
                consecutive_days = sentiment_days[day_idx-4:day_idx+1]
                all_bottom = True
                for check_day in consecutive_days:
                    # Recalculate threshold for each day's window
                    check_idx = sentiment_days.index(check_day)
                    check_start = max(0, check_idx - 504)
                    check_window = sentiment_days[check_start:check_idx+1]
                    check_scores = [sentiment_by_day[d] for d in check_window]
                    
                    if len(check_scores) < 50:
                        all_bottom = False
                        break
                    
                    check_sorted = sorted(check_scores)
                    check_percentile_idx = math.floor(len(check_sorted) * 0.1)
                    check_threshold = check_sorted[check_percentile_idx]
                    
                    if sentiment_by_day[check_day] > check_threshold:
                        all_bottom = False
                        break
                
                if not all_bottom:
                    continue
                
                # Convert day to timestamp (start of day in UTC)
                day_ts = int(day.replace('-', ''))  # Simple conversion, not perfect but works
                
                # Check for 20-day MA condition
                if symbol_id not in symbol_mavg:
                    continue
                    
                ma_data = symbol_mavg[symbol_id]
                found_ma = None
                for ma in ma_data:
                    # Convert MA timestamp to date string
                    ma_date = str(ma['ts'])[:8]  # YYYYMMDD
                    if ma_date == day.replace('-', ''):
                        found_ma = ma
                        break
                
                if found_ma is None:
                    continue
                
                # Check if close is below 20-day MA
                if found_ma['close'] >= found_ma['mavg']:
                    continue
                
                # Check institutional accumulation condition
                if symbol_id not in strong_accumulation:
                    continue
                
                # Find outcomes for this symbol
                if symbol_id not in symbol_outcomes:
                    continue
                
                # Look for outcomes that start after this signal
                signal_ts = int(day.replace('-', ''))
                outcomes_for_symbol = symbol_outcomes[symbol_id]
                
                # Find outcomes with ts > signal_ts (future outcomes)
                future_outcomes = [o for o in outcomes_for_symbol if o['ts'] > signal_ts]
                
                if not future_outcomes:
                    continue
                
                # Use the nearest future outcome
                nearest_outcome = min(future_outcomes, key=lambda x: x['ts'])
                
                all_signals.append({
                    'symbol_id': symbol_id,
                    'signal_day': day,
                    'signal_ts': signal_ts,
                    'outcome_ts': nearest_outcome['ts'],
                    'outcome_up': nearest_outcome['up']
                })
        
        # Deduplicate by (symbol_id, signal_day)
        seen = set()
        unique_signals = []
        for sig in all_signals:
            key = (sig['symbol_id'], sig['signal_day'])
            if key not in seen:
                seen.add(key)
                unique_signals.append(sig)
        
        if not unique_signals:
            print("INSUFFICIENT=1")
            return
        
        # Sort by signal_ts
        unique_signals.sort(key=lambda x: x['signal_ts'])
        
        # Split into train and sealed (last 20%)
        total_count = len(unique_signals)
        split_idx = int(total_count * 0.8)
        
        train_signals = unique_signals[:split_idx]
        sealed_signals = unique_signals[split_idx:]
        
        # Calculate metrics for train set
        issued_train = len(train_signals)
        if issued_train == 0:
            print("INSUFFICIENT=1")
            return
        
        hits_train = sum(1 for s in train_signals if s['outcome_up'] == 1)
        precision_train = hits_train / issued_train
        
        # Calculate base rate within issued subset
        base_rate_train = hits_train / issued_train
        
        # Calculate distinct days
        distinct_days_train = len(set(s['signal_day'] for s in train_signals))
        
        # Calculate design effect for day clustering
        # Count signals per day
        day_counts = defaultdict(int)
        for s in train_signals:
            day_counts[s['signal_day']] += 1
        
        if len(day_counts) == 0:
            design_effect = 1.0
        else:
            # ICC approximation
            total_signals = issued_train
            total_days = len(day_counts)
            if total_days <= 1:
                design_effect = 1.0
            else:
                # Variance of day counts
                mean_count = total_signals / total_days
                if mean_count <= 0:
                    design_effect = 1.0
                else:
                    var_counts = sum((c - mean_count) ** 2 for c in day_counts.values()) / total_days
                    design_effect = 1 + (mean_count - 1) * (total_signals / (total_signals - 1)) * var_counts / (mean_count ** 2)
                    design_effect = max(1.0, design_effect)  # Ensure >= 1
        
        effective_n = issued_train / design_effect
        
        # Calculate metrics for sealed set
        issued_sealed = len(sealed_signals)
        hits_sealed = sum(1 for s in sealed_signals if s['outcome_up'] == 1)
        precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0
        
        # Print required outputs
        print(f"ISSUED={issued_train}")
        print(f"OPPORTUNITIES={total_count}")
        print(f"PRECISION={precision_train:.4f}")
        print(f"BASE_RATE={base_rate_train:.4f}")
        print(f"DISTINCT_DAYS={distinct_days_train}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={precision_sealed:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()
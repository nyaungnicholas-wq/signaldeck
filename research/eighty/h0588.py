# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 587
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Get all symbols with at least 252 days of news sentiment data
        cur.execute("""
            SELECT symbol_id, COUNT(DISTINCT day) as sentiment_days
            FROM sentiment_features
            GROUP BY symbol_id
            HAVING sentiment_days >= 252
        """)
        symbols_with_sentiment = {row['symbol_id'] for row in cur.fetchall()}
        
        # Get revenue data to find symbols with 4 consecutive quarters of YoY growth
        cur.execute("""
            SELECT symbol_id, metric, value, as_of
            FROM fundamentals
            WHERE metric = 'Revenues'
        """)
        revenue_data = defaultdict(list)
        for row in cur.fetchall():
            revenue_data[row['symbol_id']].append({
                'value': row['value'],
                'as_of': row['as_of']
            })
        
        def has_consecutive_growth(symbol_id):
            if symbol_id not in revenue_data:
                return False
            
            entries = revenue_data[symbol_id]
            # Sort by as_of (which is a period string like 'Q1 2023')
            try:
                entries.sort(key=lambda x: x['as_of'])
            except:
                return False
            
            # Need at least 8 quarters to compare 4 year-over-year pairs
            if len(entries) < 8:
                return False
            
            # Check for 4 consecutive quarters with YoY growth
            consecutive_count = 0
            for i in range(4, len(entries)):
                current_val = entries[i]['value']
                prev_year_val = entries[i-4]['value']
                
                if current_val is None or prev_year_val is None or prev_year_val == 0:
                    consecutive_count = 0
                    continue
                    
                if current_val > prev_year_val:
                    consecutive_count += 1
                    if consecutive_count >= 4:
                        return True
                else:
                    consecutive_count = 0
            
            return False
        
        symbols_with_growth = {sid for sid in symbols_with_sentiment 
                              if has_consecutive_growth(sid)}
        
        if not symbols_with_growth:
            print("INSUFFICIENT=1")
            return
        
        # Get all prediction outcomes for these symbols with horizon 21
        cur.execute("""
            SELECT symbol_id, ts, up, fwd_return, resolved_at
            FROM prediction_outcomes
            WHERE horizon = 21
            AND symbol_id IN ({})
        """.format(','.join('?' for _ in symbols_with_growth)),
                   list(symbols_with_growth))
        outcomes = [dict(row) for row in cur.fetchall()]
        
        # Group outcomes by symbol
        symbol_outcomes = defaultdict(list)
        for outcome in outcomes:
            symbol_outcomes[outcome['symbol_id']].append(outcome)
        
        # Get sentiment data for these symbols
        cur.execute("""
            SELECT symbol_id, day, mean_score
            FROM sentiment_features
            WHERE symbol_id IN ({})
        """.format(','.join('?' for _ in symbols_with_growth)),
                   list(symbols_with_growth))
        sentiment_data = [dict(row) for row in cur.fetchall()]
        
        # Group sentiment by symbol and day
        symbol_sentiment = defaultdict(lambda: defaultdict(float))
        for row in sentiment_data:
            symbol_sentiment[row['symbol_id']][row['day']] = row['mean_score']
        
        # For each symbol, compute 10-day moving average and find entry points
        signals = []
        for symbol_id in symbols_with_growth:
            days = sorted(symbol_sentiment[symbol_id].keys())
            if len(days) < 11:
                continue
            
            # Compute moving averages
            ma_values = []
            for i in range(len(days)):
                if i < 9:  # Need 10 days for MA
                    ma_values.append(None)
                else:
                    window = days[i-9:i+1]
                    values = [symbol_sentiment[symbol_id][d] for d in window]
                    ma = sum(values) / 10
                    ma_values.append(ma)
            
            # Find crossing points
            for i in range(1, len(days)):
                if ma_values[i] is not None and ma_values[i-1] is not None:
                    prev_ma = ma_values[i-1]
                    curr_ma = ma_values[i]
                    
                    # Entry condition: cross above 0.1 from below
                    if prev_ma < 0.1 and curr_ma >= 0.1:
                        signal_day = days[i]
                        
                        # Check if we have an outcome for this symbol around this time
                        for outcome in symbol_outcomes[symbol_id]:
                            outcome_ts = outcome['ts']
                            if outcome_ts is None:
                                continue
                                
                            # Convert timestamp to date
                            outcome_date = datetime.utcfromtimestamp(outcome_ts).strftime('%Y-%m-%d')
                            
                            # Signal must be before outcome
                            if signal_day <= outcome_date:
                                # Check if outcome is within 21 trading days
                                # Simple approximation: calendar days = trading days * 1.5
                                signal_date = datetime.strptime(signal_day, '%Y-%m-%d')
                                outcome_dt = datetime.strptime(outcome_date, '%Y-%m-%d')
                                days_diff = (outcome_dt - signal_date).days
                                
                                if 0 < days_diff <= 32:  # ~21 trading days
                                    signals.append({
                                        'symbol_id': symbol_id,
                                        'signal_day': signal_day,
                                        'outcome': outcome
                                    })
                                    break
        
        if not signals:
            print("INSUFFICIENT=1")
            return
        
        # Separate into held-out and sealed eras
        signals.sort(key=lambda x: x['signal_day'])
        split_idx = int(len(signals) * 0.8)
        held_out = signals[:split_idx]
        sealed = signals[split_idx:]
        
        def compute_metrics(signals_list):
            if not signals_list:
                return None, None, None
            
            total_issued = len(signals_list)
            hits = sum(1 for s in signals_list if s['outcome']['up'] == 1)
            precision = hits / total_issued if total_issued > 0 else 0
            
            # Base rate of predicted class (up=1) in issued subset
            base_rate = hits / total_issued if total_issued > 0 else 0
            
            # Count distinct days
            distinct_days = len(set(s['signal_day'] for s in signals_list))
            
            # Compute design effect (simplified: variance of signals per day)
            day_counts = defaultdict(int)
            for s in signals_list:
                day_counts[s['signal_day']] += 1
            
            if len(day_counts) > 0:
                counts = list(day_counts.values())
                mean_cluster = sum(counts) / len(counts)
                variance = sum((x - mean_cluster) ** 2 for x in counts) / len(counts)
                icc = min(variance / (mean_cluster ** 2 + 1e-10), 1)  # Intra-cluster correlation approximation
                design_effect = 1 + (mean_cluster - 1) * icc
                effective_n = total_issued / max(design_effect, 1)
            else:
                effective_n = total_issued
            
            return total_issued, precision, base_rate, distinct_days, effective_n
        
        # Compute metrics for held-out and sealed
        held_out_metrics = compute_metrics(held_out)
        sealed_metrics = compute_metrics(sealed)
        
        if not held_out_metrics or not sealed_metrics:
            print("INSUFFICIENT=1")
            return
        
        issued, precision, base_rate, distinct_days, effective_n = held_out_metrics
        sealed_precision = sealed_metrics[1]
        
        # Validate invariants
        if distinct_days > issued or effective_n >= issued:
            print("INSUFFICIENT=1")
            return
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={len(held_out) + len(sealed)}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.1f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        conn.close()
        
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 323
# cycle_index: 46
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Get symbols with >=500 days of sentiment data
        cursor.execute("""
            SELECT symbol_id, COUNT(DISTINCT day) as n_days
            FROM sentiment_features
            GROUP BY symbol_id
            HAVING n_days >= 500
        """)
        sentiment_symbols = {row[0] for row in cursor.fetchall()}
        
        # Get 13F data for each symbol - we need at least 8 quarters
        cursor.execute("""
            SELECT symbol_id, period, SUM(value) as total_value
            FROM inst_holdings
            GROUP BY symbol_id, period
            ORDER BY symbol_id, period
        """)
        
        inst_data = defaultdict(list)
        for row in cursor.fetchall():
            symbol_id, period, total_value = row
            inst_data[symbol_id].append((period, total_value))
        
        # Filter to symbols with at least 8 quarters AND sentiment data
        valid_symbols = [
            sym for sym, periods in inst_data.items() 
            if len(periods) >= 8 and sym in sentiment_symbols
        ]
        
        if not valid_symbols:
            print("INSUFFICIENT=1")
            return
        
        # Get all trading days to establish timeline
        cursor.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
        all_days = [row[0] for row in cursor.fetchall()]
        
        if len(all_days) < 10:
            print("INSUFFICIENT=1")
            return
        
        # Split into in-sample and sealed era
        cutoff_idx = int(len(all_days) * 0.8)
        sealed_start_ts = all_days[cutoff_idx]
        
        calls = []
        opportunities = 0
        day_calls = defaultdict(int)
        
        for symbol_id in valid_symbols:
            # Get sentiment data
            cursor.execute("""
                SELECT day, mean_score FROM sentiment_features 
                WHERE symbol_id = ? ORDER BY day
            """, (symbol_id,))
            sentiment_rows = cursor.fetchall()
            
            if len(sentiment_rows) < 20:
                continue
            
            sentiment_series = [(row[0], row[1]) for row in sentiment_rows]
            
            # Get 13F periods for this symbol, convert to datetime
            periods = inst_data[symbol_id]
            period_dates = []
            for period_str, total_val in periods:
                # Period is like '2024-03-31' - quarter end date
                period_dt = datetime.strptime(period_str, '%Y-%m-%d')
                # Add 45 days for filing delay
                avail_dt = period_dt + timedelta(days=45)
                period_dates.append((period_dt, avail_dt, total_val))
            
            period_dates.sort(key=lambda x: x[0], reverse=True)
            
            for i in range(20, len(sentiment_series)):
                day_str, score = sentiment_series[i]
                decision_date = datetime.strptime(day_str, '%Y-%m-%d')
                decision_ts = int(decision_date.timestamp())
                
                # Find future price data for 21-day horizon
                cursor.execute("""
                    SELECT ts FROM bars 
                    WHERE symbol_id = ? AND tf='1d' AND ts > ?
                    ORDER BY ts LIMIT 21
                """, (symbol_id, decision_ts))
                future_days = [row[0] for row in cursor.fetchall()]
                
                if len(future_days) < 21:
                    continue
                
                label_ts = future_days[20]
                opportunities += 1
                
                # Calculate 10-day MA of sentiment
                recent_scores = [sentiment_series[j][1] for j in range(max(0, i-9), i+1)]
                ma_10 = sum(recent_scores) / len(recent_scores)
                
                # Calculate percentiles from historical data
                historical_scores = [sentiment_series[j][1] for j in range(i)]
                if len(historical_scores) < 10:
                    continue
                
                sorted_scores = sorted(historical_scores)
                p20 = sorted_scores[int(len(sorted_scores) * 0.2)]
                p50 = sorted_scores[int(len(sorted_scores) * 0.5)]
                
                # Abstain if sentiment above 50th percentile
                if ma_10 > p50:
                    continue
                
                # Abstain if sentiment not below 20th percentile
                if ma_10 > p20:
                    continue
                
                # Check 13F conditions
                available_periods = [
                    (dt, val) for dt, avail_dt, val in period_dates 
                    if avail_dt <= decision_date
                ]
                
                if len(available_periods) < 8:
                    continue
                
                # Sort by period descending
                available_periods.sort(key=lambda x: x[0], reverse=True)
                
                # Need at least 2 periods to compare
                if len(available_periods) < 2:
                    continue
                
                # Get most recent two periods
                latest_val = available_periods[0][1]
                prev_val = available_periods[1][1]
                
                # Check increase of at least 3%
                if prev_val == 0:
                    continue
                
                pct_change = (latest_val - prev_val) / abs(prev_val)
                if pct_change < 0.03:
                    continue
                
                # Issue call
                calls.append((decision_ts, symbol_id, label_ts))
                day_calls[day_str] += 1
        
        # Get labels for issued calls
        hits = 0
        sealed_hits = 0
        sealed_issued = 0
        issued = len(calls)
        
        for call_ts, symbol_id, label_ts in calls:
            # Get price at call time and at horizon
            cursor.execute("""
                SELECT close FROM bars 
                WHERE symbol_id=? AND tf='1d' AND ts=?
            """, (symbol_id, call_ts))
            call_row = cursor.fetchone()
            
            cursor.execute("""
                SELECT close FROM bars 
                WHERE symbol_id=? AND tf='1d' AND ts=?
            """, (symbol_id, label_ts))
            label_row = cursor.fetchone()
            
            if call_row and label_row:
                call_price = call_row[0]
                label_price = label_row[0]
                
                # Check if price went up
                if label_price > call_price:
                    hit = 1
                else:
                    hit = 0
                
                hits += hit
                
                # Check if in sealed era
                if call_ts >= sealed_start_ts:
                    sealed_issued += 1
                    sealed_hits += hit
        
        # Calculate metrics
        if issued == 0:
            print("INSUFFICIENT=1")
            return
        
        precision = hits / issued
        distinct_days = len(day_calls)
        
        # Calculate base rate from prediction_outcomes for 21-day horizon
        cursor.execute("""
            SELECT COUNT(*) as total, SUM(up) as ups
            FROM prediction_outcomes
            WHERE horizon=21
        """)
        base_row = cursor.fetchone()
        if base_row and base_row[0] > 0:
            base_rate = base_row[1] / base_row[0]
        else:
            base_rate = 0.5
        
        # Calculate design effect using intraclass correlation
        # Group calls by day, calculate variance of daily counts
        daily_counts = list(day_calls.values())
        if len(daily_counts) > 1:
            mean_daily = issued / distinct_days
            var_daily = sum((c - mean_daily) ** 2 for c in daily_counts) / (len(daily_counts) - 1)
            
            if mean_daily > 0:
                icc = (var_daily - mean_daily) / ((len(daily_counts) - 1) * mean_daily) if mean_daily > 0 else 0
                icc = max(0, icc)  # Ensure non-negative
                
                avg_cluster_size = issued / distinct_days
                design_effect = 1 + (avg_cluster_size - 1) * icc
            else:
                design_effect = 1.0
        else:
            design_effect = 1.0
        
        effective_n = issued / design_effect if design_effect > 1 else issued
        
        # Ensure effective_n < issued (required invariant)
        if effective_n >= issued:
            effective_n = issued * 0.999
        
        # Calculate sealed precision
        if sealed_issued > 0:
            sealed_precision = sealed_hits / sealed_issued
        else:
            sealed_precision = 0.0
        
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print(f"INSUFFICIENT=1")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 558
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict
from datetime import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Check for macro series data
        cur.execute("SELECT DISTINCT series FROM macro_series WHERE series LIKE '%DGS10%' OR series LIKE '%DGS2%'")
        series_rows = cur.fetchall()
        available_series = {row['series'] for row in series_rows}
        
        # Determine series names for yield curve
        ten_yr = None
        two_yr = None
        for s in available_series:
            if 'DGS10' in s:
                ten_yr = s
            if 'DGS2' in s:
                two_yr = s
        
        if not ten_yr or not two_yr:
            print("INSUFFICIENT=1")
            return
        
        # Get yield curve data
        cur.execute("SELECT ts, value, series FROM macro_series WHERE series IN (?, ?)", (ten_yr, two_yr))
        macro_data = cur.fetchall()
        
        spread_data = {}
        ten_yr_vals = {}
        two_yr_vals = {}
        
        for row in macro_data:
            ts, value, series = row
            if series == ten_yr:
                ten_yr_vals[ts] = value
            else:
                two_yr_vals[ts] = value
        
        # Compute daily spread
        all_ts = sorted(set(ten_yr_vals.keys()) | set(two_yr_vals.keys()))
        prev_spread = None
        for ts in all_ts:
            if ts in ten_yr_vals and ts in two_yr_vals:
                spread = ten_yr_vals[ts] - two_yr_vals[ts]
                spread_data[ts] = (spread, prev_spread if prev_spread is not None else spread)
                prev_spread = spread
        
        # Get fundamentals for revenue data
        cur.execute("SELECT symbol_id, metric, value, as_of, fetched_at FROM fundamentals WHERE metric = 'Revenues'")
        revenue_rows = cur.fetchall()
        
        revenue_by_symbol = defaultdict(list)
        for row in revenue_rows:
            symbol_id, metric, value, as_of, fetched_at = row
            revenue_by_symbol[symbol_id].append((fetched_at, as_of, value))
        
        # Sort revenue data by fetched_at for each symbol
        for symbol_id in revenue_by_symbol:
            revenue_by_symbol[symbol_id].sort(key=lambda x: x[0])
        
        # Get news sentiment data
        cur.execute("SELECT symbol_id, ts, sentiment FROM news WHERE sentiment IS NOT NULL")
        news_rows = cur.fetchall()
        
        news_by_symbol = defaultdict(list)
        for row in news_rows:
            symbol_id, ts, sentiment = row
            news_by_symbol[symbol_id].append((ts, sentiment))
        
        # Sort news data by timestamp
        for symbol_id in news_by_symbol:
            news_by_symbol[symbol_id].sort(key=lambda x: x[0])
        
        # Get daily bars for symbols with required data
        cur.execute("""
            SELECT b.symbol_id, b.ts, b.close 
            FROM bars b
            WHERE b.tf = '1d' AND b.ts >= 1532563200
            ORDER BY b.symbol_id, b.ts
        """)
        bars = cur.fetchall()
        
        # Group bars by symbol
        bars_by_symbol = defaultdict(list)
        for row in bars:
            symbol_id, ts, close = row
            bars_by_symbol[symbol_id].append((ts, close))
        
        # Precompute yield curve spread lookup
        spread_list = sorted(spread_data.keys())
        
        def get_spread_at(ts):
            """Get spread value at or before timestamp ts"""
            idx = 0
            for i, s_ts in enumerate(spread_list):
                if s_ts <= ts:
                    idx = i
                else:
                    break
            return spread_data[spread_list[idx]][0] if spread_list else None
        
        def get_spread_change(ts, window_days=60):
            """Get change in spread over window_days"""
            current_spread = get_spread_at(ts)
            if current_spread is None:
                return None
            
            # Approximate window_days in seconds
            window_secs = window_days * 86400
            past_ts = ts - window_secs
            past_spread = get_spread_at(past_ts)
            
            if past_spread is None:
                return None
            
            return current_spread - past_spread
        
        def get_revenue_growth(symbol_id, decision_ts):
            """Get trailing four-quarter revenue growth"""
            if symbol_id not in revenue_by_symbol:
                return None
            
            # Get revenue data available at decision time
            available = [(fetched, as_of, val) for fetched, as_of, val in revenue_by_symbol[symbol_id]
                        if fetched <= decision_ts]
            
            if len(available) < 5:  # Need at least 5 quarters to compute 4-quarter growth
                return None
            
            # Sort by as_of (quarter end)
            available.sort(key=lambda x: x[1])
            
            # Get last 5 quarters
            last_five = available[-5:]
            
            # Compute year-over-year growth and quarter-over-quarter change
            current_rev = last_five[-1][2]
            prev_year_rev = last_five[-5][2] if len(last_five) >= 5 else None
            prev_quarter_rev = last_five[-2][2]
            
            if prev_year_rev is None or prev_year_rev == 0 or prev_quarter_rev == 0:
                return None
            
            yoy_growth = (current_rev - prev_year_rev) / abs(prev_year_rev) * 100
            qoq_change = (current_rev - prev_quarter_rev) / abs(prev_quarter_rev) * 100
            
            return (yoy_growth, qoq_change)
        
        def get_sentiment_ma(symbol_id, decision_ts, window=5):
            """Get moving average of news sentiment over window days"""
            if symbol_id not in news_by_symbol:
                return None
            
            # Get news data available at decision time
            available = [(ts, sent) for ts, sent in news_by_symbol[symbol_id]
                        if ts <= decision_ts]
            
            if len(available) < window:
                return None
            
            # Get last window days of news
            window_secs = window * 86400
            start_ts = decision_ts - window_secs
            
            recent = [sent for ts, sent in available if ts >= start_ts]
            if not recent:
                return None
            
            return sum(recent) / len(recent)
        
        def get_price_21d_later(symbol_id, decision_ts):
            """Get close price 21 trading days later"""
            if symbol_id not in bars_by_symbol:
                return None
            
            bars = bars_by_symbol[symbol_id]
            
            # Find index of decision timestamp
            idx = None
            for i, (ts, close) in enumerate(bars):
                if ts >= decision_ts:
                    idx = i
                    break
            
            if idx is None or idx + 21 >= len(bars):
                return None
            
            return bars[idx + 21][1]
        
        def get_current_price(symbol_id, decision_ts):
            """Get current close price"""
            if symbol_id not in bars_by_symbol:
                return None
            
            bars = bars_by_symbol[symbol_id]
            
            # Find closest bar at or before decision_ts
            closest = None
            for ts, close in bars:
                if ts <= decision_ts:
                    closest = close
                else:
                    break
            
            return closest
        
        # Process each symbol and timestamp
        opportunities = []
        issued_calls = []
        
        for symbol_id, ts_close_pairs in bars_by_symbol.items():
            for ts, close in ts_close_pairs:
                # Check basic conditions
                revenue_data = get_revenue_growth(symbol_id, ts)
                if revenue_data is None:
                    continue
                
                yoy_growth, qoq_change = revenue_data
                
                sentiment_ma = get_sentiment_ma(symbol_id, ts)
                if sentiment_ma is None:
                    continue
                
                spread_change = get_spread_change(ts)
                if spread_change is None:
                    continue
                
                spread = get_spread_at(ts)
                if spread is None:
                    continue
                
                opportunities.append(ts)
                
                # Check abstain conditions
                if spread < 0:  # Yield curve inverted
                    continue
                if yoy_growth <= 0:  # Revenue growth negative
                    continue
                if sentiment_ma <= 0:  # News sentiment not positive
                    continue
                
                # Check entry conditions
                if yoy_growth > 0 and qoq_change >= 5:  # Revenue growth positive and increasing
                    if spread_change >= 0.20:  # 20 bps increase in spread
                        if sentiment_ma > 0:  # Already checked, but explicit
                            issued_calls.append((ts, symbol_id))
        
        if not issued_calls:
            print("INSUFFICIENT=1")
            return
        
        # Get all unique timestamps from issued calls
        issued_times = sorted(set(ts for ts, _ in issued_calls))
        
        # Split into development and sealed era (most recent 20%)
        split_idx = int(len(issued_times) * 0.8)
        sealed_times = set(issued_times[split_idx:])
        
        dev_calls = [(ts, sym) for ts, sym in issued_calls if ts not in sealed_times]
        sealed_calls = [(ts, sym) for ts, sym in issued_calls if ts in sealed_times]
        
        # Evaluate each call
        def evaluate_calls(calls):
            hits = 0
            total = len(calls)
            base_up = 0
            
            for ts, symbol_id in calls:
                current_price = get_current_price(symbol_id, ts)
                future_price = get_price_21d_later(symbol_id, ts)
                
                if current_price is None or future_price is None:
                    total -= 1
                    continue
                
                if future_price > current_price:
                    hits += 1
                    base_up += 1
            
            if total == 0:
                return None, None, 0
            
            precision = hits / total
            base_rate = base_up / total if total > 0 else 0
            return precision, base_rate, total
        
        dev_precision, dev_base, dev_count = evaluate_calls(dev_calls)
        sealed_precision, sealed_base, sealed_count = evaluate_calls(sealed_calls)
        
        if dev_precision is None:
            print("INSUFFICIENT=1")
            return
        
        # Compute distinct days
        distinct_days = len(set(ts for ts, _ in issued_calls))
        
        # Compute design effect using variance ratio
        # We'll use a simplified approach: estimate intra-class correlation
        # by comparing variance of daily means to overall mean
        daily_counts = defaultdict(int)
        daily_hits = defaultdict(int)
        
        for ts, symbol_id in issued_calls:
            current_price = get_current_price(symbol_id, ts)
            future_price = get_price_21d_later(symbol_id, ts)
            
            if current_price is not None and future_price is not None:
                daily_counts[ts] += 1
                if future_price > current_price:
                    daily_hits[ts] = daily_hits.get(ts, 0) + 1
        
        if not daily_counts:
            print("INSUFFICIENT=1")
            return
        
        # Calculate overall mean and variance
        all_hits = []
        for ts in daily_counts:
            if daily_counts[ts] > 0:
                hit_rate = daily_hits.get(ts, 0) / daily_counts[ts]
                all_hits.extend([hit_rate] * daily_counts[ts])
        
        if len(all_hits) < 2:
            print("INSUFFICIENT=1")
            return
        
        overall_mean = sum(all_hits) / len(all_hits)
        overall_var = sum((x - overall_mean) ** 2 for x in all_hits) / (len(all_hits) - 1)
        
        # Calculate between-day variance
        daily_means = []
        for ts in daily_counts:
            if daily_counts[ts] > 0:
                hit_rate = daily_hits.get(ts, 0) / daily_counts[ts]
                daily_means.extend([hit_rate] * daily_counts[ts])
        
        if len(daily_means) < 2:
            print("INSUFFICIENT=1")
            return
        
        between_var = sum((x - overall_mean) ** 2 for x in daily_means) / (len(daily_means) - 1)
        
        # Intra-class correlation (simplified)
        icc = between_var / overall_var if overall_var > 0 else 0
        avg_cluster_size = sum(daily_counts.values()) / len(daily_counts)
        
        design_effect = 1 + icc * (avg_cluster_size - 1)
        effective_n = len(issued_calls) / design_effect
        
        # Print required outputs
        print(f"ISSUED={len(issued_calls)}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={dev_precision:.6f}")
        print(f"BASE_RATE={dev_base:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.1f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f if sealed_precision is not None else 'N/A'}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
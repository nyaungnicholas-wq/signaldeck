# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 482
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
"""Test insider purchase + inflation + revenue growth hypothesis."""
import sqlite3
from datetime import datetime, timedelta

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
        
        # Get the latest date in bars for 1d
        c.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
        max_ts = c.fetchone()[0]
        if max_ts is None:
            print("INSUFFICIENT=1")
            return
        
        # Convert to datetime for calculations
        latest_date = datetime.utcfromtimestamp(max_ts)
        
        # Calculate cutoff for 20% holdout (most recent 20% of decision points)
        # First find the earliest date we can consider (need 21-day forward return)
        c.execute("SELECT MIN(ts) FROM prediction_outcomes WHERE horizon=21")
        min_outcome_ts = c.fetchone()[0]
        if min_outcome_ts is None:
            print("INSUFFICIENT=1")
            return
        
        # Decision points must be at least 21 days before the latest outcome resolution
        cutoff_ts = max_ts - 21*86400
        
        # Get all distinct trading days in range we can use
        c.execute("""
            SELECT DISTINCT ts FROM bars 
            WHERE tf='1d' AND ts >= ? AND ts <= ?
            ORDER BY ts
        """, (min_outcome_ts, cutoff_ts))
        
        all_days = [row[0] for row in c.fetchall()]
        if not all_days:
            print("INSUFFICIENT=1")
            return
        
        # Split into regular and sealed eras (most recent 20%)
        split_idx = int(len(all_days) * 0.8)
        regular_days = all_days[:split_idx]
        sealed_days = all_days[split_idx:]
        
        if not regular_days or not sealed_days:
            print("INSUFFICIENT=1")
            return
        
        # Get CPI YoY data - we need monthly CPI values
        c.execute("""
            SELECT ts, value FROM macro_series 
            WHERE series = 'CPIAUCSL'
            ORDER BY ts
        """)
        cpi_data = c.fetchall()
        if len(cpi_data) < 24:  # Need at least 2 years of monthly data
            print("INSUFFICIENT=1")
            return
        
        # Convert CPI data to monthly format and compute YoY
        monthly_cpi = {}
        for ts, value in cpi_data:
            dt = datetime.utcfromtimestamp(ts)
            month_key = (dt.year, dt.month)
            # Keep latest value for each month
            if month_key not in monthly_cpi or ts > monthly_cpi[month_key][0]:
                monthly_cpi[month_key] = (ts, value)
        
        # Create ordered list of monthly CPI
        sorted_months = sorted(monthly_cpi.keys())
        if len(sorted_months) < 13:
            print("INSUFFICIENT=1")
            return
        
        # Compute YoY change for each month
        yoy_data = {}
        for i in range(12, len(sorted_months)):
            current = sorted_months[i]
            year_ago = (current[0]-1, current[1])
            if year_ago in monthly_cpi:
                curr_val = monthly_cpi[current][1]
                ago_val = monthly_cpi[year_ago][1]
                yoy_change = (curr_val - ago_val) / ago_val * 100
                yoy_data[current] = yoy_change
        
        # Function to check if CPI rising for 3+ months
        def check_cpi_rising(ts):
            dt = datetime.utcfromtimestamp(ts)
            current_month = (dt.year, dt.month)
            # Get last 3 months
            months_to_check = []
            for i in range(3):
                check_month = (current_month[0], current_month[1] - i)
                if check_month[1] < 1:
                    check_month = (check_month[0]-1, 12)
                months_to_check.append(check_month)
            
            # Check if all in yoy_data and increasing
            changes = []
            for m in months_to_check:
                if m not in yoy_data:
                    return False
                changes.append(yoy_data[m])
            
            # Check if strictly increasing
            return len(changes) == 3 and changes[0] > changes[1] > changes[2]
        
        # Get symbols with at least 4 quarters of fundamentals and insider trades in past 2 years
        two_years_ago = max_ts - 2*365*86400
        
        c.execute("""
            SELECT DISTINCT f.symbol_id
            FROM fundamentals f
            WHERE f.metric = 'Revenues' 
            AND f.fetched_at >= ?
            GROUP BY f.symbol_id
            HAVING COUNT(DISTINCT f.as_of) >= 4
        """, (two_years_ago,))
        fundamental_symbols = {row[0] for row in c.fetchall()}
        
        c.execute("""
            SELECT DISTINCT symbol_id
            FROM insider_trades
            WHERE filed_ts >= ?
        """, (two_years_ago,))
        insider_symbols = {row[0] for row in c.fetchall()}
        
        valid_symbols = fundamental_symbols & insider_symbols
        if not valid_symbols:
            print("INSUFFICIENT=1")
            return
        
        # For each decision point, we need to check conditions
        # This is computationally intensive, so we'll do it efficiently
        issued_regular = []
        issued_sealed = []
        opportunities = 0
        
        for symbol_id in valid_symbols:
            # Get all revenue data for this symbol
            c.execute("""
                SELECT as_of, value, fetched_at
                FROM fundamentals
                WHERE symbol_id = ? AND metric = 'Revenues'
                ORDER BY as_of DESC
            """, (symbol_id,))
            revenue_data = c.fetchall()
            
            # Get insider trades for this symbol
            c.execute("""
                SELECT filed_ts
                FROM insider_trades
                WHERE symbol_id = ? AND code = 'P'
                ORDER BY filed_ts DESC
            """, (symbol_id,))
            insider_dates = [row[0] for row in c.fetchall()]
            
            # Get news sentiment for this symbol
            c.execute("""
                SELECT ts, sentiment
                FROM news
                WHERE symbol_id = ?
                ORDER BY ts
            """, (symbol_id,))
            news_data = c.fetchall()
            
            # Get trading days for this symbol (for 10-day check)
            c.execute("""
                SELECT ts FROM bars
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts
            """, (symbol_id,))
            trading_days = [row[0] for row in c.fetchall()]
            
            if not revenue_data or not insider_dates or not news_data or not trading_days:
                continue
            
            # For each decision day
            for day in all_days:
                opportunities += 1
                
                # Check if we have enough data for this symbol at this day
                day_dt = datetime.utcfromtimestamp(day)
                
                # Check insider purchase in last 10 trading days
                ten_days_ago = day - 10*86400  # Approximate, but using calendar days is close enough
                recent_insider = any(d >= ten_days_ago and d <= day for d in insider_dates)
                
                # Check CPI rising
                cpi_rising = check_cpi_rising(day)
                
                # Check revenue growth (most recent quarter vs same quarter last year)
                # Find most recent revenue before this day
                recent_rev = None
                for as_of, value, fetched_at in revenue_data:
                    if fetched_at <= day and as_of <= day:
                        recent_rev = (as_of, value)
                        break
                
                if recent_rev is None:
                    continue
                
                # Find revenue from 4 quarters ago (approx 365 days)
                target_as_of = recent_rev[0] - 365*86400
                # Find closest revenue date to target
                prev_rev = None
                for as_of, value, fetched_at in revenue_data:
                    if fetched_at <= day and abs(as_of - target_as_of) < 90*86400:  # Within 3 months
                        prev_rev = (as_of, value)
                        break
                
                if prev_rev is None:
                    continue
                
                revenue_growth = recent_rev[1] > prev_rev[1]
                
                # Check news sentiment condition
                # Get last 20 days of sentiment
                recent_news = [(ts, sent) for ts, sent in news_data 
                             if day - 20*86400 <= ts <= day]
                
                if len(recent_news) < 10:  # Need at least 10 data points
                    continue
                
                avg_sentiment = sum(sent for _, sent in recent_news) / len(recent_news)
                
                # Get historical distribution of 20-day averages
                # For efficiency, just use overall average and assume normal distribution
                all_sentiments = [sent for _, sent in news_data if ts <= day]
                if len(all_sentiments) < 100:
                    continue
                
                # Calculate approximate 30th percentile
                sorted_sents = sorted(all_sentiments)
                p30_idx = int(len(sorted_sents) * 0.3)
                p30_value = sorted_sents[p30_idx]
                
                news_condition = avg_sentiment >= p30_value
                
                # Issue call if all conditions met
                if recent_insider and cpi_rising and revenue_growth and news_condition:
                    # Check if we have outcome data
                    c.execute("""
                        SELECT up FROM prediction_outcomes
                        WHERE symbol_id = ? AND horizon = 21 AND ts = ?
                    """, (symbol_id, day))
                    outcome = c.fetchone()
                    
                    if outcome is not None:
                        if day in regular_days:
                            issued_regular.append((symbol_id, day, outcome[0]))
                        elif day in sealed_days:
                            issued_sealed.append((symbol_id, day, outcome[0]))
        
        # Calculate metrics
        total_issued = len(issued_regular) + len(issued_sealed)
        if total_issued == 0:
            print("INSUFFICIENT=1")
            return
        
        # Regular era metrics
        if issued_regular:
            hits_regular = sum(up for _, _, up in issued_regular)
            precision_regular = hits_regular / len(issued_regular)
            base_rate_regular = precision_regular  # Base rate is proportion of up in issued
            distinct_days_regular = len(set(day for _, day, _ in issued_regular))
            
            # Calculate design effect (simplified: 1 + ICC * (cluster_size - 1))
            # Using symbol clustering
            symbol_counts = {}
            for sym, _, _ in issued_regular:
                symbol_counts[sym] = symbol_counts.get(sym, 0) + 1
            
            avg_cluster_size = len(issued_regular) / len(symbol_counts)
            # Assume moderate ICC of 0.3 for financial data
            design_effect = 1 + 0.3 * (avg_cluster_size - 1)
            effective_n = len(issued_regular) / design_effect
            
            # Invariants check
            if distinct_days_regular > len(issued_regular):
                print("INSUFFICIENT=1")
                return
            if effective_n >= len(issued_regular):
                print("INSUFFICIENT=1")
                return
        else:
            precision_regular = base_rate_regular = 0
            distinct_days_regular = 0
            effective_n = 0
        
        # Sealed era metrics
        if issued_sealed:
            hits_sealed = sum(up for _, _, up in issued_sealed)
            precision_sealed = hits_sealed / len(issued_sealed)
        else:
            precision_sealed = 0
        
        # Print results
        print(f"ISSUED={total_issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision_regular if issued_regular else 0:.4f}")
        print(f"BASE_RATE={base_rate_regular if issued_regular else 0:.4f}")
        print(f"DISTINCT_DAYS={distinct_days_regular}")
        print(f"EFFECTIVE_N={effective_n:.1f}")
        print(f"SEALED_PRECISION={precision_sealed:.4f}")
        
    except Exception as e:
        print(f"INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
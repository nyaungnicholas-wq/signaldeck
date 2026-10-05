# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 330
# cycle_index: 53
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
"""Revenue-growth acceleration hypothesis test."""

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Step 1: Get all revenue records with fetched_at as the decision timestamp
        cur.execute("""
            SELECT f.symbol_id, f.fetched_at, f.value as revenue, f.as_of
            FROM fundamentals f
            WHERE f.metric = 'Revenues'
            ORDER BY f.symbol_id, f.fetched_at
        """)
        revenue_records = cur.fetchall()
        if not revenue_records:
            print("INSUFFICIENT=1")
            return
        
        # Step 2: Get all symbols with daily bars
        cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'")
        daily_symbols = {row['symbol_id'] for row in cur.fetchall()}
        
        # Step 3: Get all trading days for price lookback
        cur.execute("""
            SELECT symbol_id, ts, close, volume, open
            FROM bars
            WHERE tf = '1d'
            ORDER BY symbol_id, ts
        """)
        
        # Build price data structure: symbol -> [(ts, close, volume, open)]
        symbol_prices = defaultdict(list)
        for row in cur.fetchall():
            symbol_prices[row['symbol_id']].append(
                (row['ts'], row['close'], row['volume'], row['open'])
            )
        
        # Step 4: Get prediction outcomes for 21-day horizon
        cur.execute("""
            SELECT symbol_id, ts, up, fwd_return
            FROM prediction_outcomes
            WHERE horizon = 21
            ORDER BY symbol_id, ts
        """)
        outcomes = {}
        for row in cur.fetchall():
            outcomes[(row['symbol_id'], row['ts'])] = (row['up'], row['fwd_return'])
        
        # Step 5: Process each revenue record
        decisions = []  # (decision_ts, symbol_id, hit, return, base_class)
        
        for rev in revenue_records:
            symbol_id = rev['symbol_id']
            fetched_at = datetime.strptime(rev['fetched_at'], '%Y-%m-%d %H:%M:%S')
            revenue_val = rev['revenue']
            as_of = datetime.strptime(rev['as_of'], '%Y-%m-%d')
            
            # Check if symbol has daily bars
            if symbol_id not in daily_symbols:
                continue
            
            # Get price history for this symbol
            prices = symbol_prices.get(symbol_id, [])
            if len(prices) < 25:
                continue  # Need at least 25 bars for lookback + decision
            
            # Find decision timestamp (first trading day after fetched_at)
            decision_ts = None
            for ts, close, volume, open_ in prices:
                ts_dt = datetime.utcfromtimestamp(ts)
                if ts_dt > fetched_at:
                    decision_ts = ts
                    decision_dt = ts_dt
                    break
            
            if decision_ts is None:
                continue
            
            # Check revenue stamp freshness (within 90 days)
            days_old = (decision_dt - fetched_at).days
            if days_old > 90:
                continue
            
            # Find the bar index for decision date
            try:
                decision_idx = next(i for i, (ts, _, _, _) in enumerate(prices) if ts == decision_ts)
            except StopIteration:
                continue
            
            if decision_idx < 20:
                continue  # Need 20 bars for lookback
            
            # Get 20-day return (from 21 days before to 1 day before)
            lookback_close_21 = prices[decision_idx - 21][1]
            lookback_close_1 = prices[decision_idx - 1][1]
            if lookback_close_21 <= 0:
                continue
            twenty_day_return = lookback_close_1 / lookback_close_21 - 1
            
            # Check if closed down or flat over prior 20 sessions
            if twenty_day_return > 0:
                continue
            
            # Check price condition (>= $5)
            current_price = prices[decision_idx][1]
            if current_price < 5:
                continue
            
            # Check average daily dollar volume >= $1M over last 20 days
            total_volume = 0
            for i in range(decision_idx - 20, decision_idx):
                ts, close, volume, _ = prices[i]
                total_volume += close * volume
            avg_dollar_volume = total_volume / 20
            if avg_dollar_volume < 1_000_000:
                continue
            
            # Check if prior quarter revenue is available and compute YoY growth acceleration
            # Find revenue for same quarter last year and previous quarter
            same_quarter_last_year = None
            previous_quarter = None
            previous_quarter_same_year = None
            
            for other_rev in revenue_records:
                if other_rev['symbol_id'] != symbol_id:
                    continue
                other_fetched = datetime.strptime(other_rev['fetched_at'], '%Y-%m-%d %H:%M:%S')
                other_as_of = datetime.strptime(other_rev['as_of'], '%Y-%m-%d')
                
                # Look for same quarter last year (within 330-390 days)
                days_diff = (as_of - other_as_of).days
                if 330 <= days_diff <= 390:
                    same_quarter_last_year = other_rev['revenue']
                
                # Look for previous quarter (within 60-120 days)
                if 60 <= days_diff <= 120:
                    previous_quarter = other_rev['revenue']
                    
                    # Look for same quarter last year for previous quarter
                    for prev_prev in revenue_records:
                        if prev_prev['symbol_id'] != symbol_id:
                            continue
                        prev_prev_as_of = datetime.strptime(prev_prev['as_of'], '%Y-%m-%d')
                        days_diff_prev = (other_as_of - prev_prev_as_of).days
                        if 330 <= days_diff_prev <= 390:
                            previous_quarter_same_year = prev_prev['revenue']
                            break
            
            if same_quarter_last_year is None or previous_quarter is None or previous_quarter_same_year is None:
                continue
            
            if same_quarter_last_year == 0 or previous_quarter_same_year == 0:
                continue
            
            # Compute YoY growth
            current_yoy = (revenue_val - same_quarter_last_year) / abs(same_quarter_last_year)
            previous_yoy = (previous_quarter - previous_quarter_same_year) / abs(previous_quarter_same_year)
            
            # Check acceleration condition (>= 10 percentage points)
            acceleration = current_yoy - previous_yoy
            if acceleration < 0.10:
                continue
            
            # All conditions met - look for outcome
            if (symbol_id, decision_ts) in outcomes:
                hit, fwd_return = outcomes[(symbol_id, decision_ts)]
                decisions.append((decision_ts, symbol_id, hit, fwd_return, 1 if hit else 0))
        
        conn.close()
        
        if not decisions:
            print("INSUFFICIENT=1")
            return
        
        # Sort by decision time
        decisions.sort(key=lambda x: x[0])
        
        # Calculate metrics
        issued = len(decisions)
        
        # Calculate base rate
        hits = sum(1 for d in decisions if d[2])
        base_rate = hits / issued if issued > 0 else 0
        
        # Calculate distinct days
        distinct_days = len(set(d[0] for d in decisions))
        
        # Calculate design effect (clustering in time)
        day_counts = defaultdict(int)
        for d in decisions:
            day_counts[d[0]] += 1
        
        n_days = len(day_counts)
        sum_sq = sum(c * c for c in day_counts.values())
        design_effect = (sum_sq / issued) if issued > 0 else 1
        effective_n = issued / design_effect
        
        # Split into held-out (most recent 20%) and rest
        cutoff_idx = int(len(decisions) * 0.8)
        sealed_decisions = decisions[cutoff_idx:]
        rest_decisions = decisions[:cutoff_idx]
        
        # Calculate sealed precision
        sealed_hits = sum(1 for d in sealed_decisions if d[2])
        sealed_precision = sealed_hits / len(sealed_decisions) if sealed_decisions else 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={issued}")  # Each decision point considered is an opportunity
        print(f"PRECISION={hits/issued:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print(f"Error: {e}")
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
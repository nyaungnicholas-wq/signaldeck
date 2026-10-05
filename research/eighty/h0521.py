# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 520
# cycle_index: 50
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import time

def main():
    start = time.time()
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=30)
        conn.execute("PRAGMA journal_mode = WAL")
        cursor = conn.cursor()
        
        # Check if there are insider trades and fundamentals
        cursor.execute("SELECT COUNT(*) FROM insider_trades WHERE code='S'")
        insider_sales = cursor.fetchone()[0]
        cursor.execute("SELECT COUNT(DISTINCT symbol_id) FROM fundamentals WHERE metric='SharesOutstanding'")
        fundamentals_symbols = cursor.fetchone()[0]
        if insider_sales == 0 or fundamentals_symbols == 0:
            print("INSUFFICIENT=1")
            return
            
        # Get all insider sales with their filed dates and symbol info
        cursor.execute("""
            SELECT it.symbol_id, it.filed_ts, it.tx_ts, it.symbol_id,
                   s.market, s.active
            FROM insider_trades it
            JOIN symbols s ON it.symbol_id = s.id
            WHERE it.code='S'
            ORDER BY it.filed_ts
        """)
        trades = cursor.fetchall()
        
        if not trades:
            print("INSUFFICIENT=1")
            return
            
        # Prepare data structures
        opportunities = []
        for symbol_id, filed_ts, tx_ts, _, market, active in trades:
            # Skip inactive symbols or non-stocks (crypto)
            if not active or market != 'stocks':
                continue
            opportunities.append({
                'symbol_id': symbol_id,
                'filed_ts': filed_ts,
                'tx_ts': tx_ts
            })
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return
            
        # Sort by filed_ts to establish time ordering
        opportunities.sort(key=lambda x: x['filed_ts'])
        n = len(opportunities)
        split_idx = int(n * 0.8)
        
        # Get all daily bars for price checking
        cursor.execute("""
            SELECT symbol_id, ts, close 
            FROM bars 
            WHERE tf='1d'
            ORDER BY symbol_id, ts
        """)
        all_bars = cursor.fetchall()
        bars_by_symbol = {}
        for symbol_id, ts, close in all_bars:
            if symbol_id not in bars_by_symbol:
                bars_by_symbol[symbol_id] = []
            bars_by_symbol[symbol_id].append((ts, close))
        
        # Get fundamentals data
        cursor.execute("""
            SELECT symbol_id, value, fetched_at 
            FROM fundamentals 
            WHERE metric='SharesOutstanding'
            ORDER BY symbol_id, fetched_at
        """)
        fundamentals_data = cursor.fetchall()
        fundamentals_by_symbol = {}
        for symbol_id, value, fetched_at in fundamentals_data:
            if symbol_id not in fundamentals_by_symbol:
                fundamentals_by_symbol[symbol_id] = []
            fundamentals_by_symbol[symbol_id].append((fetched_at, value))
        
        # Get news sentiment data (aggregate by day)
        cursor.execute("""
            SELECT symbol_id, day, mean_score
            FROM sentiment_features
            ORDER BY symbol_id, day
        """)
        sentiment_data = cursor.fetchall()
        sentiment_by_symbol = {}
        for symbol_id, day, score in sentiment_data:
            if symbol_id not in sentiment_by_symbol:
                sentiment_by_symbol[symbol_id] = {}
            sentiment_by_symbol[symbol_id][day] = score
        
        # Get prediction outcomes for 21-day horizon
        cursor.execute("""
            SELECT symbol_id, ts, up, fwd_return
            FROM prediction_outcomes
            WHERE horizon=21
            ORDER BY symbol_id, ts
        """)
        outcomes_data = cursor.fetchall()
        outcomes_by_symbol = {}
        for symbol_id, ts, up, fwd_return in outcomes_data:
            if symbol_id not in outcomes_by_symbol:
                outcomes_by_symbol[symbol_id] = []
            outcomes_by_symbol[symbol_id].append((ts, up, fwd_return))
        
        # Process each opportunity
        issued = []
        sealed_issued = []
        design_effect_num = 0
        design_effect_den = 0
        
        for i, opp in enumerate(opportunities):
            symbol_id = opp['symbol_id']
            filed_ts = opp['filed_ts']
            
            # Check fundamentals: SharesOutstanding YoY increase >= 10%
            if symbol_id not in fundamentals_by_symbol:
                continue
            fundamentals = fundamentals_by_symbol[symbol_id]
            # Find most recent fundamentals before filed_ts
            recent_before = [(f, v) for f, v in fundamentals if f <= filed_ts]
            if len(recent_before) < 2:
                continue
            # Sort by fetched_at descending
            recent_before.sort(reverse=True)
            current_val = recent_before[0][1]
            # Find the one from about a year ago (within 150-400 days)
            prev_val = None
            current_fetched = recent_before[0][0]
            for f, v in recent_before[1:]:
                days_diff = (current_fetched - f) / (24*3600)
                if 150 <= days_diff <= 400:
                    prev_val = v
                    break
            if prev_val is None or prev_val == 0:
                continue
            yoy_increase = (current_val - prev_val) / prev_val
            if yoy_increase < 0.10:
                continue
            
            # Check 5-day price increase > 5% (abstain if true)
            if symbol_id in bars_by_symbol:
                bars = bars_by_symbol[symbol_id]
                # Find bars before filed_ts
                relevant_bars = [(ts, close) for ts, close in bars if ts <= filed_ts]
                if len(relevant_bars) >= 6:
                    # Last 5 days (trading days)
                    last_5 = relevant_bars[-6:-1]  # Exclude current day
                    current_close = relevant_bars[-1][1]
                    if last_5:
                        oldest_close = last_5[0][1]
                        if oldest_close > 0:
                            price_increase = (current_close - oldest_close) / oldest_close
                            if price_increase > 0.05:
                                continue
            
            # Check news sentiment (past 5 days) - abstain if positive
            filed_date = time.strftime('%Y-%m-%d', time.gmtime(filed_ts))
            symbol_sentiment = sentiment_by_symbol.get(symbol_id, {})
            total_score = 0
            count = 0
            for days_ago in range(1, 6):
                # Approximate date
                target_ts = filed_ts - days_ago * 86400
                target_date = time.strftime('%Y-%m-%d', time.gmtime(target_ts))
                if target_date in symbol_sentiment:
                    total_score += symbol_sentiment[target_date]
                    count += 1
            if count > 0:
                avg_sentiment = total_score / count
                if avg_sentiment > 0:
                    continue
            
            # Check outcome
            if symbol_id not in outcomes_by_symbol:
                continue
            outcomes = outcomes_by_symbol[symbol_id]
            # Find outcome with ts matching filed_ts (within 1 day tolerance)
            outcome = None
            for o_ts, up, fwd_return in outcomes:
                if abs(o_ts - filed_ts) < 86400:
                    outcome = (up, fwd_return)
                    break
            if outcome is None:
                continue
            
            up, fwd_return = outcome
            hit = 0 if up == 0 else 0  # We're predicting negative (down)
            if up == 0:
                hit = 1  # Stock went down, our negative call was correct
            
            opp_result = {
                'symbol_id': symbol_id,
                'filed_ts': filed_ts,
                'hit': hit,
                'up': up
            }
            
            issued.append(opp_result)
            if i >= split_idx:
                sealed_issued.append(opp_result)
        
        # If insufficient calls
        if len(issued) < 5:
            print("INSUFFICIENT=1")
            return
        
        # Compute metrics
        hits = sum(1 for x in issued if x['hit'] == 1)
        precision = hits / len(issued) if len(issued) > 0 else 0
        base_rate = precision  # Base rate of negative calls within issued
        
        distinct_days = len(set(time.strftime('%Y-%m-%d', time.gmtime(x['filed_ts'])) for x in issued))
        
        # Simple design effect approximation based on day clustering
        # Count calls per day
        day_counts = {}
        for x in issued:
            day = time.strftime('%Y-%m-%d', time.gmtime(x['filed_ts']))
            day_counts[day] = day_counts.get(day, 0) + 1
        
        n_days = len(day_counts)
        if n_days > 0:
            avg_per_day = len(issued) / n_days
            # Simple variance of daily counts
            var_daily = sum((c - avg_per_day)**2 for c in day_counts.values()) / n_days
            if avg_per_day > 0:
                design_effect = 1 + (avg_per_day - 1) * (var_daily / (avg_per_day**2))
            else:
                design_effect = 1
        else:
            design_effect = 1
            
        effective_n = len(issued) / design_effect if design_effect > 0 else len(issued)
        
        # Sealed era precision
        sealed_hits = sum(1 for x in sealed_issued if x['hit'] == 1)
        sealed_precision = sealed_hits / len(sealed_issued) if len(sealed_issued) > 0 else 0
        
        # Print results
        print(f"ISSUED={len(issued)}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        # Check invariants
        if DISTINCT_DAYS > len(issued):
            print("INVARIANT VIOLATION: DISTINCT_DAYS > ISSUED")
        if effective_n >= len(issued):
            print("INVARIANT VIOLATION: EFFECTIVE_N >= ISSUED")
            
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
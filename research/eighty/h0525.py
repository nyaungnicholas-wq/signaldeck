# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 524
# cycle_index: 54
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Check for sufficient data
        cur.execute("SELECT COUNT(*) FROM insider_trades WHERE code='P'")
        insider_count = cur.fetchone()[0]
        if insider_count == 0:
            print("INSUFFICIENT=1")
            return
            
        cur.execute("SELECT COUNT(*) FROM fundamentals WHERE metric='SharesOutstanding'")
        fundamental_count = cur.fetchone()[0]
        if fundamental_count == 0:
            print("INSUFFICIENT=1")
            return
            
        cur.execute("SELECT COUNT(*) FROM news")
        news_count = cur.fetchone()[0]
        if news_count == 0:
            print("INSUFFICIENT=1")
            return
            
        cur.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE horizon=21")
        label_count = cur.fetchone()[0]
        if label_count == 0:
            print("INSUFFICIENT=1")
            return
        
        # Get all insider purchases with their disclosure dates
        cur.execute("""
            SELECT symbol_id, filed_ts, date(filed_ts, 'unixepoch') as disclosure_day
            FROM insider_trades 
            WHERE code='P'
        """)
        purchases = cur.fetchall()
        
        if not purchases:
            print("INSUFFICIENT=1")
            return
        
        # Get all quarterly SharesOutstanding data
        cur.execute("""
            SELECT symbol_id, value, fetched_at, as_of
            FROM fundamentals 
            WHERE metric='SharesOutstanding'
            ORDER BY symbol_id, as_of
        """)
        shares_data = cur.fetchall()
        
        # Index shares data by symbol and time
        shares_by_symbol = defaultdict(list)
        for symbol_id, value, fetched_at, as_of in shares_data:
            shares_by_symbol[symbol_id].append((as_of, fetched_at, value))
        
        # For each symbol, sort by as_of and compute consecutive quarters
        quarterly_declines = defaultdict(list)
        for symbol_id, quarters in shares_by_symbol.items():
            quarters.sort(key=lambda x: x[0])  # Sort by as_of
            for i in range(1, len(quarters)):
                prev_as_of, prev_fetched, prev_val = quarters[i-1]
                curr_as_of, curr_fetched, curr_val = quarters[i]
                if prev_val is not None and curr_val is not None:
                    if curr_val < prev_val:
                        # Record the quarter where decline is observable (using fetched_at as availability)
                        quarterly_declines[symbol_id].append((curr_fetched, curr_as_of, curr_val, prev_val))
        
        # Get all news sentiment data (we'll compute moving average per symbol)
        cur.execute("""
            SELECT symbol_id, ts, score
            FROM news 
            WHERE score IS NOT NULL
            ORDER BY symbol_id, ts
        """)
        news_data = cur.fetchall()
        
        # Index news by symbol and timestamp
        news_by_symbol = defaultdict(list)
        for symbol_id, ts, score in news_data:
            news_by_symbol[symbol_id].append((ts, score))
        
        # Process each potential decision point
        calls = []
        opportunities = 0
        
        # For efficiency, precompute the 20-day moving average for each symbol's news
        # We'll compute this on-the-fly for each purchase date
        for symbol_id, filed_ts, disclosure_day in purchases:
            opportunities += 1
            
            # Check condition 2: SharesOutstanding decline
            has_decline = False
            if symbol_id in quarterly_declines:
                for avail_date, as_of, curr_val, prev_val in quarterly_declines[symbol_id]:
                    # The decline must be observable before disclosure (filed_ts)
                    if avail_date <= filed_ts:
                        has_decline = True
                        break
            
            if not has_decline:
                continue
            
            # Check condition 3: Positive news sentiment above 20-day moving average
            if symbol_id not in news_by_symbol:
                continue
            
            # Get all news up to and including disclosure day
            symbol_news = news_by_symbol[symbol_id]
            up_to_disclosure = [(ts, score) for ts, score in symbol_news if ts <= filed_ts]
            
            if len(up_to_disclosure) < 20:
                continue  # Need at least 20 days of history
            
            # Calculate 20-day moving average (by timestamp, not trading days)
            # Sort by timestamp descending to get recent 20
            sorted_news = sorted(up_to_disclosure, key=lambda x: x[0], reverse=True)
            recent_20 = sorted_news[:20]
            avg_score = sum(score for _, score in recent_20) / len(recent_20)
            
            # Get today's score (most recent news on disclosure day)
            today_news = [(ts, score) for ts, score in up_to_disclosure if ts == filed_ts]
            if not today_news:
                continue
            
            today_score = today_news[0][1]  # Take the first if multiple
            
            if today_score <= avg_score:
                continue  # Not positive enough
            
            # All conditions met, issue BUY call
            calls.append((symbol_id, filed_ts, disclosure_day))
        
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Sort calls by timestamp for time-based split
        calls.sort(key=lambda x: x[1])
        
        # Split into held-out (80%) and sealed era (20%)
        split_idx = int(len(calls) * 0.8)
        held_out_calls = calls[:split_idx]
        sealed_calls = calls[split_idx:]
        
        # Function to compute metrics for a set of calls
        def compute_metrics(call_list):
            if not call_list:
                return 0, 0, 0, 0, 0, 0, 0
            
            hits = 0
            up_count = 0
            days_set = set()
            
            for symbol_id, decision_ts, disclosure_day in call_list:
                days_set.add(disclosure_day)
                
                # Look up label
                cur.execute("""
                    SELECT up 
                    FROM prediction_outcomes 
                    WHERE symbol_id=? AND horizon=21 AND ts=? AND resolved_at IS NOT NULL
                """, (symbol_id, decision_ts))
                
                row = cur.fetchone()
                if row is None:
                    continue  # Skip if no label available
                    
                up = row[0]
                if up is not None:
                    if up:
                        hits += 1
                    up_count += 1
            
            issued = len(call_list)
            opportunities = len(call_list)  # All considered calls are opportunities
            precision = hits / issued if issued > 0 else 0
            base_rate = up_count / issued if issued > 0 else 0
            distinct_days = len(days_set)
            
            # Compute design effect using day clustering
            # Group calls by day
            day_groups = defaultdict(list)
            for symbol_id, decision_ts, disclosure_day in call_list:
                day_groups[disclosure_day].append(1)  # Just count
            
            # Compute ICC (intra-class correlation)
            # Simple approach: if all days have same number of calls, DEFF = 1 + (m-1)*ICC
            # We'll compute variance between days and within days
            total_var = 0
            within_var = 0
            between_var = 0
            
            # Overall mean (should be close to 1 since all entries are 1)
            overall_mean = 1.0
            n = issued
            
            # Between-group variance
            day_means = []
            for day, group in day_groups.items():
                day_mean = sum(group) / len(group)
                day_means.append((day_mean, len(group)))
            
            for day_mean, size in day_means:
                between_var += size * (day_mean - overall_mean) ** 2
            
            between_var /= n
            
            # Within-group variance
            for day, group in day_groups.items():
                day_mean = sum(group) / len(group)
                for val in group:
                    within_var += (val - day_mean) ** 2
            
            within_var /= n
            
            # Total variance should be close to 0 since all values are 1
            # Actually total variance should be 0, but we compute anyway
            total_var = between_var + within_var
            
            if total_var == 0:
                # All calls are identical (all 1), perfect clustering
                # Use simple average cluster size
                avg_cluster = n / max(1, len(day_groups))
                deff = avg_cluster  # Assuming perfect correlation
            else:
                icc = between_var / total_var if total_var > 0 else 0
                avg_cluster = n / max(1, len(day_groups))
                deff = 1 + (avg_cluster - 1) * icc
            
            effective_n = n / deff if deff > 0 else n
            
            return issued, precision, base_rate, distinct_days, effective_n, hits, up_count
        
        # Compute metrics for held-out and sealed
        held_out_issued, held_out_precision, held_out_base_rate, held_out_days, held_out_eff_n, _, _ = compute_metrics(held_out_calls)
        sealed_issued, sealed_precision, sealed_base_rate, sealed_days, sealed_eff_n, _, _ = compute_metrics(sealed_calls)
        
        # Total metrics
        total_issued, total_precision, total_base_rate, total_days, total_eff_n, total_hits, total_up = compute_metrics(calls)
        
        # Output results
        print(f"ISSUED={total_issued}")
        print(f"OPPORTUNITIES={len(calls)}")
        print(f"PRECISION={total_precision:.4f}")
        print(f"BASE_RATE={total_base_rate:.4f}")
        print(f"DISTINCT_DAYS={total_days}")
        print(f"EFFECTIVE_N={total_eff_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print(f"ERROR: {e}")
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
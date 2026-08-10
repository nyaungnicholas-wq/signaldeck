# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 414
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
from collections import defaultdict
from math import sqrt

def connect_db():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def get_dates_from_observations(cursor, query, params=()):
    cursor.execute(query, params)
    return [row[0] for row in cursor.fetchall()]

def compute_percentile(values, pct):
    if not values:
        return None
    sorted_vals = sorted(values)
    idx = int(pct * (len(sorted_vals) - 1))
    return sorted_vals[idx]

def main():
    try:
        conn = connect_db()
        cur = conn.cursor()
        
        # Check for insider purchase data
        cur.execute("SELECT COUNT(*) FROM insider_trades WHERE code = 'P'")
        if cur.fetchone()[0] == 0:
            print("INSUFFICIENT=1")
            return
            
        # Check for StockTwits data
        cur.execute("SELECT COUNT(*) FROM stocktwits_sentiment")
        if cur.fetchone()[0] == 0:
            print("INSUFFICIENT=1")
            return
            
        # Get all unique (symbol, day) pairs from insider purchases
        cur.execute("""
            SELECT symbol_id, date(filed_ts, 'unixepoch') as day
            FROM insider_trades 
            WHERE code = 'P'
            GROUP BY symbol_id, date(filed_ts, 'unixepoch')
        """)
        all_opportunities = cur.fetchall()
        OPPORTUNITIES = len(all_opportunities)
        
        if OPPORTUNITIES == 0:
            print("INSUFFICIENT=1")
            return
            
        # Get recent 20% cutoff
        cur.execute("""
            SELECT date(filed_ts, 'unixepoch') as day
            FROM insider_trades 
            WHERE code = 'P'
            ORDER BY filed_ts DESC
        """)
        all_days = [row[0] for row in cur.fetchall()]
        unique_days = sorted(set(all_days))
        cutoff_idx = int(len(unique_days) * 0.8)
        cutoff_day = unique_days[cutoff_idx] if cutoff_idx < len(unique_days) else unique_days[-1]
        
        calls = []
        calls_sealed = []
        
        # Process each opportunity
        for symbol_id, day in all_opportunities:
            # Check StockTwits data for this symbol
            cur.execute("""
                SELECT date(ts, 'unixepoch') as day, bullish, bearish
                FROM stocktwits_sentiment
                WHERE symbol_id = ?
                ORDER BY ts
            """, (symbol_id,))
            sentiment_data = cur.fetchall()
            
            if not sentiment_data:
                continue
                
            # Build 252-day rolling history of bearish/bullish ratio
            ratios_by_day = defaultdict(list)
            for sday, bullish, bearish in sentiment_data:
                if bullish > 0:
                    ratios_by_day[sday].append(bearish / bullish)
                else:
                    ratios_by_day[sday].append(float('inf'))
            
            # Get ratio for the disclosure day
            if day not in ratios_by_day:
                continue
            day_ratio = ratios_by_day[day][0]
            
            # Get 252-day historical ratios
            hist_ratios = []
            for sday, rlist in ratios_by_day.items():
                if sday <= day:
                    hist_ratios.extend(rlist)
            
            if len(hist_ratios) < 100:  # Need reasonable history
                continue
                
            threshold_95 = compute_percentile(hist_ratios, 0.95)
            if day_ratio <= threshold_95:
                continue  # Entry condition not met
            
            # Get 21-day forward return from prediction_outcomes
            cur.execute("""
                SELECT up, fwd_return
                FROM prediction_outcomes
                WHERE symbol_id = ? 
                AND date(ts, 'unixepoch') = ?
                AND horizon = 21
            """, (symbol_id, day))
            outcome = cur.fetchone()
            
            if not outcome:
                continue
                
            up, fwd_return = outcome
            if day >= cutoff_day:
                calls_sealed.append((symbol_id, day, up, fwd_return))
            else:
                calls.append((symbol_id, day, up, fwd_return))
        
        all_calls = calls + calls_sealed
        ISSUED = len(all_calls)
        
        if ISSUED == 0:
            print("INSUFFICIENT=1")
            return
            
        # Calculate metrics
        hits = sum(1 for c in all_calls if c[2])
        PRECISION = hits / ISSUED if ISSUED > 0 else 0
        BASE_RATE = hits / ISSUED  # Base rate within issued subset
        
        # Distinct days
        days = [c[1] for c in all_calls]
        distinct_days = len(set(days))
        DISTINCT_DAYS = distinct_days
        
        # Effective sample size
        day_counts = defaultdict(int)
        for d in days:
            day_counts[d] += 1
        k = len(day_counts)
        N = ISSUED
        if k > 1:
            sum_sq = sum(cnt**2 for cnt in day_counts.values())
            DEFF = (N - 1) / (k - 1) * (sum_sq / (N**2))
            EFFECTIVE_N = N / DEFF
        else:
            EFFECTIVE_N = 1  # Only one day, no effective sample
        
        # Sealed precision
        sealed_hits = sum(1 for c in calls_sealed if c[2])
        SEALED_PRECISION = sealed_hits / len(calls_sealed) if calls_sealed else 0
        
        # Print results
        print(f"ISSUED={ISSUED}")
        print(f"OPPORTUNITIES={OPPORTUNITIES}")
        print(f"PRECISION={PRECISION:.4f}")
        print(f"BASE_RATE={BASE_RATE:.4f}")
        print(f"DISTINCT_DAYS={DISTINCT_DAYS}")
        print(f"EFFECTIVE_N={EFFECTIVE_N:.2f}")
        print(f"SEALED_PRECISION={SEALED_PRECISION:.4f}")
        
        # Validate invariants
        if DISTINCT_DAYS > ISSUED:
            print("INVARIANT_VIOLATED: DISTINCT_DAYS > ISSUED")
        if EFFECTIVE_N >= ISSUED:
            print("INVARIANT_VIOLATED: EFFECTIVE_N >= ISSUED")
            
    except Exception as e:
        print(f"ERROR: {e}")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
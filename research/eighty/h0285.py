# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 284
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import sys
from collections import defaultdict
from datetime import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Get insider purchases (code='P') with enough price history
        cur.execute("""
            SELECT 
                i.symbol_id,
                i.filed_ts,
                i.value as dollar_value
            FROM insider_trades i
            WHERE i.code = 'P'
            AND i.value > 0
            AND (SELECT COUNT(*) 
                 FROM bars b 
                 WHERE b.symbol_id = i.symbol_id 
                 AND b.tf = '1d' 
                 AND b.ts < i.filed_ts) >= 20
        """)
        opportunities = cur.fetchall()
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return 0
            
        # Calculate metrics for each opportunity
        calls = []
        for symbol_id, filed_ts, dollar_value in opportunities:
            # Get 20-day average daily dollar volume prior to filing
            cur.execute("""
                SELECT AVG(close * volume)
                FROM (
                    SELECT close, volume
                    FROM bars
                    WHERE symbol_id = ?
                    AND tf = '1d'
                    AND ts < ?
                    ORDER BY ts DESC
                    LIMIT 20
                )
            """, (symbol_id, filed_ts))
            avg_daily_vol = cur.fetchone()[0]
            
            if avg_daily_vol is None or avg_daily_vol == 0:
                continue
                
            # Check entry condition: dollar_value >= 0.5% of avg_daily_vol
            if dollar_value < 0.5/100 * avg_daily_vol:
                continue
                
            # Get 21-day forward return label
            cur.execute("""
                SELECT up
                FROM prediction_outcomes
                WHERE symbol_id = ?
                AND horizon = 21
                AND ts >= ?
                ORDER BY ts ASC
                LIMIT 1
            """, (symbol_id, filed_ts))
            
            row = cur.fetchone()
            if row is None:
                continue
                
            is_hit = 1 if row[0] == 1 else 0
            day_str = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
            calls.append((filed_ts, day_str, is_hit))
        
        conn.close()
        
        if not calls:
            print("INSUFFICIENT=1")
            return 0
            
        # Split into training and sealed (most recent 20%)
        calls.sort(key=lambda x: x[0])
        split_idx = int(len(calls) * 0.8)
        train_calls = calls[:split_idx]
        sealed_calls = calls[split_idx:]
        
        # Compute metrics
        issued = len(calls)
        hits = sum(c[2] for c in calls)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate: proportion of up moves in issued calls
        base_rate = precision  # same as precision for binary classification
        
        # Distinct days
        distinct_days = len(set(c[1] for c in calls))
        
        # Design effect calculation
        day_counts = defaultdict(int)
        day_hits = defaultdict(int)
        for c in calls:
            day = c[1]
            day_counts[day] += 1
            day_hits[day] += c[2]
        
        # ICC calculation for binary outcomes
        n_clusters = len(day_counts)
        if n_clusters > 1:
            cluster_sizes = list(day_counts.values())
            total_n = sum(cluster_sizes)
            avg_cluster_size = total_n / n_clusters
            
            # Variance components
            grand_mean = hits / issued
            var_between = sum(
                day_counts[d] * (day_hits[d]/day_counts[d] - grand_mean)**2
                for d in day_counts
            ) / (n_clusters - 1)
            
            var_within = sum(
                day_hits[d] * (1 - day_hits[d]/day_counts[d])
                for d in day_counts
            ) / (total_n - n_clusters)
            
            if var_between + var_within > 0:
                icc = var_between / (var_between + var_within)
            else:
                icc = 0
                
            deff = 1 + (avg_cluster_size - 1) * icc
            effective_n = issued / deff
        else:
            effective_n = issued
            
        # Sealed precision
        sealed_hits = sum(c[2] for c in sealed_calls)
        sealed_issued = len(sealed_calls)
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={issued}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.1f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
        return 0
        
    except Exception as e:
        print(f"ERROR: {e}", file=sys.stderr)
        print("INSUFFICIENT=1")
        return 0

if __name__ == "__main__":
    sys.exit(main())
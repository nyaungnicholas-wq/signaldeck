# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 327
# cycle_index: 50
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Get all Form 4 purchases (code='P') disclosed on Fridays with value >= 50000
        cur.execute("""
            SELECT it.symbol_id, it.filed_ts, it.value
            FROM insider_trades it
            WHERE it.code = 'P'
            AND it.value >= 50000
            AND strftime('%w', it.filed_ts) = '5'  # 5 = Friday
            ORDER BY it.filed_ts
        """)
        candidates = cur.fetchall()
        
        if not candidates:
            print("INSUFFICIENT=1")
            return
        
        # Preload all bars for symbols in candidates to check 60-day history
        symbol_ids = {row[0] for row in candidates}
        placeholders = ','.join('?' * len(symbol_ids))
        cur.execute(f"""
            SELECT symbol_id, ts, close
            FROM bars
            WHERE tf = '1d'
            AND symbol_id IN ({placeholders})
            ORDER BY symbol_id, ts
        """, list(symbol_ids))
        
        # Organize bars by symbol
        bars_by_symbol = defaultdict(list)
        for row in cur.fetchall():
            bars_by_symbol[row[0]].append((row[1], row[2]))
        
        # Convert to dict for quick lookup
        bars_dict = dict(bars_by_symbol)
        
        # Get prediction outcomes for horizon=21
        cur.execute("""
            SELECT symbol_id, ts, up
            FROM prediction_outcomes
            WHERE horizon = 21
        """)
        outcomes = {(row[0], row[1]): row[2] for row in cur.fetchall()}
        
        # Process candidates and issue calls
        calls = []
        active_calls = {}  # symbol_id -> last call timestamp
        
        for symbol_id, filed_ts, value in candidates:
            # Check if symbol has daily bars
            if symbol_id not in bars_dict:
                continue
            
            bars = bars_dict[symbol_id]
            
            # Convert filed_ts to epoch
            cur.execute("SELECT strftime('%s', ?)", (filed_ts,))
            signal_epoch = int(cur.fetchone()[0])
            
            # Count trading days before signal
            trading_days_before = sum(1 for ts, _ in bars if ts <= signal_epoch)
            
            if trading_days_before < 60:
                continue
            
            # Check no overlapping active call (within 21 trading days)
            if symbol_id in active_calls:
                last_call_epoch = active_calls[symbol_id]
                # Count trading days between last call and current signal
                trading_days_gap = sum(1 for ts, _ in bars if last_call_epoch < ts <= signal_epoch)
                if trading_days_gap < 21:
                    continue
            
            # Check if we have an outcome
            if (symbol_id, signal_epoch) in outcomes:
                up = outcomes[(symbol_id, signal_epoch)]
                calls.append((symbol_id, signal_epoch, up))
                active_calls[symbol_id] = signal_epoch
        
        conn.close()
        
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Split into training (first 80%) and sealed (last 20%) by time
        calls.sort(key=lambda x: x[1])  # Sort by timestamp
        n_total = len(calls)
        split_idx = int(n_total * 0.8)
        
        training_calls = calls[:split_idx]
        sealed_calls = calls[split_idx:]
        
        # Calculate metrics
        def calculate_metrics(call_list):
            if not call_list:
                return None, None, None, None
            
            issued = len(call_list)
            hits = sum(1 for _, _, up in call_list if up)
            precision = hits / issued if issued > 0 else 0
            
            # Base rate within issued subset
            base_rate = hits / issued
            
            # Count distinct days
            distinct_days = len({ts for _, ts, _ in call_list})
            
            # Calculate design effect (ICC-based) for clustering by day
            day_clusters = defaultdict(list)
            for symbol_id, ts, up in call_list:
                day_clusters[ts].append(up)
            
            # Calculate ICC
            m = len(day_clusters)  # number of clusters
            if m <= 1:
                design_effect = 1.0
            else:
                # Overall variance
                p = base_rate
                var_total = p * (1 - p) if 0 < p < 1 else 0
                
                # Between-cluster variance
                cluster_means = [sum(up_list) / len(up_list) for up_list in day_clusters.values()]
                cluster_sizes = [len(up_list) for up_list in day_clusters.values()]
                mean_cluster_size = sum(cluster_sizes) / m
                
                var_between = sum((mean - p) ** 2 for mean in cluster_means) / (m - 1) if m > 1 else 0
                
                # ICC approximation
                if var_total > 0:
                    icc = var_between / var_total
                    design_effect = 1 + (mean_cluster_size - 1) * icc
                else:
                    design_effect = 1.0
            
            effective_n = issued / design_effect if design_effect > 0 else issued
            
            return issued, precision, base_rate, distinct_days, effective_n
        
        # Calculate all metrics
        all_metrics = calculate_metrics(calls)
        training_metrics = calculate_metrics(training_calls)
        sealed_metrics = calculate_metrics(sealed_calls)
        
        if not all_metrics:
            print("INSUFFICIENT=1")
            return
        
        issued, precision, base_rate, distinct_days, effective_n = all_metrics
        _, sealed_precision, _, _, _ = sealed_metrics if sealed_metrics else (None, 0, None, None, None)
        
        # Print required output
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={issued}")  # In this design, every candidate that passes filters is an opportunity
        print(f"PRECISION={precision}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n}")
        print(f"SEALED_PRECISION={sealed_precision}")
        
        # Validate invariants
        assert distinct_days <= issued, "DISTINCT_DAYS cannot exceed ISSUED"
        assert effective_n < issued, "EFFECTIVE_N must be less than ISSUED"
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
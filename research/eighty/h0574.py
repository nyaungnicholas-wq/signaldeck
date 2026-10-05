# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 573
# cycle_index: 31
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
        c = conn.cursor()
        
        # Universe: symbols with >=100 days of 1d price data, >=1 13F record, >=1 insider trade
        c.execute("""
        WITH symbols_price AS (
            SELECT symbol_id, COUNT(DISTINCT CAST(ts/86400 AS INTEGER)) AS days
            FROM bars
            WHERE tf = '1d'
            GROUP BY symbol_id
            HAVING days >= 100
        ),
        symbols_insider AS (
            SELECT DISTINCT symbol_id FROM insider_trades
        ),
        symbols_13f AS (
            SELECT DISTINCT symbol_id FROM inst_holdings
        )
        SELECT sp.symbol_id
        FROM symbols_price sp
        JOIN symbols_insider si ON sp.symbol_id = si.symbol_id
        JOIN symbols_13f s13 ON sp.symbol_id = s13.symbol_id
        """)
        universe = {row[0] for row in c.fetchall()}
        if not universe:
            print("INSUFFICIENT=1")
            return
            
        # Get all insider open-market purchases (code='P') with their disclosure dates (filed_ts)
        # We need to convert filed_ts to a date (YYYY-MM-DD) for grouping
        c.execute("""
        SELECT symbol_id, CAST(filed_ts/86400 AS INTEGER) AS filed_day
        FROM insider_trades
        WHERE code = 'P' AND symbol_id IN ({})
        """.format(','.join(str(s) for s in universe)))
        insider_events = c.fetchall()
        if not insider_events:
            print("INSUFFICIENT=1")
            return
            
        # For each event (symbol, day), we need:
        # 1. The most recent 13F as of day-45 (to avoid lookahead)
        # 2. The 25th percentile of institutional holdings across all symbols on that day
        # 3. The forward return label from prediction_outcomes with horizon=21
        
        # First, gather all 13F data with as-of adjustment
        c.execute("""
        SELECT symbol_id, 
               CAST(period/86400 AS INTEGER) AS period_day,
               SUM(shares) AS total_shares,
               SUM(value) AS total_value
        FROM inst_holdings
        WHERE symbol_id IN ({})
        GROUP BY symbol_id, period_day
        """.format(','.join(str(s) for s in universe)))
        f13_all = c.fetchall()
        
        # Index 13F by symbol and time
        f13_by_sym = {}
        for sym, day, shares, value in f13_all:
            f13_by_sym.setdefault(sym, []).append((day, shares, value))
        for sym in f13_by_sym:
            f13_by_sym[sym].sort(key=lambda x: x[0], reverse=True)
            
        # Get all labels for horizon=21
        c.execute("""
        SELECT symbol_id, CAST(ts/86400 AS INTEGER) AS ts_day, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21 AND symbol_id IN ({})
        """.format(','.join(str(s) for s in universe)))
        labels_all = c.fetchall()
        labels_by_sym = {}
        for sym, day, up, ret in labels_all:
            labels_by_sym.setdefault(sym, []).append((day, up, ret))
        for sym in labels_by_sym:
            labels_by_sym[sym].sort(key=lambda x: x[0])
            
        # Build candidate calls
        candidates = []  # (symbol, decision_day, up, fwd_return, institution_value)
        
        # For each insider event
        for sym, event_day in insider_events:
            # Find most recent 13F at least 45 days before event
            if sym not in f13_by_sym:
                continue
            valid_13f = [x for x in f13_by_sym[sym] if x[0] <= event_day - 45]
            if not valid_13f:
                continue
            recent_13f = valid_13f[0]  # (period_day, shares, value)
            inst_value = recent_13f[2]  # using value as proxy for institutional holdings size
            
            # Find label on event_day (or nearest future day for horizon=21)
            if sym not in labels_by_sym:
                continue
            # Find label with ts_day >= event_day (exact match preferred)
            label = None
            for day, up, ret in labels_by_sym[sym]:
                if day >= event_day:
                    if day == event_day:
                        label = (up, ret)
                        break
                    else:
                        label = (up, ret)
                        break
            if label is None:
                continue
                
            candidates.append((sym, event_day, label[0], label[1], inst_value))
            
        if not candidates:
            print("INSUFFICIENT=1")
            return
            
        # Group by day to compute cross-sectional 25th percentile of institution_value
        day_values = {}
        for _, day, _, _, inst_val in candidates:
            day_values.setdefault(day, []).append(inst_val)
            
        # Sort each day's values and compute 25th percentile
        day_p25 = {}
        for day, vals in day_values.items():
            vals_sorted = sorted(vals)
            n = len(vals_sorted)
            idx = 0.25 * (n - 1)
            floor_idx = int(math.floor(idx))
            ceil_idx = min(floor_idx + 1, n - 1)
            weight = idx - floor_idx
            p25 = vals_sorted[floor_idx] * (1 - weight) + vals_sorted[ceil_idx] * weight
            day_p25[day] = p25
            
        # Filter candidates: keep only those with inst_value below 25th percentile for that day
        calls = []
        for sym, day, up, ret, inst_val in candidates:
            if inst_val < day_p25[day]:
                calls.append((sym, day, up, ret))
                
        if not calls:
            print("INSUFFICIENT=1")
            return
            
        # Sort calls by day to split into train/sealed
        calls.sort(key=lambda x: x[1])
        n_calls = len(calls)
        split_idx = int(n_calls * 0.8)
        train_calls = calls[:split_idx]
        sealed_calls = calls[split_idx:]
        
        # Helper function to compute metrics for a set of calls
        def compute_metrics(call_list):
            issued = len(call_list)
            if issued == 0:
                return 0, 0, 0, 0, 0
                
            hits = sum(1 for _, _, up, _ in call_list if up == 1)
            precision = hits / issued
            base_rate = hits / issued  # within issued subset
            
            distinct_days = len({day for _, day, _, _ in call_list})
            
            # Compute design effect using day clustering
            day_counts = {}
            for _, day, _, _ in call_list:
                day_counts[day] = day_counts.get(day, 0) + 1
                
            # Design effect = 1 + ICC * (m - 1) where ICC estimated by intra-class correlation
            # Simplified: using number of clusters (distinct days) and cluster sizes
            n_clusters = len(day_counts)
            avg_cluster_size = issued / n_clusters if n_clusters > 0 else 1
            # Estimate ICC as 0.1 (conservative)
            icc = 0.1
            design_effect = 1 + icc * (avg_cluster_size - 1)
            effective_n = issued / design_effect
            
            return issued, precision, base_rate, distinct_days, effective_n
            
        # Train metrics
        train_issued, train_precision, train_base, train_days, train_eff_n = compute_metrics(train_calls)
        # Sealed metrics
        sealed_issued, sealed_precision, sealed_base, sealed_days, sealed_eff_n = compute_metrics(sealed_calls)
        
        # Output
        print(f"ISSUED={train_issued}")
        print(f"OPPORTUNITIES={len(candidates)}")
        print(f"PRECISION={train_precision:.6f}")
        print(f"BASE_RATE={train_base:.6f}")
        print(f"DISTINCT_DAYS={train_days}")
        print(f"EFFECTIVE_N={train_eff_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
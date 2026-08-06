import sqlite3
import math
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
        
        # Get daily bars with features in one efficient query
        c.execute("""
            WITH bars_with_features AS (
                SELECT 
                    symbol_id, ts, close, volume,
                    LAG(close) OVER (PARTITION BY symbol_id ORDER BY ts) as prev_close,
                    AVG(volume) OVER (PARTITION BY symbol_id ORDER BY ts 
                        ROWS BETWEEN 20 PRECEDING AND 1 PRECEDING) as avg_vol_20,
                    LEAD(close, 20) OVER (PARTITION BY symbol_id ORDER BY ts) as close_t20
                FROM bars
                WHERE tf = '1d'
            )
            SELECT 
                b.symbol_id, b.ts, b.close, b.volume, b.prev_close, b.avg_vol_20, b.close_t20,
                s.mean_score
            FROM bars_with_features b
            LEFT JOIN sentiment_features s 
                ON b.symbol_id = s.symbol_id 
                AND s.day = date(b.ts, 'unixepoch')
            WHERE b.prev_close IS NOT NULL 
                AND b.avg_vol_20 IS NOT NULL 
                AND b.avg_vol_20 > 0
                AND b.close_t20 IS NOT NULL
            ORDER BY b.ts
        """)
        
        rows = c.fetchall()
        conn.close()
        
        if not rows:
            print("INSUFFICIENT=1")
            return
            
        # Process rows into observations
        observations = []
        for row in rows:
            sym, ts, close, volume, prev_close, avg_vol_20, close_t20, mean_score = row
            
            # Skip if sentiment data missing
            if mean_score is None:
                continue
                
            # Compute daily return and volume ratio
            ret = (close - prev_close) / prev_close
            vol_ratio = volume / avg_vol_20 if avg_vol_20 > 0 else 0
            
            # Compute 20-day forward return
            fwd_ret = (close_t20 - close) / close if close > 0 else 0
            
            observations.append((sym, ts, ret, vol_ratio, mean_score, fwd_ret))
        
        if len(observations) < 100:  # Need reasonable sample
            print("INSUFFICIENT=1")
            return
            
        # Sort by time for chronological split
        observations.sort(key=lambda x: x[1])
        n = len(observations)
        cutoff_idx = int(n * 0.8)
        
        # Split into training and sealed era
        train = observations[:cutoff_idx]
        sealed = observations[cutoff_idx:]
        
        if not train or not sealed:
            print("INSUFFICIENT=1")
            return
            
        # Compute percentiles from training data
        def percentile(lst, p):
            if not lst:
                return 0
            sorted_lst = sorted(lst)
            k = (len(sorted_lst)-1) * p
            f = math.floor(k)
            c = math.ceil(k)
            if f == c:
                return sorted_lst[int(k)]
            return sorted_lst[f] * (c - k) + sorted_lst[c_] * (k - f)
        
        train_scores = [o[4] for o in train]
        train_rets = [o[2] for o in train]
        train_vol = [o[3] for o in train]
        
        thr_score = percentile(train_scores, 0.10)
        thr_ret = percentile(train_rets, 0.10)
        thr_vol = percentile(train_vol, 0.90)
        
        # Issue signals based on thresholds
        issued = []
        opportunities = len(observations)
        
        for sym, ts, ret, vol_ratio, score, fwd_ret in observations:
            if score <= thr_score and ret <= thr_ret and vol_ratio >= thr_vol:
                issued.append((sym, ts, fwd_ret > 0))
        
        if not issued:
            print("INSUFFICIENT=1")
            return
            
        # Compute metrics
        n_issued = len(issued)
        hits = sum(1 for _, _, up in issued if up)
        precision = hits / n_issued
        base_rate = precision  # Predicted class = up, base rate within issued
        
        # Distinct days among issued only
        day_set = set()
        for _, ts, _ in issued:
            # Convert epoch to day string
            day_ts = ts - (ts % 86400)  # Truncate to day
            day_set.add(day_ts)
        distinct_days = len(day_set)
        
        # Design effect: cluster by day
        day_clusters = defaultdict(list)
        for _, ts, up in issued:
            day_ts = ts - (ts % 86400)
            day_clusters[day_ts].append(1 if up else 0)
        
        # Intraclass correlation
        p = precision
        var_total = p * (1 - p) if p > 0 and p < 1 else 0.001
        
        day_means = [sum(v)/len(v) for v in day_clusters.values() if v]
        if day_means:
            var_between = sum((m - p)**2 for m in day_means) / len(day_means)
            rho = var_between / var_total if var_total > 0 else 0
        else:
            rho = 0
            
        avg_cluster_size = n_issued / len(day_clusters) if day_clusters else 1
        deff = 1 + (avg_cluster_size - 1) * rho
        effective_n = n_issued / deff if deff > 0 else n_issued
        
        # Sealed era precision
        sealed_cutoff = observations[cutoff_idx - 1][1] if cutoff_idx > 0 else 0
        sealed_issued = [(sym, ts, up) for sym, ts, up in issued if ts > sealed_cutoff]
        sealed_hits = sum(1 for _, _, up in sealed_issued if up)
        sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0
        
        # Output results
        print(f"ISSUED={n_issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()
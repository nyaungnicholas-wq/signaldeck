# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 526
# cycle_index: 56
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

def percentile(sorted_list, p):
    if not sorted_list:
        return None
    idx = (len(sorted_list) - 1) * p
    lo = int(idx)
    hi = lo + 1
    if hi >= len(sorted_list):
        return sorted_list[lo]
    frac = idx - lo
    return sorted_list[lo] * (1 - frac) + sorted_list[hi] * frac

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=30)
    except sqlite3.Error:
        print("INSUFFICIENT=1")
        return
    
    try:
        # Get symbols with enough bars and sentiment data
        cursor = conn.cursor()
        cursor.execute("""
            SELECT s.id, s.symbol
            FROM symbols s
            WHERE s.market = 'stocks'
              AND (SELECT COUNT(DISTINCT date(b.ts, 'unixepoch'))
                   FROM bars b
                   WHERE b.symbol_id = s.id AND b.tf = '1d') >= 252
              AND (SELECT COUNT(DISTINCT sf.day)
                   FROM sentiment_features sf
                   WHERE sf.symbol_id = s.id) >= 252
        """)
        symbols = cursor.fetchall()
        
        if not symbols:
            print("INSUFFICIENT=1")
            return
        
        # Get max timestamp for 20% holdout
        cursor.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
        max_ts = cursor.fetchone()[0]
        cutoff_ts = max_ts - int(max_ts * 0.2)
        
        # Process each symbol
        all_calls = []
        opportunities = 0
        
        for symbol_id, symbol_name in symbols:
            # Get daily bars
            cursor.execute("""
                SELECT ts, open, high, low, close, volume
                FROM bars
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts
            """, (symbol_id,))
            bars = cursor.fetchall()
            
            # Get sentiment features
            cursor.execute("""
                SELECT day, mean_score
                FROM sentiment_features
                WHERE symbol_id = ?
                ORDER BY day
            """, (symbol_id,))
            sentiments = cursor.fetchall()
            
            if len(bars) < 120 or len(sentiments) < 120:
                continue
            
            # Build dictionaries for alignment
            ts_to_date = {}
            date_to_idx = {}
            for i, (ts, *_) in enumerate(bars):
                import datetime
                date_str = datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
                ts_to_date[ts] = date_str
                date_to_idx[date_str] = i
            
            # Align sentiment with bars
            aligned = []
            for day, score in sentiments:
                if day in date_to_idx:
                    idx = date_to_idx[day]
                    ts = bars[idx][0]
                    close = bars[idx][4]
                    vol = bars[idx][5]
                    aligned.append((ts, day, score, close, vol))
            
            if len(aligned) < 120:
                continue
            
            # Calculate rolling metrics
            for i in range(119, len(aligned)):
                ts, day, _, close_price, volume = aligned[i]
                
                # Skip if after cutoff (sealed era)
                if ts > cutoff_ts:
                    continue
                
                opportunities += 1
                
                # Get last 10 days of sentiment
                last10_sentiments = [aligned[j][2] for j in range(i-9, i+1)]
                if sum(1 for s in last10_sentiments if s is None) > 2:
                    continue
                
                # Calculate 10-day sentiment MA
                valid_sentiments = [s for s in last10_sentiments if s is not None]
                if len(valid_sentiments) < 8:
                    continue
                sentiment_ma10 = sum(valid_sentiments) / len(valid_sentiments)
                
                # Calculate 120-day sentiment MA minimum
                sentiment_ma_min120 = float('inf')
                for j in range(i-119, i+1):
                    window = [aligned[k][2] for k in range(j-9, j+1) if k >= 0]
                    valid_window = [s for s in window if s is not None]
                    if len(valid_window) >= 8:
                        ma = sum(valid_window) / len(valid_window)
                        if ma < sentiment_ma_min120:
                            sentiment_ma_min120 = ma
                
                if sentiment_ma10 > sentiment_ma_min120:
                    continue
                
                # Calculate 10-day price return
                if i < 9:
                    continue
                price_10d_ago = aligned[i-9][3]
                if price_10d_ago <= 0:
                    continue
                ret10 = (close_price / price_10d_ago) - 1
                
                # Calculate 120-day return distribution percentiles
                returns_120d = []
                for j in range(i-119, i+1):
                    if j >= 9:
                        p_ago = aligned[j-9][3]
                        p_now = aligned[j][3]
                        if p_ago > 0:
                            returns_120d.append((p_now / p_ago) - 1)
                
                if len(returns_120d) < 20:
                    continue
                returns_120d.sort()
                p30 = percentile(returns_120d, 0.3)
                
                if ret10 <= p30:
                    continue
                
                # Calculate 10-day average dollar volume
                dollar_vols_10d = [aligned[j][4] * aligned[j][3] for j in range(i-9, i+1)]
                avg_dv10 = sum(dollar_vols_10d) / len(dollar_vols_10d)
                
                # Calculate 120-day average dollar volume percentiles
                avg_dv120d = []
                for j in range(i-119, i+1):
                    window_dv = [aligned[k][4] * aligned[k][3] for k in range(j-9, j+1) if k >= 0]
                    if len(window_dv) >= 8:
                        avg_dv120d.append(sum(window_dv) / len(window_dv))
                
                if len(avg_dv120d) < 20:
                    continue
                avg_dv120d.sort()
                p20_dv = percentile(avg_dv120d, 0.2)
                
                if avg_dv10 < p20_dv:
                    continue
                
                # Get label from prediction_outcomes (21-day horizon)
                cursor.execute("""
                    SELECT up
                    FROM prediction_outcomes
                    WHERE symbol_id = ? AND ts = ? AND horizon = 21
                """, (symbol_id, ts))
                label_row = cursor.fetchone()
                
                if label_row is None:
                    continue
                
                up = label_row[0]
                call_date = day
                all_calls.append((ts, call_date, symbol_id, up))
        
        if not all_calls:
            print("INSUFFICIENT=1")
            return
        
        # Split into train and sealed
        calls_train = [c for c in all_calls if c[0] <= cutoff_ts]
        calls_sealed = [c for c in all_calls if c[0] > cutoff_ts]
        
        if not calls_train:
            print("INSUFFICIENT=1")
            return
        
        # Calculate metrics
        issued = len(calls_train)
        distinct_days_train = len(set(c[1] for c in calls_train))
        
        # Base rate: majority class in issued calls
        ups = sum(1 for c in calls_train if c[3] == 1)
        downs = issued - ups
        if ups >= downs:
            base_rate = ups / issued
            hits = ups
        else:
            base_rate = downs / issued
            hits = downs
        
        precision = hits / issued if issued > 0 else 0
        
        # Design effect
        day_counts = defaultdict(int)
        for c in calls_train:
            day_counts[c[1]] += 1
        counts = list(day_counts.values())
        if len(counts) > 1:
            mean_count = sum(counts) / len(counts)
            var_count = sum((x - mean_count) ** 2 for x in counts) / len(counts)
            design_effect = 1 + (var_count / mean_count) if mean_count > 0 else 1
        else:
            design_effect = 1
        
        effective_n = issued / design_effect if design_effect > 0 else 0
        
        # Sealed metrics
        issued_sealed = len(calls_sealed)
        if issued_sealed > 0:
            ups_sealed = sum(1 for c in calls_sealed if c[3] == 1)
            downs_sealed = issued_sealed - ups_sealed
            if ups_sealed >= downs_sealed:
                hits_sealed = ups_sealed
            else:
                hits_sealed = downs_sealed
            sealed_precision = hits_sealed / issued_sealed
        else:
            sealed_precision = 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days_train}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        # Validate invariants
        assert distinct_days_train <= issued, "DISTINCT_DAYS exceeds ISSUED"
        assert effective_n < issued, "EFFECTIVE_N not less than ISSUED"
        
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == "__main__":
    main()
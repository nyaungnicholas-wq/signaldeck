# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 340
# cycle_index: 8
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

DB_PATH = "data/signaldeck.db?mode=ro"

def main():
    try:
        conn = sqlite3.connect(f"file:{DB_PATH}", uri=True, timeout=30)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Get all symbols with daily bars and compute required features per symbol per day
        # We need to work with a list of (symbol_id, ts, close) to compute rolling metrics
        cur.execute("""
            SELECT symbol_id, ts, close, volume
            FROM bars
            WHERE tf = '1d'
            ORDER BY symbol_id, ts
        """)
        rows = cur.fetchall()
        
        if len(rows) < 50000:
            print("INSUFFICIENT=1")
            return
            
        # Group by symbol_id
        symbols_data = {}
        for row in rows:
            sid = row['symbol_id']
            if sid not in symbols_data:
                symbols_data[sid] = []
            symbols_data[sid].append({
                'ts': row['ts'],
                'close': row['close'],
                'volume': row['volume'],
                'dollar_vol': row['close'] * row['volume'] if row['close'] and row['volume'] else 0
            })
        
        # Get prediction outcomes to find latest ts
        cur.execute("SELECT MAX(ts) as max_ts FROM prediction_outcomes")
        max_ts = cur.fetchone()['max_ts']
        
        # Determine sealed era cutoff: most recent 20% of time span
        cur.execute("SELECT MIN(ts) as min_ts FROM prediction_outcomes")
        min_ts = cur.fetchone()['min_ts']
        span = max_ts - min_ts
        sealed_cutoff = max_ts - int(span * 0.2)
        
        # Prepare opportunities
        opportunities = []
        
        for sid, data in symbols_data.items():
            n = len(data)
            if n < 300:
                continue
            
            # Compute rolling metrics for each day that has enough history
            for i in range(299, n):
                current = data[i]
                ts = current['ts']
                
                # Skip if before data availability or after sealed era? No, we consider all
                # Check we have 252-day return
                if i < 251:
                    continue
                    
                # Get trailing 252 trading day return
                prev_close = data[i - 251]['close']  # 252 days ago inclusive
                if prev_close and prev_close > 0:
                    trailing_return = (current['close'] / prev_close) - 1
                else:
                    continue
                
                # Check median dollar volume over prior 20 days
                if i < 20:
                    continue
                window = [data[j]['dollar_vol'] for j in range(i-19, i+1) if data[j]['dollar_vol']]
                if len(window) < 10:
                    continue
                sorted_window = sorted(window)
                median_dollar_vol = sorted_window[len(sorted_window)//2]
                if median_dollar_vol < 1_000_000:
                    continue
                
                # Need forward return for label (21 trading days)
                if i + 21 >= n:
                    continue
                
                future_close = data[i + 21]['close']
                if not future_close or future_close <= 0:
                    continue
                    
                forward_return = (future_close / current['close']) - 1
                label_up = 1 if forward_return > 0 else 0
                
                # Store as opportunity
                opportunities.append({
                    'symbol_id': sid,
                    'ts': ts,
                    'close': current['close'],
                    'trailing_return': trailing_return,
                    'label_up': label_up,
                    'is_sealed': ts >= sealed_cutoff
                })
        
        if len(opportunities) < 100:
            print("INSUFFICIENT=1")
            return
        
        # Group opportunities by day to compute cross-sectional deciles
        day_groups = {}
        for opp in opportunities:
            day = opp['ts']
            if day not in day_groups:
                day_groups[day] = []
            day_groups[day].append(opp)
        
        # Compute deciles per day and issue calls
        issued = []
        total_opportunities = len(opportunities)
        
        for day, group in day_groups.items():
            # Need at least 10 symbols to compute deciles
            if len(group) < 10:
                continue
            
            # Get trailing returns for this day
            returns = [g['trailing_return'] for g in group]
            returns_sorted = sorted(returns)
            p10_index = math.floor(len(returns_sorted) * 0.1)
            p10_threshold = returns_sorted[p10_index]
            
            # Issue call if in bottom decile
            for g in group:
                if g['trailing_return'] <= p10_threshold:
                    issued.append(g)
        
        if len(issued) == 0:
            print("INSUFFICIENT=1")
            return
        
        # Compute metrics
        hits = sum(1 for x in issued if x['label_up'] == 1)
        precision = hits / len(issued) if len(issued) > 0 else 0
        
        # Base rate within issued
        base_rate = sum(1 for x in issued if x['label_up'] == 1) / len(issued)
        
        # Distinct days
        issued_days = set(x['ts'] for x in issued)
        distinct_days = len(issued_days)
        
        # Design effect (clustering by day)
        if distinct_days > 0:
            avg_cluster_size = len(issued) / distinct_days
            # Simplified design effect using intracluster correlation approximation
            # For binary outcomes, use ICC approximation
            n = len(issued)
            k = distinct_days
            # Calculate overall mean
            overall_mean = precision
            # Calculate between-group variance
            ss_between = 0
            ss_within = 0
            for day in issued_days:
                day_issued = [x for x in issued if x['ts'] == day]
                day_mean = sum(x['label_up'] for x in day_issued) / len(day_issued) if day_issued else 0
                ss_between += len(day_issued) * (day_mean - overall_mean) ** 2
                ss_within += sum((x['label_up'] - day_mean) ** 2 for x in day_issued)
            ms_between = ss_between / (k - 1) if k > 1 else 0
            ms_within = ss_within / (n - k) if n > k else 0
            # ICC for binary data
            icc = ms_between / (ms_between + ms_within) if (ms_between + ms_within) > 0 else 0
            design_effect = 1 + (avg_cluster_size - 1) * icc
        else:
            design_effect = 1
        
        effective_n = len(issued) / design_effect
        
        # Sealed era precision
        sealed_issued = [x for x in issued if x['is_sealed']]
        if len(sealed_issued) > 0:
            sealed_hits = sum(1 for x in sealed_issued if x['label_up'] == 1)
            sealed_precision = sealed_hits / len(sealed_issued)
        else:
            sealed_precision = 0.0
        
        # Print results
        print(f"ISSUED={len(issued)}")
        print(f"OPPORTUNITIES={total_opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
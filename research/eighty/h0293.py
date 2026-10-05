# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 292
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except sqlite3.Error as e:
        print("INSUFFICIENT=1")
        return
    cursor = conn.cursor()

    # Check if we have enough data
    cursor.execute("SELECT MIN(ts) FROM bars WHERE tf='1d'")
    min_ts = cursor.fetchone()[0]
    if min_ts is None:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Get the latest ts in the database
    cursor.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
    max_ts = cursor.fetchone()[0]
    if max_ts is None:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Get all month-end dates (last trading day of each month)
    cursor.execute("""
        SELECT ts, strftime('%Y-%m', ts, 'unixepoch') as month
        FROM bars 
        WHERE tf='1d'
        GROUP BY month
        HAVING ts = MAX(ts)
        ORDER BY ts
    """)
    month_ends = cursor.fetchall()
    
    if len(month_ends) < 10:  # Need enough months for analysis
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Split into in-sample and sealed (most recent 20% of months)
    n_months = len(month_ends)
    n_sealed = max(1, int(n_months * 0.2))
    n_in_sample = n_months - n_sealed
    in_sample_months = month_ends[:n_in_sample]
    sealed_months = month_ends[n_in_sample:]

    all_calls = []
    all_opportunities = []
    
    # Process each month-end
    for month_end in in_sample_months + sealed_months:
        signal_ts = month_end[0]
        
        # Get symbols with sufficient price history (252 trading days before signal)
        cursor.execute("""
            SELECT symbol_id, COUNT(*) as n_days
            FROM bars 
            WHERE tf='1d' AND ts < ?
            GROUP BY symbol_id
            HAVING n_days >= 252
        """, (signal_ts,))
        eligible_symbols = {row[0] for row in cursor.fetchall()}
        
        if not eligible_symbols:
            continue
            
        # Calculate average daily dollar volume over trailing 63 trading days
        cursor.execute("""
            SELECT symbol_id, AVG(volume * close) as avg_dollar_vol
            FROM bars 
            WHERE tf='1d' AND ts < ?
            AND symbol_id IN ({})
            GROUP BY symbol_id
            HAVING avg_dollar_vol >= 10000000
        """.format(','.join('?' * len(eligible_symbols))), 
        (signal_ts,) + tuple(eligible_symbols))
        
        volume_eligible = {row[0] for row in cursor.fetchall()}
        
        if not volume_eligible:
            continue
            
        # Get the ts of 21 trading days before signal for each eligible symbol
        # and the close prices for return calculation
        cursor.execute("""
            WITH ranked_bars AS (
                SELECT 
                    symbol_id,
                    ts,
                    close,
                    ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts DESC) as rn
                FROM bars 
                WHERE tf='1d' AND ts <= ?
                AND symbol_id IN ({})
            )
            SELECT 
                t21.symbol_id,
                t21.close as close_21d_ago,
                t0.close as close_signal
            FROM 
                (SELECT symbol_id, close FROM ranked_bars WHERE rn = 21) t21
            JOIN 
                (SELECT symbol_id, close FROM ranked_bars WHERE rn = 1) t0
            ON t21.symbol_id = t0.symbol_id
            WHERE t21.symbol_id IN ({})
        """.format(','.join('?' * len(volume_eligible)), 
                   ','.join('?' * len(volume_eligible))),
        (signal_ts,) + tuple(volume_eligible) + tuple(volume_eligible))
        
        return_data = cursor.fetchall()
        
        if len(return_data) < 5:  # Need at least 5 symbols for quintile calculation
            continue
            
        # Calculate returns and find bottom quintile
        returns = []
        for row in return_data:
            symbol_id, close_21d_ago, close_signal = row
            if close_21d_ago and close_signal and close_21d_ago > 0:
                ret = (close_signal - close_21d_ago) / close_21d_ago
                returns.append((symbol_id, ret))
        
        if len(returns) < 5:
            continue
            
        # Sort by return and get bottom quintile (20%)
        returns.sort(key=lambda x: x[1])
        n_bottom_quintile = max(1, int(len(returns) * 0.2))
        calls_today = [symbol_id for symbol_id, ret in returns[:n_bottom_quintile]]
        
        if not calls_today:
            continue
            
        # For each call, check if price went up in next 5 trading days
        for symbol_id in calls_today:
            # Get the close price 5 trading days after signal
            cursor.execute("""
                WITH future_bars AS (
                    SELECT 
                        ts,
                        close,
                        ROW_NUMBER() OVER (ORDER BY ts) as rn
                    FROM bars 
                    WHERE tf='1d' AND symbol_id = ? AND ts > ?
                )
                SELECT close 
                FROM future_bars 
                WHERE rn = 5
            """, (symbol_id, signal_ts))
            
            future_row = cursor.fetchone()
            if not future_row:
                continue
                
            future_close = future_row[0]
            
            # Get the signal date close
            cursor.execute("""
                SELECT close 
                FROM bars 
                WHERE tf='1d' AND symbol_id = ? AND ts = ?
            """, (symbol_id, signal_ts))
            
            signal_row = cursor.fetchone()
            if not signal_row:
                continue
                
            signal_close = signal_row[0]
            
            # Calculate if up
            is_up = 1 if future_close > signal_close else 0
            
            all_calls.append({
                'symbol_id': symbol_id,
                'ts': signal_ts,
                'is_up': is_up
            })
            
        # Count this as an opportunity (month-end day considered)
        all_opportunities.append(signal_ts)
    
    conn.close()
    
    if not all_calls:
        print("INSUFFICIENT=1")
        return
    
    # Separate in-sample and sealed calls
    in_sample_calls = [c for c in all_calls if c['ts'] <= in_sample_months[-1][0]]
    sealed_calls = [c for c in all_calls if c['ts'] > in_sample_months[-1][0]]
    
    # Calculate metrics
    n_issued = len(all_calls)
    n_opportunities = len(all_opportunities)
    
    # Precision
    n_hits = sum(c['is_up'] for c in all_calls)
    precision = n_hits / n_issued if n_issued > 0 else 0
    
    # Base rate within issued subset
    base_rate = precision  # This is the base rate of up within issued calls
    
    # Distinct days (from issued calls only)
    distinct_days = len(set(c['ts'] for c in all_calls))
    
    # Effective sample size calculation
    # Group calls by day (cluster)
    day_clusters = {}
    for c in all_calls:
        day = c['ts']
        if day not in day_clusters:
            day_clusters[day] = []
        day_clusters[day].append(c['is_up'])
    
    # Calculate intracluster correlation
    n_clusters = len(day_clusters)
    if n_clusters < 2:
        design_effect = 1.0
    else:
        # Calculate proportions per cluster
        p_i = []
        n_i = []
        for day, outcomes in day_clusters.items():
            p_i.append(sum(outcomes) / len(outcomes))
            n_i.append(len(outcomes))
        
        # Overall proportion
        p = n_hits / n_issued
        
        # Between-cluster variance
        var_between = sum(ni * (pi - p) ** 2 for pi, ni in zip(p_i, n_i))
        var_between /= (n_clusters - 1) if n_clusters > 1 else 1
        
        # Within-cluster variance (Bernoulli)
        var_within = p * (1 - p) if p > 0 and p < 1 else 0.01
        
        # ICC
        icc = var_between / (var_within + var_between) if (var_within + var_between) > 0 else 0
        
        # Average cluster size
        avg_cluster_size = n_issued / n_clusters
        
        # Design effect
        design_effect = 1 + (avg_cluster_size - 1) * icc
    
    effective_n = n_issued / design_effect if design_effect > 0 else n_issued
    
    # Sealed era precision
    if sealed_calls:
        sealed_hits = sum(c['is_up'] for c in sealed_calls)
        sealed_precision = sealed_hits / len(sealed_calls)
    else:
        sealed_precision = 0.0
    
    # Print results
    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={n_opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
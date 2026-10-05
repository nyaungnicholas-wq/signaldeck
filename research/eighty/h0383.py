# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 382
# cycle_index: 50
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import statistics
from collections import defaultdict
from datetime import datetime

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get eligible symbols with at least 250 daily bars from 2018-07 onward
    cursor = conn.execute('''
        SELECT b.symbol_id, COUNT(DISTINCT b.ts) as bar_count
        FROM bars b
        JOIN symbols s ON b.symbol_id = s.id
        WHERE b.tf = '1d' 
        AND s.market = 'stocks'
        AND b.ts >= 1530412800
        GROUP BY b.symbol_id
        HAVING COUNT(DISTINCT b.ts) >= 250
    ''')
    eligible_symbols = {row['symbol_id'] for row in cursor}
    
    if not eligible_symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Get all prediction outcomes for 21-day horizon
    cursor = conn.execute('''
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21
    ''')
    outcomes = {}
    all_timestamps = set()
    for row in cursor:
        symbol = row['symbol_id']
        ts = row['ts']
        outcomes[(symbol, ts)] = row['up']
        all_timestamps.add(ts)
    
    if not outcomes:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Determine 80/20 split
    sorted_timestamps = sorted(all_timestamps)
    split_index = int(len(sorted_timestamps) * 0.8)
    training_cutoff = sorted_timestamps[split_index]
    
    # Preload public float fundamentals
    cursor = conn.execute('''
        SELECT symbol_id, value, fetched_at
        FROM fundamentals
        WHERE metric = 'EntityPublicFloat'
        AND symbol_id IN ({})
        ORDER BY symbol_id, fetched_at DESC
    '''.format(','.join('?' * len(eligible_symbols))), tuple(eligible_symbols))
    
    public_floats = defaultdict(list)
    for row in cursor:
        symbol = row['symbol_id']
        try:
            value = float(row['value'])
            fetched_at = row['fetched_at']
            public_floats[symbol].append((fetched_at, value))
        except (ValueError, TypeError):
            continue
    
    # Preload sentiment features
    cursor = conn.execute('''
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({})
    '''.format(','.join('?' * len(eligible_symbols))), tuple(eligible_symbols))
    
    sentiment_data = defaultdict(dict)
    for row in cursor:
        symbol = row['symbol_id']
        day_str = row['day']
        try:
            score = float(row['mean_score'])
            day_ts = datetime.strptime(day_str, '%Y-%m-%d').timestamp()
            sentiment_data[symbol][day_ts] = score
        except (ValueError, TypeError):
            continue
    
    issued_calls = []
    opportunities = 0
    
    for symbol in eligible_symbols:
        if symbol not in outcomes:
            continue
        
        symbol_timestamps = [ts for (s, ts) in outcomes.keys() if s == symbol]
        if not symbol_timestamps:
            continue
        
        if symbol not in public_floats:
            continue
        pf_list = public_floats[symbol]
        if len(pf_list) < 5:
            continue
        
        if symbol not in sentiment_data:
            continue
        sent_dict = sentiment_data[symbol]
        
        for ts in symbol_timestamps:
            opportunities += 1
            
            # Check recent trading activity
            cursor = conn.execute('''
                SELECT COUNT(*) as count
                FROM bars
                WHERE symbol_id = ? 
                AND tf = '1d'
                AND ts >= ?
                AND ts <= ?
            ''', (symbol, ts - 5*86400, ts))
            if cursor.fetchone()['count'] == 0:
                continue
            
            # Find valid public float records up to ts
            valid_pf = [(fa, val) for fa, val in pf_list if fa <= ts]
            if len(valid_pf) < 5:
                continue
            
            valid_pf.sort(key=lambda x: x[0], reverse=True)
            current_pf = valid_pf[0][1]
            
            # Find public float from ~4 quarters prior (365 days)
            target_time = valid_pf[0][0] - 365*86400
            prior_pf = None
            for fa, val in valid_pf[1:]:
                if fa <= target_time + 30*86400:
                    prior_pf = val
                    break
            
            if prior_pf is None or prior_pf <= 0:
                continue
            
            # Calculate YoY change
            yoy_change = (current_pf - prior_pf) / prior_pf
            if yoy_change >= -0.10:
                continue
            
            # Get current day sentiment
            if ts not in sent_dict:
                earlier_days = [d for d in sent_dict.keys() if d <= ts]
                if not earlier_days:
                    continue
                sent_day = max(earlier_days)
            else:
                sent_day = ts
            
            current_sent = sent_dict[sent_day]
            
            # Calculate trailing 1-year sentiment distribution
            one_year_ago = ts - 365*86400
            trailing_sentiments = [score for day, score in sent_dict.items() 
                                   if one_year_ago <= day <= ts]
            
            if len(trailing_sentiments) < 10:
                continue
            
            sorted_sent = sorted(trailing_sentiments)
            p90 = sorted_sent[int(len(sorted_sent) * 0.9)]
            
            if current_sent < p90:
                continue
            
            issued_calls.append((symbol, ts))
    
    conn.close()
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics
    issued_count = len(issued_calls)
    hits = sum(1 for symbol, ts in issued_calls if outcomes.get((symbol, ts)) == 1)
    base_up_count = hits
    distinct_days = set()
    for symbol, ts in issued_calls:
        day = datetime.utcfromtimestamp(ts).date()
        distinct_days.add(day)
    
    precision = hits / issued_count
    base_rate = base_up_count / issued_count
    distinct_days_count = len(distinct_days)
    
    # Sealed era
    sealed_hits = sum(1 for symbol, ts in issued_calls if ts >= training_cutoff and outcomes.get((symbol, ts)) == 1)
    sealed_issued = sum(1 for symbol, ts in issued_calls if ts >= training_cutoff)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Design effect
    symbol_counts = defaultdict(int)
    for symbol, ts in issued_calls:
        symbol_counts[symbol] += 1
    
    avg_cluster_size = sum(symbol_counts.values()) / len(symbol_counts)
    
    # Calculate ICC
    p = precision
    total_variance = p * (1 - p)
    
    # Calculate cluster means and weighted variance
    cluster_means = []
    cluster_sizes = []
    for symbol in symbol_counts:
        n_i = symbol_counts[symbol]
        hits_i = sum(1 for s, t in issued_calls if s == symbol and outcomes.get((s, t)) == 1)
        p_i = hits_i / n_i if n_i > 0 else 0
        cluster_means.append(p_i)
        cluster_sizes.append(n_i)
    
    N = issued_count
    K = len(symbol_means) if 'symbol_means' in dir() else len(cluster_means)
    
    # Weighted between-cluster variance
    between_var = sum(n_i * (p_i - p)**2 for p_i, n_i in zip(cluster_means, cluster_sizes)) / N
    
    # ICC and design effect
    if total_variance > 0:
        icc = between_var / total_variance
        design_effect = 1 + (avg_cluster_size - 1) * icc
    else:
        design_effect = 1
    
    effective_n = issued_count / design_effect
    
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_count}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
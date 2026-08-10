# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 437
# cycle_index: 28
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get all symbols with at least two consecutive quarters of EPS data
    eps_cursor = conn.execute("""
        SELECT symbol_id, as_of, fetched_at, value as eps
        FROM fundamentals
        WHERE metric = 'EPS'
        ORDER BY symbol_id, as_of
    """)
    
    eps_data = defaultdict(list)
    for row in eps_cursor:
        eps_data[row['symbol_id']].append({
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at'],
            'eps': float(row['eps'])
        })
    
    # Filter symbols with at least 2 consecutive quarters
    valid_symbols = []
    for symbol_id, quarters in eps_data.items():
        if len(quarters) < 2:
            continue
        # Sort by fetched_at to get chronological order
        quarters.sort(key=lambda x: x['fetched_at'])
        # Check for at least 2 consecutive quarters with positive EPS
        has_consecutive = False
        for i in range(1, len(quarters)):
            if quarters[i-1]['eps'] > 0 and quarters[i]['eps'] > 0:
                has_consecutive = True
                break
        if has_consecutive:
            valid_symbols.append(symbol_id)
    
    if not valid_symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get daily prices for valid symbols from 2018-07 onward
    start_epoch = 1530412800  # 2018-07-01
    symbols_with_prices = set()
    price_data = defaultdict(list)
    
    placeholders = ','.join(['?' for _ in valid_symbols])
    price_cursor = conn.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' 
        AND symbol_id IN ({placeholders})
        AND ts >= ?
        ORDER BY symbol_id, ts
    """, valid_symbols + [start_epoch])
    
    for row in price_cursor:
        symbol_id = row['symbol_id']
        symbols_with_prices.add(symbol_id)
        price_data[symbol_id].append({
            'ts': row['ts'],
            'close': float(row['close']),
            'open': float(row['open'])
        })
    
    if not symbols_with_prices:
        print("INSUFFICIENT=1")
        return
    
    # Get prediction outcomes for horizon=21
    label_cursor = conn.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
        AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join(['?' for _ in symbols_with_prices])), 
        list(symbols_with_prices))
    
    label_data = defaultdict(list)
    for row in label_cursor:
        label_data[row['symbol_id']].append({
            'ts': row['ts'],
            'up': int(row['up'])
        })
    
    # Combine all data and apply as-of discipline
    all_decisions = []  # (symbol_id, decision_ts, up_call, actual_up, is_sealed, day)
    
    # Calculate split point for sealed era (most recent 20%)
    all_ts = set()
    for symbol_id in symbols_with_prices:
        for price in price_data[symbol_id]:
            all_ts.add(price['ts'])
        for label in label_data[symbol_id]:
            all_ts.add(label['ts'])
    
    sorted_ts = sorted(all_ts)
    split_idx = int(len(sorted_ts) * 0.8)
    split_ts = sorted_ts[split_idx] if split_idx < len(sorted_ts) else sorted_ts[-1]
    
    # Process each symbol
    for symbol_id in symbols_with_prices:
        if symbol_id not in label_data:
            continue
            
        prices = price_data[symbol_id]
        labels = label_data[symbol_id]
        quarters = eps_data[symbol_id]
        
        # Create lookup for prices by timestamp
        price_by_ts = {p['ts']: p for p in prices}
        
        # Create lookup for labels by timestamp
        label_by_ts = {l['ts']: l for l in labels}
        
        # Get all decision timestamps (intersection of prices and labels)
        decision_ts_list = sorted(set(price_by_ts.keys()) & set(label_by_ts.keys()))
        
        # Sort quarters by fetched_at
        quarters.sort(key=lambda x: x['fetched_at'])
        
        for decision_ts in decision_ts_list:
            # As-of check: ensure we have EPS data fetched before decision
            available_quarters = [q for q in quarters if q['fetched_at'] <= decision_ts]
            
            if len(available_quarters) < 2:
                continue
            
            # Get last two quarters
            last_two = available_quarters[-2:]
            eps_prev = last_two[0]['eps']
            eps_curr = last_two[1]['eps']
            
            if eps_prev <= 0 or eps_curr <= 0:
                continue
            
            # Calculate EPS growth rate for last two quarters
            growth_prev = 0
            if len(available_quarters) >= 3:
                eps_earlier = available_quarters[-3]['eps']
                if eps_earlier > 0:
                    growth_prev = (eps_prev - eps_earlier) / eps_earlier
            
            growth_curr = (eps_curr - eps_prev) / eps_prev
            
            # Check if EPS growth has accelerated for last two quarters
            # Both quarters must have positive growth and current must be higher
            if growth_prev <= 0 or growth_curr <= 0 or growth_curr <= growth_prev:
                continue
            
            # Calculate current P/E
            current_price = price_by_ts[decision_ts]['close']
            if current_price <= 0:
                continue
            
            current_pe = current_price / eps_curr
            
            # Calculate trailing P/E medians for 3-year window
            three_year_ms = 3 * 365 * 24 * 3600  # 3 years in seconds
            window_start = decision_ts - three_year_ms
            pe_ratios = []
            
            for price in prices:
                if window_start <= price['ts'] <= decision_ts:
                    # Find latest EPS available as of this price's timestamp
                    eps_as_of = None
                    for q in quarters:
                        if q['fetched_at'] <= price['ts']:
                            eps_as_of = q['eps']
                        else:
                            break
                    
                    if eps_as_of and eps_as_of > 0:
                        pe = price['close'] / eps_as_of
                        pe_ratios.append(pe)
            
            if not pe_ratios:
                continue
            
            # Calculate median P/E
            pe_ratios.sort()
            n = len(pe_ratios)
            if n % 2 == 1:
                median_pe = pe_ratios[n // 2]
            else:
                median_pe = (pe_ratios[n // 2 - 1] + pe_ratios[n // 2]) / 2
            
            # Check condition: P/E below symbol's 3-year median
            if current_pe >= median_pe:
                continue
            
            # Issue "up" call
            actual_up = label_by_ts[decision_ts]['up']
            day_key = decision_ts // (24 * 3600)  # group by UTC day
            
            all_decisions.append({
                'symbol_id': symbol_id,
                'ts': decision_ts,
                'call': 1,  # up call
                'actual': actual_up,
                'is_sealed': decision_ts > split_ts,
                'day': day_key
            })
    
    if not all_decisions:
        print("INSUFFICIENT=1")
        return
    
    # Split into training and sealed
    training = [d for d in all_decisions if not d['is_sealed']]
    sealed = [d for d in all_decisions if d['is_sealed']]
    
    if not training:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics for training set
    issued = len(training)
    
    # Count hits (precision)
    hits = sum(1 for d in training if d['actual'] == 1)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate of predicted class within issued calls
    base_rate = hits / issued if issued > 0 else 0
    
    # Distinct days
    distinct_days = len(set(d['day'] for d in training))
    
    # Calculate opportunities: all days considered (all symbol-days in universe)
    # We need to count all days where we had the data to make a decision
    # For each symbol, count days where we had price and label data
    opportunities = 0
    for symbol_id in symbols_with_prices:
        if symbol_id not in label_data:
            continue
        prices = price_data[symbol_id]
        labels = label_data[symbol_id]
        price_dates = set(p['ts'] // (24*3600) for p in prices)
        label_dates = set(l['ts'] // (24*3600) for l in labels)
        opportunities += len(price_dates & label_dates)
    
    # Effective N (design effect)
    # Group calls by day
    day_counts = defaultdict(int)
    for d in training:
        day_counts[d['day']] += 1
    
    total_calls = issued
    if day_counts:
        cluster_sizes = list(day_counts.values())
        avg_cluster_size = sum(cluster_sizes) / len(cluster_sizes)
        variance = sum((x - avg_cluster_size) ** 2 for x in cluster_sizes) / len(cluster_sizes)
        # Simple design effect approximation: DEFF = 1 + (cluster_size - 1) * ICC
        # We assume ICC based on the ratio of variance to expected variance under independence
        if avg_cluster_size > 1:
            # Expected variance under independence: avg_cluster_size * (1 - 1/n_symbols) * p * (1-p)
            # But we don't have that, so we use the ratio of observed variance to binomial variance
            p = base_rate
            if p > 0 and p < 1:
                binomial_var = avg_cluster_size * p * (1 - p)
                if binomial_var > 0:
                    ICC = min(1, variance / binomial_var)
                else:
                    ICC = 0
            else:
                ICC = 0
            DEFF = 1 + (avg_cluster_size - 1) * ICC
        else:
            DEFF = 1
    else:
        DEFF = 1
    
    effective_n = issued / DEFF if DEFF > 0 else issued
    
    # Calculate sealed precision
    sealed_hits = sum(1 for d in sealed if d['actual'] == 1)
    sealed_issued = len(sealed)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
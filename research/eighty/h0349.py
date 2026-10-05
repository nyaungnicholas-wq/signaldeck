# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 348
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import statistics

def main():
    db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    db.row_factory = sqlite3.Row
    
    # 1. Get all symbols with insider purchases (officer/director) and required data
    symbols_query = """
    SELECT DISTINCT symbol_id 
    FROM insider_trades 
    WHERE code = 'P' 
      AND (title LIKE '%Officer%' OR title LIKE '%Director%')
      AND symbol_id IN (
          SELECT symbol_id FROM bars WHERE tf = '1d' GROUP BY symbol_id 
          HAVING COUNT(DISTINCT ts) >= 365
      )
      AND symbol_id IN (
          SELECT symbol_id FROM sentiment_features WHERE mean_score IS NOT NULL 
          GROUP BY symbol_id HAVING COUNT(DISTINCT day) >= 365
      )
    """
    symbol_rows = db.execute(symbols_query).fetchall()
    if not symbol_rows:
        print("INSUFFICIENT=1")
        return
    valid_symbols = [r['symbol_id'] for r in symbol_rows]
    
    # 2. Get all decision points: (symbol, day, sentiment, has_recent_purchase, up_label)
    # We need to be careful about as-of: only use sentiment from that day, only purchases filed before that day
    # and only use prediction_outcomes with ts corresponding to that same day
    decision_query = """
    SELECT 
        sf.symbol_id,
        sf.day,
        sf.mean_score,
        -- Check for insider purchase in last 60 calendar days disclosed before this day
        CASE WHEN EXISTS (
            SELECT 1 FROM insider_trades it
            WHERE it.symbol_id = sf.symbol_id
              AND it.code = 'P'
              AND (it.title LIKE '%Officer%' OR it.title LIKE '%Director%')
              AND it.filed_ts < strftime('%s', sf.day) + 86400  -- end of that day
              AND it.filed_ts > strftime('%s', sf.day, '-60 days')
        ) THEN 1 ELSE 0 END AS has_purchase,
        -- Get the label from prediction_outcomes for the same day (ts = unix epoch of day start)
        po.up
    FROM sentiment_features sf
    LEFT JOIN prediction_outcomes po
        ON po.symbol_id = sf.symbol_id
        AND po.horizon = 21
        AND po.ts = strftime('%s', sf.day)
    WHERE sf.symbol_id IN ({})
    AND sf.mean_score IS NOT NULL
    AND po.up IS NOT NULL
    ORDER BY sf.symbol_id, sf.day
    """.format(','.join('?' * len(valid_symbols)))
    
    all_rows = db.execute(decision_query, valid_symbols).fetchall()
    if not all_rows:
        print("INSUFFICIENT=1")
        return
    
    # 3. Group by symbol to compute rolling sentiment distribution
    symbol_data = {}
    for row in all_rows:
        sym = row['symbol_id']
        if sym not in symbol_data:
            symbol_data[sym] = []
        symbol_data[sym].append({
            'day': row['day'],
            'sentiment': row['mean_score'],
            'has_purchase': row['has_purchase'],
            'up': row['up']
        })
    
    # 4. For each symbol, compute rolling 1-year (365-day) distribution of sentiment
    opportunities = []
    for sym, days in symbol_data.items():
        # Sort by day
        days.sort(key=lambda x: x['day'])
        sentiments = [d['sentiment'] for d in days]
        
        # For each day, check if sentiment is in bottom decile of last 365 days
        for i, day_info in enumerate(days):
            # Get last 365 days of sentiment up to and including this day
            lookback_start = max(0, i - 364)
            window = sentiments[lookback_start:i+1]
            if len(window) < 100:  # Need enough data for decile
                continue
            
            # Compute bottom decile
            sorted_window = sorted(window)
            p10 = sorted_window[int(len(sorted_window) * 0.1)]
            
            if day_info['sentiment'] <= p10 and day_info['has_purchase']:
                opportunities.append(day_info)
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # 5. Split into non-sealed (80%) and sealed (20%)
    opportunities.sort(key=lambda x: x['day'])
    split_idx = int(len(opportunities) * 0.8)
    non_sealed = opportunities[:split_idx]
    sealed = opportunities[split_idx:]
    
    # 6. Compute metrics for non-sealed
    issued_calls = [o for o in non_sealed if o['up'] == 1]
    issued_count = len(issued_calls)
    if issued_count == 0:
        print("INSUFFICIENT=1")
        return
    
    opportunities_count = len(non_sealed)
    precision = sum(1 for o in issued_calls) / issued_count
    base_rate = precision  # Base rate within issued subset is the same as precision
    
    # Count distinct UTC days
    distinct_days = len(set(o['day'] for o in issued_calls))
    
    # Compute design effect by clustering on day
    day_counts = {}
    day_up_counts = {}
    for o in non_sealed:
        d = o['day']
        if d not in day_counts:
            day_counts[d] = 0
            day_up_counts[d] = 0
        day_counts[d] += 1
        if o['up'] == 1:
            day_up_counts[d] += 1
    
    # For design effect: need cluster size and ICC
    # Using formula: DEFF = 1 + (m - 1) * ICC
    # where m = average cluster size, ICC = (between-cluster variance) / (total variance)
    cluster_sizes = list(day_counts.values())
    m = statistics.mean(cluster_sizes) if cluster_sizes else 1
    
    # Proportions for ICC calculation
    all_up = [o['up'] for o in non_sealed]
    if len(all_up) < 2:
        design_effect = 1.0
    else:
        p_overall = statistics.mean(all_up)
        var_total = p_overall * (1 - p_overall)
        
        # Between-cluster variance
        day_props = [day_up_counts[d] / day_counts[d] if day_counts[d] > 0 else 0 
                    for d in day_counts]
        var_between = statistics.variance(day_props) if len(day_props) > 1 else 0
        
        if var_total > 0:
            ICC = var_between / var_total
            ICC = max(0, min(ICC, 1))  # Bound between 0 and 1
        else:
            ICC = 0
        
        design_effect = 1 + (m - 1) * ICC
    
    effective_n = issued_count / design_effect if design_effect > 0 else issued_count
    
    # 7. Compute sealed precision
    sealed_issued = [o for o in sealed if o['up'] == 1]
    sealed_precision = len(sealed_issued) / len(sealed) if sealed else 0
    
    # 8. Print results
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    db.close()

if __name__ == "__main__":
    main()
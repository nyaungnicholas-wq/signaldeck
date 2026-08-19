# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 576
# cycle_index: 34
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
from datetime import datetime, timedelta
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()
    
    # Get all symbols with daily bars from 2018-07 onward
    cursor.execute("""
        SELECT DISTINCT symbol_id FROM bars 
        WHERE tf = '1d' AND ts >= ? 
        ORDER BY symbol_id
    """, (int(datetime(2018, 7, 26).timestamp()),))
    symbols_with_bars = set(row['symbol_id'] for row in cursor.fetchall())
    
    # Get symbols with sentiment data
    cursor.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
    symbols_with_sentiment = set(row['symbol_id'] for row in cursor.fetchall())
    
    # Get symbols with insider trades
    cursor.execute("SELECT DISTINCT symbol_id FROM insider_trades WHERE code = 'P'")
    symbols_with_insider = set(row['symbol_id'] for row in cursor.fetchall())
    
    # Intersection
    universe = symbols_with_bars & symbols_with_sentiment & symbols_with_insider
    
    if not universe:
        print("INSUFFICIENT=1")
        return
    
    # Get insider purchases with filed dates
    placeholders = ','.join(['?'] * len(universe))
    cursor.execute(f"""
        SELECT symbol_id, filed_ts, tx_ts, value, shares
        FROM insider_trades 
        WHERE symbol_id IN ({placeholders}) AND code = 'P'
        ORDER BY filed_ts
    """, tuple(universe))
    insider_trades = cursor.fetchall()
    
    if not insider_trades:
        print("INSUFFICIENT=1")
        return
    
    # Get all sentiment data grouped by symbol
    cursor.execute("""
        SELECT symbol_id, day, mean_score 
        FROM sentiment_features 
        WHERE symbol_id IN ({})
        ORDER BY symbol_id, day
    """.format(placeholders), tuple(universe))
    sentiment_data = defaultdict(list)
    for row in cursor.fetchall():
        sentiment_data[row['symbol_id']].append((row['day'], row['mean_score']))
    
    # Pre-process sentiment for each symbol to enable rolling calculations
    sentiment_by_symbol = {}
    for sym, data in sentiment_data.items():
        days = [item[0] for item in data]
        scores = [item[1] for item in data]
        sentiment_by_symbol[sym] = (days, scores)
    
    # Get all daily bars for universe
    cursor.execute(f"""
        SELECT symbol_id, ts, close 
        FROM bars 
        WHERE symbol_id IN ({placeholders}) AND tf = '1d'
        ORDER BY symbol_id, ts
    """, tuple(universe))
    bar_data = defaultdict(list)
    for row in cursor.fetchall():
        bar_data[row['symbol_id']].append((row['ts'], row['close']))
    
    # Process each insider trade as potential entry point
    opportunities = []
    issued = []
    
    for trade in insider_trades:
        sym_id = trade['symbol_id']
        filed_ts = trade['filed_ts']
        decision_day = datetime.utcfromtimestamp(filed_ts).date()
        decision_ts = int(filed_ts)
        
        # Check if we have bar data for this symbol
        if sym_id not in bar_data:
            continue
            
        # Find bar for decision day (same day or previous trading day)
        bars = bar_data[sym_id]
        decision_bar_idx = None
        for i, (bar_ts, _) in enumerate(bars):
            bar_date = datetime.utcfromtimestamp(bar_ts).date()
            if bar_date <= decision_day:
                decision_bar_idx = i
            else:
                break
        
        if decision_bar_idx is None:
            continue
            
        decision_close = bars[decision_bar_idx][1]
        
        # Check if we have sentiment data
        if sym_id not in sentiment_by_symbol:
            continue
            
        days_list, scores_list = sentiment_by_symbol[sym_id]
        
        # Get sentiment data up to and including decision day
        sentiment_up_to = []
        for day_str, score in zip(days_list, scores_list):
            day_date = datetime.strptime(day_str, '%Y-%m-%d').date()
            if day_date <= decision_day:
                sentiment_up_to.append((day_date, score))
        
        if len(sentiment_up_to) < 21:  # Need at least 21 days for 20-day average + 5-day MA
            continue
            
        # Sort by date
        sentiment_up_to.sort(key=lambda x: x[0])
        
        # Get 20-day average
        last_20 = [item[1] for item in sentiment_up_to[-20:]]
        avg_20 = sum(last_20) / 20
        
        # Get 5-day MAs
        last_5_today = [item[1] for item in sentiment_up_to[-5:]]
        last_5_yesterday = [item[1] for item in sentiment_up_to[-6:-1]]  # Previous 5 days
        
        if len(last_5_today) < 5 or len(last_5_yesterday) < 5:
            continue
            
        ma_today = sum(last_5_today) / 5
        ma_yesterday = sum(last_5_yesterday) / 5
        
        # Check entry criteria
        if avg_20 < 0 and ma_today > ma_yesterday:
            # Find 21 trading days later
            future_bar_idx = decision_bar_idx + 21
            if future_bar_idx < len(bars):
                future_close = bars[future_bar_idx][1]
                hit = 1 if future_close > decision_close else 0
                issued.append({
                    'sym': sym_id,
                    'day': decision_day,
                    'hit': hit,
                    'ts': decision_ts
                })
        
        opportunities.append(decision_ts)
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Split into development and sealed eras (most recent 20% as sealed)
    all_ts = sorted([o['ts'] for o in issued])
    cutoff_idx = int(len(all_ts) * 0.8)
    cutoff_ts = all_ts[cutoff_idx]
    
    dev_issued = [o for o in issued if o['ts'] < cutoff_ts]
    sealed_issued = [o for o in issued if o['ts'] >= cutoff_ts]
    
    if not dev_issued or not sealed_issued:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics
    def calc_metrics(subset_issued):
        if not subset_issued:
            return None, None, None, None, None
        
        hits = sum(o['hit'] for o in subset_issued)
        precision = hits / len(subset_issued)
        
        # Base rate (average hit rate within the subset)
        base_rate = precision  # For this binary outcome, base rate = precision in subset
        
        # Distinct days
        days = set(o['day'] for o in subset_issued)
        distinct_days = len(days)
        
        # Effective N (design effect)
        # Group by day
        day_counts = defaultdict(int)
        for o in subset_issued:
            day_counts[o['day']] += 1
        
        avg_cluster_size = len(subset_issued) / distinct_days if distinct_days > 0 else 1
        
        # Estimate ICC using variance of proportions by day
        day_hits = defaultdict(int)
        for o in subset_issued:
            if o['hit'] == 1:
                day_hits[o['day']] += 1
        
        day_props = []
        for day, count in day_counts.items():
            day_props.append(day_hits.get(day, 0) / count)
        
        if len(day_props) < 2:
            icc = 0.1  # Default assumption
        else:
            mean_prop = sum(day_props) / len(day_props)
            var_prop = sum((p - mean_prop) ** 2 for p in day_props) / (len(day_props) - 1)
            overall_prop = hits / len(subset_issued)
            total_var = overall_prop * (1 - overall_prop)
            icc = var_prop / total_var if total_var > 0 else 0.1
        
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = len(subset_issued) / design_effect if design_effect > 1 else len(subset_issued) - 0.0001
        
        return len(subset_issued), precision, base_rate, distinct_days, effective_n
    
    dev_metrics = calc_metrics(dev_issued)
    sealed_metrics = calc_metrics(sealed_issued)
    
    if not dev_metrics or not sealed_metrics:
        print("INSUFFICIENT=1")
        return
    
    issued_count, precision, base_rate, distinct_days, effective_n = dev_metrics
    sealed_precision = sealed_metrics[1]
    
    # Verify invariants
    if distinct_days > issued_count:
        print("INSUFFICIENT=1")
        return
    
    if effective_n >= issued_count:
        print("INSUFFICIENT=1")
        return
    
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
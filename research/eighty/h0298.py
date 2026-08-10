# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 297
# cycle_index: 20
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import statistics
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get symbols with >=2 years of news sentiment and insider trades
    cur.execute("""
        SELECT symbol_id FROM sentiment_features
        GROUP BY symbol_id
        HAVING JULIANDAY(MAX(day)) - JULIANDAY(MIN(day)) >= 730
        INTERSECT
        SELECT symbol_id FROM insider_trades
        GROUP BY symbol_id
        HAVING JULIANDAY(MAX(tx_ts), 'unixepoch') - JULIANDAY(MIN(tx_ts), 'unixepoch') >= 730
    """)
    universe = [row[0] for row in cur.fetchall()]
    
    if not universe:
        print("INSUFFICIENT=1")
        return
    
    # Get all potential decision points (symbol_id, ts) from prediction_outcomes with horizon=60
    cur.execute("""
        SELECT symbol_id, ts, up FROM prediction_outcomes
        WHERE horizon = 60
    """)
    outcomes = cur.fetchall()
    
    if not outcomes:
        print("INSUFFICIENT=1")
        return
    
    # Get all insider purchases with filing dates
    cur.execute("""
        SELECT symbol_id, filed_ts FROM insider_trades
        WHERE code = 'P'
        ORDER BY symbol_id, filed_ts
    """)
    purchases = cur.fetchall()
    
    # Get news sentiment features
    cur.execute("""
        SELECT symbol_id, day, mean_score FROM sentiment_features
        ORDER BY symbol_id, day
    """)
    sentiment_features = cur.fetchall()
    
    # Get daily bars for price declines
    cur.execute("""
        SELECT symbol_id, ts, close FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars = cur.fetchall()
    
    conn.close()
    
    # Organize data by symbol
    purchase_by_symbol = defaultdict(list)
    for sid, ts in purchases:
        purchase_by_symbol[sid].append(ts)
    
    sentiment_by_symbol = defaultdict(list)
    for sid, day, score in sentiment_features:
        sentiment_by_symbol[sid].append((day, score))
    
    bars_by_symbol = defaultdict(list)
    for sid, ts, close in bars:
        bars_by_symbol[sid].append((ts, close))
    
    # Convert sentiment days to timestamps for easier processing
    def day_to_ts(day_str):
        from datetime import datetime
        return int(datetime.strptime(day_str, '%Y-%m-%d').timestamp())
    
    # Build sentiment timestamps
    sentiment_ts_by_symbol = defaultdict(list)
    for sid, day_list in sentiment_by_symbol.items():
        for day, score in day_list:
            ts = day_to_ts(day)
            sentiment_ts_by_symbol[sid].append((ts, score))
        sentiment_ts_by_symbol[sid].sort()
    
    # Process all decision points
    opportunities = []
    for sid, ts, up in outcomes:
        if sid not in universe:
            continue
        
        # Check condition 1: >=3 purchases in last 10 trading days
        purchases_for_symbol = purchase_by_symbol.get(sid, [])
        # Filter purchases with filed_ts <= ts (as-of discipline)
        recent_purchases = [p_ts for p_ts in purchases_for_symbol if p_ts <= ts]
        
        # Get trading days from bars
        symbol_bars = bars_by_symbol.get(sid, [])
        trading_days = [bar_ts for bar_ts, _ in symbol_bars if bar_ts <= ts]
        if len(trading_days) < 10:
            continue
        
        # Get last 10 trading days
        last_10_days = trading_days[-10:]
        purchase_count = sum(1 for p_ts in recent_purchases if p_ts in last_10_days)
        
        if purchase_count < 3:
            continue
        
        # Check condition 2: 20-day moving average of sentiment in bottom 1% of 2-year distribution
        symbol_sentiment = sentiment_ts_by_symbol.get(sid, [])
        # Filter sentiment up to ts
        sentiment_up_to_ts = [(s_ts, score) for s_ts, score in symbol_sentiment if s_ts <= ts]
        if len(sentiment_up_to_ts) < 20:
            continue
        
        # Calculate 20-day moving average
        scores_last_20 = [score for _, score in sentiment_up_to_ts[-20:]]
        current_ma = sum(scores_last_20) / len(scores_last_20)
        
        # Get 2-year window (730 days)
        two_year_start = ts - 730 * 24 * 3600
        sentiment_2yr = [(s_ts, score) for s_ts, score in symbol_sentiment 
                        if two_year_start <= s_ts <= ts]
        
        if len(sentiment_2yr) < 100:  # Need reasonable sample for percentile
            continue
        
        # Calculate all 20-day MAs in 2-year window
        ma_values = []
        for i in range(19, len(sentiment_2yr)):
            window_scores = [score for _, score in sentiment_2yr[i-19:i+1]]
            ma_values.append(sum(window_scores) / len(window_scores))
        
        if not ma_values:
            continue
        
        # Calculate 1st percentile
        sorted_ma = sorted(ma_values)
        percentile_index = int(0.01 * len(sorted_ma))
        if percentile_index >= len(sorted_ma):
            percentile_index = len(sorted_ma) - 1
        bottom_1pct = sorted_ma[percentile_index]
        
        if current_ma > bottom_1pct:
            continue
        
        # Check condition 3: >=30% decline over past 60 days
        symbol_prices = bars_by_symbol.get(sid, [])
        prices_up_to_ts = [(p_ts, close) for p_ts, close in symbol_prices if p_ts <= ts]
        if len(prices_up_to_ts) < 60:
            continue
        
        current_price = prices_up_to_ts[-1][1]
        price_60_days_ago = prices_up_to_ts[-60][1]
        
        if price_60_days_ago <= 0:
            continue
            
        decline = (current_price - price_60_days_ago) / price_60_days_ago
        if decline >= -0.30:
            continue
        
        # All conditions met, issue call
        opportunities.append((sid, ts, up))
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Split into training and held-out (most recent 20%)
    all_ts = sorted(set(ts for _, ts, _ in opportunities))
    cutoff_index = int(0.8 * len(all_ts))
    cutoff_ts = all_ts[cutoff_index] if cutoff_index < len(all_ts) else all_ts[-1]
    
    training = [(sid, ts, up) for sid, ts, up in opportunities if ts < cutoff_ts]
    sealed = [(sid, ts, up) for sid, ts, up in opportunities if ts >= cutoff_ts]
    
    # Calculate metrics
    total_issued = len(opportunities)
    
    # Count independent observations (symbol, day)
    days_issued = set()
    for _, ts, _ in opportunities:
        from datetime import datetime
        day = datetime.utcfromtimestamp(ts).date()
        days_issued.add(day)
    
    distinct_days = len(days_issued)
    
    # Calculate base rate and precision
    up_count = sum(up for _, _, up in opportunities)
    base_rate = up_count / total_issued if total_issued > 0 else 0
    
    # Calculate design effect (intra-cluster correlation by day)
    if distinct_days > 0:
        # Group by day
        day_groups = defaultdict(list)
        for _, ts, up in opportunities:
            from datetime import datetime
            day = datetime.utcfromtimestamp(ts).date()
            day_groups[day].append(up)
        
        # Calculate ICC
        group_means = []
        group_sizes = []
        for day, ups in day_groups.items():
            group_means.append(statistics.mean(ups))
            group_sizes.append(len(ups))
        
        overall_mean = base_rate
        total_n = sum(group_sizes)
        
        # Between-group variance
        if len(group_means) > 1:
            between_var = statistics.variance(group_means)
        else:
            between_var = 0
        
        # Within-group variance (pooled)
        within_var = 0
        for day, ups in day_groups.items():
            if len(ups) > 1:
                within_var += statistics.variance(ups) * (len(ups) - 1)
        within_var /= (total_n - len(group_means))
        
        # ICC
        if within_var + between_var > 0:
            icc = between_var / (within_var + between_var)
        else:
            icc = 0
        
        # Design effect
        avg_cluster_size = statistics.mean(group_sizes)
        design_effect = 1 + (avg_cluster_size - 1) * icc
        
        effective_n = total_issued / design_effect if design_effect > 0 else total_issued
    else:
        effective_n = 0
    
    # Held-out precision
    if sealed:
        sealed_up = sum(up for _, _, up in sealed)
        sealed_precision = sealed_up / len(sealed)
    else:
        sealed_precision = 0
    
    # Print results
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={base_rate}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.2f}")

if __name__ == "__main__":
    main()
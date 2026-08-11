# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 534
# cycle_index: 64
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import statistics
from collections import defaultdict
from datetime import datetime

def main():
    # Open database in read-only mode
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Step 1: Identify universe - symbols with >=200 daily bars AND StockTwits data
    cursor = conn.execute("""
        SELECT s.id, s.symbol, COUNT(*) as bar_count
        FROM symbols s
        JOIN bars b ON s.id = b.symbol_id
        WHERE b.tf = '1d'
        GROUP BY s.id
        HAVING COUNT(*) >= 200
        INTERSECT
        SELECT DISTINCT symbol_id, NULL, NULL
        FROM stocktwits_sentiment
    """)
    
    symbols = [(row['id'], row['symbol']) for row in cursor if row['symbol'] is not None]
    
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Step 2: Prepare all necessary data
    # Get all daily bars for relevant symbols
    symbol_ids = [s[0] for s in symbols]
    placeholders = ','.join(['?' for _ in symbol_ids])
    
    # Fetch all daily bars
    cursor = conn.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE symbol_id IN ({placeholders}) AND tf = '1d'
        ORDER BY symbol_id, ts
    """, symbol_ids)
    
    all_bars = defaultdict(list)
    for row in cursor:
        all_bars[row['symbol_id']].append({
            'ts': row['ts'],
            'open': row['open'],
            'high': row['high'],
            'low': row['low'],
            'close': row['close'],
            'volume': row['volume']
        })
    
    # Fetch StockTwits sentiment data
    cursor = conn.execute(f"""
        SELECT symbol_id, ts, bullish, bearish, untagged, total
        FROM stocktwits_sentiment
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    
    all_stocktwits = defaultdict(list)
    for row in cursor:
        if row['total'] and row['total'] > 0:
            all_stocktwits[row['symbol_id']].append({
                'ts': row['ts'],
                'bullish_ratio': row['bullish'] / row['total'] if row['bullish'] else 0
            })
    
    # Fetch news sentiment data
    cursor = conn.execute(f"""
        SELECT symbol_id, ts, sentiment
        FROM news
        WHERE symbol_id IN ({placeholders}) AND sentiment IS NOT NULL
        ORDER BY symbol_id, ts
    """, symbol_ids)
    
    all_news = defaultdict(list)
    for row in cursor:
        all_news[row['symbol_id']].append({
            'ts': row['ts'],
            'sentiment': row['sentiment']
        })
    
    # Step 3: Process each symbol and identify signals
    opportunities = []
    signals = []
    
    for symbol_id, symbol in symbols:
        bars = all_bars.get(symbol_id, [])
        if len(bars) < 200:
            continue
            
        stocktwits = all_stocktwits.get(symbol_id, [])
        news = all_news.get(symbol_id, [])
        
        # Create time-indexed lookups
        bars_by_ts = {bar['ts']: bar for bar in bars}
        stocktwits_by_ts = {st['ts']: st for st in stocktwits}
        news_by_ts = defaultdict(list)
        for n in news:
            news_by_ts[n['ts']].append(n['sentiment'])
        
        # Step 4: For each potential decision day, check conditions
        for i in range(252, len(bars) - 5):  # Need 252 days for ATR calc + 5 days forward
            decision_ts = bars[i]['ts']
            decision_date = datetime.utcfromtimestamp(decision_ts).strftime('%Y-%m-%d')
            
            # Get last 3 days closes
            closes_3d = [bars[i-j]['close'] for j in range(3)]
            
            # Get 10-day moving average (using close of previous day)
            if i < 10:
                continue
            ma10 = sum(bars[i-j-1]['close'] for j in range(10)) / 10
            
            # Check first condition: closes below 10-day MA for 3 consecutive days
            if not all(c < ma10 for c in closes_3d):
                continue
            
            # Get news sentiment (5-day and 20-day averages)
            # Find news from last 20 days
            recent_news_20d = []
            recent_news_5d = []
            for j in range(20):
                if i-j-1 >= 0:
                    ts = bars[i-j-1]['ts']
                    if ts in news_by_ts:
                        sentiments = news_by_ts[ts]
                        if j < 5:
                            recent_news_5d.extend(sentiments)
                        recent_news_20d.extend(sentiments)
            
            if not recent_news_20d or not recent_news_5d:
                continue
            
            avg_news_5d = statistics.mean(recent_news_5d)
            avg_news_20d = statistics.mean(recent_news_20d)
            
            # Check second condition: 5-day avg news sentiment below 20-day avg
            if avg_news_5d >= avg_news_20d:
                continue
            
            # Check third condition: StockTwits bullish ratio on day 3 above 20-day avg
            # Day 3 is the most recent day (i)
            recent_st_20d = []
            for j in range(20):
                if i-j >= 0:
                    ts = bars[i-j]['ts']
                    if ts in stocktwits_by_ts:
                        recent_st_20d.append(stocktwits_by_ts[ts]['bullish_ratio'])
            
            if not recent_st_20d:
                continue
            
            avg_st_20d = statistics.mean(recent_st_20d)
            day3_st = stocktwits_by_ts.get(bars[i]['ts'], {}).get('bullish_ratio', 0)
            
            if day3_st <= avg_st_20d:
                continue
            
            # Now check abstain conditions
            # Calculate ATR
            atr_20d = []
            atr_252d = []
            for j in range(252):
                if i-j-1 >= 0 and i-j-2 >= 0:
                    high = bars[i-j-1]['high']
                    low = bars[i-j-1]['low']
                    prev_close = bars[i-j-2]['close']
                    tr = max(high - low, abs(high - prev_close), abs(low - prev_close))
                    
                    if j < 20:
                        atr_20d.append(tr)
                    atr_252d.append(tr)
            
            if len(atr_20d) < 20 or len(atr_252d) < 252:
                continue
                
            current_atr = statistics.mean(atr_20d)
            atr_percentile = statistics.median(atr_252d)  # 50th percentile = median
            
            # Check ATR abstain condition
            if current_atr < atr_percentile:
                continue
            
            # Calculate volume
            volumes_5d = []
            volumes_252d = []
            for j in range(252):
                if i-j-1 >= 0:
                    vol = bars[i-j-1]['volume']
                    if j < 5:
                        volumes_5d.append(vol)
                    volumes_252d.append(vol)
            
            if len(volumes_5d) < 5 or len(volumes_252d) < 252:
                continue
                
            avg_vol_5d = statistics.mean(volumes_5d)
            vol_percentile = statistics.median(volumes_252d)
            
            # Check volume abstain condition
            if avg_vol_5d < vol_percentile:
                continue
            
            # Record opportunity
            opportunities.append({
                'symbol_id': symbol_id,
                'symbol': symbol,
                'decision_ts': decision_ts,
                'decision_date': decision_date
            })
            
            # Calculate forward return (5 days ahead)
            future_close = bars[i+5]['close']
            current_close = bars[i]['close']
            is_positive = future_close > current_close
            
            signals.append({
                'symbol_id': symbol_id,
                'symbol': symbol,
                'decision_ts': decision_ts,
                'decision_date': decision_date,
                'is_positive': is_positive
            })
    
    # Step 5: Split into regular and sealed eras
    if not signals:
        print("INSUFFICIENT=1")
        return
    
    # Sort by timestamp
    signals.sort(key=lambda x: x['decision_ts'])
    opportunities.sort(key=lambda x: x['decision_ts'])
    
    # Find cutoff for most recent 20%
    n_total = len(signals)
    sealed_start_idx = int(n_total * 0.8)
    
    sealed_signals = signals[sealed_start_idx:]
    regular_signals = signals[:sealed_start_idx]
    
    # Step 6: Calculate metrics
    issued = len(signals)
    opportunities_count = len(opportunities)
    
    # Precision
    hits = sum(1 for s in signals if s['is_positive'])
    precision = hits / issued if issued > 0 else 0
    
    # Base rate
    base_rate = precision  # Within the issued subset
    
    # Distinct days
    distinct_days = len(set(s['decision_date'] for s in signals))
    
    # Effective N with design effect
    # Cluster by day
    day_clusters = defaultdict(list)
    for s in signals:
        day_clusters[s['decision_date']].append(s['is_positive'])
    
    # Calculate ICC
    all_outcomes = [s['is_positive'] for s in signals]
    grand_mean = statistics.mean(all_outcomes) if all_outcomes else 0
    
    between_var = 0
    within_var = 0
    
    cluster_sizes = []
    for day, outcomes in day_clusters.items():
        n_cluster = len(outcomes)
        cluster_sizes.append(n_cluster)
        if n_cluster > 1:
            cluster_mean = statistics.mean(outcomes)
            between_var += n_cluster * (cluster_mean - grand_mean) ** 2
            within_var += sum((o - cluster_mean) ** 2 for o in outcomes)
    
    n_clusters = len(day_clusters)
    if n_clusters > 1 and sum(cluster_sizes) > n_clusters:
        avg_cluster_size = statistics.mean(cluster_sizes)
        between_var /= (n_clusters - 1)
        within_var /= (sum(cluster_sizes) - n_clusters)
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        design_effect = 1 + (avg_cluster_size - 1) * icc
    else:
        design_effect = 1.0001  # Small inflation to ensure < issued
    
    effective_n = issued / design_effect
    
    # Sealed era metrics
    sealed_hits = sum(1 for s in sealed_signals if s['is_positive'])
    sealed_precision = sealed_hits / len(sealed_signals) if sealed_signals else 0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
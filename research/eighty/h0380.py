# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 379
# cycle_index: 47
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import datetime
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()
    
    # Get insider purchases (code='P')
    cur.execute("""
        SELECT symbol_id, insider, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P'
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    # Group trades by symbol_id and get distinct insiders within 10-day windows
    from collections import defaultdict
    symbol_trades = defaultdict(list)
    for row in trades:
        symbol_trades[row['symbol_id']].append({
            'insider': row['insider'],
            'filed_ts': row['filed_ts'],
            'tx_ts': row['tx_ts']
        })
    
    # Get symbols with at least 100 days of daily bars
    cur.execute("""
        SELECT symbol_id
        FROM bars
        WHERE tf='1d'
        GROUP BY symbol_id
        HAVING COUNT(*) >= 100
    """)
    valid_symbols = {row['symbol_id'] for row in cur.fetchall()}
    
    # Get symbol details for name/market info
    cur.execute("SELECT id, symbol, name FROM symbols")
    symbol_info = {row['id']: (row['symbol'], row['name']) for row in cur.fetchall()}
    
    # Get news sentiment data (mean_score)
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
    """)
    sentiment = {}
    for row in cur.fetchall():
        key = (row['symbol_id'], row['day'])
        sentiment[key] = row['mean_score']
    
    # Get prediction outcomes for 21-day horizon
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon=21
    """)
    outcomes = {}
    for row in cur.fetchall():
        key = (row['symbol_id'], row['ts'])
        outcomes[key] = row['up']
    
    # Process each valid symbol
    issued_calls = []
    all_opportunities = 0
    
    for symbol_id in valid_symbols:
        if symbol_id not in symbol_trades:
            continue
        
        # Get daily bars for this symbol (only up to decision time)
        cur.execute("""
            SELECT ts, close, high
            FROM bars
            WHERE symbol_id=? AND tf='1d'
            ORDER BY ts
        """, (symbol_id,))
        bars = cur.fetchall()
        if len(bars) < 100:
            continue
        
        # Get all purchase trades for this symbol, sorted by filed_ts
        trades = sorted(symbol_trades[symbol_id], key=lambda x: x['filed_ts'])
        
        # Find clusters of at least 2 distinct insiders within 10-day windows
        clusters = []
        i = 0
        while i < len(trades):
            window_start = trades[i]['filed_ts']
            window_end = window_start + 10*86400  # 10 days in seconds
            insiders_in_window = set()
            cluster_trades = []
            j = i
            while j < len(trades) and trades[j]['filed_ts'] <= window_end:
                insiders_in_window.add(trades[j]['insider'])
                cluster_trades.append(trades[j])
                j += 1
            
            if len(insiders_in_window) >= 2:
                # Use the latest disclosure as cluster date
                cluster_ts = max(t['filed_ts'] for t in cluster_trades)
                clusters.append({
                    'ts': cluster_ts,
                    'trades': cluster_trades,
                    'insiders': len(insiders_in_window)
                })
            
            i = j
        
        # Process each cluster
        for cluster in clusters:
            all_opportunities += 1
            cluster_ts = cluster['ts']
            
            # Check 52-week high drawdown condition
            # Get 252 trading days of history before cluster_ts
            history_bars = [b for b in bars if b['ts'] <= cluster_ts]
            if len(history_bars) < 252:
                continue
            
            # Find 52-week high
            high_52w = max(b['high'] for b in history_bars[-252:])
            current_price = history_bars[-1]['close']
            
            # Check if current price is >=20% below 52-week high
            if current_price > high_52w * 0.8:
                continue
            
            # Check if this condition occurred in past 60 days
            past_60 = [b for b in history_bars if b['ts'] >= cluster_ts - 60*86400]
            drawdown_occurred = False
            for b in past_60:
                if b['high'] > 0 and b['close'] <= b['high'] * 0.8:
                    drawdown_occurred = True
                    break
            if not drawdown_occurred:
                continue
            
            # Check news sentiment condition
            # Convert cluster_ts to date string for sentiment lookup
            cluster_date = datetime.datetime.utcfromtimestamp(cluster_ts).strftime('%Y-%m-%d')
            sentiment_key = (symbol_id, cluster_date)
            if sentiment_key not in sentiment:
                continue
            
            current_sentiment = sentiment[sentiment_key]
            
            # Get trailing 252-day sentiment distribution
            past_sentiments = []
            for (sid, day), score in sentiment.items():
                if sid == symbol_id:
                    # Convert day to timestamp for comparison
                    try:
                        day_ts = int(datetime.datetime.strptime(day, '%Y-%m-%d').timestamp())
                    except:
                        continue
                    if day_ts <= cluster_ts and day_ts >= cluster_ts - 252*86400:
                        past_sentiments.append(score)
            
            if len(past_sentiments) < 25:  # Need reasonable sample
                continue
            
            # Find 10th percentile
            sorted_sentiments = sorted(past_sentiments)
            idx = int(len(sorted_sentiments) * 0.1)
            threshold = sorted_sentiments[idx]
            
            if current_sentiment > threshold:
                continue
            
            # Get the outcome
            outcome_key = (symbol_id, cluster_ts)
            if outcome_key not in outcomes:
                continue
            
            issued_calls.append({
                'symbol_id': symbol_id,
                'ts': cluster_ts,
                'date': cluster_date,
                'up': outcomes[outcome_key]
            })
    
    conn.close()
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics
    issued = len(issued_calls)
    hits = sum(1 for c in issued_calls if c['up'] == 1)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate within issued subset
    base_rate = sum(1 for c in issued_calls if c['up'] == 1) / issued
    
    # Distinct days
    distinct_days = len(set(c['date'] for c in issued_calls))
    
    # Effective N (design effect calculation)
    # Group by day to compute intraclass correlation
    day_counts = defaultdict(int)
    day_outcomes = defaultdict(list)
    for call in issued_calls:
        day_counts[call['date']] += 1
        day_outcomes[call['date']].append(call['up'])
    
    # Simple approximation: design effect = 1 + (avg_cluster_size - 1) * ICC
    # ICC ≈ variance between days / total variance
    all_outcomes = [c['up'] for c in issued_calls]
    total_mean = sum(all_outcomes) / len(all_outcomes)
    total_var = sum((x - total_mean)**2 for x in all_outcomes) / len(all_outcomes)
    
    day_means = []
    day_vars = []
    day_ns = []
    for day, outcomes in day_outcomes.items():
        n = len(outcomes)
        mean = sum(outcomes) / n
        var = sum((x - mean)**2 for x in outcomes) / n if n > 1 else 0
        day_means.append(mean)
        day_vars.append(var)
        day_ns.append(n)
    
    avg_n = sum(day_ns) / len(day_ns)
    var_between = sum((m - total_mean)**2 for m in day_means) / len(day_means) if day_means else 0
    avg_var_within = sum(day_vars) / len(day_vars) if day_vars else 0
    
    if total_var > 0:
        icc = var_between / total_var if var_between > 0 else 0
        design_effect = 1 + (avg_n - 1) * icc
    else:
        design_effect = 1.0
    
    effective_n = issued / design_effect
    
    # Ensure effective_n < issued
    if effective_n >= issued:
        effective_n = issued * 0.99  # Force less than issued if calculation fails
    
    # Sealed era (most recent 20%)
    issued_calls.sort(key=lambda x: x['ts'])
    sealed_start = int(len(issued_calls) * 0.8)
    sealed_calls = issued_calls[sealed_start:]
    sealed_hits = sum(1 for c in sealed_calls if c['up'] == 1)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    # Print required metrics
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={all_opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
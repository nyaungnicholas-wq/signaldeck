# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 494
# cycle_index: 24
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def get_symbols_with_enough_data(conn):
    query = """
    SELECT symbol_id
    FROM bars b
    JOIN sentiment_features s ON b.symbol_id = s.symbol_id
    WHERE b.tf = '1d'
    GROUP BY b.symbol_id
    HAVING COUNT(DISTINCT DATE(b.ts, 'unixepoch')) >= 252
       AND COUNT(DISTINCT s.day) >= 252
    """
    return [row[0] for row in conn.execute(query)]

def get_insider_purchases(conn, symbols):
    if not symbols:
        return []
    placeholders = ','.join('?' * len(symbols))
    query = f"""
    SELECT symbol_id, filed_ts
    FROM insider_trades
    WHERE code = 'P' AND symbol_id IN ({placeholders})
    ORDER BY filed_ts
    """
    return conn.execute(query, symbols).fetchall()

def get_all_daily_bars(conn, symbols):
    if not symbols:
        return {}
    placeholders = ','.join('?' * len(symbols))
    query = f"""
    SELECT symbol_id, ts, close, volume
    FROM bars
    WHERE tf = '1d' AND symbol_id IN ({placeholders})
    ORDER BY symbol_id, ts
    """
    result = defaultdict(list)
    for row in conn.execute(query, symbols):
        result[row[0]].append((row[1], row[2], row[3]))
    return result

def get_all_sentiment(conn, symbols):
    if not symbols:
        return {}
    placeholders = ','.join('?' * len(symbols))
    query = f"""
    SELECT symbol_id, day, mean_score
    FROM sentiment_features
    WHERE symbol_id IN ({placeholders})
    ORDER BY symbol_id, day
    """
    result = defaultdict(list)
    for row in conn.execute(query, symbols):
        result[row[0]].append((row[1], row[2]))
    return result

def is_above_200ma(bars, date_ts):
    if len(bars) < 200:
        return None
    closes = [close for ts, close, _ in bars if ts < date_ts]
    if len(closes) < 200:
        return None
    ma200 = sum(closes[-200:]) / 200
    last_close = closes[-1]
    return last_close > ma200

def get_20day_avg_volume(bars, date_ts):
    if len(bars) < 20:
        return None
    volumes = [vol for ts, _, vol in bars if ts < date_ts]
    if len(volumes) < 20:
        return None
    return sum(volumes[-20:]) / 20

def compute_sentiment_signals(sentiment, date_str):
    if len(sentiment) < 25:
        return None
    scores = [s for day, s in sentiment if day <= date_str]
    if len(scores) < 25:
        return None
    # Standardize using all available history up to each point
    standardized = []
    for i in range(len(scores)):
        window = scores[:i+1]
        mean = sum(window) / len(window)
        var = sum((x - mean) ** 2 for x in window) / len(window)
        std = math.sqrt(var) if var > 0 else 1.0
        z = (scores[i] - mean) / std if std > 0 else 0.0
        standardized.append(z)
    
    if len(standardized) < 20:
        return None
    
    # 5-day moving average of standardized scores
    ma5 = []
    for i in range(4, len(standardized)):
        ma5.append(sum(standardized[i-4:i+1]) / 5)
    
    if len(ma5) < 20:
        return None
    
    current_ma5 = ma5[-1]
    past_ma5 = ma5[-20]
    
    # Increase in standard deviations
    if len(standardized) < 20:
        return None
    hist_mean = sum(standardized[:-1]) / len(standardized[:-1]) if len(standardized) > 1 else 0
    hist_var = sum((x - hist_mean) ** 2 for x in standardized[:-1]) / len(standardized[:-1]) if len(standardized) > 1 else 1
    hist_std = math.sqrt(hist_var) if hist_var > 0 else 1
    
    increase = (current_ma5 - past_ma5) / hist_std if hist_std > 0 else 0
    return increase >= 0.5

def get_forward_return(bars, entry_ts, horizon_days=21):
    if not bars:
        return None
    future_bars = [ts for ts, _, _ in bars if ts > entry_ts]
    if len(future_bars) < horizon_days:
        return None
    # Simple return from entry to horizon_days later
    entry_close = None
    for ts, close, _ in bars:
        if ts == entry_ts:
            entry_close = close
            break
    if entry_close is None or entry_close == 0:
        return None
    
    target_ts = future_bars[horizon_days - 1]
    target_close = None
    for ts, close, _ in bars:
        if ts == target_ts:
            target_close = close
            break
    if target_close is None:
        return None
    
    return (target_close - entry_close) / entry_close

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        conn.row_factory = sqlite3.Row
        
        symbols = get_symbols_with_enough_data(conn)
        if not symbols:
            print("INSUFFICIENT=1")
            return 0
        
        purchases = get_insider_purchases(conn, symbols)
        if not purchases:
            print("INSUFFICIENT=1")
            return 0
        
        all_bars = get_all_daily_bars(conn, symbols)
        all_sentiment = get_all_sentiment(conn, symbols)
        
        opportunities = []
        for symbol_id, filed_ts in purchases:
            # Get bars for this symbol up to filing date
            bars = all_bars.get(symbol_id, [])
            sentiment = all_sentiment.get(symbol_id, [])
            
            # Convert filed_ts to date string YYYY-MM-DD
            import datetime
            date_str = datetime.datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
            
            # Check 200-day MA abstention
            above_ma = is_above_200ma(bars, filed_ts)
            if above_ma:
                continue
            
            # Check volume condition
            avg_vol = get_20day_avg_volume(bars, filed_ts)
            if avg_vol is None:
                continue
            
            # Get current volume
            current_vol = None
            for ts, _, vol in bars:
                if ts == filed_ts:
                    current_vol = vol
                    break
            if current_vol is None or current_vol <= avg_vol:
                continue
            
            # Check sentiment condition
            if not compute_sentiment_signals(sentiment, date_str):
                continue
            
            # Get forward return
            fwd_return = get_forward_return(bars, filed_ts, 21)
            if fwd_return is None:
                continue
            
            hit = 1 if fwd_return > 0 else 0
            opportunities.append((filed_ts, hit))
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return 0
        
        # Sort by time
        opportunities.sort(key=lambda x: x[0])
        
        # Split into sealed era (most recent 20%) and the rest
        n = len(opportunities)
        split_idx = int(n * 0.8)
        main_sample = opportunities[:split_idx]
        sealed_sample = opportunities[split_idx:]
        
        # Compute metrics for main sample
        issued_main = len(main_sample)
        if issued_main == 0:
            print("INSUFFICIENT=1")
            return 0
        
        hits_main = sum(hit for _, hit in main_sample)
        precision_main = hits_main / issued_main
        base_rate_main = hits_main / issued_main  # Same as precision since subset is issued calls
        
        # Distinct days
        distinct_days = len(set(datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d') 
                               for ts, _ in main_sample))
        
        # Effective N calculation
        # Group by day
        day_groups = defaultdict(list)
        for ts, hit in main_sample:
            day = datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
            day_groups[day].append(hit)
        
        # Compute ICC for binary outcomes
        n_clusters = len(day_groups)
        if n_clusters <= 1:
            design_effect = 1.0
        else:
            # Overall mean
            p = hits_main / issued_main
            overall_var = p * (1 - p) if p > 0 and p < 1 else 0.0001
            
            # Cluster means and variances
            cluster_means = [sum(hits) / len(hits) for hits in day_groups.values()]
            cluster_sizes = [len(hits) for hits in day_groups.values()]
            avg_cluster_size = issued_main / n_clusters
            
            # Variance of cluster means
            mean_of_means = sum(cluster_means) / n_clusters
            var_of_means = sum((m - mean_of_means) ** 2 for m in cluster_means) / n_clusters
            
            # ICC
            icc = var_of_means / overall_var if overall_var > 0 else 0
            
            # Design effect
            design_effect = 1 + (avg_cluster_size - 1) * icc
        
        effective_n = issued_main / design_effect if design_effect > 0 else 0
        
        # Sealed era precision
        issued_sealed = len(sealed_sample)
        hits_sealed = sum(hit for _, hit in sealed_sample)
        precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0
        
        # Print results
        print(f"ISSUED={issued_main}")
        print(f"OPPORTUNITIES={n}")
        print(f"PRECISION={precision_main:.6f}")
        print(f"BASE_RATE={base_rate_main:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={precision_sealed:.6f}")
        
        return 0
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

if __name__ == "__main__":
    main()
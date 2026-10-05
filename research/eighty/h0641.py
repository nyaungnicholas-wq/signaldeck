# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 640
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict
from datetime import datetime, timedelta

def connect_ro():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def get_trading_days(conn):
    """Get all trading days from 1d bars."""
    cur = conn.execute("""
        SELECT DISTINCT date(ts, 'unixepoch') as day
        FROM bars
        WHERE tf = '1d'
        ORDER BY day
    """)
    return [row[0] for row in cur.fetchall()]

def get_symbols_with_coverage(conn, start_date='2019-01-01'):
    """Get symbols with StockTwits and news coverage since 2019."""
    cur = conn.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.active = 1
        AND EXISTS (
            SELECT 1 FROM stocktwits_sentiment st
            WHERE st.symbol_id = s.id
            AND date(st.ts, 'unixepoch') >= ?
        )
        AND EXISTS (
            SELECT 1 FROM sentiment_features sf
            WHERE sf.symbol_id = s.id
            AND sf.day >= ?
        )
    """, (start_date, start_date))
    return cur.fetchall()

def get_daily_stocktwits(conn, symbol_id):
    """Get daily bullish count for a symbol."""
    cur = conn.execute("""
        SELECT date(ts, 'unixepoch') as day, SUM(bullish) as bullish
        FROM stocktwits_sentiment
        WHERE symbol_id = ?
        GROUP BY day
        ORDER BY day
    """, (symbol_id,))
    return {row[0]: row[1] for row in cur.fetchall()}

def get_daily_news_sentiment(conn, symbol_id):
    """Get daily news sentiment from sentiment_features."""
    cur = conn.execute("""
        SELECT day, mean_score, n_all
        FROM sentiment_features
        WHERE symbol_id = ?
        ORDER BY day
    """, (symbol_id,))
    return {row[0]: (row[1], row[2]) for row in cur.fetchall()}

def get_daily_bars(conn, symbol_id):
    """Get daily OHLCV for a symbol."""
    cur = conn.execute("""
        SELECT date(ts, 'unixepoch') as day, open, high, low, close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY day
    """, (symbol_id,))
    return {row[0]: (row[1], row[2], row[3], row[4], row[5]) for row in cur.fetchall()}

def get_labels(conn, symbol_id, horizon='5d'):
    """Get 5-day forward return labels."""
    cur = conn.execute("""
        SELECT date(ts, 'unixepoch') as day, fwd_return, up
        FROM prediction_outcomes
        WHERE symbol_id = ? AND horizon = ?
        ORDER BY day
    """, (symbol_id, horizon))
    return {row[0]: (row[1], row[2]) for row in cur.fetchall()}

def compute_rolling_stats(values, window):
    """Compute rolling mean and std for a list of values aligned to days."""
    n = len(values)
    means = [None] * n
    stds = [None] * n
    for i in range(window, n):
        window_vals = values[i-window:i]
        mean = sum(window_vals) / window
        variance = sum((x - mean) ** 2 for x in window_vals) / window
        std = math.sqrt(variance) if variance > 0 else 0
        means[i] = mean
        stds[i] = std
    return means, stds

def main():
    conn = connect_ro()
    
    # Get all trading days
    all_days = get_trading_days(conn)
    if not all_days:
        print("INSUFFICIENT=1")
        return
    
    # Split: hold out most recent 20% as sealed era
    split_idx = int(len(all_days) * 0.8)
    train_days = set(all_days[:split_idx])
    sealed_days = set(all_days[split_idx:])
    
    # Get symbols with coverage since 2019
    symbols = get_symbols_with_coverage(conn)
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    all_opportunities = 0
    all_issued = []
    all_issued_sealed = []
    
    for symbol_id, symbol in symbols:
        # Get daily data
        st_data = get_daily_stocktwits(conn, symbol_id)
        news_data = get_daily_news_sentiment(conn, symbol_id)
        bars_data = get_daily_bars(conn, symbol_id)
        labels = get_labels(conn, symbol_id, '5d')
        
        # Align to trading days
        common_days = sorted(set(st_data.keys()) & set(news_data.keys()) & set(bars_data.keys()) & set(labels.keys()))
        if len(common_days) < 60:
            continue
        
        # Build aligned arrays
        bullish_vals = []
        news_sentiment = []
        news_count = []
        returns = []
        volumes = []
        day_list = []
        
        for day in common_days:
            bullish_vals.append(st_data[day])
            mean_score, n_all = news_data[day]
            news_sentiment.append(mean_score)
            news_count.append(n_all)
            o, h, l, c, v = bars_data[day]
            returns.append((c - o) / o if o > 0 else 0)
            volumes.append(v)
            day_list.append(day)
        
        # Rolling stats for bullish (60-day)
        bullish_mean, bullish_std = compute_rolling_stats(bullish_vals, 60)
        
        # Rolling avg volume (20-day)
        vol_mean, _ = compute_rolling_stats(volumes, 20)
        
        # News sentiment change (Δ)
        news_delta = [None] * len(news_sentiment)
        for i in range(1, len(news_sentiment)):
            if news_sentiment[i-1] is not None and news_sentiment[i] is not None:
                news_delta[i] = news_sentiment[i] - news_sentiment[i-1]
        
        # Evaluate each day starting from day 60
        for i in range(60, len(day_list)):
            day = day_list[i]
            is_sealed = day in sealed_days
            
            all_opportunities += 1
            
            # ABSTAIN conditions
            # 1. Any news headline appears that day (n_all > 0)
            if news_count[i] > 0:
                continue
            # 2. Volume > 1.5x 20-day average
            if vol_mean[i] and volumes[i] > 1.5 * vol_mean[i]:
                continue
            # 3. Symbol lacks 60 prior days (already ensured by loop start)
            
            # ENTRY conditions
            # 1. Bullish > 3σ above 60-day mean
            if bullish_mean[i] is None or bullish_std[i] is None or bullish_std[i] == 0:
                continue
            if bullish_vals[i] <= bullish_mean[i] + 3 * bullish_std[i]:
                continue
            # 2. Same-day return > +3%
            if returns[i] <= 0.03:
                continue
            # 3. |Δnews sentiment| < 0.1
            if news_delta[i] is None or abs(news_delta[i]) >= 0.1:
                continue
            
            # Issue DOWN call
            fwd_return, up = labels[day]
            # DOWN call means we predict price goes down (fwd_return < 0)
            hit = 1 if fwd_return < 0 else 0
            
            call = (day, symbol, hit, fwd_return)
            all_issued.append(call)
            if is_sealed:
                all_issued_sealed.append(call)
    
    if not all_issued:
        print("INSUFFICIENT=1")
        return
    
    # Compute metrics
    issued_count = len(all_issued)
    hits = sum(c[2] for c in all_issued)
    precision = hits / issued_count if issued_count > 0 else 0
    
    # Base rate of predicted class (DOWN) within issued subset
    # Predicted class is DOWN, so base rate = proportion of actual DOWN in issued
    base_rate = hits / issued_count if issued_count > 0 else 0
    
    # Distinct days among issued calls
    distinct_days = len(set(c[0] for c in all_issued))
    
    # Design effect: estimate from temporal clustering
    # Group calls by day, compute variance inflation
    calls_by_day = defaultdict(int)
    for c in all_issued:
        calls_by_day[c[0]] += 1
    
    if len(calls_by_day) > 1:
        day_counts = list(calls_by_day.values())
        mean_calls = sum(day_counts) / len(day_counts)
        var_calls = sum((c - mean_calls) ** 2 for c in day_counts) / len(day_counts)
        # Design effect = 1 + (mean_cluster_size - 1) * ICC
        # Approximate ICC from day-level variance
        if mean_calls > 0:
            deff = 1 + (var_calls / mean_calls) if mean_calls > 0 else 1
            deff = max(deff, 1.01)  # Ensure > 1
        else:
            deff = 1.01
    else:
        deff = 1.01
    
    effective_n = issued_count / deff
    
    # Sealed era precision
    sealed_precision = 0
    if all_issued_sealed:
        sealed_hits = sum(c[2] for c in all_issued_sealed)
        sealed_precision = sealed_hits / len(all_issued_sealed)
    
    # Print required lines
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={all_opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
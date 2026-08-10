# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 300
# cycle_index: 23
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return

    # Get daily bars and compute 200-day SMA
    # Get sentiment features aggregated by day
    # Get prediction outcomes with 21-day horizon

    # First, get all symbols with daily bars
    c.execute("""
        SELECT DISTINCT symbol_id 
        FROM bars 
        WHERE tf = '1d'
    """)
    symbols = [row[0] for row in c.fetchall()]

    if not symbols:
        print("INSUFFICIENT=1")
        return

    # For each symbol, compute daily close prices and 200-day SMA
    # We need timestamp data to split into train/test
    symbol_prices = {}
    for sym in symbols:
        c.execute("""
            SELECT ts, close 
            FROM bars 
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym,))
        prices = c.fetchall()
        if len(prices) >= 200:
            symbol_prices[sym] = prices

    if not symbol_prices:
        print("INSUFFICIENT=1")
        return

    # Get all unique timestamps
    all_timestamps = set()
    for sym, prices in symbol_prices.items():
        for ts, _ in prices:
            all_timestamps.add(ts)

    all_timestamps = sorted(all_timestamps)
    if not all_timestamps:
        print("INSUFFICIENT=1")
        return

    # Split into train/test (80%/20% most recent)
    split_idx = int(len(all_timestamps) * 0.8)
    train_timestamps = set(all_timestamps[:split_idx])
    test_timestamps = set(all_timestamps[split_idx:])

    # Get sentiment features by day
    # We'll aggregate sentiment by day (YYYY-MM-DD)
    c.execute("""
        SELECT symbol_id, day, mean_score 
        FROM sentiment_features
        WHERE day IS NOT NULL
    """)
    sentiment_data = c.fetchall()

    # Organize by symbol and date string
    sentiment_by_sym = defaultdict(dict)
    for sym, day, score in sentiment_data:
        if score is not None and day is not None:
            sentiment_by_sym[sym][day] = float(score)

    # Get prediction outcomes with 21-day horizon
    c.execute("""
        SELECT symbol_id, ts, up 
        FROM prediction_outcomes 
        WHERE horizon = 21
    """)
    outcomes = c.fetchall()
    
    # Organize outcomes by symbol and timestamp
    outcomes_by_sym = defaultdict(dict)
    for sym, ts, up in outcomes:
        if up is not None:
            outcomes_by_sym[sym][ts] = bool(up)

    # Process signals
    issued_calls = []
    opportunities = 0

    for sym, prices in symbol_prices.items():
        price_dict = {ts: close for ts, close in prices}
        timestamps = sorted(price_dict.keys())
        
        if sym not in sentiment_by_sym:
            continue
            
        sentiment_dict = sentiment_by_sym[sym]
        
        # Compute 200-day SMA for each timestamp
        sma200 = {}
        for i, ts in enumerate(timestamps):
            if i < 199:
                continue
            window = timestamps[i-199:i+1]
            closes = [price_dict[t] for t in window]
            sma200[ts] = sum(closes) / len(closes)
        
        # Compute 60-day rolling window for sentiment
        for i, ts in enumerate(timestamps):
            opportunities += 1
            
            if ts not in sma200:
                continue
            
            close = price_dict[ts]
            sma = sma200[ts]
            
            # Get date string for sentiment lookup
            dt = datetime.utcfromtimestamp(ts)
            date_str = dt.strftime('%Y-%m-%d')
            
            if date_str not in sentiment_dict:
                continue
            
            current_sentiment = sentiment_dict[date_str]
            
            # Get last 60 days of sentiment for distribution
            recent_dates = []
            for j in range(max(0, i-59), i+1):
                t = timestamps[j]
                d = datetime.utcfromtimestamp(t).strftime('%Y-%m-%d')
                if d in sentiment_dict:
                    recent_dates.append(sentiment_dict[d])
            
            if len(recent_dates) < 60:
                continue
            
            # Compute 90th percentile threshold
            sorted_s = sorted(recent_dates)
            threshold_idx = int(len(sorted_s) * 0.9)
            threshold = sorted_s[threshold_idx]
            
            # Entry conditions
            if close < sma and current_sentiment >= threshold:
                # Check if we have outcome
                if sym in outcomes_by_sym and ts in outcomes_by_sym[sym]:
                    label = outcomes_by_sym[sym][ts]
                    issued_calls.append((ts, label, sym))

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # Sort by timestamp
    issued_calls.sort(key=lambda x: x[0])

    # Split into train and sealed era
    train_calls = [c for c in issued_calls if c[0] in train_timestamps]
    test_calls = [c for c in issued_calls if c[0] in test_timestamps]

    # Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0
        
        hits = sum(1 for _, label, _ in calls if label)
        precision = hits / len(calls) if calls else 0
        
        # Base rate within issued subset
        positive = sum(1 for _, label, _ in calls if label)
        base_rate = positive / len(calls) if calls else 0
        
        # Distinct days
        distinct_days = len(set(ts for ts, _, _ in calls))
        
        return len(calls), precision, base_rate, distinct_days

    issued, precision, base_rate, distinct_days = compute_metrics(train_calls)
    _, sealed_precision, _, _ = compute_metrics(test_calls)

    if issued == 0:
        print("INSUFFICIENT=1")
        return

    # Design effect approximation: calls per day
    calls_per_day = issued / distinct_days if distinct_days > 0 else 1
    # Conservative design effect (ICC ~0.1)
    design_effect = 1 + (calls_per_day - 1) * 0.1
    effective_n = issued / design_effect

    # Print required outputs
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

    conn.close()

if __name__ == "__main__":
    main()
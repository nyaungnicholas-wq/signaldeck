# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 371
# cycle_index: 39
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except:
        print("INSUFFICIENT=1")
        return

    try:
        # Get symbols with news
        news_symbols = set(row[0] for row in conn.execute(
            "SELECT DISTINCT symbol_id FROM news").fetchall())
        
        # Get symbols with at least 252 trading days
        symbols_with_history = set()
        for row in conn.execute(
            "SELECT symbol_id, COUNT(*) as n FROM bars WHERE tf='1d' GROUP BY symbol_id HAVING n >= 252"
        ).fetchall():
            if row[0] in news_symbols:
                symbols_with_history.add(row[0])
        
        if not symbols_with_history:
            print("INSUFFICIENT=1")
            return
        
        # Load daily bars for relevant symbols
        bars_data = {}
        for sid in symbols_with_history:
            rows = conn.execute(
                "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts",
                (sid,)).fetchall()
            if len(rows) >= 252:
                dates = [datetime.utcfromtimestamp(ts).date() for ts, _ in rows]
                closes = [close for _, close in rows]
                bars_data[sid] = (dates, closes)
        
        # Load news data
        news_data = defaultdict(list)
        for sid in symbols_with_history:
            rows = conn.execute(
                "SELECT ts, score FROM news WHERE symbol_id=? ORDER BY ts",
                (sid,)).fetchall()
            if rows:
                news_data[sid] = [(datetime.utcfromtimestamp(ts).date(), score) for ts, score in rows]
        
        conn.close()
        
        # Process each symbol
        calls = []  # list of (symbol_id, signal_date, index, close, hit)
        opportunities = 0
        
        for sid in symbols_with_history:
            if sid not in bars_data or sid not in news_data:
                continue
            
            dates, closes = bars_data[sid]
            news_items = news_data[sid]
            
            # Group news by date
            news_by_date = defaultdict(list)
            for date, score in news_items:
                news_by_date[date].append(score)
            
            # Create mapping from date to index
            date_to_idx = {date: i for i, date in enumerate(dates)}
            
            # Prepare daily sentiment aggregates
            daily_sentiment = [0.0] * len(dates)
            has_news = [False] * len(dates)
            
            for date, scores in news_by_date.items():
                if date in date_to_idx:
                    idx = date_to_idx[date]
                    daily_sentiment[idx] = sum(scores) / len(scores)
                    has_news[idx] = True
            
            # Sliding window for median
            last_issued_idx = -100  # very old index
            
            for i in range(252, len(dates) - 21):
                opportunities += 1
                date_t = dates[i]
                
                # Condition a: at least one headline today
                if not has_news[i]:
                    continue
                
                # Condition b: zero headlines in previous 40 trading days
                drought = True
                for j in range(i-40, i):
                    if has_news[j]:
                        drought = False
                        break
                if not drought:
                    continue
                
                # Condition c: sentiment above trailing 252-day median
                # Get 252-day window before today
                window = daily_sentiment[i-252:i]
                # Sort and compute median
                sorted_window = sorted(window)
                n = len(sorted_window)
                if n % 2 == 1:
                    median = sorted_window[n//2]
                else:
                    median = (sorted_window[n//2-1] + sorted_window[n//2]) / 2.0
                
                if daily_sentiment[i] <= median:
                    continue
                
                # Condition d: return <= 5%
                if i > 0:
                    ret = closes[i] / closes[i-1] - 1
                    if ret > 0.05:
                        continue
                
                # Condition e: not within 10 trading days of previous call
                if i - last_issued_idx <= 10:
                    continue
                
                # All conditions passed, issue call
                # Compute 21-day forward return
                if i + 21 < len(closes):
                    fwd_ret = closes[i+21] / closes[i] - 1
                    hit = 1 if fwd_ret > 0 else 0
                    calls.append((sid, date_t, i, closes[i], hit))
                    last_issued_idx = i
        
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Split into sealed era (most recent 20%)
        calls.sort(key=lambda x: x[1])  # sort by date
        n_calls = len(calls)
        sealed_count = math.ceil(n_calls * 0.2)
        sealed_calls = calls[-sealed_count:]
        dev_calls = calls[:-sealed_count]
        
        # Compute metrics
        issued = n_calls
        hits = sum(4 for c in calls)
        precision = hits / issued if issued > 0 else 0
        base_rate = precision  # as per instruction: base rate within issued subset
        
        sealed_hits = sum(4 for c in sealed_calls)
        sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
        
        distinct_days = len(set(c[1] for c in calls))
        
        # Compute design effect (simple: deff = issued/distinct_days if >1)
        deff = issued / distinct_days if distinct_days > 0 else 1
        if deff <= 1:
            deff = 1.0001
        effective_n = issued / deff
        
        # Print required lines
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
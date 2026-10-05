# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 533
# cycle_index: 63
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cursor = conn.cursor()
    
    # Get all symbols with daily bars
    cursor.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
    symbols_with_bars = {row[0] for row in cursor.fetchall()}
    
    # Get symbols with sentiment_features (news sentiment)
    cursor.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
    symbols_with_sentiment = {row[0] for row in cursor.fetchall()}
    
    # Get symbols with insider trades (Form 4 purchases)
    cursor.execute("""
        SELECT DISTINCT symbol_id FROM insider_trades 
        WHERE code='P'  -- purchase code
    """)
    symbols_with_insider = {row[0] for row in cursor.fetchall()}
    
    # Universe: intersection of all three
    universe = symbols_with_bars & symbols_with_sentiment & symbols_with_insider
    
    if not universe:
        print("INSUFFICIENT=1")
        return
    
    # Pre-fetch all needed data for universe symbols
    # Daily bars
    cursor.execute("""
        SELECT symbol_id, ts, close FROM bars 
        WHERE tf='1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(universe))), list(universe))
    bars_data = {}
    for row in cursor.fetchall():
        sid, ts, close = row
        if sid not in bars_data:
            bars_data[sid] = []
        bars_data[sid].append((ts, close))
    
    # Sentiment features
    cursor.execute("""
        SELECT symbol_id, day, mean_score FROM sentiment_features
        WHERE symbol_id IN ({})
        ORDER BY symbol_id, day
    """.format(','.join('?'*len(universe))), list(universe))
    sentiment_data = {}
    for row in cursor.fetchall():
        sid, day, mean_score = row
        if sid not in sentiment_data:
            sentiment_data[sid] = []
        sentiment_data[sid].append((day, mean_score))
    
    # Insider trades (Form 4 purchases)
    cursor.execute("""
        SELECT symbol_id, filed_ts FROM insider_trades
        WHERE code='P' AND symbol_id IN ({})
        ORDER BY symbol_id, filed_ts
    """.format(','.join('?'*len(universe))), list(universe))
    insider_data = {}
    for row in cursor.fetchall():
        sid, filed_ts = row
        if sid not in insider_data:
            insider_data[sid] = []
        insider_data[sid].append(filed_ts)
    
    # Get prediction outcomes with horizon=21 for the universe
    cursor.execute("""
        SELECT symbol_id, ts, up FROM prediction_outcomes
        WHERE horizon=21 AND symbol_id IN ({})
    """.format(','.join('?'*len(universe))), list(universe))
    outcomes_data = {}
    for row in cursor.fetchall():
        sid, ts, up = row
        if sid not in outcomes_data:
            outcomes_data[sid] = []
        outcomes_data[sid].append((ts, up))
    
    # Process each symbol
    opportunities = []  # (symbol_id, decision_ts, outcome_up)
    
    for sid in universe:
        if sid not in bars_data or sid not in sentiment_data or sid not in insider_data:
            continue
        
        # Convert bars to timestamp-sorted list
        bars = sorted(bars_data[sid], key=lambda x: x[0])
        # Convert sentiment to date-sorted list
        sentiment = sorted(sentiment_data[sid], key=lambda x: x[0])
        # Insider purchase dates
        insider_dates = sorted(insider_data[sid])
        
        # Create lookup dictionaries for quick access
        bars_by_ts = {ts: close for ts, close in bars}
        sentiment_by_date = {day: score for day, score in sentiment}
        
        # Compute moving averages for sentiment (20-day and 60-day)
        sentiment_dates = [day for day, _ in sentiment]
        sentiment_scores = [score for _, score in sentiment]
        
        # Compute 20-day and 60-day moving averages for sentiment
        sentiment_ma20 = {}
        sentiment_ma60 = {}
        
        for i in range(len(sentiment)):
            if i >= 19:  # Need at least 20 days for 20-day MA
                ma20 = sum(sentiment_scores[i-19:i+1]) / 20
                sentiment_ma20[sentiment_dates[i]] = ma20
            if i >= 59:  # Need at least 60 days for 60-day MA
                ma60 = sum(sentiment_scores[i-59:i+1]) / 60
                sentiment_ma60[sentiment_dates[i]] = ma60
        
        # Compute 200-day moving average for price
        price_ma200 = {}
        for i in range(len(bars)):
            if i >= 199:  # Need at least 200 days for 200-day MA
                ts, _ = bars[i]
                ma200 = sum(close for _, close in bars[i-199:i+1]) / 200
                price_ma200[ts] = ma200
        
        # Process each insider purchase date
        for insider_ts in insider_dates:
            # Convert filed_ts to date string
            insider_date = datetime.datetime.utcfromtimestamp(insider_ts).strftime('%Y-%m-%d')
            
            # Get the close price on or before this date
            # Find the most recent bar with ts <= insider_ts
            recent_bar_ts = None
            recent_bar_close = None
            for ts, close in bars:
                if ts <= insider_ts:
                    recent_bar_ts = ts
                    recent_bar_close = close
                else:
                    break
            
            if recent_bar_ts is None:
                continue
            
            # Check if we have price MA200 for this timestamp
            if recent_bar_ts not in price_ma200:
                continue
            
            ma200 = price_ma200[recent_bar_ts]
            
            # Condition: price below 200-day MA
            if not (recent_bar_close < ma200):
                continue
            
            # Get sentiment MA20 and MA60 for this date
            if insider_date not in sentiment_ma20 or insider_date not in sentiment_ma60:
                continue
            
            ma20 = sentiment_ma20[insider_date]
            ma60 = sentiment_ma60[insider_date]
            
            # Check crossover condition: MA20 > MA60
            if not (ma20 > ma60):
                continue
            
            # Need previous day's MA20 and MA60 to check crossover
            # Find previous day in sentiment data
            prev_date = None
            for i, day in enumerate(sentiment_dates):
                if day == insider_date and i > 0:
                    prev_date = sentiment_dates[i-1]
                    break
            
            if prev_date is None:
                continue
            
            if prev_date not in sentiment_ma20 or prev_date not in sentiment_ma60:
                continue
            
            prev_ma20 = sentiment_ma20[prev_date]
            prev_ma60 = sentiment_ma60[prev_date]
            
            # Crossover condition: previous MA20 <= MA60
            if not (prev_ma20 <= prev_ma60):
                continue
            
            # Check if we have outcome for this symbol and timestamp
            if sid in outcomes_data:
                for out_ts, up in outcomes_data[sid]:
                    # out_ts should be the decision timestamp (same as insider_ts)
                    if out_ts == insider_ts and up is not None:
                        opportunities.append((sid, insider_ts, up))
                        break
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Split into train and sealed (most recent 20%)
    all_ts = [ts for _, ts, _ in opportunities]
    all_ts_sorted = sorted(set(all_ts))
    split_idx = int(len(all_ts_sorted) * 0.8)
    sealed_cutoff = all_ts_sorted[split_idx]
    
    train_ops = [(sid, ts, up) for sid, ts, up in opportunities if ts < sealed_cutoff]
    sealed_ops = [(sid, ts, up) for sid, ts, up in opportunities if ts >= sealed_cutoff]
    
    # Compute metrics for train set
    if train_ops:
        issued = len(train_ops)
        opportunities_count = issued  # In this implementation, we only count issued as opportunities
        hits = sum(1 for _, _, up in train_ops if up == 1)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0
        
        distinct_days = len(set(ts for _, ts, _ in train_ops))
        
        # Compute design effect
        # Group by day
        day_counts = {}
        day_hits = {}
        for _, ts, up in train_ops:
            day = ts // 86400  # Group by day
            day_counts[day] = day_counts.get(day, 0) + 1
            if up == 1:
                day_hits[day] = day_hits.get(day, 0) + 1
        
        # Compute intracluster correlation
        total_clusters = len(day_counts)
        total_n = issued
        overall_p = hits / total_n
        
        between_var = 0
        for day in day_counts:
            n_i = day_counts[day]
            p_i = day_hits.get(day, 0) / n_i if n_i > 0 else 0
            between_var += n_i * (p_i - overall_p) ** 2
        
        between_var /= (total_clusters - 1) if total_clusters > 1 else 1
        
        within_var = 0
        for day in day_counts:
            n_i = day_counts[day]
            p_i = day_hits.get(day, 0) / n_i if n_i > 0 else 0
            for _, _, up in train_ops:
                if ts // 86400 == day:
                    within_var += (up - p_i) ** 2
        
        within_var /= (total_n - total_clusters) if total_n > total_clusters else 1
        
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        
        avg_cluster_size = total_n / total_clusters if total_clusters > 0 else 1
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = total_n / design_effect if design_effect > 0 else total_n
        
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.1f}")
        
        # Sealed precision
        if sealed_ops:
            sealed_issued = len(sealed_ops)
            sealed_hits = sum(1 for _, _, up in sealed_ops if up == 1)
            sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
            print(f"SEALED_PRECISION={sealed_precision:.4f}")
        else:
            print("SEALED_PRECISION=0.0000")
    else:
        print("INSUFFICIENT=1")
    
    conn.close()

if __name__ == "__main__":
    main()
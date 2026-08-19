#!/usr/bin/env python3

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'data/signaldeck.db'

def main():
    conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get 10-year Treasury yield series name
    cur.execute("SELECT DISTINCT series FROM macro_series WHERE series LIKE '%10%' OR series LIKE '%treasury%'")
    yield_series = [row[0] for row in cur.fetchall()]
    # Fallback to common FRED names
    for s in ['DGS10', 'GS10', 'DGS10']:
        if s in yield_series:
            yield_series = s
            break
    else:
        print("INSUFFICIENT=1")
        return
    
    # Load all insider purchases with disclosure dates
    cur.execute("""
        SELECT symbol_id, filed_ts, tx_ts 
        FROM insider_trades 
        WHERE form = '4' AND code = 'P'
    """)
    purchases = cur.fetchall()
    
    # Load yield data
    cur.execute(f"SELECT ts, value FROM macro_series WHERE series = '{yield_series}' ORDER BY ts")
    yield_data = cur.fetchall()
    yield_dict = {}
    for ts, val in yield_data:
        dt = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        yield_dict[dt] = val
    
    # Load news sentiment data
    cur.execute("""
        SELECT symbol_id, day, mean_score 
        FROM sentiment_features 
        WHERE mean_score IS NOT NULL
        ORDER BY symbol_id, day
    """)
    sentiment_data = cur.fetchall()
    sentiment_dict = defaultdict(dict)
    for sym, day, score in sentiment_data:
        sentiment_dict[sym][day] = score
    
    # Load prediction outcomes for horizon 1d
    cur.execute("""
        SELECT symbol_id, ts, up 
        FROM prediction_outcomes 
        WHERE horizon = '1d'
    """)
    outcomes = cur.fetchall()
    outcome_dict = defaultdict(dict)
    for sym, ts, up in outcomes:
        dt = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        outcome_dict[sym][dt] = up
    
    conn.close()
    
    if not purchases or not yield_data or not sentiment_data or not outcomes:
        print("INSUFFICIENT=1")
        return
    
    # Build decision points
    calls = []
    seen_days = set()
    
    for sym_id, filed_ts, tx_ts in purchases:
        disc_dt = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
        
        # Check if we have yield data for this date
        if disc_dt not in yield_dict:
            continue
        
        # Get yield 5 trading days before
        disc_date = datetime.strptime(disc_dt, '%Y-%m-%d')
        prev_dates = []
        for i in range(1, 20):  # look back up to 20 days to find 5 trading days
            test_date = disc_date - timedelta(days=i)
            test_str = test_date.strftime('%Y-%m-%d')
            if test_str in yield_dict:
                prev_dates.append(test_str)
            if len(prev_dates) >= 5:
                break
        
        if len(prev_dates) < 5:
            continue
        
        yield_change = yield_dict[disc_dt] - yield_dict[prev_dates[4]]
        if yield_change < 0.10:
            continue
        
        # Check news sentiment
        if sym_id not in sentiment_dict:
            continue
        
        # Get last 5 days sentiment
        sent_dates = sorted([d for d in sentiment_dict[sym_id].keys() if d <= disc_dt], reverse=True)
        if len(sent_dates) < 5:
            continue
        
        recent_sent = [sentiment_dict[sym_id][d] for d in sent_dates[:5]]
        avg_5d = sum(recent_sent) / 5
        
        # Get 60-day sentiment
        if len(sent_dates) < 60:
            continue
        
        long_sent = [sentiment_dict[sym_id][d] for d in sent_dates[:60]]
        avg_60d = sum(long_sent) / 60
        
        if avg_5d <= avg_60d:
            continue
        
        # Check if outcome exists for next day
        next_day = disc_date + timedelta(days=1)
        next_str = next_day.strftime('%Y-%m-%d')
        
        if sym_id in outcome_dict and next_str in outcome_dict[sym_id]:
            up = outcome_dict[sym_id][next_str]
            calls.append((disc_dt, up))
            seen_days.add(disc_dt)
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Split into train and sealed era (last 20% of distinct days)
    sorted_days = sorted(seen_days)
    sealed_cutoff = int(len(sorted_days) * 0.8)
    sealed_days = set(sorted_days[sealed_cutoff:])
    
    train_calls = [(d, up) for d, up in calls if d not in sealed_days]
    sealed_calls = [(d, up) for d, up in calls if d in sealed_days]
    
    # Calculate metrics
    total_issued = len(train_calls)
    total_hits = sum(up for _, up in train_calls)
    base_rate = total_hits / total_issued if total_issued > 0 else 0
    
    # Effective N (cluster by day)
    day_counts = defaultdict(int)
    for d, _ in train_calls:
        day_counts[d] += 1
    
    # Design effect: 1 + average cluster size - 1 * ICC (approximate)
    n_days = len(day_counts)
    avg_cluster = total_issued / n_days if n_days > 0 else 1
    
    # Estimate ICC from binary outcomes
    day_means = []
    for d in day_counts:
        day_calls = [up for dd, up in train_calls if dd == d]
        if day_calls:
            day_means.append(sum(day_calls) / len(day_calls))
    
    if len(day_means) > 1:
        overall_mean = total_hits / total_issued
        between_var = sum((m - overall_mean) ** 2 for m in day_means) / (n_days - 1)
        # Within variance
        within_var = 0
        for d in day_counts:
            day_calls = [up for dd, up in train_calls if dd == d]
            p = sum(day_calls) / len(day_calls)
            within_var += len(day_calls) * p * (1 - p)
        within_var /= (total_issued - n_days) if total_issued > n_days else 1
        
        total_var = between_var + within_var
        icc = between_var / total_var if total_var > 0 else 0
    else:
        icc = 0
    
    deff = 1 + (avg_cluster - 1) * icc
    effective_n = total_issued / deff if deff > 0 else total_issued
    
    # Sealed era precision
    sealed_hits = sum(up for _, up in sealed_calls)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={len(calls)}")
    print(f"PRECISION={total_hits/total_issued:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={len(day_counts)}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
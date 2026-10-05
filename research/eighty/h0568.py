# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 567
# cycle_index: 25
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Check for ICSA data (FRED series)
    cur.execute("SELECT COUNT(*) FROM macro_series WHERE series='ICSA'")
    if cur.fetchone()[0] < 100:
        print("INSUFFICIENT=1")
        return
    
    # Check for insider trades
    cur.execute("SELECT COUNT(*) FROM insider_trades WHERE code='P'")
    if cur.fetchone()[0] < 100:
        print("INSUFFICIENT=1")
        return
    
    # Get prediction outcomes for horizon=21
    cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=21")
    outcomes = cur.fetchall()
    if len(outcomes) < 100:
        print("INSUFFICIENT=1")
        return
    
    # Build date->ICSA moving average dictionary
    cur.execute("SELECT ts, value FROM macro_series WHERE series='ICSA' ORDER BY ts")
    icsa_data = cur.fetchall()
    if len(icsa_data) < 50:
        print("INSUFFICIENT=1")
        return
    
    # Convert ts to date strings for macro condition
    icsa_by_date = {}
    for ts, value in icsa_data:
        day_str = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        icsa_by_date[day_str] = value
    
    # Compute 4-week moving average and rising streak
    sorted_dates = sorted(icsa_by_date.keys())
    moving_avg = {}
    for i in range(3, len(sorted_dates)):
        window = [icsa_by_date[d] for d in sorted_dates[i-3:i+1]]
        moving_avg[sorted_dates[i]] = sum(window)/4
    
    # Compute rising streaks
    rising_streak = {}
    for i in range(1, len(sorted_dates)):
        date = sorted_dates[i]
        prev = sorted_dates[i-1]
        if date in moving_avg and prev in moving_avg:
            if moving_avg[date] > moving_avg[prev]:
                rising_streak[date] = rising_streak.get(prev, 0) + 1
            else:
                rising_streak[date] = 0
    
    # Load insider purchases with filed_ts (public knowledge date)
    cur.execute("SELECT symbol_id, filed_ts FROM insider_trades WHERE code='P' ORDER BY filed_ts")
    insider_data = cur.fetchall()
    
    # Load sentiment features
    cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
    sentiment_data = cur.fetchall()
    sentiment_by_sym = defaultdict(dict)
    for sym_id, day, score in sentiment_data:
        sentiment_by_sym[sym_id][day] = score
    
    # Load daily bars for volume
    cur.execute("SELECT symbol_id, ts, volume FROM bars WHERE tf='1d'")
    volume_data = cur.fetchall()
    volume_by_sym = defaultdict(dict)
    for sym_id, ts, vol in volume_data:
        day_str = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        volume_by_sym[sym_id][day_str] = vol
    
    # Get symbol list with daily bar history
    cur.execute("SELECT id, symbol FROM symbols")
    symbols = cur.fetchall()
    
    # Build 20-day sentiment averages and 100-day sentiment averages
    def get_avg_sentiment(sym_id, center_date, days):
        d = datetime.strptime(center_date, '%Y-%m-%d')
        scores = []
        for i in range(days):
            check_date = (d - timedelta(days=i)).strftime('%Y-%m-%d')
            if sym_id in sentiment_by_sym and check_date in sentiment_by_sym[sym_id]:
                scores.append(sentiment_by_sym[sym_id][check_date])
        return sum(scores)/len(scores) if scores else None
    
    # Process each outcome
    issued = []
    opportunities = 0
    
    for sym_id, ts, up_label in outcomes:
        dec_date = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        
        # Check insider purchase in last 60 days
        has_insider = False
        dec_dt = datetime.utcfromtimestamp(ts)
        for sym_insider, filed_ts in insider_data:
            if sym_insider != sym_id:
                continue
            filed_dt = datetime.utcfromtimestamp(filed_ts)
            if dec_dt - timedelta(days=60) <= filed_dt <= dec_dt:
                has_insider = True
                break
        
        if not has_insider:
            continue
            
        opportunities += 1
        
        # Check macro condition
        if dec_date not in rising_streak or rising_streak[dec_date] < 5:
            continue
        
        # Check volume condition
        if sym_id not in volume_by_sym or dec_date not in volume_by_sym[sym_id]:
            continue
        vol_20d = []
        d = datetime.strptime(dec_date, '%Y-%m-%d')
        for i in range(20):
            check_date = (d - timedelta(days=i)).strftime('%Y-%m-%d')
            if check_date in volume_by_sym[sym_id]:
                vol_20d.append(volume_by_sym[sym_id][check_date])
        if len(vol_20d) < 10:
            continue
        avg_vol = sum(vol_20d)/len(vol_20d)
        if avg_vol < 100000:
            continue
        
        # Check sentiment condition
        avg20 = get_avg_sentiment(sym_id, dec_date, 20)
        avg100 = get_avg_sentiment(sym_id, dec_date, 100)
        if avg20 is None or avg100 is None:
            continue
        if not (avg20 < avg100):
            continue
        
        issued.append((sym_id, dec_date, up_label))
    
    if len(issued) < 10:
        print("INSUFFICIENT=1")
        return
    
    # Compute base metrics
    total_issued = len(issued)
    hits = sum(1 for _, _, up in issued if up == 1)
    precision = hits / total_issued if total_issued > 0 else 0
    base_rate = sum(1 for _, _, up in issued if up == 1) / total_issued
    
    # Distinct days
    distinct_days = len(set(day for _, day, _ in issued))
    
    # Design effect (clustering by day)
    day_counts = defaultdict(int)
    for _, day, _ in issued:
        day_counts[day] += 1
    avg_cluster = total_issued / distinct_days
    variance = sum((c - avg_cluster)**2 for c in day_counts.values()) / (distinct_days - 1) if distinct_days > 1 else 0
    deff = 1 + (variance / avg_cluster) if avg_cluster > 0 else 1
    effective_n = total_issued / deff
    
    # Hold out most recent 20% by time
    sorted_issued = sorted(issued, key=lambda x: x[1])
    split_idx = int(len(sorted_issued) * 0.8)
    sealed_era = sorted_issued[split_idx:]
    sealed_hits = sum(1 for _, _, up in sealed_era if up == 1)
    sealed_precision = sealed_hits / len(sealed_era) if sealed_era else 0
    
    # Output
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
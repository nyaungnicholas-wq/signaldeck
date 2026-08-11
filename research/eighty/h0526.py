# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 525
# cycle_index: 55
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timezone
import statistics

def main():
    # Connect read-only
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Universe: symbols with >=2 years of daily news sentiment
    c.execute('''
        SELECT symbol_id, COUNT(DISTINCT date(ts, 'unixepoch')) as days
        FROM news
        GROUP BY symbol_id
        HAVING days >= 730
    ''')
    universe = {row[0] for row in c.fetchall()}
    
    if not universe:
        print('INSUFFICIENT=1')
        return
    
    # Get all daily sentiment data per symbol per day
    c.execute('''
        SELECT symbol_id, date(ts, 'unixepoch') as day, AVG(score) as sentiment
        FROM news
        WHERE symbol_id IN ({}) 
        GROUP BY symbol_id, day
        ORDER BY symbol_id, day
    '''.format(','.join('?' * len(universe))), list(universe))
    sentiment_data = c.fetchall()
    
    # Get all daily bars (close, volume) for these symbols
    c.execute('''
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    '''.format(','.join('?' * len(universe))), list(universe))
    bar_data = c.fetchall()
    
    # Convert to per-symbol dictionaries for easier processing
    sentiment_by_sym = defaultdict(list)  # (day_str, sentiment)
    bar_by_sym = defaultdict(list)  # (ts, close, volume)
    
    for sym_id, day_str, sentiment in sentiment_data:
        sentiment_by_sym[sym_id].append((day_str, sentiment))
    
    for sym_id, ts, close, volume in bar_data:
        dt = datetime.fromtimestamp(ts, tz=timezone.utc)
        day_str = dt.strftime('%Y-%m-%d')
        bar_by_sym[sym_id].append((day_str, close, volume))
    
    # Compute decision points: for each symbol/day where we have sentiment AND bar data
    decision_points = []
    
    for sym_id in universe:
        if sym_id not in sentiment_by_sym or sym_id not in bar_by_sym:
            continue
            
        # Align sentiment and price by day
        sent_dict = {d: s for d, s in sentiment_by_sym[sym_id]}
        bar_dict = {d: (close, vol) for d, close, vol in bar_by_sym[sym_id]}
        
        common_days = sorted(set(sent_dict.keys()) & set(bar_dict.keys()))
        
        if len(common_days) < 60:  # Need at least 60 days for rolling window
            continue
            
        # Compute rolling 60-day sentiment stats and daily returns
        sent_values = []
        returns = []
        
        for day in common_days:
            sent_values.append(sent_dict[day])
            close_today = bar_dict[day][0]
            
            # Find previous day's close for return calculation
            idx = common_days.index(day)
            if idx > 0:
                prev_day = common_days[idx - 1]
                close_prev = bar_dict[prev_day][0]
                daily_ret = (close_today / close_prev) - 1 if close_prev > 0 else 0
            else:
                daily_ret = 0
            returns.append(daily_ret)
        
        # For each day after we have 60 days of sentiment history
        for i in range(60, len(common_days)):
            day = common_days[i]
            
            # Rolling 60-day sentiment stats
            window_sent = sent_values[i-60:i]
            sent_mean = statistics.mean(window_sent)
            sent_std = statistics.stdev(window_sent) if len(window_sent) > 1 else 0
            
            if sent_std == 0:
                continue
                
            z_score = (sent_values[i] - sent_mean) / sent_std
            
            # Daily return (today)
            daily_ret = returns[i]
            
            # Need 20-day volatility and 14-day RSI for abstain checks
            if i < 20 or i < 14:
                continue
                
            # 20-day realized volatility
            window_returns = returns[i-20:i]
            vol_20d = statistics.stdev(window_returns) if len(window_returns) > 1 else 0
            
            # 14-day RSI
            gains = []
            losses = []
            for r in returns[i-14:i]:
                if r >= 0:
                    gains.append(r)
                    losses.append(0)
                else:
                    gains.append(0)
                    losses.append(abs(r))
            
            avg_gain = statistics.mean(gains) if gains else 0
            avg_loss = statistics.mean(losses) if losses else 0
            
            if avg_loss == 0:
                rsi = 100
            else:
                rs = avg_gain / avg_loss
                rsi = 100 - (100 / (1 + rs))
            
            # Forward 21-day return (for label)
            if i + 21 < len(common_days):
                close_today = bar_dict[day][0]
                future_day = common_days[i + 21]
                close_future = bar_dict[future_day][0]
                fwd_ret = (close_future / close_today) - 1 if close_today > 0 else 0
                fwd_up = 1 if fwd_ret > 0 else 0
                
                decision_points.append({
                    'sym_id': sym_id,
                    'day': day,
                    'z_score': z_score,
                    'daily_ret': daily_ret,
                    'vol_20d': vol_20d,
                    'rsi_14': rsi,
                    'fwd_ret': fwd_ret,
                    'fwd_up': fwd_up,
                    'ts': datetime.strptime(day, '%Y-%m-%d').timestamp()
                })
    
    if not decision_points:
        print('INSUFFICIENT=1')
        return
    
    # Sort by timestamp for time split
    decision_points.sort(key=lambda x: x['ts'])
    
    # Time split: most recent 20% as sealed era
    split_idx = int(0.8 * len(decision_points))
    main_era = decision_points[:split_idx]
    sealed_era = decision_points[split_idx:]
    
    # Compute median returns for each z-score bucket (across universe)
    # Bucket z-scores to 0.1 precision
    z_buckets = defaultdict(list)
    for dp in main_era:
        bucket = round(dp['z_score'], 1)
        z_buckets[bucket].append(dp['daily_ret'])
    
    # Compute median for each bucket
    z_medians = {}
    for bucket, rets in z_buckets.items():
        z_medians[bucket] = statistics.median(rets)
    
    # Issue calls on main era
    issued_main = []
    for dp in main_era:
        # Entry conditions
        bucket = round(dp['z_score'], 1)
        if bucket not in z_medians:
            continue
            
        if dp['z_score'] >= 2.5 and dp['daily_ret'] < z_medians[bucket]:
            # Abstain conditions
            # 20-day vol in top decile (compute across universe)
            # Compute 90th percentile of vol_20d in main era
            vol_90th = sorted([x['vol_20d'] for x in main_era])[int(0.9 * len(main_era))]
            
            if dp['vol_20d'] > vol_90th:
                continue
            if dp['rsi_14'] > 80:
                continue
                
            issued_main.append(dp)
    
    # Issue calls on sealed era (same thresholds)
    issued_sealed = []
    for dp in sealed_era:
        bucket = round(dp['z_score'], 1)
        if bucket not in z_medians:
            continue
            
        if dp['z_score'] >= 2.5 and dp['daily_ret'] < z_medians[bucket]:
            vol_90th = sorted([x['vol_20d'] for x in main_era])[int(0.9 * len(main_era))]
            
            if dp['vol_20d'] > vol_90th:
                continue
            if dp['rsi_14'] > 80:
                continue
                
            issued_sealed.append(dp)
    
    # Compute metrics for main era
    if not issued_main:
        print('INSUFFICIENT=1')
        return
    
    issued_count = len(issued_main)
    opportunities = len(main_era)
    hits = sum(dp['fwd_up'] for dp in issued_main)
    precision = hits / issued_count if issued_count > 0 else 0
    
    # Base rate of predicted class (up) within issued subset
    base_rate = hits / issued_count if issued_count > 0 else 0
    
    # Distinct days in issued calls
    issued_days = set(dp['day'] for dp in issued_main)
    distinct_days = len(issued_days)
    
    # Effective N (clustered by day)
    # Compute design effect: 1 + (m - 1) * ICC
    # m = average calls per day
    # ICC = intra-class correlation of hits across days
    
    # Group hits by day
    hits_by_day = defaultdict(list)
    for dp in issued_main:
        hits_by_day[dp['day']].append(dp['fwd_up'])
    
    n_days = len(hits_by_day)
    total_calls = issued_count
    
    # Overall mean of hits
    overall_mean = precision
    
    # Between-day variance
    ss_between = 0
    for day, hits in hits_by_day.items():
        day_mean = statistics.mean(hits)
        ss_between += len(hits) * (day_mean - overall_mean) ** 2
    
    var_between = ss_between / (n_days - 1) if n_days > 1 else 0
    
    # Within-day variance
    ss_within = 0
    for day, hits in hits_by_day.items():
        if len(hits) > 1:
            day_mean = statistics.mean(hits)
            for h in hits:
                ss_within += (h - day_mean) ** 2
    
    df_within = total_calls - n_days
    var_within = ss_within / df_within if df_within > 0 else 0
    
    # ICC
    if var_between + var_within > 0:
        icc = var_between / (var_between + var_within)
    else:
        icc = 0
    
    # Average calls per day
    m = total_calls / n_days if n_days > 0 else 0
    
    # Design effect
    deff = 1 + (m - 1) * icc if m > 1 else 1
    
    # Effective N
    effective_n = total_calls / deff if deff > 0 else total_calls
    
    # Sealed era precision
    if issued_sealed:
        hits_sealed = sum(dp['fwd_up'] for dp in issued_sealed)
        sealed_precision = hits_sealed / len(issued_sealed)
    else:
        sealed_precision = 0
    
    # Print required metrics
    print(f'ISSUED={issued_count}')
    print(f'OPPORTUNITIES={opportunities}')
    print(f'PRECISION={precision:.4f}')
    print(f'BASE_RATE={base_rate:.4f}')
    print(f'DISTINCT_DAYS={distinct_days}')
    print(f'EFFECTIVE_N={effective_n:.2f}')
    print(f'SEALED_PRECISION={sealed_precision:.4f}')
    
    conn.close()

if __name__ == '__main__':
    main()
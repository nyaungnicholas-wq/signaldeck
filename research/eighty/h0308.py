# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 307
# cycle_index: 30
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from datetime import datetime
from collections import defaultdict

DB_PATH = 'data/signaldeck.db'
ROLLING_WINDOW_SIR = 30
ROLLING_WINDOW_SENTIMENT = 10
ROLLING_WINDOW_VOLUME = 20
HORIZON_DAYS = 3
SHORT_INTEREST_THRESHOLD = 0.30
SENTIMENT_JUMP_THRESHOLD = 0.5
PRICE_UP_THRESHOLD = 0.01
MIN_AVG_VOLUME = 500_000
OUT_SAMPLE_FRACTION = 0.20

def main():
    try:
        conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        
        # Get all data in bulk
        sv = conn.execute("""
            SELECT symbol_id, day, short_vol, total_vol
            FROM short_volume
            WHERE day >= '2026-05-20'
            ORDER BY symbol_id, day
        """).fetchall()
        
        sf = conn.execute("""
            SELECT symbol_id, day, mean_score
            FROM sentiment_features
            ORDER BY symbol_id, day
        """).fetchall()
        
        bars = conn.execute("""
            SELECT symbol_id, ts, close, volume
            FROM bars
            WHERE tf = '1d'
            ORDER BY symbol_id, ts
        """).fetchall()
        
        symbols = conn.execute("""
            SELECT id, active
            FROM symbols
        """).fetchall()
        
        conn.close()
    except Exception:
        print("INSUFFICIENT=1")
        return

    if not sv or not sf or not bars:
        print("INSUFFICIENT=1")
        return

    active_symbols = {s['id'] for s in symbols if s['active']}
    
    # Convert bars to daily format and organize by symbol
    bars_by_symbol = defaultdict(dict)
    for row in bars:
        if row['symbol_id'] not in active_symbols:
            continue
        day = datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d')
        bars_by_symbol[row['symbol_id']][day] = {
            'ts': row['ts'],
            'close': row['close'],
            'volume': row['volume']
        }
    
    # Organize short volume by symbol
    sv_by_symbol = defaultdict(list)
    for row in sv:
        if row['symbol_id'] not in active_symbols:
            continue
        sv_by_symbol[row['symbol_id']].append({
            'day': row['day'],
            'short_vol': row['short_vol'],
            'total_vol': row['total_vol']
        })
    
    # Compute 30-day rolling avg short interest ratio per symbol
    sir_by_symbol = defaultdict(list)
    for sym, records in sv_by_symbol.items():
        if len(records) < ROLLING_WINDOW_SIR:
            continue
        records.sort(key=lambda x: x['day'])
        daily_sir = []
        for r in records:
            if r['total_vol'] > 0:
                daily_sir.append((r['day'], r['short_vol'] / r['total_vol']))
        if len(daily_sir) < ROLLING_WINDOW_SIR:
            continue
        for i in range(ROLLING_WINDOW_SIR - 1, len(daily_sir)):
            window = [x[1] for x in daily_sir[i - ROLLING_WINDOW_SIR + 1:i + 1]]
            avg_sir = sum(window) / ROLLING_WINDOW_SIR
            sir_by_symbol[sym].append((daily_sir[i][0], avg_sir))
    
    # Organize sentiment by symbol
    sent_by_symbol = defaultdict(list)
    for row in sf:
        if row['symbol_id'] not in active_symbols:
            continue
        sent_by_symbol[row['symbol_id']].append({
            'day': row['day'],
            'score': row['mean_score']
        })
    
    # Compute rolling 10-day sentiment z-score per symbol
    sent_stats_by_symbol = defaultdict(list)
    for sym, records in sent_by_symbol.items():
        if len(records) < ROLLING_WINDOW_SENTIMENT + 1:
            continue
        records.sort(key=lambda x: x['day'])
        for i in range(ROLLING_WINDOW_SENTIMENT, len(records)):
            window = [records[j]['score'] for j in range(i - ROLLING_WINDOW_SENTIMENT, i)]
            mean = sum(window) / ROLLING_WINDOW_SENTIMENT
            variance = sum((x - mean) ** 2 for x in window) / ROLLING_WINDOW_SENTIMENT
            std = math.sqrt(variance) if variance > 0 else 0
            z = (records[i]['score'] - mean) / std if std > 0 else 0
            sent_stats_by_symbol[sym].append({
                'day': records[i]['day'],
                'z_score': z
            })
    
    # Find all dates with SIR data
    all_dates = set()
    for sym, records in sir_by_symbol.items():
        for day, _ in records:
            all_dates.add(day)
    
    if not all_dates:
        print("INSUFFICIENT=1")
        return
    
    date_list = sorted(all_dates)
    
    # Precompute 95th percentile SIR per day
    sir_by_day = defaultdict(list)
    for sym, records in sir_by_symbol.items():
        for day, sir in records:
            sir_by_day[day].append(sir)
    
    percentile_95_by_day = {}
    for day, vals in sir_by_day.items():
        if len(vals) < 20:
            continue
        vals_sorted = sorted(vals)
        idx = int(0.95 * len(vals))
        percentile_95_by_day[day] = vals_sorted[idx]
    
    # Build lookup for sentiment stats by symbol and day
    sent_lookup = defaultdict(dict)
    for sym, records in sent_stats_by_symbol.items():
        for r in records:
            sent_lookup[sym][r['day']] = r['z_score']
    
    # Track opportunities and issued calls
    opportunities = []
    issued = []
    
    # For each potential decision day T
    for i in range(ROLLING_WINDOW_SIR, len(date_list)):
        T_minus_1 = date_list[i - 1]
        T = date_list[i]
        
        if T_minus_1 not in percentile_95_by_day:
            continue
        
        threshold_95 = percentile_95_by_day[T_minus_1]
        
        # Check each symbol
        for sym in sir_by_symbol:
            # Find SIR value for T_minus_1
            sir_val = None
            for day, s in sir_by_symbol[sym]:
                if day == T_minus_1:
                    sir_val = s
                    break
            if sir_val is None:
                continue
            
            # Check SIR conditions
            if sir_val <= threshold_95:
                continue
            if sir_val <= SHORT_INTEREST_THRESHOLD:
                continue
            
            # Check sentiment condition
            if T not in sent_lookup[sym]:
                continue
            z_score = sent_lookup[sym][T]
            if z_score <= SENTIMENT_JUMP_THRESHOLD:
                continue
            
            # Check price condition
            daily_data = bars_by_symbol.get(sym, {})
            if T not in daily_data or T_minus_1 not in daily_data:
                continue
            
            close_T = daily_data[T]['close']
            close_T_minus_1 = daily_data[T_minus_1]['close']
            if close_T_minus_1 == 0:
                continue
            pct_change = (close_T - close_T_minus_1) / close_T_minus_1
            if pct_change <= PRICE_UP_THRESHOLD:
                continue
            
            # Check volume condition (20-day avg)
            vol_window = []
            for day in date_list:
                if day < T and day in daily_data:
                    vol_window.append(daily_data[day]['volume'])
                if len(vol_window) >= ROLLING_WINDOW_VOLUME:
                    break
            
            if len(vol_window) < ROLLING_WINDOW_VOLUME:
                continue
            
            avg_vol = sum(vol_window) / len(vol_window)
            if avg_vol < MIN_AVG_VOLUME:
                continue
            
            # Check if we have horizon data
            T_horizon = None
            horizon_day_idx = i + HORIZON_DAYS
            if horizon_day_idx < len(date_list):
                T_horizon = date_list[horizon_day_idx]
            else:
                continue
            
            if T_horizon not in daily_data:
                continue
            
            opportunities.append({
                'symbol': sym,
                'T': T,
                'T_minus_1': T_minus_1,
                'T_horizon': T_horizon,
                'close_T': close_T,
                'close_horizon': daily_data[T_horizon]['close']
            })
            
            # Determine if hit
            hit = daily_data[T_horizon]['close'] > close_T
            
            issued.append({
                'symbol': sym,
                'T': T,
                'T_horizon': T_horizon,
                'hit': hit
            })
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Split into in-sample and sealed era
    n_opportunities = len(opportunities)
    n_issued = len(issued)
    
    # Sort by time (using T as proxy)
    issued_sorted = sorted(issued, key=lambda x: x['T'])
    
    split_idx = int(n_issued * (1 - OUT_SAMPLE_FRACTION))
    in_sample = issued_sorted[:split_idx]
    sealed_era = issued_sorted[split_idx:]
    
    # Calculate metrics
    hits = sum(1 for item in in_sample if item['hit'])
    sealed_hits = sum(1 for item in sealed_era if item['hit'])
    
    precision = hits / len(in_sample) if in_sample else 0
    sealed_precision = sealed_hits / len(sealed_era) if sealed_era else 0
    
    # Calculate base rate (proportion of positive outcomes in all opportunities)
    # Note: opportunities include both issued and abstained cases
    total_positive_opportunities = 0
    total_opportunities_with_horizon = 0
    for opp in opportunities:
        if opp['T_horizon']:
            total_opportunities_with_horizon += 1
            if opp['close_horizon'] > opp['close_T']:
                total_positive_opportunities += 1
    
    base_rate = total_positive_opportunities / total_opportunities_with_horizon if total_opportunities_with_horizon > 0 else 0
    
    # Calculate distinct days
    distinct_days = len(set(item['T'] for item in issued))
    
    # Calculate design effect and effective N
    # Count issued per day
    day_counts = defaultdict(int)
    for item in issued:
        day_counts[item['T']] += 1
    
    # Design effect = 1 + (m - 1) * ICC
    # For simplicity, use formula: design_effect = 1 + (average_cluster_size - 1) * ICC
    # Estimate ICC from variance of outcomes between days vs within days
    if len(day_counts) > 1:
        # Calculate outcomes per day
        outcomes_per_day = defaultdict(list)
        for item in issued:
            outcomes_per_day[item['T']].append(1 if item['hit'] else 0)
        
        # Overall mean
        all_outcomes = [1 if item['hit'] else 0 for item in issued]
        overall_mean = sum(all_outcomes) / len(all_outcomes)
        
        # Between-day variance
        between_sum_sq = 0
        for day, outcomes in outcomes_per_day.items():
            day_mean = sum(outcomes) / len(outcomes)
            between_sum_sq += len(outcomes) * (day_mean - overall_mean) ** 2
        between_var = between_sum_sq / (len(day_counts) - 1) if len(day_counts) > 1 else 0
        
        # Within-day variance
        within_sum_sq = 0
        df_within = 0
        for day, outcomes in outcomes_per_day.items():
            day_mean = sum(outcomes) / len(outcomes)
            for outcome in outcomes:
                within_sum_sq += (outcome - day_mean) ** 2
                df_within += 1
        within_var = within_sum_sq / df_within if df_within > 0 else 0
        
        # ICC
        avg_cluster_size = len(issued) / len(day_counts)
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        
        design_effect = 1 + (avg_cluster_size - 1) * icc
    else:
        design_effect = 1
    
    effective_n = n_issued / design_effect
    
    # Print results
    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={n_opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
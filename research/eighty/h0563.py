# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 562
# cycle_index: 20
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import statistics

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 21
LOOKBACK_YEARS = 12
CUTOFF_PCT = 0.20
MA_SPIKE_THRESHOLD = 0.8
ICSA_WEEKS_RISING = 3

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get symbols with sentiment_features data (daily aggregated news sentiment)
    sym_sql = "SELECT DISTINCT symbol_id FROM sentiment_features WHERE day >= '2012-07-01'"
    symbols = [row['symbol_id'] for row in conn.execute(sym_sql)]
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get all sentiment data for universe
    sent_sql = """
        SELECT symbol_id, day, mean_score 
        FROM sentiment_features 
        WHERE symbol_id IN ({}) 
        ORDER BY symbol_id, day
    """.format(','.join(['?']*len(symbols)))
    sentiment_data = defaultdict(list)
    for row in conn.execute(sent_sql, symbols):
        sentiment_data[row['symbol_id']].append((row['day'], row['mean_score']))
    
    # Get ICSA macro data
    icsa_sql = "SELECT ts, value FROM macro_series WHERE series='ICSA' ORDER BY ts"
    icsa_raw = [(row['ts'], row['value']) for row in conn.execute(icsa_sql)]
    if len(icsa_raw) < 28:  # Need at least 4 weeks
        print("INSUFFICIENT=1")
        return
    
    # Convert ICSA timestamps to dates and compute weekly changes
    icsa_dates = {}
    for ts, val in icsa_raw:
        dt = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        icsa_dates[dt] = val
    
    # Get all opportunity dates from sentiment data
    all_dates = set()
    for sym_dates in sentiment_data.values():
        for day, _ in sym_dates:
            all_dates.add(day)
    all_dates_sorted = sorted(all_dates)
    if not all_dates_sorted:
        print("INSUFFICIENT=1")
        return
    
    # Get labels from prediction_outcomes for horizon 21
    label_sql = """
        SELECT symbol_id, ts, up, fwd_return 
        FROM prediction_outcomes 
        WHERE horizon = ? AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join(['?']*len(symbols)))
    label_data = defaultdict(list)
    for row in conn.execute(label_sql, [HORIZON] + symbols):
        dt = datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d')
        label_data[row['symbol_id']].append((dt, row['up'], row['fwd_return']))
    
    # Precompute label lookup by symbol and date
    label_lookup = defaultdict(dict)
    for sym, entries in label_data.items():
        for dt, up, ret in entries:
            label_lookup[sym][dt] = (up, ret)
    
    # Process each decision point
    opportunities = []
    issued_calls = []
    
    for decision_date in all_dates_sorted:
        dec_dt = datetime.strptime(decision_date, '%Y-%m-%d')
        
        # Check ICSA condition: rising for at least 3 of past 4 weeks
        icsa_rising_count = 0
        current_week_start = dec_dt - timedelta(days=dec_dt.weekday())
        prev_weeks = []
        
        for week_offset in range(1, 5):
            week_start = current_week_start - timedelta(weeks=week_offset)
            week_end = week_start + timedelta(days=6)
            # Get ICSA value at end of that week
            week_vals = []
            for day_offset in range(7):
                day = week_start + timedelta(days=day_offset)
                day_str = day.strftime('%Y-%m-%d')
                if day_str in icsa_dates:
                    week_vals.append(icsa_dates[day_str])
            if week_vals:
                prev_weeks.append(max(week_vals))
        
        if len(prev_weeks) >= 4:
            for i in range(3):
                if prev_weeks[i] > prev_weeks[i+1]:
                    icsa_rising_count += 1
        
        icsa_condition = icsa_rising_count >= ICSA_WEEKS_RISING
        
        # Process each symbol at this date
        for sym in symbols:
            if sym not in sentiment_data:
                continue
            
            # Get sentiment history up to decision date
            sym_dates_vals = sentiment_data[sym]
            up_to_date = [(d, v) for d, v in sym_dates_vals if d <= decision_date]
            if len(up_to_date) < 252:
                continue
            
            # Extract values and compute standardization
            values = [v for _, v in up_to_date]
            mean_252 = statistics.mean(values[-252:])
            std_252 = statistics.stdev(values[-252:]) if len(values[-252:]) > 1 else 0
            if std_252 == 0:
                continue
            
            # Standardize last 20 values for MA20, last 5 for MA5
            if len(values) < 20:
                continue
            
            # Standardize daily values using 252-day stats
            standardized = [(v - mean_252) / std_252 for v in values[-252:]]
            ma5 = statistics.mean(standardized[-5:])
            ma20 = statistics.mean(standardized[-20:])
            
            # Check sentiment condition
            sentiment_condition = ma5 > 0 and (ma5 - ma20) > MA_SPIKE_THRESHOLD
            
            # Final entry condition
            if icsa_condition and sentiment_condition:
                # Check if label exists for horizon 21 days after
                future_date = dec_dt + timedelta(days=HORIZON)
                future_str = future_date.strftime('%Y-%m-%d')
                
                if sym in label_lookup and decision_date in label_lookup[sym]:
                    up, fwd_return = label_lookup[sym][decision_date]
                    issued_calls.append({
                        'symbol': sym,
                        'date': decision_date,
                        'up': up,
                        'fwd_return': fwd_return
                    })
            
            opportunities.append(dec_date)
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Split into main and sealed (last 20% of time)
    unique_dates = sorted(set(call['date'] for call in issued_calls))
    split_idx = int(len(unique_dates) * (1 - CUTOFF_PCT))
    main_cutoff = unique_dates[split_idx] if split_idx < len(unique_dates) else unique_dates[-1]
    
    main_calls = [call for call in issued_calls if call['date'] < main_cutoff]
    sealed_calls = [call for call in issued_calls if call['date'] >= main_cutoff]
    
    # Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, set(), 0
        hits = sum(1 for call in calls if call['up'] == 1)
        precision = hits / len(calls)
        base_rate = hits / len(calls)  # same as precision within issued
        distinct_days = len(set(call['date'] for call in calls))
        
        # Design effect: cluster calls by date, compute ICC approximation
        date_groups = defaultdict(list)
        for call in calls:
            date_groups[call['date']].append(call)
        
        n_groups = len(date_groups)
        if n_groups <= 1:
            design_effect = 1.0
        else:
            group_sizes = [len(group) for group in date_groups.values()]
            avg_group_size = sum(group_sizes) / n_groups
            if avg_group_size > 1:
                design_effect = 1 + (avg_group_size - 1) * 0.5  # conservative estimate
            else:
                design_effect = 1.0
        
        effective_n = len(calls) / design_effect if design_effect > 0 else len(calls)
        return len(calls), precision, base_rate, distinct_days, effective_n
    
    main_issued, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_metrics(main_calls)
    sealed_issued, sealed_precision, sealed_base_rate, sealed_distinct_days, sealed_effective_n = compute_metrics(sealed_calls)
    
    # Validate invariants
    if main_distinct_days > main_issued:
        print("INSUFFICIENT=1")
        return
    if main_effective_n >= main_issued:
        print("INSUFFICIENT=1")
        return
    
    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={main_precision:.6f}")
    print(f"BASE_RATE={main_base_rate:.6f}")
    print(f"DISTINCT_DAYS={main_distinct_days}")
    print(f"EFFECTIVE_N={main_effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
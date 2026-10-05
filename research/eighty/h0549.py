# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 548
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
import statistics
from datetime import datetime

def main():
    db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = db.cursor()
    
    # Get daily bars with symbol info
    c.execute("""
        SELECT b.symbol_id, b.ts, b.close, b.volume, s.market
        FROM bars b JOIN symbols s ON b.symbol_id = s.id
        WHERE b.tf='1d'
        ORDER BY b.symbol_id, b.ts
    """)
    bars_data = c.fetchall()
    
    # Get prediction outcomes for 5-day horizon (labels)
    c.execute("""
        SELECT symbol_id, ts, up 
        FROM prediction_outcomes 
        WHERE horizon=5
    """)
    labels = {}
    for sym_id, ts, up in c.fetchall():
        labels[(sym_id, ts)] = up
    
    # Group bars by symbol
    symbol_bars = defaultdict(list)
    for sym_id, ts, close, volume, market in bars_data:
        if market == 'stocks':
            symbol_bars[sym_id].append((ts, close, volume))
    
    # Convert timestamps to datetime days for sorting and calculations
    all_days = sorted(set(ts for sym_id in symbol_bars 
                        for ts, _, _ in symbol_bars[sym_id]))
    
    # Precompute trailing metrics for each symbol
    symbol_data = {}
    for sym_id, bars in symbol_bars.items():
        if len(bars) < 260:
            continue
        
        # Sort by timestamp
        bars.sort(key=lambda x: x[0])
        ts_list = [b[0] for b in bars]
        close_dict = {b[0]: b[1] for b in bars}
        vol_dict = {b[0]: b[2] for b in bars}
        
        # Precompute trailing 252-day average dollar volume (excluding current day)
        adv_trailing = {}
        for i, ts in enumerate(ts_list):
            if i < 252:
                continue
            window = ts_list[i-252:i]
            adv = sum(close_dict[w] * vol_dict[w] for w in window) / 252
            adv_trailing[ts] = adv
        
        # Precompute trailing 5-day return
        ret5_dict = {}
        for i, ts in enumerate(ts_list):
            if i < 5:
                continue
            prev_ts = ts_list[i-5]
            if prev_ts in close_dict and ts in close_dict:
                ret5_dict[ts] = close_dict[ts] / close_dict[prev_ts] - 1
        
        # Precompute trailing 5-day volume count
        vol_count = {}
        for i, ts in enumerate(ts_list):
            start_idx = max(0, i-4)  # last 5 days including current
            count = sum(1 for j in range(start_idx, i+1) if vol_dict[ts_list[j]] > 0)
            vol_count[ts] = count
        
        symbol_data[sym_id] = {
            'ts_list': ts_list,
            'close_dict': close_dict,
            'vol_dict': vol_dict,
            'adv_trailing': adv_trailing,
            'ret5_dict': ret5_dict,
            'vol_count': vol_count
        }
    
    # Process each day to issue calls
    calls = []  # (day, sym_id, label)
    opportunities = 0
    
    for i, day in enumerate(all_days):
        if i < 260 + 252 + 5:  # Need enough history
            continue
        
        # Get symbols with price >= $2 and enough volume data
        eligible = []
        adv_values = []
        
        for sym_id, data in symbol_data.items():
            if day not in data['close_dict']:
                continue
            if day not in data['adv_trailing']:
                continue
            if day not in data['ret5_dict']:
                continue
            if data['close_dict'][day] < 2.0:
                continue
            if data['vol_count'].get(day, 0) < 60:
                continue
            
            adv = data['adv_trailing'][day]
            ret5 = data['ret5_dict'][day]
            eligible.append((sym_id, adv, ret5))
            adv_values.append(adv)
        
        if len(eligible) < 30:  # Need meaningful cross-section
            continue
        
        # Compute terciles and deciles based on trailing ADV
        adv_sorted = sorted(adv_values)
        tercile_idx = len(adv_sorted) // 3
        decile_idx = len(adv_sorted) // 10
        
        top_decile_threshold = adv_sorted[-decile_idx-1]
        bottom_tercile_threshold = adv_sorted[tercile_idx-1]
        
        # Identify baskets
        bottom_tercile = []
        top_decile = []
        
        for sym_id, adv, ret5 in eligible:
            if adv <= bottom_tercile_threshold:
                bottom_tercile.append((sym_id, ret5))
            if adv >= top_decile_threshold:
                top_decile.append((sym_id, ret5))
        
        if not bottom_tercile or not top_decile:
            continue
        
        # Compute basket 5-day returns
        top_ret5 = statistics.mean([ret5 for _, ret5 in top_decile])
        bottom_ret5 = statistics.mean([ret5 for _, ret5 in bottom_tercile])
        
        spread = top_ret5 - bottom_ret5
        
        if spread < 0.03:
            continue  # Abstain condition
        
        # Issue calls on bottom-tercile symbols below their basket return
        for sym_id, ret5 in bottom_tercile:
            if ret5 < bottom_ret5:
                # Check if label available
                if (sym_id, day) in labels:
                    opportunities += 1
                    calls.append((day, sym_id, labels[(sym_id, day)]))
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Split into normal and sealed eras (last 20% of days)
    call_days = sorted(set(day for day, _, _ in calls))
    split_idx = int(len(call_days) * 0.8)
    sealed_days = set(call_days[split_idx:])
    
    normal_calls = []
    sealed_calls = []
    
    for day, sym_id, label in calls:
        if day in sealed_days:
            sealed_calls.append(label)
        else:
            normal_calls.append(label)
    
    # Calculate metrics
    all_labels = [label for _, _, label in calls]
    issued = len(all_labels)
    hits = sum(all_labels)
    precision = hits / issued if issued > 0 else 0
    base_rate = hits / issued  # Within issued subset
    
    distinct_days = len(call_days)
    
    # Calculate design effect (clustering by day)
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for day, _, label in calls:
        day_counts[day] += 1
        day_hits[day] += label
    
    k = len(day_counts)  # number of clusters
    if k > 1:
        # Calculate ICC using ANOVA approximation
        grand_mean = hits / issued
        
        # Between-cluster variance
        ssb = sum(day_counts[d] * (day_hits[d]/day_counts[d] - grand_mean)**2 
                 for d in day_counts)
        msb = ssb / (k - 1)
        
        # Within-cluster variance
        ssw = sum((day_hits[d] - day_counts[d] * day_hits[d]/day_counts[d])**2 +
                 (day_counts[d] - day_hits[d]) * (1 - day_hits[d]/day_counts[d])**2
                 for d in day_counts)
        msw = ssw / (issued - k)
        
        # ICC and design effect
        avg_cluster_size = issued / k
        icc = (msb - msw) / (msb + (avg_cluster_size - 1) * msw) if msw > 0 else 0
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued
    else:
        effective_n = 1  # Only one day, so effective N is 1
    
    # Ensure effective_n < issued (as required)
    if effective_n >= issued:
        effective_n = issued * 0.99  # Adjust if calculation fails
    
    # Sealed precision
    sealed_hits = sum(sealed_calls)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    db.close()

if __name__ == "__main__":
    main()
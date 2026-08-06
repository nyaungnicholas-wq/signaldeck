import sqlite3
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get all symbols with enough history
    cur.execute("SELECT id FROM symbols WHERE market='stocks'")
    symbol_ids = [row[0] for row in cur.fetchall()]
    
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return
    
    # Get all 1d bars
    cur.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY ts")
    all_bars = cur.fetchall()
    conn.close()
    
    # Organize bars by symbol
    symbol_bars = defaultdict(list)
    for sid, ts, close, vol in all_bars:
        symbol_bars[sid].append((ts, close, vol))
    
    # Calculate the 20% cutoff for sealed era
    all_ts = sorted([ts for bars in symbol_bars.values() for ts, _, _ in bars])
    if not all_ts:
        print("INSUFFICIENT=1")
        return
    cutoff_ts = all_ts[int(len(all_ts) * 0.8)]
    
    # Process each symbol
    opportunities = []
    for sid, bars in symbol_bars.items():
        if len(bars) < 504:  # Need at least 2 years of data
            continue
            
        # Sort by timestamp
        bars.sort(key=lambda x: x[0])
        
        # For each possible decision point
        for i in range(504, len(bars)):
            decision_ts, decision_close, decision_vol = bars[i]
            
            # Two-year window (504 trading days)
            window = bars[i-504:i+1]
            
            # Get prices and volumes for range calculations
            prices = [close for _, close, _ in window]
            volumes = [vol for _, _, vol in window]
            
            # Calculate 90th percentile of prices
            prices_sorted = sorted(prices)
            idx = int(0.9 * (len(prices_sorted) - 1))
            p90 = prices_sorted[idx]
            
            # Calculate median volume
            volumes_sorted = sorted(volumes)
            mid = len(volumes_sorted) // 2
            median_vol = volumes_sorted[mid]
            
            # Check conditions
            if decision_close >= p90 and decision_vol >= median_vol:
                # Find forward return (21 trading days ahead)
                if i + 21 < len(bars):
                    future_ts, future_close, _ = bars[i + 21]
                    fwd_return = (future_close - decision_close) / decision_close
                    hit = 1 if fwd_return > 0 else 0
                    
                    opportunities.append({
                        'ts': decision_ts,
                        'hit': hit,
                        'issued': True
                    })
                else:
                    # No forward data, skip
                    continue
            else:
                # Not issued, but counted as opportunity
                opportunities.append({
                    'ts': decision_ts,
                    'hit': None,
                    'issued': False
                })
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Split into regular and sealed era
    regular = [o for o in opportunities if o['ts'] < cutoff_ts]
    sealed = [o for o in opportunities if o['ts'] >= cutoff_ts]
    
    # Count metrics for regular era
    issued_reg = [o for o in regular if o['issued']]
    hits_reg = sum(o['hit'] for o in issued_reg)
    issued_count_reg = len(issued_reg)
    opportunities_count_reg = len(regular)
    
    # Count metrics for sealed era
    issued_seal = [o for o in sealed if o['issued']]
    hits_seal = sum(o['hit'] for o in issued_seal)
    issued_count_seal = len(issued_seal)
    
    # Calculate distinct days for issued calls
    distinct_days = set(o['ts'] // 86400 for o in issued_reg)
    
    # Calculate design effect for effective N
    # Group by day
    day_groups = defaultdict(list)
    for o in issued_reg:
        day = o['ts'] // 86400
        day_groups[day].append(o['hit'])
    
    if not day_groups:
        print("INSUFFICIENT=1")
        return
    
    # Calculate ICC using random effects model
    # Overall proportion
    overall_prop = hits_reg / issued_count_reg if issued_count_reg > 0 else 0
    
    # Variance between days
    sum_sq_between = 0
    for day, hits in day_groups.items():
        n_day = len(hits)
        p_day = sum(hits) / n_day
        sum_sq_between += n_day * ((p_day - overall_prop) ** 2)
    
    var_between = sum_sq_between / (len(day_groups) - 1) if len(day_groups) > 1 else 0
    
    # Variance within days
    sum_sq_within = 0
    for day, hits in day_groups.items():
        n_day = len(hits)
        p_day = sum(hits) / n_day
        sum_sq_within += (n_day - 1) * p_day * (1 - p_day)
    
    var_within = sum_sq_within / (issued_count_reg - len(day_groups)) if issued_count_reg > len(day_groups) else 0
    
    # ICC and design effect
    if var_within + var_between > 0:
        icc = var_between / (var_within + var_between)
        avg_cluster_size = issued_count_reg / len(day_groups)
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued_count_reg / design_effect
    else:
        design_effect = 1
        effective_n = issued_count_reg
    
    # Base rate calculation
    base_rate = hits_reg / issued_count_reg if issued_count_reg > 0 else 0
    
    # Print results
    print(f"ISSUED={issued_count_reg}")
    print(f"OPPORTUNITIES={opportunities_count_reg}")
    print(f"PRECISION={hits_reg / issued_count_reg:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={len(distinct_days)}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    
    # Sealed era precision
    if issued_count_seal > 0:
        print(f"SEALED_PRECISION={hits_seal / issued_count_seal:.6f}")
    else:
        print("SEALED_PRECISION=0.000000")

if __name__ == "__main__":
    main()
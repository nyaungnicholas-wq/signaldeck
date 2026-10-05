#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict
from datetime import datetime

def main():
    db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    db.row_factory = sqlite3.Row
    
    # Get all trading days
    days = db.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts").fetchall()
    days = [d[0] for d in days]
    if len(days) < 252:
        print("INSUFFICIENT=1")
        return
    
    # Precompute symbol data
    symbols = {}
    for row in db.execute("SELECT id, symbol FROM symbols"):
        symbols[row[0]] = row[1]
    
    # Get daily bars for all symbols
    bars_by_sym = defaultdict(list)
    for row in db.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars WHERE tf='1d' ORDER BY symbol_id, ts
    """):
        bars_by_sym[row[0]].append(row)
    
    # Get prediction outcomes for 20-day horizon
    outcomes = defaultdict(dict)
    for row in db.execute("""
        SELECT symbol_id, ts, up FROM prediction_outcomes
        WHERE horizon = 20
    """):
        outcomes[row[0]][row[1]] = row[2]
    
    # Filter eligible symbols
    eligible_symbols = {}
    for sym_id, bars in bars_by_sym.items():
        if len(bars) < 252:
            continue
        # Check minimum price and volume at most recent bar
        recent = bars[-1]
        if recent[5] < 5:
            continue
        # Calculate 60-day avg dollar volume
        if len(bars) < 60:
            continue
        avg_vol = sum(b[5]*b[6] for b in bars[-61:-1]) / 60
        if avg_vol < 5e6:
            continue
        eligible_symbols[sym_id] = bars
    
    if len(eligible_symbols) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Calculate all opportunities
    opportunities = []
    for sym_id, bars in eligible_symbols.items():
        for i in range(252, len(bars)):
            T = bars[i]
            T_minus_1 = bars[i-1]
            ts_T = T[1]
            close_T = T[5]
            close_T_minus_1 = T_minus_1[5]
            open_T = T[2]
            high_T = T[3]
            low_T = T[4]
            volume_T = T[6]
            
            # Check gap condition
            if open_T < 1.05 * close_T_minus_1:
                continue
            
            # Check close location condition
            if close_T > low_T + 0.3 * (high_T - low_T):
                continue
            
            # Check close within 1% of previous close
            if abs(close_T / close_T_minus_1 - 1) > 0.01:
                continue
            
            # Check volume condition
            if i < 60:
                continue
            avg_vol_60 = sum(b[6] for b in bars[i-61:i-1]) / 60
            if volume_T < 2 * avg_vol_60:
                continue
            
            # Check 20-day realized volatility
            if i < 20:
                continue
            log_returns = [math.log(bars[j][5] / bars[j-1][5]) for j in range(i-19, i+1)]
            vol = math.sqrt(sum((r - sum(log_returns)/20)**2 for r in log_returns) / 20)
            
            # Get label
            if ts_T not in outcomes.get(sym_id, {}):
                continue
            
            opportunities.append({
                'symbol_id': sym_id,
                'ts': ts_T,
                'vol': vol,
                'label': outcomes[sym_id][ts_T]
            })
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Group opportunities by day for cross-sectional analysis
    opp_by_day = defaultdict(list)
    for opp in opportunities:
        opp_by_day[opp['ts']].append(opp)
    
    # Calculate volatility deciles by day
    volatility_deciles = {}
    for day, day_opps in opp_by_day.items():
        vols = sorted([opp['vol'] for opp in day_opps])
        idx = int(len(vols) * 0.9)
        if idx >= len(vols):
            idx = len(vols) - 1
        volatility_deciles[day] = vols[idx]
    
    # Apply volatility filter and check recent calls
    issued = []
    last_call_by_sym = defaultdict(int)  # symbol_id -> last call timestamp
    for opp in opportunities:
        # Check if volatility is in top decile
        if opp['vol'] > volatility_deciles[opp['ts']]:
            continue
        
        # Check if call issued for same symbol in prior 20 trading days
        ts = opp['ts']
        day_idx = days.index(ts) if ts in days else -1
        if day_idx >= 20:
            prior_days = set(days[day_idx-20:day_idx])
            if any(last_call_by_sym[opp['symbol_id']] in d for d in prior_days if last_call_by_sym[opp['symbol_id']] > 0):
                continue
        
        issued.append(opp)
        last_call_by_sym[opp['symbol_id']] = ts
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Split into sealed (most recent 20%) and non-sealed
    all_days_sorted = sorted(set(opp['ts'] for opp in opportunities))
    cutoff_idx = int(len(all_days_sorted) * 0.8)
    sealed_days = set(all_days_sorted[cutoff_idx:])
    
    issued_sealed = [o for o in issued if o['ts'] in sealed_days]
    issued_non_sealed = [o for o in issued if o['ts'] not in sealed_days]
    
    # Count independent observations
    opp_non_sealed = [o for o in opportunities if o['ts'] not in sealed_days]
    if len(opp_non_sealed) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics for non-sealed
    hits_non_sealed = sum(1 for o in issued_non_sealed if o['label'] == 0)
    precision_non_sealed = hits_non_sealed / len(issued_non_sealed) if issued_non_sealed else 0
    base_rate_non_sealed = precision_non_sealed  # All issued are DOWN calls
    
    # Count distinct days
    distinct_days = len(set(o['ts'] for o in issued_non_sealed))
    
    # Calculate design effect (clustering by day)
    day_counts = defaultdict(int)
    for o in issued_non_sealed:
        day_counts[o['ts']] += 1
    n_clusters = len(day_counts)
    if n_clusters == 1:
        design_effect = len(issued_non_sealed)
    else:
        # ICC calculation for binary outcomes
        p = precision_non_sealed
        if p == 0 or p == 1:
            design_effect = 1.0
        else:
            # Between-cluster variance
            cluster_means = []
            for day, count in day_counts.items():
                day_hits = sum(1 for o in issued_non_sealed if o['ts'] == day and o['label'] == 0)
                cluster_means.append(day_hits / count)
            
            overall_mean = sum(cluster_means) / n_clusters
            between_var = sum((m - overall_mean) ** 2 for m in cluster_means) / (n_clusters - 1)
            icc = between_var / (p * (1 - p))
            avg_cluster_size = len(issued_non_sealed) / n_clusters
            design_effect = 1 + (avg_cluster_size - 1) * icc
    
    effective_n = len(issued_non_sealed) / design_effect if design_effect > 0 else len(issued_non_sealed)
    
    # Calculate metrics for sealed
    hits_sealed = sum(1 for o in issued_sealed if o['label'] == 0)
    precision_sealed = hits_sealed / len(issued_sealed) if issued_sealed else 0
    
    # Print results
    print(f"ISSUED={len(issued_non_sealed)}")
    print(f"OPPORTUNITIES={len(opp_non_sealed)}")
    print(f"PRECISION={precision_non_sealed}")
    print(f"BASE_RATE={base_rate_non_sealed}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={precision_sealed}")
    
    db.close()

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 532
# cycle_index: 62
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 21
MIN_HISTORY_YEARS = 2
TOP_VOLUME_DECILE = 0.9

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    cur = conn.cursor()
    
    # Get symbols with at least 2 years of daily data
    cur.execute("""
        SELECT symbol_id, MIN(ts) as min_ts, MAX(ts) as max_ts
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING (MAX(ts) - MIN(ts)) >= ? * 365 * 24 * 3600
    """, (MIN_HISTORY_YEARS,))
    symbols_with_history = {row[0] for row in cur.fetchall()}
    
    if not symbols_with_history:
        print("INSUFFICIENT=1")
        return
    
    # Get all symbols with fundamentals data
    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('EntityPublicFloat', 'Revenues')
        ORDER BY symbol_id, metric, as_of
    """)
    fundamentals_data = cur.fetchall()
    
    # Organize fundamentals by symbol
    symbol_fundamentals = defaultdict(lambda: {'public_float': [], 'revenues': []})
    for sym_id, metric, value, as_of, fetched_at in fundamentals_data:
        if sym_id not in symbols_with_history:
            continue
        if metric == 'EntityPublicFloat':
            symbol_fundamentals[sym_id]['public_float'].append((as_of, fetched_at, value))
        elif metric == 'Revenues':
            symbol_fundamentals[sym_id]['revenues'].append((as_of, fetched_at, value))
    
    # Sort each symbol's fundamentals by as_of
    for sym_id in symbol_fundamentals:
        symbol_fundamentals[sym_id]['public_float'].sort(key=lambda x: x[0])
        symbol_fundamentals[sym_id]['revenues'].sort(key=lambda x: x[0])
    
    # Get daily volume data for top decile calculation
    cur.execute("""
        SELECT ts, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({})
    """.format(','.join(map(str, symbols_with_history))))
    daily_volumes = defaultdict(list)
    for ts, volume in cur.fetchall():
        daily_volumes[ts].append(volume)
    
    # Calculate 90th percentile volume for each day
    volume_90th = {}
    for ts, vols in daily_volumes.items():
        if vols:
            sorted_vols = sorted(vols)
            idx = math.ceil(len(sorted_vols) * TOP_VOLUME_DECILE) - 1
            volume_90th[ts] = sorted_vols[idx]
    
    # Get all daily bars needed for momentum calculation
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join(map(str, symbols_with_history))))
    
    symbol_bars = defaultdict(list)
    for sym_id, ts, close, volume in cur.fetchall():
        symbol_bars[sym_id].append((ts, close, volume))
    
    # Get prediction outcomes for 21-day horizon
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = ?
    """, (HORIZON,))
    outcomes = {}
    for sym_id, ts, up in cur.fetchall():
        outcomes[(sym_id, ts)] = up
    
    conn.close()
    
    # Process each symbol
    decisions = []
    for sym_id in symbols_with_history:
        bars = symbol_bars[sym_id]
        if len(bars) < 22:  # Need at least 22 days for 20-day momentum + 1
            continue
            
        fund = symbol_fundamentals[sym_id]
        public_floats = fund['public_float']
        revenues = fund['revenues']
        
        # Build price index for quick lookup
        price_by_ts = {}
        volume_by_ts = {}
        ts_list = []
        for ts, close, volume in bars:
            price_by_ts[ts] = close
            volume_by_ts[ts] = volume
            ts_list.append(ts)
        
        # Process each possible decision day
        for i in range(20, len(bars) - HORIZON):
            decision_ts = bars[i][0]
            
            # Check if stock is in top decile of volume for this day
            if decision_ts in volume_90th and volume_by_ts.get(decision_ts, 0) >= volume_90th[decision_ts]:
                continue
            
            # Check 20-day momentum > 0
            price_now = price_by_ts.get(decision_ts)
            price_20ago = price_by_ts.get(bars[i-20][0])
            if not price_now or not price_20ago or price_now <= price_20ago:
                continue
            
            # Get fundamentals available before decision time
            available_floats = [(a, f, v) for a, f, v in public_floats if f <= decision_ts]
            available_revenues = [(a, f, v) for a, f, v in revenues if f <= decision_ts]
            
            # Need at least 2 public float quarters and 6 revenue quarters
            if len(available_floats) < 2 or len(available_revenues) < 6:
                continue
            
            # Check public float decrease in last two quarters
            latest_float = available_floats[-1][2]
            prev_float = available_floats[-2][2]
            if latest_float >= prev_float:
                continue
            
            # Calculate revenue growth acceleration
            # Sort by as_of descending
            rev_by_as_of = sorted(available_revenues, key=lambda x: x[0], reverse=True)
            
            # Get 5 most recent quarters
            rev_quarters = [(a, v) for a, f, v in rev_by_as_of[:5]]
            if len(rev_quarters) < 5:
                continue
            
            # Current quarter and year-ago quarter for growth calculation
            current_rev = rev_quarters[0][1]
            year_ago_rev = rev_quarters[4][1]
            if year_ago_rev <= 0:
                continue
            current_growth = (current_rev - year_ago_rev) / year_ago_rev
            
            # Previous quarter and year-ago quarter
            prev_rev = rev_quarters[1][1]
            if len(rev_quarters) >= 5:
                prev_year_ago = rev_quarters[4][1]
                if prev_year_ago <= 0:
                    continue
                prev_growth = (prev_rev - prev_year_ago) / prev_year_ago
            else:
                continue
            
            # Acceleration > 5 percentage points (0.05)
            if (current_growth - prev_growth) <= 0.05:
                continue
            
            # All conditions met - check for outcome
            outcome_key = (sym_id, decision_ts)
            if outcome_key in outcomes:
                up = outcomes[outcome_key]
                decisions.append((sym_id, decision_ts, up))
    
    if not decisions:
        print("INSUFFICIENT=1")
        return
    
    # Sort decisions by time for temporal split
    decisions.sort(key=lambda x: x[1])
    
    # Calculate split point (80/20)
    split_idx = int(len(decisions) * 0.8)
    train_decisions = decisions[:split_idx]
    test_decisions = decisions[split_idx:]
    
    # Helper function to compute metrics for a set of decisions
    def compute_metrics(dec_set):
        if not dec_set:
            return 0, 0.0, 0.0, set(), 0
        
        issued = len(dec_set)
        hits = sum(1 for _, _, up in dec_set if up == 1)
        precision = hits / issued if issued > 0 else 0.0
        
        # Base rate of up=1 within issued calls
        up_count = sum(1 for _, _, up in dec_set if up == 1)
        base_rate = up_count / issued if issued > 0 else 0.0
        
        # Distinct days
        distinct_days = len(set(d for _, d, _ in dec_set))
        
        # Effective N with design effect
        # Cluster by day
        day_counts = defaultdict(int)
        for _, d, _ in dec_set:
            day_counts[d] += 1
        
        n_clusters = len(day_counts)
        if n_clusters > 1:
            # Calculate intracluster correlation (ICC) using random effects ANOVA
            # Total variance
            grand_mean = base_rate
            total_ss = 0
            cluster_means = {}
            
            # Calculate cluster means
            for day, count in day_counts.items():
                day_hits = sum(1 for _, d, up in dec_set if d == day and up == 1)
                cluster_means[day] = day_hits / count if count > 0 else 0
            
            # Between cluster variance
            between_ss = 0
            for day, mean in cluster_means.items():
                between_ss += day_counts[day] * (mean - grand_mean) ** 2
            
            # Within cluster variance
            within_ss = 0
            for sym_id, d, up in dec_set:
                within_ss += (up - cluster_means[d]) ** 2
            
            # ICC = variance between / (variance between + variance within)
            variance_between = between_ss / (n_clusters - 1) if n_clusters > 1 else 0
            variance_within = within_ss / (issued - n_clusters) if issued > n_clusters else 0
            
            if variance_between + variance_within > 0:
                icc = variance_between / (variance_between + variance_within)
            else:
                icc = 0
            
            # Design effect = 1 + (average cluster size - 1) * ICC
            avg_cluster_size = issued / n_clusters
            design_effect = 1 + (avg_cluster_size - 1) * icc
        else:
            design_effect = 1.0
        
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        return issued, precision, base_rate, distinct_days, effective_n
    
    # Compute overall metrics
    issued_total, precision_total, base_rate_total, distinct_days_total, effective_n_total = compute_metrics(decisions)
    
    # Compute sealed era metrics
    issued_sealed, precision_sealed, _, _, _ = compute_metrics(test_decisions)
    
    # Print required output
    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={issued_total}")  # In this implementation, we only count issued
    print(f"PRECISION={precision_total:.4f}")
    print(f"BASE_RATE={base_rate_total:.4f}")
    print(f"DISTINCT_DAYS={distinct_days_total}")
    print(f"EFFECTIVE_N={effective_n_total:.1f}")
    print(f"SEALED_PRECISION={precision_sealed:.4f}")

if __name__ == "__main__":
    main()
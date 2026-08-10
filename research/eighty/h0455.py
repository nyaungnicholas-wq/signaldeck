# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 454
# cycle_index: 45
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    # Get all Form 4 open-market purchases with as-of discipline
    cursor = conn.execute("""
        SELECT i.symbol_id, i.filed_ts, i.insider, s.symbol
        FROM insider_trades i
        JOIN symbols s ON i.symbol_id = s.id
        WHERE i.code = 'P'  -- open-market purchase
        ORDER BY i.filed_ts
    """)
    trades = [dict(row) for row in cursor.fetchall()]
    
    if len(trades) == 0:
        print("INSUFFICIENT=1")
        return

    # Get all fundamental data for SharesOutstanding
    cursor = conn.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding'
        ORDER BY symbol_id, as_of
    """)
    fundamentals = {}
    for row in cursor:
        sid = row['symbol_id']
        if sid not in fundamentals:
            fundamentals[sid] = []
        fundamentals[sid].append({
            'value': row['value'],
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at']
        })

    # Process trades to find eligible entry points
    entry_points = []
    for trade in trades:
        sid = trade['symbol_id']
        filed_ts = trade['filed_ts']
        
        # Parse filed_ts as datetime
        try:
            decision_date = datetime.fromisoformat(filed_ts.replace('Z', '+00:00'))
        except:
            continue
            
        # Get fundamentals for this symbol, sorted by as_of
        if sid not in fundamentals:
            continue
            
        # Filter fundamentals available before decision date (as-of discipline)
        available = []
        for f in fundamentals[sid]:
            try:
                as_of_date = datetime.fromisoformat(f['as_of'])
                if as_of_date < decision_date:
                    available.append(f)
            except:
                continue
                
        # Need at least 3 quarters to check 2 consecutive declines
        if len(available) < 3:
            continue
            
        # Sort by as_of descending to get most recent first
        available.sort(key=lambda x: x['as_of'], reverse=True)
        
        # Check for two consecutive quarterly declines
        # Most recent: q0, then q1, then q2
        q0, q1, q2 = available[0], available[1], available[2]
        
        if q0['value'] < q1['value'] and q1['value'] < q2['value']:
            entry_points.append({
                'symbol_id': sid,
                'symbol': trade['symbol'],
                'decision_date': decision_date,
                'filed_ts': filed_ts
            })

    if len(entry_points) == 0:
        print("INSUFFICIENT=1")
        return

    # Get labels for 21-day forward returns
    horizon_days = 21
    outcomes = {}
    cursor = conn.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = ?
    """, (horizon_days,))
    for row in cursor:
        key = (row['symbol_id'], row['ts'])
        outcomes[key] = {
            'up': row['up'],
            'fwd_return': row['fwd_return']
        }

    # Filter to entry points with available labels and compute returns
    valid_entries = []
    for entry in entry_points:
        key = (entry['symbol_id'], entry['filed_ts'])
        if key in outcomes:
            entry['label'] = outcomes[key]['up']
            entry['fwd_return'] = outcomes[key]['fwd_return']
            valid_entries.append(entry)

    if len(valid_entries) == 0:
        print("INSUFFICIENT=1")
        return

    # Sort by decision date
    valid_entries.sort(key=lambda x: x['decision_date'])

    # Split into training and sealed era (most recent 20%)
    n = len(valid_entries)
    split_idx = int(n * 0.8)
    train = valid_entries[:split_idx]
    sealed = valid_entries[split_idx:]

    # Function to compute metrics for a set of entries
    def compute_metrics(entries):
        if len(entries) == 0:
            return None, None, None, None, None, None
        
        # Count independent observations: one per (symbol, UTC day)
        day_counts = {}
        day_labels = {}
        for e in entries:
            day = e['decision_date'].strftime('%Y-%m-%d')
            key = (e['symbol_id'], day)
            if key not in day_counts:
                day_counts[key] = 0
                day_labels[key] = []
            day_counts[key] += 1
            day_labels[key].append(e['label'])
        
        # Use first observation per day per symbol
        observations = []
        unique_days = set()
        for (sid, day), count in day_counts.items():
            # Take the first observation for this symbol-day
            for e in entries:
                e_day = e['decision_date'].strftime('%Y-%m-%d')
                if e['symbol_id'] == sid and e_day == day:
                    observations.append(e['label'])
                    unique_days.add(day)
                    break
        
        if len(observations) == 0:
            return None, None, None, None, None, None
            
        hits = sum(observations)
        precision = hits / len(observations)
        
        # Base rate is same as precision since we're predicting positive
        base_rate = precision
        
        # Compute design effect for clustering by day
        # Group observations by day
        day_groups = {}
        for e in entries:
            day = e['decision_date'].strftime('%Y-%m-%d')
            if day not in day_groups:
                day_groups[day] = []
            day_groups[day].append(e['label'])
        
        n_clusters = len(day_groups)
        if n_clusters <= 1:
            design_effect = 1.0
        else:
            # ICC calculation for binary outcomes
            total_mean = sum(observations) / len(observations)
            total_var = sum((x - total_mean) ** 2 for x in observations) / (len(observations) - 1) if len(observations) > 1 else 0
            
            between_var = 0
            for day, group in day_groups.items():
                if len(group) > 1:
                    group_mean = sum(group) / len(group)
                    between_var += len(group) * (group_mean - total_mean) ** 2
            between_var /= (n_clusters - 1) if n_clusters > 1 else 1
            
            # Within variance
            within_var = 0
            for day, group in day_groups.items():
                if len(group) > 1:
                    group_mean = sum(group) / len(group)
                    within_var += sum((x - group_mean) ** 2 for x in group)
            within_var /= (len(observations) - n_clusters) if len(observations) > n_clusters else 1
            
            total_var_calc = between_var * (n_clusters / len(observations)) + within_var * ((len(observations) - n_clusters) / len(observations))
            rho = between_var / total_var_calc if total_var_calc > 0 else 0
            
            avg_cluster_size = len(observations) / n_clusters
            design_effect = 1 + (avg_cluster_size - 1) * rho
        
        effective_n = len(observations) / design_effect if design_effect > 0 else len(observations)
        
        return len(observations), precision, base_rate, len(unique_days), effective_n, hits

    # Compute metrics for all valid entries
    total_issued, total_precision, total_base_rate, total_days, total_eff_n, _ = compute_metrics(valid_entries)
    if total_issued is None:
        print("INSUFFICIENT=1")
        return
    
    # Compute metrics for sealed era
    sealed_issued, sealed_precision, sealed_base_rate, sealed_days, sealed_eff_n, _ = compute_metrics(sealed)
    if sealed_issued is None:
        # If no sealed entries, report 0 precision
        sealed_precision = 0.0
    
    # Print required metrics
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={len(valid_entries)}")
    print(f"PRECISION={total_precision:.4f}")
    print(f"BASE_RATE={total_base_rate:.4f}")
    print(f"DISTINCT_DAYS={total_days}")
    print(f"EFFECTIVE_N={total_eff_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    # Validate invariants
    if total_days > total_issued:
        print("ERROR: DISTINCT_DAYS exceeds ISSUED")
    if total_eff_n >= total_issued:
        print("ERROR: EFFECTIVE_N not less than ISSUED")
    
    conn.close()

if __name__ == "__main__":
    main()
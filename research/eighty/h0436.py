# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 435
# cycle_index: 26
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta

def get_quarter_end(dt):
    """Convert date to quarter end date (YYYY-MM-DD) for 13F alignment."""
    q_month = ((dt.month - 1) // 3) * 3 + 3
    q_year = dt.year if q_month <= 12 else dt.year + 1
    if q_month > 12:
        q_month = 12
    import calendar
    last_day = calendar.monthrange(q_year, q_month)[1]
    return datetime(q_year, q_month, last_day).strftime('%Y-%m-%d')

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get all active symbols (stocks and crypto) that might have data
    symbols = [row['symbol_id'] for row in 
               conn.execute("SELECT id as symbol_id FROM symbols WHERE active=1 AND market IN ('stocks','crypto')")]
    
    opportunities = []
    calls = []
    
    for symbol_id in symbols:
        # Get public float data (EntityPublicFloat) with fetched_at as as-of date
        pf_data = conn.execute("""
            SELECT as_of as quarter_end, value as public_float, fetched_at
            FROM fundamentals
            WHERE symbol_id=? AND metric='EntityPublicFloat'
            ORDER BY as_of
        """, (symbol_id,)).fetchall()
        
        if len(pf_data) < 3:
            continue  # Need at least 3 quarters of public float
        
        # Get institutional holdings by quarter (summed value)
        inst_data = conn.execute("""
            SELECT period as quarter_end, SUM(value) as inst_value
            FROM inst_holdings
            WHERE symbol_id=?
            GROUP BY period
            ORDER BY period
        """, (symbol_id,)).fetchall()
        
        if len(inst_data) < 2:
            continue  # Need at least 2 quarters of institutional data
        
        # Align quarters: create mapping of quarter_end to (public_float, fetched_at) and inst_value
        pf_by_q = {}
        for row in pf_data:
            pf_by_q[row['quarter_end']] = {
                'public_float': row['public_float'],
                'fetched_at': row['fetched_at']
            }
        
        inst_by_q = {}
        for row in inst_data:
            inst_by_q[row['quarter_end']] = row['inst_value']
        
        # Get all quarter ends that appear in either dataset
        all_q_ends = sorted(set(pf_by_q.keys()) | set(inst_by_q.keys()))
        
        # For each possible entry point (quarter + 45 days)
        for i, q_end in enumerate(all_q_ends):
            if i < 2:
                continue  # Need at least two previous quarters
            
            # Current quarter must have both public float and institutional data
            if q_end not in pf_by_q or q_end not in inst_by_q:
                continue
            
            # Need two previous quarters for public float decline condition
            if i < 2:
                continue
            
            # Get previous two quarters
            prev_q = all_q_ends[i-1]
            prev_prev_q = all_q_ends[i-2]
            
            if prev_q not in pf_by_q or prev_prev_q not in pf_by_q:
                continue
            
            # Check public float decline: Q_{t-2} > Q_{t-1} > Q_t
            pf_t2 = pf_by_q[prev_prev_q]['public_float']
            pf_t1 = pf_by_q[prev_q]['public_float']
            pf_t = pf_by_q[q_end]['public_float']
            
            if pf_t2 is None or pf_t1 is None or pf_t is None:
                continue
            if pf_t2 <= pf_t1 or pf_t1 <= pf_t:
                continue  # Not declining
            
            # Check institutional ownership increase in most recent quarter
            if prev_q not in inst_by_q:
                continue
            inst_t = inst_by_q[q_end]
            inst_t1 = inst_by_q[prev_q]
            
            if inst_t is None or inst_t1 is None:
                continue
            if inst_t <= inst_t1:
                continue  # Not increasing
            
            # Check fetched_at for public float data is available before decision point
            fetched_at = pf_by_q[q_end]['fetched_at']
            if fetched_at is None:
                continue
            
            # Parse dates
            q_end_dt = datetime.strptime(q_end, '%Y-%m-%d')
            fetched_at_dt = datetime.strptime(fetched_at, '%Y-%m-%d')
            decision_date = q_end_dt + timedelta(days=45)
            
            # Ensure public float data was fetched before decision date
            if fetched_at_dt >= decision_date:
                continue
            
            # Check 20-day average dollar volume at decision point
            # We need bars up to decision_date - 1 (as-of discipline)
            decision_ts = int(decision_date.timestamp())
            
            # Get last 20 trading days before decision
            vol_rows = conn.execute("""
                SELECT ts, close, volume
                FROM bars
                WHERE symbol_id=? AND tf='1d' AND ts < ?
                ORDER BY ts DESC
                LIMIT 20
            """, (symbol_id, decision_ts)).fetchall()
            
            if len(vol_rows) < 20:
                continue  # Not enough trading data
            
            # Calculate 20-day average dollar volume
            total_dollar_vol = sum(row['close'] * row['volume'] for row in vol_rows)
            avg_dollar_vol = total_dollar_vol / len(vol_rows)
            
            if avg_dollar_vol < 1_000_000:
                continue  # Below $1M threshold
            
            # Get label from prediction_outcomes
            label_row = conn.execute("""
                SELECT up, fwd_return
                FROM prediction_outcomes
                WHERE symbol_id=? AND ts=? AND horizon=21
            """, (symbol_id, decision_ts)).fetchone()
            
            if label_row is None:
                continue  # No label available
            
            # Record opportunity and call
            opportunities.append({
                'symbol_id': symbol_id,
                'decision_date': decision_date.strftime('%Y-%m-%d'),
                'decision_ts': decision_ts
            })
            
            calls.append({
                'symbol_id': symbol_id,
                'decision_date': decision_date.strftime('%Y-%m-%d'),
                'decision_ts': decision_ts,
                'up': label_row['up'],
                'fwd_return': label_row['fwd_return']
            })
    
    conn.close()
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Sort calls by decision date
    calls.sort(key=lambda x: x['decision_ts'])
    
    # Split into train (first 80%) and sealed era (last 20%)
    split_idx = int(len(calls) * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]
    
    # Compute metrics
    issued = len(calls)
    opportunities_count = len(opportunities)
    
    # Precision and base rate for entire sample
    hits = sum(1 for c in calls if c['up'] == 1)
    precision = hits / issued if issued > 0 else 0
    base_rate = precision  # Base rate of predicted class (up) in issued calls
    
    # Distinct days in issued calls
    distinct_days = len(set(c['decision_date'] for c in calls))
    
    # Compute design effect and effective N
    # Group calls by day
    day_groups = {}
    for c in calls:
        day = c['decision_date']
        if day not in day_groups:
            day_groups[day] = {'total': 0, 'hits': 0}
        day_groups[day]['total'] += 1
        if c['up'] == 1:
            day_groups[day]['hits'] += 1
    
    # Compute intra-class correlation (ICC)
    total_calls = issued
    total_hits = hits
    
    # Overall proportion
    p = total_hits / total_calls if total_calls > 0 else 0
    
    # Sum of squared deviations
    ssd = 0
    for day, data in day_groups.items():
        n_j = data['total']
        p_j = data['hits'] / n_j if n_j > 0 else 0
        ssd += n_j * ((p_j - p) ** 2)
    
    # Variance between clusters
    var_between = ssd / (total_calls - 1) if total_calls > 1 else 0
    
    # Average cluster size
    m = total_calls / len(day_groups) if day_groups else 1
    
    # ICC calculation
    if p * (1 - p) > 0:
        icc = var_between / (p * (1 - p))
    else:
        icc = 0
    
    # Design effect
    de = 1 + (m - 1) * icc
    
    # Effective N
    effective_n = total_calls / de if de > 0 else total_calls
    
    # SEALED_PRECISION
    sealed_hits = sum(1 for c in sealed_calls if c['up'] == 1)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    # Print required outputs
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
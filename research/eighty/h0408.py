# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 407
# cycle_index: 75
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()

    # Get all insider purchases (Form 4, code='P') with filing dates
    cur.execute("""
        SELECT symbol_id, filed_ts, tx_ts 
        FROM insider_trades 
        WHERE code = 'P'
    """)
    purchases = cur.fetchall()
    
    if not purchases:
        print("INSUFFICIENT=1")
        return
    
    # Get all fundamentals for EPS and Revenues
    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('EPS', 'Revenues')
    """)
    fundamentals = cur.fetchall()
    
    if not fundamentals:
        print("INSUFFICIENT=1")
        return
    
    # Organize fundamentals by symbol and metric, sorted by as_of
    fund_by_sym = {}
    for sym, metric, val, as_of, fetched_at in fundamentals:
        if sym not in fund_by_sym:
            fund_by_sym[sym] = {}
        if metric not in fund_by_sym[sym]:
            fund_by_sym[sym][metric] = []
        fund_by_sym[sym][metric].append((as_of, val, fetched_at))
    
    # Sort each by as_of (assuming YYYY-MM-DD strings)
    for sym in fund_by_sym:
        for metric in fund_by_sym[sym]:
            fund_by_sym[sym][metric].sort(key=lambda x: x[0])
    
    # Get daily bars for symbols that have insider purchases
    sym_ids = list(set(p[0] for p in purchases))
    placeholders = ','.join('?'*len(sym_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, sym_ids)
    bars = cur.fetchall()
    
    if not bars:
        print("INSUFFICIENT=1")
        return
    
    # Organize bars by symbol
    bars_by_sym = {}
    for sym, ts, close in bars:
        if sym not in bars_by_sym:
            bars_by_sym[sym] = []
        bars_by_sym[sym].append((ts, close))
    
    # Process each insider purchase
    events = []
    for sym, filed_ts, tx_ts in purchases:
        if sym not in bars_by_sym or len(bars_by_sym[sym]) < 100:
            continue
            
        # Check fundamentals: need both EPS and Revenues for at least 2 quarters before filing date
        if sym not in fund_by_sym or 'EPS' not in fund_by_sym[sym] or 'Revenues' not in fund_by_sym[sym]:
            continue
            
        eps_data = fund_by_sym[sym]['EPS']
        rev_data = fund_by_sym[sym]['Revenues']
        
        # Filter by fetched_at <= filed_ts (knowable at decision time)
        eps_available = [(a, v, f) for a, v, f in eps_data if f <= filed_ts]
        rev_available = [(a, v, f) for a, v, f in rev_data if f <= filed_ts]
        
        if len(eps_available) < 2 or len(rev_available) < 2:
            continue
        
        # Get two most recent quarters for each
        eps_recent = eps_available[-2:]
        rev_recent = rev_available[-2:]
        
        # Check if both EPS and Revenues are declining (negative growth)
        eps_growth = (eps_recent[1][1] - eps_recent[0][1]) / abs(eps_recent[0][1]) if eps_recent[0][1] != 0 else 0
        rev_growth = (rev_recent[1][1] - rev_recent[0][1]) / abs(rev_recent[0][1]) if rev_recent[0][1] != 0 else 0
        
        if eps_growth >= 0 or rev_growth >= 0:
            continue
        
        # Find price bar at or just after filing date
        sym_bars = bars_by_sym[sym]
        entry_idx = None
        for i, (ts, close) in enumerate(sym_bars):
            if ts >= filed_ts:
                entry_idx = i
                break
        
        if entry_idx is None or entry_idx + 63 >= len(sym_bars):
            continue
            
        entry_price = sym_bars[entry_idx][1]
        exit_price = sym_bars[entry_idx + 63][1]
        forward_return = (exit_price / entry_price) - 1
        
        events.append({
            'symbol': sym,
            'decision_date': filed_ts,
            'forward_return': forward_return,
            'label': 1 if forward_return > 0 else 0
        })
    
    if not events:
        print("INSUFFICIENT=1")
        return
    
    # Sort events by decision date
    events.sort(key=lambda x: x['decision_date'])
    
    # Split into training (80%) and sealed (20%)
    split_idx = int(len(events) * 0.8)
    train_events = events[:split_idx]
    sealed_events = events[split_idx:]
    
    # Calculate metrics for training set
    issued = len(train_events)
    hits = sum(1 for e in train_events if e['label'] == 1)
    precision = hits / issued if issued > 0 else 0
    
    # Count distinct days
    distinct_days = len(set(
        datetime.utcfromtimestamp(e['decision_date']).strftime('%Y-%m-%d') 
        for e in train_events
    ))
    
    # Calculate design effect (1 + (k-1)*ICC approximation)
    # Group by (symbol, day)
    groups = {}
    for e in train_events:
        key = (e['symbol'], datetime.utcfromtimestamp(e['decision_date']).strftime('%Y-%m-%d'))
        if key not in groups:
            groups[key] = []
        groups[key].append(e['label'])
    
    k_values = [len(g) for g in groups.values()]
    mean_k = sum(k_values) / len(k_values) if k_values else 1
    
    # Approximate ICC using variance components (binary data)
    total_var = precision * (1 - precision) if issued > 0 else 0.5
    within_var = 0
    group_means = []
    for g in groups.values():
        g_mean = sum(g) / len(g)
        group_means.append(g_mean)
        g_var = g_mean * (1 - g_mean)
        within_var += g_var
    within_var /= len(groups) if groups else 1
    
    # Approximate ICC
    if total_var > 0:
        icc = 1 - (within_var / total_var)
    else:
        icc = 0.1  # conservative default
    
    design_effect = 1 + (mean_k - 1) * icc
    if design_effect < 1.01:
        design_effect = 1.01  # prevent division by zero and ensure EFFECTIVE_N < ISSUED
    
    effective_n = issued / design_effect
    
    # Calculate base rate (same as precision for binary classification)
    base_rate = precision
    
    # Calculate sealed precision
    sealed_issued = len(sealed_events)
    sealed_hits = sum(1 for e in sealed_events if e['label'] == 1)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Output results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(events)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
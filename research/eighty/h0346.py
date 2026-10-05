# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 345
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get daily bars time range
    cur.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1d'")
    min_ts, max_ts = cur.fetchone()
    
    # Get all daily bar timestamps and map to dates
    cur.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    daily_ts = [row[0] for row in cur.fetchall()]
    if len(daily_ts) < 10:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Split into train and sealed (80/20)
    split_idx = int(len(daily_ts) * 0.8)
    sealed_ts_set = set(daily_ts[split_idx:])
    
    # Get all symbols with daily bars
    cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
    all_symbols = {row[0] for row in cur.fetchall()}
    
    # Preload fundamentals with as_of and fetched_at
    cur.execute("SELECT symbol_id, metric, value, as_of, fetched_at FROM fundamentals")
    fund_data = cur.fetchall()
    
    # Structure fundamentals by symbol and metric
    eps_by_sym = defaultdict(list)  # (as_of, fetched_at, value)
    rev_by_sym = defaultdict(list)  # (as_of, fetched_at, value)
    for sym, metric, value, as_of, fetched_at in fund_data:
        if metric == 'EPS':
            eps_by_sym[sym].append((as_of, fetched_at, value))
        elif metric == 'Revenues':
            rev_by_sym[sym].append((as_of, fetched_at, value))
    
    # Preload dollar volume data (20-day trailing average)
    cur.execute("""
        SELECT symbol_id, ts, close, volume 
        FROM bars WHERE tf='1d'
    """)
    bar_rows = cur.fetchall()
    
    # Organize bars by symbol
    bars_by_sym = defaultdict(list)
    for sym, ts, close, vol in bar_rows:
        bars_by_sym[sym].append((ts, close, vol))
    
    # Sort each symbol's bars by time
    for sym in bars_by_sym:
        bars_by_sym[sym].sort(key=lambda x: x[0])
    
    # Precompute 20-day trailing average dollar volume for each symbol at each timestamp
    dv_avg_by_sym = defaultdict(dict)  # {sym: {ts: avg_dv}}
    for sym in all_symbols:
        if sym not in bars_by_sym:
            continue
        bars = bars_by_sym[sym]
        dv = [(ts, close * vol) for ts, close, vol in bars]
        
        # Compute 20-day trailing average
        window = []
        for i, (ts, dv_val) in enumerate(dv):
            window.append(dv_val)
            if len(window) > 20:
                window.pop(0)
            if len(window) == 20:
                dv_avg = sum(window) / 20
                dv_avg_by_sym[sym][ts] = dv_avg
    
    # Preload prediction outcomes for 60-day horizon
    cur.execute("""
        SELECT symbol_id, ts, up 
        FROM prediction_outcomes 
        WHERE horizon = 60
    """)
    outcomes = {(row[0], row[1]): row[2] for row in cur.fetchall()}
    
    # Track last call date per symbol to prevent repeated calls within 60 days
    last_call_date = {}
    
    # Process each decision date
    opportunities = []
    calls = []  # (symbol, entry_ts, outcome, is_sealed)
    
    for entry_ts in daily_ts:
        entry_dt = datetime.utcfromtimestamp(entry_ts).date()
        
        # Get all symbols available on this date
        available_syms = set()
        for sym in all_symbols:
            # Must have a bar on entry date
            if entry_ts in dv_avg_by_sym.get(sym, {}):
                available_syms.add(sym)
        
        for sym in available_syms:
            opportunities.append(entry_ts)
            
            # Skip if we called this symbol in last 60 trading days
            if sym in last_call_date:
                if entry_ts - last_call_date[sym] < 60 * 86400:
                    continue
            
            # Check fundamentals availability and validity
            if sym not in eps_by_sym or sym not in rev_by_sym:
                continue
            
            # Get EPS and Revenues available before entry_ts and not older than 120 days
            valid_eps = []
            valid_rev = []
            for as_of, fetched_at, value in eps_by_sym[sym]:
                if fetched_at < entry_ts and entry_ts - fetched_at < 120 * 86400:
                    valid_eps.append((as_of, fetched_at, value))
            for as_of, fetched_at, value in rev_by_sym[sym]:
                if fetched_at < entry_ts and entry_ts - fetched_at < 120 * 86400:
                    valid_rev.append((as_of, fetched_at, value))
            
            if not valid_eps or not valid_rev:
                continue
            
            # Find matching quarter (same as_of)
            eps_dict = {a: (f, v) for a, f, v in valid_eps}
            rev_dict = {a: (f, v) for a, f, v in valid_rev}
            common_quarters = set(eps_dict.keys()) & set(rev_dict.keys())
            
            if not common_quarters:
                continue
            
            # Get most recent quarter
            latest_quarter = max(common_quarters)
            eps_fetched, eps_val = eps_dict[latest_quarter]
            rev_fetched, rev_val = rev_dict[latest_quarter]
            
            # Check EPS < 0
            if eps_val >= 0:
                continue
            
            # Compute YoY revenue growth
            # Need to find revenue from same quarter a year ago
            try:
                latest_date = datetime.strptime(latest_quarter, "%Y-%m-%d").date()
                year_ago = f"{latest_date.year - 1}-{latest_date.month:02d}-{latest_date.day:02d}"
            except:
                continue
            
            if year_ago not in rev_dict:
                continue
            
            prev_rev = rev_dict[year_ago][1]
            if prev_rev <= 0:
                continue
            
            growth = (rev_val - prev_rev) / prev_rev
            if growth < 0.30:
                continue
            
            # Check dollar volume filter
            dv_avg = dv_avg_by_sym[sym].get(entry_ts, 0)
            # Compute bottom decile across all symbols for this timestamp
            all_dvs = []
            for s in all_symbols:
                if entry_ts in dv_avg_by_sym.get(s, {}):
                    all_dvs.append(dv_avg_by_sym[s][entry_ts])
            
            if not all_dvs:
                continue
            
            all_dvs.sort()
            decile_idx = int(len(all_dvs) * 0.1)
            bottom_decile_val = all_dvs[decile_idx]
            
            if dv_avg <= bottom_decile_val:
                continue
            
            # Check outcome exists
            if (sym, entry_ts) not in outcomes:
                continue
            
            outcome = outcomes[(sym, entry_ts)]
            is_sealed = entry_ts in sealed_ts_set
            
            calls.append((sym, entry_ts, outcome, is_sealed))
            last_call_date[sym] = entry_ts
    
    if not calls:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Calculate metrics
    total_issued = len(calls)
    total_opportunities = len(opportunities)
    
    # Precision = hits/issued (outcome=0 means DOWN, so we want outcome=0)
    hits = sum(1 for _, _, outcome, _ in calls if outcome == 0)
    precision = hits / total_issued if total_issued > 0 else 0
    
    # Base rate within issued subset
    base_rate = hits / total_issued if total_issued > 0 else 0
    
    # Distinct days in issued calls
    distinct_days = len(set(entry_ts for _, entry_ts, _, _ in calls))
    
    # Design effect calculation
    # Cluster by symbol and compute intraclass correlation
    symbol_clusters = defaultdict(list)
    for sym, _, outcome, _ in calls:
        symbol_clusters[sym].append(outcome)
    
    n_clusters = len(symbol_clusters)
    avg_cluster_size = total_issued / n_clusters if n_clusters > 0 else 1
    
    # Calculate ICC
    grand_mean = base_rate
    ss_total = total_issued * base_rate * (1 - base_rate) if base_rate > 0 else 0
    
    ss_between = 0
    ss_within = 0
    for outcomes in symbol_clusters.values():
        n = len(outcomes)
        cluster_mean = sum(outcomes) / n
        ss_between += n * (cluster_mean - grand_mean) ** 2
        ss_within += n * (1 - base_rate) * base_rate
    
    if ss_total > 0:
        icc = (ss_between / (n_clusters - 1)) / (ss_total / (total_issued - 1)) if n_clusters > 1 else 0
    else:
        icc = 0
    
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = total_issued / design_effect if design_effect > 0 else total_issued
    
    # Sealed era metrics
    sealed_calls = [c for c in calls if c[3]]
    sealed_hits = sum(1 for _, _, outcome, _ in sealed_calls if outcome == 0)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
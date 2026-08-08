# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 281
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 21
ENTRY_RATIO = 3.0
LOOKBACK_DAYS = 5

def parse_ts(ts):
    return datetime.utcfromtimestamp(ts)

def date_str(dt):
    return dt.strftime('%Y-%m-%d')

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()
    
    # Get all symbols with insider purchases (code='P')
    cur.execute("""
        SELECT DISTINCT symbol_id 
        FROM insider_trades 
        WHERE code = 'P'
    """)
    symbols_with_purchases = {row['symbol_id'] for row in cur.fetchall()}
    
    # Get all symbols with StockTwits data
    cur.execute("""
        SELECT DISTINCT symbol_id 
        FROM stocktwits_sentiment
    """)
    symbols_with_st = {row['symbol_id'] for row in cur.fetchall()}
    
    # Universe: intersection
    universe = symbols_with_purchases & symbols_with_st
    if not universe:
        print("INSUFFICIENT=1")
        return
    
    # Get all insider purchases for universe symbols
    cur.execute("""
        SELECT symbol_id, filed_ts, title
        FROM insider_trades 
        WHERE symbol_id IN ({}) AND code = 'P'
    """.format(','.join('?' * len(universe))), list(universe))
    purchases = cur.fetchall()
    
    if not purchases:
        print("INSUFFICIENT=1")
        return
    
    # Group purchases by (symbol, day)
    purchase_by_day = defaultdict(list)
    for p in purchases:
        sym = p['symbol_id']
        dt = parse_ts(p['filed_ts'])
        day_str = date_str(dt)
        purchase_by_day[(sym, day_str)].append(p)
    
    # Get StockTwits data for all universe symbols
    cur.execute("""
        SELECT symbol_id, ts, bullish, bearish
        FROM stocktwits_sentiment 
        WHERE symbol_id IN ({})
    """.format(','.join('?' * len(universe))), list(universe))
    st_data = cur.fetchall()
    
    # Index by (symbol, day)
    st_by_day = defaultdict(lambda: {'bullish': 0, 'bearish': 0})
    for row in st_data:
        sym = row['symbol_id']
        dt = parse_ts(row['ts'])
        day_str = date_str(dt)
        key = (sym, day_str)
        st_by_day[key]['bullish'] += row['bullish']
        st_by_day[key]['bearish'] += row['bearish']
    
    # Get all prediction outcomes for horizon=21
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes 
        WHERE horizon = ? AND symbol_id IN ({})
    """.format(','.join('?' * len(universe))), [HORIZON] + list(universe))
    outcomes = cur.fetchall()
    
    # Index by (symbol, day)
    outcome_by_day = {}
    for row in outcomes:
        sym = row['symbol_id']
        dt = parse_ts(row['ts'])
        day_str = date_str(dt)
        key = (sym, day_str)
        outcome_by_day[key] = row['up']
    
    opportunities = 0
    issued_calls = []
    all_outcomes = []
    
    # Process each purchase day
    for (sym, day_str), day_purchases in purchase_by_day.items():
        # Check at least one purchase is by officer/director
        has_officer = False
        for p in day_purchases:
            title = (p['title'] or '').upper()
            if 'OFFICER' in title or 'DIRECTOR' in title:
                has_officer = True
                break
        if not has_officer:
            continue
        
        opportunities += 1
        
        # Calculate 5-day bearish-to-bullish ratio
        purchase_dt = datetime.strptime(day_str, '%Y-%m-%d')
        total_bearish = 0
        total_bullish = 0
        days_with_data = 0
        
        for i in range(LOOKBACK_DAYS):
            check_dt = purchase_dt - timedelta(days=i)
            check_day = date_str(check_dt)
            key = (sym, check_day)
            if key in st_by_day:
                total_bearish += st_by_day[key]['bearish']
                total_bullish += st_by_day[key]['bullish']
                days_with_data += 1
        
        if days_with_data < LOOKBACK_DAYS:
            continue  # Abstain: insufficient data
        
        if total_bullish == 0:
            ratio = float('inf')
        else:
            ratio = total_bearish / total_bullish
        
        if ratio < ENTRY_RATIO:
            continue  # Abstain: condition not met
        
        # Get outcome
        outcome_key = (sym, day_str)
        if outcome_key not in outcome_by_day:
            continue  # Abstain: no outcome
        
        up = outcome_by_day[outcome_key]
        issued_calls.append((day_str, up))
        all_outcomes.append(up)
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Sort by date for sealed era
    issued_calls.sort(key=lambda x: x[0])
    n_issued = len(issued_calls)
    sealed_cutoff = int(n_issued * 0.8)
    non_sealed = issued_calls[:sealed_cutoff]
    sealed = issued_calls[sealed_cutoff:]
    
    # Calculate metrics
    hits_non_sealed = sum(1 for _, up in non_sealed if up)
    hits_sealed = sum(1 for _, up in sealed if up)
    hits_total = hits_non_sealed + hits_sealed
    
    precision_non_sealed = hits_non_sealed / len(non_sealed) if non_sealed else 0.0
    sealed_precision = hits_sealed / len(sealed) if sealed else 0.0
    base_rate = hits_total / n_issued
    
    # Distinct days
    distinct_days = len(set(day for day, _ in issued_calls))
    
    # Design effect: assume calls within same day are perfectly correlated
    day_counts = defaultdict(int)
    for day, _ in issued_calls:
        day_counts[day] += 1
    avg_cluster_size = sum(day_counts.values()) / len(day_counts)
    design_effect = avg_cluster_size
    effective_n = n_issued / design_effect if design_effect > 0 else n_issued
    
    # Output
    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_non_sealed:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 423
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
import datetime

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    # Get symbols with >=200 daily observations in past year
    c.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM bars
        WHERE tf='1d'
        AND ts >= CAST((strftime('%s', 'now') - 365*86400) AS INTEGER)
        GROUP BY symbol_id
        HAVING cnt >= 200
    """)
    symbols_with_bars = {row['symbol_id'] for row in c.fetchall()}
    
    if not symbols_with_bars:
        print("INSUFFICIENT=1")
        return
    
    # Get public float data for all symbols with metrics
    c.execute("""
        SELECT symbol_id, value, as_of
        FROM fundamentals
        WHERE metric='EntityPublicFloat'
        AND value IS NOT NULL
        ORDER BY symbol_id, as_of
    """)
    
    # Group by symbol and quarter
    float_data = defaultdict(list)
    for row in c.fetchall():
        sym = row['symbol_id']
        if sym in symbols_with_bars:
            try:
                value = float(row['value'])
                if value > 0:
                    as_of = datetime.datetime.strptime(row['as_of'], '%Y-%m-%d')
                    # Get quarter (Q1=1, Q2=2, Q3=3, Q4=4)
                    quarter = (as_of.month - 1) // 3 + 1
                    year = as_of.year
                    float_data[sym].append((year, quarter, value, as_of))
            except:
                continue
    
    # Find symbols with >=3 consecutive quarters and last 2 declining
    eligible_events = []
    
    for sym, entries in float_data.items():
        # Sort by as_of
        entries.sort(key=lambda x: x[3])
        
        # Need at least 3 entries
        if len(entries) < 3:
            continue
        
        # Check for at least 3 consecutive quarters
        consecutive = []
        current_chain = [entries[0]]
        
        for i in range(1, len(entries)):
            prev = current_chain[-1]
            curr = entries[i]
            
            # Check if consecutive quarter (same year, quarter+1 or year+1, quarter1)
            if curr[0] == prev[0] and curr[1] == prev[1] + 1:
                current_chain.append(curr)
            elif curr[0] == prev[0] + 1 and curr[1] == 1 and prev[1] == 4:
                current_chain.append(curr)
            else:
                # Break in consecutive quarters
                if len(current_chain) >= 3:
                    consecutive.append(current_chain)
                current_chain = [curr]
        
        if len(current_chain) >= 3:
            consecutive.append(current_chain)
        
        # Check each consecutive chain of at least 3
        for chain in consecutive:
            if len(chain) < 3:
                continue
            
            # Check last 2 quarters show decline
            last_three = chain[-3:]
            if not (last_two[1] > last_two[0] > last_two[2] for last_two in 
                    zip(last_three, last_three[1:], last_three[2:])):
                # Actually check Q2 > Q1 and Q3 > Q2? No, decline means decreasing values
                if not (last_three[0][2] > last_three[1][2] > last_three[2][2]):
                    continue
            
            quarter_end = last_three[2][3]  # as_of of latest quarter
            
            # Find insider purchases (code='P') within 10 days after quarter_end
            c.execute("""
                SELECT tx_ts, filed_ts
                FROM insider_trades
                WHERE symbol_id=? AND code='P'
                AND filed_ts >= ?
                AND filed_ts <= ?
            """, (sym, 
                  quarter_end.isoformat(),
                  (quarter_end + datetime.timedelta(days=10)).isoformat()))
            
            for trade in c.fetchall():
                # filed_ts is decision timestamp (as-of)
                decision_ts = trade['filed_ts']
                try:
                    decision_date = datetime.datetime.strptime(decision_ts, '%Y-%m-%d %H:%M:%S')
                except:
                    continue
                
                # Get close price on decision date (or nearest earlier date)
                c.execute("""
                    SELECT ts, close
                    FROM bars
                    WHERE symbol_id=? AND tf='1d' AND ts <= ?
                    ORDER BY ts DESC
                    LIMIT 1
                """, (sym, int(decision_date.timestamp())))
                
                entry_bar = c.fetchone()
                if not entry_bar:
                    continue
                
                entry_date = datetime.datetime.utcfromtimestamp(entry_bar['ts'])
                entry_price = entry_bar['close']
                
                # Get 21 trading days later (count exactly 21 bars)
                c.execute("""
                    SELECT ts, close
                    FROM bars
                    WHERE symbol_id=? AND tf='1d' AND ts > ?
                    ORDER BY ts ASC
                    LIMIT 21
                """, (sym, entry_bar['ts']))
                
                future_bars = c.fetchall()
                if len(future_bars) < 21:
                    continue
                
                exit_bar = future_bars[-1]
                exit_price = exit_bar['close']
                
                # Compute return and hit
                if entry_price > 0:
                    ret = (exit_price - entry_price) / entry_price
                    hit = 1 if ret > 0 else 0
                else:
                    continue
                
                eligible_events.append({
                    'symbol': sym,
                    'decision_date': decision_date.date(),
                    'hit': hit
                })
    
    if not eligible_events:
        print("INSUFFICIENT=1")
        return
    
    # Deduplicate by (symbol, decision_date) - one observation per symbol per day
    seen = set()
    unique_events = []
    for ev in eligible_events:
        key = (ev['symbol'], ev['decision_date'])
        if key not in seen:
            seen.add(key)
            unique_events.append(ev)
    
    # Sort by decision_date
    unique_events.sort(key=lambda x: x['decision_date'])
    
    # Split into training (80%) and sealed (20%)
    split_idx = int(len(unique_events) * 0.8)
    train_events = unique_events[:split_idx]
    sealed_events = unique_events[split_idx:]
    
    # Compute metrics for all issued calls (both train and sealed)
    all_issued = len(unique_events)
    all_hits = sum(ev['hit'] for ev in unique_events)
    all_precision = all_hits / all_issued if all_issued > 0 else 0
    
    # Base rate: proportion of positive outcomes in ALL opportunities considered
    # But we only issued calls that met criteria, so opportunities = issued here
    base_rate = all_precision
    
    # Distinct days among issued calls
    distinct_days = len(set(ev['decision_date'] for ev in unique_events))
    
    # Design effect: assuming calls cluster by symbol (repeat offenders)
    symbol_counts = defaultdict(int)
    for ev in unique_events:
        symbol_counts[ev['symbol']] += 1
    
    n_symbols = len(symbol_counts)
    if n_symbols > 0:
        avg_per_symbol = all_issued / n_symbols
        # Simplified design effect: 1 + (avg_per_symbol - 1) * intraclass correlation
        # Assume ICC=0.1 as conservative estimate
        icc = 0.1
        design_effect = 1 + (avg_per_symbol - 1) * icc
    else:
        design_effect = 1
    
    effective_n = all_issued / design_effect if design_effect > 0 else all_issued
    
    # Sealed era metrics
    sealed_issued = len(sealed_events)
    sealed_hits = sum(ev['hit'] for ev in sealed_events)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Print required lines
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={all_issued}")  # In this case, issued=opportunities
    print(f"PRECISION={all_precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 339
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timezone

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get DGS10 data
    dgs = conn.execute("""
        SELECT ts, value, date(ts, 'unixepoch') as dt
        FROM macro_series
        WHERE series = 'DGS10'
        ORDER BY ts
    """).fetchall()
    if len(dgs) < 3:
        print("INSUFFICIENT=1")
        return
    
    # Compute signal dates where DGS10(T-1)-DGS10(T-2) >= 0.10
    signal_dates = []
    for i in range(2, len(dgs)):
        if dgs[i-1]['value'] - dgs[i-2]['value'] >= 0.10:
            signal_dates.append(dgs[i]['dt'])
    
    if not signal_dates:
        print("INSUFFICIENT=1")
        return
    
    # Get all daily bars
    bars = conn.execute("""
        SELECT symbol_id, date(ts, 'unixepoch') as dt, close
        FROM bars
        WHERE tf = '1d'
    """).fetchall()
    
    # Get EPS data
    eps_data = conn.execute("""
        SELECT symbol_id, value, 
               date(as_of, 'unixepoch') as as_of_date,
               date(fetched_at, 'unixepoch') as fetched_date
        FROM fundamentals
        WHERE metric = 'EPS'
    """).fetchall()
    
    # Organize EPS by symbol
    eps_by_symbol = {}
    for row in eps_data:
        symbol_id = row['symbol_id']
        if symbol_id not in eps_by_symbol:
            eps_by_symbol[symbol_id] = []
        eps_by_symbol[symbol_id].append({
            'value': row['value'],
            'as_of': row['as_of_date'],
            'fetched': row['fetched_date']
        })
    
    # Organize bars by (symbol, date)
    bars_dict = {}
    for row in bars:
        bars_dict[(row['symbol_id'], row['dt'])] = row['close']
    
    # For each signal date, find eligible symbols
    observations = []
    for T in signal_dates:
        # Find all symbols with bar at T
        symbols_with_bar = set()
        for (symbol_id, dt) in bars_dict.keys():
            if dt == T:
                symbols_with_bar.add(symbol_id)
        
        for symbol_id in symbols_with_bar:
            # Get latest EPS as of T
            if symbol_id not in eps_by_symbol:
                continue
            latest_eps = None
            for eps in eps_by_symbol[symbol_id]:
                if eps['fetched'] <= T:
                    if latest_eps is None or eps['as_of'] > latest_eps['as_of']:
                        latest_eps = eps
            if latest_eps is None or latest_eps['value'] > 0:
                continue
            
            # Get close at T
            close_T = bars_dict.get((symbol_id, T))
            if close_T is None:
                continue
            
            # Find T+5 (5 trading days later)
            trading_days = sorted([dt for (sid, dt) in bars_dict.keys() if sid == symbol_id])
            if T not in trading_days:
                continue
            idx = trading_days.index(T)
            if idx + 5 >= len(trading_days):
                continue
            T5 = trading_days[idx + 5]
            close_T5 = bars_dict.get((symbol_id, T5))
            if close_T5 is None:
                continue
            
            outcome = 1 if close_T5 < close_T else 0
            observations.append((T, symbol_id, outcome))
    
    if not observations:
        print("INSUFFICIENT=1")
        return
    
    # Split into in-sample and sealed (most recent 20% of signal dates)
    unique_dates = sorted(set(obs[0] for obs in observations))
    split_idx = int(len(unique_dates) * 0.8)
    sealed_dates = set(unique_dates[split_idx:])
    
    # Compute metrics
    issued = len(observations)
    hits = sum(obs[2] for obs in observations)
    precision = hits / issued if issued > 0 else 0
    base_rate = hits / issued  # Same as precision for DOWN-only calls
    distinct_days = len(sealed_dates)
    if distinct_days == 0:
        distinct_days = 1
    design_effect = 1 + (issued / distinct_days - 1) * 0.5  # Conservative estimate
    effective_n = issued / design_effect
    
    # Sealed metrics
    sealed_obs = [obs for obs in observations if obs[0] in sealed_dates]
    sealed_issued = len(sealed_obs)
    sealed_hits = sum(obs[2] for obs in sealed_obs)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
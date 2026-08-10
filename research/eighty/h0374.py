# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 373
# cycle_index: 41
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import bisect
from collections import defaultdict
from statistics import median

DB = 'file:data/signaldeck.db?mode=ro'

def main():
    con = sqlite3.connect(DB, uri=True)
    con.row_factory = sqlite3.Row
    c = con.cursor()
    
    # Get universe: symbols with insider trades and daily bars
    c.execute("""
        SELECT DISTINCT symbol_id 
        FROM insider_trades 
        WHERE symbol_id IN (SELECT DISTINCT symbol_id FROM bars WHERE tf='1d')
    """)
    symbols = [r['symbol_id'] for r in c.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Preload insider trades (open-market purchases only)
    insider_buys = defaultdict(list)
    c.execute("""
        SELECT symbol_id, filed_ts 
        FROM insider_trades 
        WHERE code = 'P'
    """)
    for r in c.fetchall():
        insider_buys[r['symbol_id']].append(r['filed_ts'])
    for k in insider_buys:
        insider_buys[k].sort()
    
    # Preload public floats (most recent fetched_at for each symbol)
    c.execute("""
        SELECT symbol_id, value 
        FROM fundamentals 
        WHERE metric = 'EntityPublicFloat'
        GROUP BY symbol_id
        HAVING fetched_at = MAX(fetched_at)
    """)
    public_floats = {r['symbol_id']: float(r['value']) for r in c.fetchall()}
    if not public_floats:
        print("INSUFFICIENT=1")
        return
    median_float = median(public_floats.values())
    
    # Load all daily bars for the universe
    bars = defaultdict(list)
    c.execute("""
        SELECT symbol_id, ts, close, volume 
        FROM bars 
        WHERE tf='1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(symbols))), symbols)
    for r in c.fetchall():
        bars[r['symbol_id']].append((r['ts'], r['close'], r['volume']))
    
    # Load prediction outcomes for 21-day horizon
    c.execute("""
        SELECT symbol_id, ts, up, fwd_return 
        FROM prediction_outcomes 
        WHERE horizon = 21
    """)
    outcomes = {}
    for r in c.fetchall():
        outcomes[(r['symbol_id'], r['ts'])] = (r['up'], r['fwd_return'])
    
    # Find decision points and issue calls
    calls = []
    opportunities = 0
    
    for sym in symbols:
        sym_bars = bars.get(sym)
        if len(sym_bars) < 252:
            continue
        sym_insider = insider_buys.get(sym, [])
        sym_float = public_floats.get(sym)
        if sym_float is None or sym_float > median_float:
            continue
        
        closes = [b[1] for b in sym_bars]
        volumes = [b[2] for b in sym_bars]
        timestamps = [b[0] for b in sym_bars]
        
        # Process each potential decision day
        for i in range(252, len(sym_bars)):
            ts, close, vol = sym_bars[i]
            opportunities += 1
            
            # Check 1-year low (252-day window)
            window_closes = closes[i-252:i]
            if close > min(window_closes):
                continue
            
            # Check bottom-decile volume (60-day average)
            window_volumes = volumes[i-60:i]
            avg_vol = sum(window_volumes) / len(window_volumes)
            if avg_vol > 0 and vol / avg_vol > 0.1:
                continue
            
            # Check insider purchase within 5 calendar days
            ts_date = ts  # Already unix epoch
            end_ts = ts + 5*86400
            idx = bisect.bisect_left(sym_insider, ts_date)
            if idx >= len(sym_insider) or sym_insider[idx] > end_ts:
                continue
            
            # All conditions met - issue call
            # Check if we have outcome data
            outcome = outcomes.get((sym, ts))
            if outcome is None:
                continue
            
            calls.append({
                'symbol': sym,
                'ts': ts,
                'up': outcome[0],
                'fwd_return': outcome[1]
            })
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Sort calls by time
    calls.sort(key=lambda x: x['ts'])
    
    # Split into regular and sealed (last 20% by time)
    split_idx = int(len(calls) * 0.8)
    regular_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]
    
    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0, 0, 0, 0
        
        issued = len(call_list)
        hits = sum(1 for c in call_list if c['up'])
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued  # Same as precision for issued set
        
        # Count distinct days
        distinct_days = len(set(c['ts'] // 86400 for c in call_list))
        
        # Compute design effect (clustering by day)
        day_clusters = defaultdict(list)
        for c in call_list:
            day = c['ts'] // 86400
            day_clusters[day].append(c['up'])
        
        # ICC calculation for binary data
        total_prop = hits / issued if issued > 0 else 0
        between_var = 0
        for day, outcomes in day_clusters.items():
            n_day = len(outcomes)
            prop_day = sum(outcomes) / n_day
            between_var += n_day * (prop_day - total_prop)**2
        
        if len(day_clusters) > 1:
            k_avg = issued / len(day_clusters)
            s2_b = between_var / (issued - 1) if issued > 1 else 0
            total_var = total_prop * (1 - total_prop)
            icc = s2_b / total_var if total_var > 0 else 0
            deff = 1 + (k_avg - 1) * icc
        else:
            deff = issued  # Only one cluster
        
        effective_n = issued / deff if deff > 0 else 0
        
        return issued, precision, base_rate, distinct_days, effective_n
    
    # Compute metrics for regular set
    issued, precision, base_rate, distinct_days, effective_n = compute_metrics(regular_calls)
    
    # Compute metrics for sealed set
    sealed_issued, sealed_precision, _, _, _ = compute_metrics(sealed_calls)
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    con.close()

if __name__ == "__main__":
    main()
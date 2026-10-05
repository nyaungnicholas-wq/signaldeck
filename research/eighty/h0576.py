# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 575
# cycle_index: 33
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
from collections import defaultdict
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return

    c = conn.cursor()
    
    # Get all symbols with enough history
    c.execute("""
        SELECT symbol_id, COUNT(*) as days
        FROM bars
        WHERE tf='1d'
        GROUP BY symbol_id
        HAVING days >= 63
    """)
    valid_symbols = {row[0] for row in c.fetchall()}
    
    if not valid_symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Get all daily bars with necessary columns, ordered by symbol and time
    c.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf='1d'
        ORDER BY symbol_id, ts
    """)
    
    # Build daily data per symbol
    symbol_data = defaultdict(list)
    for row in c.fetchall():
        symbol_id, ts, open_, high, low, close, volume = row
        if symbol_id in valid_symbols:
            symbol_data[symbol_id].append((ts, open_, high, low, close, volume))
    
    # Precompute for each symbol: rolling 20-day average dollar volume, 20-day return, and previous day data
    symbol_stats = {}
    for symbol_id, data in symbol_data.items():
        if len(data) < 63:  # need at least 63 days for 20-day metrics + 21 days for return
            continue
            
        stats = []
        # Precompute rolling windows
        prices = [d[4] for d in data]  # close
        volumes = [d[5] for d in data]
        dollar_volumes = [prices[i] * volumes[i] for i in range(len(prices))]
        
        # Compute 20-day average dollar volume (up to day i-1) and 20-day return (up to day i-1)
        for i in range(62, len(data)-5):  # need at least 62 days to compute 20-day metrics at i-1
            ts, open_, high, low, close, volume = data[i]
            
            # Check as-of: we can use data up to i-1
            # 20-day average dollar volume through i-1
            if i-20 >= 0:
                avg_dollar_vol = sum(dollar_volumes[i-20:i]) / 20
            else:
                avg_dollar_vol = sum(dollar_volumes[:i]) / i
                
            # 20-day return through i-1
            if i-21 >= 0:
                ret20 = (prices[i-1] / prices[i-21]) - 1
            else:
                continue  # Not enough history
                
            # Previous day's close and low
            prev_close = prices[i-1]
            prev_low = data[i-1][3]
            
            stats.append({
                'ts': ts,
                'open': open_,
                'high': high,
                'low': low,
                'close': close,
                'volume': volume,
                'avg_dollar_vol': avg_dollar_vol,
                'ret20': ret20,
                'prev_close': prev_close,
                'prev_low': prev_low,
                'next_close': prices[i+5] if i+5 < len(prices) else None
            })
        
        if stats:
            symbol_stats[symbol_id] = stats
    
    conn.close()
    
    if not symbol_stats:
        print("INSUFFICIENT=1")
        return
    
    # Flatten all opportunities and sort by time
    opportunities = []
    for symbol_id, stats in symbol_stats.items():
        for s in stats:
            opportunities.append((s['ts'], symbol_id, s))
    
    opportunities.sort(key=lambda x: x[0])
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Determine sealed era cutoff (most recent 20% of days)
    all_days = sorted(set(opp[0] for opp in opportunities))
    cutoff_idx = int(len(all_days) * 0.8)
    sealed_cutoff = all_days[cutoff_idx] if cutoff_idx < len(all_days) else all_days[-1]
    
    # Issue calls
    issued = []
    base_down_count = 0
    total_opportunities = 0
    
    for ts, symbol_id, s in opportunities:
        total_opportunities += 1
        
        # Entry conditions
        if (s['open'] >= 1.03 * s['prev_close'] and
            s['close'] < s['prev_low'] and
            s['volume'] >= 1.5 * s['avg_dollar_vol'] and
            s['ret20'] >= 0.10):
            
            # Check if we have outcome data
            if s['next_close'] is not None:
                is_down = s['next_close'] < s['close']
                issued.append({
                    'ts': ts,
                    'symbol_id': symbol_id,
                    'is_down': is_down,
                    'sealed': ts >= sealed_cutoff
                })
                if is_down:
                    base_down_count += 1
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Compute metrics
    issued_count = len(issued)
    hits = sum(1 for x in issued if x['is_down'])
    precision = hits / issued_count
    
    # Base rate is proportion of down in issued (predicted class)
    base_rate = base_down_count / issued_count
    
    # Distinct days in issued
    distinct_days = len(set(x['ts'] for x in issued))
    
    # Effective N: need design effect - approximate using day clustering
    day_counts = defaultdict(int)
    for x in issued:
        day_counts[x['ts']] += 1
    avg_cluster_size = issued_count / len(day_counts) if day_counts else 1
    # Assume ICC ~ 0.05 as rough estimate for financial data
    icc = 0.05
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = issued_count / design_effect
    
    # Sealed era metrics
    sealed_issued = [x for x in issued if x['sealed']]
    sealed_hits = sum(1 for x in sealed_issued if x['is_down'])
    sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0.0
    
    # Output results
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
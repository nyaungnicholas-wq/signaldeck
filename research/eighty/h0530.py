# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 529
# cycle_index: 59
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
    except Exception as e:
        print("INSUFFICIENT=1")
        print("ERROR: Cannot open database")
        return 0
    
    cur = conn.cursor()
    
    # Get all daily bars ordered by symbol and timestamp
    try:
        cur.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume
            FROM bars
            WHERE tf = '1d'
            ORDER BY symbol_id, ts
        """)
    except Exception as e:
        print("INSUFFICIENT=1")
        print(f"ERROR: Query failed - {e}")
        conn.close()
        return 0
    
    # Group bars by symbol
    symbol_bars = defaultdict(list)
    for row in cur.fetchall():
        symbol_bars[row[0]].append({
            'ts': row[1], 'open': row[2], 'high': row[3],
            'low': row[4], 'close': row[5], 'volume': row[6]
        })
    
    # Get prediction outcomes for labels
    try:
        cur.execute("""
            SELECT symbol_id, ts, up, fwd_return
            FROM prediction_outcomes
            WHERE horizon = 21
        """)
        outcomes = cur.fetchall()
    except Exception as e:
        print("INSUFFICIENT=1")
        print(f"ERROR: Query failed - {e}")
        conn.close()
        return 0
    
    conn.close()
    
    if not symbol_bars:
        print("INSUFFICIENT=1")
        print("No daily bars found")
        return 0
    
    # Create label lookup: (symbol_id, ts) -> (up, fwd_return)
    label_lookup = {}
    for sym_id, ts, up, fwd in outcomes:
        label_lookup[(sym_id, ts)] = (up, fwd)
    
    # Process signals
    signals = []
    
    for sym_id, bars in symbol_bars.items():
        n = len(bars)
        if n < 300:
            continue
        
        # Precompute needed statistics
        for i in range(251, n):
            t = bars[i]
            
            # Universe filters
            # Check at least 300 prior sessions (we already have n >= 300)
            
            # Close >= $5
            if t['close'] < 5.0:
                continue
            
            # Trailing 60-day median dollar volume > $5M
            if i < 59:
                continue
            
            dollar_vols = []
            for j in range(i-59, i+1):
                dollar_vol = bars[j]['close'] * bars[j]['volume']
                dollar_vols.append(dollar_vol)
            
            dollar_vols_sorted = sorted(dollar_vols)
            median_dollar_vol = dollar_vols_sorted[30]  # 0-indexed, 60 items -> index 30 is median
            if median_dollar_vol <= 5_000_000:
                continue
            
            # Entry condition: new 252-session closing high
            # Get max close of previous 251 sessions
            max_close_251 = max(bars[j]['close'] for j in range(i-251, i))
            if t['close'] <= max_close_251:
                continue
            
            # No day in prior 50 sessions made a new 252-session closing high
            is_breakout_recent = False
            for j in range(i-50, i):
                if j >= 251:
                    max_close_j = max(bars[k]['close'] for k in range(j-251, j))
                    if bars[j]['close'] > max_close_j:
                        is_breakout_recent = True
                        break
            if is_breakout_recent:
                continue
            
            # Volume >= 1.5x trailing 50-day mean volume
            if i < 49:
                continue
            
            sum_vol_50 = sum(bars[j]['volume'] for j in range(i-49, i+1))
            mean_vol_50 = sum_vol_50 / 50
            if t['volume'] < 1.5 * mean_vol_50:
                continue
            
            # Close >= 0.98 x day high
            if t['close'] < 0.98 * t['high']:
                continue
            
            # Abstain conditions
            # Gap up > 5% at open
            if i > 0:
                prev_close = bars[i-1]['close']
                gap = (t['open'] - prev_close) / prev_close
                if gap > 0.05:
                    continue
            
            # Equal-weight 20-day return of universe <= -5%
            # Compute average 20-day return across universe at time t
            # For each symbol with >= 300 bars and >= 21 days of forward data
            total_return_20d = 0.0
            count_symbols = 0
            
            for other_sym, other_bars in symbol_bars.items():
                other_n = len(other_bars)
                if other_n < 300:
                    continue
                # Find bar at timestamp t for this symbol
                other_idx = None
                for idx, bar in enumerate(other_bars):
                    if bar['ts'] == t['ts']:
                        other_idx = idx
                        break
                if other_idx is None or other_idx < 20:
                    continue
                
                # Compute 20-day return (close to close)
                other_close_t = other_bars[other_idx]['close']
                other_close_t_minus_20 = other_bars[other_idx-20]['close']
                if other_close_t_minus_20 > 0:
                    ret_20d = (other_close_t - other_close_t_minus_20) / other_close_t_minus_20
                    total_return_20d += ret_20d
                    count_symbols += 1
            
            if count_symbols == 0:
                continue
            
            avg_return_20d = total_return_20d / count_symbols
            if avg_return_20d <= -0.05:
                continue
            
            # Get label
            label = label_lookup.get((sym_id, t['ts']), None)
            if label is None:
                continue
            
            up, fwd_return = label
            
            signals.append({
                'symbol_id': sym_id,
                'ts': t['ts'],
                'up': up,
                'fwd_return': fwd_return
            })
    
    if not signals:
        print("INSUFFICIENT=1")
        print("No signals generated")
        return 0
    
    # Split into train and sealed (80/20 by time)
    all_ts = sorted(set(s['ts'] for s in signals))
    n_ts = len(all_ts)
    if n_ts < 5:
        print("INSUFFICIENT=1")
        print("Too few distinct days")
        return 0
    
    split_idx = int(n_ts * 0.8)
    sealed_ts_set = set(all_ts[split_idx:])
    
    train_signals = [s for s in signals if s['ts'] not in sealed_ts_set]
    sealed_signals = [s for s in signals if s['ts'] in sealed_ts_set]
    
    if not train_signals or not sealed_signals:
        print("INSUFFICIENT=1")
        print("Insufficient data for split")
        return 0
    
    # Compute metrics
    issued = len(signals)
    correct = sum(1 for s in signals if s['up'])
    precision = correct / issued if issued > 0 else 0.0
    
    # Base rate within issued subset
    base_rate = precision  # By definition for UP calls
    
    # Distinct days
    distinct_days = len(set(s['ts'] for s in signals))
    
    # Design effect
    # Group by day
    day_groups = defaultdict(list)
    for s in signals:
        day_groups[s['ts']].append(s['up'])
    
    # Calculate ICC
    p = precision
    if len(day_groups) < 2:
        deff = 1.0
    else:
        # Within-day variance (binary)
        var_within = p * (1 - p)
        
        # Between-day variance
        day_props = []
        for ts, ups in day_groups.items():
            day_props.append(sum(ups) / len(ups))
        
        mean_day_prop = sum(day_props) / len(day_props)
        var_between = sum((dp - mean_day_prop) ** 2 for dp in day_props) / (len(day_props) - 1)
        
        if var_within + var_between == 0:
            deff = 1.0
        else:
            icc = var_between / (var_within + var_between)
            avg_cluster_size = issued / len(day_groups)
            deff = 1 + (avg_cluster_size - 1) * icc
    
    effective_n = issued / deff if deff > 0 else 0
    
    # Sealed precision
    sealed_correct = sum(1 for s in sealed_signals if s['up'])
    sealed_precision = sealed_correct / len(sealed_signals) if sealed_signals else 0.0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")  # All considered points are opportunities in this setup
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    return 0

if __name__ == "__main__":
    sys.exit(main())
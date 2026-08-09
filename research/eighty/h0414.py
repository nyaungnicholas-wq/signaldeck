# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 413
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
import statistics

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    cursor = conn.cursor()
    
    # Get all symbols with daily bars
    cursor.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
    symbol_ids = [row[0] for row in cursor.fetchall()]
    
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return
    
    # Get VIXCLS series from macro_series
    cursor.execute("SELECT ts, value FROM macro_series WHERE series='VIXCLS' ORDER BY ts")
    vix_rows = cursor.fetchall()
    if len(vix_rows) < 2:
        print("INSUFFICIENT=1")
        return
    
    vix_dict = {}
    for ts, value in vix_rows:
        vix_dict[ts] = value
    
    # Get SPY daily bars for beta calculation
    cursor.execute("""
        SELECT b.symbol_id, b.ts, b.close
        FROM bars b
        JOIN symbols s ON b.symbol_id = s.id
        WHERE s.symbol = 'SPY' AND b.tf = '1d'
        ORDER BY b.ts
    """)
    spy_rows = cursor.fetchall()
    if not spy_rows:
        print("INSUFFICIENT=1")
        return
    
    # Build spy return series
    spy_returns = {}
    for i in range(1, len(spy_rows)):
        ts = spy_rows[i][1]
        close_prev = spy_rows[i-1][2]
        close_curr = spy_rows[i][2]
        if close_prev > 0:
            spy_returns[ts] = (close_curr - close_prev) / close_prev
    
    # Get all daily bars for all symbols (we need to compute rolling stats)
    cursor.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf='1d'
        ORDER BY symbol_id, ts
    """)
    all_bars = cursor.fetchall()
    
    # Group bars by symbol
    symbol_bars = defaultdict(list)
    for symbol_id, ts, close, volume in all_bars:
        symbol_bars[symbol_id].append((ts, close, volume))
    
    # For each symbol, compute rolling stats
    symbol_stats = {}
    for symbol_id, bars in symbol_bars.items():
        if len(bars) < 253:  # Need at least 253 days (252 prior + current)
            continue
            
        # Compute rolling 252-day median dollar volume at each point
        rolling_dollar_vol = []
        rolling_returns = []
        
        for i in range(len(bars)):
            ts, close, volume = bars[i]
            dollar_vol = close * volume
            
            # Collect recent 252 days for median dollar volume
            recent_252 = bars[max(0, i-251):i+1]
            if len(recent_252) >= 252:
                dollar_vols = [d * v for _, d, v in recent_252]
                median_dollar_vol = statistics.median(dollar_vols)
                
                # Compute returns for beta
                if i > 0 and ts in spy_returns:
                    close_prev = bars[i-1][1]
                    if close_prev > 0:
                        ret = (close - close_prev) / close_prev
                        rolling_returns.append((ts, ret))
                        symbol_stats[(symbol_id, ts)] = {
                            'median_dollar_vol': median_dollar_vol,
                            'return': ret
                        }
                        continue
        
        # Store symbol stats
        if rolling_returns:
            symbol_stats[symbol_id] = rolling_returns
    
    # Now find VIX spike days and issue calls
    decisions = []  # List of (symbol_id, entry_ts, exit_ts, label, day)
    issued_calls = []  # Track active calls to avoid overlaps
    
    vix_ts_list = sorted(vix_dict.keys())
    
    # Split into train and sealed (last 20%)
    split_idx = int(len(vix_ts_list) * 0.8)
    train_spikes = vix_ts_list[:split_idx]
    sealed_spikes = vix_ts_list[split_idx:]
    
    # Process all spikes together for simplicity, but track era
    all_spikes = vix_ts_list
    
    for spike_idx in range(1, len(all_spikes)):
        spike_ts = all_spikes[spike_idx]
        prev_ts = all_spikes[spike_idx-1]
        
        # Check VIX spike condition
        if prev_ts not in vix_dict or spike_ts not in vix_dict:
            continue
        vix_prev = vix_dict[prev_ts]
        vix_curr = vix_dict[spike_ts]
        if vix_prev <= 0:
            continue
        if (vix_curr - vix_prev) / vix_prev < 0.30:
            continue
        
        # Get all symbols eligible on this spike day
        eligible_symbols = []
        
        for symbol_id, bars in symbol_bars.items():
            # Find bar at or before spike_ts
            bar_at_spike = None
            bar_idx = None
            for i, (ts, close, volume) in enumerate(bars):
                if ts <= spike_ts:
                    bar_at_spike = (ts, close, volume)
                    bar_idx = i
                else:
                    break
            
            if bar_at_spike is None or bar_idx is None:
                continue
            
            # Check at least 252 prior trading days
            if bar_idx < 252:
                continue
            
            # Compute trailing 252-day median dollar volume
            recent_252 = bars[bar_idx-251:bar_idx+1]
            if len(recent_252) < 252:
                continue
            dollar_vols = [d * v for _, d, v in recent_252]
            median_dollar_vol = statistics.median(dollar_vols)
            
            if median_dollar_vol <= 5_000_000:
                continue
            
            # Compute beta to SPY over past 252 days
            # Get symbol returns and spy returns for same dates
            symbol_rets = []
            spy_rets = []
            
            # Find the last 252 trading days for this symbol up to spike_ts
            symbol_dates_bars = []
            for i in range(bar_idx-251, bar_idx+1):
                ts, close, volume = bars[i]
                if ts <= spike_ts:
                    symbol_dates_bars.append((ts, close))
            
            if len(symbol_dates_bars) < 252:
                continue
            
            # Get spy returns for matching dates
            spy_ret_dict = {}
            for ts, ret in spy_returns.items():
                if ts <= spike_ts:
                    spy_ret_dict[ts] = ret
            
            # Calculate beta
            n = len(symbol_dates_bars)
            if n < 252:
                continue
            
            # We need returns for both series on same dates
            common_ts = []
            for ts, _ in symbol_dates_bars:
                if ts in spy_ret_dict:
                    common_ts.append(ts)
            
            if len(common_ts) < 252:
                continue
            
            # Get returns for common timestamps
            x_vals = []  # symbol returns
            y_vals = []  # spy returns
            for ts in common_ts:
                # Symbol return at ts (need previous day's close)
                # Find index in symbol_dates_bars
                idx = None
                for i, (t, _) in enumerate(symbol_dates_bars):
                    if t == ts:
                        idx = i
                        break
                if idx is None or idx == 0:
                    continue
                
                prev_ts, prev_close = symbol_dates_bars[idx-1]
                curr_ts, curr_close = symbol_dates_bars[idx]
                if prev_close > 0:
                    symbol_ret = (curr_close - prev_close) / prev_close
                    spy_ret = spy_ret_dict[ts]
                    x_vals.append(symbol_ret)
                    y_vals.append(spy_ret)
            
            if len(x_vals) < 252:
                continue
            
            # Compute beta
            mean_x = sum(x_vals) / len(x_vals)
            mean_y = sum(y_vals) / len(y_vals)
            
            cov = sum((x - mean_x) * (y - mean_y) for x, y in zip(x_vals, y_vals)) / len(x_vals)
            var_y = sum((y - mean_y) ** 2 for y in y_vals) / len(y_vals)
            
            if var_y == 0:
                continue
            
            beta = cov / var_y
            
            if beta >= 1.5:
                eligible_symbols.append(symbol_id)
        
        # Check if fewer than 30 symbols qualify
        if len(eligible_symbols) < 30:
            continue
        
        # Issue calls for eligible symbols
        # Find next open bar for each symbol
        for symbol_id in eligible_symbols:
            # Check if symbol already has an active call (overlapping)
            # Find current bars for this symbol
            if symbol_id not in symbol_bars:
                continue
            bars = symbol_bars[symbol_id]
            
            # Find first bar after spike_ts
            entry_bar = None
            for ts, close, volume in bars:
                if ts > spike_ts:
                    entry_bar = (ts, close)
                    break
            
            if entry_bar is None:
                continue
            
            entry_ts, entry_close = entry_bar
            
            # Check for overlapping calls
            overlap = False
            for issued in issued_calls:
                if issued['symbol_id'] == symbol_id:
                    if issued['entry_ts'] <= entry_ts <= issued['exit_ts']:
                        overlap = True
                        break
            
            if overlap:
                continue
            
            # Find exit bar (5 trading days after entry)
            entry_idx = None
            for i, (ts, close, volume) in enumerate(bars):
                if ts == entry_ts:
                    entry_idx = i
                    break
            
            if entry_idx is None:
                continue
            
            if entry_idx + 5 >= len(bars):
                continue
            
            exit_ts, exit_close = bars[entry_idx + 5]
            
            # Get label from prediction_outcomes
            # horizon=5, we need the outcome
            cursor.execute("""
                SELECT up, fwd_return
                FROM prediction_outcomes
                WHERE symbol_id=? AND horizon=5 AND ts=?
            """, (symbol_id, entry_ts))
            outcome = cursor.fetchone()
            
            if outcome is None:
                continue
            
            up, fwd_return = outcome
            
            # Determine era
            is_sealed = spike_ts in sealed_spikes
            
            decisions.append({
                'symbol_id': symbol_id,
                'entry_ts': entry_ts,
                'exit_ts': exit_ts,
                'up': up,
                'fwd_return': fwd_return,
                'spike_ts': spike_ts,
                'era': 'sealed' if is_sealed else 'train'
            })
            
            issued_calls.append({
                'symbol_id': symbol_id,
                'entry_ts': entry_ts,
                'exit_ts': exit_ts
            })
    
    conn.close()
    
    if not decisions:
        print("INSUFFICIENT=1")
        return
    
    # Compute metrics for train and sealed
    train_decisions = [d for d in decisions if d['era'] == 'train']
    sealed_decisions = [d for d in decisions if d['era'] == 'sealed']
    
    # Overall metrics
    issued = len(decisions)
    hits = sum(1 for d in decisions if d['up'] == 1)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate within issued
    base_rate = sum(1 for d in decisions if d['up'] == 1) / issued if issued > 0 else 0
    
    # Distinct days
    distinct_days = len(set(d['spike_ts'] for d in decisions))
    
    # Design effect: cluster by day
    day_clusters = defaultdict(list)
    for d in decisions:
        day_clusters[d['spike_ts']].append(d)
    
    # Compute design effect (inflation factor)
    # DE = 1 + (average cluster size - 1) * ICC
    # We'll use a simple approximation: variance inflation factor
    cluster_sizes = [len(cluster) for cluster in day_clusters.values()]
    if cluster_sizes:
        mean_cluster_size = sum(cluster_sizes) / len(cluster_sizes)
        # Approximate DE as mean_cluster_size (conservative)
        design_effect = mean_cluster_size
    else:
        design_effect = 1
    
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Sealed metrics
    sealed_issued = len(sealed_decisions)
    sealed_hits = sum(1 for d in sealed_decisions if d['up'] == 1)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")  # Each considered decision point
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
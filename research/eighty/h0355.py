# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 354
# cycle_index: 22
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import collections
import sys

def main():
    try:
        db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=30)
        db.execute("PRAGMA journal_mode=OFF")
        db.execute("PRAGMA query_only=ON")
        
        # Get all daily bars
        bars = db.execute("""
            SELECT symbol_id, ts, open, close 
            FROM bars 
            WHERE tf='1d' 
            ORDER BY symbol_id, ts
        """).fetchall()
        
        if not bars:
            print("INSUFFICIENT=1")
            return
            
        # Group by symbol
        symbol_bars = {}
        for sid, ts, o, c in bars:
            if sid not in symbol_bars:
                symbol_bars[sid] = []
            symbol_bars[sid].append((ts, o, c))
        
        # Filter to symbols with at least 500 bars
        eligible_symbols = [sid for sid, blist in symbol_bars.items() if len(blist) >= 500]
        
        if len(eligible_symbols) < 100:
            print("INSUFFICIENT=1")
            return
        
        # Precompute overnight and intraday returns for each symbol
        symbol_returns = {}
        for sid in eligible_symbols:
            blist = symbol_bars[sid]
            rets = []
            for i in range(1, len(blist)):
                prev_c = blist[i-1][2]
                o = blist[i][1]
                c = blist[i][2]
                if prev_c == 0 or o == 0:
                    continue
                overnight = o/prev_c - 1
                intraday = c/o - 1
                rets.append((blist[i][0], overnight, intraday))
            symbol_returns[sid] = rets
        
        # Get all decision days
        all_days = set()
        for sid, rets in symbol_returns.items():
            for ts, _, _ in rets:
                all_days.add(ts)
        
        days_list = sorted(all_days)
        if not days_list:
            print("INSUFFICIENT=1")
            return
        
        # Hold out last 20% as sealed era
        cutoff_idx = int(len(days_list) * 0.8)
        sealed_start = days_list[cutoff_idx] if cutoff_idx < len(days_list) else None
        
        # Process day by day
        issued_calls = []
        opportunities = 0
        
        for day_idx, day_ts in enumerate(days_list):
            # For each symbol, get trailing data up to this day
            symbol_data = {}
            for sid in eligible_symbols:
                rets = symbol_returns[sid]
                # Filter returns with ts <= day_ts
                filtered = [r for r in rets if r[0] <= day_ts]
                if len(filtered) < 256:  # Need at least 252 for distribution plus 5 for trailing
                    continue
                symbol_data[sid] = filtered
            
            if len(symbol_data) < 100:
                continue
            
            opportunities += len(symbol_data)
            
            # For each eligible symbol, compute trailing 5-day cumulative returns and deciles
            for sid, rets in symbol_data.items():
                # Get last 256 returns to compute 252-day distribution of 5-day cum
                recent = rets[-256:]
                if len(recent) < 256:
                    continue
                
                # Compute trailing 5-day cumulative returns for last 252 days
                cum_overnight_dist = []
                cum_intraday_dist = []
                for j in range(len(recent)-4):
                    # Compute cumulative overnight for window [j:j+5]
                    prod_o = 1.0
                    prod_i = 1.0
                    for k in range(5):
                        prod_o *= (1 + recent[j+k][1])
                        prod_i *= (1 + recent[j+k][2])
                    cum_overnight_dist.append(prod_o - 1)
                    cum_intraday_dist.append(prod_i - 1)
                
                if not cum_overnight_dist or not cum_intraday_dist:
                    continue
                
                # Compute current 5-day cumulative returns (last 5)
                prod_o_cur = 1.0
                prod_i_cur = 1.0
                for k in range(5):
                    prod_o_cur *= (1 + recent[-(5-k)][1])
                    prod_i_cur *= (1 + recent[-(5-k)][2])
                cum_o_cur = prod_o_cur - 1
                cum_i_cur = prod_i_cur - 1
                
                # Compute deciles from distribution (252 values)
                if len(cum_overnight_dist) < 10 or len(cum_intraday_dist) < 10:
                    continue
                
                # Check range (non-zero)
                if max(cum_overnight_dist) == min(cum_overnight_dist) or max(cum_intraday_dist) == min(cum_intraday_dist):
                    continue
                
                # Compute 10th and 90th percentiles
                def percentile(data, p):
                    sorted_d = sorted(data)
                    idx = p/100 * (len(sorted_d) - 1)
                    lo = int(idx)
                    hi = lo + 1
                    if hi >= len(sorted_d):
                        return sorted_d[-1]
                    frac = idx - lo
                    return sorted_d[lo] + frac*(sorted_d[hi] - sorted_d[lo])
                
                overnight_10 = percentile(cum_overnight_dist, 10)
                overnight_90 = percentile(cum_overnight_dist, 90)
                intraday_10 = percentile(cum_intraday_dist, 10)
                intraday_90 = percentile(cum_intraday_dist, 90)
                
                # Issue call based on deciles
                if cum_o_cur >= overnight_90 and cum_i_cur <= intraday_10:
                    call = 'UP'
                elif cum_o_cur <= overnight_10 and cum_i_cur >= intraday_90:
                    call = 'DOWN'
                else:
                    continue
                
                # Get forward return for 5 trading days from prediction_outcomes
                # We need to find the close 5 bars after this day
                # First, get all bars for this symbol after day_ts
                all_bars = symbol_bars[sid]
                future_bars = [(ts, o, c) for ts, o, c in all_bars if ts > day_ts]
                
                # Need at least 5 future bars to measure 5-day return
                if len(future_bars) < 5:
                    continue
                
                # Get close at decision day (current close)
                # Find current bar index
                current_bar_idx = None
                for idx, (ts, o, c) in enumerate(all_bars):
                    if ts == day_ts:
                        current_bar_idx = idx
                        break
                
                if current_bar_idx is None:
                    continue
                
                current_close = all_bars[current_bar_idx][2]
                
                # Get close 5 bars ahead
                if current_bar_idx + 5 < len(all_bars):
                    future_close = all_bars[current_bar_idx + 5][2]
                else:
                    continue
                
                # Calculate forward return and direction
                fwd_return = future_close/current_close - 1
                up = 1 if fwd_return > 0 else 0
                
                # Store the call
                issued_calls.append({
                    'sid': sid,
                    'day_ts': day_ts,
                    'call': call,
                    'up': up,
                    'fwd_return': fwd_return,
                    'sealed': day_ts >= sealed_start if sealed_start else False
                })
        
        # Check if we have enough calls
        if len(issued_calls) < 100:
            print("INSUFFICIENT=1")
            return
        
        # Calculate metrics
        total_issued = len(issued_calls)
        hits = sum(1 for c in issued_calls if (c['call'] == 'UP' and c['up'] == 1) or 
                  (c['call'] == 'DOWN' and c['up'] == 0))
        
        precision = hits / total_issued
        
        # Base rate within issued calls
        up_calls = sum(1 for c in issued_calls if c['call'] == 'UP')
        base_rate = up_calls / total_issued
        
        # Distinct days in issued calls
        issued_days = set(c['day_ts'] for c in issued_calls)
        distinct_days = len(issued_days)
        
        # Calculate design effect for effective sample size
        # Group calls by day
        day_counts = collections.Counter(c['day_ts'] for c in issued_calls)
        n_days = len(day_counts)
        n_calls = total_issued
        
        # Calculate within-day variance of call correctness
        day_correct_rates = []
        for day, count in day_counts.items():
            day_calls = [c for c in issued_calls if c['day_ts'] == day]
            correct = sum(1 for c in day_calls if (c['call'] == 'UP' and c['up'] == 1) or 
                         (c['call'] == 'DOWN' and c['up'] == 0))
            day_correct_rates.append(correct / count if count > 0 else 0)
        
        # Overall correct rate
        p = hits / n_calls
        
        # Between-day variance
        if n_days > 1:
            mean_rate = sum(day_correct_rates) / n_days
            between_var = sum((rate - mean_rate) ** 2 for rate in day_correct_rates) / (n_days - 1)
        else:
            between_var = 0
        
        # Within-day variance (average)
        within_var_sum = 0
        for day, count in day_counts.items():
            day_calls = [c for c in issued_calls if c['day_ts'] == day]
            correct = sum(1 for c in day_calls if (c['call'] == 'UP' and c['up'] == 1) or 
                         (c['call'] == 'DOWN' and c['up'] == 0))
            p_day = correct / count if count > 0 else 0
            within_var_sum += count * p_day * (1 - p_day)
        
        within_var = within_var_sum / n_calls if n_calls > 0 else 0
        
        # ICC
        if p > 0 and p < 1:
            total_var = p * (1 - p)
            if total_var > 0 and between_var > 0:
                icc = between_var / total_var
            else:
                icc = 0
        else:
            icc = 0
        
        # Average cluster size
        m = n_calls / n_days if n_days > 0 else 0
        
        # Design effect
        deff = 1 + (m - 1) * icc if n_days > 0 else 1
        
        # Effective sample size
        effective_n = n_calls / deff if deff > 0 else n_calls
        
        # Sealed era metrics
        sealed_calls = [c for c in issued_calls if c['sealed']]
        sealed_issued = len(sealed_calls)
        sealed_hits = sum(1 for c in sealed_calls if (c['call'] == 'UP' and c['up'] == 1) or 
                        (c['call'] == 'DOWN' and c['up'] == 0))
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print required lines
        print(f"ISSUED={total_issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n}")
        print(f"SEALED_PRECISION={sealed_precision}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()
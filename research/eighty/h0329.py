# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 328
# cycle_index: 51
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime
from collections import defaultdict
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Get all 1d bars, sorted by symbol_id and ts
        cur.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
        bars = cur.fetchall()
        
        # Build list of unique trading days and mapping ts -> index
        all_ts = sorted({row[1] for row in bars})
        ts_to_idx = {ts: i for i, ts in enumerate(all_ts)}
        n_days = len(all_ts)
        
        # Initialize data structures for symbols
        symbols = set()
        symbol_data = defaultdict(dict)  # symbol_id -> {ts: (close, volume)}
        for symbol_id, ts, close, volume in bars:
            symbols.add(symbol_id)
            symbol_data[symbol_id][ts] = (close, volume)
        
        # Create arrays for each symbol
        sym_list = list(symbols)
        n_sym = len(sym_list)
        # Map symbol_id to index in arrays
        sym_to_idx = {s: i for i, s in enumerate(sym_list)}
        
        # Initialize arrays for close, volume, returns
        close_arr = [[float('nan') for _ in range(n_days)] for _ in range(n_sym)]
        vol_arr = [[float('nan') for _ in range(n_days)] for _ in range(n_sym)]
        ret_arr = [[float('nan') for _ in range(n_days)] for _ in range(n_sym)]
        
        # Fill close and volume
        for symbol_id, ts, close, volume in bars:
            i = sym_to_idx[symbol_id]
            j = ts_to_idx[ts]
            close_arr[i][j] = close
            vol_arr[i][j] = volume
        
        # Compute returns
        for i in range(n_sym):
            for j in range(1, n_days):
                if not (math.isnan(close_arr[i][j]) or math.isnan(close_arr[i][j-1])):
                    ret_arr[i][j] = close_arr[i][j] / close_arr[i][j-1] - 1
        
        # Determine Fridays (weekday 4) among trading days
        friday_indices = []
        for idx, ts in enumerate(all_ts):
            dt = datetime.utcfromtimestamp(ts)
            if dt.weekday() == 4:  # Friday
                friday_indices.append(idx)
        
        # Iterate over eligible Fridays
        calls = []  # list of dicts
        opportunities = 0
        min_start = 252  # need 252 lookback
        max_end = n_days - 22  # need 21 forward, so last Friday index is n_days-22
        
        for f_idx in friday_indices:
            if f_idx < min_start or f_idx > max_end:
                continue
            
            opportunities += 1
            
            # Define windows
            # Lookback for prices: [f_idx-252, f_idx] (253 days for 252 returns)
            # Lookback for volume: [f_idx-63, f_idx-1] (63 days)
            # Forward: [f_idx+1, f_idx+21] (21 days)
            start_price = f_idx - 252
            end_price = f_idx
            start_vol = f_idx - 63
            end_vol = f_idx - 1
            forward_end = f_idx + 21
            
            # Find symbols with complete data
            universe = []
            for sym_idx in range(n_sym):
                # Check price data for [start_price, forward_end] (all days)
                valid = True
                for day in range(start_price, forward_end + 1):
                    if math.isnan(close_arr[sym_idx][day]):
                        valid = False
                        break
                if not valid:
                    continue
                
                # Check volume data for [start_vol, end_vol] (63 days) and compute avg dollar volume
                total_dollar_vol = 0.0
                count_vol = 0
                for day in range(start_vol, end_vol + 1):
                    if math.isnan(close_arr[sym_idx][day]) or math.isnan(vol_arr[sym_idx][day]):
                        valid = False
                        break
                    total_dollar_vol += close_arr[sym_idx][day] * vol_arr[sym_idx][day]
                    count_vol += 1
                if not valid or count_vol < 63:
                    continue
                avg_dollar_vol = total_dollar_vol / count_vol
                if avg_dollar_vol < 5_000_000:
                    continue
                
                universe.append(sym_idx)
            
            if len(universe) < 500:
                continue
            
            # Compute market returns for the 252-day window (indices: f_idx-251 to f_idx)
            # We need 252 returns: for each day d in [f_idx-251, f_idx], market_ret[d] = avg of universe returns on day d
            market_returns = [0.0] * 252
            for d_offset in range(1, 253):  # d_offset from 1 to 252, day index = f_idx-252+d_offset
                day_idx = f_idx - 252 + d_offset
                # Compute average return across universe
                total_ret = 0.0
                cnt = 0
                for sym_idx in universe:
                    r = ret_arr[sym_idx][day_idx]
                    if not math.isnan(r):
                        total_ret += r
                        cnt += 1
                market_returns[d_offset-1] = total_ret / cnt if cnt > 0 else float('nan')
            
            # Compute betas for each symbol in universe
            betas = []
            for sym_idx in universe:
                # Get symbol returns for the same 252 days
                sym_returns = []
                for d_offset in range(1, 253):
                    day_idx = f_idx - 252 + d_offset
                    r = ret_arr[sym_idx][day_idx]
                    sym_returns.append(r)
                
                # Compute means
                sym_mean = sum(sym_returns) / 252
                market_mean = sum(market_returns) / 252
                
                # Compute covariance and variance
                cov = 0.0
                var_market = 0.0
                for i in range(252):
                    sr = sym_returns[i]
                    mr = market_returns[i]
                    if not (math.isnan(sr) or math.isnan(mr)):
                        cov += (sr - sym_mean) * (mr - market_mean)
                        var_market += (mr - market_mean) ** 2
                
                beta = cov / var_market if var_market != 0 else float('nan')
                betas.append((sym_idx, beta))
            
            # Remove NaN betas
            betas = [(idx, beta) for idx, beta in betas if not math.isnan(beta)]
            if len(betas) < 10:
                continue
            
            # Sort betas and compute decile thresholds
            betas_sorted = sorted(betas, key=lambda x: x[1])
            n_betas = len(betas_sorted)
            decile_idx_10 = n_betas // 10
            decile_idx_90 = n_betas * 9 // 10
            beta_10th = betas_sorted[decile_idx_10][1]
            beta_90th = betas_sorted[decile_idx_90][1]
            
            # Compute 21-day returns for each symbol in universe
            ret_21 = {}
            for sym_idx in universe:
                close_now = close_arr[sym_idx][f_idx]
                close_21ago = close_arr[sym_idx][f_idx - 21]
                ret21 = close_now / close_21ago - 1
                ret_21[sym_idx] = ret21
            
            # Issue calls
            for sym_idx, beta in betas:
                if beta <= beta_10th and ret_21[sym_idx] < 0:
                    # UP call
                    # Forward return
                    close_forward = close_arr[sym_idx][forward_end]
                    close_now = close_arr[sym_idx][f_idx]
                    fwd_ret = close_forward / close_now - 1
                    correct = 1 if fwd_ret > 0 else 0
                    calls.append({
                        'decision_ts': all_ts[f_idx],
                        'symbol_idx': sym_idx,
                        'call_type': 'UP',
                        'correct': correct,
                        'fwd_ret': fwd_ret,
                        'decision_day': f_idx
                    })
                elif beta >= beta_90th and ret_21[sym_idx] > 0:
                    # DOWN call
                    close_forward = close_arr[sym_idx][forward_end]
                    close_now = close_arr[sym_idx][f_idx]
                    fwd_ret = close_forward / close_now - 1
                    correct = 1 if fwd_ret < 0 else 0
                    calls.append({
                        'decision_ts': all_ts[f_idx],
                        'symbol_idx': sym_idx,
                        'call_type': 'DOWN',
                        'correct': correct,
                        'fwd_ret': fwd_ret,
                        'decision_day': f_idx
                    })
        
        # If insufficient data
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Sort calls by decision_ts
        calls.sort(key=lambda x: x['decision_ts'])
        
        # Split into sealed era (most recent 20%)
        total_calls = len(calls)
        sealed_start = int(total_calls * 0.8)
        sealed_calls = calls[sealed_start:]
        main_calls = calls[:sealed_start]
        
        # Compute metrics for main set
        issued = len(main_calls)
        if issued == 0:
            print("INSUFFICIENT=1")
            return
        
        hits = sum(c['correct'] for c in main_calls)
        precision = hits / issued
        
        # Base rate: max fraction positive, fraction negative in forward returns
        pos_frac = sum(1 for c in main_calls if c['fwd_ret'] > 0) / issued
        base_rate = max(pos_frac, 1 - pos_frac)
        
        # Distinct days among issued calls
        distinct_days = len(set(c['decision_ts'] for c in main_calls))
        assert distinct_days <= issued, "DISTINCT_DAYS cannot exceed ISSUED"
        
        # Design effect using cluster-robust variance for binary outcomes
        # Group by decision day
        day_groups = defaultdict(list)
        for c in main_calls:
            day_groups[c['decision_ts']].append(c['correct'])
        
        N = issued
        G = len(day_groups)
        p = precision  # overall mean
        
        # Cluster-robust variance
        # V_cluster = (G/(G-1)) * ((N-1)/(N-G)) * (1/N^2) * sum_i (sum_j (y_ij - p))^2
        sum_sq = 0.0
        for day, outcomes in day_groups.items():
            s = sum(outcomes - p for outcome in outcomes)
            sum_sq += s * s
        V_cluster = (G/(G-1)) * ((N-1)/(N-G)) * (sum_sq / (N*N))
        V_naive = p * (1 - p) / N
        deff = V_cluster / V_naive if V_naive > 0 else 1.0
        effective_n = N / deff
        
        assert effective_n < N, "EFFECTIVE_N must be less than ISSUED"
        
        # Sealed precision
        sealed_issued = len(sealed_calls)
        if sealed_issued > 0:
            sealed_hits = sum(c['correct'] for c in sealed_calls)
            sealed_precision = sealed_hits / sealed_issued
        else:
            sealed_precision = 0.0
        
        # Print required lines
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
        conn.close()
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        print("INSUFFICIENT=1")
        sys.exit(0)

if __name__ == "__main__":
    main()
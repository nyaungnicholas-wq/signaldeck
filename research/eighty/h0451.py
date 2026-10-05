# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 450
# cycle_index: 41
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Load daily bars
        cur.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
        bars = cur.fetchall()
        if not bars:
            print("INSUFFICIENT=1")
            return
        
        # Organize bars by symbol
        symbols = {}
        for sym, ts, close in bars:
            if sym not in symbols:
                symbols[sym] = []
            symbols[sym].append((ts, close))
        
        # Get all unique days sorted
        all_days = sorted({ts for _, ts, _ in bars})
        day_index = {day: i for i, day in enumerate(all_days)}
        
        # Precompute 50-day MA for each symbol at each day
        ma50 = {}
        for sym, day_bars in symbols.items():
            closes = [close for _, close in day_bars]
            ma = []
            for i in range(len(closes)):
                if i >= 49:
                    ma.append(sum(closes[i-49:i+1]) / 50.0)
                else:
                    ma.append(None)
            ma50[sym] = dict(zip([ts for ts, _ in day_bars], ma))
        
        # Determine eligible symbols per day (>=252 days history)
        eligible = {}
        for day in all_days:
            eligible[day] = []
            for sym, day_bars in symbols.items():
                ts_list = [ts for ts, _ in day_bars]
                if ts_list and ts_list[-1] <= day:
                    count = sum(1 for ts in ts_list if ts <= day)
                    if count >= 252:
                        eligible[day].append(sym)
        
        # Precompute breadth for each day
        breadth = {}
        for day in all_days:
            if day not in eligible or not eligible[day]:
                breadth[day] = None
                continue
            count_above = 0
            total = 0
            for sym in eligible[day]:
                # Get close at this day for symbol
                sym_bars = symbols[sym]
                close_at_day = None
                for ts, close in sym_bars:
                    if ts == day:
                        close_at_day = close
                        break
                if close_at_day is None:
                    continue
                # Get MA50 at this day
                ma_val = ma50[sym].get(day)
                if ma_val is not None and close_at_day > ma_val:
                    count_above += 1
                total += 1
            breadth[day] = count_above / total if total > 0 else None
        
        # Load prediction_outcomes for horizon=21
        cur.execute("SELECT symbol_id, ts, up, fwd_return FROM prediction_outcomes WHERE horizon=21")
        outcomes = {}
        for sym, ts, up, fwd in cur.fetchall():
            outcomes[(sym, ts)] = (up, fwd)
        
        # Main simulation
        calls = []  # (day, symbol)
        decisions = 0
        
        for t in all_days:
            # Need breadth at t-20 and t-1
            if t not in day_index:
                continue
            idx = day_index[t]
            if idx < 20:
                continue
            
            t_minus_20 = all_days[idx - 20]
            t_minus_1 = all_days[idx - 1]
            
            # Check market breadth condition
            b1 = breadth.get(t_minus_20)
            b2 = breadth.get(t_minus_1)
            if b1 is None or b2 is None:
                continue
            if (b1 - b2) < 0.10:
                continue
            
            # Get eligible symbols at t
            eligible_syms = eligible.get(t, [])
            if not eligible_syms:
                continue
            
            # For each symbol
            for sym in eligible_syms:
                decisions += 1
                
                # Check if symbol had a call in prior 21 trading days
                recent_call = False
                for prev_day, prev_sym in calls:
                    if prev_sym == sym:
                        prev_idx = day_index.get(prev_day)
                        if prev_idx is not None and (idx - prev_idx) < 21:
                            recent_call = True
                            break
                if recent_call:
                    continue
                
                # Get bars for symbol up to t-1
                sym_bars = symbols[sym]
                ts_close = {ts: close for ts, close in sym_bars}
                
                # Check if close at t-1 exists
                if t_minus_1 not in ts_close:
                    continue
                close_t_minus_1 = ts_close[t_minus_1]
                
                # Check 50-day MA at t-1
                ma_val = ma50[sym].get(t_minus_1)
                if ma_val is None or close_t_minus_1 <= ma_val:
                    continue
                
                # Compute return over t-20..t-1
                if t_minus_20 not in ts_close:
                    continue
                close_t_minus_20 = ts_close[t_minus_20]
                ret = close_t_minus_1 / close_t_minus_20 - 1
                
                # Get all returns at t-1 for eligible symbols
                returns_at_t1 = []
                for s in eligible_syms:
                    s_bars = symbols[s]
                    s_close_t1 = None
                    s_close_t20 = None
                    for ts, close in s_bars:
                        if ts == t_minus_1:
                            s_close_t1 = close
                        if ts == t_minus_20:
                            s_close_t20 = close
                    if s_close_t1 is not None and s_close_t20 is not None:
                        s_ret = s_close_t1 / s_close_t20 - 1
                        returns_at_t1.append(s_ret)
                
                if not returns_at_t1:
                    continue
                
                # Find top quintile threshold
                returns_at_t1.sort()
                cutoff = returns_at_t1[int(len(returns_at_t1) * 0.8)]
                if ret < cutoff:
                    continue
                
                # All conditions met, issue down call
                calls.append((t, sym))
        
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Evaluate calls
        # Split into train and sealed (last 20%)
        call_days = sorted({day for day, _ in calls})
        split_idx = int(len(call_days) * 0.8)
        sealed_days = set(call_days[split_idx:])
        
        issued = 0
        hits = 0
        base_down = 0
        distinct_days_train = set()
        day_counts = {}
        
        sealed_issued = 0
        sealed_hits = 0
        
        for day, sym in calls:
            # Get outcome
            outcome = outcomes.get((sym, day))
            if outcome is None:
                continue
            
            up, fwd = outcome
            is_down = 1 - up  # down is predicted
            
            issued += 1
            base_down += is_down
            
            if day in sealed_days:
                sealed_issued += 1
                sealed_hits += is_down
            else:
                hits += is_down
                distinct_days_train.add(day)
                day_counts[day] = day_counts.get(day, 0) + 1
        
        if issued == 0:
            print("INSUFFICIENT=1")
            return
        
        # Compute design effect for clustering by day
        total_mean = base_down / issued
        between_var = 0
        within_var = 0
        
        for day, cnt in day_counts.items():
            day_hits = 0
            for d, s in calls:
                if d == day:
                    out = outcomes.get((s, day))
                    if out:
                        _, up = out
                        day_hits += (1 - up)
            day_mean = day_hits / cnt if cnt > 0 else 0
            between_var += cnt * (day_mean - total_mean) ** 2
            for d, s in calls:
                if d == day:
                    out = outcomes.get((s, day))
                    if out:
                        _, up = out
                        day_val = 1 - up
                        within_var += (day_val - day_mean) ** 2
        
        n_days = len(day_counts)
        if n_days > 1:
            between_var /= (n_days - 1)
        if issued > n_days:
            within_var /= (issued - n_days)
        
        variance_total = between_var + within_var
        if variance_total > 0:
            icc = between_var / variance_total
        else:
            icc = 0
        
        m = issued / n_days if n_days > 0 else 1
        design_effect = 1 + (m - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Output results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={decisions}")
        print(f"PRECISION={hits / issued if issued > 0 else 0:.6f}")
        print(f"BASE_RATE={base_down / issued if issued > 0 else 0:.6f}")
        print(f"DISTINCT_DAYS={len(distinct_days_train)}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        if sealed_issued > 0:
            print(f"SEALED_PRECISION={sealed_hits / sealed_issued:.6f}")
        else:
            print("SEALED_PRECISION=0.000000")
        
    except Exception as e:
        print(f"ERROR: {e}")
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
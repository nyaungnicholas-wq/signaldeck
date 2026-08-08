# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 291
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get all symbols with at least 260 daily bars
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM bars WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING cnt >= 260
    """)
    eligible_symbols = {row[0] for row in cur.fetchall()}
    
    if not eligible_symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get all daily bars for eligible symbols
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join(str(s) for s in eligible_symbols)))
    
    # Organize data by symbol
    symbol_data = {}
    for symbol_id, ts, close, volume in cur.fetchall():
        if symbol_id not in symbol_data:
            symbol_data[symbol_id] = []
        symbol_data[symbol_id].append((ts, close, volume))
    
    # Prepare tracking structures
    issued_calls = []  # (symbol_id, decision_ts, call_end_ts)
    open_calls = {}  # symbol_id -> end_ts
    
    # Process each symbol
    for symbol_id, data in symbol_data.items():
        n = len(data)
        if n < 260:
            continue
        
        # For each potential decision point
        for i in range(259, n - 5):  # Need at least 5 more days for horizon
            current_ts, current_close, current_vol = data[i]
            
            # Check price constraint
            if not (1 <= current_close <= 5):
                continue
            
            # Calculate average dollar volume over prior 60 days (excluding current)
            if i < 60:
                continue
            total_dollar_vol = 0
            for j in range(i - 60, i):
                _, close_j, vol_j = data[j]
                total_dollar_vol += close_j * vol_j
            avg_dollar_vol = total_dollar_vol / 60
            
            if avg_dollar_vol < 1_000_000:
                continue
            
            # Calculate 20-day trailing return
            if i < 20:
                continue
            close_20d_ago = data[i - 20][1]
            if close_20d_ago == 0:
                continue
            trailing_20d_return = (current_close - close_20d_ago) / close_20d_ago
            
            if trailing_20d_return < 0.20:
                continue
            
            # Check open calls
            if symbol_id in open_calls and current_ts <= open_calls[symbol_id]:
                continue
            
            # Calculate 60-day realized volatility
            returns = []
            for j in range(i - 59, i + 1):
                close_j = data[j][1]
                close_prev = data[j - 1][1]
                if close_prev != 0:
                    returns.append((close_j - close_prev) / close_prev)
            
            if len(returns) < 60:
                continue
            
            mean_return = sum(returns) / len(returns)
            variance = sum((r - mean_return) ** 2 for r in returns) / (len(returns) - 1)
            volatility_60d = math.sqrt(variance)
            
            # Store candidate
            if not hasattr(main, 'candidates'):
                main.candidates = []
            main.candidates.append((symbol_id, current_ts, current_close, volatility_60d))
    
    if not hasattr(main, 'candidates') or not main.candidates:
        print("INSUFFICIENT=1")
        return
    
    # Sort by time
    main.candidates.sort(key=lambda x: x[1])
    
    # Group by day
    days = {}
    for symbol_id, ts, close, vol in main.candidates:
        if ts not in days:
            days[ts] = []
        days[ts].append((symbol_id, close, vol))
    
    # Process each day
    issued_calls = []
    for ts in sorted(days.keys()):
        day_candidates = days[ts]
        
        # Calculate top quintile threshold
        volatilities = [vol for _, _, vol in day_candidates]
        volatilities.sort()
        quintile_index = int(len(volatilities) * 0.8)
        if quintile_index >= len(volatilities):
            quintile_index = len(volatilities) - 1
        threshold = volatilities[quintile_index]
        
        # Issue calls
        for symbol_id, close, vol in day_candidates:
            if vol >= threshold:
                # Find the 5th trading day after decision
                cur.execute("""
                    SELECT ts FROM bars
                    WHERE symbol_id = ? AND tf = '1d' AND ts > ?
                    ORDER BY ts
                    LIMIT 5
                """, (symbol_id, ts))
                future_days = [row[0] for row in cur.fetchall()]
                if len(future_days) < 5:
                    continue
                
                horizon_end_ts = future_days[-1]
                issued_calls.append((symbol_id, ts, horizon_end_ts))
                open_calls[symbol_id] = horizon_end_ts
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Calculate outcomes
    hits = 0
    hits_sealed = 0
    issued_count = len(issued_calls)
    issued_sealed = 0
    
    # Determine 80/20 split
    all_ts = sorted(set(ts for _, ts, _ in issued_calls))
    split_index = int(len(all_ts) * 0.8)
    sealed_start = all_ts[split_index] if split_index < len(all_ts) else all_ts[-1]
    
    for symbol_id, decision_ts, horizon_end_ts in issued_calls:
        # Get price at decision
        cur.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts = ?
        """, (symbol_id, decision_ts))
        row = cur.fetchone()
        if not row:
            continue
        decision_price = row[0]
        
        # Get price at horizon end
        cur.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts = ?
        """, (symbol_id, horizon_end_ts))
        row = cur.fetchone()
        if not row:
            continue
        horizon_price = row[0]
        
        # SHORT call is a hit if price went down
        if horizon_price < decision_price:
            hits += 1
            if decision_ts >= sealed_start:
                hits_sealed += 1
        
        if decision_ts >= sealed_start:
            issued_sealed += 1
    
    # Calculate base rate (proportion of down outcomes in issued calls)
    base_rate = hits / issued_count if issued_count > 0 else 0
    precision = hits / issued_count if issued_count > 0 else 0
    sealed_precision = hits_sealed / issued_sealed if issued_sealed > 0 else 0
    
    # Calculate distinct days and design effect
    distinct_days = len(set(ts for _, ts, _ in issued_calls))
    
    # Group calls by day for clustering
    calls_by_day = {}
    for symbol_id, ts, _ in issued_calls:
        if ts not in calls_by_day:
            calls_by_day[ts] = []
        calls_by_day[ts].append(symbol_id)
    
    # Calculate design effect using one-way ANOVA for binary outcomes
    D = len(calls_by_day)
    if D > 1:
        # Get daily hit rates
        daily_hits = []
        daily_counts = []
        for day_ts, day_symbols in calls_by_day.items():
            day_hit_count = 0
            for symbol_id in day_symbols:
                # Find if this symbol had a hit
                for s_id, d_ts, h_ts in issued_calls:
                    if s_id == symbol_id and d_ts == day_ts:
                        # Check hit
                        cur.execute("""
                            SELECT close FROM bars WHERE symbol_id = ? AND tf = '1d' AND ts = ?
                        """, (symbol_id, d_ts))
                        d_row = cur.fetchone()
                        cur.execute("""
                            SELECT close FROM bars WHERE symbol_id = ? AND tf = '1d' AND ts = ?
                        """, (symbol_id, h_ts))
                        h_row = cur.fetchone()
                        if d_row and h_row:
                            if h_row[0] < d_row[0]:
                                day_hit_count += 1
                        break
            daily_hits.append(day_hit_count)
            daily_counts.append(len(day_symbols))
        
        p = precision
        # Overall variance of binary outcomes
        total_var = p * (1 - p) if p > 0 and p < 1 else 0.001
        
        # Weighted variance of daily means
        weighted_sq_diff = 0
        total_weight = 0
        for i, (day_hits, day_count) in enumerate(zip(daily_hits, daily_counts)):
            p_i = day_hits / day_count if day_count > 0 else p
            weighted_sq_diff += day_count * (p_i - p) ** 2
            total_weight += day_count
        
        var_between = weighted_sq_diff / total_weight if total_weight > 0 else 0
        
        # Intra-class correlation
        icc = var_between / total_var if total_var > 0 else 0
        avg_cluster_size = sum(daily_counts) / D if D > 0 else 1
        
        # Design effect
        deff = 1 + (avg_cluster_size - 1) * icc
    else:
        deff = 1.0
    
    effective_n = issued_count / deff if deff > 0 else issued_count
    
    # Print results
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={len(main.candidates)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
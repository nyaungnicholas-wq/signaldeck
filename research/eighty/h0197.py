#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict

def main():
    try:
        db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        db.row_factory = sqlite3.Row
    except Exception:
        print("INSUFFICIENT=1")
        return

    # Get all symbols with daily bars and 13F data
    symbol_rows = db.execute("""
        SELECT id, symbol FROM symbols
        WHERE id IN (SELECT DISTINCT symbol_id FROM bars WHERE tf='1d')
        AND id IN (SELECT DISTINCT symbol_id FROM inst_holdings)
    """).fetchall()
    symbols = {r['id']: r['symbol'] for r in symbol_rows}

    # Get all quarters with 13F data
    quarter_data = db.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        WHERE symbol_id IN ({})
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """.format(','.join('?'*len(symbols))), list(symbols.keys())).fetchall()

    # Group by symbol and organize quarters chronologically
    symbol_quarters = defaultdict(list)
    for row in quarter_data:
        symbol_quarters[row['symbol_id']].append((row['period'], row['total_shares']))

    # Get all daily bars for relevant symbols
    all_bars = db.execute("""
        SELECT symbol_id, ts, close, volume FROM bars
        WHERE tf='1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(symbols))), list(symbols.keys())).fetchall()

    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for row in all_bars:
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'close': row['close'],
            'volume': row['volume']
        })

    # Get unique timestamps to find T days
    all_ts = sorted(set(row['ts'] for row in all_bars))
    ts_to_idx = {ts: i for i, ts in enumerate(all_ts)}

    opportunities = []  # (symbol_id, T_ts, is_up, base_epoch)
    
    # For each symbol with at least 2 quarters
    for sym_id, quarters in symbol_quarters.items():
        if len(quarters) < 2:
            continue
        
        sym_bars = bars_by_symbol.get(sym_id, [])
        if len(sym_bars) < 252:
            continue
        
        # Create lookup for this symbol's bars
        bar_lookup = {bar['ts']: bar for bar in sym_bars}
        bar_ts_list = [bar['ts'] for bar in sym_bars]
        
        # For each consecutive quarter pair
        for i in range(1, len(quarters)):
            prev_period, prev_shares = quarters[i-1]
            curr_period, curr_shares = quarters[i]
            
            if prev_shares == 0:
                continue
            
            # Check if shares increased by at least 20%
            if curr_shares < prev_shares * 1.2:
                continue
            
            # Find quarter end as timestamp
            try:
                period_ts = int(sqlite3.connect(':memory:').execute(
                    "SELECT strftime('%s', ?)", [curr_period]).fetchone()[0])
            except Exception:
                continue
            
            # Find first trading day at least 45 days after quarter end
            min_T_ts = period_ts + 45*24*3600
            T_ts = None
            for ts in bar_ts_list:
                if ts >= min_T_ts:
                    T_ts = ts
                    break
            
            if T_ts is None:
                continue
            
            # Check we have enough bars before T
            if T_ts not in bar_lookup:
                continue
            
            # Count bars before T
            idx = bar_ts_list.index(T_ts)
            if idx < 251:  # Need at least 252 sessions before T
                continue
            
            # Check price >= $5
            if bar_lookup[T_ts]['close'] < 5.0:
                continue
            
            # Check ADTV >= $10M over prior 60 sessions
            if idx < 59:
                continue
            
            adtv = sum(bar_lookup[bar_ts_list[j]]['close'] * bar_lookup[bar_ts_list[j]]['volume'] 
                      for j in range(idx-59, idx+1)) / 60.0
            if adtv < 10_000_000:
                continue
            
            # Get prior 60 sessions for volume median
            vol_60 = [bar_lookup[bar_ts_list[j]]['volume'] 
                     for j in range(idx-59, idx+1)]
            vol_60_sorted = sorted(vol_60)
            vol_median = vol_60_sorted[29] if len(vol_60) > 29 else vol_60_sorted[-1]
            
            # Check volume at T >= median
            if bar_lookup[T_ts]['volume'] < vol_median:
                continue
            
            # Check close within 2% of T-1 close
            if idx < 1:
                continue
            T_minus_1_close = bar_lookup[bar_ts_list[idx-1]]['close']
            T_close = bar_lookup[T_ts]['close']
            if abs(T_close - T_minus_1_close) / T_minus_1_close > 0.02:
                continue
            
            # Check 20-session SMA: close below SMA
            if idx < 19:
                continue
            sma_20 = sum(bar_lookup[bar_ts_list[j]]['close'] 
                       for j in range(idx-19, idx+1)) / 20.0
            if T_close >= sma_20:
                continue
            
            # Check trailing 20-session gain > 30%
            if idx < 20:
                continue
            price_20_ago = bar_lookup[bar_ts_list[idx-20]]['close']
            if price_20_ago > 0:
                gain = (T_close - price_20_ago) / price_20_ago
                if gain > 0.30:
                    continue
            
            # Calculate 20-session volatility
            returns = []
            for j in range(idx-19, idx+1):
                curr_close = bar_lookup[bar_ts_list[j]]['close']
                prev_close = bar_lookup[bar_ts_list[j-1]]['close']
                if prev_close > 0:
                    returns.append(math.log(curr_close / prev_close))
            
            if len(returns) > 1:
                mean_ret = sum(returns) / len(returns)
                variance = sum((r - mean_ret) ** 2 for r in returns) / (len(returns) - 1)
                vol = math.sqrt(variance) * math.sqrt(252)  # annualized
            else:
                vol = 0
            
            # Store candidate: (symbol_id, T_ts, vol, curr_shares, prev_shares)
            opportunities.append({
                'sym_id': sym_id,
                'T_ts': T_ts,
                'vol': vol,
                'curr_shares': curr_shares,
                'prev_shares': prev_shares,
                'close': T_close,
                'base_epoch': period_ts
            })

    # Sort opportunities by T_ts
    opportunities.sort(key=lambda x: x['T_ts'])
    
    # Calculate 90th percentile of volatility for each T_ts
    vol_by_T = defaultdict(list)
    for opp in opportunities:
        vol_by_T[opp['T_ts']].append(opp['vol'])
    
    # Determine T_threshold for top decile
    for T_ts in vol_by_T:
        vols = sorted(vol_by_T[T_ts])
        if len(vols) >= 10:
            top_decile_threshold = vols[int(len(vols) * 0.9)]
        else:
            top_decile_threshold = float('inf')
        vol_by_T[T_ts] = top_decile_threshold
    
    # Apply all abstain conditions and mark valid opportunities
    valid_opps = []
    last_call_per_symbol = {}  # sym_id -> last T_ts when call issued
    
    # Separate unsealed (80%) and sealed (20%)
    n_total = len(opportunities)
    if n_total == 0:
        print("INSUFFICIENT=1")
        return
    
    n_unsealed = int(n_total * 0.8)
    unsealed_opps = opportunities[:n_unsealed]
    sealed_opps = opportunities[n_unsealed:]
    
    # Count independent observations in unsealed era
    # We need at least 30 independent observations to issue calls
    if len(unsealed_opps) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Process unsealed era
    for opp in unsealed_opps:
        sym_id = opp['sym_id']
        T_ts = opp['T_ts']
        
        # Check abstain conditions
        # 1. Price < $5 already checked
        # 2. <252 prior sessions already checked
        # 3. Missing fields already checked
        # 4. Trailing gain >30% already checked
        
        # 5. Volatility in top cross-sectional decile
        if opp['vol'] >= vol_by_T.get(T_ts, float('inf')):
            continue
        
        # 6. Call issued for same symbol in prior 60 trading days
        if sym_id in last_call_per_symbol:
            last_T = last_call_per_symbol[sym_id]
            # Approximate 60 trading days as ~84 calendar days
            if T_ts - last_T < 84 * 24 * 3600:
                continue
        
        # 7. Fewer than 30 independent observations remain
        # Since we're processing in order, we know remaining observations
        remaining = len(unsealed_opps) - unsealed_opps.index(opp)
        if remaining < 30:
            continue
        
        # All abstain conditions passed, mark as valid
        opp['valid'] = True
        last_call_per_symbol[sym_id] = T_ts
        valid_opps.append(opp)
    
    # Process sealed era similarly
    for opp in sealed_opps:
        sym_id = opp['sym_id']
        T_ts = opp['T_ts']
        
        # Same abstain conditions
        if opp['vol'] >= vol_by_T.get(T_ts, float('inf')):
            continue
        
        if sym_id in last_call_per_symbol:
            last_T = last_call_per_symbol[sym_id]
            if T_ts - last_T < 84 * 24 * 3600:
                continue
        
        # For sealed era, we don't need to check remaining observations
        opp['valid'] = True
        last_call_per_symbol[sym_id] = T_ts
        valid_opps.append(opp)
    
    # Now evaluate each valid opportunity
    # For UP calls, we need to check the label from prediction_outcomes
    # Since we can't write to database, we must fetch labels
    hits_total = 0
    hits_sealed = 0
    base_up_total = 0
    base_up_sealed = 0
    issued_total = 0
    issued_sealed = 0
    distinct_days_total = set()
    distinct_days_sealed = set()
    
    for opp in valid_opps:
        sym_id = opp['sym_id']
        T_ts = opp['T_ts']
        
        # Get the label from prediction_outcomes for horizon=60 days
        # We need to find the nearest prediction_outcomes entry after T_ts
        label_row = db.execute("""
            SELECT up, fwd_return FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = 60 AND ts >= ?
            ORDER BY ts ASC LIMIT 1
        """, [sym_id, T_ts]).fetchone()
        
        if label_row is None:
            continue
        
        # Check if label is in sealed era
        in_sealed = T_ts >= opportunities[n_unsealed]['T_ts'] if n_unsealed < len(opportunities) else False
        
        issued_total += 1
        distinct_days_total.add(T_ts // 86400)  # Group by day
        
        if in_sealed:
            issued_sealed += 1
            distinct_days_sealed.add(T_ts // 86400)
        
        if label_row['up'] == 1:
            hits_total += 1
            base_up_total += 1
            
            if in_sealed:
                hits_sealed += 1
                base_up_sealed += 1
        else:
            base_up_total += 1
            if in_sealed:
                base_up_sealed += 1
    
    # Calculate metrics
    if issued_total == 0:
        print("INSUFFICIENT=1")
        return
    
    precision_total = hits_total / issued_total
    base_rate_total = base_up_total / issued_total
    
    precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0.0
    base_rate_sealed = base_up_sealed / issued_sealed if issued_sealed > 0 else 0.0
    
    # Calculate design effect for effective sample size
    # Clustering by day
    day_counts = defaultdict(int)
    for opp in valid_opps:
        day_counts[opp['T_ts'] // 86400] += 1
    
    n_days = len(day_counts)
    if n_days > 1:
        # ICC approximation
        total_variance = 0
        day_means = []
        for day in day_counts:
            day_opps = [opp for opp in valid_opps if opp['T_ts'] // 86400 == day]
            # For binary outcomes, use proportion
            day_ups = sum(1 for opp in day_opps if opp.get('hit', False))
            day_mean = day_ups / len(day_opps)
            day_means.append(day_mean)
        
        overall_mean = sum(day_means) / len(day_means)
        between_var = sum((m - overall_mean) ** 2 for m in day_means) / (len(day_means) - 1)
        
        # Within-day variance approximation
        within_var = 0
        for day in day_counts:
            day_opps = [opp for opp in valid_opps if opp['T_ts'] // 86400 == day]
            p = sum(1 for opp in day_opps if opp.get('hit', False)) / len(day_opps)
            within_var += len(day_opps) * p * (1 - p)
        within_var /= len(valid_opps)
        
        design_effect = 1 + (between_var / (within_var + 1e-10)) * (issued_total / n_days)
        effective_n = issued_total / max(design_effect, 1.0001)  # Ensure >1
    else:
        effective_n = 1.0  # Minimum if all on one day
    
    # Check claim requirements
    precision_ok = precision_total >= 0.80
    abstention_rate = 1 - (issued_total / len(opportunities)) if len(opportunities) > 0 else 0
    abstention_ok = abstention_rate >= 0.95
    edge_ok = (precision_total - base_rate_total) >= 0.10
    
    # Print results
    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_total:.4f}")
    print(f"BASE_RATE={base_rate_total:.4f}")
    print(f"DISTINCT_DAYS={len(distinct_days_total)}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={precision_sealed:.4f}")
    
    db.close()

if __name__ == "__main__":
    main()
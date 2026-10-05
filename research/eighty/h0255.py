import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0

    # Check for 13F filing date availability
    cur.execute("SELECT COUNT(*) FROM inst_holdings LIMIT 1")
    if cur.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        return 0

    # Get all symbol_ids with enough daily bars
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING cnt >= 252
    """)
    eligible_symbols = {row[0] for row in cur.fetchall()}

    # Get all 13F periods with symbol_id and compute shares change
    # We need to compare each 13F period to the preceding one for the same symbol
    cur.execute("""
        SELECT symbol_id, period, shares, value
        FROM inst_holdings
        WHERE symbol_id IN ({})
    """.format(','.join('?' * len(eligible_symbols))), list(eligible_symbols))
    
    # Group by symbol_id and sort by period
    symbol_data = defaultdict(list)
    for row in cur.fetchall():
        symbol_data[row['symbol_id']].append((row['period'], row['shares'], row['value']))
    
    for sym in symbol_data:
        symbol_data[sym].sort(key=lambda x: x[0])

    # Precompute daily bars for eligible symbols
    cur.execute("""
        SELECT symbol_id, ts, close, high, low, open, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?' * len(eligible_symbols))), list(eligible_symbols))
    
    daily_bars = defaultdict(list)
    for row in cur.fetchall():
        daily_bars[row['symbol_id']].append(row)

    # Precompute shares outstanding from fundamentals
    cur.execute("""
        SELECT symbol_id, value as shares_out
        FROM fundamentals
        WHERE metric = 'SharesOutstanding' AND symbol_id IN ({})
        ORDER BY fetched_at DESC
    """.format(','.join('?' * len(eligible_symbols))), list(eligible_symbols))
    
    shares_outstanding = {}
    for row in cur.fetchall():
        if row['symbol_id'] not in shares_outstanding:
            shares_outstanding[row['symbol_id']] = float(row['shares_out'])

    # Get prediction outcomes for labels
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 20 AND symbol_id IN ({})
    """.format(','.join('?' * len(eligible_symbols))), list(eligible_symbols))
    
    labels = {}
    for row in cur.fetchall():
        key = (row['symbol_id'], row['ts'])
        labels[key] = row['up']

    # Determine all possible 13F disclosure dates (using period as approximation)
    all_disclosure_dates = set()
    for sym, periods in symbol_data.items():
        for period, _, _ in periods:
            all_disclosure_dates.add(period)
    
    # Convert periods to timestamps for comparison
    disclosure_timestamps = {}
    for period in all_disclosure_dates:
        try:
            # Parse period string to timestamp
            cur.execute("SELECT strftime('%s', ?)", (period,))
            ts = int(cur.fetchone()[0])
            disclosure_timestamps[period] = ts
        except:
            continue

    # Process each symbol and disclosure date
    opportunities = 0
    issued_calls = []
    
    for sym in eligible_symbols:
        if sym not in symbol_data or sym not in daily_bars or sym not in shares_outstanding:
            continue
            
        periods = symbol_data[sym]
        bars = daily_bars[sym]
        shares_out = shares_outstanding[sym]
        
        # Create timestamp to bar mapping
        ts_to_bar = {bar['ts']: bar for bar in bars}
        
        for i in range(1, len(periods)):
            current_period, current_shares, current_value = periods[i]
            prev_period, prev_shares, prev_value = periods[i-1]
            
            # Get the timestamp for current period (T)
            T = disclosure_timestamps.get(current_period)
            if not T:
                continue
                
            opportunities += 1
            
            # Check if we have enough historical bars
            if T not in ts_to_bar:
                continue
                
            # Check we have T-252 bars
            idx = None
            for j, bar in enumerate(bars):
                if bar['ts'] == T:
                    idx = j
                    break
            if idx is None or idx < 252:
                continue
                
            # Check close >= $5
            if bars[idx]['close'] < 5:
                continue
                
            # Check ADV >= $5M over T-60..T-1
            total_vol = sum(bars[idx-60:idx]['volume'])
            avg_vol = total_vol / 60
            adv = avg_vol * bars[idx-1]['close']  # approximate
            if adv < 5_000_000:
                continue
                
            # Check 20-day return through T-1 <= -5%
            close_T1 = bars[idx-1]['close']
            close_T20 = bars[idx-20]['close']
            ret_20d = (close_T1 - close_T20) / close_T20
            if ret_20d > -0.05:
                continue
                
            # Check aggregate disclosed holdings >= 1.5% of shares outstanding
            disclosed_pct = (current_shares / shares_out) * 100
            if disclosed_pct < 1.5:
                continue
                
            # Check increase >= 0.75 percentage points
            prev_disclosed_pct = (prev_shares / shares_out) * 100
            if (disclosed_pct - prev_disclosed_pct) < 0.75:
                continue
                
            # Check 20-session realized volatility not in top decile
            # Calculate volatility as std dev of returns
            returns = []
            for j in range(idx-20, idx):
                ret = (bars[j]['close'] - bars[j-1]['close']) / bars[j-1]['close']
                returns.append(ret)
            
            if len(returns) < 20:
                continue
                
            mean_ret = sum(returns) / len(returns)
            var = sum((r - mean_ret) ** 2 for r in returns) / (len(returns) - 1)
            vol = var ** 0.5
            
            # We'll check this later when we know the cross-sectional decile
            # For now, store with the volatility
            call_candidate = {
                'symbol': sym,
                'T': T,
                'vol': vol,
                'idx': idx
            }
            
            # Check no call in prior 20 trading days
            prior_calls = [c for c in issued_calls 
                         if c['symbol'] == sym 
                         and T - c['T'] <= 20 * 86400]  # approx
            if prior_calls:
                continue
                
            # We need the label
            if (sym, T) not in labels:
                continue
                
            issued_calls.append(call_candidate)

    # Now filter by volatility decile
    if issued_calls:
        vols = [c['vol'] for c in issued_calls]
        vols.sort()
        p90 = vols[int(0.9 * len(vols))]
        
        final_calls = [c for c in issued_calls if c['vol'] <= p90]
    else:
        final_calls = []

    # If fewer than 30 observations, insufficient
    if len(final_calls) < 30:
        print("INSUFFICIENT=1")
        return 0

    # Split into train/test (80/20)
    final_calls.sort(key=lambda x: x['T'])
    split_idx = int(0.8 * len(final_calls))
    train_calls = final_calls[:split_idx]
    test_calls = final_calls[split_idx:]

    # Compute metrics for train
    train_hits = 0
    for call in train_calls:
        if labels.get((call['symbol'], call['T']), False):
            train_hits += 1
    
    train_precision = train_hits / len(train_calls) if train_calls else 0
    train_base = sum(1 for c in train_calls if labels.get((c['symbol'], c['T']), False)) / len(train_calls)
    
    # Compute metrics for test (sealed)
    test_hits = 0
    for call in test_calls:
        if labels.get((call['symbol'], call['T']), False):
            test_hits += 1
    
    test_precision = test_hits / len(test_calls) if test_calls else 0

    # Compute design effect and effective N
    # Group by day
    day_counts = defaultdict(int)
    for call in final_calls:
        # Get the UTC day of T
        day = call['T'] // 86400
        day_counts[day] += 1
    
    n_days = len(day_counts)
    n_issued = len(final_calls)
    
    # Design effect = 1 + (average cluster size - 1) * ICC
    # For simplicity, assume ICC = 0.5 (conservative estimate for financial data)
    avg_cluster_size = n_issued / n_days if n_days > 0 else 1
    icc = 0.5
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = n_issued / design_effect

    # Print results
    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_precision:.4f}")
    print(f"BASE_RATE={train_base:.4f}")
    print(f"DISTINCT_DAYS={n_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={test_precision:.4f}")

    conn.close()
    return 0

if __name__ == "__main__":
    sys.exit(main())
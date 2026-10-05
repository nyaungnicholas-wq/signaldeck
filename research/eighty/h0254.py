import sqlite3
import sys
from collections import defaultdict

def main():
    # Connect to database
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    cur = conn.cursor()
    
    # Verify required tables exist
    required_tables = ['bars', 'symbols', 'insider_trades']
    try:
        for t in required_tables:
            cur.execute(f"SELECT 1 FROM {t} LIMIT 0")
    except sqlite3.OperationalError:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Get all trading days with close >= $5 and >= 252 prior sessions
    # Build mapping from symbol_id -> list of (date, close, volume, dollar_vol)
    print("Loading bars...", file=sys.stderr)
    bars_by_symbol = defaultdict(list)
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    
    prev_symbol = None
    count = 0
    for symbol_id, ts, close, volume in cur:
        if symbol_id != prev_symbol:
            if prev_symbol is not None and count >= 252:
                # Only keep symbols with >=252 prior sessions
                pass
            prev_symbol = symbol_id
            count = 0
        dollar_vol = close * volume if close and volume else 0
        bars_by_symbol[symbol_id].append((ts, close, volume, dollar_vol))
        count += 1
    
    # Filter symbols with >=252 bars
    valid_symbols = {s: bars for s, bars in bars_by_symbol.items() if len(bars) >= 252}
    print(f"Valid symbols: {len(valid_symbols)}", file=sys.stderr)
    
    # Load insider sales with code='S' (open market sales)
    print("Loading insider trades...", file=sys.stderr)
    insider_sales = []
    cur.execute("""
        SELECT symbol_id, filed_ts, tx_ts
        FROM insider_trades
        WHERE code = 'S'
    """)
    
    for symbol_id, filed_ts, tx_ts in cur.fetchall():
        insider_sales.append((symbol_id, filed_ts, tx_ts))
    
    print(f"Insider sales: {len(insider_sales)}", file=sys.stderr)
    
    # Group sales by (symbol_id, disclosure_date)
    # disclosure_date = filed_ts (as-of discipline)
    sales_by_disclosure = defaultdict(list)
    for symbol_id, filed_ts, tx_ts in insider_sales:
        if symbol_id not in valid_symbols:
            continue
        # Convert filed_ts to trading day
        sales_by_disclosure[(symbol_id, filed_ts)].append(tx_ts)
    
    # For each symbol, build lookup of trading days
    print("Building index...", file=sys.stderr)
    symbol_days = {}
    for symbol_id, bars in valid_symbols.items():
        # Create mapping: ts -> (close, dollar_vol)
        symbol_days[symbol_id] = {}
        for i, (ts, close, volume, dollar_vol) in enumerate(bars):
            symbol_days[symbol_id][ts] = (close, dollar_vol, i)
    
    # Process each disclosure date
    print("Processing opportunities...", file=sys.stderr)
    opportunities = []  # (symbol_id, disclosure_date, day_index, forward_return, is_hit)
    
    for (symbol_id, disclosure_date), tx_dates in sales_by_disclosure.items():
        bars = valid_symbols[symbol_id]
        day_map = symbol_days[symbol_id]
        
        if disclosure_date not in day_map:
            continue
        
        day_idx = day_map[disclosure_date][2]
        
        # Need at least 252 prior sessions
        if day_idx < 252:
            continue
        
        # Get close at T-1 (previous trading day)
        if day_idx < 1:
            continue
        close_t_minus_1 = bars[day_idx - 1][1]
        
        # Close >= $5 at T-1
        if close_t_minus_1 < 5:
            continue
        
        # 20-day return through T-1: from T-21 to T-1
        if day_idx < 21:
            continue
        close_t_minus_21 = bars[day_idx - 21][1]
        if close_t_minus_21 <= 0:
            continue
        ret_20d = (close_t_minus_1 / close_t_minus_21) - 1
        
        # 20-day return >= 10%
        if ret_20d < 0.10:
            continue
        
        # Average daily dollar volume over T-60..T-1
        if day_idx < 60:
            continue
        total_dollar_vol = 0
        for i in range(day_idx - 60, day_idx):
            total_dollar_vol += bars[i][3]
        avg_dollar_vol = total_dollar_vol / 60
        
        if avg_dollar_vol < 5_000_000:
            continue
        
        # Check for qualifying sale within T-10..T-1
        # Get trading days in range
        t_minus_10_idx = day_idx - 10
        if t_minus_10_idx < 0:
            continue
        qualifying_sale = False
        for tx_date in tx_dates:
            if tx_date in day_map:
                tx_idx = day_map[tx_date][2]
                if t_minus_10_idx <= tx_idx < day_idx:
                    qualifying_sale = True
                    break
        
        if not qualifying_sale:
            continue
        
        # Check 20-session realized volatility at T (T-20 to T-1)
        if day_idx < 20:
            continue
        vol_returns = []
        for i in range(day_idx - 20, day_idx):
            if bars[i][1] > 0 and bars[i-1][1] > 0:
                vol_returns.append(bars[i][1] / bars[i-1][1] - 1)
        
        if len(vol_returns) < 20:
            continue
        
        mean_ret = sum(vol_returns) / len(vol_returns)
        variance = sum((r - mean_ret) ** 2 for r in vol_returns) / (len(vol_returns) - 1)
        volatility = variance ** 0.5
        
        # Will need cross-sectional decile later, store volatility with symbol/date
        opportunities.append({
            'symbol_id': symbol_id,
            'disclosure_date': disclosure_date,
            'day_idx': day_idx,
            'volatility': volatility,
            'bars': bars,
            'day_map': day_map
        })
    
    print(f"Opportunities after initial filter: {len(opportunities)}", file=sys.stderr)
    
    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Compute cross-sectional volatility decile for each date
    # Group opportunities by disclosure_date
    by_date = defaultdict(list)
    for opp in opportunities:
        by_date[opp['disclosure_date']].append(opp)
    
    # Compute 90th percentile volatility for each date
    vol_90th = {}
    for date, opps in by_date.items():
        vols = sorted([o['volatility'] for o in opps])
        idx = int(len(vols) * 0.9)
        vol_90th[date] = vols[min(idx, len(vols)-1)]
    
    # Filter out top volatility decile
    filtered_opps = []
    for opp in opportunities:
        if opp['volatility'] < vol_90th[opp['disclosure_date']]:
            filtered_opps.append(opp)
    
    print(f"Opportunities after volatility filter: {len(filtered_opps)}", file=sys.stderr)
    
    # Sort by disclosure_date
    filtered_opps.sort(key=lambda x: x['disclosure_date'])
    
    # Hold out most recent 20%
    split_idx = int(len(filtered_opps) * 0.8)
    train_opps = filtered_opps[:split_idx]
    test_opps = filtered_opps[split_idx:]
    
    # Process calls with overlap check
    print("Processing calls...", file=sys.stderr)
    issued_calls = []  # (symbol_id, disclosure_date, day_idx, forward_return)
    last_call_by_symbol = {}  # symbol_id -> last disclosure_date with call
    seen_dates = set()
    
    for opp in train_opps:
        symbol_id = opp['symbol_id']
        disclosure_date = opp['disclosure_date']
        day_idx = opp['day_idx']
        bars = opp['bars']
        
        # Check if call was issued for same symbol in prior 20 trading days
        if symbol_id in last_call_by_symbol:
            last_date = last_call_by_symbol[symbol_id]
            # Calculate difference in trading days
            last_idx = opp['day_map'][last_date][2]
            if day_idx - last_idx < 20:
                continue
        
        # Check if we have enough remaining observations
        remaining = len(train_opps) - len(issued_calls)
        if remaining < 30:
            continue
        
        # Get forward return (T to T+20 trading days)
        if day_idx + 20 >= len(bars):
            continue
        
        close_t = bars[day_idx][1]
        close_t_plus_20 = bars[day_idx + 20][1]
        
        if close_t <= 0:
            continue
        
        forward_return = (close_t_plus_20 / close_t) - 1
        is_hit = forward_return < 0  # DOWN call, so negative return is hit
        
        issued_calls.append({
            'symbol_id': symbol_id,
            'disclosure_date': disclosure_date,
            'forward_return': forward_return,
            'is_hit': is_hit
        })
        
        last_call_by_symbol[symbol_id] = disclosure_date
        seen_dates.add(disclosure_date // 86400)  # Convert epoch to day
    
    # Same for test set
    test_calls = []
    last_call_by_symbol_test = {}
    
    for opp in test_opps:
        symbol_id = opp['symbol_id']
        disclosure_date = opp['disclosure_date']
        day_idx = opp['day_idx']
        bars = opp['bars']
        
        # Check overlap with test calls only
        if symbol_id in last_call_by_symbol_test:
            last_date = last_call_by_symbol_test[symbol_id]
            last_idx = opp['day_map'][last_date][2]
            if day_idx - last_idx < 20:
                continue
        
        if day_idx + 20 >= len(bars):
            continue
        
        close_t = bars[day_idx][1]
        close_t_plus_20 = bars[day_idx + 20][1]
        
        if close_t <= 0:
            continue
        
        forward_return = (close_t_plus_20 / close_t) - 1
        is_hit = forward_return < 0
        
        test_calls.append({
            'symbol_id': symbol_id,
            'disclosure_date': disclosure_date,
            'forward_return': forward_return,
            'is_hit': is_hit
        })
        
        last_call_by_symbol_test[symbol_id] = disclosure_date
    
    # Compute metrics
    if len(issued_calls) == 0:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    hits = sum(1 for c in issued_calls if c['is_hit'])
    precision = hits / len(issued_calls)
    
    # Base rate: proportion of DOWN (negative return) in issued calls
    down_count = len(issued_calls)  # All issued are DOWN calls
    base_rate = precision  # Since precision = hits/issued = proportion with negative return
    
    distinct_days = len(seen_dates)
    
    # Compute design effect
    # Daily counts of calls
    daily_counts = defaultdict(int)
    for c in issued_calls:
        daily_counts[c['disclosure_date'] // 86400] += 1
    
    n = len(issued_calls)
    total_days = len(daily_counts)
    
    if total_days <= 1:
        design_effect = 1
    else:
        p = n / total_days
        variance_daily = sum((count - p) ** 2 for count in daily_counts.values()) / total_days
        expected_variance = p * (1 - p) if p * (1 - p) > 0 else 1e-10
        design_effect = variance_daily / expected_variance if expected_variance > 0 else 1
        if design_effect < 1:
            design_effect = 1
    
    effective_n = n / design_effect if design_effect > 0 else n
    
    # Test metrics
    test_hits = sum(1 for c in test_calls if c['is_hit'])
    test_precision = test_hits / len(test_calls) if test_calls else 0
    
    # Print required outputs
    print(f"ISSUED={len(issued_calls)}")
    print(f"OPPORTUNITIES={len(train_opps)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={test_precision:.4f}")
    
    # Check invariants
    if distinct_days > len(issued_calls):
        print("ERROR: DISTINCT_DAYS > ISSUED", file=sys.stderr)
    if effective_n >= len(issued_calls):
        print("ERROR: EFFECTIVE_N >= ISSUED", file=sys.stderr)
    
    conn.close()

if __name__ == "__main__":
    main()
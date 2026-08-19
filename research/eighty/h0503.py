# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 502
# cycle_index: 32
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except:
        print("INSUFFICIENT=1")
        print("OPPORTUNITIES=0")
        print("ISSUED=0")
        print("PRECISION=0.0")
        print("BASE_RATE=0.0")
        print("DISTINCT_DAYS=0")
        print("EFFECTIVE_N=0.0")
        print("SEALED_PRECISION=0.0")
        return

    # Get all symbols with both insider trades and SharesOutstanding fundamentals
    symbols_with_insider = set()
    try:
        for row in conn.execute("SELECT DISTINCT symbol_id FROM insider_trades"):
            symbols_with_insider.add(row[0])
    except:
        print("INSUFFICIENT=1")
        conn.close()
        return

    symbols_with_shares = set()
    try:
        for row in conn.execute("SELECT DISTINCT symbol_id FROM fundamentals WHERE metric='SharesOutstanding'"):
            symbols_with_shares.add(row[0])
    except:
        print("INSUFFICIENT=1")
        conn.close()
        return

    universe = symbols_with_insider & symbols_with_shares
    if not universe:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Pre-load all insider trades (Form 4 sales only)
    insider_sales = []
    try:
        for row in conn.execute("""
            SELECT symbol_id, insider, code, filed_ts 
            FROM insider_trades 
            WHERE code='S' AND form='4'
        """):
            insider_sales.append(row)
    except:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Pre-load all SharesOutstanding fundamentals
    shares_data = defaultdict(list)
    try:
        for row in conn.execute("""
            SELECT symbol_id, as_of, fetched_at, value 
            FROM fundamentals 
            WHERE metric='SharesOutstanding'
        """):
            shares_data[row[0]].append((row[1], row[2], row[3]))
    except:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Pre-load all insider purchases for the same issuer check
    insider_purchases = defaultdict(list)
    try:
        for row in conn.execute("""
            SELECT symbol_id, filed_ts 
            FROM insider_trades 
            WHERE code='P' AND form='4'
        """):
            insider_purchases[row[0]].append(row[1])
    except:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Pre-load price data for all symbols in universe (daily bars)
    price_data = defaultdict(list)
    try:
        for symbol_id in universe:
            for row in conn.execute("""
                SELECT ts, close FROM bars 
                WHERE symbol_id=? AND tf='1d'
                ORDER BY ts
            """, (symbol_id,)):
                price_data[symbol_id].append((row[0], row[1]))
    except:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Process each symbol
    opportunities = 0
    calls = []
    for symbol_id in universe:
        if symbol_id not in shares_data or len(shares_data[symbol_id]) < 2:
            continue
            
        # Sort fundamentals by as_of for this symbol
        quarters = sorted(shares_data[symbol_id], key=lambda x: x[0])
        
        # Pre-compute 200-day moving averages for this symbol
        prices = price_data.get(symbol_id, [])
        if len(prices) < 200:
            continue
            
        # Build price lookup by timestamp
        price_dict = {ts: close for ts, close in prices}
        
        # Process each insider sale for this symbol
        for sale in insider_sales:
            if sale[0] != symbol_id:
                continue
                
            filed_ts = sale[3]
            opportunities += 1
            
            # Find most recent SharesOutstanding with fetched_at < filed_ts
            current_shares = None
            current_as_of = None
            for as_of, fetched_at, value in reversed(quarters):
                if fetched_at < filed_ts:
                    current_shares = value
                    current_as_of = as_of
                    break
                    
            if current_shares is None or current_as_of is None:
                continue
                
            # Check if shares data is within 180 days
            if (filed_ts - current_as_of) > 180 * 86400:
                continue
                
            # Find same quarter one year earlier
            year_ago_as_of = current_as_of - 365 * 86400
            year_ago_shares = None
            for as_of, fetched_at, value in quarters:
                if abs(as_of - year_ago_as_of) < 30 * 86400 and fetched_at < filed_ts:
                    year_ago_shares = value
                    break
                    
            if year_ago_shares is None or year_ago_shares == 0:
                continue
                
            # Check 2% decline
            if current_shares >= year_ago_shares * 0.98:
                continue
                
            # Check no purchase in prior 10 trading days
            has_recent_purchase = False
            for purchase_ts in insider_purchases.get(symbol_id, []):
                # Convert to approximate trading days (just check 10 calendar days)
                if filed_ts - 10 * 86400 <= purchase_ts < filed_ts:
                    has_recent_purchase = True
                    break
            if has_recent_purchase:
                continue
                
            # Find price at time of disclosure
            disclosure_price = None
            for ts, close in reversed(prices):
                if ts <= filed_ts:
                    disclosure_price = close
                    break
                    
            if disclosure_price is None:
                continue
                
            # Calculate 200-day moving average
            recent_prices = []
            for ts, close in reversed(prices):
                if ts <= filed_ts:
                    recent_prices.append(close)
                    if len(recent_prices) >= 200:
                        break
                        
            if len(recent_prices) < 200:
                continue
                
            ma_200 = sum(recent_prices) / 200
            if disclosure_price >= ma_200:
                continue
                
            # Find forward price (21 trading days)
            forward_ts = None
            forward_price = None
            count = 0
            for ts, close in prices:
                if ts > filed_ts:
                    count += 1
                    if count == 21:
                        forward_ts = ts
                        forward_price = close
                        break
                        
            if forward_price is None:
                continue
                
            # Determine outcome (down = 1 if forward price < disclosure price, else 0)
            outcome = 1 if forward_price < disclosure_price else 0
            
            # Store call with date (filed_ts day)
            call_date = filed_ts // 86400
            calls.append({
                'symbol_id': symbol_id,
                'filed_ts': filed_ts,
                'call_date': call_date,
                'outcome': outcome
            })

    # No sufficient data
    if len(calls) < 20:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Sort calls by filed_ts
    calls.sort(key=lambda x: x['filed_ts'])
    
    # Split into train and sealed (most recent 20%)
    split_idx = int(len(calls) * 0.8)
    sealed_calls = calls[split_idx:]
    train_calls = calls[:split_idx]
    
    # Calculate metrics for all calls
    issued = len(calls)
    hits = sum(1 for c in calls if c['outcome'] == 1)
    precision = hits / issued if issued > 0 else 0
    base_rate = hits / issued if issued > 0 else 0  # Base rate within issued subset
    
    # Calculate distinct days
    call_days = set()
    for call in calls:
        call_days.add(call['call_date'])
    distinct_days = len(call_days)
    
    # Calculate design effect (cluster by symbol and day)
    clusters = defaultdict(list)
    for call in calls:
        key = (call['symbol_id'], call['call_date'])
        clusters[key].append(call['outcome'])
    
    # Calculate ICC using one-way random effects model
    # Count per cluster
    cluster_sizes = [len(v) for v in clusters.values()]
    cluster_means = [sum(v)/len(v) for v in clusters.values()]
    grand_mean = sum(1 for c in calls if c['outcome'] == 1) / issued if issued > 0 else 0
    
    # Mean squares
    n = issued
    m = len(clusters)
    if m <= 1:
        design_effect = 1.0
    else:
        MSB = sum(s * (mean - grand_mean)**2 for s, mean in zip(cluster_sizes, cluster_means)) / (m - 1)
        MSW = 0
        for cluster in clusters.values():
            mean = sum(cluster) / len(cluster)
            MSW += sum((x - mean)**2 for x in cluster)
        MSW /= (n - m)
        
        if MSB + MSW == 0:
            design_effect = 1.0
        else:
            ICC = (MSB - MSW) / (MSB + (cluster_sizes[0] - 1) * MSW) if len(set(cluster_sizes)) == 1 else (MSB - MSW) / MSB
            avg_cluster_size = n / m
            design_effect = 1 + (avg_cluster_size - 1) * max(0, ICC)
    
    effective_n = issued / design_effect if design_effect > 0 else 0
    
    # Calculate sealed metrics
    sealed_hits = sum(1 for c in sealed_calls if c['outcome'] == 1)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    # Print results
    print(f"OPPORTUNITIES={opportunities}")
    print(f"ISSUED={issued}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
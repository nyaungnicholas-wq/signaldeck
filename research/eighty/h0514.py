# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 513
# cycle_index: 43
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Check data availability
    c.execute("SELECT COUNT(*) FROM insider_trades")
    insider_count = c.fetchone()[0]
    c.execute("SELECT COUNT(*) FROM fundamentals WHERE metric='EPS'")
    eps_count = c.fetchone()[0]
    c.execute("SELECT COUNT(*) FROM macro_series WHERE series='DGS10'")
    treasury_count = c.fetchone()[0]
    
    if insider_count < 100 or eps_count < 10 or treasury_count < 100:
        print("INSUFFICIENT=1")
        return
    
    # Get universe: symbols with >=3 years daily data, insider trades, and EPS records
    c.execute("""
        SELECT s.id, s.symbol 
        FROM symbols s
        JOIN (SELECT symbol_id, COUNT(DISTINCT ts/86400) as days 
              FROM bars WHERE tf='1d' 
              GROUP BY symbol_id 
              HAVING days >= 756) b ON s.id = b.symbol_id
        JOIN (SELECT DISTINCT symbol_id FROM insider_trades WHERE code='P') i ON s.id = i.symbol_id
        JOIN (SELECT DISTINCT symbol_id FROM fundamentals WHERE metric='EPS') e ON s.id = e.symbol_id
        WHERE s.active = 1 AND s.market = 'stocks'
    """)
    symbols = c.fetchall()
    
    if len(symbols) < 20:
        print("INSUFFICIENT=1")
        return
    
    # Get all EPS records with dates
    c.execute("""
        SELECT symbol_id, value as eps, fetched_at as release_date
        FROM fundamentals 
        WHERE metric='EPS' 
        ORDER BY fetched_at
    """)
    eps_records = c.fetchall()
    
    # Get 10Y Treasury yield history
    c.execute("""
        SELECT ts/86400 as day, value 
        FROM macro_series 
        WHERE series='DGS10'
    """)
    treasury_data = c.fetchall()
    treasury_by_day = {row[0]: row[1]/100 for row in treasury_data}
    
    # Get all insider purchases
    c.execute("""
        SELECT symbol_id, filed_ts/86400 as day
        FROM insider_trades 
        WHERE code='P'
    """)
    insider_buys = c.fetchall()
    
    # Organize insider buys by symbol and day
    insider_by_symbol = defaultdict(list)
    for symbol_id, day in insider_buys:
        insider_by_symbol[symbol_id].append(day)
    
    # Get price history for all symbols
    c.execute("""
        SELECT symbol_id, ts/86400 as day, close, volume
        FROM bars 
        WHERE tf='1d'
        ORDER BY symbol_id, ts
    """)
    price_data = c.fetchall()
    
    # Organize price data by symbol
    prices_by_symbol = defaultdict(list)
    for symbol_id, day, close, volume in price_data:
        prices_by_symbol[symbol_id].append((day, close, volume))
    
    # Get 13F institutional holdings
    c.execute("""
        SELECT symbol_id, period, shares, 
               (SELECT value FROM fundamentals 
                WHERE symbol_id = h.symbol_id 
                AND metric='SharesOutstanding' 
                AND fetched_at <= h.period
                ORDER BY fetched_at DESC LIMIT 1) as shares_outstanding
        FROM inst_holdings h
    """)
    holdings_data = c.fetchall()
    
    # Organize holdings by symbol with most recent valid period
    holdings_by_symbol = {}
    for symbol_id, period, shares, shares_outstanding in holdings_data:
        if shares_outstanding and shares_outstanding > 0:
            if symbol_id not in holdings_by_symbol or period > holdings_by_symbol[symbol_id][0]:
                ownership_pct = shares / shares_outstanding
                holdings_by_symbol[symbol_id] = (period, ownership_pct)
    
    opportunities = []
    symbol_ids = [s[0] for s in symbols]
    
    for symbol_id, symbol in symbols:
        prices = prices_by_symbol[symbol_id]
        if len(prices) < 252:  # Need at least 1 year of data
            continue
            
        # Get EPS releases for this symbol
        symbol_eps = [(eps, release_date) for sid, eps, release_date in eps_records 
                     if sid == symbol_id and eps and eps > 0]
        
        for eps, release_date in symbol_eps:
            # Find next trading day after release
            release_days = [p[0] for p in prices]
            next_days = [d for d in release_days if d > release_date]
            if not next_days:
                continue
            decision_day = min(next_days)
            
            # Get price and volume on decision day
            decision_prices = [(d, c, v) for d, c, v in prices if d == decision_day]
            if not decision_prices:
                continue
            _, price, volume = decision_prices[0]
            
            # Check 20-day average dollar volume >= $1M
            prev_prices = [(d, c, v) for d, c, v in prices if d < decision_day][-20:]
            if len(prev_prices) < 20:
                continue
            avg_vol = sum(p[1] * p[2] for p in prev_prices) / len(prev_prices)
            if avg_vol < 1_000_000:
                continue
            
            # Get 10Y Treasury yield on decision day
            if decision_day not in treasury_by_day:
                continue
            treasury_yield = treasury_by_day[decision_day]
            
            # Calculate earnings yield
            earnings_yield = eps / price
            spread = earnings_yield - treasury_yield
            
            if spread <= 0.02:
                continue
            
            # Check insider buy in prior 60 days
            insider_days = insider_by_symbol.get(symbol_id, [])
            recent_buys = [d for d in insider_days if decision_day - 60 <= d < decision_day]
            if not recent_buys:
                continue
            
            # Check institutional ownership >= 30%
            if symbol_id not in holdings_by_symbol:
                continue
            _, ownership = holdings_by_symbol[symbol_id]
            if ownership < 0.30:
                continue
            
            # Get label: prediction at horizon 21 days
            # First check prediction_outcomes table
            c.execute("""
                SELECT up FROM prediction_outcomes 
                WHERE symbol_id = ? 
                AND horizon = 21
                AND ts >= ?
                AND ts < ?
                LIMIT 1
            """, (symbol_id, decision_day * 86400, (decision_day + 30) * 86400))
            result = c.fetchone()
            
            if result:
                hit = 1 if result[0] else 0
            else:
                # Calculate from price if no prediction outcome
                future_prices = [(d, c) for d, c, _ in prices 
                               if decision_day < d <= decision_day + 30]
                if len(future_prices) < 21:
                    continue
                future_price = future_prices[20][1]
                hit = 1 if future_price > price else 0
            
            opportunities.append({
                'symbol': symbol,
                'symbol_id': symbol_id,
                'decision_day': decision_day,
                'hit': hit,
                'price': price
            })
    
    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Sort by decision day
    opportunities.sort(key=lambda x: x['decision_day'])
    
    # Split 80/20 train/sealed
    split_idx = int(len(opportunities) * 0.8)
    train_ops = opportunities[:split_idx]
    sealed_ops = opportunities[split_idx:]
    
    # Compute metrics
    issued = len(train_ops)
    hits = sum(op['hit'] for op in train_ops)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate of predicted class (up)
    base_rate = sum(op['hit'] for op in opportunities) / len(opportunities) if opportunities else 0
    
    # Distinct days in issued calls
    distinct_days = len(set(op['decision_day'] for op in train_ops))
    
    # Design effect calculation (cluster by day)
    day_clusters = defaultdict(list)
    for op in train_ops:
        day_clusters[op['decision_day']].append(op['hit'])
    
    m = issued / len(day_clusters)  # Average cluster size
    
    # Calculate ICC
    grand_mean = sum(op['hit'] for op in train_ops) / issued
    ss_between = sum(len(clusters) * (sum(clusters)/len(clusters) - grand_mean)**2 
                    for clusters in day_clusters.values())
    ss_within = sum(sum((x - sum(clusters)/len(clusters))**2 for x in clusters) 
                   for clusters in day_clusters.values())
    
    df_between = len(day_clusters) - 1
    df_within = issued - len(day_clusters)
    
    ms_between = ss_between / df_between if df_between > 0 else 0
    ms_within = ss_within / df_within if df_within > 0 else 0
    
    icc = (ms_between - ms_within) / (ms_between + (m - 1) * ms_within) if ms_within > 0 else 0
    icc = max(icc, 0.01)  # Ensure positive
    
    design_effect = 1 + (m - 1) * icc
    effective_n = issued / design_effect
    
    # Sealed era metrics
    sealed_issued = len(sealed_ops)
    sealed_hits = sum(op['hit'] for op in sealed_ops)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
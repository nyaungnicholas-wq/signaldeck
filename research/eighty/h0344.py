# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 343
# cycle_index: 11
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3, datetime, re, statistics

def main():
    # Connect to database
    try:
        db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        db.execute("PRAGMA query_only = ON")
    except:
        print("INSUFFICIENT=1")
        return

    # Compile regex patterns
    p1 = re.compile(r'\b(SEC|DOJ|FBI|prosecutor|attorney general|regulator|exchange)\b.{0,80}\b(investigat|subpoena|prosecut|indict|enforcement|charge|probe)\b', re.IGNORECASE)
    p2 = re.compile(r'\b(investigat|subpoena|prosecut|indict|enforcement|charge|probe)\b.{0,80}\b(SEC|DOJ|FBI|prosecutor|attorney general|regulator|exchange)\b', re.IGNORECASE)
    
    # Get all news headlines matching regex
    news_rows = db.execute("""
        SELECT symbol_id, ts, headline 
        FROM news 
        WHERE headline LIKE '%SEC%' OR headline LIKE '%DOJ%' OR headline LIKE '%FBI%' OR 
              headline LIKE '%prosecutor%' OR headline LIKE '%attorney general%' OR 
              headline LIKE '%regulator%' OR headline LIKE '%exchange%' OR
              headline LIKE '%investigat%' OR headline LIKE '%subpoena%' OR 
              headline LIKE '%prosecut%' OR headline LIKE '%indict%' OR 
              headline LIKE '%enforcement%' OR headline LIKE '%charge%' OR 
              headline LIKE '%probe%'
    """).fetchall()
    
    # Filter with regex
    matching = []
    for symbol_id, ts, headline in news_rows:
        if p1.search(headline) or p2.search(headline):
            matching.append((symbol_id, ts))
    
    if not matching:
        print("INSUFFICIENT=1")
        return
    
    # Get unique symbols from matches
    unique_symbols = list(set(symbol_id for symbol_id, _ in matching))
    
    # Pre-fetch all 1d bars for these symbols
    bars_by_symbol = {}
    for symbol_id in unique_symbols:
        rows = db.execute("""
            SELECT ts, close, volume 
            FROM bars 
            WHERE symbol_id = ? AND tf = '1d' 
            ORDER BY ts
        """, (symbol_id,)).fetchall()
        
        bars_by_symbol[symbol_id] = []
        for ts, close, volume in rows:
            dt = datetime.datetime.utcfromtimestamp(ts).date()
            bars_by_symbol[symbol_id].append((dt, ts, close, volume))
    
    # Group matching news by (symbol_id, UTC date)
    opportunities = {}
    for symbol_id, ts in matching:
        dt = datetime.datetime.utcfromtimestamp(ts).date()
        key = (symbol_id, dt)
        if key not in opportunities:
            opportunities[key] = True
    
    # Process each opportunity
    issued = []
    for (symbol_id, event_date), _ in opportunities.items():
        # Get bars for this symbol
        bars = bars_by_symbol.get(symbol_id, [])
        if len(bars) < 252:
            continue
        
        # Find event_date in bars
        event_idx = None
        for i, (dt, ts, close, volume) in enumerate(bars):
            if dt == event_date:
                event_idx = i
                break
        
        if event_idx is None:
            continue  # event_date not a trading day
        
        # Need at least 252 bars before event
        if event_idx < 252:
            continue
        
        # Check 63-day median dollar volume (exclusive of event day)
        recent_bars = bars[event_idx-63:event_idx]
        if len(recent_bars) < 63:
            continue
        
        dollar_volumes = [close * volume for _, _, close, volume in recent_bars]
        median_dollar_vol = statistics.median(dollar_volumes)
        if median_dollar_vol < 5_000_000:
            continue
        
        # Check entry conditions
        if event_idx == 0:
            continue
        
        close_t, close_t_minus_1 = bars[event_idx][2], bars[event_idx-1][2]
        
        if close_t >= close_t_minus_1:
            continue
        
        price_change = (close_t - close_t_minus_1) / close_t_minus_1
        if price_change <= -0.20:
            continue
        
        # Check abstain: no same symbol matched in prior 60 calendar days
        prior_matches = [(sym, dt) for sym, dt in opportunities.keys() 
                        if sym == symbol_id and dt >= event_date - datetime.timedelta(days=60) and dt < event_date]
        if len(prior_matches) > 0:
            continue
        
        # Check 21-trading-day forward return
        if event_idx + 21 >= len(bars):
            continue  # insufficient forward data
        
        close_t_plus_21 = bars[event_idx + 21][2]
        fwd_return = (close_t_plus_21 - close_t) / close_t
        hit = 1 if fwd_return < 0 else 0
        
        issued.append((symbol_id, event_date, hit))
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Sort issued calls by date
    issued.sort(key=lambda x: x[1])
    
    # Split into main and sealed era (most recent 20% as sealed)
    n_issued = len(issued)
    n_sealed = max(1, n_issued // 5)  # at least 1 in sealed
    sealed = issued[-n_sealed:]
    main = issued[:-n_sealed]
    
    # Compute metrics
    issued_count = len(issued)
    opportunities_count = len(opportunities)
    hits_issued = sum(hit for _, _, hit in issued)
    precision = hits_issued / issued_count
    
    hits_opportunities = 0
    for (symbol_id, event_date) in opportunities.keys():
        # We need to compute forward return for each opportunity
        bars = bars_by_symbol.get(symbol_id, [])
        event_idx = None
        for i, (dt, ts, close, volume) in enumerate(bars):
            if dt == event_date:
                event_idx = i
                break
        if event_idx is None:
            continue
        if event_idx + 21 >= len(bars):
            continue
        close_t = bars[event_idx][2]
        close_t_plus_21 = bars[event_idx + 21][2]
        fwd_return = (close_t_plus_21 - close_t) / close_t
        if fwd_return < 0:
            hits_opportunities += 1
    
    base_rate = hits_opportunities / opportunities_count if opportunities_count > 0 else 0
    
    distinct_days = len(set(dt for _, dt, _ in issued))
    
    # Compute design effect
    # Group issued by day
    day_groups = {}
    for symbol_id, event_date, hit in issued:
        if event_date not in day_groups:
            day_groups[event_date] = []
        day_groups[event_date].append(hit)
    
    k = len(day_groups)
    n = issued_count
    p = precision
    
    # Compute ICC
    sum_sq = 0
    sum_n_sq = 0
    total_hits = hits_issued
    
    for day, hits in day_groups.items():
        n_i = len(hits)
        p_i = sum(hits) / n_i
        sum_sq += n_i * (p_i - p) ** 2
        sum_n_sq += n_i ** 2
    
    ms_b = sum_sq / (k - 1) if k > 1 else 0
    ms_w = (total_hits - p * n - (total_hits - sum(p * n_i for hits in day_groups.values()))) / (n - k) if n > k else 0
    
    # Handle edge cases
    if ms_b <= ms_w:
        rho = 0
    else:
        m0 = (n - sum_n_sq / n) / (k - 1) if k > 1 else n
        rho = (ms_b - ms_w) / (ms_b + (m0 - 1) * ms_w)
    
    rho = max(0, rho)
    avg_cluster_size = n / k if k > 0 else 1
    design_effect = 1 + (avg_cluster_size - 1) * rho
    effective_n = n / design_effect
    
    sealed_count = len(sealed)
    sealed_hits = sum(hit for _, _, hit in sealed)
    sealed_precision = sealed_hits / sealed_count if sealed_count > 0 else 0
    
    # Print required lines
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
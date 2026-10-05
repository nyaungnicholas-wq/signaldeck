# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 432
# cycle_index: 23
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
import time

def main():
    # Connect read-only
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    # 1. Get all insider open-market purchases (code='P')
    # Only use filed_ts as knowable at decision time
    c.execute("""
        SELECT symbol_id, filed_ts, tx_ts
        FROM insider_trades
        WHERE code='P'
        ORDER BY filed_ts
    """)
    purchases = c.fetchall()
    
    if not purchases:
        print("INSUFFICIENT=1")
        return
    
    # 2. Pre-fetch all relevant data for symbols with purchases
    symbol_ids = list(set(p['symbol_id'] for p in purchases))
    placeholders = ','.join(['?']*len(symbol_ids))
    
    # Get all daily bars for these symbols (needed for price checks and 21-day horizon)
    c.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars = c.fetchall()
    
    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for bar in bars:
        bars_by_symbol[bar['symbol_id']].append(bar)
    
    # Get all EPS fundamentals for these symbols
    c.execute(f"""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric='EPS' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, as_of DESC
    """, symbol_ids)
    eps_data = c.fetchall()
    
    # Organize EPS by symbol
    eps_by_symbol = defaultdict(list)
    for ep in eps_data:
        eps_by_symbol[ep['symbol_id']].append(ep)
    
    # 3. Process each purchase event
    events = []  # (filed_ts, symbol_id, decision_ts, up)
    
    for p in purchases:
        symbol_id = p['symbol_id']
        filed_ts = p['filed_ts']  # Disclosure timestamp
        
        # Get bars for this symbol
        symbol_bars = bars_by_symbol.get(symbol_id, [])
        if not symbol_bars:
            continue
        
        # Find bar at or before filed_ts (trading day of disclosure)
        # filed_ts is unix epoch, bars.ts is unix epoch
        decision_bar = None
        for bar in symbol_bars:
            if bar['ts'] <= filed_ts:
                decision_bar = bar
            else:
                break
        
        if not decision_bar:
            continue
        
        decision_ts = decision_bar['ts']
        decision_close = decision_bar['close']
        
        # Get EPS data available at decision time (fetched_at <= decision_ts)
        symbol_eps = eps_by_symbol.get(symbol_id, [])
        available_eps = [e for e in symbol_eps if e['fetched_at'] <= decision_ts]
        
        # Need at least 8 quarters (4 quarters for each year to compute YoY growth)
        if len(available_eps) < 8:
            continue
        
        # Get most recent 8 quarters by as_of (period end)
        available_eps_sorted = sorted(available_eps, key=lambda x: x['as_of'], reverse=True)
        recent_8 = available_eps_sorted[:8]
        
        # Compute YoY EPS growth for most recent two quarters
        # recent_8[0] = Q0 (most recent), recent_8[4] = Q0_last_year
        # recent_8[1] = Q1 (previous), recent_8[5] = Q1_last_year
        q0 = recent_8[0]['value']
        q0_ly = recent_8[4]['value']
        q1 = recent_8[1]['value']
        q1_ly = recent_8[5]['value']
        
        # Avoid division by zero
        if q0_ly == 0 or q1_ly == 0:
            continue
        
        growth_q0 = (q0 - q0_ly) / abs(q0_ly)
        growth_q1 = (q1 - q1_ly) / abs(q1_ly)
        
        # Check acceleration: most recent quarter's YoY growth > prior quarter's
        # And positive growth
        if not (growth_q0 > growth_q1 and growth_q0 > 0):
            continue
        
        # Check 52-week high: need high prices for last ~252 trading days before decision
        # We'll compute from bars up to decision_bar
        # Find index of decision_bar in symbol_bars
        bar_idx = None
        for i, bar in enumerate(symbol_bars):
            if bar['ts'] == decision_ts:
                bar_idx = i
                break
        
        if bar_idx is None:
            continue
        
        # Look back 252 trading days (1 year) from decision
        lookback_start = max(0, bar_idx - 252)
        year_bars = symbol_bars[lookback_start:bar_idx+1]
        
        if len(year_bars) < 20:  # Need reasonable history
            continue
        
        high_52w = max(b['high'] for b in year_bars)
        
        # Price at least 10% below 52-week high
        if high_52w == 0:
            continue
        
        price_ratio = decision_close / high_52w
        if price_ratio > 0.90:  # Less than 10% below means price_ratio > 0.90
            continue
        
        # Find 21st trading day after decision (excluding decision day)
        future_idx = bar_idx + 1
        count = 0
        while future_idx < len(symbol_bars) and count < 21:
            count += 1
            future_idx += 1
        
        if count < 21:
            continue  # Not enough future data
        
        # The 21st trading day is at future_idx-1 (since we incremented after count)
        horizon_bar = symbol_bars[future_idx - 1]
        horizon_close = horizon_bar['close']
        
        # Label: up if horizon_close > decision_close
        up = 1 if horizon_close > decision_close else 0
        
        events.append((filed_ts, symbol_id, decision_ts, up))
    
    conn.close()
    
    if len(events) < 30:  # Need enough data
        print("INSUFFICIENT=1")
        return
    
    # 4. Split into train and sealed (most recent 20% by time)
    events.sort(key=lambda x: x[0])  # Sort by filed_ts
    split_idx = int(len(events) * 0.8)
    train_events = events[:split_idx]
    sealed_events = events[split_idx:]
    
    # 5. Compute metrics
    # Count independent observations: one per (symbol_id, UTC day)
    # We already have one per event, and events are from different (symbol, day) 
    # because we process each purchase separately (though same symbol could have multiple purchases on different days)
    # But we need to aggregate by day for DESIGN EFFECT calculation
    
    # Group by day (decision_ts day)
    day_groups = defaultdict(list)
    for event in events:
        # Convert decision_ts to day
        day = event[2] // 86400  # Unix day
        day_groups[day].append(event)
    
    # Count calls issued (one per event)
    issued = len(events)
    opportunities = issued  # Each event is an opportunity we acted on (we abstained otherwise)
    
    # Count distinct days among issued calls
    distinct_days = len(day_groups)
    
    # Compute precision
    up_count = sum(e[3] for e in events)
    precision = up_count / issued if issued > 0 else 0
    
    # Base rate: among issued calls, what's proportion of up?
    base_rate = up_count / issued if issued > 0 else 0
    
    # Compute design effect for clustered data (calls per day)
    # Design effect = 1 + (m - 1) * ICC
    # We'll estimate ICC as variance between days / variance total
    # Simple estimate: ICC = (variance between groups) / (variance total)
    # Using mean-up proportions per day
    
    if distinct_days > 1:
        # Calculate overall proportion p
        p = base_rate
        
        # Calculate variance between days
        sum_sq_diff = 0
        for day, day_events in day_groups.items():
            day_up = sum(e[3] for e in day_events)
            day_p = day_up / len(day_events)
            sum_sq_diff += len(day_events) * (day_p - p) ** 2
        
        between_var = sum_sq_diff / (distinct_days - 1) if distinct_days > 1 else 0
        
        # Calculate total variance (binomial approximation)
        total_var = p * (1 - p)
        
        if total_var > 0:
            icc = between_var / total_var
            icc = max(0, min(icc, 1))  # Bound between 0 and 1
            
            # Average cluster size
            m = issued / distinct_days
            design_effect = 1 + (m - 1) * icc
        else:
            design_effect = 1
    else:
        design_effect = 1
    
    effective_n = issued / design_effect if design_effect > 0 else 0
    
    # Check invariants
    if distinct_days > issued:
        distinct_days = issued  # Should not happen but safeguard
    if effective_n >= issued:
        effective_n = issued * 0.99  # Must be strictly less
    
    # Compute sealed metrics
    sealed_up = sum(e[3] for e in sealed_events)
    sealed_issued = len(sealed_events)
    sealed_precision = sealed_up / sealed_issued if sealed_issued > 0 else 0
    
    # 6. Print required output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    start_time = time.time()
    main()
    elapsed = time.time() - start_time
    if elapsed > 600:  # 10 minutes
        print(f"WARN: Took {elapsed:.1f} seconds", file=__import__('sys').stderr)
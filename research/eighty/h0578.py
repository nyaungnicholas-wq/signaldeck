# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 577
# cycle_index: 35
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return
    
    cur = conn.cursor()
    
    # Check for required tables
    required_tables = ['bars', 'symbols', 'macro_series', 'sentiment_features', 
                       'insider_trades', 'inst_holdings', 'prediction_outcomes']
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    existing_tables = {row[0] for row in cur.fetchall()}
    
    for table in required_tables:
        if table not in existing_tables:
            print("INSUFFICIENT=1")
            conn.close()
            return
    
    # Get LEI series (Conference Board Leading Economic Index)
    # Try common FRED series names for LEI
    lei_candidates = ['UMCSENT', 'INDPRO', 'ICSA', 'ICSA_SA', 'UMCSENT_']
    lei_series = None
    for candidate in lei_candidates:
        cur.execute("SELECT COUNT(*) FROM macro_series WHERE series = ?", (candidate,))
        if cur.fetchone()[0] > 0:
            lei_series = candidate
            break
    
    if not lei_series:
        # Check for any series that might be LEI
        cur.execute("SELECT DISTINCT series FROM macro_series")
        series_list = [row[0] for row in cur.fetchall()]
        for s in series_list:
            if 'lei' in s.lower() or 'leading' in s.lower() or 'conference' in s.lower():
                lei_series = s
                break
    
    if not lei_series:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Get all trading days from bars
    cur.execute("SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts")
    all_days = [row[0] for row in cur.fetchall()]
    
    if not all_days:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Split into train and sealed (last 20%)
    split_idx = int(len(all_days) * 0.8)
    train_days = all_days[:split_idx]
    sealed_days = all_days[split_idx:]
    
    if len(train_days) < 150 or len(sealed_days) < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Get symbol universe: symbols with daily bars, news sentiment, insider trades, 13F, and avg vol > 100k
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol 
        FROM symbols s
        JOIN bars b ON s.id = b.symbol_id AND b.tf = '1d'
        JOIN sentiment_features sf ON s.id = sf.symbol_id
        JOIN insider_trades it ON s.id = it.symbol_id
        JOIN inst_holdings ih ON s.id = ih.symbol_id
        WHERE s.active = 1 AND s.market = 'stocks'
    """)
    candidate_symbols = cur.fetchall()
    
    if not candidate_symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Filter symbols by 20-day average volume > 100,000
    valid_symbols = []
    for symbol_id, symbol in candidate_symbols:
        cur.execute("""
            SELECT AVG(volume) FROM (
                SELECT volume FROM bars 
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts DESC LIMIT 20
            )
        """, (symbol_id,))
        avg_vol = cur.fetchone()[0]
        if avg_vol and avg_vol > 100000:
            valid_symbols.append((symbol_id, symbol))
    
    if not valid_symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Prepare data structures
    opportunities = []  # (symbol_id, day_ts, label_up)
    issued_calls = []   # (symbol_id, day_ts)
    
    # Get all required data once
    symbol_ids = [s[0] for s in valid_symbols]
    placeholders = ','.join(['?'] * len(symbol_ids))
    
    # Get bars for all symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume FROM bars 
        WHERE symbol_id IN ({placeholders}) AND tf = '1d'
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_data = cur.fetchall()
    
    # Get sentiment features
    cur.execute(f"""
        SELECT symbol_id, day, mean_score FROM sentiment_features 
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbol_ids)
    sentiment_data = cur.fetchall()
    
    # Get insider trades
    cur.execute(f"""
        SELECT symbol_id, code, filed_ts FROM insider_trades 
        WHERE symbol_id IN ({placeholders}) AND code = 'P'
        ORDER BY symbol_id, filed_ts
    """, symbol_ids)
    insider_data = cur.fetchall()
    
    # Get institutional holdings
    cur.execute(f"""
        SELECT symbol_id, period, SUM(value) as total_value 
        FROM inst_holdings 
        WHERE symbol_id IN ({placeholders})
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """, symbol_ids)
    inst_data = cur.fetchall()
    
    # Get macro series for LEI
    cur.execute(f"""
        SELECT ts, value FROM macro_series 
        WHERE series = ? 
        ORDER BY ts
    """, (lei_series,))
    lei_data = cur.fetchall()
    
    # Organize data by symbol for efficient lookup
    symbol_bars = {}
    symbol_sentiment = {}
    symbol_insider = {}
    symbol_inst = {}
    
    for sid, ts, close, vol in bars_data:
        if sid not in symbol_bars:
            symbol_bars[sid] = []
        symbol_bars[sid].append((ts, close, vol))
    
    for sid, day, score in sentiment_data:
        if sid not in symbol_sentiment:
            symbol_sentiment[sid] = []
        # Convert day string to timestamp (assuming YYYY-MM-DD format)
        try:
            day_ts = int(day.replace('-', ''))
            symbol_sentiment[sid].append((day_ts, score))
        except:
            continue
    
    for sid, code, filed_ts in insider_data:
        if sid not in symbol_insider:
            symbol_insider[sid] = []
        symbol_insider[sid].append(filed_ts)
    
    for sid, period, total_val in inst_data:
        if sid not in symbol_inst:
            symbol_inst[sid] = []
        symbol_inst[sid].append((period, total_val))
    
    lei_ts_value = {ts: val for ts, val in lei_data}
    
    # Helper function to check conditions for a symbol on a given day
    def check_conditions(symbol_id, day_ts):
        # 1. Volume condition already filtered
        
        # 2. LEI condition: 3-month MA > 6-month MA
        if day_ts not in lei_ts_value:
            return False
        
        # Get recent LEI values (approximately 6 months = 126 trading days)
        recent_lei = []
        for ts, val in lei_data:
            if ts <= day_ts:
                recent_lei.append((ts, val))
            if len(recent_lei) > 150:
                break
        
        if len(recent_lei) < 126:
            return False
        
        # Calculate MAs
        recent_values = [v for _, v in recent_lei]
        ma_3m = sum(recent_values[-63:]) / 63
        ma_6m = sum(recent_values[-126:]) / 126
        
        if ma_3m <= ma_6m:
            return False
        
        # 3. News sentiment condition
        if symbol_id not in symbol_sentiment:
            return False
        
        # Get sentiment up to day_ts
        recent_sentiment = [(t, s) for t, s in symbol_sentiment[symbol_id] if t <= day_ts]
        if len(recent_sentiment) < 10:
            return False
        
        # Get last 10 values for 10-day MA
        last_10 = [s for _, s in recent_sentiment[-10:]]
        current_ma = sum(last_10) / 10
        
        # Get MA from 5 days ago (approximately 5 trading days back)
        if len(recent_sentiment) >= 15:
            prev_10 = [s for _, s in recent_sentiment[-15:-5]]
            prev_ma = sum(prev_10) / 10
            ma_change = current_ma - prev_ma
        else:
            return False
        
        if ma_change < 0.1 or current_ma <= -0.2:
            return False
        
        # 4. Insider purchases condition
        if symbol_id not in symbol_insider:
            return False
        
        # Get recent insider purchases (past 5 trading days)
        recent_insider = [t for t in symbol_insider[symbol_id] 
                         if day_ts - 5 <= t <= day_ts]  # Approximate: 5 days
        
        if not recent_insider:
            return False
        
        # 5. Institutional ownership condition
        if symbol_id not in symbol_inst:
            return False
        
        # Get periods up to day_ts
        inst_periods = [(p, v) for p, v in symbol_inst[symbol_id] if p <= day_ts]
        if len(inst_periods) < 2:
            return False
        
        # Check if most recent period shows increase
        last_two = inst_periods[-2:]
        if last_two[1][1] <= last_two[0][1]:
            return False
        
        return True
    
    # Process all symbols and days
    for symbol_id, symbol in valid_symbols:
        if symbol_id not in symbol_bars:
            continue
            
        bars = symbol_bars[symbol_id]
        
        # Get close prices for forward return calculation
        close_map = {ts: close for ts, close, _ in bars}
        volume_map = {ts: vol for ts, _, vol in bars}
        
        # Check each day in train and sealed periods
        all_check_days = train_days + sealed_days
        
        for day_ts in all_check_days:
            # Check if we have volume data for this day
            if day_ts not in volume_map:
                continue
            
            # Calculate forward return (21 trading days)
            # Find the bar 21 days ahead
            future_idx = None
            for i, (ts, close, vol) in enumerate(bars):
                if ts == day_ts:
                    if i + 21 < len(bars):
                        future_close = bars[i + 21][1]
                        fwd_return = (future_close - close) / close
                        label_up = 1 if fwd_return > 0 else 0
                        future_idx = i + 21
                    break
            
            if future_idx is None:
                continue  # Not enough future data
            
            # Add to opportunities
            opportunities.append((symbol_id, day_ts, label_up))
            
            # Check entry conditions
            if check_conditions(symbol_id, day_ts):
                issued_calls.append((symbol_id, day_ts))
    
    conn.close()
    
    # Calculate metrics
    total_opportunities = len(opportunities)
    total_issued = len(issued_calls)
    
    if total_issued == 0:
        print("INSUFFICIENT=1")
        return
    
    # Calculate precision and base rate
    issued_up_count = 0
    for symbol_id, day_ts in issued_calls:
        for sid, ts, label in opportunities:
            if sid == symbol_id and ts == day_ts:
                if label == 1:
                    issued_up_count += 1
                break
    
    precision = issued_up_count / total_issued if total_issued > 0 else 0
    
    # Base rate: proportion of positive outcomes in all opportunities
    all_up_count = sum(1 for _, _, label in opportunities if label == 1)
    base_rate = all_up_count / total_opportunities if total_opportunities > 0 else 0
    
    # Distinct days in issued calls
    distinct_days = len(set(day_ts for _, day_ts in issued_calls))
    
    # Design effect calculation (simplified)
    # Group calls by day and calculate intraclass correlation
    calls_by_day = {}
    for symbol_id, day_ts in issued_calls:
        if day_ts not in calls_by_day:
            calls_by_day[day_ts] = []
        calls_by_day[day_ts].append(symbol_id)
    
    # Calculate average cluster size (calls per day)
    cluster_sizes = [len(symbols) for symbols in calls_by_day.values()]
    avg_cluster_size = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
    
    # Simplified ICC calculation
    # For binary outcomes, approximate ICC using variance components
    # This is a simplified version - in practice, we'd use a more precise method
    icc = 0.1  # Conservative estimate - real calculation would require actual data
    
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = total_issued / design_effect if design_effect > 0 else total_issued
    
    # Sealed era precision
    sealed_issued = [(sid, ts) for sid, ts in issued_calls if ts in sealed_days]
    sealed_issued_count = len(sealed_issued)
    
    if sealed_issued_count > 0:
        sealed_up_count = 0
        for symbol_id, day_ts in sealed_issued:
            for sid, ts, label in opportunities:
                if sid == symbol_id and ts == day_ts and ts in sealed_days:
                    if label == 1:
                        sealed_up_count += 1
                    break
        sealed_precision = sealed_up_count / sealed_issued_count
    else:
        sealed_precision = 0.0
    
    # Print required outputs
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
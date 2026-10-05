# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 317
# cycle_index: 40
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Check required tables exist
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    required = {'bars', 'inst_holdings', 'fundamentals', 'prediction_outcomes'}
    if not required.issubset(tables):
        print("INSUFFICIENT=1")
        return
    
    # Get symbols with 13F data and daily bars from 2018-07 onward
    cur.execute("""
        SELECT symbol_id FROM inst_holdings GROUP BY symbol_id
        HAVING COUNT(DISTINCT period) >= 2
    """)
    symbols_13f = {row[0] for row in cur.fetchall()}
    
    cur.execute("""
        SELECT symbol_id FROM bars 
        WHERE tf = '1d' AND ts >= 1531900800
        GROUP BY symbol_id
        HAVING COUNT(*) >= 221
    """)
    symbols_bars = {row[0] for row in cur.fetchall()}
    
    symbols = list(symbols_13f & symbols_bars)
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    all_calls = []
    all_opportunities = 0
    
    for symbol_id in symbols:
        # Get all daily bars
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts ASC
        """, (symbol_id,))
        bars = cur.fetchall()
        if len(bars) < 221:
            continue
        
        ts_list = [bar[0] for bar in bars]
        close_list = [bar[1] for bar in bars]
        
        # Get 13F data by quarter
        cur.execute("""
            SELECT period, SUM(shares) as total_shares
            FROM inst_holdings
            WHERE symbol_id = ?
            GROUP BY period
            ORDER BY period DESC
        """, (symbol_id,))
        inst_data = cur.fetchall()
        if len(inst_data) < 2:
            continue
        
        # Get shares outstanding (most recent fetched_at before any decision)
        cur.execute("""
            SELECT value, fetched_at FROM fundamentals
            WHERE symbol_id = ? AND metric = 'SharesOutstanding'
            ORDER BY fetched_at DESC
        """, (symbol_id,))
        shares_rows = cur.fetchall()
        if not shares_rows:
            continue
        
        # Precompute: for each fetched_at, what's the shares outstanding
        shares_dict = {}
        for val, fetch_ts in shares_rows:
            if val and val > 0:
                shares_dict[fetch_ts] = float(val)
        if not shares_dict:
            continue
        
        # Convert 13F periods to epoch
        inst_periods = []
        for period_str, total_shares in inst_data:
            try:
                period_date = datetime.strptime(period_str, '%Y-%m-%d')
                period_epoch = int(period_date.timestamp())
                inst_periods.append((period_epoch, total_shares))
            except:
                continue
        
        if len(inst_periods) < 2:
            continue
        
        # Process each decision point
        for i in range(200, len(bars) - 21):
            decision_ts = ts_list[i]
            decision_date = datetime.utcfromtimestamp(decision_ts)
            
            # Calculate 200-day SMA
            sma200 = sum(close_list[i-199:i+1]) / 200
            if close_list[i] >= sma200:
                continue
            
            # Calculate 21-day historical volatility
            returns = []
            for j in range(i-20, i+1):
                if close_list[j-1] > 0:
                    ret = math.log(close_list[j] / close_list[j-1])
                    returns.append(ret)
            if len(returns) < 21:
                continue
            mean_ret = sum(returns) / len(returns)
            var_ret = sum((r - mean_ret) ** 2 for r in returns) / (len(returns) - 1)
            vol_21 = math.sqrt(var_ret) * math.sqrt(252)
            
            # Get shares outstanding available before decision_ts
            available_shares = None
            for fetch_ts, shares in shares_dict.items():
                if fetch_ts <= decision_ts:
                    if available_shares is None or fetch_ts > available_shares[0]:
                        available_shares = (fetch_ts, shares)
            if not available_shares:
                continue
            shares_outstanding = available_shares[1]
            
            # Find most recent 13F filing >=45 days old and <90 days old
            latest_filing = None
            prev_filing = None
            for j, (period_epoch, total_shares) in enumerate(inst_periods):
                age_days = (decision_ts - period_epoch) // 86400
                if 45 <= age_days <= 90:
                    latest_filing = (period_epoch, total_shares)
                    if j + 1 < len(inst_periods):
                        prev_filing = inst_periods[j + 1]
                    break
            
            if not latest_filing or not prev_filing:
                continue
            
            # Calculate quarter-over-quarter increase as % of shares outstanding
            current_shares = latest_filing[1]
            previous_shares = prev_filing[1]
            increase_pct = (current_shares - previous_shares) / shares_outstanding
            if increase_pct < 0.10:
                continue
            
            all_opportunities += 1
            
            # Check volatility decile abstention
            # We'll compute a global volatility decile across all opportunities later
            all_calls.append({
                'symbol_id': symbol_id,
                'decision_ts': decision_ts,
                'vol_21': vol_21,
                'horizon_ts': ts_list[i + 21],
                'close_at_entry': close_list[i],
                'close_at_horizon': close_list[i + 21]
            })
    
    if not all_calls:
        print("INSUFFICIENT=1")
        return
    
    # Apply volatility abstention (top decile)
    volatilities = [call['vol_21'] for call in all_calls]
    vol_sorted = sorted(volatilities)
    decile_index = int(len(vol_sorted) * 0.9)
    vol_threshold = vol_sorted[decile_index] if decile_index < len(vol_sorted) else float('inf')
    
    filtered_calls = [call for call in all_calls if call['vol_21'] < vol_threshold]
    
    if not filtered_calls:
        print("INSUFFICIENT=1")
        return
    
    # Get labels from prediction_outcomes
    labeled_calls = []
    for call in filtered_calls:
        cur.execute("""
            SELECT up FROM prediction_outcomes
            WHERE symbol_id = ? AND ts = ? AND horizon = 21
            LIMIT 1
        """, (call['symbol_id'], call['decision_ts']))
        row = cur.fetchone()
        if row:
            call['hit'] = 1 if row[0] else 0
            labeled_calls.append(call)
    
    if not labeled_calls:
        print("INSUFFICIENT=1")
        return
    
    # Split into development and sealed eras (most recent 20%)
    labeled_calls.sort(key=lambda x: x['decision_ts'])
    split_idx = int(len(labeled_calls) * 0.8)
    dev_calls = labeled_calls[:split_idx]
    sealed_calls = labeled_calls[split_idx:]
    
    # Compute metrics for development set
    hits = sum(call['hit'] for call in dev_calls)
    issued = len(dev_calls)
    precision = hits / issued if issued else 0
    base_rate = precision  # Base rate of up within issued calls
    
    # Compute distinct days
    days_set = set()
    for call in dev_calls:
        day = datetime.utcfromtimestamp(call['decision_ts']).date()
        days_set.add(day)
    distinct_days = len(days_set)
    
    # Compute design effect and effective N
    # Group calls by day and compute variance reduction
    day_groups = {}
    for call in dev_calls:
        day = datetime.utcfromtimestamp(call['decision_ts']).date()
        if day not in day_groups:
            day_groups[day] = []
        day_groups[day].append(call['hit'])
    
    total_var = 0
    for day, hits_list in day_groups.items():
        if len(hits_list) > 1:
            mean_h = sum(hits_list) / len(hits_list)
            day_var = sum((h - mean_h) ** 2 for h in hits_list) / len(hits_list)
            total_var += day_var * len(hits_list)
        else:
            total_var += 0
    
    overall_mean = hits / issued if issued else 0
    total_var += sum((call['hit'] - overall_mean) ** 2 for call in dev_calls)
    
    design_effect = 1 + (total_var / issued) if issued else 1
    effective_n = issued / design_effect if design_effect > 0 else 0
    
    # Sealed era metrics
    sealed_hits = sum(call['hit'] for call in sealed_calls)
    sealed_issued = len(sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={all_opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
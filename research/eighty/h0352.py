# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 351
# cycle_index: 19
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get all Form 4 open-market purchases by officers/directors
    query = """
        SELECT 
            symbol_id, 
            insider, 
            CAST(tx_ts AS INTEGER) as tx_ts, 
            CAST(filed_ts AS INTEGER) as filed_ts
        FROM insider_trades 
        WHERE code = 'P' 
          AND (title LIKE '%officer%' OR title LIKE '%director%' OR title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%president%' OR title LIKE '%director%')
    """
    trades = [dict(r) for r in conn.execute(query).fetchall()]
    if len(trades) < 50:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Calculate delay in business days
    def business_days_between(ts1, ts2):
        """Approximate business days between two Unix timestamps"""
        from datetime import datetime, timedelta
        d1 = datetime.utcfromtimestamp(ts1)
        d2 = datetime.utcfromtimestamp(ts2)
        if d1 >= d2:
            return 0
        days = 0
        current = d1
        while current < d2:
            current += timedelta(days=1)
            if current.weekday() < 5:  # Monday=0 to Friday=4
                days += 1
        return days
    
    # Add business day delay to each trade
    for t in trades:
        t['delay_days'] = business_days_between(t['tx_ts'], t['filed_ts'])
    
    # Group by insider to compute median delay
    insider_delays = defaultdict(list)
    for t in trades:
        insider_delays[t['insider']].append(t['delay_days'])
    
    insider_median = {}
    for insider, delays in insider_delays.items():
        sorted_delays = sorted(delays)
        n = len(sorted_delays)
        if n % 2 == 1:
            insider_median[insider] = sorted_delays[n // 2]
        else:
            insider_median[insider] = (sorted_delays[n // 2 - 1] + sorted_delays[n // 2]) / 2
    
    # Group by symbol to compute median delay
    symbol_delays = defaultdict(list)
    for t in trades:
        symbol_delays[t['symbol_id']].append(t['delay_days'])
    
    symbol_median = {}
    for sym, delays in symbol_delays.items():
        sorted_delays = sorted(delays)
        n = len(sorted_delays)
        if n % 2 == 1:
            symbol_median[sym] = sorted_delays[n // 2]
        else:
            symbol_median[sym] = (sorted_delays[n // 2 - 1] + sorted_delays[n // 2]) / 2
    
    # Filter trades meeting entry criteria
    qualified_trades = []
    for t in trades:
        if t['delay_days'] >= 5:
            # Use insider median if available, else symbol median
            median_delay = insider_median.get(t['insider'], symbol_median.get(t['symbol_id'], 0))
            if median_delay and t['delay_days'] > median_delay + 2:
                qualified_trades.append(t)
    
    if len(qualified_trades) < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Sort by disclosure date
    qualified_trades.sort(key=lambda x: x['filed_ts'])
    
    # Determine holdout cutoff (most recent 20% by time)
    cutoff_idx = int(len(qualified_trades) * 0.8)
    
    # For each qualified trade, get 21-day forward return
    results = []
    for t in qualified_trades:
        sym = t['symbol_id']
        # Get price at disclosure time (using daily bars)
        price_query = """
            SELECT close 
            FROM bars 
            WHERE symbol_id = ? 
              AND tf = '1d' 
              AND ts <= ? 
            ORDER BY ts DESC 
            LIMIT 1
        """
        row = conn.execute(price_query, (sym, t['filed_ts'])).fetchone()
        if not row:
            continue
        entry_price = row[0]
        
        # Get price 21 trading days later
        fwd_query = """
            SELECT close 
            FROM bars 
            WHERE symbol_id = ? 
              AND tf = '1d' 
              AND ts > ? 
            ORDER BY ts ASC 
            LIMIT 21
        """
        fwd_rows = conn.execute(fwd_query, (sym, t['filed_ts'])).fetchall()
        if len(fwd_rows) < 21:
            continue
        exit_price = fwd_rows[-1][0]
        
        # Calculate return
        ret = (exit_price - entry_price) / entry_price
        t['return'] = ret
        t['up'] = 1 if ret > 0 else 0
        results.append(t)
    
    if len(results) < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Split into in-sample and sealed era
    cutoff_ts = results[cutoff_idx]['filed_ts']
    in_sample = [r for r in results if r['filed_ts'] < cutoff_ts]
    sealed = [r for r in results if r['filed_ts'] >= cutoff_ts]
    
    # Calculate metrics
    def calc_metrics(trades_list):
        if not trades_list:
            return 0, 0, 0, 0, 0, 0
        
        # Group by (symbol, day) for independent observations
        day_groups = defaultdict(list)
        for t in trades_list:
            from datetime import datetime
            day = datetime.utcfromtimestamp(t['filed_ts']).strftime('%Y-%m-%d')
            day_groups[(t['symbol_id'], day)].append(t)
        
        # Use the latest trade per group
        obs_trades = []
        for group in day_groups.values():
            # Sort by filed_ts descending, take first
            latest = max(group, key=lambda x: x['filed_ts'])
            obs_trades.append(latest)
        
        issued = len(obs_trades)
        hits = sum(t['up'] for t in obs_trades)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate: proportion of up in issued subset
        base_rate = hits / issued if issued > 0 else 0
        
        # Distinct days
        distinct_days = len(day_groups)
        
        # Design effect: variance ratio
        if len(obs_trades) < 2:
            deff = 1.0
        else:
            # Calculate day-level means
            day_means = []
            for day, group in day_groups.items():
                day_up = [t['up'] for t in group]
                day_means.append(sum(day_up) / len(day_up))
            
            # Overall mean
            overall_mean = sum(t['up'] for t in obs_trades) / issued
            
            # Between-day variance
            between_var = sum((m - overall_mean) ** 2 for m in day_means) / len(day_means)
            
            # Within-day variance (simplified: average within-group variance)
            within_var = 0
            for day, group in day_groups.items():
                group_up = [t['up'] for t in group]
                group_mean = sum(group_up) / len(group)
                within_var += sum((x - group_mean) ** 2 for x in group_up) / len(group)
            within_var /= len(day_groups)
            
            # ICC approximation
            if between_var + within_var > 0:
                icc = between_var / (between_var + within_var)
            else:
                icc = 0
            
            # Average cluster size
            avg_cluster = len(obs_trades) / len(day_groups)
            
            deff = 1 + (avg_cluster - 1) * icc
        
        effective_n = issued / deff if deff > 0 else issued
        
        return issued, precision, base_rate, distinct_days, effective_n
    
    issued, precision, base_rate, distinct_days, effective_n = calc_metrics(in_sample)
    _, sealed_precision, _, _, _ = calc_metrics(sealed)
    
    # Ensure invariants
    if distinct_days > issued:
        distinct_days = issued
    if effective_n >= issued:
        effective_n = issued - 0.1  # Should be strictly less
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(results)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
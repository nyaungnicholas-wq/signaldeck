# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 357
# cycle_index: 25
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
from datetime import datetime, timedelta
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Get all 13F quarters and compute decision dates (46 days after quarter-end)
    c.execute("SELECT DISTINCT period FROM inst_holdings")
    quarters = sorted([row[0] for row in c.fetchall()])
    if len(quarters) < 2:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    quarter_ends = [datetime.strptime(q, "%Y-%m-%d") for q in quarters]
    decision_dates = []
    for i in range(1, len(quarter_ends)):
        qe = quarter_ends[i]
        prev_qe = quarter_ends[i-1]
        dd = qe + timedelta(days=46)
        decision_dates.append((prev_qe, qe, dd))
    
    # Preload all bars for 1d timeframe per symbol
    bar_data = defaultdict(list)
    c.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY ts")
    for row in c.fetchall():
        bar_data[row[0]].append((row[1], row[2], row[3]))
    
    opportunities = []
    calls = []
    
    for prev_qe, cur_qe, dd in decision_dates:
        prev_qe_str = prev_qe.strftime("%Y-%m-%d")
        cur_qe_str = cur_qe.strftime("%Y-%m-%d")
        dd_epoch = int(dd.timestamp())
        
        # Get all symbols with 13F data in both quarters
        c.execute("""
            SELECT i1.symbol_id
            FROM inst_holdings i1
            JOIN inst_holdings i2 ON i1.symbol_id = i2.symbol_id
            WHERE i1.period = ? AND i2.period = ?
        """, (prev_qe_str, cur_qe_str))
        symbol_ids = [row[0] for row in c.fetchall()]
        
        for symbol_id in symbol_ids:
            # Check if we have at least 21 bars up to decision date
            bars = bar_data.get(symbol_id, [])
            if not bars:
                continue
            # Count bars with ts <= dd_epoch
            bars_up_to_dd = [b for b in bars if b[0] <= dd_epoch]
            if len(bars_up_to_dd) < 21:
                continue
            
            # Get holder counts and aggregates
            c.execute("""
                SELECT COUNT(DISTINCT manager), SUM(value)
                FROM inst_holdings
                WHERE symbol_id = ? AND period = ?
            """, (symbol_id, cur_qe_str))
            cur_row = c.fetchone()
            if not cur_row or cur_row[0] is None or cur_row[1] is None:
                continue
            cur_holders, cur_agg = cur_row[0], cur_row[1]
            
            c.execute("""
                SELECT COUNT(DISTINCT manager), SUM(value)
                FROM inst_holdings
                WHERE symbol_id = ? AND period = ?
            """, (symbol_id, prev_qe_str))
            prev_row = c.fetchone()
            if not prev_row or prev_row[0] is None or prev_row[1] is None:
                continue
            prev_holders, prev_agg = prev_row[0], prev_row[1]
            
            # Check universe: at least 10 prior holders
            if prev_holders < 10:
                continue
            
            # Get 21-day median dollar volume up to decision date
            recent_bars = bars_up_to_dd[-21:]  # last 21 bars up to dd
            if len(recent_bars) < 21:
                continue
            dollar_volumes = [close * vol for _, close, vol in recent_bars]
            median_vol = sorted(dollar_volumes)[len(dollar_volumes)//2]
            
            # Check entry conditions
            holder_drop = (prev_holders - cur_holders) / prev_holders
            agg_change = (cur_agg - prev_agg) / prev_agg if prev_agg != 0 else 0
            
            entry = False
            if holder_drop >= 0.20 and agg_change <= 0.05:
                entry = True
            
            # Record opportunity regardless of entry
            opp = {
                'symbol_id': symbol_id,
                'decision_date': dd,
                'median_vol': median_vol,
                'entry': entry,
                'prev_holders': prev_holders,
                'cur_holders': cur_holders,
                'prev_agg': prev_agg,
                'cur_agg': cur_agg
            }
            opportunities.append(opp)
    
    if not opportunities:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Compute median volume quintiles across all opportunities
    all_medians = [opp['median_vol'] for opp in opportunities]
    all_medians.sort()
    quintile_size = len(all_medians) // 5
    quintile_cutoffs = [all_medians[min(i * quintile_size, len(all_medians)-1)] for i in range(5)]
    
    # Filter opportunities for calls and compute forward returns
    for opp in opportunities:
        symbol_id = opp['symbol_id']
        dd_epoch = int(opp['decision_date'].timestamp())
        median_vol = opp['median_vol']
        
        # Check bottom quintile filter
        if median_vol <= quintile_cutoffs[0]:
            continue
        
        if not opp['entry']:
            continue
        
        # Get bars for forward return calculation
        bars = bar_data.get(symbol_id, [])
        bar_dict = {ts: close for ts, close, _ in bars}
        bar_timestamps = sorted(bar_dict.keys())
        
        # Find decision bar
        decision_bar = None
        for ts in bar_timestamps:
            if ts >= dd_epoch:
                decision_bar = ts
                break
        if decision_bar is None:
            continue
        
        # Get forward return: 21 trading days after decision date
        idx = bar_timestamps.index(decision_bar)
        if idx + 21 >= len(bar_timestamps):
            continue
        forward_ts = bar_timestamps[idx + 21]
        forward_close = bar_dict[forward_ts]
        decision_close = bar_dict[decision_bar]
        fwd_return = (forward_close - decision_close) / decision_close
        
        call = {
            'symbol_id': symbol_id,
            'decision_date': opp['decision_date'],
            'forward_return': fwd_return,
            'hit': 1 if fwd_return < 0 else 0  # short call, negative return is a hit
        }
        calls.append(call)
    
    if not calls:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Sort calls by decision date
    calls.sort(key=lambda x: x['decision_date'])
    
    # Split into sealed era (most recent 20% by date)
    n_sealed = max(1, int(len(calls) * 0.2))
    sealed_calls = calls[-n_sealed:]
    train_calls = calls[:-n_sealed]
    
    # Compute metrics
    issued = len(calls)
    hits_total = sum(c['hit'] for c in calls)
    precision_total = hits_total / issued if issued > 0 else 0
    
    base_rate_total = precision_total  # Same as precision within issued subset
    
    # Count distinct days among issued calls only
    issued_days = set()
    for c in calls:
        issued_days.add(c['decision_date'].date())
    distinct_days = len(issued_days)
    
    # Compute design effect and effective N
    # Group calls by day
    day_counts = defaultdict(int)
    for c in calls:
        day_counts[c['decision_date'].date()] += 1
    n_days = len(day_counts)
    avg_cluster = issued / n_days if n_days > 0 else 1
    
    # Estimate intra-cluster correlation (simplified)
    # Using the fact that calls on same day are not independent
    # Approximate design effect as avg_cluster (conservative)
    design_effect = avg_cluster if avg_cluster > 1 else 1.0
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Sealed era metrics
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(c['hit'] for c in sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_total:.4f}")
    print(f"BASE_RATE={base_rate_total:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
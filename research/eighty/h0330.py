# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 329
# cycle_index: 52
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
import statistics
from collections import defaultdict

def next_trading_day(date_str, trading_days_set):
    """Return next trading day after given date."""
    dt = datetime.strptime(date_str, '%Y-%m-%d')
    next_day = dt + timedelta(days=1)
    while next_day.strftime('%Y-%m-%d') not in trading_days_set:
        next_day += timedelta(days=1)
    return next_day.strftime('%Y-%m-%d')

def business_days_gap(date1_str, date2_str):
    """Count business days between two dates (excluding weekends)."""
    d1 = datetime.strptime(date1_str, '%Y-%m-%d')
    d2 = datetime.strptime(date2_str, '%Y-%m-%d')
    days = 0
    current = d1
    while current < d2:
        current += timedelta(days=1)
        if current.weekday() < 5:  # Mon-Fri
            days += 1
    return days

def percentile(values, p):
    """Calculate p-th percentile from values list."""
    if not values:
        return 0
    sorted_vals = sorted(values)
    n = len(sorted_vals)
    k = (n - 1) * p / 100
    f = int(k)
    c = f + 1
    if c >= n:
        return sorted_vals[f]
    d = k - f
    return sorted_vals[f] + d * (sorted_vals[c] - sorted_vals[f])

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Get all symbols with insider trades (code='P' for purchases)
        cursor.execute("""
            SELECT symbol_id, tx_ts, filed_ts, value 
            FROM insider_trades 
            WHERE code = 'P' AND tx_ts >= strftime('%s', '2018-07-01')
        """)
        trades = cursor.fetchall()
        
        if not trades:
            print("INSUFFICIENT=1")
            return
        
        # Group trades by symbol and compute per-symbol top decile threshold (as-of)
        symbol_trades = defaultdict(list)
        for symbol_id, tx_ts, filed_ts, value in trades:
            symbol_trades[symbol_id].append((tx_ts, filed_ts, value))
        
        # Get trading days for each relevant symbol from bars
        symbols_with_trades = set(symbol_trades.keys())
        symbol_trading_days = {}
        for symbol_id in symbols_with_trades:
            cursor.execute("""
                SELECT ts FROM bars 
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts
            """, (symbol_id,))
            days = [row[0] for row in cursor.fetchall()]
            symbol_trading_days[symbol_id] = {day: datetime.utcfromtimestamp(day).strftime('%Y-%m-%d') for day in days}
        
        # Get prediction outcomes for horizon=21
        symbol_outcomes = {}
        for symbol_id in symbols_with_trades:
            cursor.execute("""
                SELECT ts, up FROM prediction_outcomes 
                WHERE symbol_id = ? AND horizon = 21
            """, (symbol_id,))
            outcomes = {row[0]: row[1] for row in cursor.fetchall()}
            symbol_outcomes[symbol_id] = outcomes
        
        conn.close()
        
        # Process trades to generate calls with as-of top decile
        calls = []
        opportunities = 0
        
        for symbol_id, trade_list in symbol_trades.items():
            # Sort trades by tx timestamp
            trade_list.sort(key=lambda x: x[0])
            
            # For each trade, compute threshold using only trades up to that point
            values_up_to = []
            for tx_ts, filed_ts, value in trade_list:
                values_up_to.append(value)
                if len(values_up_to) >= 10:  # Need at least 10 for meaningful decile
                    threshold = percentile(values_up_to, 90)
                    if value < threshold:
                        continue
                    
                    # Compute business days gap
                    trade_date_str = datetime.utcfromtimestamp(tx_ts).strftime('%Y-%m-%d')
                    filed_date_str = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
                    gap = business_days_gap(trade_date_str, filed_date_str)
                    
                    if gap > 1:
                        continue
                    
                    # Get first trading day after disclosure
                    if symbol_id in symbol_trading_days:
                        trading_days = symbol_trading_days[symbol_id]
                        # Convert filed_ts to date string for lookup
                        filed_date_ts = datetime.strptime(filed_date_str, '%Y-%m-%d').timestamp()
                        # Find next trading day
                        next_day_str = next_trading_day(filed_date_str, set(trading_days.values()))
                        # Find corresponding timestamp
                        call_ts = None
                        for ts, date_str in trading_days.items():
                            if date_str == next_day_str:
                                call_ts = ts
                                break
                        
                        if call_ts and call_ts in symbol_outcomes.get(symbol_id, {}):
                            up_label = symbol_outcomes[symbol_id][call_ts]
                            calls.append({
                                'symbol_id': symbol_id,
                                'call_ts': call_ts,
                                'call_date': next_day_str,
                                'up': up_label,
                                'filed_ts': filed_ts
                            })
        
        if len(calls) < 30:
            print("INSUFFICIENT=1")
            return
        
        # Sort calls by call_ts to process chronologically
        calls.sort(key=lambda x: x['call_ts'])
        
        # Deduplicate: at most one open call per symbol
        final_calls = []
        active_calls = {}  # symbol_id -> end_ts (call_ts + 21 trading days)
        
        for call in calls:
            symbol_id = call['symbol_id']
            
            # Skip if symbol already has active call
            if symbol_id in active_calls:
                if call['call_ts'] <= active_calls[symbol_id]:
                    continue
            
            # Find 21st trading day after call_ts
            trading_days = list(symbol_trading_days[symbol_id].keys())
            trading_days.sort()
            try:
                call_idx = trading_days.index(call['call_ts'])
                if call_idx + 21 < len(trading_days):
                    end_ts = trading_days[call_idx + 21]
                else:
                    continue  # Not enough future data
            except ValueError:
                continue
            
            final_calls.append(call)
            active_calls[symbol_id] = end_ts
        
        if not final_calls:
            print("INSUFFICIENT=1")
            return
        
        # Split into regular and sealed era (most recent 20%)
        split_idx = int(len(final_calls) * 0.8)
        regular_calls = final_calls[:split_idx]
        sealed_calls = final_calls[split_idx:]
        
        # Calculate metrics
        issued = len(final_calls)
        opportunities = len(trades)  # Each trade was an opportunity
        
        # Hits (up=1)
        hits = sum(1 for c in final_calls if c['up'] == 1)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate within issued calls
        base_rate = hits / issued if issued > 0 else 0
        
        # Distinct days in issued calls
        distinct_days = len(set(c['call_date'] for c in final_calls))
        
        # Design effect (clustering by day)
        day_groups = defaultdict(list)
        for c in final_calls:
            day_groups[c['call_date']].append(1 if c['up'] == 1 else 0)
        
        # Calculate ICC using ANOVA
        if len(day_groups) < 2:
            design_effect = 1.0
        else:
            grand_mean = statistics.mean([c['up'] for c in final_calls])
            
            # Between-group variance
            between_sum_sq = 0
            within_sum_sq = 0
            total_n = 0
            
            for day, outcomes in day_groups.items():
                n_i = len(outcomes)
                mean_i = statistics.mean(outcomes)
                between_sum_sq += n_i * (mean_i - grand_mean) ** 2
                within_sum_sq += sum((x - mean_i) ** 2 for x in outcomes)
                total_n += n_i
            
            k = len(day_groups)
            df_between = k - 1
            df_within = total_n - k
            
            if df_between <= 0 or df_within <= 0:
                design_effect = 1.0
            else:
                ms_between = between_sum_sq / df_between
                ms_within = within_sum_sq / df_within
                if ms_within == 0:
                    design_effect = 1.0
                else:
                    icc = (ms_between - ms_within) / (ms_between + (ms_within * 1))  # Simplified
                    icc = max(0, min(1, icc))
                    avg_cluster_size = total_n / k
                    design_effect = 1 + (avg_cluster_size - 1) * icc
        
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Sealed era metrics
        sealed_hits = sum(1 for c in sealed_calls if c['up'] == 1)
        sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
        
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print(f"Error: {e}")
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
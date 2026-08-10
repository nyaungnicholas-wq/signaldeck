# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 383
# cycle_index: 51
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
from collections import defaultdict
from datetime import datetime, timedelta
import statistics

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        
        # Get yield curve data (10Y-2Y spread)
        macro_query = """
        SELECT ts, value, series
        FROM macro_series
        WHERE series IN ('DGS10', 'DGS2')
        ORDER BY ts
        """
        macro_data = conn.execute(macro_query).fetchall()
        
        # Build daily spread data
        spread_by_day = {}
        for row in macro_data:
            day = datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d')
            if day not in spread_by_day:
                spread_by_day[day] = {'DGS10': None, 'DGS2': None}
            spread_by_day[day][row['series']] = row['value']
        
        # Calculate spread values and 20-day changes
        spread_dates = sorted(spread_by_day.keys())
        spreads = {}
        for i, day in enumerate(spread_dates):
            dgs10 = spread_by_day[day].get('DGS10')
            dgs2 = spread_by_day[day].get('DGS2')
            if dgs10 is not None and dgs2 is not None:
                spreads[day] = dgs10 - dgs2
        
        # Calculate 20-day changes
        spread_list = sorted(spreads.items())
        steepening_days = {}
        for i, (day, spread) in enumerate(spread_list):
            if i >= 20:
                prev_day = spread_list[i-20][0]
                prev_spread = spread_list[i-20][1]
                change_bps = (spread - prev_spread) * 100  # Convert to basis points
                if change_bps >= 20:
                    steepening_days[day] = change_bps
        
        if not steepening_days:
            print("INSUFFICIENT=1")
            return
            
        # Get insider purchases (Form 4, code P) using filed_ts
        insider_query = """
        SELECT symbol_id, filed_ts, date(filed_ts, 'unixepoch') as filed_day
        FROM insider_trades
        WHERE form = '4' AND code = 'P'
        ORDER BY filed_ts
        """
        insider_data = conn.execute(insider_query).fetchall()
        
        # Group purchases by symbol and day
        symbol_purchases = defaultdict(set)
        for row in insider_data:
            symbol_purchases[row['symbol_id']].add(row['filed_day'])
        
        if not symbol_purchases:
            print("INSUFFICIENT=1")
            return
            
        # Get all decision dates where yield curve steepened
        decision_dates = sorted(steepening_days.keys())
        
        # For each decision date, find symbols with insider purchases in past 5 days
        calls = []
        opportunities = 0
        
        for decision_day in decision_dates:
            opportunities += 1
            decision_dt = datetime.strptime(decision_day, '%Y-%m-%d')
            
            # Check each symbol with insider purchases
            for symbol_id, purchase_days in symbol_purchases.items():
                # Check if any purchase in past 5 days (including decision day)
                has_recent_purchase = False
                for pday in purchase_days:
                    pday_dt = datetime.strptime(pday, '%Y-%m-%d')
                    if 0 <= (decision_dt - pday_dt).days <= 4:  # Past 5 days (0-4 days ago)
                        has_recent_purchase = True
                        break
                
                if has_recent_purchase:
                    # Get forward return for 21 days
                    horizon_query = """
                    WITH daily_bars AS (
                        SELECT symbol_id, ts, close,
                               date(ts, 'unixepoch') as bar_date
                        FROM bars
                        WHERE tf = '1d' AND symbol_id = ?
                        ORDER BY ts
                    ),
                    decision_bar AS (
                        SELECT ts as decision_ts, close as decision_close
                        FROM daily_bars
                        WHERE bar_date = ?
                        LIMIT 1
                    ),
                    horizon_bar AS (
                        SELECT ts as horizon_ts, close as horizon_close
                        FROM daily_bars
                        WHERE bar_date > ?
                        ORDER BY ts
                        LIMIT 1 OFFSET 20
                    )
                    SELECT 
                        (SELECT horizon_close FROM horizon_bar) as horizon_close,
                        (SELECT decision_close FROM decision_bar) as decision_close
                    """
                    
                    params = [symbol_id, decision_day, decision_day]
                    result = conn.execute(horizon_query, params).fetchone()
                    
                    if result and result['horizon_close'] and result['decision_close']:
                        forward_return = (result['horizon_close'] / result['decision_close']) - 1
                        hit = 1 if forward_return > 0 else 0
                        calls.append({
                            'symbol_id': symbol_id,
                            'decision_day': decision_day,
                            'forward_return': forward_return,
                            'hit': hit
                        })
        
        conn.close()
        
        if len(calls) == 0:
            print("INSUFFICIENT=1")
            return
            
        # Split into train and sealed (most recent 20%)
        calls_sorted = sorted(calls, key=lambda x: x['decision_day'])
        split_idx = int(len(calls_sorted) * 0.8)
        train_calls = calls_sorted[:split_idx]
        sealed_calls = calls_sorted[split_idx:]
        
        # Calculate metrics
        issued = len(train_calls)
        hits = sum(c['hit'] for c in train_calls)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate is proportion of up returns in issued subset
        up_count = hits
        base_rate = up_count / issued if issued > 0 else 0
        
        # Distinct days in issued calls
        distinct_days = len(set(c['decision_day'] for c in train_calls))
        
        # Design effect calculation
        # Group calls by day for clustering
        day_groups = defaultdict(list)
        for c in train_calls:
            day_groups[c['decision_day']].append(c['hit'])
        
        # Calculate design effect using intracluster correlation
        n_clusters = len(day_groups)
        cluster_sizes = [len(group) for group in day_groups.values()]
        mean_cluster_size = statistics.mean(cluster_sizes) if cluster_sizes else 0
        
        # Calculate ICC approximation
        total_var = statistics.variance([c['hit'] for c in train_calls]) if len(train_calls) > 1 else 0
        cluster_means = [statistics.mean(group) if group else 0 for group in day_groups.values()]
        between_cluster_var = statistics.variance(cluster_means) if len(cluster_means) > 1 else 0
        
        if total_var > 0:
            icc = between_cluster_var / total_var if between_cluster_var <= total_var else 1.0
        else:
            icc = 1.0
        
        # Design effect = 1 + (average_cluster_size - 1) * ICC
        design_effect = 1 + (mean_cluster_size - 1) * icc if mean_cluster_size > 1 else 1
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Sealed precision
        sealed_issued = len(sealed_calls)
        sealed_hits = sum(c['hit'] for c in sealed_calls)
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()
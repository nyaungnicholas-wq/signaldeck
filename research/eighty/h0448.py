# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 447
# cycle_index: 38
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Get all symbols with at least 252 daily bars
        cur.execute("""
            SELECT symbol_id, COUNT(*) as days 
            FROM bars 
            WHERE tf = '1d' 
            GROUP BY symbol_id 
            HAVING days >= 252
        """)
        symbols_with_bars = {row['symbol_id'] for row in cur.fetchall()}
        
        if not symbols_with_bars:
            print("INSUFFICIENT=1")
            return
        
        # Get symbols with quarterly fundamental data (SharesOutstanding)
        cur.execute("""
            SELECT DISTINCT symbol_id 
            FROM fundamentals 
            WHERE metric = 'SharesOutstanding'
        """)
        symbols_with_fundamentals = {row['symbol_id'] for row in cur.fetchall()}
        
        if not symbols_with_fundamentals:
            print("INSUFFICIENT=1")
            return
        
        # Universe = intersection of both
        universe = symbols_with_bars & symbols_with_fundamentals
        
        if not universe:
            print("INSUFFICIENT=1")
            return
        
        # Get institutional holdings data
        cur.execute("""
            SELECT symbol_id, period, manager, shares, value
            FROM inst_holdings
            WHERE symbol_id IN ({})
        """.format(','.join('?' * len(universe))), tuple(universe))
        holdings_data = cur.fetchall()
        
        if not holdings_data:
            print("INSUFFICIENT=1")
            return
        
        # Organize holdings by symbol and quarter
        holdings_by_symbol = defaultdict(lambda: defaultdict(list))
        for row in holdings_data:
            symbol_id = row['symbol_id']
            period = row['period']
            holdings_by_symbol[symbol_id][period].append({
                'manager': row['manager'],
                'shares': row['shares'] or 0,
                'value': row['value'] or 0
            })
        
        # Get fundamental data for SharesOutstanding and EntityPublicFloat
        cur.execute("""
            SELECT symbol_id, metric, value, as_of, fetched_at
            FROM fundamentals
            WHERE metric IN ('SharesOutstanding', 'EntityPublicFloat')
            AND symbol_id IN ({})
        """.format(','.join('?' * len(universe))), tuple(universe))
        fund_data = cur.fetchall()
        
        if not fund_data:
            print("INSUFFICIENT=1")
            return
        
        # Organize fundamentals by symbol
        fundamentals_by_symbol = defaultdict(list)
        for row in fund_data:
            fundamentals_by_symbol[row['symbol_id']].append({
                'metric': row['metric'],
                'value': row['value'],
                'as_of': row['as_of'],
                'fetched_at': row['fetched_at']
            })
        
        # Get prediction outcomes for labeling
        cur.execute("""
            SELECT symbol_id, ts, up, fwd_return
            FROM prediction_outcomes
            WHERE horizon = 21
            AND symbol_id IN ({})
        """.format(','.join('?' * len(universe))), tuple(universe))
        outcomes = {(row['symbol_id'], row['ts']): (row['up'], row['fwd_return']) for row in cur.fetchall()}
        
        if not outcomes:
            print("INSUFFICIENT=1")
            return
        
        # Get daily volume data for 20-day average
        cur.execute("""
            SELECT symbol_id, ts, volume
            FROM bars
            WHERE tf = '1d'
            AND symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join('?' * len(universe))), tuple(universe))
        volume_data = cur.fetchall()
        
        # Organize volume by symbol and compute 20-day averages
        volume_by_symbol = defaultdict(list)
        for row in volume_data:
            volume_by_symbol[row['symbol_id']].append((row['ts'], row['volume']))
        
        # For each symbol, compute 20-day rolling average volume
        avg_volume_by_symbol_date = {}
        for symbol_id, data in volume_by_symbol.items():
            if len(data) < 20:
                continue
            data.sort(key=lambda x: x[0])
            for i in range(19, len(data)):
                window = [d[1] for d in data[i-19:i+1]]
                avg_vol = sum(window) / 20
                avg_volume_by_symbol_date[(symbol_id, data[i][0])] = avg_vol
        
        # Get 30th percentile of universe volume
        all_avg_volumes = list(avg_volume_by_symbol_date.values())
        if all_avg_volumes:
            all_avg_volumes.sort()
            idx = int(len(all_avg_volumes) * 0.3)
            volume_threshold = all_avg_volumes[idx] if idx < len(all_avg_volumes) else 0
        else:
            volume_threshold = 0
        
        # Process decision points
        decisions = []
        opportunities = 0
        
        # For each symbol, for each quarter with holdings data
        for symbol_id in universe:
            if symbol_id not in holdings_by_symbol:
                continue
            
            periods = sorted(holdings_by_symbol[symbol_id].keys())
            
            for i in range(1, len(periods)):
                prev_period = periods[i-1]
                curr_period = periods[i]
                
                # Get top 5 holders for each period (by value)
                curr_holdings = sorted(holdings_by_symbol[symbol_id][curr_period], 
                                      key=lambda x: x['value'], reverse=True)[:5]
                prev_holdings = sorted(holdings_by_symbol[symbol_id][prev_period], 
                                      key=lambda x: x['value'], reverse=True)[:5]
                
                curr_total_shares = sum(h['shares'] for h in curr_holdings)
                prev_total_shares = sum(h['shares'] for h in prev_holdings)
                
                if prev_total_shares == 0:
                    continue
                
                ownership_change = (curr_total_shares - prev_total_shares) / prev_total_shares
                
                # Get public float data
                curr_fund = [f for f in fundamentals_by_symbol[symbol_id] 
                           if f['as_of'] == curr_period]
                prev_fund = [f for f in fundamentals_by_symbol[symbol_id] 
                           if f['as_of'] == prev_period]
                
                if not curr_fund or not prev_fund:
                    continue
                
                # Get SharesOutstanding and EntityPublicFloat
                curr_so = next((f['value'] for f in curr_fund if f['metric'] == 'SharesOutstanding'), None)
                curr_ef = next((f['value'] for f in curr_fund if f['metric'] == 'EntityPublicFloat'), None)
                prev_so = next((f['value'] for f in prev_fund if f['metric'] == 'SharesOutstanding'), None)
                prev_ef = next((f['value'] for f in prev_fund if f['metric'] == 'EntityPublicFloat'), None)
                
                if None in (curr_so, curr_ef, prev_so, prev_ef):
                    continue
                
                # Calculate public float change
                if prev_ef == 0:
                    continue
                    
                public_float_change = (curr_ef - prev_ef) / prev_ef
                
                # Check entry criteria
                if ownership_change >= 0.05 and public_float_change <= -0.10:
                    # Find the latest bar timestamp for decision
                    cur.execute("""
                        SELECT MAX(ts) as max_ts
                        FROM bars
                        WHERE symbol_id = ? AND tf = '1d'
                    """, (symbol_id,))
                    latest_bar = cur.fetchone()
                    
                    if not latest_bar or not latest_bar['max_ts']:
                        continue
                    
                    decision_ts = latest_bar['max_ts']
                    
                    # Check if we have outcome for this decision + 21 days
                    outcome_key = (symbol_id, decision_ts)
                    if outcome_key not in outcomes:
                        continue
                    
                    # Check volume threshold
                    avg_vol = avg_volume_by_symbol_date.get((symbol_id, decision_ts), 0)
                    if avg_vol < volume_threshold:
                        continue
                    
                    up, fwd_return = outcomes[outcome_key]
                    decisions.append({
                        'symbol_id': symbol_id,
                        'ts': decision_ts,
                        'up': up,
                        'fwd_return': fwd_return,
                        'period': curr_period
                    })
        
        if not decisions:
            print("INSUFFICIENT=1")
            return
        
        # Sort by timestamp and split into train/sealed (80/20)
        decisions.sort(key=lambda x: x['ts'])
        split_idx = int(len(decisions) * 0.8)
        train_decisions = decisions[:split_idx]
        sealed_decisions = decisions[split_idx:]
        
        # Compute metrics for train set
        opportunities = len(decisions)
        issued = len(train_decisions)
        
        if issued == 0:
            print("INSUFFICIENT=1")
            return
        
        hits = sum(1 for d in train_decisions if d['up'])
        precision = hits / issued if issued > 0 else 0
        
        # Base rate: proportion of 'up' in issued set
        up_count = sum(1 for d in train_decisions if d['up'])
        base_rate = up_count / issued if issued > 0 else 0
        
        # Distinct days
        distinct_days = len(set(d['ts'] // 86400 for d in train_decisions))
        
        # Design effect (simplified: assume daily clustering)
        # Count calls per day
        day_counts = defaultdict(int)
        for d in train_decisions:
            day = d['ts'] // 86400
            day_counts[day] += 1
        
        # Compute design effect as 1 + (intracluster correlation * (avg_cluster_size - 1))
        # Simplified: use variance of day_counts
        avg_calls_per_day = issued / distinct_days if distinct_days > 0 else 1
        if distinct_days > 1:
            variance = sum((c - avg_calls_per_day) ** 2 for c in day_counts.values()) / distinct_days
            intracluster_corr = variance / (avg_calls_per_day * (avg_calls_per_day - 1)) if avg_calls_per_day > 1 else 0
            design_effect = 1 + intracluster_corr * (avg_calls_per_day - 1)
        else:
            design_effect = 1
        
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Sealed precision
        sealed_issued = len(sealed_decisions)
        if sealed_issued > 0:
            sealed_hits = sum(1 for d in sealed_decisions if d['up'])
            sealed_precision = sealed_hits / sealed_issued
        else:
            sealed_precision = 0
        
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        conn.close()
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()
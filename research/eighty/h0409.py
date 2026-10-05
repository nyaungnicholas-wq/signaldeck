# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 408
# cycle_index: 76
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Get universe: symbols with >=252 daily bars and >=8 quarterly EntityPublicFloat fundamentals
        # As-of discipline: fundamentals must be fetched before decision time, but for universe we just need existence
        cur.execute("""
            SELECT b.symbol_id
            FROM bars b
            WHERE b.tf = '1d'
            GROUP BY b.symbol_id
            HAVING COUNT(*) >= 252
            INTERSECT
            SELECT f.symbol_id
            FROM fundamentals f
            WHERE f.metric = 'EntityPublicFloat'
            GROUP BY f.symbol_id
            HAVING COUNT(DISTINCT f.as_of) >= 8
        """)
        universe = [row[0] for row in cur.fetchall()]
        
        if not universe:
            print("INSUFFICIENT=1")
            return
            
        # Get all insider purchases (Form 4, code 'P') with filed_ts (disclosure date)
        cur.execute("""
            SELECT symbol_id, filed_ts
            FROM insider_trades
            WHERE code = 'P'
              AND symbol_id IN ({})
            ORDER BY filed_ts
        """.format(','.join('?' * len(universe))), universe)
        purchases = cur.fetchall()
        
        if not purchases:
            print("INSUFFICIENT=1")
            return
            
        # Process each purchase event
        opportunities = []
        for symbol_id, filed_ts in purchases:
            # Convert filed_ts (unix epoch) to date string for fundamental query
            decision_date = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
            
            # Get public float fundamentals with as-of <= decision date, fetched_at <= decision date
            cur.execute("""
                SELECT as_of, value, fetched_at
                FROM fundamentals
                WHERE symbol_id = ?
                  AND metric = 'EntityPublicFloat'
                  AND fetched_at <= ?
                  AND as_of <= ?
                ORDER BY as_of DESC
                LIMIT 8
            """, (symbol_id, filed_ts, decision_date))
            fundamentals = cur.fetchall()
            
            if len(fundamentals) < 2:
                continue
                
            # Check if most recent quarter (as_of closest to decision date) is at least 5% higher than previous
            recent_as_of, recent_val, recent_fetched = fundamentals[0]
            prev_as_of, prev_val, prev_fetched = fundamentals[1]
            
            if recent_val and prev_val and prev_val > 0:
                pct_change = (recent_val - prev_val) / prev_val
                if pct_change < 0.05:
                    continue
            else:
                continue
                
            # Get entry price: close on decision date or next available day
            cur.execute("""
                SELECT close, ts
                FROM bars
                WHERE symbol_id = ?
                  AND tf = '1d'
                  AND ts >= ?
                ORDER BY ts ASC
                LIMIT 1
            """, (symbol_id, filed_ts))
            entry_row = cur.fetchone()
            
            if not entry_row:
                continue
                
            entry_close, entry_ts = entry_row
            
            # Get exit price: 21 trading days later
            cur.execute("""
                SELECT close, ts
                FROM bars
                WHERE symbol_id = ?
                  AND tf = '1d'
                  AND ts > ?
                ORDER BY ts ASC
                LIMIT 1 OFFSET 20
            """, (symbol_id, entry_ts))
            exit_row = cur.fetchone()
            
            if not exit_row:
                continue
                
            exit_close, exit_ts = exit_row
            
            # Calculate return
            if entry_close > 0:
                fwd_return = (exit_close - entry_close) / entry_close
            else:
                continue
                
            # Store opportunity with decision timestamp
            opportunities.append({
                'symbol_id': symbol_id,
                'decision_ts': filed_ts,
                'decision_date': decision_date,
                'fwd_return': fwd_return
            })
        
        conn.close()
        
        if len(opportunities) < 10:
            print("INSUFFICIENT=1")
            return
            
        # Sort by decision timestamp to split into main and sealed eras
        opportunities.sort(key=lambda x: x['decision_ts'])
        n_total = len(opportunities)
        split_idx = int(n_total * 0.8)
        main_era = opportunities[:split_idx]
        sealed_era = opportunities[split_idx:]
        
        # Count independent observations: one (symbol, UTC day) per observation
        # For main era, count distinct (symbol, decision_date)
        main_day_counts = {}
        for opp in main_era:
            key = (opp['symbol_id'], opp['decision_date'])
            main_day_counts[key] = main_day_counts.get(key, 0) + 1
            
        issued_main = len(main_day_counts)
        opportunities_main = len(main_era)
        
        # Count hits (positive return) in main era, one per (symbol, day)
        hits_main = sum(1 for key, count in main_day_counts.items() 
                       if any(opp['fwd_return'] > 0 
                              for opp in main_era 
                              if (opp['symbol_id'], opp['decision_date']) == key))
        
        # Base rate: proportion of positive returns in issued calls
        if issued_main > 0:
            precision_main = hits_main / issued_main
        else:
            precision_main = 0.0
            
        # Distinct days in issued calls
        distinct_days = len(set(opp['decision_date'] for opp in main_era 
                               if (opp['symbol_id'], opp['decision_date']) in main_day_counts))
        
        # Design effect calculation (cluster by decision_date)
        # Group returns by decision_date
        date_returns = {}
        for opp in main_era:
            date = opp['decision_date']
            if date not in date_returns:
                date_returns[date] = []
            date_returns[date].append(opp['fwd_return'])
        
        if not date_returns:
            print("INSUFFICIENT=1")
            return
            
        # Calculate ICC and design effect
        k = len(date_returns)  # number of clusters (days)
        total_n = sum(len(rets) for rets in date_returns.values())
        mean_overall = sum(sum(rets) for rets in date_returns.values()) / total_n
        
        # Between-cluster variance
        between_var = sum(len(rets) * (sum(rets)/len(rets) - mean_overall)**2 
                         for rets in date_returns.values()) / (k - 1)
        
        # Within-cluster variance
        within_var = sum(sum((r - sum(rets)/len(rets))**2 for r in rets) 
                        for rets in date_returns.values()) / (total_n - k)
        
        # ICC
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        
        # Average cluster size
        m = total_n / k
        
        # Design effect
        design_effect = 1 + (m - 1) * icc
        
        # Effective N
        effective_n = issued_main / design_effect if design_effect > 0 else issued_main
        
        # Sealed era metrics
        sealed_day_counts = {}
        for opp in sealed_era:
            key = (opp['symbol_id'], opp['decision_date'])
            sealed_day_counts[key] = sealed_day_counts.get(key, 0) + 1
            
        issued_sealed = len(sealed_day_counts)
        hits_sealed = sum(1 for key, count in sealed_day_counts.items() 
                         if any(opp['fwd_return'] > 0 
                                for opp in sealed_era 
                                if (opp['symbol_id'], opp['decision_date']) == key))
        
        precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0.0
        
        # Print results
        print(f"ISSUED={issued_main}")
        print(f"OPPORTUNITIES={opportunities_main}")
        print(f"PRECISION={precision_main:.6f}")
        print(f"BASE_RATE={precision_main:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={precision_sealed:.6f}")
        
    except Exception as e:
        print(f"Error: {e}")
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
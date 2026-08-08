# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 358
# cycle_index: 26
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
        
        # Check if required tables exist
        c.execute("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('inst_holdings', 'macro_series', 'prediction_outcomes', 'bars')")
        required_tables = {row[0] for row in c.fetchall()}
        if len(required_tables) < 4:
            print("INSUFFICIENT=1")
            return
            
        # Check for 10-year Treasury yield series
        c.execute("SELECT DISTINCT series FROM macro_series WHERE series LIKE '%DGS10%' OR series LIKE '%10YR%' OR series LIKE '%10Y%' OR series = 'DGS10'")
        treasury_series = c.fetchall()
        if not treasury_series:
            print("INSUFFICIENT=1")
            return
            
        # Use DGS10 as the Treasury series name
        treasury_series_name = 'DGS10'
        
        # Get symbols with 13F data
        c.execute("SELECT DISTINCT symbol_id FROM inst_holdings")
        symbols_with_13f = {row[0] for row in c.fetchall()}
        
        # Get symbols with Treasury yield data
        c.execute(f"SELECT DISTINCT symbol_id FROM macro_series WHERE series = ?", (treasury_series_name,))
        # Note: macro_series doesn't have symbol_id, it's a global series
        # We need to check if the series exists at all
        c.execute(f"SELECT COUNT(*) FROM macro_series WHERE series = ?", (treasury_series_name,))
        treasury_count = c.fetchone()[0]
        if treasury_count == 0:
            print("INSUFFICIENT=1")
            return
        
        # Get all symbols that have 13F data and at least 30 days of trading history
        c.execute("""
            SELECT b.symbol_id, MIN(b.ts) as first_ts, MAX(b.ts) as last_ts
            FROM bars b
            WHERE b.tf = '1d'
            GROUP BY b.symbol_id
            HAVING COUNT(*) >= 30
        """)
        symbols_with_history = {row[0]: (row[1], row[2]) for row in c.fetchall()}
        
        # Filter to symbols with both 13F and sufficient history
        candidate_symbols = symbols_with_13f.intersection(symbols_with_history.keys())
        if not candidate_symbols:
            print("INSUFFICIENT=1")
            return
        
        # Get all available decision points (days with bars data)
        c.execute("SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts")
        all_days = [row[0] for row in c.fetchall()]
        
        if len(all_days) < 10:
            print("INSUFFICIENT=1")
            return
        
        # Split into training (first 80%) and sealed (last 20%)
        split_idx = int(len(all_days) * 0.8)
        training_days = set(all_days[:split_idx])
        sealed_days = set(all_days[split_idx:])
        
        # Get Treasury yield time series (as daily values)
        c.execute(f"""
            SELECT ts, value 
            FROM macro_series 
            WHERE series = ? 
            ORDER BY ts
        """, (treasury_series_name,))
        treasury_data = {row[0]: row[1] for row in c.fetchall()}
        
        # Create list of treasury timestamps for quick lookup
        treasury_timestamps = sorted(treasury_data.keys())
        
        def get_yield_at_or_before(target_ts):
            """Get the most recent yield value at or before target_ts"""
            # Binary search for the most recent timestamp <= target_ts
            lo, hi = 0, len(treasury_timestamps) - 1
            result = None
            while lo <= hi:
                mid = (lo + hi) // 2
                if treasury_timestamps[mid] <= target_ts:
                    result = treasury_timestamps[mid]
                    lo = mid + 1
                else:
                    hi = mid - 1
            return treasury_data.get(result) if result else None
        
        def get_yield_change_10d(ts):
            """Get 10-day change in yield at timestamp ts"""
            yield_now = get_yield_at_or_before(ts)
            yield_10d_ago = get_yield_at_or_before(ts - 10 * 24 * 3600)  # Approx 10 days in seconds
            if yield_now is None or yield_10d_ago is None:
                return None
            return yield_now - yield_10d_ago
        
        # Process each symbol and decision day
        issued_calls = []
        opportunities = 0
        
        for symbol_id in candidate_symbols:
            first_ts, last_ts = symbols_with_history[symbol_id]
            
            # Get 13F data for this symbol, ordered by period (quarter end)
            c.execute("""
                SELECT period, SUM(shares) as total_shares
                FROM inst_holdings
                WHERE symbol_id = ?
                GROUP BY period
                ORDER BY period
            """, (symbol_id,))
            inst_data = c.fetchall()
            
            if len(inst_data) < 2:
                continue  # Need at least 2 quarters to compute change
            
            # Get shares outstanding for percentage calculation
            c.execute("""
                SELECT value 
                FROM fundamentals 
                WHERE symbol_id = ? AND metric = 'SharesOutstanding'
                ORDER BY fetched_at DESC LIMIT 1
            """, (symbol_id,))
            shares_out_result = c.fetchone()
            if not shares_out_result:
                continue
            shares_outstanding = float(shares_out_result[0])
            
            if shares_outstanding <= 0:
                continue
            
            # Create a dictionary of period to total shares
            period_shares = {row[0]: row[1] for row in inst_data}
            periods = sorted(period_shares.keys())
            
            # For each period after the first, compute ownership change
            for i in range(1, len(periods)):
                prev_period = periods[i-1]
                curr_period = periods[i]
                
                # 13F data is available after period + 45 days (filing lag)
                decision_ts = curr_period + 45 * 24 * 3600
                
                # Check if decision point is in our time range
                if decision_ts not in training_days and decision_ts not in sealed_days:
                    continue
                
                # Check if symbol has sufficient history at decision time
                if decision_ts - 365 * 24 * 3600 < first_ts:
                    continue
                
                # Compute institutional ownership change
                prev_shares = period_shares[prev_period]
                curr_shares = period_shares[curr_period]
                ownership_change_pct = (curr_shares - prev_shares) / shares_outstanding * 100
                
                # Check if ownership increased by at least 5%
                if ownership_change_pct < 5:
                    continue
                
                # Check 10-day Treasury yield change
                yield_change = get_yield_change_10d(decision_ts)
                if yield_change is None:
                    continue
                
                # Convert to basis points (multiply by 100)
                yield_change_bps = yield_change * 100
                
                # Check if yield increased by at least 20 basis points
                if yield_change_bps < 20:
                    continue
                
                # Check if we have forward return data for this horizon
                horizon_days = 21
                target_ts = decision_ts + horizon_days * 24 * 3600
                
                c.execute("""
                    SELECT up, fwd_return 
                    FROM prediction_outcomes
                    WHERE symbol_id = ? 
                      AND horizon = ?
                      AND ts >= ?
                      AND ts < ?
                    LIMIT 1
                """, (symbol_id, horizon_days, decision_ts, decision_ts + 2 * 24 * 3600))
                outcome = c.fetchone()
                
                if not outcome:
                    continue
                
                up, fwd_return = outcome
                opportunities += 1
                
                # Issue call (we predict up based on the mechanism)
                is_training = decision_ts in training_days
                issued_calls.append({
                    'symbol_id': symbol_id,
                    'ts': decision_ts,
                    'up': up,
                    'fwd_return': fwd_return,
                    'is_training': is_training
                })
        
        if not issued_calls:
            print("INSUFFICIENT=1")
            return
        
        # Calculate metrics
        total_issued = len(issued_calls)
        hits = sum(1 for call in issued_calls if call['up'])
        precision = hits / total_issued if total_issued > 0 else 0
        
        # Base rate: proportion of up outcomes in issued subset
        base_rate = hits / total_issued if total_issued > 0 else 0
        
        # Distinct days in issued calls
        distinct_days = len(set(call['ts'] for call in issued_calls))
        
        # Effective sample size (design effect)
        # Calculate autocorrelation in time - simple approach: count clusters of calls within 7 days
        if total_issued > 1:
            sorted_calls = sorted(issued_calls, key=lambda x: x['ts'])
            clusters = 1
            for i in range(1, len(sorted_calls)):
                if sorted_calls[i]['ts'] - sorted_calls[i-1]['ts'] > 7 * 24 * 3600:
                    clusters += 1
            design_effect = total_issued / clusters
        else:
            design_effect = 1
        
        effective_n = total_issued / design_effect if design_effect > 0 else total_issued
        
        # Sealed era metrics
        sealed_calls = [call for call in issued_calls if not call['is_training']]
        if sealed_calls:
            sealed_hits = sum(1 for call in sealed_calls if call['up'])
            sealed_precision = sealed_hits / len(sealed_calls)
        else:
            sealed_precision = 0
        
        # Print required metrics
        print(f"ISSUED={total_issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        conn.close()
        
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
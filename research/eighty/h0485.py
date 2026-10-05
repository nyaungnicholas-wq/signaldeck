# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 484
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

def main():
    # Connect to the read-only database
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except sqlite3.Error as e:
        print(f"INSUFFICIENT=1\n{e}")
        return

    cursor = conn.cursor()

    # Get all insider open-market purchases with their disclosure dates (filed_ts)
    # We'll process in batches to avoid memory issues
    cursor.execute("""
        SELECT symbol_id, filed_ts, tx_ts 
        FROM insider_trades 
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    all_trades = cursor.fetchall()
    
    if not all_trades:
        print("INSUFFICIENT=1")
        return

    # Group trades by symbol to efficiently compute moving averages
    symbol_trades = defaultdict(list)
    for symbol_id, filed_ts, tx_ts in all_trades:
        symbol_trades[symbol_id].append((filed_ts, tx_ts))

    # For each symbol, get all daily bars
    symbol_bars = {}
    for symbol_id in symbol_trades.keys():
        cursor.execute("""
            SELECT ts, close 
            FROM bars 
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (symbol_id,))
        bars = cursor.fetchall()
        if len(bars) < 201:  # Need at least 200 bars for 200-day SMA + current
            continue
        symbol_bars[symbol_id] = bars

    # Process each trade to find qualifying opportunities
    opportunities = []
    
    for symbol_id, trades in symbol_trades.items():
        if symbol_id not in symbol_bars:
            continue
            
        bars = symbol_bars[symbol_id]
        bar_dates = [b[0] for b in bars]
        
        for filed_ts, tx_ts in trades:
            # Find the bar for the disclosure date (filed_ts)
            # Use filed_ts (when trade became public) for as-of discipline
            try:
                # Convert filed_ts to date string for comparison
                cursor.execute("SELECT date(?)", (filed_ts,))
                disc_date_str = cursor.fetchone()[0]
                # Convert to unix timestamp for bar lookup
                cursor.execute("SELECT strftime('%s', ?)", (disc_date_str,))
                disc_ts = int(cursor.fetchone()[0])
            except:
                continue
            
            # Find the bar for this disclosure date
            if disc_ts not in bar_dates:
                # Try to find closest bar before or on disclosure date
                idx = -1
                for i, bar_ts in enumerate(bar_dates):
                    if bar_ts <= disc_ts:
                        idx = i
                    else:
                        break
                if idx == -1:
                    continue
                disc_bar = bars[idx]
            else:
                disc_bar = bars[bar_dates.index(disc_ts)]
            
            disc_close = disc_bar[1]
            
            # Find index of this bar
            bar_idx = bars.index(disc_bar)
            
            # Need at least 200 bars before for 200-day SMA
            if bar_idx < 200:
                continue
            
            # Compute 200-day SMA
            sma200_prices = [bars[bar_idx - i][1] for i in range(200)]
            sma200 = sum(sma200_prices) / 200
            
            # Compute 50-day SMA
            if bar_idx < 50:
                continue
            sma50_prices = [bars[bar_idx - i][1] for i in range(50)]
            sma50 = sum(sma50_prices) / 50
            
            # Check entry condition: price > 200-day SMA and price < 50-day SMA
            if not (disc_close > sma200 and disc_close < sma50):
                continue
            
            # Need at least 21 bars after for 21-day horizon
            if bar_idx + 21 >= len(bars):
                continue
            
            # Get horizon close
            horizon_bar = bars[bar_idx + 21]
            horizon_close = horizon_bar[1]
            horizon_ts = horizon_bar[0]
            
            # Calculate return
            fwd_return = (horizon_close - disc_close) / disc_close
            up = 1 if fwd_return > 0 else 0
            
            opportunities.append((symbol_id, disc_date_str, disc_close, horizon_close, 
                                fwd_return, up, horizon_ts))

    # If no opportunities found
    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # Sort opportunities by date
    opportunities.sort(key=lambda x: x[1])
    
    # Split into training (80%) and sealed (20%) by time
    n_opps = len(opportunities)
    split_idx = int(n_opps * 0.8)
    training_opps = opportunities[:split_idx]
    sealed_opps = opportunities[split_idx:]
    
    # Function to calculate metrics for a set of opportunities
    def calculate_metrics(opps_set):
        if not opps_set:
            return 0, 0, 0, 0, 0, 0, 0
        
        issued = len(opps_set)
        
        # Group by (symbol, day) to count independent observations
        day_groups = defaultdict(list)
        for opp in opps_set:
            symbol_id, day, _, _, _, up, _ = opp
            day_groups[(symbol_id, day)].append(up)
        
        # Count hits (up=1)
        hits = sum(1 for opp in opps_set if opp[5] == 1)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate within issued subset (same as precision in this case)
        base_rate = hits / issued if issued > 0 else 0
        
        # Distinct days
        distinct_days = len(day_groups)
        
        # Calculate design effect for time clustering
        # Group by day to get cluster sizes
        day_cluster_sizes = [len(group) for group in day_groups.values()]
        m = sum(day_cluster_sizes) / len(day_cluster_sizes) if day_cluster_sizes else 0
        
        # Calculate ICC using method of moments for binary outcomes
        p = hits / issued if issued > 0 else 0
        if m <= 1:
            deff = 1
        else:
            # Calculate between-cluster variance
            day_props = [sum(group)/len(group) if len(group) > 0 else 0 
                        for group in day_groups.values()]
            mean_p = sum(day_props) / len(day_props) if day_props else 0
            var_between = sum((prop - mean_p) ** 2 for prop in day_props) / len(day_props) if day_props else 0
            
            # Within-cluster variance (binomial approximation)
            var_within = p * (1 - p)
            
            if var_within + var_between > 0:
                icc = var_between / (var_within + var_between)
            else:
                icc = 0
            
            deff = 1 + (m - 1) * icc
        
        effective_n = issued / deff if deff > 0 else issued
        
        return issued, precision, base_rate, distinct_days, effective_n, 0, hits
    
    # Calculate training metrics
    train_issued, train_precision, train_base_rate, train_distinct_days, train_effective_n, _, train_hits = calculate_metrics(training_opps)
    
    # Calculate sealed metrics
    sealed_issued, sealed_precision, _, _, _, _, _ = calculate_metrics(sealed_opps)
    
    # Verify invariants
    if train_distinct_days > train_issued:
        print("INSUFFICIENT=1")
        return
    
    if train_effective_n >= train_issued:
        print("INSUFFICIENT=1")
        return
    
    # Print required outputs
    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={len(training_opps)}")
    print(f"PRECISION={train_precision:.4f}")
    print(f"BASE_RATE={train_base_rate:.4f}")
    print(f"DISTINCT_DAYS={train_distinct_days}")
    print(f"EFFECTIVE_N={train_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
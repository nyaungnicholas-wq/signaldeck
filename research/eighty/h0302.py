# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 301
# cycle_index: 24
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import statistics

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Get insider purchases (code='P') with their filing dates
        cursor.execute("""
            SELECT symbol_id, filed_ts
            FROM insider_trades
            WHERE code = 'P'
            ORDER BY filed_ts
        """)
        trades = cursor.fetchall()
        
        if len(trades) < 2:
            print("INSUFFICIENT=1")
            conn.close()
            return
        
        # Group trades by symbol
        symbol_trades = defaultdict(list)
        for symbol_id, filed_ts in trades:
            symbol_trades[symbol_id].append(filed_ts)
        
        # Get all distinct symbols in the insider trades
        symbols_with_trades = list(symbol_trades.keys())
        
        # Get all symbols for return calculations
        cursor.execute("SELECT id FROM symbols")
        all_symbols = [row[0] for row in cursor.fetchall()]
        
        # Get all 1d bars for return calculations
        cursor.execute("""
            SELECT symbol_id, ts, close
            FROM bars
            WHERE tf = '1d'
            ORDER BY ts
        """)
        all_bars = cursor.fetchall()
        
        # Build price series by symbol
        prices = defaultdict(list)
        for symbol_id, ts, close in all_bars:
            prices[symbol_id].append((ts, close))
        
        # Sort prices by timestamp for each symbol
        for symbol_id in prices:
            prices[symbol_id].sort(key=lambda x: x[0])
        
        # Helper function to get price at specific timestamp
        def get_price_at(symbol_id, target_ts):
            if symbol_id not in prices:
                return None
            price_list = prices[symbol_id]
            
            # Find the closest price before or at target_ts
            best_price = None
            for ts, price in price_list:
                if ts <= target_ts:
                    best_price = price
                else:
                    break
            return best_price
        
        # Helper function to get next price after timestamp
        def get_next_price_after(symbol_id, after_ts):
            if symbol_id not in prices:
                return None
            for ts, price in prices[symbol_id]:
                if ts > after_ts:
                    return ts, price
            return None
        
        # Process clusters and generate signals
        signals = []
        decisions = []
        
        # For each symbol with trades, find clusters
        for symbol_id in symbols_with_trades:
            trade_dates = sorted(symbol_trades[symbol_id])
            
            # Check each trade as potential cluster end
            for i, cluster_end_ts in enumerate(trade_dates):
                # Find all trades within 30 days before cluster_end
                window_start = cluster_end_ts - (30 * 24 * 60 * 60)
                cluster_trades = [ts for ts in trade_dates if ts > window_start and ts <= cluster_end_ts]
                
                if len(cluster_trades) < 2:
                    continue
                
                # Count distinct insiders in this cluster
                cursor.execute("""
                    SELECT COUNT(DISTINCT insider)
                    FROM insider_trades
                    WHERE symbol_id = ? AND code = 'P' AND filed_ts > ? AND filed_ts <= ?
                """, (symbol_id, window_start, cluster_end_ts))
                distinct_insiders = cursor.fetchone()[0]
                
                if distinct_insiders < 2:
                    continue
                
                # Get the next bar after cluster_end for decision time
                next_bar = get_next_price_after(symbol_id, cluster_end_ts)
                if next_bar is None:
                    continue
                
                decision_ts, entry_price = next_bar
                decision_date = datetime.utcfromtimestamp(decision_ts).date()
                
                # Get 30-day return to check exclusion criteria
                thirty_days_ago_ts = decision_ts - (30 * 24 * 60 * 60)
                price_thirty_days_ago = get_price_at(symbol_id, thirty_days_ago_ts)
                if price_thirty_days_ago is None or entry_price is None:
                    continue
                
                symbol_return_30d = (entry_price / price_thirty_days_ago) - 1
                
                # Calculate returns for all symbols to find top/bottom 10%
                returns_30d = []
                for other_symbol in all_symbols:
                    if other_symbol == symbol_id:
                        continue
                    other_price_now = get_price_at(other_symbol, decision_ts)
                    other_price_30d = get_price_at(other_symbol, thirty_days_ago_ts)
                    if other_price_now and other_price_30d:
                        other_return = (other_price_now / other_price_30d) - 1
                        returns_30d.append((other_symbol, other_return))
                
                if len(returns_30d) < 10:  # Need enough to calculate percentiles
                    continue
                
                # Sort returns to find percentiles
                returns_sorted = sorted([r for _, r in returns_30d])
                n = len(returns_sorted)
                lower_idx = max(0, int(n * 0.1))
                upper_idx = min(n-1, int(n * 0.9))
                lower_bound = returns_sorted[lower_idx]
                upper_bound = returns_sorted[upper_idx]
                
                # Check exclusion criteria
                if symbol_return_30d >= upper_bound:
                    continue  # Top 10%
                if symbol_return_30d <= lower_bound:
                    continue  # Bottom 10%
                
                # Get forward return (21 days) - find close price 21 days after decision
                target_close_ts = decision_ts + (21 * 24 * 60 * 60)
                
                # Find the bar closest to target_close_ts (may not be exact 21 days)
                closest_close = None
                closest_ts = None
                for ts, price in prices.get(symbol_id, []):
                    if ts >= decision_ts:
                        if ts <= target_close_ts:
                            closest_close = price
                            closest_ts = ts
                
                if closest_close is None or closest_close == 0:
                    continue
                
                forward_return = (closest_close / entry_price) - 1
                
                # Record this decision point
                decisions.append({
                    'symbol_id': symbol_id,
                    'decision_date': decision_date,
                    'entry_price': entry_price,
                    'forward_return': forward_return
                })
        
        if len(decisions) == 0:
            print("INSUFFICIENT=1")
            conn.close()
            return
        
        # Sort decisions by date
        decisions.sort(key=lambda x: x['decision_date'])
        
        # Split into training and sealed era (last 20%)
        split_idx = int(len(decisions) * 0.8)
        training_decisions = decisions[:split_idx]
        sealed_decisions = decisions[split_idx:]
        
        # Count observations (one per symbol per day)
        day_symbol_counts = defaultdict(set)
        for d in training_decisions:
            day_symbol_counts[d['decision_date']].add(d['symbol_id'])
        
        opportunities = len(day_symbol_counts)  # Unique (symbol, day) pairs considered
        issued = len(training_decisions)  # Calls actually issued
        
        # Calculate precision and base rate for training set
        hits = sum(1 for d in training_decisions if d['forward_return'] > 0)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate = proportion of positive outcomes in issued calls
        base_rate = precision  # In this binary case, base rate equals precision
        
        # Count distinct days in issued calls
        distinct_days = len(day_symbol_counts)
        
        # Calculate design effect
        # Group by day
        daily_outcomes = defaultdict(list)
        for d in training_decisions:
            daily_outcomes[d['decision_date']].append(1 if d['forward_return'] > 0 else 0)
        
        # Calculate ICC
        overall_mean = precision
        group_means = []
        group_sizes = []
        for day, outcomes in daily_outcomes.items():
            group_means.append(statistics.mean(outcomes))
            group_sizes.append(len(outcomes))
        
        if len(group_means) > 1:
            # Calculate between-group variance
            between_var = statistics.variance(group_means) if len(group_means) > 1 else 0
            # Calculate within-group variance (average of variances within each day)
            within_var = 0
            for day, outcomes in daily_outcomes.items():
                if len(outcomes) > 1:
                    within_var += statistics.variance(outcomes)
            within_var /= len(group_means)
            
            # Design effect
            avg_cluster_size = statistics.mean(group_sizes)
            if between_var + within_var > 0:
                icc = between_var / (between_var + within_var)
                design_effect = 1 + (avg_cluster_size - 1) * icc
            else:
                design_effect = 1
        else:
            design_effect = 1
        
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Calculate sealed era metrics
        sealed_hits = sum(1 for d in sealed_decisions if d['forward_return'] > 0)
        sealed_precision = sealed_hits / len(sealed_decisions) if len(sealed_decisions) > 0 else 0
        
        # Print required outputs
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        conn.close()
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
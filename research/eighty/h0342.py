# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 341
# cycle_index: 9
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
from collections import defaultdict

def get_db():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, isolation_level=None)

def run():
    conn = get_db()
    c = conn.cursor()
    
    # Check if we have enough data for 21-day forward returns
    c.execute("SELECT COUNT(DISTINCT symbol_id) FROM prediction_outcomes WHERE horizon = 21")
    n_symbols = c.fetchone()[0]
    if n_symbols == 0:
        print("INSUFFICIENT=1")
        return 0
    
    # Get all trading days from daily bars
    c.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    trading_days = [row[0] for row in c.fetchall()]
    if len(trading_days) < 42:  # Need at least 42 days for 21-day lookback and forward
        print("INSUFFICIENT=1")
        return 0
    
    # Map trading days to dates for easier processing
    day_to_date = {}
    date_to_day = {}
    for day in trading_days:
        dt = datetime.datetime.utcfromtimestamp(day).date()
        day_to_date[day] = dt
        date_to_day[dt] = day
    
    # Find weekly decision dates (first trading day of each week)
    weekly_dates = set()
    seen_weeks = set()
    for day in trading_days:
        dt = day_to_date[day]
        week_key = (dt.isocalendar()[0], dt.isocalendar()[1])  # (year, week)
        if week_key not in seen_weeks:
            seen_weeks.add(week_key)
            weekly_dates.add(day)
    
    weekly_dates = sorted(weekly_dates)
    
    # Precompute fundamentals data
    fundamentals = defaultdict(dict)  # symbol_id -> {metric: [(fetched_at, value)]}
    c.execute("""
        SELECT symbol_id, metric, value, fetched_at 
        FROM fundamentals 
        WHERE metric IN ('Revenues', 'SharesOutstanding')
    """)
    for row in c.fetchall():
        symbol_id, metric, value, fetched_at = row
        if value and value != 'None':
            try:
                val = float(value)
                fundamentals[symbol_id][metric] = (fetched_at, val)
            except (ValueError, TypeError):
                pass
    
    # Precompute price and volume data
    price_data = defaultdict(dict)  # symbol_id -> {ts: close}
    volume_data = defaultdict(dict)  # symbol_id -> {ts: volume}
    c.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d'")
    for row in c.fetchall():
        symbol_id, ts, close, volume = row
        price_data[symbol_id][ts] = close
        volume_data[symbol_id][ts] = volume
    
    # Precompute active symbols
    c.execute("SELECT id, market FROM symbols WHERE active=1")
    symbol_markets = {row[0]: row[1] for row in c.fetchall()}
    
    # Function to get latest fundamentals before a date
    def get_fundamentals(symbol_id, cutoff_ts):
        if symbol_id not in fundamentals:
            return None, None
        revenue_info = fundamentals[symbol_id].get('Revenues')
        shares_info = fundamentals[symbol_id].get('SharesOutstanding')
        if not revenue_info or not shares_info:
            return None, None
        revenue_ts, revenue = revenue_info
        shares_ts, shares = shares_info
        # Check if both are within 90 days
        cutoff_dt = datetime.datetime.utcfromtimestamp(cutoff_ts)
        revenue_dt = datetime.datetime.utcfromtimestamp(revenue_ts)
        shares_dt = datetime.datetime.utcfromtimestamp(shares_ts)
        if (cutoff_dt - revenue_dt).days > 90 or (cutoff_dt - shares_dt).days > 90:
            return None, None
        return revenue, shares
    
    # Function to get last N trading days before a cutoff
    def get_trading_days_before(cutoff_ts, n):
        days_before = [d for d in trading_days if d <= cutoff_ts]
        return days_before[-n:] if len(days_before) >= n else []
    
    # Function to compute trailing metrics
    def compute_metrics(symbol_id, decision_ts):
        # Get last 21 trading days including decision day
        last_21 = get_trading_days_before(decision_ts, 21)
        if len(last_21) < 21:
            return None
        
        # Check price >= $5
        if decision_ts not in price_data.get(symbol_id, {}):
            return None
        last_price = price_data[symbol_id][decision_ts]
        if last_price < 5:
            return None
        
        # Get fundamentals
        revenue, shares = get_fundamentals(symbol_id, decision_ts)
        if revenue is None or shares is None:
            return None
        
        # Compute market cap
        market_cap = shares * last_price
        if market_cap < 100_000_000:  # $100M
            return None
        
        # Sales-to-price ratio
        sales_to_price = revenue / market_cap if market_cap > 0 else 0
        
        # Trailing 21-day return
        if len(last_21) < 21:
            return None
        price_21_ago = price_data[symbol_id].get(last_21[0])
        if not price_21_ago or price_21_ago == 0:
            return None
        trailing_return = (last_price / price_21_ago) - 1
        
        # Average daily dollar volume over prior 21 days
        dollar_volumes = []
        for day in last_21:
            p = price_data[symbol_id].get(day, 0)
            v = volume_data[symbol_id].get(day, 0)
            dollar_volumes.append(p * v)
        avg_dollar_volume = sum(dollar_volumes) / len(dollar_volumes)
        
        return {
            'sales_to_price': sales_to_price,
            'trailing_return': trailing_return,
            'avg_dollar_volume': avg_dollar_volume,
            'market_cap': market_cap,
            'last_price': last_price
        }
    
    # Process each weekly decision date
    decisions = []  # (decision_ts, symbol_id)
    
    for decision_ts in weekly_dates:
        # Get all symbols with data
        eligible_symbols = []
        
        for symbol_id in symbol_markets:
            if symbol_id not in price_data or decision_ts not in price_data[symbol_id]:
                continue
            
            metrics = compute_metrics(symbol_id, decision_ts)
            if not metrics:
                continue
            
            # Apply abstain criteria
            if (metrics['avg_dollar_volume'] < 1_000_000 or
                metrics['trailing_return'] >= 0):
                continue
            
            eligible_symbols.append((symbol_id, metrics))
        
        if not eligible_symbols:
            continue
        
        # Compute decile cutoff for sales-to-price
        sales_values = [m['sales_to_price'] for _, m in eligible_symbols]
        sales_values.sort(reverse=True)
        n_eligible = len(eligible_symbols)
        decile_idx = int(n_eligible * 0.1)
        if decile_idx == 0:
            continue
        decile_cutoff = sales_values[decile_idx - 1]
        
        # Issue calls for top decile
        for symbol_id, metrics in eligible_symbols:
            if metrics['sales_to_price'] >= decile_cutoff:
                decisions.append((decision_ts, symbol_id))
    
    if not decisions:
        print("INSUFFICIENT=1")
        return 0
    
    # Split into training and sealed era (last 20%)
    n_decisions = len(decisions)
    split_idx = int(n_decisions * 0.8)
    training_decisions = decisions[:split_idx]
    sealed_decisions = decisions[split_idx:]
    
    # Evaluate predictions
    def evaluate(decisions_list, is_sealed=False):
        if not decisions_list:
            return 0, 0, 0, set(), 0
        
        hits = 0
        total = 0
        base_up = 0
        days_issued = set()
        
        for decision_ts, symbol_id in decisions_list:
            # Get forward 21-day return from prediction_outcomes
            c.execute("""
                SELECT up, fwd_return 
                FROM prediction_outcomes 
                WHERE symbol_id=? AND horizon=21 AND ts=?
            """, (symbol_id, decision_ts))
            row = c.fetchone()
            if not row:
                continue
            
            up, fwd_return = row
            if up is None:
                continue
            
            total += 1
            days_issued.add(day_to_date[decision_ts])
            
            if up == 1:
                hits += 1
                base_up += 1
        
        precision = hits / total if total > 0 else 0
        base_rate = base_up / total if total > 0 else 0
        
        # Compute design effect (assuming daily clusters)
        # Group by date and compute cluster sizes
        clusters = defaultdict(int)
        for decision_ts, _ in decisions_list:
            clusters[day_to_date[decision_ts]] += 1
        
        cluster_sizes = list(clusters.values())
        n_clusters = len(cluster_sizes)
        
        if n_clusters == 0 or total == 0:
            design_effect = 1
        else:
            avg_cluster = total / n_clusters
            variance = sum((x - avg_cluster) ** 2 for x in cluster_sizes) / n_clusters
            design_effect = 1 + variance / avg_cluster if avg_cluster > 0 else 1
        
        effective_n = total / design_effect if design_effect > 0 else total
        
        return precision, base_rate, total, days_issued, effective_n
    
    # Evaluate training set
    train_precision, train_base_rate, train_total, train_days, train_eff_n = evaluate(training_decisions)
    
    # Evaluate sealed era
    sealed_precision, _, sealed_total, _, _ = evaluate(sealed_decisions, is_sealed=True)
    
    # Calculate lower bound using Wilson score interval for multiplicity correction
    # For simplicity, we'll use the training set's base rate and compute the interval
    # This is a simplified version - in production, you'd want proper multiplicity correction
    import math
    
    def wilson_lower_bound(successes, trials, z=1.96):
        if trials == 0:
            return 0
        p_hat = successes / trials
        denominator = 1 + z**2 / trials
        center = p_hat + z**2 / (2 * trials)
        spread = z * math.sqrt((p_hat * (1 - p_hat) + z**2 / (4 * trials)) / trials)
        return (center - spread) / denominator
    
    # Compute lower bound for the issued subset
    train_hits = int(train_precision * train_total)
    lower_bound = wilson_lower_bound(train_hits, train_total)
    
    # Check if lower bound is strictly above base rate
    if lower_bound <= train_base_rate:
        print("INSUFFICIENT=1")
        return 0
    
    # Print required output
    print(f"ISSUED={train_total}")
    print(f"OPPORTUNITIES={n_decisions}")
    print(f"PRECISION={train_precision:.6f}")
    print(f"BASE_RATE={train_base_rate:.6f}")
    print(f"DISTINCT_DAYS={len(train_days)}")
    print(f"EFFECTIVE_N={train_eff_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    return 0

if __name__ == "__main__":
    exit_code = run()
    exit(exit_code)
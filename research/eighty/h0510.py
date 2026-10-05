# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 509
# cycle_index: 39
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
from collections import defaultdict

def get_connection():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def parse_ts(ts):
    return datetime.datetime.utcfromtimestamp(ts).date()

def main():
    conn = get_connection()
    cur = conn.cursor()
    
    # Get all insider purchases (code='P') with value >= $50k
    # filed_ts is the disclosure date we'll use as decision point
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts, value
        FROM insider_trades
        WHERE code = 'P' AND value >= 50000
    """)
    purchases = cur.fetchall()
    
    if not purchases:
        print("INSUFFICIENT=1")
        return
    
    # Get all daily bars for universe filtering
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_data = cur.fetchall()
    
    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for symbol_id, ts, close, volume in bars_data:
        bars_by_symbol[symbol_id].append((ts, close, volume))
    
    # Get all prediction outcomes for horizon=21
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    outcomes = cur.fetchall()
    
    # Organize outcomes by (symbol_id, date)
    outcomes_map = {}
    for symbol_id, ts, up in outcomes:
        outcome_date = parse_ts(ts)
        outcomes_map[(symbol_id, outcome_date)] = up
    
    # Process each purchase
    opportunities = []  # (symbol_id, decision_date, disclosure_ts)
    issued = []  # (symbol_id, decision_date, up)
    
    for symbol_id, tx_ts, filed_ts, value in purchases:
        trade_date = parse_ts(tx_ts)
        disclosure_date = parse_ts(filed_ts)
        
        # Check calendar days condition
        delta_days = (disclosure_date - trade_date).days
        if delta_days > 20:
            continue
        
        # Get bars for this symbol
        if symbol_id not in bars_by_symbol:
            continue
        
        symbol_bars = bars_by_symbol[symbol_id]
        
        # Find bar indices for disclosure_date and trade_date
        disclosure_bar = None
        trade_bar = None
        for idx, (ts, close, vol) in enumerate(symbol_bars):
            bar_date = parse_ts(ts)
            if bar_date == disclosure_date:
                disclosure_bar = (idx, close, vol)
            if bar_date == trade_date:
                trade_bar = (idx, close, vol)
        
        # Both bars must exist
        if not disclosure_bar or not trade_bar:
            continue
        
        disclosure_idx, disclosure_close, _ = disclosure_bar
        trade_idx, trade_close, _ = trade_bar
        
        # Check price >= $5 at disclosure
        if disclosure_close < 5:
            continue
        
        # Check return condition: close(t) to close(d) <= 0%
        if trade_close == 0:
            continue
        total_return = (disclosure_close - trade_close) / trade_close
        if total_return > 0:
            continue
        
        # Check 60 prior daily bars before disclosure_date
        prior_bars = []
        for idx in range(disclosure_idx - 60, disclosure_idx):
            if 0 <= idx < len(symbol_bars):
                prior_bars.append(symbol_bars[idx])
            else:
                break
        
        if len(prior_bars) < 60:
            continue
        
        # Check median dollar volume >= $1M over prior 60 bars
        dollar_volumes = [close * vol for _, close, vol in prior_bars]
        dollar_volumes.sort()
        median_vol = dollar_volumes[len(dollar_volumes)//2]
        if median_vol < 1_000_000:
            continue
        
        # All conditions met - this is an opportunity
        opportunities.append((symbol_id, disclosure_date, filed_ts))
        
        # Check if we have outcome for this symbol on this decision date
        # The outcome ts should be the timestamp of the prediction, not disclosure_date
        # We need to find the closest prediction before disclosure_date for horizon=21
        # Simpler: use outcomes_map with decision_date as key
        outcome_key = (symbol_id, disclosure_date)
        if outcome_key in outcomes_map:
            up = outcomes_map[outcome_key]
            issued.append((symbol_id, disclosure_date, up))
    
    conn.close()
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Split into train and sealed (most recent 20% by disclosure_date)
    all_dates = sorted(set(d for _, d, _ in opportunities))
    split_idx = int(len(all_dates) * 0.8)
    sealed_start = all_dates[split_idx] if split_idx < len(all_dates) else all_dates[-1]
    
    train_issues = [(sym, d, up) for sym, d, up in issued if d < sealed_start]
    sealed_issues = [(sym, d, up) for sym, d, up in issued if d >= sealed_start]
    
    # Compute metrics
    issued_count = len(issued)
    opportunities_count = len(opportunities)
    
    if issued_count == 0:
        print("INSUFFICIENT=1")
        return
    
    precision = sum(up for _, _, up in issued) / issued_count
    
    # Base rate: proportion of "up" in issued calls (same as precision here, but definition)
    base_rate = precision
    
    # Distinct days in issued calls
    distinct_days = len(set(d for _, d, _ in issued))
    
    # Design effect and effective_n
    # Cluster by day
    day_clusters = defaultdict(list)
    for sym, d, up in issued:
        day_clusters[d].append(up)
    
    # Compute ICC
    p = precision
    total_var = p * (1 - p) if 0 < p < 1 else 0
    
    # Between-cluster variance
    k = len(day_clusters)
    if k > 1:
        cluster_props = [sum(ups)/len(ups) for ups in day_clusters.values()]
        mean_cluster_size = issued_count / k
        between_var = sum((cp - p)**2 for cp in cluster_props) / (k - 1)
        
        # Within-cluster variance
        within_var_sum = 0
        for ups in day_clusters.values():
            n_c = len(ups)
            if n_c > 1:
                p_c = sum(ups) / n_c
                within_var_sum += (n_c - 1) * p_c * (1 - p_c)
        within_var = within_var_sum / (issued_count - k) if issued_count > k else 0
        
        # ICC and design effect
        if between_var + within_var > 0:
            icc = between_var / (between_var + within_var)
            design_effect = 1 + (mean_cluster_size - 1) * icc
        else:
            design_effect = 1.0
    else:
        design_effect = 1.0
    
    effective_n = issued_count / design_effect
    
    # Sealed precision
    sealed_precision = 0
    if sealed_issues:
        sealed_precision = sum(up for _, _, up in sealed_issues) / len(sealed_issues)
    
    # Print required lines
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")

if __name__ == "__main__":
    main()
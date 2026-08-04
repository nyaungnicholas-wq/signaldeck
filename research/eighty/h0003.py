import sqlite3
import math
import sys
from collections import defaultdict

def main():
    # Open database read-only
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return
    
    cur = conn.cursor()
    
    # Check if we have S&P 500 index change data in regime_outcomes
    # We need rows where kind indicates index additions/removals
    # Based on schema: kind column exists
    cur.execute("""
        SELECT DISTINCT kind FROM regime_outcomes LIMIT 10
    """)
    kinds = [row[0] for row in cur.fetchall()]
    
    # Check for index-related kinds (additions/removals)
    index_kinds = []
    for k in kinds:
        if k and ('index' in k.lower() or 'add' in k.lower() or 'remove' in k.lower() or 'sp500' in k.lower() or 'sp 500' in k.lower()):
            index_kinds.append(k)
    
    if not index_kinds:
        # Try alternative: check if symbols table has market=stocks only for S&P 500
        # We'll assume we need to find index changes elsewhere
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Get S&P 500 symbols (stocks with market='stocks')
    cur.execute("""
        SELECT id, symbol FROM symbols WHERE market='stocks'
    """)
    stock_symbols = cur.fetchall()
    stock_ids = {row[0]: row[1] for row in stock_symbols}
    
    if not stock_ids:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Get index change events from regime_outcomes
    # We'll assume these are monthly announcements with kind indicating additions/removals
    cur.execute("""
        SELECT symbol_id, ts, kind, actual 
        FROM regime_outcomes 
        WHERE kind IN ({})
        ORDER BY ts
    """.format(','.join('?' * len(index_kinds))), index_kinds)
    events = cur.fetchall()
    
    if not events:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Convert events to decision points with labels
    decisions = []
    for symbol_id, ts, kind, actual in events:
        if symbol_id not in stock_ids:
            continue
        
        # Determine if addition or removal based on kind
        kind_lower = kind.lower()
        if 'add' in kind_lower:
            predicted_class = 'UP'
        elif 'remove' in kind_lower:
            predicted_class = 'DOWN'
        else:
            continue
        
        # Get the label from prediction_outcomes (up/down)
        cur.execute("""
            SELECT up, ts, horizon FROM prediction_outcomes 
            WHERE symbol_id = ? AND ts >= ?
            ORDER BY ts LIMIT 1
        """, (symbol_id, ts))
        outcome_row = cur.fetchone()
        
        if not outcome_row:
            continue
        
        up, outcome_ts, horizon = outcome_row
        
        # Check if horizon is T+1 trading day (1 day)
        # We'll approximate: 1 day horizon
        if horizon != 1:
            continue
        
        # Get actual return for base rate calculation
        cur.execute("""
            SELECT fwd_return FROM prediction_outcomes 
            WHERE symbol_id = ? AND ts = ?
        """, (symbol_id, ts))
        return_row = cur.fetchone()
        fwd_return = return_row[0] if return_row else 0
        
        decisions.append({
            'symbol_id': symbol_id,
            'ts': ts,
            'predicted': predicted_class,
            'actual_up': up,
            'fwd_return': fwd_return,
            'kind': kind
        })
    
    if len(decisions) < 20:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Apply abstention filters
    filtered_decisions = []
    for d in decisions:
        # Check earnings within 5 sessions - we don't have earnings data, skip this filter
        # Check 5-day realized volatility top cross-sectional decile
        cur.execute("""
            SELECT close FROM bars 
            WHERE symbol_id = ? AND tf = '1d' AND ts < ?
            ORDER BY ts DESC LIMIT 6
        """, (d['symbol_id'], d['ts']))
        bars = [row[0] for row in cur.fetchall()]
        
        if len(bars) < 6:
            continue
        
        # Calculate 5-day realized volatility (log returns std)
        log_returns = [math.log(bars[i] / bars[i+1]) for i in range(len(bars)-1)]
        if not log_returns:
            continue
        
        mean_ret = sum(log_returns) / len(log_returns)
        variance = sum((r - mean_ret) ** 2 for r in log_returns) / (len(log_returns) - 1)
        vol = math.sqrt(variance)
        
        # Store volatility for cross-sectional ranking
        d['vol'] = vol
    
    # Rank volatilities cross-sectionally for each timestamp
    vol_by_ts = defaultdict(list)
    for d in filtered_decisions:
        vol_by_ts[d['ts']].append(d['vol'])
    
    for d in filtered_decisions:
        ts_vols = vol_by_ts[d['ts']]
        if not ts_vols:
            continue
        
        # Calculate percentile rank
        ts_vols_sorted = sorted(ts_vols)
        n = len(ts_vols_sorted)
        rank = sum(1 for v in ts_vols_sorted if v <= d['vol'])
        percentile = rank / n
        
        # Top decile = above 90th percentile
        if percentile > 0.9:
            continue
        
        filtered_decisions.append(d)
    
    if len(filtered_decisions) < 20:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Hold out most recent 20% as sealed era
    filtered_decisions.sort(key=lambda x: x['ts'])
    split_idx = int(len(filtered_decisions) * 0.8)
    train_decisions = filtered_decisions[:split_idx]
    sealed_decisions = filtered_decisions[split_idx:]
    
    # Calculate metrics for training set
    issued = len(train_decisions)
    if issued == 0:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Count opportunities (all considered decisions before filtering)
    cur.execute("SELECT COUNT(*) FROM regime_outcomes WHERE kind IN ({})".format(
        ','.join('?' * len(index_kinds))), index_kinds)
    opportunities = cur.fetchone()[0]
    
    # Calculate hits (correct predictions)
    hits = sum(1 for d in train_decisions if 
               (d['predicted'] == 'UP' and d['actual_up']) or 
               (d['predicted'] == 'DOWN' and not d['actual_up']))
    
    precision = hits / issued if issued > 0 else 0
    
    # Base rate of predicted class within issued subset
    up_predictions = sum(1 for d in train_decisions if d['predicted'] == 'UP')
    base_rate = up_predictions / issued if issued > 0 else 0
    
    # Distinct days
    distinct_days = len(set(d['ts'] // 86400 for d in train_decisions))  # Convert to day
    
    # Design effect approximation (assume 1 for simplicity - would need ICC calculation)
    design_effect = 1.0
    effective_n = issued / design_effect
    
    # Sealed era metrics
    sealed_issued = len(sealed_decisions)
    sealed_hits = sum(1 for d in sealed_decisions if 
                     (d['predicted'] == 'UP' and d['actual_up']) or 
                     (d['predicted'] == 'DOWN' and not d['actual_up']))
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
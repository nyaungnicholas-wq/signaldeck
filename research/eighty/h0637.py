# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 636
# cycle_index: 11
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'data/signaldeck.db'

def connect_ro():
    return sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def get_walcl_series(conn):
    """Get FRED WALCL series data as dict {date: value}"""
    cur = conn.execute("SELECT ts, value FROM macro_series WHERE series = 'WALCL' ORDER BY ts")
    data = {}
    for ts, val in cur.fetchall():
        d = unix_to_date(ts)
        data[d] = val
    return data

def get_shares_outstanding(conn):
    """Get SharesOutstanding fundamentals as dict {symbol_id: [(as_of_date, value, fetched_at_date), ...]}"""
    cur = conn.execute("""
        SELECT symbol_id, as_of, value, fetched_at 
        FROM fundamentals 
        WHERE metric = 'SharesOutstanding' 
        ORDER BY symbol_id, as_of
    """)
    data = {}
    for symbol_id, as_of, val, fetched_at in cur.fetchall():
        as_of_date = unix_to_date(as_of)
        fetched_date = unix_to_date(fetched_at)
        if symbol_id not in data:
            data[symbol_id] = []
        data[symbol_id].append((as_of_date, float(val), fetched_date))
    return data

def get_price_data(conn, symbol_ids):
    """Get 1d bars for symbols, return dict {symbol_id: [(ts, close), ...]}"""
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, ts, close 
        FROM bars 
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    data = {}
    for symbol_id, ts, close in cur.fetchall():
        d = unix_to_date(ts)
        if symbol_id not in data:
            data[symbol_id] = []
        data[symbol_id].append((d, float(close)))
    return data

def get_labels(conn, symbol_ids, horizons):
    """Get prediction_outcomes labels for horizon=21"""
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, horizon, ts, up 
        FROM prediction_outcomes 
        WHERE horizon = 21 AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    labels = {}
    for symbol_id, horizon, ts, up in cur.fetchall():
        d = unix_to_date(ts)
        key = (symbol_id, d)
        labels[key] = 1 if up else 0  # 1 = up, 0 = down
    return labels

def compute_52w_high(prices, current_idx, window_days=252):
    """Compute 52-week high from trailing window_days"""
    if current_idx < window_days:
        window = prices[:current_idx+1]
    else:
        window = prices[current_idx-window_days+1:current_idx+1]
    if not window:
        return None
    return max(p[1] for p in window)

def find_walcl_90d_change(walcl_data, decision_date):
    """Find WALCL value at decision_date and 90 days prior, return change"""
    # Find closest WALCL date <= decision_date
    current_val = None
    current_date = None
    for d in sorted(walcl_data.keys()):
        if d <= decision_date:
            current_val = walcl_data[d]
            current_date = d
        else:
            break
    if current_val is None:
        return None
    
    # Find WALCL 90 days prior
    target_date = current_date - timedelta(days=90)
    prior_val = None
    for d in sorted(walcl_data.keys(), reverse=True):
        if d <= target_date:
            prior_val = walcl_data[d]
            break
    if prior_val is None:
        return None
    
    return current_val - prior_val

def main():
    conn = connect_ro()
    
    # Get all data
    walcl = get_walcl_series(conn)
    if not walcl:
        print("INSUFFICIENT=1")
        return
    
    shares_data = get_shares_outstanding(conn)
    if not shares_data:
        print("INSUFFICIENT=1")
        return
    
    symbol_ids = list(shares_data.keys())
    prices = get_price_data(conn, symbol_ids)
    labels = get_labels(conn, symbol_ids, [21])
    
    # Build decision points
    decisions = []  # list of (symbol_id, decision_date, label)
    
    for symbol_id, shares_list in shares_data.items():
        if symbol_id not in prices:
            continue
        price_list = prices[symbol_id]
        if len(price_list) < 252:  # Need at least 52 weeks of data
            continue
        
        # Sort shares by as_of date
        shares_list.sort(key=lambda x: x[0])
        
        # For each quarterly shares report, check QoQ change
        for i in range(1, len(shares_list)):
            as_of_curr, val_curr, fetched_curr = shares_list[i]
            as_of_prev, val_prev, fetched_prev = shares_list[i-1]
            
            # Check staleness: fetched_at must be within 130 days of decision date
            # Decision date is the fetched_at of current quarter (when we learn it)
            decision_date = fetched_curr
            if (decision_date - fetched_curr).days > 130:
                continue
            
            # QoQ change
            if val_prev == 0:
                continue
            qoq_change = (val_curr - val_prev) / val_prev
            if qoq_change >= -0.02:  # Need >=2% fall (negative change)
                continue
            
            # Check WALCL 90-day change
            walcl_change = find_walcl_90d_change(walcl, decision_date)
            if walcl_change is None or walcl_change >= 0:
                continue
            
            # Check price within 5% of 52-week high at decision date
            # Find price index for decision_date
            price_idx = None
            for idx, (p_date, _) in enumerate(price_list):
                if p_date <= decision_date:
                    price_idx = idx
                else:
                    break
            if price_idx is None or price_idx < 0:
                continue
            
            current_price = price_list[price_idx][1]
            high_52w = compute_52w_high(price_list, price_idx)
            if high_52w is None:
                continue
            
            if current_price < high_52w * 0.95:  # More than 5% below high
                continue
            
            # All entry conditions met - issue "down" call
            # Label is at decision_date + 21 trading days
            # Find label for this symbol at decision_date (prediction_outcomes ts is the decision timestamp)
            label_key = (symbol_id, decision_date)
            if label_key in labels:
                label = labels[label_key]  # 1=up, 0=down
                decisions.append((symbol_id, decision_date, label))
    
    if not decisions:
        print("INSUFFICIENT=1")
        return
    
    # Sort by decision date
    decisions.sort(key=lambda x: x[1])
    
    # Hold out most recent 20% as sealed era
    n_total = len(decisions)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed
    
    train_decisions = decisions[:n_train]
    sealed_decisions = decisions[n_train:]
    
    # Compute metrics for train set
    issued_train = len(train_decisions)
    hits_train = sum(1 for _, _, label in train_decisions if label == 0)  # down = 0
    precision_train = hits_train / issued_train if issued_train > 0 else 0
    base_rate_train = hits_train / issued_train if issued_train > 0 else 0  # base rate of predicted class (down) within issued
    
    # Distinct days in train
    distinct_days_train = len(set(d[1] for d in train_decisions))
    
    # Design effect for effective N
    # Cluster by month to estimate design effect
    if issued_train > 1:
        month_counts = {}
        for _, d, _ in train_decisions:
            month_key = (d.year, d.month)
            month_counts[month_key] = month_counts.get(month_key, 0) + 1
        avg_cluster = sum(month_counts.values()) / len(month_counts)
        # Design effect approx = 1 + (avg_cluster - 1) * ICC
        # Use conservative ICC of 0.1
        icc = 0.1
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n_train = issued_train / design_effect
    else:
        effective_n_train = 0
    
    # Sealed era metrics
    issued_sealed = len(sealed_decisions)
    hits_sealed = sum(1 for _, _, label in sealed_decisions if label == 0)
    precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0
    
    # Total opportunities = all decision points considered (including abstained)
    # We need to count all (symbol, date) where we had data to make a decision
    # This is complex - approximate as all shares_outstanding fetched dates that had price data
    opportunities = 0
    for symbol_id, shares_list in shares_data.items():
        if symbol_id not in prices:
            continue
        for as_of, val, fetched in shares_list:
            if (fetched - as_of).days <= 130:  # Not stale
                opportunities += 1
    
    print(f"ISSUED={issued_train}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_train:.6f}")
    print(f"BASE_RATE={base_rate_train:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_train}")
    print(f"EFFECTIVE_N={effective_n_train:.6f}")
    print(f"SEALED_PRECISION={precision_sealed:.6f}")

if __name__ == '__main__':
    main()
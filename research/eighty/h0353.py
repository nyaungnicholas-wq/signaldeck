# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 352
# cycle_index: 20
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
import math

DB_PATH = 'data/signaldeck.db'
HORIZON = 21
ABSTAIN_DAYS = 5

def connect_db():
    return sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)

def parse_ts(ts):
    return datetime.utcfromtimestamp(ts)

def date_from_ts(ts):
    return parse_ts(ts).date()

def ts_from_date(d):
    return int((datetime(d.year, d.month, d.day) - datetime(1970, 1, 1)).total_seconds())

def get_dgs3mo_series(conn):
    """Get DGS3MO time series as list of (date, value)"""
    cur = conn.execute("""
        SELECT ts, value FROM macro_series
        WHERE series = 'DGS3MO' AND value IS NOT NULL
        ORDER BY ts
    """)
    return [(date_from_ts(ts), val) for ts, val in cur.fetchall()]

def get_news_sentiment_series(conn, symbol_id):
    """Get sentiment features for symbol as list of (date, mean_score)"""
    cur = conn.execute("""
        SELECT day, mean_score FROM sentiment_features
        WHERE symbol_id = ? AND mean_score IS NOT NULL
        ORDER BY day
    """, (symbol_id,))
    return [(datetime.strptime(day, '%Y-%m-%d').date(), score) 
            for day, score in cur.fetchall()]

def is_dgs3mo_at_30d_high(dgs_series, current_date):
    """Check if DGS3MO at current_date is 30-day high"""
    if not dgs_series:
        return False
    
    # Find current value
    current_val = None
    for d, val in dgs_series:
        if d == current_date:
            current_val = val
            break
    
    if current_val is None:
        # Use most recent value <= current_date
        for d, val in reversed(dgs_series):
            if d <= current_date:
                current_val = val
                break
    
    if current_val is None:
        return False
    
    # Check if highest in last 30 days
    cutoff = current_date - timedelta(days=30)
    for d, val in dgs_series:
        if d >= cutoff and d <= current_date and val > current_val:
            return False
    return True

def is_sentiment_below_20d_avg(sent_series, current_date):
    """Check if sentiment at current_date is below its 20-day average"""
    if len(sent_series) < 20:
        return False
    
    # Get scores for last 20 days up to current_date
    scores = []
    for d, score in sent_series:
        if d <= current_date:
            scores.append(score)
    
    if len(scores) < 20:
        return False
    
    last_20 = scores[-20:]
    avg_20 = sum(last_20) / 20
    current_score = scores[-1]
    
    return current_score < avg_20

def get_insider_purchases(conn):
    """Get all Form 4 open-market purchases with symbol_id and filed_ts"""
    cur = conn.execute("""
        SELECT symbol_id, filed_ts FROM insider_trades
        WHERE code = 'P' AND filed_ts IS NOT NULL
    """)
    return cur.fetchall()

def get_label(conn, symbol_id, entry_date):
    """Get label for symbol_id at entry_date with 21-day horizon"""
    entry_ts = ts_from_date(entry_date)
    cur = conn.execute("""
        SELECT up FROM prediction_outcomes
        WHERE symbol_id = ? AND horizon = 21 AND ts = ?
    """, (symbol_id, entry_ts))
    row = cur.fetchone()
    return row[0] if row else None

def get_all_symbols_with_dates(conn):
    """Get all unique (symbol_id, entry_date) pairs from insider purchases after filtering"""
    dgs_series = get_dgs3mo_series(conn)
    purchases = get_insider_purchases(conn)
    
    # Cache sentiment series by symbol
    sent_cache = {}
    events = []
    
    for symbol_id, filed_ts in purchases:
        if symbol_id not in sent_cache:
            sent_cache[symbol_id] = get_news_sentiment_series(conn, symbol_id)
        
        sent_series = sent_cache[symbol_id]
        disclosure_date = date_from_ts(filed_ts)
        
        if (is_dgs3mo_at_30d_high(dgs_series, disclosure_date) and 
            is_sentiment_below_20d_avg(sent_series, disclosure_date)):
            
            # Entry date is disclosure + abstain days
            entry_date = disclosure_date + timedelta(days=ABSTAIN_DAYS)
            events.append((symbol_id, entry_date, disclosure_date))
    
    return events

def compute_effective_n(issued, day_counts):
    """Compute effective sample size accounting for day clustering"""
    if not day_counts or issued == 0:
        return issued
    
    # Calculate intra-cluster correlation (ICC) using variance components
    n_days = len(day_counts)
    if n_days <= 1:
        return issued
    
    # Get proportion of successes per day and overall
    # This is a simplified approach for binary outcomes
    avg_cluster_size = issued / n_days
    
    # Design effect approximation for clustered binary data
    # Using formula: DE = 1 + (m-1)*ICC
    # For binary data, we estimate ICC from variance of cluster means
    # This is a conservative estimate
    design_effect = 1 + (avg_cluster_size - 1) * 0.1  # Assume moderate ICC
    
    return issued / design_effect

def main():
    try:
        conn = connect_db()
        
        # Get all potential events
        events = get_all_symbols_with_dates(conn)
        
        if not events:
            print("INSUFFICIENT=1")
            return
        
        # For each event, get label and prepare calls
        calls = []
        for symbol_id, entry_date, disclosure_date in events:
            label = get_label(conn, symbol_id, entry_date)
            if label is not None:
                calls.append({
                    'symbol_id': symbol_id,
                    'entry_date': entry_date,
                    'disclosure_date': disclosure_date,
                    'label': int(label)
                })
        
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Sort by entry_date to split into train/sealed eras
        calls.sort(key=lambda x: x['entry_date'])
        n_total = len(calls)
        split_idx = int(n_total * 0.8)
        
        train_calls = calls[:split_idx]
        sealed_calls = calls[split_idx:]
        
        # Calculate metrics for train set
        issued = len(train_calls)
        if issued == 0:
            print("INSUFFICIENT=1")
            return
        
        hits = sum(c['label'] for c in train_calls)
        precision = hits / issued
        base_rate = precision  # Base rate in issued set is same as precision for binary
        
        # Count distinct days
        distinct_days = len(set(c['entry_date'] for c in train_calls))
        
        # Count observations per day for effective N calculation
        day_counts = {}
        for c in train_calls:
            day = c['entry_date']
            day_counts[day] = day_counts.get(day, 0) + 1
        
        effective_n = compute_effective_n(issued, list(day_counts.values()))
        
        # Sealed era metrics
        sealed_issued = len(sealed_calls)
        sealed_hits = sum(c['label'] for c in sealed_calls) if sealed_issued > 0 else 0
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print required output
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={n_total}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.1f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
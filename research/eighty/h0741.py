# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 740
# cycle_index: 10
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def get_candidate_events(conn):
    """Get insider purchases with negative sentiment at trade date."""
    sql = """
    SELECT 
        it.symbol_id,
        it.tx_ts,
        it.filed_ts,
        sf.mean_score,
        sf.day as trade_day,
        s.symbol
    FROM insider_trades it
    JOIN sentiment_features sf 
        ON it.symbol_id = sf.symbol_id 
        AND sf.day = date(it.tx_ts, 'unixepoch')
    JOIN symbols s ON it.symbol_id = s.id
    WHERE it.code = 'P'
        AND sf.mean_score < -1.0
        AND it.filed_ts > it.tx_ts
        AND it.filed_ts - it.tx_ts <= 30*86400
        AND s.active = 1
        AND s.market = 'stocks'
    ORDER BY it.filed_ts
    """
    cur = conn.execute(sql)
    rows = cur.fetchall()
    return rows

def get_forward_return(conn, symbol_id, filed_ts, horizon_days=21):
    """Get forward return over horizon_days trading days from filed_ts using 1d bars."""
    # Find first trading day at or after filed_ts
    sql_first = """
        SELECT ts, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
        ORDER BY ts LIMIT 1
    """
    cur = conn.execute(sql_first, (symbol_id, filed_ts))
    first = cur.fetchone()
    if not first:
        return None
    start_ts, start_close = first
    
    # Find the bar at start_ts + horizon_days trading days
    sql_nth = """
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        ORDER BY ts LIMIT 1 OFFSET ?
    """
    cur = conn.execute(sql_nth, (symbol_id, start_ts, horizon_days - 1))
    nth = cur.fetchone()
    if not nth:
        return None
    end_close = nth[0]
    
    if start_close <= 0:
        return None
    return (end_close - start_close) / start_close

def main():
    conn = connect_ro()
    
    print("Fetching candidate events...", file=sys.stderr)
    events = get_candidate_events(conn)
    print(f"Found {len(events)} candidate events", file=sys.stderr)
    
    if len(events) < 30:
        print("INSUFFICIENT=1")
        return 0
    
    results = []
    for symbol_id, tx_ts, filed_ts, mean_score, trade_day, symbol in events:
        fwd_ret = get_forward_return(conn, symbol_id, filed_ts, 21)
        if fwd_ret is None:
            continue
        up = 1 if fwd_ret > 0 else 0
        filing_date = datetime.utcfromtimestamp(filed_ts).date()
        results.append({
            'symbol_id': symbol_id,
            'symbol': symbol,
            'tx_ts': tx_ts,
            'filed_ts': filed_ts,
            'filing_date': filing_date,
            'mean_score': mean_score,
            'fwd_return': fwd_ret,
            'up': up
        })
    
    print(f"Computed forward returns for {len(results)} events", file=sys.stderr)
    
    if len(results) < 30:
        print("INSUFFICIENT=1")
        return 0
    
    # Sort by filing date
    results.sort(key=lambda x: x['filed_ts'])
    
    # Deduplicate by (symbol_id, filing_date) - one observation per symbol per day
    seen = set()
    deduped = []
    for r in results:
        key = (r['symbol_id'], r['filing_date'])
        if key not in seen:
            seen.add(key)
            deduped.append(r)
    
    print(f"After deduplication: {len(deduped)} independent observations", file=sys.stderr)
    
    if len(deduped) < 30:
        print("INSUFFICIENT=1")
        return 0
    
    # Split: most recent 20% as sealed
    n = len(deduped)
    split_idx = int(n * 0.8)
    train = deduped[:split_idx]
    sealed = deduped[split_idx:]
    
    def compute_metrics(data, label):
        if not data:
            return None
        issued = len(data)
        hits = sum(1 for r in data if r['up'] == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(set(r['filing_date'] for r in data))
        # Design effect: 1 + (avg_cluster_size - 1) * ICC, ICC=0.1
        avg_cluster = issued / distinct_days if distinct_days > 0 else 1
        design_effect = 1 + (avg_cluster - 1) * 0.1
        effective_n = issued / design_effect
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n,
            'label': label
        }
    
    train_metrics = compute_metrics(train, 'train')
    sealed_metrics = compute_metrics(sealed, 'sealed')
    
    if not train_metrics or not sealed_metrics:
        print("INSUFFICIENT=1")
        return 0
    
    # Overall metrics (for required output)
    all_metrics = compute_metrics(deduped, 'all')
    
    # Required output lines
    print(f"ISSUED={all_metrics['issued']}")
    print(f"OPPORTUNITIES={len(events)}")  # decision points considered before dedup
    print(f"PRECISION={all_metrics['precision']:.6f}")
    print(f"BASE_RATE={all_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={all_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={all_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")
    
    # Also print train for reference
    print(f"TRAIN_PRECISION={train_metrics['precision']:.6f}", file=sys.stderr)
    print(f"TRAIN_ISSUED={train_metrics['issued']}", file=sys.stderr)
    print(f"SEALED_ISSUED={sealed_metrics['issued']}", file=sys.stderr)
    
    return 0

if __name__ == '__main__':
    sys.exit(main())
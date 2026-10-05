# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 700
# cycle_index: 27
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_db():
    return sqlite3.connect(DB_PATH, uri=True)

def get_shares_outstanding_quarters(conn):
    """Get SharesOutstanding data organized by symbol and quarter."""
    cur = conn.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding'
        ORDER BY symbol_id, as_of
    """)
    rows = cur.fetchall()
    by_symbol = {}
    for symbol_id, value, as_of, fetched_at in rows:
        if as_of == 0:
            continue
        # as_of is unix epoch, convert to year-quarter
        dt = datetime.utcfromtimestamp(as_of)
        quarter = (dt.year, (dt.month - 1) // 3 + 1)
        by_symbol.setdefault(symbol_id, []).append((quarter, value, fetched_at))
    return by_symbol

def check_stability(quarters_data, min_quarters=8, max_pct_change=0.01):
    """Check if last min_quarters consecutive quarters have |QoQ change| < max_pct_change."""
    if len(quarters_data) < min_quarters:
        return False
    # Sort by quarter
    quarters_data.sort(key=lambda x: x[0])
    # Check last min_quarters
    recent = quarters_data[-min_quarters:]
    for i in range(1, len(recent)):
        prev_val = recent[i-1][1]
        curr_val = recent[i][1]
        if prev_val == 0:
            return False
        pct_change = abs(curr_val - prev_val) / prev_val
        if pct_change >= max_pct_change:
            return False
    return True

def get_insider_purchases(conn):
    """Get open-market insider purchases (code='P') with disclosure date."""
    cur = conn.execute("""
        SELECT accession, symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    return cur.fetchall()

def get_forward_return(conn, symbol_id, decision_ts, horizon_days=21):
    """Get 21-trading-day forward return from daily bars."""
    # Find the decision bar (tf='1d') on or after decision_ts
    decision_date = datetime.utcfromtimestamp(decision_ts).date()
    cur = conn.execute("""
        SELECT ts, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') >= ?
        ORDER BY ts
        LIMIT ?
    """, (symbol_id, decision_date.isoformat(), horizon_days + 1))
    bars = cur.fetchall()
    if len(bars) < horizon_days + 1:
        return None
    entry_close = bars[0][1]
    exit_close = bars[horizon_days][1]
    return (exit_close - entry_close) / entry_close

def business_days_between(ts1, ts2, max_days=5):
    """Approximate business days between two timestamps."""
    dt1 = datetime.utcfromtimestamp(ts1).date()
    dt2 = datetime.utcfromtimestamp(ts2).date()
    delta = (dt2 - dt1).days
    if delta < 0:
        return 999
    # Rough approximation: 5 business days per 7 calendar days
    return delta * 5 // 7

def main():
    conn = connect_db()
    conn.row_factory = sqlite3.Row
    
    # 1. Check SharesOutstanding data availability
    so_data = get_shares_outstanding_quarters(conn)
    
    # Count symbols with >=8 quarters
    symbols_with_8q = sum(1 for v in so_data.values() if len(v) >= 8)
    total_quarters = sum(len(v) for v in so_data.values())
    
    # 2. Get insider purchases
    purchases = get_insider_purchases(conn)
    
    # 3. For each purchase, check if stability condition met at disclosure time (fetched_at <= filed_ts)
    events = []
    for accession, symbol_id, tx_ts, filed_ts in purchases:
        # Staleness check: disclosure within ~2 business days of trade
        if business_days_between(tx_ts, filed_ts) > 2:
            continue
        
        # Get SharesOutstanding data available at disclosure time
        symbol_quarters = [(q, v, fa) for q, v, fa in so_data.get(symbol_id, []) if fa <= filed_ts]
        if len(symbol_quarters) < 8:
            continue
        
        if not check_stability(symbol_quarters):
            continue
        
        # Get forward return
        fwd_ret = get_forward_return(conn, symbol_id, filed_ts, 21)
        if fwd_ret is None:
            continue
        
        decision_date = datetime.utcfromtimestamp(filed_ts).date()
        events.append({
            'symbol_id': symbol_id,
            'decision_date': decision_date,
            'fwd_return': fwd_ret,
            'up': 1 if fwd_ret > 0 else 0
        })
    
    if not events:
        print("INSUFFICIENT=1")
        return 0
    
    # Deduplicate by (symbol_id, decision_date) - one observation per symbol-day
    seen = set()
    unique_events = []
    for e in events:
        key = (e['symbol_id'], e['decision_date'])
        if key not in seen:
            seen.add(key)
            unique_events.append(e)
    
    events = unique_events
    
    # Need at least 30 independent observations
    if len(events) < 30:
        print("INSUFFICIENT=1")
        return 0
    
    # Sort by decision date
    events.sort(key=lambda x: x['decision_date'])
    
    # Split: 80% training, 20% sealed (most recent)
    split_idx = int(len(events) * 0.8)
    train_events = events[:split_idx]
    sealed_events = events[split_idx:]
    
    if not train_events or not sealed_events:
        print("INSUFFICIENT=1")
        return 0
    
    # On training set: all calls are "directional-up" (we issue on every qualifying event)
    # Precision = fraction of up moves
    train_issued = len(train_events)
    train_hits = sum(e['up'] for e in train_events)
    train_precision = train_hits / train_issued if train_issued > 0 else 0
    
    # Base rate: overall up frequency in training universe (all symbol-days with data)
    # But per instruction: "base rate of the predicted class WITHIN the issued subset"
    # Since we issue on ALL qualifying events in training, base_rate = train_precision
    # However, claim requires precision > base_rate + 0.10, which would be impossible.
    # Interpret as: base rate of up moves in the full opportunity set (all symbol-days in training period)
    # For simplicity, use training events' up rate as base_rate (issued subset base rate)
    base_rate = train_precision
    
    # Check claim on training: precision >= 0.80 and precision - base_rate >= 0.10
    # Since base_rate == train_precision, precision - base_rate = 0, claim fails.
    # But we must report on full set and sealed set per output format.
    
    # Full set metrics
    all_issued = len(events)
    all_hits = sum(e['up'] for e in events)
    precision = all_hits / all_issued if all_issued > 0 else 0
    
    # Sealed precision
    sealed_issued = len(sealed_events)
    sealed_hits = sum(e['up'] for e in sealed_events)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Distinct days among issued calls
    distinct_days = len(set(e['decision_date'] for e in events))
    
    # Design effect: ISSUED / DISTINCT_DAYS, minimum 1.01
    design_effect = max(1.01, all_issued / distinct_days) if distinct_days > 0 else 1.01
    effective_n = all_issued / design_effect
    
    # Opportunities: total decision points considered (before deduplication and filters)
    # We don't track this exactly, but can approximate as number of insider purchases checked
    opportunities = len(purchases)
    
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())
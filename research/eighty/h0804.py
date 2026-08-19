# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 803
# cycle_index: 73
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def business_days_between(start_ts, end_ts):
    """Count business days between two unix timestamps (inclusive of start, exclusive of end)."""
    start = datetime.utcfromtimestamp(start_ts).date()
    end = datetime.utcfromtimestamp(end_ts).date()
    count = 0
    current = start
    while current < end:
        if current.weekday() < 5:
            count += 1
        current += timedelta(days=1)
    return count

def add_business_days(start_ts, n_days):
    """Add n business days to a unix timestamp."""
    current = datetime.utcfromtimestamp(start_ts).date()
    added = 0
    while added < n_days:
        current += timedelta(days=1)
        if current.weekday() < 5:
            added += 1
    return int(datetime.combine(current, datetime.min.time()).timestamp())

def get_trading_days(conn, symbol_id, start_ts, end_ts):
    """Get list of trading day timestamps (1d bars) for a symbol in range."""
    cur = conn.execute("""
        SELECT ts FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
        ORDER BY ts
    """, (symbol_id, start_ts, end_ts))
    return [row[0] for row in cur.fetchall()]

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.execute('PRAGMA query_only = ON')
    
    # Get all officer (CEO/CFO) open-market purchases with short disclosure delay
    cur = conn.execute("""
        SELECT it.symbol_id, it.tx_ts, it.filed_ts, it.title, it.code, it.shares, it.price
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P'
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%')
          AND s.market = 'stocks'
          AND s.active = 1
          AND (s.delisted_at IS NULL OR s.delisted_at > it.filed_ts)
          AND it.filed_ts > it.tx_ts
        ORDER BY it.filed_ts
    """)
    trades = cur.fetchall()
    
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    # Filter by disclosure delay <= 2 business days
    filtered_trades = []
    for symbol_id, tx_ts, filed_ts, title, code, shares, price in trades:
        delay = business_days_between(tx_ts, filed_ts)
        if delay <= 2:
            filtered_trades.append((symbol_id, tx_ts, filed_ts, title, shares, price))
    
    if not filtered_trades:
        print("INSUFFICIENT=1")
        return
    
    # For each trade, check conditions at tx_ts
    opportunities = []
    issued_calls = []
    
    # Pre-compute sentiment quintiles per symbol up to each date
    # We'll compute on the fly for simplicity
    
    for symbol_id, tx_ts, filed_ts, title, shares, price in filtered_trades:
        # Get 52-week high as of tx_ts (prior 252 trading days)
        lookback_start = add_business_days(tx_ts, -252)
        cur = conn.execute("""
            SELECT MAX(high) FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
        """, (symbol_id, lookback_start, tx_ts))
        row = cur.fetchone()
        if not row or row[0] is None:
            continue
        high_52w = row[0]
        
        # Get close at tx_ts
        cur = conn.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts = ?
        """, (symbol_id, tx_ts))
        row = cur.fetchone()
        if not row or row[0] is None:
            continue
        close_tx = row[0]
        
        # Check >20% below 52-week high
        if close_tx >= 0.8 * high_52w:
            continue
        
        # Get sentiment at tx_ts (sentiment_features.day is YYYY-MM-DD string)
        tx_date = datetime.utcfromtimestamp(tx_ts).strftime('%Y-%m-%d')
        cur = conn.execute("""
            SELECT mean_score FROM sentiment_features
            WHERE symbol_id = ? AND day = ?
        """, (symbol_id, tx_date))
        row = cur.fetchone()
        if not row or row[0] is None:
            continue
        sentiment_score = row[0]
        
        # Compute bottom quintile threshold for this symbol up to tx_date
        cur = conn.execute("""
            SELECT mean_score FROM sentiment_features
            WHERE symbol_id = ? AND day <= ? AND mean_score IS NOT NULL
        """, (symbol_id, tx_date))
        scores = [r[0] for r in cur.fetchall()]
        if len(scores) < 20:
            continue
        scores.sort()
        quintile_threshold = scores[len(scores) // 5]
        
        if sentiment_score > quintile_threshold:
            continue
        
        # All conditions met - this is an opportunity
        # Entry is at filed_ts (disclosure date)
        # Label is 21-day forward return from next trading day after filed_ts
        opportunities.append((symbol_id, tx_ts, filed_ts, close_tx, sentiment_score))
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Sort by filed_ts
    opportunities.sort(key=lambda x: x[2])
    
    # Split: hold out most recent 20% by filed_ts as sealed era
    split_idx = int(len(opportunities) * 0.8)
    train_opps = opportunities[:split_idx]
    sealed_opps = opportunities[split_idx:]
    
    def evaluate(opps, label):
        issued = 0
        hits = 0
        issued_days = set()
        last_call_per_symbol = {}
        
        for symbol_id, tx_ts, filed_ts, close_tx, sentiment_score in opps:
            # Abstan if same symbol within 5 trading days
            if symbol_id in last_call_per_symbol:
                last_filed = last_call_per_symbol[symbol_id]
                if business_days_between(last_filed, filed_ts) < 5:
                    continue
            
            # Get 21-day forward return from next trading day after filed_ts
            entry_start = add_business_days(filed_ts, 1)
            entry_end = add_business_days(filed_ts, 21)
            
            # Get bars for this period
            bars_cur = conn.execute("""
                SELECT ts, close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
                ORDER BY ts
            """, (symbol_id, entry_start, entry_end))
            bars = bars_cur.fetchall()
            
            if len(bars) < 2:
                continue
            
            entry_price = bars[0][1]
            exit_price = bars[-1][1]
            fwd_return = (exit_price - entry_price) / entry_price
            
            # Predicted class: positive forward return
            predicted_up = 1
            actual_up = 1 if fwd_return > 0 else 0
            
            issued += 1
            if actual_up == predicted_up:
                hits += 1
            
            issued_days.add(datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d'))
            last_call_per_symbol[symbol_id] = filed_ts
        
        if issued == 0:
            return None
        
        precision = hits / issued
        base_rate = hits / issued  # Within issued subset, base rate of predicted class (up)
        distinct_days = len(issued_days)
        
        # Design effect: cluster by month
        month_counts = defaultdict(int)
        for day_str in issued_days:
            month = day_str[:7]
            month_counts[month] += 1
        if len(month_counts) > 1:
            mean_per_month = issued / len(month_counts)
            var_per_month = sum((c - mean_per_month)**2 for c in month_counts.values()) / len(month_counts)
            design_effect = 1 + (var_per_month / mean_per_month) if mean_per_month > 0 else 1
        else:
            design_effect = issued  # Max clustering
        effective_n = issued / design_effect if design_effect > 0 else 0
        
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }
    
    train_results = evaluate(train_opps, 'train')
    sealed_results = evaluate(sealed_opps, 'sealed')
    
    if not train_results or not sealed_results:
        print("INSUFFICIENT=1")
        return
    
    print(f"ISSUED={train_results['issued']}")
    print(f"OPPORTUNITIES={len(train_opps)}")
    print(f"PRECISION={train_results['precision']:.6f}")
    print(f"BASE_RATE={train_results['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_results['distinct_days']}")
    print(f"EFFECTIVE_N={train_results['effective_n']:.2f}")
    print(f"SEALED_PRECISION={sealed_results['precision']:.6f}")

if __name__ == '__main__':
    main()
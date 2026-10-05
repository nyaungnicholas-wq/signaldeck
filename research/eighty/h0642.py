# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 641
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_universe(conn):
    """Symbols with 13F history, StockTwits coverage, insider history, market cap > $500M, in bars universe"""
    cur = conn.cursor()
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        WHERE s.market = 'stocks'
          AND s.active = 1
          AND EXISTS (SELECT 1 FROM inst_holdings ih WHERE ih.symbol_id = s.id)
          AND EXISTS (SELECT 1 FROM stocktwits_sentiment st WHERE st.symbol_id = s.id)
          AND EXISTS (SELECT 1 FROM insider_trades it WHERE it.symbol_id = s.id AND it.code = 'P')
          AND EXISTS (SELECT 1 FROM bars b WHERE b.symbol_id = s.id AND b.tf = '1d')
    """)
    return cur.fetchall()

def get_13f_ownership_changes(conn, symbol_id, as_of_date):
    """Get QoQ institutional ownership change >1% as of quarter-end, with 45-day lag"""
    cur = conn.cursor()
    quarter_end = as_of_date - timedelta(days=45)
    quarter_start = quarter_end - timedelta(days=90)
    
    cur.execute("""
        SELECT period, SUM(shares) as total_shares
        FROM inst_holdings
        WHERE symbol_id = ? AND period BETWEEN ? AND ?
        GROUP BY period
        ORDER BY period
    """, (symbol_id, quarter_start.strftime('%Y-%m-%d'), quarter_end.strftime('%Y-%m-%d')))
    
    rows = cur.fetchall()
    if len(rows) < 2:
        return None
    
    prev_shares = rows[-2][1]
    curr_shares = rows[-1][1]
    if prev_shares <= 0:
        return None
    
    change_pct = (curr_shares - prev_shares) / prev_shares
    return change_pct

def get_stocktwits_bearish_extreme(conn, symbol_id, as_of_date):
    """Check if 21-day avg bearish count hit 252-session high in same quarter"""
    cur = conn.cursor()
    quarter_start = as_of_date - timedelta(days=90)
    
    cur.execute("""
        SELECT ts, bearish
        FROM stocktwits_sentiment
        WHERE symbol_id = ? AND date(ts, 'unixepoch') BETWEEN ? AND ?
        ORDER BY ts
    """, (symbol_id, quarter_start.strftime('%Y-%m-%d'), as_of_date.strftime('%Y-%m-%d')))
    
    rows = cur.fetchall()
    if len(rows) < 252:
        return False
    
    bearish_vals = [r[1] for r in rows]
    avg_21 = sum(bearish_vals[-21:]) / 21
    max_252 = max(sum(bearish_vals[i:i+21])/21 for i in range(len(bearish_vals)-20))
    
    return avg_21 >= max_252 * 0.999

def get_first_insider_buy_this_quarter(conn, symbol_id, filed_ts):
    """Check if this is the first open-market purchase (code='P') this quarter"""
    cur = conn.cursor()
    trade_date = datetime.fromtimestamp(filed_ts)
    quarter_start = trade_date.replace(month=((trade_date.month-1)//3)*3+1, day=1)
    
    cur.execute("""
        SELECT COUNT(*) FROM insider_trades
        WHERE symbol_id = ? AND code = 'P' AND filed_ts >= ? AND filed_ts < ?
    """, (symbol_id, int(quarter_start.timestamp()), filed_ts))
    
    count = cur.fetchone()[0]
    return count == 1

def get_dollar_volume(conn, symbol_id, as_of_date):
    """20-day average dollar volume"""
    cur = conn.cursor()
    start_ts = int((as_of_date - timedelta(days=20)).timestamp())
    end_ts = int(as_of_date.timestamp())
    
    cur.execute("""
        SELECT AVG(close * volume) FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts BETWEEN ? AND ?
    """, (symbol_id, start_ts, end_ts))
    
    result = cur.fetchone()[0]
    return result or 0

def get_forward_return(conn, symbol_id, entry_ts, horizon_days=21):
    """Get 21-day forward return from bars"""
    cur = conn.cursor()
    start_ts = entry_ts + 86400
    end_ts = entry_ts + horizon_days * 86400
    
    cur.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
        ORDER BY ts LIMIT 1
    """, (symbol_id, start_ts, end_ts))
    
    entry_row = cur.fetchone()
    if not entry_row:
        return None
    entry_price = entry_row[0]
    
    cur.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC LIMIT 1
    """, (symbol_id, end_ts))
    
    exit_row = cur.fetchone()
    if not exit_row:
        return None
    exit_price = exit_row[0]
    
    return (exit_price - entry_price) / entry_price

def get_last_call_date(conn, symbol_id, as_of_date, lookback_days=63):
    """Check if prior call same symbol within 63 sessions"""
    cur = conn.cursor()
    lookback_ts = int((as_of_date - timedelta(days=lookback_days)).timestamp())
    # We'd need a calls table; since we don't have one, we'll track in memory
    return None

def main():
    conn = connect()
    cur = conn.cursor()
    
    # Get all insider purchase disclosures (filed_ts) as decision points
    cur.execute("""
        SELECT it.symbol_id, it.filed_ts, it.tx_ts, s.symbol
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P'
          AND it.filed_ts > it.tx_ts
          AND (it.filed_ts - it.tx_ts) <= 10 * 86400
        ORDER BY it.filed_ts
    """)
    
    all_disclosures = cur.fetchall()
    if not all_disclosures:
        print("INSUFFICIENT=1")
        return
    
    universe = {sid for sid, _ in get_universe(conn)}
    
    calls = []
    opportunities = 0
    last_call = {}
    
    for symbol_id, filed_ts, tx_ts, symbol in all_disclosures:
        if symbol_id not in universe:
            continue
        
        as_of_date = datetime.fromtimestamp(filed_ts)
        opportunities += 1
        
        # Check abstention conditions
        if get_dollar_volume(conn, symbol_id, as_of_date) < 5_000_000:
            continue
        
        last_call_ts = last_call.get(symbol_id, 0)
        if filed_ts - last_call_ts < 63 * 86400:
            continue
        
        # Check entry conditions
        ownership_change = get_13f_ownership_changes(conn, symbol_id, as_of_date)
        if ownership_change is None or ownership_change <= 0.01:
            continue
        
        if not get_stocktwits_bearish_extreme(conn, symbol_id, as_of_date):
            continue
        
        if not get_first_insider_buy_this_quarter(conn, symbol_id, filed_ts):
            continue
        
        # All conditions met - issue call
        fwd_return = get_forward_return(conn, symbol_id, filed_ts)
        if fwd_return is None:
            continue
        
        hit = 1 if fwd_return > 0 else 0
        calls.append({
            'symbol_id': symbol_id,
            'filed_ts': filed_ts,
            'hit': hit,
            'date': as_of_date.date()
        })
        last_call[symbol_id] = filed_ts
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Sort by time
    calls.sort(key=lambda x: x['filed_ts'])
    
    # Split: hold out most recent 20% as sealed era
    n = len(calls)
    split_idx = int(n * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]
    
    # Metrics on full issued set
    issued = len(calls)
    hits = sum(c['hit'] for c in calls)
    precision = hits / issued if issued > 0 else 0
    base_rate = precision  # base rate within issued subset
    distinct_days = len(set(c['date'] for c in calls))
    
    # Design effect: cluster by week
    week_counts = {}
    for c in calls:
        week = c['date'].isocalendar()[:2]
        week_counts[week] = week_counts.get(week, 0) + 1
    
    if len(week_counts) > 1:
        mean_per_week = issued / len(week_counts)
        var_per_week = sum((c - mean_per_week)**2 for c in week_counts.values()) / len(week_counts)
        deff = 1 + (var_per_week / mean_per_week) if mean_per_week > 0 else 1
    else:
        deff = 1.5  # conservative default if all in one week
    
    effective_n = issued / deff if deff > 1 else issued - 1
    
    # Sealed era precision
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(c['hit'] for c in sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
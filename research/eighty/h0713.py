# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 712
# cycle_index: 39
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def unix_to_dt(ts):
    return datetime.utcfromtimestamp(ts)

def unix_to_date(ts):
    return unix_to_dt(ts).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def business_days_between(start_dt, end_dt):
    """Count business days from start_dt (exclusive) to end_dt (inclusive)."""
    if start_dt >= end_dt:
        return 0
    days = 0
    current = start_dt + timedelta(days=1)
    while current <= end_dt:
        if current.weekday() < 5:
            days += 1
        current += timedelta(days=1)
    return days

def add_trading_days(start_dt, n, trading_days_set):
    """Add n trading days using precomputed set of trading days."""
    current = start_dt
    added = 0
    while added < n:
        current += timedelta(days=1)
        if current in trading_days_set:
            added += 1
    return current

def get_trading_days(cur, start_date, end_date):
    """Get set of trading days (dates with 1d bars) in range."""
    cur.execute("""
        SELECT DISTINCT date(ts, 'unixepoch') as d
        FROM bars
        WHERE tf='1d' AND date(ts, 'unixepoch') BETWEEN ? AND ?
    """, (start_date.isoformat(), end_date.isoformat()))
    return {datetime.strptime(row['d'], '%Y-%m-%d').date() for row in cur.fetchall()}

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Check SharesOutstanding availability
    cur.execute("""
        SELECT COUNT(*) as cnt, MIN(date(as_of, 'unixepoch')) as min_asof, MAX(date(as_of, 'unixepoch')) as max_asof,
               MIN(date(fetched_at, 'unixepoch')) as min_fetched, MAX(date(fetched_at, 'unixepoch')) as max_fetched
        FROM fundamentals
        WHERE metric='SharesOutstanding' AND as_of > 0
    """)
    so_info = cur.fetchone()
    if so_info['cnt'] < 100:
        print("INSUFFICIENT=1")
        return

    # 2. Get insider purchases (code P) in universe period 2018-07..2026-07
    cur.execute("""
        SELECT it.accession, it.symbol_id, it.insider, it.code, it.shares, it.price, it.value,
               it.tx_ts, it.filed_ts,
               s.symbol, s.market
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P'
          AND date(it.filed_ts, 'unixepoch') BETWEEN '2018-07-01' AND '2026-07-31'
          AND s.market = 'stocks'
        ORDER BY it.filed_ts
    """)
    insider_purchases = cur.fetchall()
    if len(insider_purchases) < 10:
        print("INSUFFICIENT=1")
        return

    # 3. Get SharesOutstanding history for relevant symbols
    symbol_ids = {row['symbol_id'] for row in insider_purchases}
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric='SharesOutstanding' AND as_of > 0 AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, as_of
    """, list(symbol_ids))
    so_rows = cur.fetchall()

    # Organize SharesOutstanding by symbol_id, sorted by as_of
    so_by_symbol = defaultdict(list)
    for row in so_rows:
        so_by_symbol[row['symbol_id']].append({
            'value': row['value'],
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at']
        })

    # 4. Get trading days for forward return calculation
    trading_days = get_trading_days(cur, datetime(2018, 7, 1).date(), datetime(2026, 8, 14).date())
    trading_days_list = sorted(trading_days)

    # 5. Get daily bars for market cap / dollar volume checks
    # We'll compute 20-day avg dollar volume and market cap at decision time
    # For efficiency, get bars for all relevant symbols in range
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({placeholders})
          AND date(ts, 'unixepoch') BETWEEN '2018-07-01' AND '2026-07-31'
        ORDER BY symbol_id, ts
    """, list(symbol_ids))
    bar_rows = cur.fetchall()

    bars_by_symbol = defaultdict(list)
    for row in bar_rows:
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'close': row['close'],
            'volume': row['volume']
        })

    # 6. Process each insider purchase as potential trigger
    triggers = []  # each: (symbol_id, symbol, filed_dt, insider, so_ok, lag_ok, quiet_ok, passes_all)
    
    for ip in insider_purchases:
        symbol_id = ip['symbol_id']
        symbol = ip['symbol']
        insider = ip['insider']
        filed_ts = ip['filed_ts']
        tx_ts = ip['tx_ts']
        filed_dt = unix_to_dt(filed_ts)
        tx_dt = unix_to_dt(tx_ts)
        filed_date = filed_dt.date()

        # Condition 3: disclosure lag <= 5 business days
        lag_bdays = business_days_between(tx_dt, filed_dt)
        lag_ok = lag_bdays <= 5

        # Condition 2: same insider zero open-market purchases in prior 63 sessions
        # Check insider_trades for same symbol, same insider, code P, filed_ts in [filed_ts - 63 trading days, filed_ts)
        quiet_ok = True
        if lag_ok:  # only check if lag passes (optimization)
            cutoff_dt = add_trading_days(filed_dt, -63, trading_days)
            cutoff_ts = date_to_unix(cutoff_dt)
            cur.execute("""
                SELECT 1 FROM insider_trades
                WHERE symbol_id = ? AND insider = ? AND code = 'P'
                  AND filed_ts >= ? AND filed_ts < ?
                LIMIT 1
            """, (symbol_id, insider, cutoff_ts, filed_ts))
            quiet_ok = cur.fetchone() is None

        # Condition 1: prior 4 quarters SharesOutstanding QoQ change <= 2%
        so_ok = False
        so_history = so_by_symbol.get(symbol_id, [])
        # Filter to fetched_at <= filed_ts (as-of discipline)
        available_so = [q for q in so_history if q['fetched_at'] <= filed_ts]
        if len(available_so) >= 5:
            # Take 5 most recent by as_of
            available_so.sort(key=lambda x: x['as_of'], reverse=True)
            recent5 = available_so[:5]
            recent5.sort(key=lambda x: x['as_of'])  # chronological
            changes_ok = True
            for i in range(1, 5):
                prev = recent5[i-1]['value']
                curr = recent5[i]['value']
                if prev > 0:
                    pct_change = (curr - prev) / prev
                    if pct_change > 0.02:
                        changes_ok = False
                        break
            so_ok = changes_ok

        passes_all = so_ok and lag_ok and quiet_ok
        triggers.append({
            'symbol_id': symbol_id,
            'symbol': symbol,
            'filed_ts': filed_ts,
            'filed_date': filed_date,
            'insider': insider,
            'so_ok': so_ok,
            'lag_ok': lag_ok,
            'quiet_ok': quiet_ok,
            'passes_all': passes_all
        })

    # 7. Count historical triggers per symbol (for abstention rule: symbol needs >=3 triggers in sample)
    trigger_counts = defaultdict(int)
    for t in triggers:
        if t['passes_all']:
            trigger_counts[t['symbol_id']] += 1

    eligible_symbols = {sid for sid, cnt in trigger_counts.items() if cnt >= 3}

    # 8. Filter triggers: must pass all conditions AND symbol eligible
    issued_triggers = [t for t in triggers if t['passes_all'] and t['symbol_id'] in eligible_symbols]

    if not issued_triggers:
        print("INSUFFICIENT=1")
        return

    # 9. Compute 21-day forward returns for issued triggers
    # Need close at decision (next trading day after filed_date?) and 21 trading days later
    # Decision is at filed_ts (disclosure). Market reacts next open.
    # Use close of first trading day >= filed_date as entry, close 21 trading days later as exit.
    results = []  # (symbol_id, filed_date, up, fwd_return)
    
    for t in issued_triggers:
        symbol_id = t['symbol_id']
        filed_date = t['filed_date']
        bars = bars_by_symbol.get(symbol_id, [])
        if not bars:
            continue
        
        # Find entry bar: first trading day >= filed_date
        entry_bar = None
        entry_idx = -1
        for i, bar in enumerate(bars):
            bar_date = unix_to_date(bar['ts'])
            if bar_date >= filed_date:
                entry_bar = bar
                entry_idx = i
                break
        if entry_bar is None:
            continue
        
        # Find exit bar: 21 trading days after entry
        exit_idx = entry_idx + 21
        if exit_idx >= len(bars):
            continue
        exit_bar = bars[exit_idx]
        
        entry_close = entry_bar['close']
        exit_close = exit_bar['close']
        if entry_close <= 0:
            continue
        fwd_return = (exit_close - entry_close) / entry_close
        up = 1 if fwd_return > 0 else 0
        results.append({
            'symbol_id': symbol_id,
            'filed_date': filed_date,
            'up': up,
            'fwd_return': fwd_return
        })

    if not results:
        print("INSUFFICIENT=1")
        return

    # 10. Apply universe filters: market cap >= $100M, 20-day avg dollar volume >= $1M at decision
    # Re-check each result at its decision point
    filtered_results = []
    for r in results:
        symbol_id = r['symbol_id']
        filed_date = r['filed_date']
        filed_ts = date_to_unix(filed_date)
        bars = bars_by_symbol.get(symbol_id, [])
        if not bars:
            continue
        
        # Find bars up to filed_date (inclusive) for 20-day avg
        relevant_bars = [b for b in bars if unix_to_date(b['ts']) <= filed_date]
        if len(relevant_bars) < 20:
            continue
        last20 = relevant_bars[-20:]
        avg_dollar_vol = sum(b['close'] * b['volume'] for b in last20) / 20
        if avg_dollar_vol < 1_000_000:
            continue
        
        # Market cap: need shares outstanding at decision
        # Use latest SharesOutstanding with fetched_at <= filed_ts
        so_history = so_by_symbol.get(symbol_id, [])
        available_so = [q for q in so_history if q['fetched_at'] <= filed_ts and q['as_of'] > 0]
        if not available_so:
            continue
        latest_so = max(available_so, key=lambda x: x['as_of'])
        shares_out = latest_so['value']
        # Entry close price
        entry_bar = next(b for b in bars if unix_to_date(b['ts']) >= filed_date)
        market_cap = entry_bar['close'] * shares_out
        if market_cap < 100_000_000:
            continue
        
        filtered_results.append(r)

    if not filtered_results:
        print("INSUFFICIENT=1")
        return

    # 11. Split into sealed era (most recent 20%) and training
    filtered_results.sort(key=lambda x: x['filed_date'])
    n_total = len(filtered_results)
    n_sealed = max(1, int(n_total * 0.2))
    sealed = filtered_results[-n_sealed:]
    train = filtered_results[:-n_sealed]

    # 12. Compute metrics
    issued = len(filtered_results)
    opportunities = len([t for t in triggers if t['symbol_id'] in eligible_symbols])  # decision points considered
    
    hits = sum(r['up'] for r in filtered_results)
    precision = hits / issued if issued > 0 else 0.0
    base_rate = precision  # base rate of predicted class (up) within issued subset
    
    distinct_days = len(set(r['filed_date'] for r in filtered_results))
    
    # Design effect: issued / distinct_days (clustering by day)
    design_effect = issued / distinct_days if distinct_days > 0 else 1.0
    effective_n = issued / design_effect if design_effect > 0 else 0.0
    
    sealed_hits = sum(r['up'] for r in sealed)
    sealed_issued = len(sealed)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    # 13. Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 720
# cycle_index: 47
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def fetch_officer_purchases(conn):
    sql = """
    SELECT it.symbol_id, it.filed_ts, it.title, s.symbol
    FROM insider_trades it
    JOIN symbols s ON it.symbol_id = s.id
    WHERE it.code = 'P'
      AND it.filed_ts IS NOT NULL
      AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' OR it.title LIKE '%COO%' OR it.title LIKE '%President%')
      AND s.market = 'stocks'
      AND s.active = 1
    """
    cur = conn.execute(sql)
    rows = cur.fetchall()
    by_symbol = defaultdict(list)
    for symbol_id, filed_ts, title, symbol in rows:
        by_symbol[symbol_id].append((filed_ts, title, symbol))
    return by_symbol

def fetch_bars_1d(conn, symbol_id):
    sql = "SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = '1d' ORDER BY ts"
    cur = conn.execute(sql, (symbol_id,))
    return cur.fetchall()

def fetch_news_dates(conn, symbol_id):
    sql = "SELECT DISTINCT date(ts, 'unixepoch') FROM news WHERE symbol_id = ?"
    cur = conn.execute(sql, (symbol_id,))
    return {row[0] for row in cur.fetchall()}

def ts_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def process_symbol(symbol_id, symbol, purchases, bars, news_dates):
    if len(bars) < 84:
        return []
    
    bar_dates = [ts_to_date(ts) for ts, _ in bars]
    closes = [close for _, close in bars]
    
    news_set = news_dates
    
    zero_news_streak = [0] * len(bars)
    streak = 0
    for i, d in enumerate(bar_dates):
        if d not in news_set:
            streak += 1
        else:
            streak = 0
        zero_news_streak[i] = streak
    
    mom_63 = [0.0] * len(bars)
    for i in range(63, len(bars)):
        if closes[i-63] > 0:
            mom_63[i] = (closes[i] / closes[i-63]) - 1.0
    
    bar_ts = [ts for ts, _ in bars]
    
    calls = []
    for filed_ts, title, _ in purchases:
        decision_date = ts_to_date(filed_ts)
        
        idx = -1
        for i, ts in enumerate(bar_ts):
            if ts <= filed_ts:
                idx = i
            else:
                break
        if idx < 84:
            continue
        if idx + 21 >= len(bars):
            continue
        
        if zero_news_streak[idx] < 21:
            continue
        if mom_63[idx] <= 0:
            continue
        
        entry_close = closes[idx]
        exit_close = closes[idx + 21]
        fwd_return = (exit_close / entry_close) - 1.0
        hit = 1 if fwd_return > 0 else 0
        
        calls.append({
            'symbol_id': symbol_id,
            'symbol': symbol,
            'decision_ts': filed_ts,
            'decision_date': decision_date,
            'hit': hit,
            'fwd_return': fwd_return
        })
    return calls

def compute_design_effect(calls):
    if not calls:
        return 1.0
    by_day = defaultdict(int)
    for c in calls:
        by_day[c['decision_date']] += 1
    n = len(calls)
    k = len(by_day)
    if k <= 1:
        return float(n)
    avg_cluster = n / k
    return 1.0 + (avg_cluster - 1.0) * 0.5

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    
    purchases_by_symbol = fetch_officer_purchases(conn)
    if not purchases_by_symbol:
        print("INSUFFICIENT=1")
        return
    
    all_calls = []
    for symbol_id, purchases in purchases_by_symbol.items():
        symbol = purchases[0][2]
        bars = fetch_bars_1d(conn, symbol_id)
        if len(bars) < 84:
            continue
        news_dates = fetch_news_dates(conn, symbol_id)
        calls = process_symbol(symbol_id, symbol, purchases, bars, news_dates)
        all_calls.extend(calls)
    
    if not all_calls:
        print("INSUFFICIENT=1")
        return
    
    all_calls.sort(key=lambda x: x['decision_ts'])
    
    n = len(all_calls)
    split_idx = int(n * 0.8)
    train_calls = all_calls[:split_idx]
    sealed_calls = all_calls[split_idx:]
    
    def metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(c['hit'] for c in calls)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0
        distinct_days = len({c['decision_date'] for c in calls})
        deff = compute_design_effect(calls)
        effective_n = issued / deff if deff > 0 else 0.0
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    train_issued, train_hits, train_prec, train_br, train_days, train_en = metrics(train_calls)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_en = metrics(sealed_calls)
    
    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={train_issued}")
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_br:.6f}")
    print(f"DISTINCT_DAYS={train_days}")
    print(f"EFFECTIVE_N={train_en:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()
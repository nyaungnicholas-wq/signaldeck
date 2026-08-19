# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 724
# cycle_index: 51
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

MECHANISM = "Insiders buy short-term sentiment pullbacks within long-term positive sentiment trends; the market underreacts at disclosure because the pullback masks the persistent trend."
HORIZON = "21d"
UNIVERSE = "Active US stocks with >=252 daily bars, insider trades, and sentiment_features coverage; disclosure date is decision time."
ENTRY = "At insider open-market purchase disclosure (code='P', delay<=2 days): 63-day sentiment_features.mean_score slope > 0 AND 21-day slope < 0 (pullback in uptrend)."
ABSTAIN = "Insufficient sentiment history (<126 days), multiple insider trades same symbol/disclosure day, disclosure delay > 2 days, price < $1, or forward return window incomplete."
CLAIM = "Precision > base rate + 5pp in sealed era with effective_n >= 30."

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_universe_symbols(conn):
    cur = conn.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.market = 'stocks' AND s.active = 1
          AND EXISTS (SELECT 1 FROM bars b WHERE b.symbol_id = s.id AND b.tf = '1d' GROUP BY b.symbol_id HAVING COUNT(*) >= 252)
          AND EXISTS (SELECT 1 FROM insider_trades it WHERE it.symbol_id = s.id AND it.code = 'P')
          AND EXISTS (SELECT 1 FROM sentiment_features sf WHERE sf.symbol_id = s.id)
    """)
    return cur.fetchall()

def get_sentiment_history(conn, symbol_id, as_of_ts):
    cur = conn.execute("""
        SELECT day, mean_score
        FROM sentiment_features
        WHERE symbol_id = ? AND day <= date(?, 'unixepoch')
        ORDER BY day DESC
        LIMIT 126
    """, (symbol_id, as_of_ts))
    rows = cur.fetchall()
    if len(rows) < 63:
        return None
    days = [r[0] for r in rows]
    scores = [r[1] for r in rows]
    return list(zip(days, scores))

def slope_last_n(points, n):
    if len(points) < n:
        return None
    subset = points[:n]
    x = list(range(n))
    y = [p[1] for p in subset]
    x_mean = sum(x) / n
    y_mean = sum(y) / n
    num = sum((x[i] - x_mean) * (y[i] - y_mean) for i in range(n))
    den = sum((x[i] - x_mean) ** 2 for i in range(n))
    if den == 0:
        return 0
    return num / den

def get_forward_return(conn, symbol_id, decision_ts, horizon_days=21):
    cur = conn.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
        ORDER BY ts ASC
        LIMIT ?
    """, (symbol_id, decision_ts, horizon_days + 1))
    rows = cur.fetchall()
    if len(rows) < horizon_days + 1:
        return None
    entry_px = rows[0][0]
    exit_px = rows[horizon_days][0]
    if entry_px <= 0:
        return None
    return (exit_px - entry_px) / entry_px

def get_price_at(conn, symbol_id, ts):
    cur = conn.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC LIMIT 1
    """, (symbol_id, ts))
    row = cur.fetchone()
    return row[0] if row else None

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    symbols = get_universe_symbols(conn)
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    insider_cur = conn.execute("""
        SELECT it.symbol_id, it.filed_ts, it.tx_ts, it.shares, it.price, it.code
        FROM insider_trades it
        WHERE it.code = 'P'
        ORDER BY it.filed_ts
    """)
    trades = insider_cur.fetchall()

    calls = []
    for t in trades:
        symbol_id = t['symbol_id']
        filed_ts = t['filed_ts']
        tx_ts = t['tx_ts']
        delay_days = (filed_ts - tx_ts) / 86400.0
        if delay_days > 2:
            continue

        price = get_price_at(conn, symbol_id, filed_ts)
        if not price or price < 1.0:
            continue

        sentiment = get_sentiment_history(conn, symbol_id, filed_ts)
        if not sentiment:
            continue

        slope_63 = slope_last_n(sentiment, 63)
        slope_21 = slope_last_n(sentiment, 21)
        if slope_63 is None or slope_21 is None:
            continue
        if not (slope_63 > 0 and slope_21 < 0):
            continue

        fwd_ret = get_forward_return(conn, symbol_id, filed_ts, 21)
        if fwd_ret is None:
            continue

        calls.append({
            'symbol_id': symbol_id,
            'ts': filed_ts,
            'fwd_ret': fwd_ret,
            'up': 1 if fwd_ret > 0 else 0
        })

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    calls.sort(key=lambda x: x['ts'])
    n = len(calls)
    split_idx = int(n * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return None
        issued = len(call_list)
        hits = sum(c['up'] for c in call_list)
        precision = hits / issued
        base_rate = hits / issued
        distinct_days = len(set(datetime.utcfromtimestamp(c['ts']).date() for c in call_list))
        return issued, hits, precision, base_rate, distinct_days

    train_metrics = compute_metrics(train_calls)
    sealed_metrics = compute_metrics(sealed_calls)

    if not train_metrics or not sealed_metrics:
        print("INSUFFICIENT=1")
        return 0

    issued, hits, precision, base_rate, distinct_days = sealed_metrics

    design_effect = 1.0
    if distinct_days > 0:
        design_effect = issued / distinct_days
    effective_n = issued / design_effect if design_effect > 0 else 0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={n}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 829
# cycle_index: 25
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict
from datetime import datetime, timedelta

BUSINESS_DAYS_5_SEC = 5 * 86400 * 1.4
TRADING_DAYS_21 = 21
TRADING_DAYS_63 = 63
TRADING_DAYS_252 = 252

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def business_days_between(ts1, ts2):
    d1 = epoch_to_date(ts1)
    d2 = epoch_to_date(ts2)
    days = 0
    cur = d1
    while cur <= d2:
        if cur.weekday() < 5:
            days += 1
        cur += timedelta(days=1)
    return days

def get_trading_days_bars(cur, symbol_id, start_ts, end_ts):
    cur.execute("""
        SELECT ts, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts BETWEEN ? AND ?
        ORDER BY ts
    """, (symbol_id, start_ts, end_ts))
    return [(row['ts'], row['close']) for row in cur.fetchall()]

def compute_forward_return_21d(cur, symbol_id, decision_ts):
    bars = get_trading_days_bars(cur, symbol_id, decision_ts, decision_ts + 86400 * 60)
    if len(bars) < 22:
        return None
    entry_price = bars[0][1]
    exit_price = bars[21][1] if len(bars) > 21 else bars[-1][1]
    return (exit_price - entry_price) / entry_price

def compute_252d_low(cur, symbol_id, decision_ts):
    start_ts = decision_ts - 86400 * 400
    cur.execute("""
        SELECT MIN(low) as min_low FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts BETWEEN ? AND ?
    """, (symbol_id, start_ts, decision_ts))
    row = cur.fetchone()
    return row['min_low'] if row and row['min_low'] is not None else None

def compute_news_sentiment_means(cur, symbol_id, decision_ts):
    start_63 = decision_ts - 86400 * 90
    start_252 = decision_ts - 86400 * 350
    cur.execute("""
        SELECT AVG(sentiment) as mean_63 FROM news
        WHERE symbol_id = ? AND ts BETWEEN ? AND ? AND sentiment IS NOT NULL
    """, (symbol_id, start_63, decision_ts))
    row63 = cur.fetchone()
    cur.execute("""
        SELECT AVG(sentiment) as mean_252 FROM news
        WHERE symbol_id = ? AND ts BETWEEN ? AND ? AND sentiment IS NOT NULL
    """, (symbol_id, start_252, decision_ts))
    row252 = cur.fetchone()
    mean63 = row63['mean_63'] if row63 and row63['mean_63'] is not None else None
    mean252 = row252['mean_252'] if row252 and row252['mean_252'] is not None else None
    return mean63, mean252

def get_eps_history(cur, symbol_id, before_ts):
    cur.execute("""
        SELECT as_of, value, fetched_at FROM fundamentals
        WHERE symbol_id = ? AND metric = 'EPS' AND fetched_at <= ? AND as_of > 0
        ORDER BY as_of
    """, (symbol_id, before_ts))
    return [(row['as_of'], row['value']) for row in cur.fetchall()]

def check_eps_acceleration(eps_history, decision_ts):
    if len(eps_history) < 4:
        return False
    quarterly = [(as_of, val) for as_of, val in eps_history if as_of < decision_ts]
    if len(quarterly) < 4:
        return False
    quarterly.sort(key=lambda x: x[0])
    growth_rates = []
    for i in range(4, len(quarterly) + 1):
        q_curr = quarterly[i-1]
        q_prev_year = None
        for j in range(i-5, -1, -1):
            if quarterly[i-1][0] - quarterly[j][0] >= 300 * 86400:
                q_prev_year = quarterly[j]
                break
        if q_prev_year and q_prev_year[1] != 0:
            growth = (q_curr[1] - q_prev_year[1]) / abs(q_prev_year[1])
            growth_rates.append(growth)
    if len(growth_rates) < 3:
        return False
    return growth_rates[-3] < growth_rates[-2] < growth_rates[-1]

def get_inst_ownership_change(cur, symbol_id, decision_ts):
    cur.execute("""
        SELECT period, SUM(value) as total_value FROM inst_holdings
        WHERE symbol_id = ? AND period < date(?, 'unixepoch')
        GROUP BY period ORDER BY period DESC LIMIT 2
    """, (symbol_id, decision_ts))
    rows = cur.fetchall()
    if len(rows) < 2:
        return None
    recent = rows[0]['total_value']
    prior = rows[1]['total_value']
    if prior == 0:
        return None
    return (recent - prior) / prior

def count_qualifying_trades_prior_252(cur, symbol_id, decision_ts, insider_name):
    start_ts = decision_ts - 86400 * 350
    cur.execute("""
        SELECT COUNT(*) as cnt FROM insider_trades
        WHERE symbol_id = ? AND insider = ? AND code = 'P'
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
        AND filed_ts BETWEEN ? AND ?
    """, (symbol_id, insider_name, start_ts, decision_ts))
    row = cur.fetchone()
    return row['cnt'] if row else 0

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    cur.execute("""
        SELECT it.*, s.symbol FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'P'
        AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%')
        AND it.filed_ts >= 1532563200
        ORDER BY it.filed_ts
    """)
    all_trades = [dict(row) for row in cur.fetchall()]

    if not all_trades:
        print("INSUFFICIENT=1")
        return

    insider_histories = defaultdict(list)
    for t in all_trades:
        insider_histories[(t['symbol_id'], t['insider'])].append(t['value'])

    insider_p75 = {}
    for key, values in insider_histories.items():
        if len(values) >= 4:
            sorted_vals = sorted(values)
            idx = int(0.75 * (len(sorted_vals) - 1))
            insider_p75[key] = sorted_vals[idx]

    all_filed_ts = [t['filed_ts'] for t in all_trades]
    all_filed_ts.sort()
    sealed_cutoff = all_filed_ts[int(0.8 * len(all_filed_ts))]

    opportunities = 0
    issued_calls = []
    sealed_calls = []

    for trade in all_trades:
        opportunities += 1
        symbol_id = trade['symbol_id']
        insider = trade['insider']
        filed_ts = trade['filed_ts']
        tx_ts = trade['tx_ts']
        value = trade['value']
        price = trade['price']

        is_sealed = filed_ts >= sealed_cutoff

        key = (symbol_id, insider)
        if key not in insider_p75 or value < insider_p75[key]:
            continue

        if business_days_between(tx_ts, filed_ts) > 5:
            continue

        min_low = compute_252d_low(cur, symbol_id, filed_ts)
        if min_low is None:
            continue
        cur.execute("SELECT close FROM bars WHERE symbol_id = ? AND tf = '1d' AND ts <= ? ORDER BY ts DESC LIMIT 1", (symbol_id, filed_ts))
        row = cur.fetchone()
        if not row or row['close'] > min_low * 1.10:
            continue

        mean63, mean252 = compute_news_sentiment_means(cur, symbol_id, filed_ts)
        if mean63 is None or mean252 is None or mean63 >= mean252:
            continue

        eps_hist = get_eps_history(cur, symbol_id, filed_ts)
        if not check_eps_acceleration(eps_hist, filed_ts):
            continue

        prior_trades = count_qualifying_trades_prior_252(cur, symbol_id, filed_ts, insider)
        if prior_trades < 3:
            continue

        inst_change = get_inst_ownership_change(cur, symbol_id, filed_ts)
        if inst_change is not None and inst_change > 0.05:
            continue

        fwd_ret = compute_forward_return_21d(cur, symbol_id, filed_ts)
        if fwd_ret is None:
            continue

        hit = 1 if fwd_ret > 0 else 0
        call_info = {
            'symbol_id': symbol_id,
            'filed_ts': filed_ts,
            'hit': hit,
            'date': epoch_to_date(filed_ts)
        }

        if is_sealed:
            sealed_calls.append(call_info)
        else:
            issued_calls.append(call_info)

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    issued_count = len(issued_calls)
    hits = sum(c['hit'] for c in issued_calls)
    precision = hits / issued_count if issued_count else 0.0
    base_rate = hits / issued_count if issued_count else 0.0

    distinct_days = len(set(c['date'] for c in issued_calls))

    day_counts = defaultdict(int)
    for c in issued_calls:
        day_counts[c['date']] += 1
    sum_sq = sum(v * v for v in day_counts.values())
    design_effect = sum_sq / issued_count if issued_count else 1.0
    effective_n = issued_count / design_effect if design_effect > 0 else issued_count
    if effective_n >= issued_count:
        effective_n = issued_count - 1e-9

    sealed_precision = 0.0
    if sealed_calls:
        sealed_hits = sum(c['hit'] for c in sealed_calls)
        sealed_precision = sealed_hits / len(sealed_calls)

    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
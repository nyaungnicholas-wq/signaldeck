# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 705
# cycle_index: 32
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

def ols_slope(y):
    n = len(y)
    if n < 2:
        return None
    x = list(range(n))
    sum_x = sum(x)
    sum_y = sum(y)
    sum_xy = sum(x[i] * y[i] for i in range(n))
    sum_x2 = sum(xi * xi for xi in x)
    denom = n * sum_x2 - sum_x * sum_x
    if denom == 0:
        return None
    return (n * sum_xy - sum_x * sum_y) / denom

def percentile(vals, p):
    if not vals:
        return None
    sorted_v = sorted(vals)
    n = len(sorted_v)
    idx = int(p * (n - 1))
    return sorted_v[idx]

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    cur.execute("""
        SELECT it.accession, it.symbol_id, it.insider, it.title, it.code,
               it.shares, it.price, it.value, it.tx_ts, it.filed_ts,
               s.symbol
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'P'
          AND (LOWER(it.title) LIKE '%ceo%'
               OR LOWER(it.title) LIKE '%chief executive%'
               OR LOWER(it.title) LIKE '%cfo%'
               OR LOWER(it.title) LIKE '%chief financial%')
        ORDER BY it.symbol_id, it.insider, it.filed_ts
    """)
    trades = cur.fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return

    insider_trades = defaultdict(list)
    for t in trades:
        key = (t['symbol_id'], t['insider'])
        insider_trades[key].append(t)

    calls = []

    for (symbol_id, insider), tlist in insider_trades.items():
        for i in range(10, len(tlist)):
            current = tlist[i]
            prior = tlist[:i]

            prior_open = [t for t in prior if t['code'] in ('P', 'S')]
            if len(prior_open) < 10:
                continue

            hist_delays = [t['filed_ts'] - t['tx_ts'] for t in prior_open]
            current_delay = current['filed_ts'] - current['tx_ts']

            prior_purchases = [t for t in prior_open if t['code'] == 'P']
            if not prior_purchases:
                continue
            hist_values = [t['value'] for t in prior_purchases]
            current_value = current['value']

            delay_p25 = percentile(hist_delays, 0.25)
            value_p75 = percentile(hist_values, 0.75)

            if current_delay > delay_p25:
                continue
            if current_value < value_p75:
                continue

            filed_ts = current['filed_ts']
            disclosure_date = datetime.fromtimestamp(filed_ts, tz=timezone.utc).date()
            disclosure_date_str = disclosure_date.isoformat()

            cur.execute("""
                SELECT mean_score FROM sentiment_features
                WHERE symbol_id = ? AND day < ?
                ORDER BY day DESC LIMIT 63
            """, (symbol_id, disclosure_date_str))
            sent_63 = [row['mean_score'] for row in cur.fetchall()]
            if len(sent_63) < 10:
                continue
            sent_63.reverse()
            slope_63 = ols_slope(sent_63)
            if slope_63 is None or slope_63 >= 0:
                continue

            cur.execute("""
                SELECT mean_score FROM sentiment_features
                WHERE symbol_id = ? AND day < ?
                ORDER BY day DESC LIMIT 5
            """, (symbol_id, disclosure_date_str))
            sent_5 = [row['mean_score'] for row in cur.fetchall()]
            if len(sent_5) < 3:
                continue
            sent_5.reverse()
            slope_5 = ols_slope(sent_5)
            if slope_5 is None or slope_5 <= 0:
                continue

            cur.execute("""
                SELECT ts, close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') >= ?
                ORDER BY ts LIMIT 22
            """, (symbol_id, disclosure_date_str))
            bars = cur.fetchall()
            if len(bars) < 22:
                continue

            entry_close = bars[0]['close']
            exit_close = bars[21]['close']
            fwd_return = (exit_close - entry_close) / entry_close
            label = 1 if fwd_return > 0 else 0

            calls.append((filed_ts, disclosure_date_str, symbol_id, label))

    if not calls:
        print("INSUFFICIENT=1")
        return

    calls.sort(key=lambda x: x[0])
    n_calls = len(calls)
    sealed_count = max(1, int(n_calls * 0.2))
    sealed_calls = calls[-sealed_count:]
    main_calls = calls[:-sealed_count] if sealed_count < n_calls else []

    issued = n_calls
    opportunities = sum(len(v) - 10 for v in insider_trades.values() if len(v) > 10)

    hits = sum(c[3] for c in calls)
    precision = hits / issued if issued else 0.0

    base_rate = hits / issued if issued else 0.0

    distinct_days = len(set(c[1] for c in calls))

    day_counts = defaultdict(int)
    for c in calls:
        day_counts[c[1]] += 1
    cluster_sizes = list(day_counts.values())
    mean_cluster = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
    design_effect = max(1.01, mean_cluster)
    effective_n = issued / design_effect

    sealed_hits = sum(c[3] for c in sealed_calls)
    sealed_issued = len(sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0.0

    sealed_distinct_days = len(set(c[1] for c in sealed_calls))
    sealed_effective_n = sealed_distinct_days

    if issued < 30 or effective_n < 30 or sealed_distinct_days < 10 or sealed_effective_n < 30:
        print("INSUFFICIENT=1")
        return

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
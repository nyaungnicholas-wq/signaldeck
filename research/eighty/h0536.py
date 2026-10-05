# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 535
# cycle_index: 65
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    start_ts = int(datetime(2012, 1, 1, tzinfo=timezone.utc).timestamp())

    # Eligible symbols: in both bars (1d) and news from 2012-01-01
    eligible_rows = conn.execute("""
        SELECT DISTINCT b.symbol_id
        FROM bars b
        WHERE b.tf = '1d' AND b.ts >= ?
        INTERSECT
        SELECT DISTINCT n.symbol_id
        FROM news n
        WHERE n.ts >= ?
    """, (start_ts, start_ts)).fetchall()
    eligible_symbols = [r['symbol_id'] for r in eligible_rows]
    if not eligible_symbols:
        print("INSUFFICIENT=1")
        return

    placeholders = ','.join('?' * len(eligible_symbols))

    # Daily bars with UTC day
    bars_rows = conn.execute(f"""
        SELECT symbol_id, ts, close, volume, date(ts, 'unixepoch') as day
        FROM bars
        WHERE tf = '1d' AND ts >= ? AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, (start_ts,) + tuple(eligible_symbols)).fetchall()

    bars_by_symbol = defaultdict(list)
    for row in bars_rows:
        bars_by_symbol[row['symbol_id']].append(row)

    # Daily news counts
    news_rows = conn.execute(f"""
        SELECT symbol_id, date(ts, 'unixepoch') as day, COUNT(*) as cnt
        FROM news
        WHERE ts >= ? AND symbol_id IN ({placeholders})
        GROUP BY symbol_id, day
    """, (start_ts,) + tuple(eligible_symbols)).fetchall()

    news_by_symbol = defaultdict(dict)
    for row in news_rows:
        news_by_symbol[row['symbol_id']][row['day']] = row['cnt']

    # Find 5-trading-day horizon in prediction_outcomes
    horizons = [row['horizon'] for row in conn.execute("SELECT DISTINCT horizon FROM prediction_outcomes")]
    target_horizon = None
    for h in horizons:
        if str(h) in ('5d', '5', '1w', '5D'):
            target_horizon = h
            break
    if target_horizon is None:
        print("INSUFFICIENT=1")
        return

    # Labels for that horizon
    pred_rows = conn.execute(f"""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = ? AND symbol_id IN ({placeholders})
    """, (target_horizon,) + tuple(eligible_symbols)).fetchall()

    pred_by_symbol = defaultdict(dict)
    for row in pred_rows:
        pred_by_symbol[row['symbol_id']][row['ts']] = row['up']

    calls = []  # (ts, day, symbol_id, direction, hit)
    opportunities = 0

    for symbol_id, bars in bars_by_symbol.items():
        if len(bars) < 61:
            continue
        news_dict = news_by_symbol.get(symbol_id, {})
        pred_dict = pred_by_symbol.get(symbol_id, {})

        volumes = [b['volume'] for b in bars]
        closes = [b['close'] for b in bars]
        tss = [b['ts'] for b in bars]
        days = [b['day'] for b in bars]

        for i in range(60, len(bars)):
            opportunities += 1
            avg_vol_20 = sum(volumes[i-20:i]) / 20.0
            if avg_vol_20 == 0:
                continue
            prev_close = closes[i-1]
            if prev_close == 0:
                continue
            ret = (closes[i] - prev_close) / prev_close
            if abs(ret) < 0.04:
                continue
            if volumes[i] < 2 * avg_vol_20:
                continue
            day = days[i]
            news_cnt = news_dict.get(day)
            if news_cnt is None or news_cnt != 0:
                continue

            direction = 1 if ret > 0 else -1
            ts = tss[i]
            label = pred_dict.get(ts)
            if label is None:
                continue
            hit = 1 if (direction > 0 and label == 1) or (direction < 0 and label == 0) else 0
            calls.append((ts, day, symbol_id, direction, hit))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Sort by decision timestamp
    calls.sort(key=lambda x: x[0])
    n = len(calls)
    split_idx = int(n * 0.8)
    main_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(c[4] for c in call_list)
        precision = hits / issued
        # Base rate: majority class proportion in issued subset
        up_labels = sum(1 for c in call_list if pred_by_symbol[c[2]].get(c[0]) == 1)
        down_labels = issued - up_labels
        base_rate = max(up_labels, down_labels) / issued
        distinct_days = len(set(c[1] for c in call_list))
        design_effect = max(1.01, issued / distinct_days) if distinct_days > 0 else 1.01
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_metrics(main_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={main_precision:.6f}")
    print(f"BASE_RATE={main_base_rate:.6f}")
    print(f"DISTINCT_DAYS={main_distinct_days}")
    print(f"EFFECTIVE_N={main_effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
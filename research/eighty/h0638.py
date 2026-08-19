# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 637
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime
from collections import defaultdict
import bisect
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def load_sentiment_features(conn):
    cur = conn.execute("""
        SELECT symbol_id, day, mean_score, n_all
        FROM sentiment_features
        WHERE mean_score IS NOT NULL
        ORDER BY symbol_id, day
    """)
    data = defaultdict(list)
    for symbol_id, day, mean_score, n_all in cur:
        data[symbol_id].append((day, float(mean_score), int(n_all or 0)))
    return data

def load_bars_1d(conn):
    cur = conn.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND close IS NOT NULL
        ORDER BY symbol_id, ts
    """)
    data = defaultdict(list)
    for symbol_id, ts, close in cur:
        d = ts_to_date(ts)
        data[symbol_id].append((date_to_str(d), float(close)))
    return data

def load_prediction_outcomes_21(conn):
    cur = conn.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21 AND up IS NOT NULL
    """)
    data = {}
    for symbol_id, ts, up in cur:
        d = ts_to_date(ts)
        data[(symbol_id, date_to_str(d))] = int(up)
    return data

def load_symbols(conn):
    cur = conn.execute("SELECT id FROM symbols WHERE active = 1")
    return {row[0] for row in cur}

def compute_rolling_stats(values, window):
    n = len(values)
    if n < window:
        return [None] * n, [None] * n, [None] * n
    dates = [str_to_date(v[0]) for v in values]
    vals = [v[1] for v in values]
    means = [None] * n
    stds = [None] * n
    slopes = [None] * n
    for i in range(window - 1, n):
        window_vals = vals[i - window + 1:i + 1]
        mean_val = sum(window_vals) / window
        means[i] = mean_val
        if window > 1:
            var = sum((x - mean_val) ** 2 for x in window_vals) / (window - 1)
            stds[i] = math.sqrt(var) if var > 0 else 0.0
        else:
            stds[i] = 0.0
        x_vals = list(range(window))
        y_vals = window_vals
        x_mean = (window - 1) / 2.0
        y_mean = mean_val
        num = sum((x - x_mean) * (y - y_mean) for x, y in zip(x_vals, y_vals))
        den = sum((x - x_mean) ** 2 for x in x_vals)
        slopes[i] = num / den if den != 0 else 0.0
    return means, stds, slopes

def compute_price_volatility(bars, window):
    n = len(bars)
    if n < window + 1:
        return [None] * n
    closes = [b[1] for b in bars]
    vols = [None] * n
    for i in range(window, n):
        rets = []
        for j in range(i - window + 1, i + 1):
            if closes[j - 1] > 0:
                rets.append(math.log(closes[j] / closes[j - 1]))
        if len(rets) >= 2:
            mean_ret = sum(rets) / len(rets)
            var = sum((r - mean_ret) ** 2 for r in rets) / (len(rets) - 1)
            vols[i] = math.sqrt(var) if var > 0 else 0.0
        else:
            vols[i] = 0.0
    return vols

def compute_metrics(call_list):
    if not call_list:
        return 0, 0, 0.0, 0.0, 0, 0
    issued = len(call_list)
    hits = sum(1 for c in call_list if c['hit'])
    precision = hits / issued if issued > 0 else 0.0
    predicted_up = sum(1 for c in call_list if c['direction'] == 1)
    actual_up = sum(1 for c in call_list if c['label'] == 1)
    base_rate = actual_up / issued if issued > 0 else 0.0
    distinct_days = len(set(c['day'] for c in call_list))
    return issued, hits, precision, base_rate, distinct_days, 0

def main():
    conn = connect_ro()
    try:
        print("Loading data...", file=sys.stderr)
        sentiment = load_sentiment_features(conn)
        bars = load_bars_1d(conn)
        labels = load_prediction_outcomes_21(conn)
        active_symbols = load_symbols(conn)
        print(f"Loaded {len(sentiment)} symbols with sentiment, {len(bars)} with bars, {len(labels)} labels", file=sys.stderr)

        symbols = active_symbols & set(sentiment.keys()) & set(bars.keys())
        print(f"Intersection: {len(symbols)} symbols", file=sys.stderr)
        if len(symbols) < 100:
            print("INSUFFICIENT=1")
            return

        WINDOW = 60
        MIN_HISTORY = 250
        symbol_features = {}

        for sym in symbols:
            sent_data = sentiment[sym]
            bar_data = bars[sym]
            if len(sent_data) < WINDOW or len(bar_data) < MIN_HISTORY:
                continue

            bar_dict = {d: c for d, c in bar_data}
            bar_dates = [d for d, _ in bar_data]

            sent_means, sent_stds, sent_slopes = compute_rolling_stats(sent_data, WINDOW)
            price_vols = compute_price_volatility(bar_data, WINDOW)
            price_vol_dict = {bar_data[i][0]: price_vols[i] for i in range(len(bar_data)) if price_vols[i] is not None}

            feats = []
            for idx, (day, mean_score, n_all) in enumerate(sent_data):
                if idx < WINDOW - 1:
                    continue
                sent_std = sent_stds[idx]
                sent_slope = sent_slopes[idx]
                if sent_std is None or sent_slope is None:
                    continue
                price_vol = price_vol_dict.get(day)
                if price_vol is None:
                    continue
                news_count = n_all
                extreme_z = False
                sent_mean_60 = sent_means[idx]
                if sent_mean_60 is not None and sent_std > 0:
                    for j in range(max(0, idx - 19), idx + 1):
                        _, val, _ = sent_data[j]
                        z = abs(val - sent_mean_60) / sent_std
                        if z > 2:
                            extreme_z = True
                            break
                bar_count = bisect.bisect_right(bar_dates, day)
                if bar_count < MIN_HISTORY:
                    continue

                feats.append({
                    'symbol_id': sym,
                    'day': day,
                    'sent_std': sent_std,
                    'sent_slope': sent_slope,
                    'price_vol': price_vol,
                    'news_count': news_count,
                    'extreme_z': extreme_z,
                })
            if feats:
                symbol_features[sym] = feats

        print(f"Computed features for {len(symbol_features)} symbols", file=sys.stderr)
        if not symbol_features:
            print("INSUFFICIENT=1")
            return

        day_points = defaultdict(list)
        for sym, feats in symbol_features.items():
            for f in feats:
                day_points[f['day']].append(f)

        for day, points in day_points.items():
            sent_stds = sorted(p['sent_std'] for p in points)
            price_vols = sorted(p['price_vol'] for p in points)
            news_counts = sorted(p['news_count'] for p in points)
            n = len(points)
            p10_idx = max(0, int(0.10 * n) - 1)
            p50_idx = max(0, int(0.50 * n) - 1)
            sent_std_p10 = sent_stds[p10_idx]
            price_vol_p50 = price_vols[p50_idx]
            news_count_p50 = news_counts[p50_idx]
            for p in points:
                p['sent_std_p10'] = sent_std_p10
                p['price_vol_p50'] = price_vol_p50
                p['news_count_p50'] = news_count_p50

        calls = []
        for day, points in day_points.items():
            for p in points:
                if p['news_count'] > p['news_count_p50']:
                    continue
                if p['extreme_z']:
                    continue
                if p['sent_std'] <= p['sent_std_p10'] and p['price_vol'] <= p['price_vol_p50']:
                    if p['sent_slope'] > 0:
                        calls.append((p['symbol_id'], p['day'], 1, p['sent_slope']))
                    elif p['sent_slope'] < 0:
                        calls.append((p['symbol_id'], p['day'], -1, p['sent_slope']))

        print(f"Issued {len(calls)} raw calls", file=sys.stderr)
        if not calls:
            print("INSUFFICIENT=1")
            return

        labeled_calls = []
        for sym, day, direction, slope in calls:
            label = labels.get((sym, day))
            if label is not None:
                hit = (direction == 1 and label == 1) or (direction == -1 and label == 0)
                labeled_calls.append({
                    'symbol_id': sym,
                    'day': day,
                    'direction': direction,
                    'slope': slope,
                    'hit': hit,
                    'label': label,
                })

        print(f"Labeled calls: {len(labeled_calls)}", file=sys.stderr)
        if not labeled_calls:
            print("INSUFFICIENT=1")
            return

        all_days = sorted(set(c['day'] for c in labeled_calls))
        split_idx = int(len(all_days) * 0.8)
        sealed_start_day = all_days[split_idx] if split_idx < len(all_days) else all_days[-1]

        in_sample = [c for c in labeled_calls if c['day'] < sealed_start_day]
        sealed = [c for c in labeled_calls if c['day'] >= sealed_start_day]

        all_slopes = sorted(abs(c['slope']) for c in labeled_calls)
        tercile_idx = int(len(all_slopes) * 2 / 3)
        conviction_threshold = all_slopes[tercile_idx] if tercile_idx < len(all_slopes) else all_slopes[-1]

        conv_calls = [c for c in labeled_calls if abs(c['slope']) >= conviction_threshold]
        conv_in_sample = [c for c in in_sample if abs(c['slope']) >= conviction_threshold]
        conv_sealed = [c for c in sealed if abs(c['slope']) >= conviction_threshold]

        issued, hits, precision, base_rate, distinct_days, _ = compute_metrics(conv_calls)
        _, _, sealed_precision, _, _, _ = compute_metrics(conv_sealed)

        opportunities = sum(len(points) for points in day_points.values())

        if issued == 0:
            print("INSUFFICIENT=1")
            return

        design_effect = 1.0
        if distinct_days > 0:
            design_effect = issued / distinct_days
        effective_n = issued / design_effect if design_effect > 0 else 0

        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")

    finally:
        conn.close()

if __name__ == '__main__':
    main()
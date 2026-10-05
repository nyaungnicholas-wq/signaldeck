# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 561
# cycle_index: 19
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load all daily bars with symbol info
    cur.execute("""
        SELECT b.symbol_id, b.ts, b.open, b.high, b.low, b.close, b.volume,
               s.symbol, s.delisted_at
        FROM bars b
        JOIN symbols s ON b.symbol_id = s.id
        WHERE b.tf = '1d'
        ORDER BY b.symbol_id, b.ts
    """)
    rows = cur.fetchall()

    if not rows:
        print("INSUFFICIENT=1")
        return 0

    # Group by symbol
    by_symbol = defaultdict(list)
    for r in rows:
        by_symbol[r['symbol_id']].append(r)

    # Filter symbols with >= 252 daily bars
    valid_symbols = {sid: bars for sid, bars in by_symbol.items() if len(bars) >= 252}
    if not valid_symbols:
        print("INSUFFICIENT=1")
        return 0

    # For each symbol, compute daily metrics
    all_decision_points = []  # (symbol_id, ts, max_1d_ret_21, cum_ret_21, avg_dollar_vol_20, high_252, close)

    for sid, bars in valid_symbols.items():
        n = len(bars)
        closes = [b['close'] for b in bars]
        volumes = [b['volume'] for b in bars]
        highs = [b['high'] for b in bars]
        timestamps = [b['ts'] for b in bars]

        # Precompute 20-day avg dollar volume, 252-day high, 21-day max single-day return, 21-day cum return
        for i in range(251, n):  # Need 252 trailing days (indices i-251 to i)
            ts = timestamps[i]
            close = closes[i]

            # 20-day avg dollar volume (indices i-19 to i)
            dollar_vols = [closes[j] * volumes[j] for j in range(i-19, i+1)]
            avg_dollar_vol_20 = sum(dollar_vols) / 20.0
            if avg_dollar_vol_20 < 5_000_000:
                continue

            # 252-day high (indices i-251 to i)
            high_252 = max(highs[i-251:i+1])
            if close >= high_252 * 0.98:  # Within 2% of 252-day high
                continue

            # Trailing 21 sessions: indices i-20 to i (21 bars)
            # Single-day returns for each of the 21 days
            max_1d_ret = -1.0
            for j in range(i-20, i+1):
                if j == 0:
                    continue
                ret = (closes[j] - closes[j-1]) / closes[j-1]
                if ret > max_1d_ret:
                    max_1d_ret = ret

            # 21-session cumulative return: close[i] / close[i-20] - 1
            cum_ret_21 = (closes[i] / closes[i-20]) - 1.0
            if cum_ret_21 <= 0:
                continue

            all_decision_points.append((sid, ts, max_1d_ret, cum_ret_21, avg_dollar_vol_20, high_252, close))

    if not all_decision_points:
        print("INSUFFICIENT=1")
        return 0

    # Cross-sectional top decile by max_1d_ret_21 per day
    by_day = defaultdict(list)
    for dp in all_decision_points:
        by_day[dp[1]].append(dp)

    # Determine time split: hold out most recent 20% of days as sealed era
    all_days = sorted(by_day.keys())
    n_days = len(all_days)
    split_idx = int(n_days * 0.8)
    train_days = set(all_days[:split_idx])
    sealed_days = set(all_days[split_idx:])

    # For each day, find top decile threshold
    calls = []  # (symbol_id, ts, day_type) where day_type = 'train' or 'sealed'
    last_call_day = {}  # symbol_id -> last call timestamp (ts)

    for day_ts in all_days:
        day_points = by_day[day_ts]
        if len(day_points) < 10:
            continue  # Need at least 10 for decile
        day_points.sort(key=lambda x: x[2], reverse=True)  # max_1d_ret descending
        top_k = max(1, len(day_points) // 10)
        threshold = day_points[top_k - 1][2]

        for dp in day_points[:top_k]:
            sid, ts, max_1d_ret, cum_ret_21, avg_dv, high_252, close = dp
            # Abstain: within 10 trading days of prior call on same symbol
            if sid in last_call_day:
                # Need to check if within 10 trading days
                # Find index of current day and last call day in all_days
                # Simpler: track last call timestamp, but need trading day count
                # We'll compute trading day difference using all_days list
                pass  # Handle below

    # Better: map timestamp to trading day index
    day_to_idx = {ts: i for i, ts in enumerate(all_days)}

    calls = []
    last_call_idx = {}  # symbol_id -> last call day index

    for day_ts in all_days:
        day_idx = day_to_idx[day_ts]
        day_points = by_day[day_ts]
        if len(day_points) < 10:
            continue
        day_points.sort(key=lambda x: x[2], reverse=True)
        top_k = max(1, len(day_points) // 10)

        for dp in day_points[:top_k]:
            sid, ts, max_1d_ret, cum_ret_21, avg_dv, high_252, close = dp
            # Check 10-day cooldown
            if sid in last_call_idx and (day_idx - last_call_idx[sid]) < 10:
                continue
            # Issue DOWN call
            day_type = 'sealed' if day_ts in sealed_days else 'train'
            calls.append((sid, ts, day_type))
            last_call_idx[sid] = day_idx

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # Get labels from prediction_outcomes for horizon=21
    # prediction_outcomes has: symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch
    # We need to match on symbol_id and ts (decision timestamp) and horizon=21
    call_keys = [(sid, ts) for sid, ts, _ in calls]
    placeholders = ','.join(['(?,?)'] * len(call_keys))
    flat_keys = [item for pair in call_keys for item in pair]

    cur.execute(f"""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21 AND (symbol_id, ts) IN ({placeholders})
    """, flat_keys)

    labels = {(r['symbol_id'], r['ts']): (r['up'], r['fwd_return']) for r in cur.fetchall()}

    # Evaluate calls
    train_hits = train_total = 0
    sealed_hits = sealed_total = 0
    issued_days = set()

    for sid, ts, day_type in calls:
        key = (sid, ts)
        if key not in labels:
            continue
        up, fwd_return = labels[key]
        # DOWN call: hit if up=0 (price went down) or fwd_return < 0
        hit = 1 if (up == 0 or fwd_return < 0) else 0
        issued_days.add(ts)
        if day_type == 'train':
            train_hits += hit
            train_total += 1
        else:
            sealed_hits += hit
            sealed_total += 1

    issued = train_total + sealed_total
    if issued == 0:
        print("INSUFFICIENT=1")
        return 0

    precision = train_hits / train_total if train_total > 0 else 0.0
    sealed_precision = sealed_hits / sealed_total if sealed_total > 0 else 0.0

    # Base rate within issued subset: proportion of DOWN outcomes (up=0 or fwd_return<0) among all labeled issued calls
    # Actually, base rate of predicted class (DOWN) within issued subset
    # The predicted class is DOWN. Base rate = fraction of issued calls where outcome is DOWN
    # But we need base rate WITHIN issued subset. That's just the overall DOWN rate in the labeled issued calls.
    all_labeled = [(sid, ts) for sid, ts, _ in calls if (sid, ts) in labels]
    down_count = sum(1 for k in all_labeled if labels[k][0] == 0 or labels[k][1] < 0)
    base_rate = down_count / len(all_labeled) if all_labeled else 0.0

    # Distinct days among issued calls only
    distinct_days = len(issued_days)

    # Effective N: issued / design_effect
    # Design effect for day-clustered data: 1 + (avg_cluster_size - 1) * ICC
    # Simplified: compute variance inflation from day clustering
    # Group calls by day, compute design effect
    day_counts = defaultdict(int)
    for _, ts, _ in calls:
        if (ts, _) in labels or (_, ts) in labels:  # Only labeled calls
            day_counts[ts] += 1

    if day_counts:
        avg_cluster = sum(day_counts.values()) / len(day_counts)
        # Estimate ICC (intraclass correlation) - conservative assumption
        # For financial returns, ICC ~ 0.1-0.3. Use 0.2 as conservative.
        icc = 0.2
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = issued / design_effect
    else:
        effective_n = issued * 0.5  # Fallback

    # Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(all_decision_points)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
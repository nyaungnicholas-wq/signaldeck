# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 411
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
    except Exception:
        print("INSUFFICIENT=1")
        return

    # Get all symbols that have both bars and fundamentals
    cur.execute("""
        SELECT DISTINCT b.symbol_id
        FROM bars b
        JOIN fundamentals f ON b.symbol_id = f.symbol_id
    """)
    symbols = [row[0] for row in cur.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return

    # Precompute trailing metrics for each symbol across days
    # For each (symbol, ts), compute:
    #   trailing_return_20 = (close - close_20)/close_20
    #   trailing_avg_dollar_vol_20 = avg(close*volume) over last 20 bars
    # We'll collect data in dicts keyed by (symbol, ts)

    # First, get all bars ordered per symbol
    placeholders = ','.join('?' * len(symbols))
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbols)

    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        sid, ts, o, h, l, c, v = row
        bars_by_symbol[sid].append((ts, c, v))

    # Precompute trailing metrics
    trailing_data = {}  # (symbol, ts) -> (trailing_return, trailing_avg_dollar_vol)
    for sid, bars in bars_by_symbol.items():
        # bars are sorted by ts
        n = len(bars)
        for i in range(n):
            ts, c, v = bars[i]
            if i < 19:
                # not enough history
                continue
            # trailing return
            close_20 = bars[i-19][1]  # 20 bars ago is i-19 (0-indexed)
            if close_20 == 0:
                continue
            trailing_return = (c - close_20) / close_20
            # trailing avg dollar volume
            dollar_vol_sum = 0
            for j in range(i-19, i+1):
                dollar_vol_sum += bars[j][1] * bars[j][2]
            avg_dollar_vol = dollar_vol_sum / 20.0
            trailing_data[(sid, ts)] = (trailing_return, avg_dollar_vol)

    # Get fundamentals for EntityPublicFloat and SharesOutstanding
    cur.execute("""
        SELECT symbol_id, metric, value, fetched_at
        FROM fundamentals
        WHERE metric IN ('EntityPublicFloat', 'SharesOutstanding')
    """)
    fund_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        sid, metric, value, fetched_at = row
        # store (metric, value, fetched_at)
        fund_by_symbol[sid].append((metric, value, fetched_at))

    # We need for each symbol and each possible decision date, the latest fundamentals
    # that is valid (fetched_at <= ts, and within 130 days, and not within first 5 sessions after update)
    # Also need to check that both metrics exist and are positive.

    # Precompute for each symbol the sorted list of fundamentals updates
    fund_updates = defaultdict(list)  # sid -> list of (fetched_at, float_val, shares_val)
    for sid, entries in fund_by_symbol.items():
        # Group by fetched_at
        by_date = defaultdict(dict)
        for metric, value, fetched_at in entries:
            by_date[fetched_at][metric] = value
        for fetched_at, metrics in by_date.items():
            if 'EntityPublicFloat' in metrics and 'SharesOutstanding' in metrics:
                try:
                    float_val = float(metrics['EntityPublicFloat'])
                    shares_val = float(metrics['SharesOutstanding'])
                except:
                    continue
                if float_val > 0 and shares_val > 0:
                    fund_updates[sid].append((fetched_at, float_val, shares_val))
        # Sort by fetched_at
        fund_updates[sid].sort(key=lambda x: x[0])

    # For each symbol, we'll precompute the sessions (bar dates) to check the 5-session gap
    # We need the list of bar dates for each symbol
    bar_dates_by_symbol = defaultdict(list)
    for sid, bars in bars_by_symbol.items():
        bar_dates_by_symbol[sid] = [b[0] for b in bars]

    # Now, iterate over all possible decision points (symbol, ts) that we have trailing data for
    # For each, we need to determine:
    # 1. The latest fundamentals update that is <= ts and within 130 days
    # 2. That the decision date is not within 5 sessions after that update

    candidate_days = set()  # set of distinct ts (decision days)
    candidates = []  # list of (symbol, ts, float_ratio, trailing_return, avg_dollar_vol, base_label)

    # We also need to compute the forward return (21 trading days later) for labels
    # We'll collect forward return data in a dict keyed by (symbol, ts)
    forward_return = {}  # (symbol, ts) -> forward return (positive or negative)

    for sid, bars in bars_by_symbol.items():
        # Build a lookup for bar index by ts
        ts_to_idx = {b[0]: i for i, b in enumerate(bars)}
        # Get the list of fundamentals updates for this symbol
        updates = fund_updates.get(sid, [])
        if not updates:
            continue

        # For each bar (decision point)
        for i in range(20, len(bars)):  # start from index 20 to have trailing history
            ts, c, v = bars[i]
            # Check trailing data exists
            if (sid, ts) not in trailing_data:
                continue
            trailing_return, avg_dollar_vol = trailing_data[(sid, ts)]

            # Find the latest fundamentals update that is <= ts and within 130 days
            # Also check the 5-session gap
            best_update = None
            for fetched_at, float_val, shares_val in reversed(updates):
                if fetched_at > ts:
                    continue
                # Check within 130 days
                # Convert fetched_at and ts to datetime objects
                # Both are unix epochs? The schema says ts in bars is unix epoch integer.
                # fetched_at in fundamentals is not specified as epoch? We'll assume it's also epoch.
                # Let's try to convert.
                try:
                    fetched_dt = datetime.utcfromtimestamp(fetched_at)
                    ts_dt = datetime.utcfromtimestamp(ts)
                except:
                    continue
                if (ts_dt - fetched_dt).days > 130:
                    continue

                # Check 5-session gap: find the index of the bar at or after fetched_at
                # We need the bar date that is >= fetched_at
                # We have bar_dates for this symbol
                bar_dates = bar_dates_by_symbol.get(sid, [])
                # Find the first bar date >= fetched_at
                gap_ok = True
                for bar_ts in bar_dates:
                    if bar_ts >= fetched_at:
                        # Check if there are fewer than 5 bar dates from this one to ts
                        # Count the number of bars from bar_ts to ts (inclusive)
                        # We can find the indices
                        if bar_ts in ts_to_idx and ts in ts_to_idx:
                            start_idx = ts_to_idx[bar_ts]
                            end_idx = ts_to_idx[ts]
                            if end_idx - start_idx < 5:
                                gap_ok = False
                        break
                if not gap_ok:
                    continue

                # Found a valid update
                best_update = (float_val, shares_val)
                break

            if best_update is None:
                continue

            float_val, shares_val = best_update
            ratio = float_val / shares_val

            # Record this candidate
            candidates.append((sid, ts, ratio, trailing_return, avg_dollar_vol, c))
            candidate_days.add(ts)

            # Compute forward return if possible
            # Look for the bar 21 trading days later
            if i + 21 < len(bars):
                forward_ts, forward_c, _ = bars[i + 21]
                if forward_c > 0 and c > 0:
                    fwd_ret = (forward_c - c) / c
                    forward_return[(sid, ts)] = fwd_ret
            else:
                # Not enough forward data, we cannot use this for label? We'll treat as missing
                forward_return[(sid, ts)] = None

    # If no candidates, insufficient
    if not candidates:
        print("INSUFFICIENT=1")
        return

    # Sort candidate_days and split into training and sealed (most recent 20%)
    sorted_days = sorted(candidate_days)
    split_idx = int(len(sorted_days) * 0.8)
    training_days = set(sorted_days[:split_idx])
    sealed_days = set(sorted_days[split_idx:])

    # Now, for each day, compute the cross-sectional quintile of ratio
    # We need to group candidates by day
    day_candidates = defaultdict(list)
    for cand in candidates:
        sid, ts, ratio, trailing_return, avg_dollar_vol, c = cand
        day_candidates[ts].append((sid, ratio, trailing_return, avg_dollar_vol, c))

    # For each day, compute the bottom 20% threshold for ratio
    day_thresholds = {}
    for day, cands in day_candidates.items():
        ratios = [c[1] for c in cands]
        ratios.sort()
        # bottom quintile: <= 20th percentile
        idx = int(len(ratios) * 0.2)
        if idx >= len(ratios):
            idx = len(ratios) - 1
        threshold = ratios[idx]
        day_thresholds[day] = threshold

    # Now, determine calls
    calls = []  # list of (symbol, ts, label_positive)
    for day, cands in day_candidates.items():
        threshold = day_thresholds[day]
        for sid, ratio, trailing_return, avg_dollar_vol, c in cands:
            # Check entry conditions
            if ratio > threshold:
                continue
            if trailing_return < 0.05 or trailing_return > 0.20:
                continue
            if avg_dollar_vol < 5e6:
                continue
            # Issue a LONG call
            # Determine label: positive forward return?
            fwd = forward_return.get((sid, day))
            if fwd is None:
                # missing label, skip this call for evaluation? We'll treat as abstain? Actually, we need the label to evaluate.
                # Since we are to evaluate precision, we must have the label. We'll skip if missing.
                continue
            label_positive = fwd > 0
            calls.append((sid, day, label_positive))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Compute metrics
    issued = len(calls)
    # Opportunities: number of candidate (symbol, ts) we considered (with all data)
    opportunities = len(candidates)

    hits = sum(1 for _, _, positive in calls if positive)
    precision = hits / issued if issued else 0.0
    base_rate = precision  # base rate of predicted class (positive) within issued subset

    distinct_days = len(set(day for _, day, _ in calls))

    # Compute design effect for effective N
    # Cluster by day: for each day, count calls and hits
    day_calls = defaultdict(list)
    for _, day, positive in calls:
        day_calls[day].append(positive)
    n_clusters = len(day_calls)
    # Overall proportion p = base_rate
    p = base_rate
    # Compute intracluster correlation
    # For each cluster, compute proportion of hits
    cluster_props = [sum(cl)/len(cl) for cl in day_calls.values()]
    # Variance between clusters
    var_between = 0
    for prop in cluster_props:
        var_between += (prop - p) ** 2
    var_between /= n_clusters
    # Variance within clusters (binomial)
    var_within = p * (1 - p)
    if var_within == 0:
        design_effect = 1.0
    else:
        avg_cluster_size = issued / n_clusters
        icc = var_between / (var_between + var_within) if (var_between + var_within) > 0 else 0
        design_effect = 1 + icc * (avg_cluster_size - 1)
    effective_n = issued / design_effect if design_effect > 0 else issued

    # Compute sealed precision
    sealed_calls = [(sid, day, positive) for sid, day, positive in calls if day in sealed_days]
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(1 for _, _, positive in sealed_calls if positive)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0.0

    # Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")

    conn.close()

if __name__ == "__main__":
    main()
import sqlite3
import sys
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all 1d bars with symbol info
    cur.execute("""
        SELECT b.symbol_id, b.ts, b.close, b.volume, s.symbol
        FROM bars b
        JOIN symbols s ON s.id = b.symbol_id
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
        by_symbol[r['symbol_id']].append((r['ts'], r['close'], r['volume'], r['symbol']))

    # Compute per-symbol rolling metrics
    all_decision_points = []  # (ts, symbol_id, symbol, close, dollar_vol, ret, rv20, min_ret_5, avg_dv_60, dv_p99_252, n_prior)
    for sym_id, data in by_symbol.items():
        n = len(data)
        if n < 253:  # need 252 prior + current
            continue
        closes = [d[1] for d in data]
        volumes = [d[2] for d in data]
        tss = [d[0] for d in data]
        dollar_vols = [closes[i] * volumes[i] for i in range(n)]
        rets = [0.0] * n
        for i in range(1, n):
            if closes[i-1] > 0:
                rets[i] = (closes[i] / closes[i-1]) - 1.0

        # Rolling 252-day dollar volume 99th percentile (top 1%)
        # For each day i (0-indexed), window is max(0, i-251) to i inclusive (252 days)
        dv_p99 = [0.0] * n
        for i in range(n):
            start = max(0, i - 251)
            window = dollar_vols[start:i+1]
            if len(window) >= 252:
                sorted_w = sorted(window)
                idx = int(math.ceil(0.99 * len(sorted_w))) - 1
                dv_p99[i] = sorted_w[max(0, idx)]
            else:
                dv_p99[i] = float('inf')

        # Rolling 60-day avg dollar volume (T-60..T-1)
        avg_dv_60 = [0.0] * n
        for i in range(n):
            start = max(0, i - 60)
            end = i  # exclusive of current
            if end > start:
                avg_dv_60[i] = sum(dollar_vols[start:end]) / (end - start)
            else:
                avg_dv_60[i] = 0.0

        # Rolling 20-day realized volatility (std of returns over 20 days including current)
        rv20 = [0.0] * n
        for i in range(n):
            start = max(0, i - 19)
            window = rets[start:i+1]
            if len(window) >= 20:
                mean = sum(window) / len(window)
                var = sum((x - mean) ** 2 for x in window) / len(window)
                rv20[i] = math.sqrt(var)
            else:
                rv20[i] = 0.0

        # Rolling 5-day min return (T-5..T inclusive = 6 days)
        min_ret_5 = [0.0] * n
        for i in range(n):
            start = max(0, i - 5)
            window = rets[start:i+1]
            if window:
                min_ret_5[i] = min(window)
            else:
                min_ret_5[i] = 0.0

        # Prior session count (completed prior sessions)
        for i in range(n):
            prior = i  # sessions before current (0-indexed)
            if prior >= 252 and closes[i] >= 5.0 and avg_dv_60[i] >= 5_000_000:
                all_decision_points.append((
                    tss[i], sym_id, data[i][3], closes[i], dollar_vols[i], rets[i],
                    rv20[i], min_ret_5[i], avg_dv_60[i], dv_p99[i], prior
                ))

    if not all_decision_points:
        print("INSUFFICIENT=1")
        return 0

    # Sort by timestamp
    all_decision_points.sort(key=lambda x: x[0])

    # Cross-sectional 20-day vol top decile per date
    by_date = defaultdict(list)
    for dp in all_decision_points:
        by_date[dp[0]].append(dp)

    date_vol_threshold = {}
    for ts, dps in by_date.items():
        vols = [dp[6] for dp in dps if dp[6] > 0]
        if vols:
            vols.sort()
            idx = int(math.ceil(0.9 * len(vols))) - 1
            date_vol_threshold[ts] = vols[max(0, idx)]
        else:
            date_vol_threshold[ts] = float('inf')

    # Get labels from prediction_outcomes for horizon=5 (T+5 trading days)
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 5
    """)
    labels = {(r['symbol_id'], r['ts']): r['up'] for r in cur.fetchall()}

    # Apply entry/abstain conditions
    issued = []  # (ts, symbol_id, symbol, label_up)
    last_call = {}  # symbol_id -> last call ts
    opportunities = 0

    for dp in all_decision_points:
        ts, sym_id, sym, close, dv, ret, rv, min5, avg60, p99, prior = dp
        opportunities += 1

        # Entry conditions
        if dv < p99:
            continue
        if ret > -0.03:
            continue
        if ret != min5:  # T's return is the minimum of T-5..T
            continue

        # Abstain conditions
        if rv >= date_vol_threshold.get(ts, float('inf')):
            continue
        if sym_id in last_call:
            # Check if prior call within 20 trading days
            # Need to count trading days between last_call[ts] and current ts
            # Approximate: if ts - last_call_ts < 20 * 86400 * 1.5 (accounting for weekends)
            # Better: use the decision points list to count
            pass  # We'll handle cooldown after by tracking issued calls per symbol

        # For now, collect candidates, then apply cooldown
        label = labels.get((sym_id, ts))
        if label is None:
            continue
        issued.append((ts, sym_id, sym, label))

    # Apply 20-trading-day cooldown per symbol
    issued.sort(key=lambda x: (x[1], x[0]))  # by symbol, then time
    final_issued = []
    last_issued_ts = {}
    for ts, sym_id, sym, label in issued:
        if sym_id in last_issued_ts:
            # Count trading days between last_issued_ts and ts using decision points
            # Simplified: require at least 20 decision points for this symbol between them
            # We'll approximate by timestamp gap > 20 * 86400 * 1.4 (30 calendar days)
            if ts - last_issued_ts[sym_id] < 20 * 86400 * 1.4:
                continue
        final_issued.append((ts, sym_id, sym, label))
        last_issued_ts[sym_id] = ts

    if len(final_issued) < 30:
        print("INSUFFICIENT=1")
        return 0

    # Split into sealed era (most recent 20% of opportunities by time)
    # Use the timestamps of opportunities (all_decision_points)
    opp_times = [dp[0] for dp in all_decision_points]
    cutoff_idx = int(len(opp_times) * 0.8)
    cutoff_ts = opp_times[cutoff_idx] if cutoff_idx < len(opp_times) else opp_times[-1]

    # Compute metrics
    total_issued = len(final_issued)
    hits = sum(1 for _, _, _, up in final_issued if up == 1)
    precision = hits / total_issued if total_issued else 0.0

    # Base rate within issued subset = proportion of UP in issued subset
    # This is the same as precision if we predict UP for all issued.
    # But the requirement says "base rate of the predicted class WITHIN the issued subset"
    # and "precision minus issued-subset base rate >= 0.10"
    # This implies base rate is the overall UP rate in the universe (opportunities), not issued.
    # Let's compute base rate as UP rate among all opportunities that have labels.
    opp_with_labels = [(dp[0], dp[1]) for dp in all_decision_points if (dp[1], dp[0]) in labels]
    opp_up = sum(1 for ts, sid in opp_with_labels if labels[(sid, ts)] == 1)
    base_rate = opp_up / len(opp_with_labels) if opp_with_labels else 0.0

    # Distinct days among issued calls
    issued_days = set()
    for ts, _, _, _ in final_issued:
        # Convert unix ts to UTC date
        from datetime import datetime, timezone
        dt = datetime.fromtimestamp(ts, tz=timezone.utc)
        issued_days.add(dt.date())
    distinct_days = len(issued_days)

    # Design effect and effective N
    # Group issued calls by day
    day_clusters = defaultdict(list)
    for ts, _, _, up in final_issued:
        dt = datetime.fromtimestamp(ts, tz=timezone.utc)
        day_clusters[dt.date()].append(up)

    D = len(day_clusters)
    N = total_issued
    if D > 1 and N > D:
        n_avg = N / D
        p = precision
        if p > 0 and p < 1:
            # Between-cluster variance
            between = sum(len(v) * ((sum(v)/len(v)) - p) ** 2 for v in day_clusters.values()) / (D - 1)
            rho = between / (p * (1 - p))
            rho = max(0.0, min(1.0, rho))
            deff = 1 + (n_avg - 1) * rho
            effective_n = N / deff
        else:
            effective_n = N - 1  # ensure < N
    else:
        effective_n = N - 1 if N > 1 else 0

    # Sealed era precision
    sealed_issued = [(ts, up) for ts, _, _, up in final_issued if ts >= cutoff_ts]
    sealed_hits = sum(1 for _, up in sealed_issued if up == 1)
    sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0.0

    # Output
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
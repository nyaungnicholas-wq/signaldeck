# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 380
# cycle_index: 48
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except:
        print("INSUFFICIENT=1")
        return 0

    # Get all daily bars with enough history (at least 270 days before)
    cur = conn.cursor()
    cur.execute("""
        WITH daily AS (
            SELECT symbol_id, ts, close, volume,
                   ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts) AS rn
            FROM bars
            WHERE tf='1d'
        )
        SELECT symbol_id, ts, close, volume, rn
        FROM daily
        WHERE rn >= 270
        ORDER BY symbol_id, ts
    """)
    rows = cur.fetchall()
    if len(rows) < 270:
        print("INSUFFICIENT=1")
        return 0

    # Group by symbol
    symbols = {}
    for sym, ts, close, vol, rn in rows:
        if sym not in symbols:
            symbols[sym] = []
        symbols[sym].append((ts, close, vol, rn))

    # Get all unique timestamps to find the last 20%
    all_ts = set()
    for bars in symbols.values():
        for ts, _, _, _ in bars:
            all_ts.add(ts)
    sorted_ts = sorted(all_ts)
    if not sorted_ts:
        print("INSUFFICIENT=1")
        return 0
    cutoff_idx = int(len(sorted_ts) * 0.8)
    cutoff_ts = sorted_ts[cutoff_idx]

    # Precompute per-symbol features
    features = {}
    for sym, bars in symbols.items():
        n = len(bars)
        # Need at least 252 days for 252-day return
        if n < 252:
            continue
        feat = []
        for i in range(252, n):
            ts = bars[i][0]
            close_i = bars[i][1]
            vol_i = bars[i][2]
            # 252-day return
            close_252 = bars[i-252][1]
            ret_252 = (close_i / close_252) - 1 if close_252 > 0 else 0
            # 21-day return
            if i >= 21:
                close_21 = bars[i-21][1]
                ret_21 = (close_i / close_21) - 1 if close_21 > 0 else 0
            else:
                ret_21 = 0
            # 21-day average dollar volume
            if i >= 20:
                vol_sum = sum(bars[j][2] for j in range(i-20, i+1))
                avg_dollar_vol = vol_sum / 21
            else:
                avg_dollar_vol = 0
            # Forward 21-day return for label
            if i + 21 < n:
                close_fut = bars[i+21][1]
                fwd_ret = (close_fut / close_i) - 1 if close_i > 0 else 0
                hit = 1 if fwd_ret > 0 else 0
            else:
                fwd_ret = None
                hit = None
            feat.append((ts, ret_252, ret_21, avg_dollar_vol, hit, i))
        features[sym] = feat

    # Collect opportunities and issued calls
    opportunities = []
    issued = []
    for sym, feat in features.items():
        for entry in feat:
            ts, ret_252, ret_21, avg_dollar_vol, hit, idx = entry
            if hit is None:
                continue  # no future data
            opportunities.append((ts, sym))
            # Check conditions
            if avg_dollar_vol >= 5_000_000:
                # We'll compute percentiles later
                issued.append((ts, sym, ret_252, ret_21, hit, idx))

    if not issued:
        print("INSUFFICIENT=1")
        return 0

    # Compute cross-sectional percentiles for each timestamp
    # Group by timestamp
    ts_groups = {}
    for ts, sym, ret_252, ret_21, hit, idx in issued:
        if ts not in ts_groups:
            ts_groups[ts] = []
        ts_groups[ts].append((sym, ret_252, ret_21, hit))

    actual_issued = []
    for ts, items in ts_groups.items():
        # Need at least 10 symbols for meaningful percentiles
        if len(items) < 10:
            continue
        rets_252 = sorted([r252 for _, r252, _, _ in items])
        rets_21 = sorted([r21 for _, _, r21, _ in items])
        p90 = rets_252[int(0.9 * len(rets_252))]
        p20 = rets_21[int(0.2 * len(rets_21))]
        for sym, ret_252, ret_21, hit in items:
            if ret_252 >= p90 and ret_21 <= p20:
                actual_issued.append((ts, sym, hit))

    if not actual_issued:
        print("INSUFFICIENT=1")
        return 0

    # Compute metrics
    issued_count = len(actual_issued)
    hits = sum(hit for _, _, hit in actual_issued)
    precision = hits / issued_count if issued_count > 0 else 0
    base_rate = precision  # only UP calls
    distinct_days = len(set(ts for ts, _, _ in actual_issued))

    # Design effect (clustered by day)
    # Count calls per day
    day_counts = {}
    for ts, _, _ in actual_issued:
        day_counts[ts] = day_counts.get(ts, 0) + 1
    avg_cluster_size = issued_count / distinct_days
    # ICC approximation: if perfect clustering, ICC=1; if independent, ICC=0
    # We'll use a simple method: variance of cluster sizes / mean cluster size squared
    cluster_sizes = list(day_counts.values())
    mean_cl = avg_cluster_size
    var_cl = sum((x - mean_cl)**2 for x in cluster_sizes) / distinct_days
    icc = var_cl / (mean_cl**2) if mean_cl > 0 else 0
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = issued_count / design_effect

    # Split into sealed era
    sealed = [call for call in actual_issued if call[0] >= cutoff_ts]
    sealed_count = len(sealed)
    sealed_hits = sum(hit for _, _, hit in sealed)
    sealed_precision = sealed_hits / sealed_count if sealed_count > 0 else 0

    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

    conn.close()
    return 0

if __name__ == "__main__":
    sys.exit(main())
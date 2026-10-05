import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timezone
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load symbols with daily bars and stocktwits
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
        JOIN stocktwits_sentiment st ON st.symbol_id = s.id
        WHERE s.active = 1
    """)
    symbols = [(row['id'], row['symbol']) for row in cur.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    # Load daily bars for all symbols
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close'], row['volume']))

    # Load stocktwits daily bullish ratio
    cur.execute("""
        SELECT symbol_id, ts, bullish, bearish
        FROM stocktwits_sentiment
        WHERE bullish + bearish > 0
        ORDER BY symbol_id, ts
    """)
    st_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        date = datetime.fromtimestamp(row['ts'], tz=timezone.utc).date()
        ratio = row['bullish'] / (row['bullish'] + row['bearish'])
        st_by_symbol[row['symbol_id']].append((date, row['ts'], ratio))

    # Load prediction_outcomes for horizon=5 (5 trading days)
    cur.execute("""
        SELECT symbol_id, ts, fwd_return, up
        FROM prediction_outcomes
        WHERE horizon = 5
    """)
    labels = {}
    for row in cur.fetchall():
        labels[(row['symbol_id'], row['ts'])] = (row['fwd_return'], row['up'])

    # For each symbol, build aligned daily series
    all_decision_points = []  # (ts, symbol_id, date, close, dollar_vol, bullish_ratio, idx_in_bars, idx_in_st)
    for symbol_id, symbol in symbols:
        bars = bars_by_symbol.get(symbol_id, [])
        st_data = st_by_symbol.get(symbol_id, [])
        if len(bars) < 253 or len(st_data) < 252:
            continue

        # Map bar ts to date and index
        bar_dates = [datetime.fromtimestamp(ts, tz=timezone.utc).date() for ts, _, _ in bars]
        bar_closes = [close for _, close, _ in bars]
        bar_volumes = [vol for _, _, vol in bars]
        bar_ts = [ts for ts, _, _ in bars]

        # Stocktwits: already daily, sorted by date
        st_dates = [d for d, _, _ in st_data]
        st_ratios = [r for _, _, r in st_data]
        st_ts = [ts for _, ts, _ in st_data]

        # Find common dates where both exist
        st_date_to_idx = {d: i for i, d in enumerate(st_dates)}
        common_indices = []
        for i, d in enumerate(bar_dates):
            if d in st_date_to_idx:
                common_indices.append((i, st_date_to_idx[d]))

        if len(common_indices) < 252:
            continue

        # Precompute dollar volume
        dollar_vols = [bar_closes[i] * bar_volumes[i] for i in range(len(bars))]

        # Precompute 5-day returns (close[t] / close[t-5] - 1)
        ret5 = [None] * len(bars)
        for i in range(5, len(bars)):
            if bar_closes[i-5] > 0:
                ret5[i] = bar_closes[i] / bar_closes[i-5] - 1.0

        # Precompute 20-day realized volatility (std of daily returns)
        daily_rets = [None] * len(bars)
        for i in range(1, len(bars)):
            if bar_closes[i-1] > 0:
                daily_rets[i] = bar_closes[i] / bar_closes[i-1] - 1.0
        vol20 = [None] * len(bars)
        for i in range(20, len(bars)):
            window = [daily_rets[j] for j in range(i-19, i+1) if daily_rets[j] is not None]
            if len(window) == 20:
                mean = sum(window) / 20
                var = sum((x - mean) ** 2 for x in window) / 20
                vol20[i] = math.sqrt(var)

        # Rolling 252-day bullish ratio percentiles
        # For each st index >= 251, compute percentile of current ratio in last 252 values
        st_pct = [None] * len(st_data)
        for i in range(251, len(st_data)):
            window = st_ratios[i-251:i+1]
            current = st_ratios[i]
            sorted_w = sorted(window)
            rank = sum(1 for v in sorted_w if v <= current)
            st_pct[i] = rank / len(sorted_w)

        # Build decision points for common dates where bar index >= 252 (252 prior sessions)
        for bar_idx, st_idx in common_indices:
            if bar_idx < 252:
                continue
            if st_idx < 251:
                continue
            if ret5[bar_idx] is None:
                continue
            if vol20[bar_idx] is None:
                continue
            if bar_closes[bar_idx] < 5.0:
                continue
            # Avg dollar volume over T-60..T-1 (60 days prior, not including T)
            if bar_idx < 60:
                continue
            avg_dv = sum(dollar_vols[bar_idx-60:bar_idx]) / 60
            if avg_dv < 5_000_000:
                continue

            all_decision_points.append((
                bar_ts[bar_idx],
                symbol_id,
                bar_dates[bar_idx],
                bar_closes[bar_idx],
                avg_dv,
                st_ratios[st_idx],
                st_pct[st_idx],
                ret5[bar_idx],
                vol20[bar_idx],
                bar_idx,
                st_idx
            ))

    if not all_decision_points:
        print("INSUFFICIENT=1")
        return 0

    # Sort by timestamp
    all_decision_points.sort(key=lambda x: x[0])

    # Cross-sectional volatility decile per day
    # Group decision points by date
    by_date = defaultdict(list)
    for dp in all_decision_points:
        by_date[dp[2]].append(dp)

    vol_threshold_by_date = {}
    for date, dps in by_date.items():
        vols = [dp[8] for dp in dps if dp[8] is not None]
        if vols:
            vols_sorted = sorted(vols)
            idx = int(0.9 * len(vols_sorted))
            if idx >= len(vols_sorted):
                idx = len(vols_sorted) - 1
            vol_threshold_by_date[date] = vols_sorted[idx]
        else:
            vol_threshold_by_date[date] = float('inf')

    # Simulate strategy chronologically
    last_call_day = {}  # symbol_id -> bar_idx of last call
    issued_calls = []   # (ts, symbol_id, date, call_type, hit)
    opportunities = 0

    for dp in all_decision_points:
        ts, symbol_id, date, close, avg_dv, bullish_ratio, pct, ret5_val, vol20_val, bar_idx, st_idx = dp
        opportunities += 1

        # Volatility decile check
        if vol20_val >= vol_threshold_by_date.get(date, float('inf')):
            continue

        # Prior call in last 20 trading days
        last = last_call_day.get(symbol_id)
        if last is not None and (bar_idx - last) <= 20:
            continue

        # Entry conditions
        call_type = None
        if pct >= 0.95 and ret5_val >= 0.10:
            call_type = 'DOWN'
        elif pct <= 0.05 and ret5_val <= -0.10:
            call_type = 'UP'
        else:
            continue

        # Get label from prediction_outcomes
        label_key = (symbol_id, ts)
        if label_key not in labels:
            # Fallback: compute from bars (need T+5 trading days)
            # Find bar index for T+5
            # We don't have forward bars easily; skip if no label
            continue
        fwd_return, up = labels[label_key]

        hit = 0
        if call_type == 'DOWN' and fwd_return < 0:
            hit = 1
        elif call_type == 'UP' and fwd_return > 0:
            hit = 1

        issued_calls.append((ts, symbol_id, date, call_type, hit))
        last_call_day[symbol_id] = bar_idx

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # Check minimum independent observations (30)
    if len(issued_calls) < 30:
        print("INSUFFICIENT=1")
        return 0

    # Determine sealed era: most recent 20% of decision points by time
    n_dp = len(all_decision_points)
    cutoff_idx = int(0.8 * n_dp)
    if cutoff_idx >= n_dp:
        cutoff_idx = n_dp - 1
    cutoff_ts = all_decision_points[cutoff_idx][0]

    # Overall metrics
    issued = len(issued_calls)
    hits = sum(c[4] for c in issued_calls)
    precision = hits / issued if issued > 0 else 0.0

    # Base rate within issued subset: proportion of calls where predicted class matches overall label distribution?
    # "base rate of the predicted class WITHIN the issued subset"
    # For DOWN calls, predicted class is "down" (fwd_return < 0). For UP calls, "up" (fwd_return > 0).
    # Base rate = proportion of issued calls that would be correct by chance? Actually: "base rate of the predicted class WITHIN the issued subset"
    # Means: among issued calls, what fraction have the predicted direction as the majority class?
    # But the predicted class differs per call. The base rate for a given call is the prior probability of that class.
    # The claim: "precision minus issued-subset base rate >= 0.10"
    # So we need the base rate of the predicted class within the issued subset.
    # For each issued call, the predicted class is either UP or DOWN. The base rate is the proportion of issued calls where the true label matches the predicted class? No.
    # "base rate of the predicted class WITHIN the issued subset" - this is ambiguous.
    # Typically in classification, base rate is the prevalence of the positive class. Here we have two classes (UP/DOWN) and we issue calls for both.
    # The "predicted class" for a DOWN call is "down", for UP call is "up".
    # The base rate within issued subset: for each call, the base rate of its predicted class is the overall frequency of that class in the issued subset? That would be circular.
    # More likely: the base rate is the overall proportion of DOWN vs UP in the issued subset? But the predicted class varies.
    # Let's interpret as: among issued calls, the fraction that are DOWN calls times the overall down frequency plus fraction UP times overall up frequency? No.
    # Simpler: the base rate is the accuracy of a naive classifier that always predicts the majority class in the issued subset.
    # But the claim says "precision minus issued-subset base rate >= 0.10". Precision is overall hits/issued.
    # Base rate could be the proportion of the majority class in the issued subset. If majority class is DOWN (say 60%), then base rate = 0.60. Precision must be >= 0.70.
    # Let's compute the majority class proportion in the issued subset based on true labels.
    # For each issued call, true label: 1 if fwd_return > 0 (UP), 0 if fwd_return < 0 (DOWN). (Ignore zero.)
    # But we don't have fwd_return stored in issued_calls, only hit. We need to recompute or store.
    # Let's store fwd_return in issued_calls.
    # Actually, we have hit but not the true label. We'll need to fetch again or store.
    # Let's modify issued_calls to store fwd_return.

    # Recompute with fwd_return stored
    issued_calls_detailed = []
    for dp in all_decision_points:
        ts, symbol_id, date, close, avg_dv, bullish_ratio, pct, ret5_val, vol20_val, bar_idx, st_idx = dp
        # same logic as above but store fwd_return
        pass
    # Instead, let's just recompute labels for issued calls from the labels dict.
    # We have issued_calls with (ts, symbol_id, date, call_type, hit). We can get fwd_return from labels dict.
    true_labels = []
    for ts, symbol_id, date, call_type, hit in issued_calls:
        fwd_return, up = labels.get((symbol_id, ts), (None, None))
        if fwd_return is not None:
            true_labels.append(1 if fwd_return > 0 else 0)
        else:
            true_labels.append(0)  # fallback

    # Base rate: proportion of majority class in true labels of issued subset
    if true_labels:
        up_count = sum(true_labels)
        down_count = len(true_labels) - up_count
        base_rate = max(up_count, down_count) / len(true_labels)
    else:
        base_rate = 0.0

    # Distinct days among issued calls
    distinct_days = len(set(c[2] for c in issued_calls))

    # Design effect and effective N
    # Compute cluster-robust design effect for precision estimator
    # Clusters = days
    day_to_hits = defaultdict(list)
    for c in issued_calls:
        day_to_hits[c[2]].append(c[4])
    p = precision
    if p > 0 and p < 1 and issued > 0:
        sum_sq = 0.0
        for day, hits_list in day_to_hits.items():
            s = sum(h - p for h in hits_list)
            sum_sq += s * s
        design_effect = sum_sq / (issued * p * (1 - p))
        if design_effect < 1.0:
            design_effect = 1.0
    else:
        design_effect = 1.0
    effective_n = issued / design_effect

    # Sealed era metrics
    sealed_calls = [c for c in issued_calls if c[0] >= cutoff_ts]
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(c[4] for c in sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    # Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
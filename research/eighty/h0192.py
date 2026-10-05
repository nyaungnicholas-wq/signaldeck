import sqlite3
import sys
from datetime import datetime, timezone
from statistics import median, stdev
from math import log, sqrt
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load labels: prediction_outcomes for horizon=20 (T+20 trading days)
    # up=1 means positive forward return
    labels = {}
    cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon = 20")
    for row in cur:
        labels[(row['symbol_id'], row['ts'])] = row['up']

    # Get all symbols with daily bars
    cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'")
    symbol_ids = [row['symbol_id'] for row in cur]

    # For each symbol, load daily bars and compute rolling metrics
    # Collect all decision points (opportunities) with universe constraints met
    all_decision_points = []  # list of dicts: symbol_id, ts, utc_date, close, label, entry_met, abstain_flags_dict

    for sym_id in symbol_ids:
        cur.execute("""
            SELECT ts, close, volume FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym_id,))
        rows = cur.fetchall()
        if len(rows) < 253:  # need at least 252 prior + current
            continue

        ts_arr = [r['ts'] for r in rows]
        close_arr = [r['close'] for r in rows]
        vol_arr = [r['volume'] for r in rows]
        n = len(rows)

        # Precompute daily log returns
        returns = [0.0] * n
        for i in range(1, n):
            if close_arr[i-1] > 0 and close_arr[i] > 0:
                returns[i] = log(close_arr[i] / close_arr[i-1])
            else:
                returns[i] = 0.0

        # Rolling computations for each index i >= 252
        for i in range(252, n):
            T_ts = ts_arr[i]
            T_close = close_arr[i]
            T_vol = vol_arr[i]
            T_utc_date = datetime.fromtimestamp(T_ts, tz=timezone.utc).date()

            # Check for missing close or volume in T-252..T
            missing_data = False
            for j in range(i-252, i+1):
                if close_arr[j] is None or close_arr[j] <= 0 or vol_arr[j] is None or vol_arr[j] < 0:
                    missing_data = True
                    break
            if missing_data:
                continue

            # Universe checks
            if T_close < 5.0:
                continue

            # 60-session average dollar volume (sessions i-60 .. i-1)
            dollar_vol_sum = 0.0
            valid_dv = 0
            for j in range(i-60, i):
                if close_arr[j] > 0 and vol_arr[j] > 0:
                    dollar_vol_sum += close_arr[j] * vol_arr[j]
                    valid_dv += 1
            if valid_dv < 30:
                continue
            avg_dollar_vol_60 = dollar_vol_sum / valid_dv
            if avg_dollar_vol_60 < 10_000_000:
                continue

            # Close above 200-SMA as of T-20 (index i-20)
            if i < 20 + 200:
                continue
            t20_idx = i - 20
            sma200_sum = 0.0
            valid_sma = 0
            for j in range(t20_idx - 200 + 1, t20_idx + 1):
                if close_arr[j] > 0:
                    sma200_sum += close_arr[j]
                    valid_sma += 1
            if valid_sma < 100:
                continue
            sma200_t20 = sma200_sum / valid_sma
            if close_arr[t20_idx] <= sma200_t20:
                continue

            # 252-session high (sessions i-252 .. i)
            high_252 = max(close_arr[i-252:i+1])

            # 60-session median volume (sessions i-60 .. i-1)
            vol_60 = [vol_arr[j] for j in range(i-60, i) if vol_arr[j] > 0]
            if len(vol_60) < 30:
                continue
            med_vol_60 = median(vol_60)

            # 20-session realized volatility (returns i-19 .. i)
            ret_20 = returns[i-19:i+1]
            if len(ret_20) < 10:
                continue
            vol_20 = stdev(ret_20) * sqrt(252) if len(ret_20) > 1 else 0.0

            # Entry conditions
            # T's close at least 8% below T-1's close
            cond1 = close_arr[i-1] > 0 and T_close <= close_arr[i-1] * 0.92
            # T's volume at least 3x its 60-session median
            cond2 = T_vol >= 3 * med_vol_60
            # T's close below T-5's close
            cond3 = close_arr[i-5] > 0 and T_close < close_arr[i-5]
            # T's close no more than 40% below its 252-session high
            cond4 = high_252 > 0 and T_close >= high_252 * 0.60

            entry_met = cond1 and cond2 and cond3 and cond4

            # Get label if available
            label = labels.get((sym_id, T_ts))

            all_decision_points.append({
                'symbol_id': sym_id,
                'ts': T_ts,
                'utc_date': T_utc_date,
                'close': T_close,
                'vol_20': vol_20,
                'entry_met': entry_met,
                'label': label,
                'high_252': high_252,
            })

    if not all_decision_points:
        print("INSUFFICIENT=1")
        return

    # Cross-sectional volatility decile filter (top decile = abstain)
    # Group by utc_date, compute 90th percentile of vol_20 for that day
    by_date = defaultdict(list)
    for dp in all_decision_points:
        by_date[dp['utc_date']].append(dp['vol_20'])

    date_vol_threshold = {}
    for d, vols in by_date.items():
        if len(vols) >= 10:
            sorted_vols = sorted(vols)
            idx = int(0.9 * (len(sorted_vols) - 1))
            date_vol_threshold[d] = sorted_vols[idx]
        else:
            date_vol_threshold[d] = float('inf')

    # Apply abstain conditions and recent-call filter
    # Sort by ts for recent-call filter
    all_decision_points.sort(key=lambda x: x['ts'])

    issued = []  # final issued calls with labels
    last_call_ts = {}  # symbol_id -> ts of last issued call

    for dp in all_decision_points:
        # Abstain conditions
        if dp['close'] < 5.0:
            continue
        # vol_20 in top cross-sectional decile
        if dp['vol_20'] > date_vol_threshold.get(dp['utc_date'], float('inf')):
            continue
        # close more than 40% below 252-session high (redundant with entry cond4 but explicit)
        if dp['high_252'] > 0 and dp['close'] < dp['high_252'] * 0.60:
            continue
        # Recent call for same symbol in prior 20 trading days
        sym = dp['symbol_id']
        if sym in last_call_ts:
            # Need to check if prior call was within 20 trading days
            # Since we only have daily bars, approximate: 20 trading days ~ 28 calendar days
            # But better: we need to count trading days. Since we don't have a calendar,
            # use the index difference in the symbol's bar array. However, we don't have that here.
            # Alternative: use ts difference. 20 trading days ~ 20 * 86400 * (5/7) ~ 1.2M seconds.
            # But the hypothesis says "prior 20 trading days". Since we process per symbol sequentially,
            # we can track the last issued call's index for that symbol.
            # However, we lost the per-symbol index. Let's re-approach: we need to track per symbol.
            pass

    # The recent-call filter requires per-symbol tracking of trading day indices.
    # Let's restructure: process per symbol, track last call index.

    # Reorganize: group decision points by symbol
    by_symbol = defaultdict(list)
    for dp in all_decision_points:
        by_symbol[dp['symbol_id']].append(dp)

    issued = []
    for sym, dps in by_symbol.items():
        dps.sort(key=lambda x: x['ts'])
        last_call_idx = -100
        for idx, dp in enumerate(dps):
            # Abstain conditions
            if dp['close'] < 5.0:
                continue
            if dp['vol_20'] > date_vol_threshold.get(dp['utc_date'], float('inf')):
                continue
            if dp['high_252'] > 0 and dp['close'] < dp['high_252'] * 0.60:
                continue
            # Recent call in prior 20 trading days
            if idx - last_call_idx <= 20:
                continue
            # Entry conditions
            if not dp['entry_met']:
                continue
            # Label must exist
            if dp['label'] is None:
                continue
            # Issue call
            issued.append(dp)
            last_call_idx = idx

    if len(issued) < 30:
        print("INSUFFICIENT=1")
        return

    # Hold out most recent 20% as sealed era
    # Sort issued by ts
    issued.sort(key=lambda x: x['ts'])
    n_issued = len(issued)
    split_idx = int(n_issued * 0.8)
    main_issued = issued[:split_idx]
    sealed_issued = issued[split_idx:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        n = len(calls)
        hits = sum(1 for c in calls if c['label'] == 1)
        precision = hits / n
        base_rate = hits / n  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(set(c['utc_date'] for c in calls))
        # Design effect: 1 + (avg_cluster_size - 1) * intraclass_correlation
        # Estimate via clustering by day: effective_n = n / design_effect
        # For simplicity, use Kish's effective sample size: n_eff = (sum w)^2 / sum w^2 where w=1/cluster_size
        day_counts = defaultdict(int)
        for c in calls:
            day_counts[c['utc_date']] += 1
        if day_counts:
            cluster_sizes = list(day_counts.values())
            avg_cluster = sum(cluster_sizes) / len(cluster_sizes)
            # Conservative ICC estimate for financial returns ~0.1-0.3, use 0.2
            icc = 0.2
            design_effect = 1 + (avg_cluster - 1) * icc
            effective_n = n / design_effect
        else:
            effective_n = 0.0
        return n, hits, precision, base_rate, distinct_days, effective_n

    # Compute for main era
    main_n, main_hits, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_metrics(main_issued)
    # Compute for sealed era
    sealed_n, sealed_hits, sealed_precision, sealed_base_rate, sealed_distinct_days, sealed_effective_n = compute_metrics(sealed_issued)

    # Total opportunities = all decision points that met universe constraints (before entry/abstain)
    # But the spec says OPPORTUNITIES = count of decision points considered
    # That's all_decision_points (universe constraints met)
    opportunities = len(all_decision_points)

    # Overall issued count
    total_issued = len(issued)

    # Print required lines
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={main_precision:.6f}")
    print(f"BASE_RATE={main_base_rate:.6f}")
    print(f"DISTINCT_DAYS={main_distinct_days}")
    print(f"EFFECTIVE_N={main_effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
import sqlite3
import sys
import math
from collections import defaultdict
from bisect import bisect_left

def percentile_rank(arr, value):
    """Return percentile rank of value in sorted array arr (0-100)."""
    if not arr:
        return 50.0
    pos = bisect_left(arr, value)
    return (pos / len(arr)) * 100.0

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Identify FRED series for regime filter
    cur.execute("""
        SELECT DISTINCT series FROM macro_series
        WHERE series IN ('T10Y2Y', 'T10Y2YM', 'VIXCLS', 'VIX', 'NAPM', 'ISM_MANUFACTURING', 'MANUFACTURING_PMI')
    """)
    fred_series = {row['series'] for row in cur.fetchall()}
    spread_series = next((s for s in ['T10Y2Y', 'T10Y2YM'] if s in fred_series), None)
    vix_series = next((s for s in ['VIXCLS', 'VIX'] if s in fred_series), None)
    ism_series = next((s for s in ['NAPM', 'ISM_MANUFACTURING', 'MANUFACTURING_PMI'] if s in fred_series), None)

    if not all([spread_series, vix_series, ism_series]):
        print("INSUFFICIENT=1")
        return 0

    # 2. Load macro data, aligned to trading days (from bars 1d)
    cur.execute("""
        SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts
    """)
    trading_days = [row['ts'] for row in cur.fetchall()]
    if len(trading_days) < 252:
        print("INSUFFICIENT=1")
        return 0

    day_to_idx = {ts: i for i, ts in enumerate(trading_days)}

    # Load macro values for each trading day (use latest value on or before each trading day)
    macro_data = {}
    for series, name in [(spread_series, 'spread'), (vix_series, 'vix'), (ism_series, 'ism')]:
        cur.execute("SELECT ts, value FROM macro_series WHERE series=? ORDER BY ts", (series,))
        rows = cur.fetchall()
        if not rows:
            print("INSUFFICIENT=1")
            return 0
        macro_ts = [r['ts'] for r in rows]
        macro_val = [r['value'] for r in rows]
        aligned = []
        j = 0
        for td in trading_days:
            while j + 1 < len(macro_ts) and macro_ts[j + 1] <= td:
                j += 1
            if macro_ts[j] <= td:
                aligned.append(macro_val[j])
            else:
                aligned.append(None)
        macro_data[name] = aligned

    # 3. Load symbols with daily bars
    cur.execute("SELECT id, symbol FROM symbols WHERE active=1")
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}

    # 4. Load daily bars for price/volume calculations (symbol_id, ts, close, volume)
    cur.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close'], row['volume']))

    # 5. Load daily sentiment from sentiment_features (symbol_id, day, mean_score)
    cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features ORDER BY symbol_id, day")
    sent_by_symbol = defaultdict(dict)
    for row in cur.fetchall():
        sent_by_symbol[row['symbol_id']][row['day']] = row['mean_score']

    # 6. Load prediction_outcomes for horizon=10 (labels)
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon=10
    """)
    labels = {}
    for row in cur.fetchall():
        labels[(row['symbol_id'], row['ts'])] = (row['up'], row['fwd_return'])

    # 7. For each symbol, compute rolling metrics and generate decision points
    all_decisions = []  # (decision_ts, symbol_id, issued, label_up, label_fwd)

    # Precompute trading day to date string mapping
    ts_to_date = {}
    for ts in trading_days:
        import datetime
        ts_to_date[ts] = datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

    for sym_id, bars in bars_by_symbol.items():
        if len(bars) < 252:
            continue
        if sym_id not in sent_by_symbol or not sent_by_symbol[sym_id]:
            continue

        # Build arrays aligned to trading_days
        n_days = len(trading_days)
        close_arr = [None] * n_days
        vol_arr = [None] * n_days
        for ts, close, vol in bars:
            if ts in day_to_idx:
                close_arr[day_to_idx[ts]] = close
                vol_arr[day_to_idx[ts]] = vol

        # Forward fill missing bars (weekends/holidays already excluded from trading_days)
        last_close = None
        last_vol = None
        for i in range(n_days):
            if close_arr[i] is not None:
                last_close = close_arr[i]
                last_vol = vol_arr[i]
            elif last_close is not None:
                close_arr[i] = last_close
                vol_arr[i] = last_vol

        # Sentiment aligned to trading_days
        sent_arr = [None] * n_days
        for i, ts in enumerate(trading_days):
            date_str = ts_to_date[ts]
            if date_str in sent_by_symbol[sym_id]:
                sent_arr[i] = sent_by_symbol[sym_id][date_str]

        # Forward fill sentiment (use last available)
        last_sent = None
        for i in range(n_days):
            if sent_arr[i] is not None:
                last_sent = sent_arr[i]
            elif last_sent is not None:
                sent_arr[i] = last_sent

        # Compute 20-day returns, 20-day vol, 5-day avg sentiment, 252-day sentiment percentile
        # Only for days where we have 252 prior sessions
        for i in range(252, n_days - 10):  # -10 for label horizon
            # Universe filters at T (index i)
            close_T = close_arr[i]
            if close_T is None or close_T < 5.0:
                continue

            # ADV >= $5M over T-60..T-1
            adv_sum = 0.0
            adv_count = 0
            for j in range(max(0, i-60), i):
                if close_arr[j] is not None and vol_arr[j] is not None:
                    adv_sum += close_arr[j] * vol_arr[j]
                    adv_count += 1
            if adv_count == 0 or (adv_sum / adv_count) < 5_000_000:
                continue

            # 20-day return (T-20 to T)
            close_T20 = close_arr[i-20] if i >= 20 else None
            if close_T20 is None or close_T20 <= 0:
                continue
            ret_20 = (close_T / close_T20) - 1.0
            if not (0.05 <= ret_20 <= 0.25):
                continue

            # 20-day realized volatility (daily returns std)
            rets = []
            for j in range(i-19, i+1):
                if j > 0 and close_arr[j] is not None and close_arr[j-1] is not None and close_arr[j-1] > 0:
                    rets.append(math.log(close_arr[j] / close_arr[j-1]))
            if len(rets) < 10:
                continue
            vol_20 = math.sqrt(sum((r - sum(rets)/len(rets))**2 for r in rets) / len(rets)) * math.sqrt(252)

            # 5-day average sentiment (T-5 to T-1)
            sent_5 = []
            for j in range(i-5, i):
                if sent_arr[j] is not None:
                    sent_5.append(sent_arr[j])
            if len(sent_5) < 3:
                continue
            avg_sent_5 = sum(sent_5) / len(sent_5)

            # 252-day rolling sentiment percentile (T-252 to T-1)
            sent_252 = []
            for j in range(i-252, i):
                if sent_arr[j] is not None:
                    sent_252.append(sent_arr[j])
            if len(sent_252) < 100:
                continue
            sent_252_sorted = sorted(sent_252)
            sent_pct = percentile_rank(sent_252_sorted, avg_sent_5)
            if sent_pct < 85:  # top 15%
                continue

            # Regime at T-1 (index i-1)
            if i-1 < 0:
                continue
            spread = macro_data['spread'][i-1]
            vix = macro_data['vix'][i-1]
            ism = macro_data['ism'][i-1]
            if spread is None or vix is None or ism is None:
                continue
            if not (spread > 0 and vix < 20 and ism > 50):
                continue

            # Cross-sectional 20-day vol decile check (will do after collecting all)
            # Store for later filtering
            all_decisions.append({
                'ts': trading_days[i],
                'idx': i,
                'sym_id': sym_id,
                'vol_20': vol_20,
                'close_T': close_T,
            })

    if not all_decisions:
        print("INSUFFICIENT=1")
        return 0

    # 8. Cross-sectional vol decile filter at each decision date
    # Group by decision date
    by_date = defaultdict(list)
    for d in all_decisions:
        by_date[d['ts']].append(d)

    # Compute 90th percentile vol per date
    date_vol_thresh = {}
    for ts, items in by_date.items():
        vols = [d['vol_20'] for d in items]
        if len(vols) < 30:  # fewer than 30 independent observations
            date_vol_thresh[ts] = None  # will abstain
            continue
        vols_sorted = sorted(vols)
        thresh = vols_sorted[int(0.9 * (len(vols_sorted) - 1))]
        date_vol_thresh[ts] = thresh

    # 9. Apply vol filter, no-repeat filter, and collect final calls with labels
    issued_calls = []  # (ts, sym_id, label_up, label_fwd)
    last_call = {}  # sym_id -> last call ts

    for d in all_decisions:
        ts = d['ts']
        sym_id = d['sym_id']
        thresh = date_vol_thresh.get(ts)
        if thresh is None:
            continue
        if d['vol_20'] > thresh:
            continue
        # No call for same symbol in prior 20 days
        if sym_id in last_call:
            last_ts = last_call[sym_id]
            if ts - last_ts < 20 * 86400:  # approx 20 trading days in seconds
                continue
        # Get label at T+10
        label_key = (sym_id, ts)
        if label_key not in labels:
            continue
        up, fwd = labels[label_key]
        issued_calls.append((ts, sym_id, up, fwd))
        last_call[sym_id] = ts

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # 10. Split into sealed era (most recent 20% of decision points by time)
    # Use all decision points (opportunities) for era split, not just issued
    all_decision_ts = sorted(set(d['ts'] for d in all_decisions))
    n_total = len(all_decision_ts)
    split_idx = int(n_total * 0.8)
    sealed_start_ts = all_decision_ts[split_idx] if split_idx < n_total else all_decision_ts[-1]

    # Separate issued calls
    in_sample = [c for c in issued_calls if c[0] < sealed_start_ts]
    sealed = [c for c in issued_calls if c[0] >= sealed_start_ts]

    # 11. Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 1.0
        issued = len(calls)
        hits = sum(1 for c in calls if c[2] == 1)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (UP) within issued subset
        distinct_days = len(set(c[0] for c in calls))
        # Design effect: 1 + (avg_cluster_size - 1) * rho
        # Estimate rho from autocorrelation of daily call counts
        day_counts = defaultdict(int)
        for c in calls:
            day_counts[c[0]] += 1
        counts = list(day_counts.values())
        if len(counts) > 1:
            mean_c = sum(counts) / len(counts)
            var_c = sum((c - mean_c)**2 for c in counts) / len(counts)
            rho = max(0, (var_c - mean_c) / (mean_c * (mean_c - 1)) if mean_c > 1 else 0)
            deff = 1 + (mean_c - 1) * rho
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_in, hits_in, prec_in, br_in, dd_in, en_in = compute_metrics(in_sample)
    issued_seal, hits_seal, prec_seal, br_seal, dd_seal, en_seal = compute_metrics(sealed)

    # Opportunities = all decision points considered (before abstentions)
    # But we need opportunities in the same era split
    opp_in = sum(1 for ts in all_decision_ts if ts < sealed_start_ts)
    opp_seal = sum(1 for ts in all_decision_ts if ts >= sealed_start_ts)

    # Report in-sample (primary) and sealed
    print(f"ISSUED={issued_in}")
    print(f"OPPORTUNITIES={opp_in}")
    print(f"PRECISION={prec_in:.6f}")
    print(f"BASE_RATE={br_in:.6f}")
    print(f"DISTINCT_DAYS={dd_in}")
    print(f"EFFECTIVE_N={en_in:.2f}")
    print(f"SEALED_PRECISION={prec_seal:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
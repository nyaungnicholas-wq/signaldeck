# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 618
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import sys
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load DFF series from macro_series
    cur.execute("SELECT ts, value FROM macro_series WHERE series = 'DFF' ORDER BY ts")
    dff_rows = cur.fetchall()
    if len(dff_rows) < 61:
        print("INSUFFICIENT=1")
        return

    dff_ts = [r['ts'] for r in dff_rows]
    dff_val = [r['value'] for r in dff_rows]

    # Compute day-over-day changes
    dff_chg = [dff_val[i] - dff_val[i-1] for i in range(1, len(dff_val))]
    dff_chg_ts = dff_ts[1:]

    # Trailing 60-day std of changes, identify surprises (>3σ)
    surprises = []  # (ts, change, sign)
    for i in range(60, len(dff_chg)):
        window = dff_chg[i-60:i]
        mean_chg = sum(window) / 60
        var = sum((x - mean_chg)**2 for x in window) / 60
        std = math.sqrt(var) if var > 0 else 0
        if std > 0 and abs(dff_chg[i]) > 3 * std:
            surprises.append((dff_chg_ts[i], dff_chg[i], 1 if dff_chg[i] > 0 else -1))

    if not surprises:
        print("INSUFFICIENT=1")
        return

    # 2. Get all symbols with 1d bars
    cur.execute("""
        SELECT s.id, s.symbol, MIN(b.ts) as first_ts, MAX(b.ts) as last_ts, COUNT(*) as n_bars
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
        GROUP BY s.id
        HAVING n_bars >= 250
    """)
    symbols = cur.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        return

    symbol_ids = [s['id'] for s in symbols]

    # 3. Load 1d bars for these symbols (close prices)
    # We need returns aligned with DFF timestamps (unix epoch days)
    # Bars ts is unix epoch; DFF ts is also unix epoch (daily)
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bar_rows = cur.fetchall()

    # Organize bars by symbol_id
    bars_by_sym = defaultdict(list)
    for r in bar_rows:
        bars_by_sym[r['symbol_id']].append((r['ts'], r['close']))

    # 4. For each symbol, compute daily log returns aligned to DFF dates
    # DFF is daily (business days), bars are daily. Align by timestamp.
    dff_ts_set = set(dff_ts)
    sym_returns = {}  # symbol_id -> {ts: log_return}
    for sym_id, bars in bars_by_sym.items():
        if len(bars) < 2:
            continue
        rets = {}
        for i in range(1, len(bars)):
            ts_prev, close_prev = bars[i-1]
            ts_curr, close_curr = bars[i]
            if close_prev > 0 and close_curr > 0:
                rets[ts_curr] = math.log(close_curr / close_prev)
        sym_returns[sym_id] = rets

    # 5. For each surprise date, compute 60-day beta for each symbol (trailing 60 days)
    # Beta = Cov(r_stock, r_dff) / Var(r_dff) over trailing 60 days
    # DFF changes are in dff_chg aligned to dff_chg_ts
    dff_chg_by_ts = dict(zip(dff_chg_ts, dff_chg))

    # Precompute DFF returns (changes) for beta calculation
    # We need overlapping dates between symbol returns and DFF changes
    all_dates = sorted(set(dff_chg_ts) & set().union(*[set(r.keys()) for r in sym_returns.values()]))

    # For each symbol, build aligned series of (date, stock_ret, dff_chg)
    aligned_by_sym = {}
    for sym_id, rets in sym_returns.items():
        aligned = []
        for ts in all_dates:
            if ts in rets and ts in dff_chg_by_ts:
                aligned.append((ts, rets[ts], dff_chg_by_ts[ts]))
        if len(aligned) >= 60:
            aligned_by_sym[sym_id] = aligned

    # 6. Process each surprise as decision point
    # Decision timestamp = surprise ts (DFF date)
    # Need 60-day trailing beta computed as of that date (using data strictly before decision)
    # Label: prediction_outcomes with horizon=21 (trading days), ts = decision ts
    # Predict direction opposite to sign of rate change: if DFF up (positive surprise), predict DOWN (up=0)
    # If DFF down (negative surprise), predict UP (up=1)

    # Load prediction_outcomes for horizon=21
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    outcomes = cur.fetchall()
    outcome_map = {(r['symbol_id'], r['ts']): (r['up'], r['fwd_return']) for r in outcomes}

    # For each surprise, find eligible symbols (top decile beta, 250+ days history)
    decisions = []  # (decision_ts, symbol_id, predicted_up, actual_up, surprise_sign)
    opportunities = 0

    for surprise_ts, surprise_chg, surprise_sign in surprises:
        # Compute beta for each symbol as of surprise_ts (using trailing 60 days strictly before)
        betas = []
        for sym_id, aligned in aligned_by_sym.items():
            # Filter aligned dates < surprise_ts
            trailing = [(ts, sr, dr) for ts, sr, dr in aligned if ts < surprise_ts]
            if len(trailing) < 60:
                continue
            trailing = trailing[-60:]
            n = len(trailing)
            mean_sr = sum(sr for _, sr, _ in trailing) / n
            mean_dr = sum(dr for _, _, dr in trailing) / n
            cov = sum((sr - mean_sr) * (dr - mean_dr) for _, sr, dr in trailing) / n
            var_dr = sum((dr - mean_dr)**2 for _, _, dr in trailing) / n
            if var_dr > 0:
                beta = cov / var_dr
                betas.append((sym_id, beta))

        if not betas:
            continue

        # Top decile beta (highest absolute beta? or highest beta? "rate-sensitivity" suggests absolute)
        # "60-day beta to DFF is in top decile" - typically beta magnitude for sensitivity
        betas.sort(key=lambda x: abs(x[1]), reverse=True)
        top_k = max(1, len(betas) // 10)
        top_symbols = set(sym_id for sym_id, _ in betas[:top_k])

        for sym_id in top_symbols:
            opportunities += 1
            # Check if outcome exists for this symbol at this decision timestamp
            key = (sym_id, surprise_ts)
            if key in outcome_map:
                actual_up, fwd_ret = outcome_map[key]
                # Predict opposite to sign of rate change
                # surprise_sign = 1 means DFF increased -> predict DOWN (up=0)
                # surprise_sign = -1 means DFF decreased -> predict UP (up=1)
                predicted_up = 0 if surprise_sign > 0 else 1
                decisions.append((surprise_ts, sym_id, predicted_up, actual_up, surprise_sign))

    if not decisions:
        print("INSUFFICIENT=1")
        return

    # 7. Hold out most recent 20% as sealed era
    # Sort by decision_ts
    decisions.sort(key=lambda x: x[0])
    n_total = len(decisions)
    n_sealed = max(1, n_total // 5)
    sealed_decisions = decisions[-n_sealed:]
    train_decisions = decisions[:-n_sealed]

    # 8. Compute metrics on full set and sealed era
    def compute_metrics(dec_list):
        if not dec_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(dec_list)
        hits = sum(1 for d in dec_list if d[2] == d[3])  # predicted_up == actual_up
        precision = hits / issued if issued > 0 else 0.0
        # Base rate of predicted class within issued subset
        predicted_ups = sum(1 for d in dec_list if d[2] == 1)
        base_rate = predicted_ups / issued if issued > 0 else 0.0
        # Distinct UTC days among issued calls
        distinct_days = len(set(d[0] // 86400 for d in dec_list))  # ts is unix epoch, convert to day
        # Design effect: cluster by day, compute effective N
        # Simple design effect: 1 + (avg_cluster_size - 1) * ICC
        # Use conservative ICC=0.5 for financial returns clustered in time
        day_counts = defaultdict(int)
        for d in dec_list:
            day = d[0] // 86400
            day_counts[day] += 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        icc = 0.5
        deff = 1 + (avg_cluster - 1) * icc
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(decisions)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_decisions)

    # 9. Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
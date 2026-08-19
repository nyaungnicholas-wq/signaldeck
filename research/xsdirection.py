#!/usr/bin/env python3
"""Cross-sectional directional model vs naive majority-class baseline.

Question: does a cross-sectional directional model beat the naive
majority-class baseline out-of-sample, on 8 years of daily bars?

Walk-forward by calendar year. Train on all days STRICTLY BEFORE test year Y
minus an EMBARGO of 10 trading days; test on days in Y. As-of discipline:
features use only bars at or before day t; the label up = 1 if C_{t+1} > C_t
uses the NEXT available 1d bar for that symbol. Predictions are clustered by
day for confidence intervals.
"""

import argparse
import json
import math
import sqlite3
import sys
from collections import defaultdict

import numpy as np


def eprint(*a, **k):
    print(*a, file=sys.stderr, flush=True, **k)


# ---------------------------------------------------------------------------
# Data loading: stream per symbol to stay memory-aware.
# ---------------------------------------------------------------------------

def load_symbols(db_path):
    """Return list of (id, symbol) for active stocks."""
    uri = f"file:{db_path}?mode=ro"
    conn = sqlite3.connect(uri, uri=True)
    conn.row_factory = sqlite3.Row
    rows = conn.execute(
        "SELECT id, symbol FROM symbols WHERE market='stocks' ORDER BY id"
    ).fetchall()
    conn.close()
    return [(r["id"], r["symbol"]) for r in rows]


def load_bars_for_symbol(conn, symbol_id):
    """Stream all 1d bars for one symbol, ordered by timestamp."""
    cur = conn.execute(
        "SELECT ts, close, volume FROM bars "
        "WHERE symbol_id=? AND tf='1d' ORDER BY ts",
        (symbol_id,),
    )
    return cur.fetchall()


# ---------------------------------------------------------------------------
# Feature / label computation per symbol (as-of discipline enforced here).
# ---------------------------------------------------------------------------

def compute_observations(rows):
    """Given list of (ts, close, volume) for one symbol, yield observation
    dicts with features computed STRICTLY AT OR BEFORE day t.

    For day t (index i in the sorted bar list):
      - label up = 1 if close[i+1] > close[i]  (next available 1d bar)
      - r1  = close[i]/close[i-1] - 1
      - r5  = close[i]/close[i-5] - 1
      - r21 = close[i]/close[i-21] - 1
      - r63 = close[i]/close[i-63] - 1
      - vol21 = stdev of daily returns over last 21 days ending at t
      - dvol  = log1p(volume[i]) - mean(log1p(volume)) over last 21 days

    All lookbacks use only bars at indices <= i (as-of discipline).
    The label uses bar i+1 which is the next available bar after t.
    """
    n = len(rows)
    if n < 65:  # need at least 64 lookback + 1 forward
        return

    ts = np.array([r[0] for r in rows], dtype=np.int64)
    close = np.array([r[1] for r in rows], dtype=np.float64)
    volume = np.array([r[2] for r in rows], dtype=np.float64)

    # Daily returns: ret[i] = close[i]/close[i-1] - 1, defined for i >= 1
    ret = np.empty(n, dtype=np.float64)
    ret[0] = 0.0
    for i in range(1, n):
        ret[i] = close[i] / close[i - 1] - 1.0

    logvol = np.log1p(volume)

    # Precompute rolling mean and std of returns over 21 days ending at i.
    # vol21[i] = stdev(ret[i-20..i])  (21 values)
    # dvol[i]  = logvol[i] - mean(logvol[i-20..i])

    for i in range(63, n - 1):  # need i-63 >= 0 and i+1 < n
        # Features (all from bars at or before index i)
        r1 = ret[i]
        r5 = close[i] / close[i - 5] - 1.0
        r21 = close[i] / close[i - 21] - 1.0
        r63 = close[i] / close[i - 63] - 1.0

        # vol21: stdev of daily returns over last 21 days ending at t (indices i-20..i)
        window_ret = ret[i - 20 : i + 1]
        vol21 = float(np.std(window_ret, ddof=1))

        # dvol: log1p(vol_t) - mean(log1p(vol)) over last 21 days ending at t
        window_lv = logvol[i - 20 : i + 1]
        dvol = float(logvol[i] - np.mean(window_lv))

        # Label: next available bar after t
        up = 1 if close[i + 1] > close[i] else 0
        fwd_ret = float(close[i + 1] / close[i] - 1.0)

        yield {
            "ts": int(ts[i]),
            "up": up,
            "fwd_ret": fwd_ret,
            "r1": r1,
            "r5": r5,
            "r21": r21,
            "r63": r63,
            "vol21": vol21,
            "dvol": dvol,
        }


FEATURE_NAMES = ["r1", "r5", "r21", "r63", "vol21", "dvol"]


def collect_all_observations(db_path):
    """Stream per symbol, collect observations into per-day buckets.

    Returns:
        day_obs: dict mapping day_key -> list of observation dicts
        symbol_ids: set of symbol ids that contributed
    """
    uri = f"file:{db_path}?mode=ro"
    conn = sqlite3.connect(uri, uri=True)

    symbols = load_symbols(db_path)
    eprint(f"Loaded {len(symbols)} stock symbols from DB")

    day_obs = defaultdict(list)
    seen_symbols = set()

    for idx, (sid, sym) in enumerate(symbols):
        if idx % 200 == 0:
            eprint(f"  Processing symbol {idx}/{len(symbols)}...")
        rows = load_bars_for_symbol(conn, sid)
        if len(rows) < 65:
            continue
        seen_symbols.add(sid)
        for obs in compute_observations(rows):
            # day_key: convert ts to date (YYYYMMDD as int for easy year extraction)
            day_key = obs["ts"]
            day_obs[day_key].append(obs)

    conn.close()
    eprint(f"Collected observations for {len(seen_symbols)} symbols across "
           f"{len(day_obs)} distinct days")
    return day_obs, seen_symbols


# ---------------------------------------------------------------------------
# Cross-sectional ranking within each day.
# ---------------------------------------------------------------------------

def rank_cross_sectional(values):
    """Rank values to [-0.5, +0.5]: rank/(n-1) - 0.5.

    Ties get average rank. Returns float array.
    """
    n = len(values)
    if n <= 1:
        return np.zeros(n, dtype=np.float64)
    arr = np.asarray(values, dtype=np.float64)
    # Average rank for ties
    order = np.argsort(arr, kind="mergesort")
    ranks = np.empty(n, dtype=np.float64)
    i = 0
    while i < n:
        j = i
        while j < n and arr[order[j]] == arr[order[i]]:
            j += 1
        avg_rank = (i + j - 1) / 2.0  # 0-based average rank
        for k in range(i, j):
            ranks[order[k]] = avg_rank
        i = j
    return ranks / (n - 1) - 0.5


def build_dataset(day_obs, shuffle_labels=False, seed=0, target="absolute"):
    """Build feature matrix X, labels y, and day indices from day_obs.

    Cross-sectional ranking is applied within each day.
    Drops any (symbol, day) missing a feature (NaN/Inf).

    Returns:
        X: (N, 6) float64 ranked features
        y: (N,) int labels
        days: (N,) int day keys (ts)
        sorted_day_keys: sorted list of unique day keys
    """
    sorted_day_keys = sorted(day_obs.keys())

    X_list = []
    y_list = []
    days_list = []

    rng = np.random.RandomState(seed)

    for dk in sorted_day_keys:
        obs_list = day_obs[dk]
        n = len(obs_list)
        if n == 0:
            continue

        # Build raw feature matrix for this day
        raw = np.empty((n, len(FEATURE_NAMES)), dtype=np.float64)
        labels = np.empty(n, dtype=np.int64)
        fwd = np.empty(n, dtype=np.float64)
        for j, obs in enumerate(obs_list):
            raw[j, 0] = obs["r1"]
            raw[j, 1] = obs["r5"]
            raw[j, 2] = obs["r21"]
            raw[j, 3] = obs["r63"]
            raw[j, 4] = obs["vol21"]
            raw[j, 5] = obs["dvol"]
            labels[j] = obs["up"]
            fwd[j] = obs["fwd_ret"]

        # RELATIVE target: did this symbol beat the day's own median forward
        # return? A cross-sectional model ranks symbols against each other on
        # one day (INVERSION_INVESTIGATION_2026-08-08 makes the same point), so
        # that is the target it is actually built for. It is also the honest one
        # to score: the split is balanced by construction, the null is a real
        # 50%, and the drift-hedging artifact that inflated the absolute test
        # cannot arise. It uses no information the absolute label did not.
        if target == "relative" and n > 1:
            med = float(np.median(fwd))
            labels = (fwd > med).astype(np.int64)

        # Shuffle labels within day if requested (leakage canary)
        if shuffle_labels:
            labels = rng.permutation(labels)

        # Drop rows with any NaN/Inf feature
        valid = np.all(np.isfinite(raw), axis=1)
        if not np.all(valid):
            raw = raw[valid]
            labels = labels[valid]

        n_valid = len(raw)
        if n_valid <= 1:
            continue

        # Cross-sectional rank each feature within this day
        ranked = np.empty_like(raw)
        for f in range(len(FEATURE_NAMES)):
            ranked[:, f] = rank_cross_sectional(raw[:, f])

        X_list.append(ranked)
        y_list.append(labels)
        days_list.append(np.full(n_valid, dk, dtype=np.int64))

    if not X_list:
        return (np.zeros((0, len(FEATURE_NAMES))), np.zeros(0, dtype=np.int64),
                np.zeros(0, dtype=np.int64), sorted_day_keys)

    X = np.vstack(X_list)
    y = np.concatenate(y_list)
    days = np.concatenate(days_list)

    return X, y, days, sorted_day_keys


# ---------------------------------------------------------------------------
# Logistic regression via gradient descent (hand-written, ~30 lines).
# ---------------------------------------------------------------------------

def logistic_fit(X, y, l2=1e-3, lr=0.1, n_iter=500, tol=1e-6):
    """Fit logistic regression with intercept and small L2 via gradient descent.

    Returns (weights, intercept) where weights has shape (n_features,).
    """
    n, d = X.shape
    w = np.zeros(d, dtype=np.float64)
    b = 0.0

    for it in range(n_iter):
        z = X @ w + b
        # Clip z for numerical stability
        z = np.clip(z, -30, 30)
        p = 1.0 / (1.0 + np.exp(-z))
        err = p - y  # gradient of log-likelihood w.r.t. z

        gw = X.T @ err / n + l2 * w
        gb = np.mean(err)

        w_new = w - lr * gw
        b_new = b - lr * gb

        if np.max(np.abs(w_new - w)) < tol and abs(b_new - b) < tol:
            w, b = w_new, b_new
            break
        w, b = w_new, b_new

    return w, b


def logistic_predict_proba(X, w, b):
    z = X @ w + b
    z = np.clip(z, -30, 30)
    return 1.0 / (1.0 + np.exp(-z))


# ---------------------------------------------------------------------------
# Walk-forward evaluation.
# ---------------------------------------------------------------------------

def ts_to_year(ts):
    """Convert unix epoch seconds to calendar year."""
    import datetime
    return datetime.datetime.utcfromtimestamp(ts).year


def walk_forward_evaluate(X, y, days, sorted_day_keys):
    """Walk-forward by calendar year.

    For each test year Y from the 4th year onward:
      - Train on all days STRICTLY BEFORE Y minus an EMBARGO of 10 trading days.
        The embargo removes the last 10 trading days before Y so that no
        information from the test period leaks into training.
      - Test on days in Y.
      - Never train on data at or after the test period.

    Returns list of fold dicts and overall stats.
    """
    if len(days) == 0:
        return [], None

    # Map each day key to its year
    day_to_year = {}
    for dk in sorted_day_keys:
        day_to_year[dk] = ts_to_year(dk)

    years = sorted(set(day_to_year.values()))

    # Index arrays: for each day key, the row indices in X/y
    day_to_rows = defaultdict(list)
    for i, dk in enumerate(days):
        day_to_rows[dk].append(i)

    folds = []
    all_test_preds = []  # (day_key, correct, baseline_correct) per row aggregated per day
    per_day_diff = []  # (day_key, acc - baseline) per test day

    # "From the 4th year onward" means we need at least 3 prior years of data.
    # Test years start from years[3] (0-indexed), i.e., the 4th distinct year.
    for yi in range(3, len(years)):
        Y = years[yi]

        # Training days: all days STRICTLY BEFORE year Y, minus embargo of 10
        # trading days. The embargo removes the 10 most recent training days
        # before Y to prevent leakage from the boundary.
        train_days_all = [dk for dk in sorted_day_keys if day_to_year[dk] < Y]
        if len(train_days_all) <= 10:
            continue

        # EMBARGO: drop the last 10 trading days before Y
        train_days = train_days_all[:-10]

        # Test days: all days in year Y
        test_days = [dk for dk in sorted_day_keys if day_to_year[dk] == Y]
        if not test_days:
            continue

        # Build train set
        train_idx = []
        for dk in train_days:
            train_idx.extend(day_to_rows[dk])
        if not train_idx:
            continue

        # Build test set
        test_idx = []
        for dk in test_days:
            test_idx.extend(day_to_rows[dk])
        if not test_idx:
            continue

        train_idx = np.array(train_idx, dtype=np.int64)
        test_idx = np.array(test_idx, dtype=np.int64)

        X_train = X[train_idx]
        y_train = y[train_idx]
        X_test = X[test_idx]
        y_test = y[test_idx]

        # Fit model
        w, b = logistic_fit(X_train, y_train.astype(np.float64))

        # Predict
        p_test = logistic_predict_proba(X_test, w, b)
        pred_up = (p_test > 0.5).astype(np.int64)

        # Accuracy
        correct = (pred_up == y_test).astype(np.float64)
        acc = float(np.mean(correct))

        # Baseline: PREQUENTIAL majority class — the side that was the majority
        # in TRAINING, applied as a fixed rule to every test row.
        #
        # It must not be max(mean(y_test), 1-mean(y_test)): that picks the
        # winning side after seeing the test labels. Pooled over a year the
        # difference is small, but PER DAY it is large — a ~300-symbol day's
        # realised majority is a hindsight rule no forecaster could have run,
        # and clustering on it put the daily mean 12pp below the pooled lift,
        # i.e. a CI that did not contain its own point estimate. This is also
        # the null the accuracy registry means by "prequential-majority".
        baseline_pred = 1 if float(np.mean(y_train)) > 0.5 else 0
        baseline_correct = (y_test == baseline_pred).astype(np.float64)
        baseline = float(np.mean(baseline_correct))

        lift_pp = (acc - baseline) * 100.0

        # Per-day differences for clustering
        test_days_arr = days[test_idx]
        unique_test_days = sorted(set(test_days_arr.tolist()))
        for dk in unique_test_days:
            mask = test_days_arr == dk
            day_correct = correct[mask]
            day_acc = float(np.mean(day_correct))
            day_baseline = float(np.mean(baseline_correct[mask]))
            per_day_diff.append((dk, day_acc - day_baseline))

        n_test = len(test_idx)
        n_test_days = len(unique_test_days)

        folds.append({
            "year": Y,
            "n": n_test,
            "n_days": n_test_days,
            "acc": acc,
            "baseline": baseline,
            "lift_pp": lift_pp,
        })

        eprint(f"  Fold {Y}: n={n_test}, days={n_test_days}, "
               f"acc={acc:.4f}, baseline={baseline:.4f}, lift={lift_pp:+.2f}pp")

    # Overall stats
    if not per_day_diff:
        return folds, None

    diffs = np.array([d[1] for d in per_day_diff])  # (acc - baseline) per day
    n_days = len(diffs)
    mean_diff = float(np.mean(diffs))

    # 95% CI on mean of daily differences using t distribution with (n_days - 1) df
    if n_days > 1:
        se = float(np.std(diffs, ddof=1) / math.sqrt(n_days))
        # t critical value for 95% CI, df = n_days - 1
        t_crit = _t_critical_95(n_days - 1)
        ci_lo = (mean_diff * 100.0) - t_crit * se * 100.0
        ci_hi = (mean_diff * 100.0) + t_crit * se * 100.0
    else:
        ci_lo = float("nan")
        ci_hi = float("nan")

    # Overall accuracy and baseline (pooled across all test rows)
    # Recompute from per-day data
    total_correct = 0
    total_n = 0
    total_up = 0
    for dk, _ in per_day_diff:
        rows_for_day = day_to_rows[dk]
        # We need to recompute from stored predictions... but we didn't store them.
        # Instead, recompute overall from the folds' weighted average.
        pass

    # Simpler: compute overall acc and baseline from all test rows across folds
    # We need to re-run prediction for all test folds pooled. But we already have
    # per-day diffs. Let's compute overall acc and baseline from per-day data.
    # We stored (dk, acc - baseline) but not acc and baseline separately per day.
    # Let's recompute by storing more info.

    # Actually, let's just recompute overall from the folds' row counts.
    # overall_acc = sum(fold_acc * fold_n) / sum(fold_n)
    # overall_baseline = sum(fold_baseline * fold_n) / sum(fold_n)
    total_n = sum(f["n"] for f in folds)
    if total_n > 0:
        overall_acc = sum(f["acc"] * f["n"] for f in folds) / total_n
        overall_baseline = sum(f["baseline"] * f["n"] for f in folds) / total_n
    else:
        overall_acc = 0.0
        overall_baseline = 0.0

    overall_lift_pp = (overall_acc - overall_baseline) * 100.0

    overall = {
        "acc": overall_acc,
        "baseline": overall_baseline,
        "lift_pp": overall_lift_pp,
        "ci_lo_pp": ci_lo,
        "ci_hi_pp": ci_hi,
        "n_days": n_days,
    }

    return folds, overall


def _t_critical_95(df):
    """Approximate t critical value for two-sided 95% CI.

    Uses a reasonable approximation. For large df, converges to 1.96.
    """
    if df < 1:
        return float("nan")
    if df >= 1000:
        return 1.96
    # Cornish-Fisher expansion approximation
    # t_p ≈ z_p + (z_p^3 + z_p) / (4*df) + ...
    z = 1.959963985  # z_{0.975}
    t = z + (z**3 + z) / (4.0 * df) + (5 * z**5 + 16 * z**3 + 3 * z) / (96.0 * df**2)
    return t


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Cross-sectional directional model vs majority-class baseline"
    )
    parser.add_argument("--target", choices=["absolute", "relative"],
                        default="absolute",
                        help="absolute = will it rise; relative = will it beat "
                             "the day's median forward return (null is 50%%)")
    parser.add_argument("--shuffle-labels", action="store_true",
                        help="Randomly permute up labels within each day (leakage canary)")
    parser.add_argument("--seed", type=int, default=0,
                        help="Random seed for label shuffling")
    parser.add_argument("--json", type=str, default=None,
                        help="Write result object to PATH")
    parser.add_argument("--db", type=str, default="data/signaldeck.db",
                        help="Path to SQLite database (default: data/signaldeck.db)")
    args = parser.parse_args()

    eprint("=== Cross-Sectional Directional Model ===")
    eprint(f"Mode: {'shuffled' if args.shuffle_labels else 'normal'}")
    eprint(f"DB: {args.db}")
    eprint()

    # Step 1: Load and collect observations (streaming per symbol)
    eprint("Step 1: Loading bars and computing features/labels per symbol...")
    day_obs, seen_symbols = collect_all_observations(args.db)

    if not day_obs:
        eprint("ERROR: No observations collected. Check database.")
        sys.exit(1)

    # Step 2: Build dataset with cross-sectional ranking
    eprint("\nStep 2: Building dataset with cross-sectional ranking...")
    X, y, days, sorted_day_keys = build_dataset(
        day_obs, shuffle_labels=args.shuffle_labels, seed=args.seed,
        target=args.target
    )

    n_obs = X.shape[0]
    distinct_days = len(sorted(set(days.tolist())))
    n_symbols = len(seen_symbols)

    eprint(f"  n_obs = {n_obs}")
    eprint(f"  distinct_days = {distinct_days}")
    eprint(f"  n_symbols = {n_symbols}")

    # Step 3: Walk-forward evaluation
    eprint("\nStep 3: Walk-forward evaluation by calendar year...")
    folds, overall = walk_forward_evaluate(X, y, days, sorted_day_keys)

    if not folds:
        eprint("ERROR: No folds could be evaluated. Need at least 4 years of data.")
        sys.exit(1)

    # Step 4: Print readable table
    eprint("\n" + "=" * 80)
    mode_str = "SHUFFLED (canary)" if args.shuffle_labels else "NORMAL"
    print(f"\nCross-Sectional Directional Model — {mode_str}")
    print(f"n_obs={n_obs}  distinct_days={distinct_days}  n_symbols={n_symbols}")
    print()
    print(f"{'Year':>6}  {'N':>7}  {'Days':>5}  {'Acc':>8}  {'Base':>8}  {'Lift':>8}")
    print("-" * 52)
    for f in folds:
        print(f"{f['year']:>6}  {f['n']:>7}  {f['n_days']:>5}  "
              f"{f['acc']:>8.4f}  {f['baseline']:>8.4f}  {f['lift_pp']:>+8.2f}")
    print("-" * 52)
    if overall:
        print(f"{'OVERALL':>6}  {'':>7}  {overall['n_days']:>5}  "
              f"{overall['acc']:>8.4f}  {overall['baseline']:>8.4f}  "
              f"{overall['lift_pp']:>+8.2f}")
        print(f"\n95% CI on daily lift: [{overall['ci_lo_pp']:+.2f}, {overall['ci_hi_pp']:+.2f}] pp "
              f"({overall['n_days']} days)")
    print()

    # Step 5: Emit JSON
    result = {
        "mode": "shuffled" if args.shuffle_labels else "normal",
        "n_obs": n_obs,
        "distinct_days": distinct_days,
        "n_symbols": n_symbols,
        "folds": folds,
        "overall": overall,
    }

    if args.json:
        with open(args.json, "w") as f:
            json.dump(result, f, indent=2)
        eprint(f"JSON written to {args.json}")
    else:
        print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
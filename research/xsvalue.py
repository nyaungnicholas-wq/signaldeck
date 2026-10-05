#!/usr/bin/env python3
"""Cross-sectional value model: IC, decile spread, turnover, net spread.

Walk-forward by calendar year. Train on all days STRICTLY BEFORE test year Y
minus an EMBARGO of 10 trading days; test on days in Y. As-of discipline:
features use only bars at or before day t; the label and forward return use
the NEXT available N-day bar for that symbol. Predictions are clustered by
day for confidence intervals.

A positive gross spread that goes negative after costs is a NEGATIVE result
and must be reported as one.
"""

import argparse
import json
import math
import re
import sqlite3
import sys
from collections import defaultdict

import numpy as np


# EXCLUDE FUNDS. `market='stocks'` does NOT exclude ETFs in this database.
# Confirmed present and passing that filter: SPY, QQQ, TQQQ, SQQQ, TLT, LQD, XLY,
# ZSL, and the leveraged inverse products SOXS (-3x), TSLZ (-2x) and MSTZ (-2x).
#
# research/dirfix measured this on the SAME database on 2026-08-15: funds were
# 52.7% of long picks and 51.2% of short picks, "roughly half the apparent edge",
# and beta flipped +0.47 -> -0.65 once removed. The filter was written there and
# never back-applied here, and neither result document carries the caveat.
#
# It matters more than generic contamination for THIS model: leveraged-inverse
# decay is a deterministic function of realized volatility, and vol21 is one of
# the six features -- so the model can learn "high recent vol + leveraged product
# -> decays", which is mechanically forecastable and not tradeable at 20-100%/yr
# borrow against the 10bp/side charged here.
#
# Pattern copied verbatim from research/dirfix/extract.py, including its own
# warning: it deliberately does NOT match "Trust", "Shares" or "Depositary",
# which would catch ADRs, foreign issuers and REITs -- real companies.
# Word boundaries on the ambiguous tokens. dirfix's pattern matches Bear/Bull/
# Ultra as bare substrings, which drops REAL operating companies: BBAI is
# "BigBear.ai Holdings, Inc." and was being excluded as a leveraged fund.
# Dropping real companies biases the universe in the opposite direction to the
# contamination this filter exists to remove, so it is tightened here.
# "UltraShort"/"UltraPro" keep their own un-bounded alternatives, so genuine
# ProShares names still match.
FUND_PAT = (r"ETF|ETN|Fund|ProShares|Direxion|iShares|SPDR|Invesco|Vanguard|"
            r"UltraShort|UltraPro|Ultra|Bear|Bull|[23]X|"
            r"Leveraged|Select Sector|Daily Target|Index Trust")

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
        "SELECT id, symbol, COALESCE(name,'') AS name FROM symbols "
        "WHERE market='stocks' ORDER BY id"
    ).fetchall()
    conn.close()
    keep, dropped = [], 0
    for r in rows:
        if re.search(FUND_PAT, r["name"], re.IGNORECASE):
            dropped += 1
            continue
        keep.append((r["id"], r["symbol"]))
    print("fund filter: %d -> %d symbols (%d fund/leveraged dropped)"
          % (len(rows), len(keep), dropped))
    return keep


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

def compute_observations(rows, hold=1):
    """Given list of (ts, close, volume) for one symbol, yield observation
    dicts with features computed STRICTLY AT OR BEFORE day t.

    For day t (index i in the sorted bar list):
      - label up = 1 if close[i+hold] > close[i]  (next available N-day bar)
      - fwd_ret = close[i+hold]/close[i] - 1
      - r1  = close[i]/close[i-1] - 1
      - r5  = close[i]/close[i-5] - 1
      - r21 = close[i]/close[i-21] - 1
      - r63 = close[i]/close[i-63] - 1
      - vol21 = stdev of daily returns over last 21 days ending at t
      - dvol  = log1p(volume[i]) - mean(log1p(volume)) over last 21 days
      - dvol21 = close[i]*volume[i] mean over last 21 days (liquidity filter)

    All lookbacks use only bars at indices <= i (as-of discipline).
    The label uses bar i+hold which is the next available bar after t.
    """
    n = len(rows)
    if n < 65 + hold:
        return

    ts = np.array([r[0] for r in rows], dtype=np.int64)
    close = np.array([r[1] for r in rows], dtype=np.float64)
    volume = np.array([r[2] for r in rows], dtype=np.float64)

    ret = np.empty(n, dtype=np.float64)
    ret[0] = 0.0
    for i in range(1, n):
        ret[i] = close[i] / close[i - 1] - 1.0

    logvol = np.log1p(volume)
    dollar_vol = close * volume

    for i in range(63, n - hold):
        r1 = ret[i]
        r5 = close[i] / close[i - 5] - 1.0
        r21 = close[i] / close[i - 21] - 1.0
        r63 = close[i] / close[i - 63] - 1.0

        window_ret = ret[i - 20 : i + 1]
        vol21 = float(np.std(window_ret, ddof=1))

        window_lv = logvol[i - 20 : i + 1]
        dvol = float(logvol[i] - np.mean(window_lv))

        # Liquidity: 21-day mean dollar volume, strictly at or before day t
        window_dv = dollar_vol[i - 20 : i + 1]
        dvol21 = float(np.mean(window_dv))

        up = 1 if close[i + hold] > close[i] else 0
        fwd_ret = float(close[i + hold] / close[i] - 1.0)

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
            "dvol21": dvol21,
        }


FEATURE_NAMES = ["r1", "r5", "r21", "r63", "vol21", "dvol"]


def collect_all_observations(db_path, hold=1):
    """Stream per symbol, collect observations into per-day buckets."""
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
        if len(rows) < 65 + hold:
            continue
        seen_symbols.add(sid)
        for obs in compute_observations(rows, hold=hold):
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
    """Rank values to [-0.5, +0.5]: rank/(n-1) - 0.5. Ties get average rank."""
    n = len(values)
    if n <= 1:
        return np.zeros(n, dtype=np.float64)
    arr = np.asarray(values, dtype=np.float64)
    order = np.argsort(arr, kind="mergesort")
    ranks = np.empty(n, dtype=np.float64)
    i = 0
    while i < n:
        j = i
        while j < n and arr[order[j]] == arr[order[i]]:
            j += 1
        avg_rank = (i + j - 1) / 2.0
        for k in range(i, j):
            ranks[order[k]] = avg_rank
        i = j
    return ranks / (n - 1) - 0.5


def build_dataset(day_obs, shuffle_labels=False, seed=0, liquid_top=0):
    """Build feature matrix X, labels y, forward returns, day indices, and
    liquidity values from day_obs.

    Cross-sectional ranking is applied within each day.
    Drops any (symbol, day) missing a feature (NaN/Inf).

    When liquid_top > 0, on each day keep only the N symbols with the highest
    21-day mean dollar volume computed strictly at or before day t.
    """
    sorted_day_keys = sorted(day_obs.keys())

    X_list = []
    y_list = []
    fwd_list = []
    days_list = []

    rng = np.random.RandomState(seed)

    for dk in sorted_day_keys:
        obs_list = day_obs[dk]
        n = len(obs_list)
        if n == 0:
            continue

        raw = np.empty((n, len(FEATURE_NAMES)), dtype=np.float64)
        labels = np.empty(n, dtype=np.int64)
        fwd = np.empty(n, dtype=np.float64)
        dvol21 = np.empty(n, dtype=np.float64)
        for j, obs in enumerate(obs_list):
            raw[j, 0] = obs["r1"]
            raw[j, 1] = obs["r5"]
            raw[j, 2] = obs["r21"]
            raw[j, 3] = obs["r63"]
            raw[j, 4] = obs["vol21"]
            raw[j, 5] = obs["dvol"]
            labels[j] = obs["up"]
            fwd[j] = obs["fwd_ret"]
            dvol21[j] = obs["dvol21"]

        # Liquidity filter: keep only top N by 21-day mean dollar volume
        # (computed strictly at or before day t — as-of discipline).
        if liquid_top > 0 and n > liquid_top:
            keep = np.argsort(dvol21, kind="mergesort")[-liquid_top:]
            keep.sort()
            raw = raw[keep]
            labels = labels[keep]
            fwd = fwd[keep]
            n = len(raw)

        # Shuffle labels within day if requested (leakage canary)
        if shuffle_labels:
            labels = rng.permutation(labels)

        valid = np.all(np.isfinite(raw), axis=1)
        if not np.all(valid):
            raw = raw[valid]
            labels = labels[valid]
            fwd = fwd[valid]

        n_valid = len(raw)
        if n_valid <= 1:
            continue

        ranked = np.empty_like(raw)
        for f in range(len(FEATURE_NAMES)):
            ranked[:, f] = rank_cross_sectional(raw[:, f])

        X_list.append(ranked)
        y_list.append(labels)
        fwd_list.append(fwd)
        days_list.append(np.full(n_valid, dk, dtype=np.int64))

    if not X_list:
        return (np.zeros((0, len(FEATURE_NAMES))), np.zeros(0, dtype=np.int64),
                np.zeros(0, dtype=np.float64), np.zeros(0, dtype=np.int64),
                sorted_day_keys)

    X = np.vstack(X_list)
    y = np.concatenate(y_list)
    fwd = np.concatenate(fwd_list)
    days = np.concatenate(days_list)

    return X, y, fwd, days, sorted_day_keys


# ---------------------------------------------------------------------------
# Logistic regression via gradient descent (hand-written, ~30 lines).
# ---------------------------------------------------------------------------

def logistic_fit(X, y, l2=1e-3, lr=0.1, n_iter=500, tol=1e-6):
    """Fit logistic regression with intercept and small L2 via gradient descent."""
    n, d = X.shape
    w = np.zeros(d, dtype=np.float64)
    b = 0.0

    for it in range(n_iter):
        z = X @ w + b
        z = np.clip(z, -30, 30)
        p = 1.0 / (1.0 + np.exp(-z))
        err = p - y

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


def spearman_rankcorr(a, b):
    """Spearman rank correlation between two arrays."""
    if len(a) < 2:
        return float("nan")
    ra = rank_cross_sectional(a)
    rb = rank_cross_sectional(b)
    ra = ra - np.mean(ra)
    rb = rb - np.mean(rb)
    denom = np.sqrt(np.sum(ra * ra) * np.sum(rb * rb))
    if denom == 0:
        return float("nan")
    return float(np.sum(ra * rb) / denom)


def _t_critical_95(df):
    """Approximate t critical value for two-sided 95% CI."""
    if df < 1:
        return float("nan")
    if df >= 1000:
        return 1.96
    z = 1.959963985
    t = z + (z**3 + z) / (4.0 * df) + (5 * z**5 + 16 * z**3 + 3 * z) / (96.0 * df**2)
    return t


def ci_mean(values):
    """95% CI on the mean of a list of values using t distribution."""
    arr = np.array(values, dtype=np.float64)
    n = len(arr)
    if n == 0:
        return float("nan"), float("nan")
    if n == 1:
        return float("nan"), float("nan")
    m = float(np.mean(arr))
    se = float(np.std(arr, ddof=1) / math.sqrt(n))
    t = _t_critical_95(n - 1)
    return m - t * se, m + t * se


def walk_forward_evaluate(X, y, fwd, days, sorted_day_keys, hold=1, winsor=0.0):
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

    day_to_year = {}
    for dk in sorted_day_keys:
        day_to_year[dk] = ts_to_year(dk)

    years = sorted(set(day_to_year.values()))

    day_to_rows = defaultdict(list)
    for i, dk in enumerate(days):
        day_to_rows[dk].append(i)

    folds = []
    # Per test day: (day_key, ic, spread_bp, top_set, bot_set)
    per_day = []

    for yi in range(3, len(years)):
        Y = years[yi]

        # Training days: all days STRICTLY BEFORE year Y, minus embargo of 10
        # trading days (EMBARGO enforced here).
        train_days_all = [dk for dk in sorted_day_keys if day_to_year[dk] < Y]
        if len(train_days_all) <= 10:
            continue
        train_days = train_days_all[:-10]

        test_days = [dk for dk in sorted_day_keys if day_to_year[dk] == Y]
        if not test_days:
            continue

        train_idx = []
        for dk in train_days:
            train_idx.extend(day_to_rows[dk])
        if not train_idx:
            continue

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
        fwd_test = fwd[test_idx]

        w, b = logistic_fit(X_train, y_train.astype(np.float64))
        p_test = logistic_predict_proba(X_test, w, b)

        test_days_arr = days[test_idx]
        unique_test_days = sorted(set(test_days_arr.tolist()))

        fold_ics = []
        fold_spreads = []
        fold_turnovers = []
        prev_top = None
        prev_bot = None

        for dk in unique_test_days:
            mask = test_days_arr == dk
            p_day = p_test[mask]
            fwd_day = fwd_test[mask]
            n_day = len(p_day)

            if n_day < 10:
                prev_top = None
                prev_bot = None
                continue

            # IC: Spearman rank correlation between prediction and forward
            # return, computed WITHIN the day.
            ic = spearman_rankcorr(p_day, fwd_day)
            fold_ics.append(ic)

            # WINSORIZE within the day, before the decile means and AFTER the
            # IC. This is the estimator question cell B raised: IC is a rank
            # statistic and outlier-robust, a decile MEAN is neither, and a
            # handful of microcap five-day returns can flip that mean's sign
            # while the ranking is unchanged. Clipping at the p/100-p
            # percentiles of THAT DAY's realised returns bounds the tails
            # without reordering anything, so the decile membership is
            # identical and only the averaging changes. IC is computed above on
            # unclipped returns deliberately — winsorising it would be
            # pointless, since rank correlation already ignores magnitude.
            fwd_day_eff = fwd_day
            if winsor > 0.0 and n_day >= 20:
                lo = float(np.percentile(fwd_day, winsor))
                hi = float(np.percentile(fwd_day, 100.0 - winsor))
                fwd_day_eff = np.clip(fwd_day, lo, hi)

            # DECILE SPREAD: top decile minus bottom decile forward return,
            # equal-weighted, per day.
            order = np.argsort(p_day, kind="mergesort")
            decile_size = max(1, n_day // 10)
            bot_idx = order[:decile_size]
            top_idx = order[-decile_size:]
            spread = float(np.mean(fwd_day_eff[top_idx]) - np.mean(fwd_day_eff[bot_idx]))
            spread_bp = spread * 1e4
            fold_spreads.append(spread_bp)

            # TURNOVER: fraction of top-decile membership that changes from
            # the previous test day (and same for bottom). Report the mean.
            top_set = set(top_idx.tolist())
            bot_set = set(bot_idx.tolist())
            if prev_top is not None and prev_bot is not None:
                top_chg = 1.0 - len(top_set & prev_top) / len(top_set) if len(top_set) > 0 else 0.0
                bot_chg = 1.0 - len(bot_set & prev_bot) / len(bot_set) if len(bot_set) > 0 else 0.0
                turnover = (top_chg + bot_chg) / 2.0
                # NOT divided by hold. `turnover` is decile churn against the
                # PREVIOUS DAY, and dividing by hold made it a per-DAY rate --
                # which was then subtracted from `spread_bp`, a per-HOLDING-PERIOD
                # return (fwd_ret = close[i+hold]/close[i] - 1). Charging a daily
                # cost against a period return understates the drag by exactly
                # `hold`x, always in the flattering direction.
                #
                # Reproduced from the published table before changing it: hold=1
                # cells were correct (A: +18.75 gross, +5.39 net@10bp), hold=5
                # cells understated ~5x (B: -34.97 published vs -42.26
                # units-consistent; D: -2.80 vs -8.25). No published CONCLUSION
                # moves -- all four cells fail either way and the document says
                # so -- but the bias points straight at the next hypothesis that
                # document proposes, a SLOWER construction, whose costs are
                # exactly the ones this shrank. At hold=21 a book paying ~10bp
                # would have been charged 0.48bp, and a losing slow book could
                # have printed a positive net spread.
                fold_turnovers.append(turnover)
                per_day.append((dk, ic, spread_bp, turnover))
            else:
                per_day.append((dk, ic, spread_bp, float("nan")))

            prev_top = top_set
            prev_bot = bot_set

        n_test = len(test_idx)
        n_test_days = len(unique_test_days)

        fold_ic = float(np.mean(fold_ics)) if fold_ics else float("nan")
        fold_spread = float(np.mean(fold_spreads)) if fold_spreads else float("nan")
        fold_turnover = float(np.mean(fold_turnovers)) if fold_turnovers else float("nan")

        folds.append({
            "year": Y,
            "n": n_test,
            "n_days": n_test_days,
            "ic": fold_ic,
            "spread_bp": fold_spread,
            "turnover": fold_turnover,
        })

        eprint(f"  Fold {Y}: n={n_test}, days={n_test_days}, "
               f"ic={fold_ic:+.4f}, spread={fold_spread:+.2f}bp, "
               f"turnover={fold_turnover:.4f}")

    if not per_day:
        return folds, None

    # Overall stats — cluster by day. When hold>1, forward-return observations
    # overlap across consecutive days; the daily CI therefore has overlapping
    # observations. We additionally report a non-overlapping CI computed on
    # every Nth day only.
    ics = [d[1] for d in per_day if not np.isnan(d[1])]
    spreads = [d[2] for d in per_day if not np.isnan(d[2])]
    turnovers = [d[3] for d in per_day if not np.isnan(d[3])]

    overall_ic = float(np.mean(ics)) if ics else float("nan")
    ic_lo, ic_hi = ci_mean(ics)

    mean_spread = float(np.mean(spreads)) if spreads else float("nan")
    ci_lo, ci_hi = ci_mean(spreads)

    # Non-overlapping CI: every Nth day only (hold>1)
    if hold > 1:
        nonoverlap = [d for i, d in enumerate(per_day) if i % hold == 0]
        no_spreads = [d[2] for d in nonoverlap if not np.isnan(d[2])]
        no_lo, no_hi = ci_mean(no_spreads)
    else:
        no_lo, no_hi = ci_lo, ci_hi

    mean_turnover = float(np.mean(turnovers)) if turnovers else float("nan")

    # NET SPREAD = decile spread minus cost, where
    # cost = 2 * turnover * cost_bps_per_side (two sides, long and short).
    # A positive gross spread that goes negative after costs is a NEGATIVE
    # result and must be reported as one.
    net_5 = mean_spread - 2.0 * mean_turnover * 5.0
    net_10 = mean_spread - 2.0 * mean_turnover * 10.0
    net_20 = mean_spread - 2.0 * mean_turnover * 20.0

    overall = {
        "ic": overall_ic,
        "ic_lo": ic_lo,
        "ic_hi": ic_hi,
        "spread_bp": mean_spread,
        "ci_lo_bp": ci_lo,
        "ci_hi_bp": ci_hi,
        "ci_lo_bp_nonoverlap": no_lo,
        "ci_hi_bp_nonoverlap": no_hi,
        "turnover": mean_turnover,
        "net_bp_5": net_5,
        "net_bp_10": net_10,
        "net_bp_20": net_20,
        "n_days": len(per_day),
    }

    return folds, overall


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Cross-sectional value model: IC, decile spread, turnover, net spread"
    )
    parser.add_argument("--winsor", type=float, default=0.0,
                        help="clip forward returns at this percentile and its "
                             "complement WITHIN each day before the decile "
                             "means (e.g. 1 = 1st/99th). 0 = off. Does not "
                             "affect IC or decile membership.")
    parser.add_argument("--hold", type=int, default=1,
                        help="Holding period in trading days (default 1)")
    parser.add_argument("--liquid-top", type=int, default=0,
                        help="Keep only N symbols with highest 21-day mean dollar volume per day (0=off)")
    parser.add_argument("--shuffle-labels", action="store_true",
                        help="Randomly permute up labels within each day (leakage canary)")
    parser.add_argument("--seed", type=int, default=0,
                        help="Random seed for label shuffling")
    parser.add_argument("--json", type=str, default=None,
                        help="Write result object to PATH")
    parser.add_argument("--db", type=str, default="data/signaldeck.db",
                        help="Path to SQLite database (default: data/signaldeck.db)")
    args = parser.parse_args()

    eprint("=== Cross-Sectional Value Model ===")
    eprint(f"Mode: {'shuffled' if args.shuffle_labels else 'normal'}")
    eprint(f"Hold: {args.hold}")
    eprint(f"Liquid top: {args.liquid_top}")
    eprint(f"DB: {args.db}")
    eprint()

    eprint("Step 1: Loading bars and computing features/labels per symbol...")
    day_obs, seen_symbols = collect_all_observations(args.db, hold=args.hold)

    if not day_obs:
        eprint("ERROR: No observations collected. Check database.")
        sys.exit(1)

    eprint("\nStep 2: Building dataset with cross-sectional ranking...")
    X, y, fwd, days, sorted_day_keys = build_dataset(
        day_obs, shuffle_labels=args.shuffle_labels, seed=args.seed,
        liquid_top=args.liquid_top
    )

    n_obs = X.shape[0]
    distinct_days = len(sorted(set(days.tolist())))
    n_symbols = len(seen_symbols)

    eprint(f"  n_obs = {n_obs}")
    eprint(f"  distinct_days = {distinct_days}")
    eprint(f"  n_symbols = {n_symbols}")

    eprint("\nStep 3: Walk-forward evaluation by calendar year...")
    folds, overall = walk_forward_evaluate(
        X, y, fwd, days, sorted_day_keys, hold=args.hold, winsor=args.winsor
    )

    if not folds:
        eprint("ERROR: No folds could be evaluated. Need at least 4 years of data.")
        sys.exit(1)

    eprint("\n" + "=" * 80)
    mode_str = "SHUFFLED (canary)" if args.shuffle_labels else "NORMAL"
    print(f"\nCross-Sectional Value Model — {mode_str}")
    print(f"hold={args.hold}  liquid_top={args.liquid_top}")
    print(f"n_obs={n_obs}  distinct_days={distinct_days}  n_symbols={n_symbols}")
    print()
    print(f"{'Year':>6}  {'N':>7}  {'Days':>5}  {'IC':>8}  {'Spread':>8}  {'Turnover':>8}")
    print("-" * 56)
    for f in folds:
        print(f"{f['year']:>6}  {f['n']:>7}  {f['n_days']:>5}  "
              f"{f['ic']:>+8.4f}  {f['spread_bp']:>+8.2f}  {f['turnover']:>8.4f}")
    print("-" * 56)
    if overall:
        print(f"{'OVERALL':>6}  {'':>7}  {overall['n_days']:>5}  "
              f"{overall['ic']:>+8.4f}  {overall['spread_bp']:>+8.2f}  "
              f"{overall['turnover']:>8.4f}")
        print(f"\n95% CI on daily IC: [{overall['ic_lo']:+.4f}, {overall['ic_hi']:+.4f}] "
              f"({overall['n_days']} days)")
        print(f"95% CI on daily spread: [{overall['ci_lo_bp']:+.2f}, {overall['ci_hi_bp']:+.2f}] bp")
        if args.hold > 1:
            print(f"95% CI non-overlapping:   [{overall['ci_lo_bp_nonoverlap']:+.2f}, "
                  f"{overall['ci_hi_bp_nonoverlap']:+.2f}] bp")
        print(f"\nNet spread (gross {overall['spread_bp']:+.2f} bp, turnover {overall['turnover']:.4f}):")
        print(f"  @5bp/side:  {overall['net_bp_5']:+.2f} bp")
        print(f"  @10bp/side: {overall['net_bp_10']:+.2f} bp")
        print(f"  @20bp/side: {overall['net_bp_20']:+.2f} bp")
        if overall['spread_bp'] > 0 and overall['net_bp_10'] < 0:
            print("\n  NOTE: Positive gross spread goes negative after costs — NEGATIVE result.")
    print()

    result = {
        "mode": "shuffled" if args.shuffle_labels else "normal",
        "hold": args.hold,
        "liquid_top": args.liquid_top,
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
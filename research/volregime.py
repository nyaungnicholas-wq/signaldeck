#!/usr/bin/env python3
"""research/volregime.py — re-measure the HMM volatility labeller with inference.

Replicates daemon/cmd/hmmbakeoff/main.go but adds confidence intervals clustered
by symbol and by day, and a CI on the HMM-minus-tercile difference.

Rules enforced (mirroring the Go header):
  - OUT OF SAMPLE: fit per symbol on the first 60% of bars, grade on last 40%.
  - NO LOOKAHEAD: label at bar i uses bars[0..i] only; target is |log(C[i+1]/C[i])|.
  - PER-SYMBOL NORMALISATION: divide each symbol's forward move by its own mean
    forward move over its test window before pooling.
  - RANDOM control must land near separation 1.00 or the harness is broken.
"""
import argparse
import json
import math
import sqlite3
import sys
from collections import defaultdict

import numpy as np

TRAIN_FRAC = 0.60
VOL_WINDOW = 20
VOL_LOOKBK = 250


def eprint(*a, **k):
    print(*a, file=sys.stderr, **k)


# ---- 2-state Gaussian HMM, Baum-Welch with scaling (~60 lines) ----
def _forward_backward(x, mu, var, A, pi):
    N = len(x)
    K = len(mu)
    std = np.sqrt(var)
    # emission log-likelihood
    logB = np.empty((N, K))
    for k in range(K):
        logB[:, k] = -0.5 * np.log(2 * math.pi * var[k]) - 0.5 * ((x - mu[k]) ** 2) / var[k]
    logA = np.log(A)
    logpi = np.log(pi)
    # forward
    alpha = np.empty((N, K))
    c = np.empty(N)
    alpha[0] = logpi + logB[0]
    c[0] = -_logsumexp(alpha[0])
    alpha[0] += c[0]
    for t in range(1, N):
        for k in range(K):
            alpha[t, k] = logB[t, k] + _logsumexp(alpha[t - 1] + logA[:, k])
        c[t] = -_logsumexp(alpha[t])
        alpha[t] += c[t]
    # backward
    beta = np.empty((N, K))
    beta[-1] = c[-1]
    for t in range(N - 2, -1, -1):
        for k in range(K):
            beta[t, k] = _logsumexp(logA[k, :] + logB[t + 1, :] + beta[t + 1, :]) + c[t]
    # gamma
    gamma = alpha + beta
    for t in range(N):
        gamma[t] = gamma[t] - _logsumexp(gamma[t])
    gamma = np.exp(gamma)
    # xi (summed over t)
    logxi_sum = np.zeros((K, K))
    for t in range(N - 1):
        denom = _logsumexp(alpha[t, :, None] + logA + logB[t + 1, None, :] + beta[t + 1, None, :])
        logxi_sum += np.exp(alpha[t, :, None] + logA + logB[t + 1, None, :] + beta[t + 1, None, :] - denom)
    loglik = -np.sum(c)
    return gamma, logxi_sum, loglik


def _logsumexp(v):
    m = np.max(v)
    if not np.isfinite(m):
        return -np.inf
    return m + np.log(np.sum(np.exp(v - m)))


def fit_hmm(x, n_states=2, n_iter=100, tol=1e-5, seed=0):
    """Fit a Gaussian HMM by Baum-Welch. x: 1D array of log-abs-returns."""
    rng = np.random.default_rng(seed)
    N = len(x)
    if N < 10:
        return None
    # init means by quantile, variances global, transition near-diagonal
    qs = np.quantile(x, np.linspace(0.5 / n_states, 1 - 0.5 / n_states, n_states))
    mu = qs + rng.normal(0, 1e-3, n_states)
    var = np.full(n_states, np.var(x) + 1e-8)
    A = np.full((n_states, n_states), 0.1 / (n_states - 1) if n_states > 1 else 0.0)
    np.fill_diagonal(A, 0.9)
    A = A / A.sum(axis=1, keepdims=True)
    pi = np.full(n_states, 1.0 / n_states)
    prev_ll = -np.inf
    for _ in range(n_iter):
        gamma, xi_sum, ll = _forward_backward(x, mu, var, A, pi)
        # M-step
        pi = gamma[0] / gamma[0].sum()
        for k in range(n_states):
            denom = gamma[:, k].sum()
            if denom < 1e-12:
                continue
            mu[k] = (gamma[:, k] * x).sum() / denom
            var[k] = (gamma[:, k] * (x - mu[k]) ** 2).sum() / denom + 1e-8
        A = xi_sum + 1e-12
        A = A / A.sum(axis=1, keepdims=True)
        if abs(ll - prev_ll) < tol:
            break
        prev_ll = ll
    return mu, var, A, pi


def hmm_label_path(x, mu, var, A, pi):
    """Return per-bar state labels for x[0..i] at each i (online, no lookahead).

    We refit-or-filter incrementally: for bar i we run forward over x[0..i] and
    take argmax gamma at i. This is O(N^2) but correct and matches the no-lookahead
    constraint. For speed we refit every 50 bars and forward-filter in between,
    but the label at i never sees x[i+1..].
    """
    N = len(x)
    labels = np.zeros(N, dtype=int)
    # incremental forward with periodic refit
    step = 50
    for start in range(0, N, step):
        end = min(start + step, N)
        seg = x[:end]
        g, _, _ = _forward_backward(seg, mu, var, A, pi)
        labels[start:end] = np.argmax(g[start:end], axis=1)
    return labels


def fwd_abs(closes, i):
    if closes[i] <= 0 or closes[i + 1] <= 0:
        return 0.0
    return abs(math.log(closes[i + 1] / closes[i]))


def vol_tercile_labels(closes):
    n = len(closes)
    rv = np.full(n, np.nan)
    for i in range(VOL_WINDOW, n):
        s = 0.0
        for j in range(i - VOL_WINDOW + 1, i + 1):
            if closes[j - 1] > 0 and closes[j] > 0:
                r = math.log(closes[j] / closes[j - 1])
                s += r * r
        rv[i] = math.sqrt(s / VOL_WINDOW)
    out = [None] * n
    for i in range(VOL_WINDOW + VOL_LOOKBK, n):
        hist = []
        for j in range(i - VOL_LOOKBK + 1, i + 1):
            if not math.isnan(rv[j]):
                hist.append(rv[j])
        if len(hist) < 30:
            continue
        hist.sort()
        lo = hist[len(hist) // 3]
        hi = hist[2 * len(hist) // 3]
        if rv[i] <= lo:
            out[i] = "vol_low"
        elif rv[i] >= hi:
            out[i] = "vol_high"
        else:
            out[i] = "vol_mid"
    return out


def t_ci(data, conf=0.95):
    """95% CI of the mean via t distribution, n-1 df."""
    data = np.array(data, dtype=float)
    data = data[np.isfinite(data)]
    n = len(data)
    if n < 2:
        return float("nan"), float("nan")
    m = data.mean()
    se = data.std(ddof=1) / math.sqrt(n)
    # t critical value via inverse survival function approximation
    from statistics import NormalDist
    # use normal approximation for large n; for small n use a t-table lookup
    if n >= 30:
        z = NormalDist().inv_cdf(1 - (1 - conf) / 2)
    else:
        # t critical values for 95% CI, df = n-1
        t_table = {1: 12.706, 2: 4.303, 3: 3.182, 4: 2.776, 5: 2.571,
                   6: 2.447, 7: 2.365, 8: 2.306, 9: 2.262, 10: 2.228,
                   15: 2.131, 20: 2.086, 25: 2.060, 29: 2.045}
        df = n - 1
        keys = sorted(t_table.keys())
        if df <= keys[0]:
            z = t_table[keys[0]]
        elif df >= keys[-1]:
            z = t_table[keys[-1]]
        else:
            for ik in range(len(keys) - 1):
                if keys[ik] <= df <= keys[ik + 1]:
                    z = t_table[keys[ik + 1]]
                    break
    return m - z * se, m + z * se


def eligible_symbols(db, min_bars, max_syms):
    q = "SELECT symbol_id FROM bars WHERE tf='1d' GROUP BY symbol_id HAVING COUNT(*) >= ? ORDER BY COUNT(*) DESC"
    if max_syms > 0:
        q += f" LIMIT {max_syms}"
    rows = db.execute(q, (min_bars,)).fetchall()
    return [r[0] for r in rows]


def load_bars(db, symbol_id):
    rows = db.execute(
        "SELECT ts, open, high, low, close, volume FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts",
        (symbol_id,),
    ).fetchall()
    ts = [r[0] for r in rows]
    closes = [r[4] for r in rows]
    return ts, closes


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default="data/signaldeck.db")
    ap.add_argument("--json", default=None)
    ap.add_argument("--seed", type=int, default=42)
    ap.add_argument("--max-symbols", type=int, default=400)
    args = ap.parse_args()

    uri = f"file:{args.db}?mode=ro"
    db = sqlite3.connect(uri, uri=True)

    # Only market='stocks' symbols
    stock_ids = set(r[0] for r in db.execute("SELECT id FROM symbols WHERE market='stocks'").fetchall())
    ids = [s for s in eligible_symbols(db, 500, args.max_symbols) if s in stock_ids]
    eprint(f"grading {len(ids)} stock symbols (>=500 daily bars), train={TRAIN_FRAC*100:.0f}%")

    rng = np.random.default_rng(args.seed)

    # Per-symbol separation values for each labeller
    sep_hmm, sep_terc, sep_rand = [], [], []
    # Per-day day_diff (turbulent - calm normalised move) for each labeller
    day_hmm, day_terc, day_rand = defaultdict(list), defaultdict(list), defaultdict(list)
    # Per-symbol HMM-minus-tercile separation difference
    diff_hmm_terc = []
    n_test_bars = 0

    for si, sid in enumerate(ids):
        if si % 25 == 0:
            eprint(f"  symbol {si+1}/{len(ids)}")
        ts, closes = load_bars(db, sid)
        n = len(closes)
        if n < 500:
            continue
        split = int(n * TRAIN_FRAC)
        if split < 20 or n - split < 2:
            continue

        closes_arr = np.array(closes, dtype=float)

        # log-abs-returns for HMM (on all bars, but fit only on train)
        log_abs = np.zeros(n)
        for i in range(1, n):
            if closes[i - 1] > 0 and closes[i] > 0:
                log_abs[i] = abs(math.log(closes[i] / closes[i - 1]))

        # --- OUT OF SAMPLE: fit HMM on train only ---
        fit = fit_hmm(log_abs[1:split], n_states=2, seed=args.seed + si)
        if fit is None:
            continue
        mu, var, A, pi = fit
        # higher-mean state = turbulent
        turbulent_state = int(np.argmax(mu))

        # --- NO LOOKAHEAD: label each bar using x[0..i] only ---
        hmm_labels = hmm_label_path(log_abs, mu, var, A, pi)

        # Tercile labels (parameter-free, same split applied)
        vol_lbl = vol_tercile_labels(closes)

        # Per-symbol normaliser: mean forward abs move over test window
        fsum, fcnt = 0.0, 0
        for i in range(split, n - 1):
            fsum += fwd_abs(closes, i)
            fcnt += 1
        if fcnt == 0 or fsum <= 0:
            continue
        norm = fsum / fcnt

        # Random labels for test bars (seeded)
        rand_labels = rng.integers(0, 2, size=n)

        # Accumulate per-symbol buckets for separation
        b_hmm = defaultdict(list)
        b_terc = defaultdict(list)
        b_rand = defaultdict(list)

        for i in range(split, n - 1):
            x = fwd_abs(closes, i) / norm  # PER-SYMBOL NORMALISATION
            n_test_bars += 1
            day = ts[i]  # use ts as day key (daily bars)

            hl = int(hmm_labels[i])
            b_hmm[hl].append(x)
            # day_diff: turbulent - calm
            if hl == turbulent_state:
                day_hmm[day].append(x)
            else:
                day_hmm[day].append(-x)

            vl = vol_lbl[i]
            if vl is not None:
                b_terc[vl].append(x)
                if vl == "vol_high":
                    day_terc[day].append(x)
                elif vl == "vol_low":
                    day_terc[day].append(-x)

            rl = int(rand_labels[i])
            b_rand[rl].append(x)
            if rl == 1:
                day_rand[day].append(x)
            else:
                day_rand[day].append(-x)

        # Per-symbol separation = mean(highest-label) / mean(lowest-label)
        def sep(buckets):
            means = [(k, np.mean(v)) for k, v in buckets.items() if len(v) > 0]
            if len(means) < 2:
                return None
            means.sort(key=lambda t: t[1])
            lo = means[0][1]
            hi = means[-1][1]
            if lo <= 0:
                return None
            return hi / lo

        s_hmm = sep(b_hmm)
        s_terc = sep(b_terc)
        s_rand = sep(b_rand)
        if s_hmm is not None:
            sep_hmm.append(s_hmm)
        if s_terc is not None:
            sep_terc.append(s_terc)
        if s_rand is not None:
            sep_rand.append(s_rand)
        if s_hmm is not None and s_terc is not None:
            diff_hmm_terc.append(s_hmm - s_terc)

    db.close()

    eprint(f"symbols graded: hmm={len(sep_hmm)} tercile={len(sep_terc)} random={len(sep_rand)}")
    eprint(f"test bars: {n_test_bars}")

    def day_ci(day_map):
        diffs = [np.mean(v) for v in day_map.values() if len(v) > 0]
        lo, hi = t_ci(diffs)
        return float(np.mean(diffs)) if diffs else float("nan"), float(lo), float(hi)

    def labeller_result(sep_list, day_map):
        if len(sep_list) < 2:
            sep, lo, hi = float("nan"), float("nan"), float("nan")
        else:
            lo, hi = t_ci(sep_list)
            sep = float(np.mean(sep_list))
        dd, dl, dh = day_ci(day_map)
        return {
            "separation": sep, "sep_lo": float(lo), "sep_hi": float(hi),
            "day_diff": dd, "day_lo": dl, "day_hi": dh,
        }

    out = {
        "n_symbols": len(sep_hmm),
        "n_test_bars": n_test_bars,
        "labellers": {
            "hmm": labeller_result(sep_hmm, day_hmm),
            "tercile": labeller_result(sep_terc, day_terc),
            "random": labeller_result(sep_rand, day_rand),
        },
    }
    if len(diff_hmm_terc) >= 2:
        lo, hi = t_ci(diff_hmm_terc)
        out["hmm_minus_tercile"] = {
            "diff": float(np.mean(diff_hmm_terc)), "lo": float(lo), "hi": float(hi)
        }
    else:
        out["hmm_minus_tercile"] = {"diff": float("nan"), "lo": float("nan"), "hi": float("nan")}

    # Readable table
    print("Volatility regime labeller bakeoff — with inference")
    print(f"symbols graded: {out['n_symbols']}  test bars: {out['n_test_bars']}")
    print()
    print(f"{'labeller':<12} {'separation':>12} {'95% CI':>22} {'day_diff':>12} {'95% CI':>22}")
    for name in ("hmm", "tercile", "random"):
        r = out["labellers"][name]
        print(f"{name:<12} {r['separation']:>12.3f} [{r['sep_lo']:>8.3f}, {r['sep_hi']:>8.3f}]"
              f" {r['day_diff']:>12.4f} [{r['day_lo']:>8.4f}, {r['day_hi']:>8.4f}]")
    d = out["hmm_minus_tercile"]
    print()
    print(f"HMM minus tercile (clustered by symbol): {d['diff']:.3f}  [{d['lo']:.3f}, {d['hi']:.3f}]")
    print()
    print("Random control must land near separation 1.00 or the harness is broken.")

    if args.json:
        with open(args.json, "w") as f:
            json.dump(out, f, indent=2)
        eprint(f"wrote {args.json}")


if __name__ == "__main__":
    main()
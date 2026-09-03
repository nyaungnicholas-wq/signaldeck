#!/usr/bin/env python3
"""Walk-forward backtest of the HAR realized-variance forecast and the VaR built on it.

READ-ONLY. Opens the database with mode=ro and writes nothing but its own JSON
result. It never touches data/accuracy_registry.json and never calls
tools/accuracy_registry.py, whose sha256 is pinned in the pre-registration
chain.

WHY THIS EXISTS SEPARATELY FROM THE GO PACKAGE
----------------------------------------------
daemon/internal/harrv implements the same model. This is a SECOND, independent
implementation written from the formulas rather than from that code, and
`--crosscheck` compares the two on a real symbol. Two implementations agreeing
to 1e-9 is the cheapest available defence against the class of bug that killed
the earlier predictors here: a transcription error that produces plausible
numbers nobody can see is wrong.

WHAT IT REFUSES TO DO
---------------------
It does not choose anything after seeing a result. The horizons, the nulls, the
losses, the universe screen and the inference are all fixed here, in code, and
the pre-registration names them before the definitive run. Every figure it
prints comes with its sample size and its null.

Usage:
    python tools/rv_forecast_backtest.py --db data/signaldeck.db --limit 200
    python tools/rv_forecast_backtest.py --crosscheck AAPL
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import bisect
import os
import random
import sqlite3
import statistics
import sys

# ── the estimator ────────────────────────────────────────────────────────────
# Identical in intent to daemon/internal/harrv/estimator.go. Written from the
# formulas, not transcribed from it.

LN2 = math.log(2.0)
OVERNIGHT_GUARD = 0.65   # volregime.maxSaneReturn / revalidate_structural MAX_SANE_RETURN
MIN_VARIANCE = 1e-8

LAG_W, LAG_M = 5, 22
MIN_TRAIN_W, MIN_TRAIN_M = 4, 18
MIN_TRAIN = 500
MIN_HISTORY = 530
REFIT_EVERY = 5          # sessions between refits; a compute choice, never a fitted one

# BOTH horizons are registered. h=1 is non-overlapping by construction; h=5 is a
# less noisy estimand whose consecutive forecasts share 4 forward days.
HORIZONS = (1, 5)

RISKMETRICS_LAMBDA = 0.94
FLAT_WINDOW = 22
VAR_LEVELS = (0.05, 0.01)
MIN_TAIL_OBS = 10        # risklens.MinVaRTailObservations
BOOTSTRAP_BLOCK = 21
BOOTSTRAP_ITERS = 2000
BOOTSTRAP_SEED = 20260903

# A symbol whose range estimator is structurally unusable is excluded UP FRONT,
# by a rule fixed before the run, not dropped later for scoring badly.
MAX_FLAT_SHARE = 0.01
MIN_BARS = MIN_HISTORY


def rv_series(bars):
    """bars: ascending (ts, o, h, l, c). Returns (ts[], rv[] with None holes, exclusions)."""
    ts, rv = [], []
    x = {"flat_range": 0, "overnight": 0, "non_pos": 0, "gk_negative": 0,
         "floored": 0, "no_prev": 0}
    prev_close = None
    for (t, o, h, l, c) in bars:
        ts.append(t)
        if prev_close is None:
            rv.append(None); x["no_prev"] += 1; prev_close = c; continue
        if min(o, h, l, c, prev_close) <= 0 or h < l:
            rv.append(None); x["non_pos"] += 1; prev_close = c; continue
        gap = math.log(o / prev_close)
        if abs(gap) > OVERNIGHT_GUARD:
            rv.append(None); x["overnight"] += 1; prev_close = c; continue
        if h == l:
            rv.append(None); x["flat_range"] += 1; prev_close = c; continue
        hl2 = math.log(h / l) ** 2
        co2 = math.log(c / o) ** 2
        gk = 0.5 * hl2 - (2 * LN2 - 1) * co2
        if gk < 0:
            x["gk_negative"] += 1
            v = gap * gap + hl2 / (4 * LN2)
        else:
            v = gap * gap + gk
        if v < MIN_VARIANCE:
            v = MIN_VARIANCE; x["floored"] += 1
        rv.append(v)
        prev_close = c
    return ts, rv, x


def rolling_mean(rv, i, w, min_count):
    if i < 0 or i >= len(rv) or w <= 0 or i - w + 1 < 0:
        return None
    vals = [rv[j] for j in range(i - w + 1, i + 1) if rv[j] is not None]
    if len(vals) < min_count:
        return None
    return sum(vals) / len(vals)


def features(rv, i):
    if i < 0 or i >= len(rv) or rv[i] is None or rv[i] <= 0:
        return None
    w = rolling_mean(rv, i, LAG_W, MIN_TRAIN_W)
    m = rolling_mean(rv, i, LAG_M, MIN_TRAIN_M)
    if not w or not m or w <= 0 or m <= 0:
        return None
    return (1.0, math.log(rv[i]), math.log(w), math.log(m))


def target_at(rv, t, h):
    """The OUTCOME: mean RV over t+1..t+h. Refuses a partial window."""
    if h < 1 or t < 0 or t + h >= len(rv):
        return None
    s = 0.0
    for i in range(t + 1, t + h + 1):
        v = rv[i]
        if v is None or v <= 0:
            return None
        s += v
    return s / h


def solve4(a, b):
    """Gauss-Jordan with partial pivoting. Returns None on a singular system."""
    a = [row[:] for row in a]
    b = b[:]
    for col in range(4):
        p = max(range(col, 4), key=lambda r: abs(a[r][col]))
        if abs(a[p][col]) < 1e-12:
            return None
        a[col], a[p] = a[p], a[col]
        b[col], b[p] = b[p], b[col]
        for r in range(4):
            if r == col:
                continue
            f = a[r][col] / a[col][col]
            for c in range(col, 4):
                a[r][c] -= f * a[col][c]
            b[r] -= f * b[col]
    return [b[i] / a[i][i] for i in range(4)]


def fit_at(rv, t, h):
    """Expanding-window OLS using only information available at bar t."""
    if h < 1 or t < MIN_HISTORY or t >= len(rv):
        return None
    xtx = [[0.0] * 4 for _ in range(4)]
    xty = [0.0] * 4
    rows = []
    s = LAG_M
    while s + h <= t:
        f = features(rv, s)
        if f is not None:
            tgt = target_at(rv, s, h)
            if tgt is not None:
                y = math.log(tgt)
                rows.append((f, y))
                for i in range(4):
                    for j in range(4):
                        xtx[i][j] += f[i] * f[j]
                    xty[i] += f[i] * y
        s += 1
    if len(rows) < MIN_TRAIN:
        return None
    beta = solve4(xtx, xty)
    if beta is None:
        return None
    ss = 0.0
    for f, y in rows:
        yhat = sum(beta[k] * f[k] for k in range(4))
        ss += (y - yhat) ** 2
    dof = len(rows) - 4
    if dof <= 0:
        return None
    return {"beta": beta, "resid_var": ss / dof, "n": len(rows), "h": h}


def predict_at(rv, t, fit):
    """Level forecast. The +s^2/2 retransform is mandatory: exp(yhat) is the
    MEDIAN of a lognormal, and publishing it biases every level 10-20% low."""
    f = features(rv, t)
    if f is None:
        return None
    yhat = sum(fit["beta"][k] * f[k] for k in range(4))
    v = math.exp(yhat + fit["resid_var"] / 2)
    if not math.isfinite(v) or v <= 0:
        return None
    return v


# ── the nulls ────────────────────────────────────────────────────────────────

def ewma_series(rv, lam=RISKMETRICS_LAMBDA):
    """Causal EWMA; a hole updates nothing and does not reset the state."""
    out, ew, seeded = [], None, False
    for v in rv:
        if v is not None and v > 0:
            if not seeded:
                ew, seeded = v, True
            else:
                ew = lam * ew + (1 - lam) * v
        out.append(ew if seeded else None)
    return out


# ── losses and inference ─────────────────────────────────────────────────────

def qlike(actual, forecast):
    if actual is None or forecast is None or actual <= 0 or forecast <= 0:
        return None
    r = actual / forecast
    return r - math.log(r) - 1


def mse(actual, forecast):
    if actual is None or forecast is None:
        return None
    return (actual - forecast) ** 2


def newey_west_lag(n):
    return int(4 * (n / 100.0) ** (2.0 / 9.0)) if n > 0 else 0


def diebold_mariano(d, lag):
    """d must already be ONE VALUE PER DAY, cross-sectionally averaged.

    Pooling (symbol, day) pairs is the single most dangerous shortcut here: a
    panel of ~750 symbols over ~2,000 sessions is ~1.5M forecasts, and treating
    them as independent returns a t-statistic in the hundreds for any model,
    because every symbol shares one market shock per day.
    """
    n = len(d)
    if n < 8:
        return None
    lag = min(max(lag, 0), n - 1)
    mean = statistics.fmean(d)

    def gamma(k):
        return sum((d[i] - mean) * (d[i - k] - mean) for i in range(k, n)) / n

    lrv = gamma(0)
    for k in range(1, lag + 1):
        lrv += 2 * (1 - k / (lag + 1)) * gamma(k)
    if lrv <= 0:
        return {"mean": mean, "t": None, "n": n, "lag": lag}
    t = mean / math.sqrt(lrv / n)
    t *= math.sqrt((n - 1) / n)          # Harvey-Leybourne-Newbold; only widens
    return {"mean": mean, "t": t, "n": n, "lag": lag}


def block_bootstrap_ci(d, block=BOOTSTRAP_BLOCK, iters=BOOTSTRAP_ITERS,
                       seed=BOOTSTRAP_SEED, alpha=0.05):
    """Moving-block bootstrap on the mean. Blocks preserve the autocorrelation
    that an i.i.d. resample would destroy, which is the whole point at h > 1.

    Uses random.Random(seed) -- Mersenne Twister, reproducible for a fixed seed
    -- rather than a hand-rolled generator. An earlier version used a small LCG
    on the argument that it should not depend on a library RNG staying stable.
    That traded a real risk for a cosmetic one: a weak generator understates
    the variance of a resampled statistic, and a too-narrow interval is a
    fabricated precision. Reproducibility comes from the seed either way.

    THE TWO INFERENCES DISAGREE ON THIS DATA, AND THAT IS NOT A BUG.
    Measured on a 25-symbol smoke run at h=1: DM t = -1.17 (not significant)
    while this percentile interval was [-0.84, -0.03], excluding zero. The
    cause is skew. QLIKE blows up when a forecast is far too low, so the loss
    differential has a long left tail; the percentile bootstrap follows that
    shape, while DM assumes a symmetric normal interval and therefore straddles
    zero.

    THE DIEBOLD-MARIANO t IS THE REGISTERED DECISION RULE. The interval is
    reported ALONGSIDE it and never instead of it. Reading whichever of two
    disagreeing statistics happens to clear the bar is the selection effect
    that retired every previous predictor in this repository, and it is
    exactly as wrong when the selection is between inference methods as when
    it is between configurations."""
    n = len(d)
    if n < block * 3:
        return None
    rng = random.Random(seed)
    nb = math.ceil(n / block)
    means = []
    for _ in range(iters):
        s, cnt = 0.0, 0
        for _ in range(nb):
            start = rng.randrange(n - block + 1)
            for j in range(block):
                s += d[start + j]; cnt += 1
        means.append(s / cnt)
    means.sort()
    lo = means[int((alpha / 2) * len(means))]
    hi = means[min(len(means) - 1, int((1 - alpha / 2) * len(means)))]
    return {"lo": lo, "hi": hi, "iters": iters, "block": block}


# ── VaR ──────────────────────────────────────────────────────────────────────

def var_es(sigma2, z, level):
    if sigma2 is None or sigma2 <= 0 or not z:
        return None
    k = int(math.floor(level * len(z)))
    if k < MIN_TAIL_OBS:
        return None
    s = sorted(z)
    q = s[k - 1]
    sigma = math.sqrt(sigma2)
    return {"var": -q * sigma, "es": -statistics.fmean(s[:k]) * sigma, "tail_n": k}


def chisq_sf(x, df):
    if x <= 0:
        return 1.0
    if df == 1:
        return math.erfc(math.sqrt(x / 2))
    if df == 2:
        return math.exp(-x / 2)
    return float("nan")


def kupiec(breaches, alpha):
    n = len(breaches)
    if n < 30 or not (0 < alpha < 1):
        return None
    x = sum(1 for b in breaches if b)
    p = x / n

    def term(count, prob):
        return count * math.log(prob) if count > 0 and prob > 0 else 0.0

    l0 = term(n - x, 1 - alpha) + term(x, alpha)
    l1 = term(n - x, 1 - p) + term(x, p)
    lr = max(0.0, -2 * (l0 - l1))
    return {"lr": lr, "p": min(1.0, max(0.0, chisq_sf(lr, 1))), "n": n,
            "breaches": x, "rate": p}


# ── data access (read-only) ──────────────────────────────────────────────────

def open_db(path):
    return sqlite3.connect(f"file:{path}?mode=ro", uri=True)


def universe(con, limit=None, companies_only=False):
    """Symbols eligible BEFORE any result is seen.

    Two screens, both fixed here rather than applied after scoring:
      - at least MIN_BARS daily bars, because the model needs MIN_TRAIN rows;
      - fewer than MAX_FLAT_SHARE flat (High == Low) sessions, because a range
        estimator is structurally invalid on a name that barely trades.

    Deliberately NOT screened on active=1. That is the survivorship trap
    tools/revalidate_structural.py exists to expose: dropping delisted names
    keeps only the symbols that survived, which is a result in itself.
    """
    q = """
      SELECT b.symbol_id,
             COUNT(*) AS n,
             SUM(CASE WHEN b.high = b.low THEN 1 ELSE 0 END) AS flat
        FROM bars b
       WHERE b.tf = '1d'
       GROUP BY b.symbol_id
      HAVING n >= ?
    """
    rows = con.execute(q, (MIN_BARS,)).fetchall()
    keep = [sid for (sid, n, flat) in rows if flat / n < MAX_FLAT_SHARE]
    if companies_only:
        have = {r[0] for r in con.execute("SELECT DISTINCT symbol_id FROM fundamentals")}
        keep = [s for s in keep if s in have]
    keep.sort()
    return keep[:limit] if limit else keep


def bars_for(con, symbol_id):
    return con.execute(
        "SELECT ts, open, high, low, close FROM bars "
        "WHERE symbol_id=? AND tf='1d' ORDER BY ts", (symbol_id,)).fetchall()


# ── the walk-forward ─────────────────────────────────────────────────────────

def walk_symbol(ts, rv, h):
    """Yield one record per forecastable day. Refits every REFIT_EVERY sessions.

    Everything a record contains was computable at time t except `actual`,
    which is the outcome it is graded against.
    """
    ewma = ewma_series(rv)
    fit, out = None, []
    for t in range(MIN_HISTORY, len(rv) - h):
        if fit is None or (t - MIN_HISTORY) % REFIT_EVERY == 0:
            f = fit_at(rv, t, h)
            if f is not None:
                fit = f
        if fit is None:
            continue
        actual = target_at(rv, t, h)
        if actual is None:
            continue
        har = predict_at(rv, t, fit)
        rw = rv[t] if rv[t] is not None and rv[t] > 0 else None
        ew = ewma[t]
        fl = rolling_mean(rv, t, FLAT_WINDOW, FLAT_WINDOW // 2)
        if har is None or rw is None or ew is None or fl is None:
            continue
        out.append({"ts": ts[t], "actual": actual, "har": har,
                    "rw": rw, "ewma": ew, "flat": fl, "beta": fit["beta"]})
    return out


def day_key(ts):
    return dt.datetime.fromtimestamp(ts, dt.timezone.utc).strftime("%Y-%m-%d")


def aggregate(records_by_symbol, h):
    """Collapse (symbol, day) to ONE observation per day, then test.

    This is the step that decides whether the answer is real. See
    diebold_mariano's docstring.
    """
    per_day = {}   # day -> model -> [losses]
    n_fc = 0
    for recs in records_by_symbol:
        for r in recs:
            d = day_key(r["ts"])
            slot = per_day.setdefault(d, {"har_q": [], "rw_q": [], "ew_q": [], "fl_q": [],
                                          "har_m": [], "ew_m": []})
            a = r["actual"]
            for key, fc in (("har", r["har"]), ("rw", r["rw"]), ("ew", r["ewma"]), ("fl", r["flat"])):
                q = qlike(a, fc)
                if q is not None:
                    slot[f"{key}_q"].append(q)
            for key, fc in (("har", r["har"]), ("ew", r["ewma"])):
                m = mse(a, fc)
                if m is not None:
                    slot[f"{key}_m"].append(m)
            n_fc += 1

    days = sorted(d for d, s in per_day.items() if s["har_q"] and s["ew_q"])
    if not days:
        return None

    def daily(model):
        return [statistics.fmean(per_day[d][model]) for d in days]

    har_q, rw_q, ew_q, fl_q = daily("har_q"), daily("rw_q"), daily("ew_q"), daily("fl_q")
    har_m, ew_m = daily("har_m"), daily("ew_m")

    # HAC lag must be at least the horizon: overlapping targets are
    # autocorrelated by construction.
    lag = max(newey_west_lag(len(days)), h)

    def compare(model, null, label):
        d = [a - b for a, b in zip(model, null)]
        r = diebold_mariano(d, lag)
        if r is None:
            return None
        r["label"] = label
        r["ci"] = block_bootstrap_ci(d)
        r["better"] = "model" if r["mean"] < 0 else "null"
        return r

    return {
        "horizon": h,
        "forecasts": n_fc,
        "day_clusters": len(days),
        "hac_lag": lag,
        "mean_qlike": {
            "har": statistics.fmean(har_q), "rw": statistics.fmean(rw_q),
            "ewma": statistics.fmean(ew_q), "flat22": statistics.fmean(fl_q),
        },
        "mean_mse": {"har": statistics.fmean(har_m), "ewma": statistics.fmean(ew_m)},
        "dm": [r for r in (
            compare(har_q, ew_q, "QLIKE: HAR vs EWMA(0.94)   [HEADLINE]"),
            compare(har_q, rw_q, "QLIKE: HAR vs random walk"),
            compare(har_q, fl_q, "QLIKE: HAR vs flat 22d"),
            compare(har_m, ew_m, "MSE:   HAR vs EWMA(0.94)"),
        ) if r],
        "by_year": by_year(per_day, days),
    }


def by_year(per_day, days):
    """Sub-period stability. NOT optional here: this repository's settled
    verdict on its own IC is that it FLIPS SIGN per sub-period, so a pooled
    figure that conceals a flip is dishonest in this codebase specifically."""
    years = {}
    for d in days:
        y = d[:4]
        s = per_day[d]
        if not s["har_q"] or not s["ew_q"]:
            continue
        years.setdefault(y, []).append(
            statistics.fmean(s["har_q"]) - statistics.fmean(s["ew_q"]))
    out = {}
    for y, ds in sorted(years.items()):
        if len(ds) < 20:
            out[y] = {"days": len(ds), "mean_loss_diff": None,
                      "note": "fewer than 20 day-clusters; no figure"}
            continue
        r = diebold_mariano(ds, newey_west_lag(len(ds)))
        out[y] = {"days": len(ds),
                  "mean_loss_diff": statistics.fmean(ds),
                  "t": r["t"] if r else None,
                  "better": "HAR" if statistics.fmean(ds) < 0 else "EWMA"}
    return out


# -- VaR section -------------------------------------------------------------

def var_section(con, symbol_ids, rv_cache):
    """Per-symbol coverage. NEVER pooled: a market-wide down day breaches every
    symbol at once, so a pooled independence test returns p ~ 0 for any model.
    The panel headline is the breach-rate distribution, not a pooled statistic."""
    per_symbol, rates = [], {lv: [] for lv in VAR_LEVELS}
    for sid in symbol_ids:
        cached = rv_cache.get(sid)
        if not cached:
            continue
        ts, rv, closes = cached
        recs = walk_symbol(ts, rv, 1)
        if len(recs) < 300:
            continue
        idx = {r["ts"]: r for r in recs}
        rets, fc = [], []
        for i in range(1, len(ts)):
            r = idx.get(ts[i - 1])
            if r is None or closes[i - 1] <= 0 or closes[i] <= 0:
                continue
            rets.append(math.log(closes[i] / closes[i - 1]))
            fc.append(r["har"])
        if len(rets) < 400:
            continue
        z = [r / math.sqrt(v) for r, v in zip(rets, fc) if v > 0]
        if len(z) < 400:
            continue
        for lv in VAR_LEVELS:
            breaches = causal_breaches(rets, fc, z, lv)
            k = kupiec(breaches, lv)
            if k is None:
                continue
            rates[lv].append(k["rate"])
            per_symbol.append({"symbol_id": sid, "level": lv, "rate": k["rate"],
                               "p": k["p"], "n": k["n"]})
    out = {}
    for lv in VAR_LEVELS:
        rs = rates[lv]
        if not rs:
            out[str(lv)] = {"symbols": 0,
                            "note": "no symbol had a tail deeper than MIN_TAIL_OBS"}
            continue
        rejected = sum(1 for s in per_symbol if s["level"] == lv and s["p"] < 0.05)
        out[str(lv)] = {
            "symbols": len(rs),
            "nominal": lv,
            "mean_breach_rate": statistics.fmean(rs),
            "median_breach_rate": statistics.median(rs),
            "kupiec_rejected_at_5pct": rejected,
            "kupiec_rejected_share": rejected / len(rs),
            "expected_rejection_share_by_chance": 0.05,
        }
    return out


def causal_breaches(rets, fc, z, level):
    """Breach flags whose VaR quantile reads ONLY the past.

    THE BUG THIS REPLACED. The first version sorted the WHOLE residual sample,
    took its alpha-quantile, and then counted breaches on that same sample.
    That forces the breach rate to equal alpha almost exactly BY CONSTRUCTION,
    and it produced a spectacular-looking result on the full universe: 740
    symbols, mean breach rate 4.88% against a 5% nominal, and ZERO Kupiec
    rejections at the 5% level.

    Zero rejections is what gave it away. A perfectly calibrated model should
    still be rejected about 5% of the time by chance, so a test that never
    rejects anything is not measuring calibration -- it is measuring its own
    fitted quantile. The number was manufactured by lookahead.

    Here the quantile used on day i is built from residuals STRICTLY BEFORE i,
    expanding as history accrues, and days without a deep enough tail yet are
    EXCLUDED rather than scored as passes. bisect maintains the sorted prefix
    without re-sorting each step.
    """
    past, out = [], []
    for i in range(len(z)):
        k = int(math.floor(level * len(past)))
        if k >= MIN_TAIL_OBS:
            out.append(rets[i] < past[k - 1] * math.sqrt(fc[i]))
        bisect.insort(past, z[i])
    return out


# -- cross-check against the Go implementation -------------------------------

def crosscheck(con, symbol):
    """Dump this file's RV series for one symbol so the Go estimator can be
    compared against it. Agreement is asserted on the ESTIMATOR, which is where
    a transcription error would silently change every downstream number."""
    row = con.execute("SELECT id FROM symbols WHERE symbol=? LIMIT 1",
                      (symbol,)).fetchone()
    if not row:
        print("no such symbol: " + symbol, file=sys.stderr)
        return 2
    bars = bars_for(con, row[0])
    ts, rv, x = rv_series(bars)
    real = [(t, v) for t, v in zip(ts, rv) if v is not None]
    print("%s: %d bars, %d estimable, exclusions %s" % (symbol, len(bars), len(real), x))
    os.makedirs("scratchpad", exist_ok=True)
    out = os.path.join("scratchpad", "rv_" + symbol + ".csv")
    with open(out, "w", encoding="utf-8") as fh:
        fh.write("ts,rv\n")
        for t, v in real:
            fh.write("%d,%.17g\n" % (t, v))
    print("wrote " + out)
    return 0


def selfcheck():
    """Assert the identities a silent change would break. Prints on success."""
    lo = 100.0
    hi = lo * math.e
    mid = lo * math.sqrt(math.e)
    _, rv, _ = rv_series([(0, mid, hi, lo, mid), (1, mid, hi, lo, mid)])
    assert abs(rv[1] - 0.5) < 1e-9, rv[1]

    _, rv, x = rv_series([(0, 100, 101, 99, 100), (1, 100, 100, 100, 100)])
    assert rv[1] is None and x["flat_range"] == 1

    _, rv, x = rv_series([(0, 100, 101, 99, 100), (1, 200, 210, 195, 205)])
    assert rv[1] is None and x["overnight"] == 1

    assert target_at([1, 2, None, 4, 5], 0, 3) is None
    assert abs(target_at([1, 2, 3, 4, 5], 0, 3) - 3.0) < 1e-12

    assert abs(qlike(0.04, 0.04)) < 1e-15
    assert qlike(0.04, 0.02) > qlike(0.04, 0.08)

    # A CONSTANT differential has zero variance and therefore no t-statistic;
    # both implementations return one rather than dividing by zero, so the
    # series used here has to actually vary.
    d = [-0.01 + 0.002 * math.sin(i) for i in range(300)]
    r = diebold_mariano(d, newey_west_lag(len(d)))
    assert r["mean"] < 0 and r["t"] < 0, r
    up = diebold_mariano([-v for v in d], newey_west_lag(len(d)))
    assert up["mean"] > 0 and up["t"] > 0, up
    flat = diebold_mariano([-0.01] * 200, 4)
    assert flat["t"] is None, flat

    assert abs(chisq_sf(3.84146, 1) - 0.05) < 1e-4
    assert abs(chisq_sf(5.99146, 2) - 0.05) < 1e-4

    # THE VaR QUANTILE MUST NOT READ THE FUTURE. Changing residuals AFTER day
    # i must not change whether day i was a breach. This is the assertion that
    # would have caught the lookahead described in causal_breaches.
    n = 600
    rets = [-0.01 + 0.02 * math.sin(i) for i in range(n)]
    fc = [4e-4] * n
    zc = [r / math.sqrt(4e-4) for r in rets]
    base = causal_breaches(rets, fc, zc, 0.05)
    poisoned = list(zc)
    for i in range(400, n):
        poisoned[i] = -99.0          # a catastrophic future
    after = causal_breaches(rets, fc, poisoned, 0.05)
    assert len(base) == len(after), (len(base), len(after))
    cut = 400 - (n - len(base))      # index of day 400 within the emitted list
    assert base[:cut] == after[:cut], "the VaR quantile read the future"

    good = kupiec([i % 20 == 0 for i in range(1000)], 0.05)
    assert good["lr"] < 1e-9, good
    bad = kupiec([i % 4 == 0 for i in range(1000)], 0.05)
    assert bad["p"] < 1e-6, bad

    print("SELFCHECK OK - 12 identities hold, including VaR causality")
    return 0


def main():
    ap = argparse.ArgumentParser(description="HAR-RV walk-forward backtest")
    ap.add_argument("--db", default="data/signaldeck.db")
    ap.add_argument("--json", default="tools/rv_forecast_backtest_result.json")
    ap.add_argument("--limit", type=int, default=None,
                    help="cap the universe; for a smoke run, never for a verdict")
    ap.add_argument("--companies-only", action="store_true",
                    help="operating companies only (join fundamentals)")
    ap.add_argument("--crosscheck", metavar="SYMBOL")
    ap.add_argument("--selfcheck", action="store_true")
    ap.add_argument("--var-only", action="store_true",
                    help="recompute ONLY the VaR section and merge it into --json")
    a = ap.parse_args()

    if a.selfcheck:
        return selfcheck()

    con = open_db(a.db)
    if a.crosscheck:
        return crosscheck(con, a.crosscheck)

    syms = universe(con, a.limit, a.companies_only)
    print("universe: %d symbols (>= %d daily bars, < %.0f%% flat sessions%s)"
          % (len(syms), MIN_BARS, MAX_FLAT_SHARE * 100,
             ", operating companies only" if a.companies_only else ""),
          file=sys.stderr)

    rv_cache = {}
    excl = {"flat_range": 0, "overnight": 0, "non_pos": 0,
            "gk_negative": 0, "floored": 0, "no_prev": 0}
    for i, sid in enumerate(syms):
        bars = bars_for(con, sid)
        ts, rv, x = rv_series(bars)
        rv_cache[sid] = (ts, rv, [b[4] for b in bars])
        for k in excl:
            excl[k] += x[k]
        if (i + 1) % 100 == 0:
            print("  rv: %d/%d" % (i + 1, len(syms)), file=sys.stderr)

    if a.var_only:
        # Recompute just the coverage section and merge it back. The horizon
        # walk-forwards are unchanged by a VaR fix and cost most of the runtime.
        with open(a.json, encoding="utf-8") as fh:
            existing = json.load(fh)
        existing["var_coverage"] = var_section(con, syms, rv_cache)
        existing["var_recomputed"] = dt.datetime.now(dt.timezone.utc).isoformat()
        with open(a.json, "w", encoding="utf-8") as fh:
            json.dump(existing, fh, indent=2)
        print(json.dumps(existing["var_coverage"], indent=2))
        return 0

    result = {
        "generated": dt.datetime.now(dt.timezone.utc).isoformat(),
        "db": os.path.abspath(a.db),
        "universe": {"symbols": len(syms), "min_bars": MIN_BARS,
                     "max_flat_share": MAX_FLAT_SHARE,
                     "companies_only": a.companies_only,
                     "survivorship": "NOT screened on active=1"},
        "exclusions": excl,
        "horizons": {},
    }

    for h in HORIZONS:
        print("  walk-forward h=%d ..." % h, file=sys.stderr)
        recs = []
        for j, sid in enumerate(syms):
            ts, rv, _ = rv_cache[sid]
            r = walk_symbol(ts, rv, h)
            if r:
                recs.append(r)
            if (j + 1) % 100 == 0:
                print("    %d/%d" % (j + 1, len(syms)), file=sys.stderr)
        agg = aggregate(recs, h)
        if agg:
            result["horizons"][str(h)] = agg

    print("  VaR coverage ...", file=sys.stderr)
    result["var_coverage"] = var_section(con, syms, rv_cache)

    with open(a.json, "w", encoding="utf-8") as fh:
        json.dump(result, fh, indent=2)

    print(json.dumps(result["horizons"], indent=2)[:4000])
    print("wrote " + a.json, file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())

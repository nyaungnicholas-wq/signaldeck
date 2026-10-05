#!/usr/bin/env python3
"""Cointegration pairs trading — and the rigorous resolution of H018 (CORR63).

Why this exists
---------------
H018 sits in the research ledger unshipped: "63d SPY-correlation regime persists
(trailing-rank predictable)", 73.1% point estimate, quarter-clustered CI
[0.671, 0.795]. The lower bound straddles the 0.70 product bar, so it was
ledgered as "real but tentative" and never surfaced. It has been stuck there
because the test as framed cannot settle it — a correlation-rank persistence
statistic is not a decision anyone can trade, so no amount of extra quarters
turns it into a product.

Pairs trading is the rigorous version of the same idea. Cointegration is a
strictly stronger condition than correlation: correlated names move together in
returns, cointegrated names have a stationary price SPREAD that mean-reverts to
a level. If H018's persistence is real AND economically meaningful, then pairs
selected on trailing co-movement should make money out of sample. If the
persistence is a statistical artifact of shared market beta, the spread trade
loses to costs and H018 is dead.

So this tool answers three questions in one walk-forward pass:

  Q1  Does trailing co-movement persist out of sample?         (H018 restated)
  Q2  Does cointegration persist BETTER than raw correlation?  (is the stronger
      condition worth the extra machinery, or is it the same information?)
  Q3  Does any of it survive contact with trading costs?       (the only
      question that decides whether H018 ships)

Method — the parts that keep it honest
--------------------------------------
  * WALK-FORWARD, NON-OVERLAPPING. Formation window 252 sessions selects and
    parameterizes pairs; the following 63 sessions trade them and are never
    looked at first. Blocks step a full 63 sessions so no two trade windows
    share a day.
  * FROZEN PARAMETERS. Hedge ratio, spread mean and spread sd all come from the
    formation window only. Recomputing them inside the trade window is the
    classic pairs-trading lookahead and it manufactures most published Sharpes.
  * SURVIVORSHIP — what is and is not controlled. Inactive symbols are
    included, and a leg that stops printing is closed at its last price rather
    than quietly dropped. That removes WATCHLIST-SELECTION bias: pairs are not
    restricted to names the platform still tracks today. It does NOT establish
    delisting-risk realism, because this database has almost no delistings —
    delisted_at is NULL for every symbol and only ~18 of 746 inactive names
    ever stop printing bars. The trade that matters most to a pairs book, the
    leg that goes to zero and never converges, is essentially absent from the
    sample. Treat the tail risk here as understated by an unknown amount; the
    run reports how many legs actually died so the reader can see it is small.
  * MATCHED NULLS. Cointegration-selected pairs are compared against random
    same-sector pairs traded on identical rules, and against the LEAST
    cointegrated pairs. If selection carries no information the three agree.
  * BLOCK BOOTSTRAP. Each walk-forward block is one cluster. Trades inside a
    block share a market regime; treating them as independent is how a pairs
    study reports a t-stat of 6 on what is really 28 observations.
  * COSTS QUOTED AS A SWEEP, not a single optimistic number, because pairs
    trading is a cost-dominated strategy and the break-even level is the
    finding.

Reproducibility — this study is PINNED to a snapshot
-----------------------------------------------------
The exact universe the published result was computed from (dates, closes,
dollar volumes, symbols, SIC sectors) is frozen at repro/pairs_universe_v1.npz
and content-hashed with SNAPSHOT_SHA256 below. When that file exists it is the
default data source. The hash is over the canonical array bytes, not the
container file, so re-zipping cannot silently change what "the same data" means.
The live DB keeps growing; the snapshot is the citable dataset.

The snapshot is a LOCAL pin, not a redistributable artifact. The raw bar matrix
is license-classified (audit finding A10) and repro/.gitignore refuses `*.npz`
outright, so it has never been committed and a fresh clone does not have it --
this docstring previously claimed "a stranger with the repo but WITHOUT
data/signaldeck.db reruns the identical study", and that stranger could in fact
reproduce nothing. SNAPSHOT_SHA256 below is the only place the digest lives;
PAIRS_TRADING.md does not carry it. For the redistribution-safe procedure --
load your own licensed bars, match them against the per-series hashes in
repro/pairs_inputs.csv, then rerun -- see REPRODUCE.md, section "Pairs study".

Usage:
    python3 tools/pairs_trading.py                    # full run, default costs
    python3 tools/pairs_trading.py --cost-bps 5       # single cost level
    python3 tools/pairs_trading.py --quick            # fewer sectors, for a smoke test
    python3 tools/pairs_trading.py --write-snapshot   # freeze the DB universe to repro/
    python3 tools/pairs_trading.py --from-db          # ignore the snapshot, read the DB
"""
from __future__ import annotations

import argparse
import datetime
import hashlib
import itertools
import json
import math
import os
import random
import sqlite3
import sys
from collections import defaultdict

import numpy as np

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_DB = os.path.join(REPO_ROOT, "data", "signaldeck.db")
DEFAULT_SNAPSHOT = os.path.join(REPO_ROOT, "repro", "pairs_universe_v1.npz")

# SHA-256 of the CANONICAL CONTENT of the pinned snapshot (dates ++ close ++
# dvol raw little-endian bytes ++ symbols ++ sectors), not of the .npz file, so
# zip metadata cannot masquerade as a data change. Set by --write-snapshot;
# verified on every snapshot load. Changing it means the published numbers in
# PAIRS_TRADING.md refer to a different dataset — rev the filename if you do.
SNAPSHOT_SHA256 = "f84ee4bdeafb9afa329011909cdd6469e9bf49d71dbb6ec33aefb31523a39c61"

FORMATION = 252          # 1 trading year to select and parameterize
TRADE = 63               # H018's own horizon, and one quarter of trading
MIN_PRICE = 5.0          # sub-$5 names have spreads that eat any edge
MIN_DOLLAR_VOL = 1e6     # median formation-window dollar volume
MAX_PER_SECTOR = 40      # cap the candidate pool so pair count stays sane
TOP_PAIRS = 20           # pairs traded per block
ENTRY_Z = 2.0
EXIT_Z = 0.5
STOP_Z = 4.0
MAX_SANE_RETURN = 0.65   # the engine's wild-move guard, same constant
BOOTSTRAP = 2000
SEED = 12345

# Engle-Granger residual-based ADF critical values (2 variables, with constant).
# These are NOT the standard ADF values — using -2.86 on a regression residual
# is the single most common way a pairs study finds cointegration that is not
# there, because the residual was fitted to be stationary-looking.
EG_CRIT = {"1%": -3.90, "5%": -3.34, "10%": -3.04}


# ---------------------------------------------------------------- primitives

def ols(y, X):
    """Least squares with an intercept prepended. Returns (coefs, residuals)."""
    A = np.column_stack([np.ones(len(y)), X])
    coef, *_ = np.linalg.lstsq(A, y, rcond=None)
    return coef, y - A @ coef


def adf(series, lags=1):
    """Augmented Dickey-Fuller t-statistic on the level, with constant.

    More negative = stronger evidence the series is stationary (mean-reverting).
    Returns None when the regression is degenerate.
    """
    y = np.asarray(series, dtype=float)
    n = len(y)
    if n < lags + 20:
        return None
    dy = np.diff(y)
    rows = n - 1 - lags
    if rows < 10:
        return None
    lhs = dy[lags:]
    cols = [y[lags:-1]]
    for L in range(1, lags + 1):
        cols.append(dy[lags - L:-L] if L else dy)
    X = np.column_stack(cols)
    A = np.column_stack([np.ones(rows), X])
    try:
        coef, res, rank, _ = np.linalg.lstsq(A, lhs, rcond=None)
    except np.linalg.LinAlgError:
        return None
    if rank < A.shape[1]:
        return None
    resid = lhs - A @ coef
    dof = rows - A.shape[1]
    if dof <= 0:
        return None
    s2 = float(resid @ resid) / dof
    try:
        xtx_inv = np.linalg.inv(A.T @ A)
    except np.linalg.LinAlgError:
        return None
    se = math.sqrt(max(s2 * xtx_inv[1, 1], 1e-300))
    if se <= 0:
        return None
    return float(coef[1] / se)


def half_life(spread):
    """OU half-life in sessions. A spread that reverts slower than the trade
    window cannot be traded inside it, however cointegrated it tests."""
    s = np.asarray(spread, dtype=float)
    ds = np.diff(s)
    lag = s[:-1]
    coef, _ = ols(ds, lag)
    k = coef[1]
    if k >= 0:
        return float("inf")
    return -math.log(2) / k


def spearman(a, b):
    """Rank correlation. Written out because scipy is not installed here."""
    a, b = np.asarray(a, float), np.asarray(b, float)
    if len(a) < 3:
        return None
    ra, rb = _rank(a), _rank(b)
    ra = ra - ra.mean()
    rb = rb - rb.mean()
    d = math.sqrt(float(ra @ ra) * float(rb @ rb))
    return float(ra @ rb) / d if d > 0 else None


def _rank(x):
    order = np.argsort(x, kind="mergesort")
    r = np.empty(len(x), dtype=float)
    r[order] = np.arange(len(x), dtype=float)
    # average ties so a wall of identical values does not create a fake ordering
    _, inv, cnt = np.unique(x, return_inverse=True, return_counts=True)
    if (cnt > 1).any():
        sums = np.zeros(len(cnt))
        np.add.at(sums, inv, r)
        r = (sums / cnt)[inv]
    return r


def wilson(k, n, z=1.96):
    if n <= 0:
        return (0.0, 0.0)
    p = k / n
    d = 1 + z * z / n
    c = (p + z * z / (2 * n)) / d
    m = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / d
    return (max(0.0, c - m), min(1.0, c + m))


def block_bootstrap(by_block, stat="mean", iters=BOOTSTRAP):
    """Resample whole BLOCKS. by_block: {block_key: [values]}.

    Trades opened in the same quarter share a market regime; resampling
    individual trades would treat 400 correlated outcomes as 400 independent
    ones and shrink the interval by ~sqrt(cluster size).
    """
    keys = [k for k, v in by_block.items() if v]
    if len(keys) < 4:
        return (None, None)
    rng = random.Random(SEED)
    out = []
    for _ in range(iters):
        pool = []
        for _ in range(len(keys)):
            pool.extend(by_block[keys[rng.randrange(len(keys))]])
        if not pool:
            continue
        arr = np.asarray(pool, dtype=float)
        out.append(float(arr.mean()) if stat == "mean" else float((arr > 0).mean()))
    if not out:
        return (None, None)
    out.sort()
    return (out[int(0.025 * len(out))], out[int(0.975 * len(out))])


def quarter_of(ts):
    d = datetime.datetime.utcfromtimestamp(ts)
    return f"{d.year}Q{(d.month - 1) // 3 + 1}"


# ------------------------------------------------------------------- loading

def universe_sha256(dates, close, dvol, syms, sectors):
    """Content hash of the universe — canonical bytes, container-independent.

    Field order, dtypes and the NUL separator are pinned: changing any of them
    invalidates the published hash, exactly like datasetver's canonicalFloatFmt.
    """
    h = hashlib.sha256()
    h.update(np.ascontiguousarray(np.asarray(dates, dtype="<i8")).tobytes())
    h.update(np.ascontiguousarray(np.asarray(close, dtype="<f8")).tobytes())
    h.update(np.ascontiguousarray(np.asarray(dvol, dtype="<f8")).tobytes())
    h.update("\n".join(syms).encode())
    h.update(b"\x00")
    h.update("\n".join(sectors).encode())
    return h.hexdigest()


def write_snapshot(path, dates, close, dvol, syms, sectors):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    np.savez_compressed(path, dates=np.asarray(dates, dtype="<i8"),
                        close=np.asarray(close, dtype="<f8"),
                        dvol=np.asarray(dvol, dtype="<f8"),
                        symbols=np.asarray(syms), sectors=np.asarray(sectors))
    return universe_sha256(dates, close, dvol, syms, sectors)


def load_snapshot(path, verify=True):
    z = np.load(path, allow_pickle=False)
    dates = z["dates"]
    close = z["close"].astype(np.float64)
    dvol = z["dvol"].astype(np.float64)
    syms = [str(s) for s in z["symbols"]]
    sectors = [str(s) for s in z["sectors"]]
    got = universe_sha256(dates, close, dvol, syms, sectors)
    if verify:
        if SNAPSHOT_SHA256 is None:
            print(f"  WARNING: no pinned hash in this file; snapshot content "
                  f"hash is {got}", file=sys.stderr)
        elif got != SNAPSHOT_SHA256:
            sys.exit(f"snapshot content hash mismatch:\n  pinned {SNAPSHOT_SHA256}"
                     f"\n  loaded {got}\nThis is not the dataset the published "
                     f"result was computed from. Pass --no-verify to run anyway.")
    return dates, close, dvol, syms, sectors, got


def quick_filter(close, dvol, syms, sectors):
    """Keep only the 4 biggest sectors — the smoke-test universe. Applied after
    loading so it works identically for the DB and the snapshot."""
    by_sec = defaultdict(list)
    for j, sec in enumerate(sectors):
        by_sec[sec].append(j)
    big = set(sorted(by_sec, key=lambda s: -len(by_sec[s]))[:4])
    cols = [j for j, sec in enumerate(sectors) if sec in big]
    return (close[:, cols], dvol[:, cols],
            [syms[j] for j in cols], [sectors[j] for j in cols])


def load_universe(db_path):
    """Returns (dates, close matrix, dollar-volume matrix, symbols, sectors).

    Matrices are (n_days, n_symbols) with NaN where a name had no print — which
    is exactly how a delisted name should look after its last session.
    """
    con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    cur = con.cursor()

    cur.execute("""
        SELECT s.id, s.symbol, COALESCE(substr(c.sic,1,2),'')
        FROM symbols s
        LEFT JOIN companies c ON c.ticker = s.symbol
        WHERE s.market='stocks'
    """)
    meta = {}
    for sid, sym, sic in cur.fetchall():
        if sic:                       # no sector -> no same-industry pairing
            meta[sid] = (sym, sic)
    if not meta:
        sys.exit("no stock symbols with SIC sectors — cannot form same-sector pairs")

    ids = sorted(meta)
    idx = {sid: i for i, sid in enumerate(ids)}

    cur.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    all_ts = [r[0] for r in cur.fetchall()]
    tidx = {t: i for i, t in enumerate(all_ts)}

    close = np.full((len(all_ts), len(ids)), np.nan)
    dvol = np.full((len(all_ts), len(ids)), np.nan)

    q = "SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d'"
    for sid, ts, c, v in cur.execute(q):
        j = idx.get(sid)
        if j is None or c is None or c <= 0:
            continue
        i = tidx[ts]
        close[i, j] = c
        dvol[i, j] = c * (v or 0.0)
    con.close()

    # Drop calendar days that are clearly not real sessions for this universe.
    live = np.isfinite(close).sum(axis=1)
    keep = live >= max(20, int(0.1 * len(ids)))
    dates = [t for t, k in zip(all_ts, keep) if k]
    return (np.asarray(dates), close[keep], dvol[keep],
            [meta[s][0] for s in ids], [meta[s][1] for s in ids])


# ------------------------------------------------------------------ the pass

def eligible(close, dvol, f0, f1, t1):
    """Columns tradable for a whole formation window.

    Note what is NOT required: data through the trade window. Requiring it
    would silently drop every pair whose leg died mid-trade — the survivorship
    bug that makes pairs backtests look safe.
    """
    fc = close[f0:f1]
    ok = np.isfinite(fc).all(axis=0)
    ok &= np.nanmin(np.where(np.isfinite(fc), fc, np.inf), axis=0) >= MIN_PRICE
    with np.errstate(invalid="ignore"):
        med = np.nanmedian(dvol[f0:f1], axis=0)
    ok &= np.nan_to_num(med) >= MIN_DOLLAR_VOL
    # refuse windows containing a wild move — same guard the engine applies
    r = fc[1:] / fc[:-1] - 1
    ok &= ~(np.abs(np.nan_to_num(r)) > MAX_SANE_RETURN).any(axis=0)
    return np.where(ok)[0]


def candidate_pairs(cols, sectors, rng):
    """Same-sector pairs, capped per sector so the pool stays comparable across
    blocks (an uncapped pool would make crowded sectors dominate)."""
    by_sec = defaultdict(list)
    for j in cols:
        by_sec[sectors[j]].append(j)
    pairs = []
    for sec, members in by_sec.items():
        if len(members) < 2:
            continue
        if len(members) > MAX_PER_SECTOR:
            members = rng.sample(members, MAX_PER_SECTOR)
        pairs.extend(itertools.combinations(sorted(members), 2))
    return pairs


def score_pairs(logc, pairs, f0, f1):
    """Formation-window statistics for every candidate pair.

    Returns list of dicts. Everything here is computed on formation data only.
    """
    win = logc[f0:f1]
    rets = np.diff(win, axis=0)
    out = []
    for a, b in pairs:
        ya, yb = win[:, a], win[:, b]
        ra, rb = rets[:, a], rets[:, b]
        sa, sb = ra.std(), rb.std()
        if sa <= 0 or sb <= 0:
            continue
        corr = float(np.corrcoef(ra, rb)[0, 1])
        if not math.isfinite(corr):
            continue
        coef, resid = ols(ya, yb)
        alpha, beta = float(coef[0]), float(coef[1])
        # A negative or extreme hedge ratio is not a pair, it is a curve fit.
        if not (0.1 <= beta <= 10.0):
            continue
        sd = float(resid.std())
        if sd <= 1e-6:
            continue
        stat = adf(resid)
        if stat is None:
            continue
        hl = half_life(resid)
        out.append({"a": a, "b": b, "corr": corr, "beta": beta, "alpha": alpha,
                    "mu": float(resid.mean()), "sd": sd, "adf": stat, "hl": hl})
    return out


def trade_pair(p, logc, close, t0, t1, cost_bps):
    """Trade one pair through the out-of-sample window on frozen parameters.

    Returns (daily return series over the window, list of closed trades).
    Daily returns are per unit of GROSS notional, so a 1-vs-beta pair is not
    quietly levered up relative to a 1-vs-1 pair.
    """
    a, b, beta = p["a"], p["b"], p["beta"]
    gross = 1.0 + abs(beta)
    cost = cost_bps / 1e4                       # per side, per unit gross

    daily = np.zeros(t1 - t0)
    trades = []
    pos = 0                                     # +1 long spread, -1 short
    entry_z = None
    entry_i = None
    cum = 0.0

    for k in range(t0, t1):
        la, lb = logc[k, a], logc[k, b]
        prev_ok = math.isfinite(logc[k - 1, a]) and math.isfinite(logc[k - 1, b])
        alive = math.isfinite(la) and math.isfinite(lb)

        # A leg stopped printing: close at the last good price, take the loss.
        if pos != 0 and not alive:
            trades.append({"ret": cum - cost, "bars": k - entry_i, "ei": entry_i,
                           "exit": "delisted", "entry_z": entry_z})
            pos, cum, entry_z, entry_i = 0, 0.0, None, None
            continue
        if not alive:
            continue

        if pos != 0 and prev_ok:
            ra = la - logc[k - 1, a]
            rb = lb - logc[k - 1, b]
            step = pos * (ra - beta * rb) / gross
            daily[k - t0] = step
            cum += step

        z = (la - beta * lb - p["alpha"] - p["mu"]) / p["sd"]

        if pos != 0:
            hit_stop = abs(z) >= STOP_Z
            reverted = (pos == 1 and z >= -EXIT_Z) or (pos == -1 and z <= EXIT_Z)
            last_bar = (k == t1 - 1)
            if hit_stop or reverted or last_bar:
                cum -= cost                     # exit cost
                daily[k - t0] -= cost
                trades.append({"ret": cum, "bars": k - entry_i, "ei": entry_i,
                               "exit": "stop" if hit_stop else
                                       ("revert" if reverted else "window_end"),
                               "entry_z": entry_z})
                pos, cum, entry_z, entry_i = 0, 0.0, None, None
        elif k < t1 - 1 and abs(z) >= ENTRY_Z and abs(z) < STOP_Z:
            pos = -1 if z > 0 else 1            # fade the divergence
            entry_z, entry_i, cum = float(z), k, -cost
            daily[k - t0] -= cost               # entry cost

    return daily, trades


def run(close, dvol, logc, dates, sectors, cost_bps, verbose=True):
    n_days = close.shape[0]
    rng = random.Random(SEED)
    res = {
        "coint": {"daily": defaultdict(list), "trades": defaultdict(list)},
        "random": {"daily": defaultdict(list), "trades": defaultdict(list)},
        "worst": {"daily": defaultdict(list), "trades": defaultdict(list)},
    }
    persistence = []          # H018: per-block correlation-rank persistence
    coint_persist = []        # Q2: does ADF rank persist better than corr rank?
    blocks = 0

    start = 0
    while start + FORMATION + TRADE <= n_days:
        f0, f1 = start, start + FORMATION
        t0, t1 = f1, f1 + TRADE
        bkey = quarter_of(int(dates[t0]))

        cols = eligible(close, dvol, f0, f1, t1)
        if len(cols) < 10:
            start += TRADE
            continue
        pairs = candidate_pairs(cols, sectors, rng)
        if len(pairs) < TOP_PAIRS * 3:
            start += TRADE
            continue

        scored = score_pairs(logc, pairs, f0, f1)
        if len(scored) < TOP_PAIRS * 3:
            start += TRADE
            continue

        # ---- H018, restated as a forward test on the SAME pair population.
        fwd = logc[t0:t1]
        fret = np.diff(fwd, axis=0)
        f_corr, x_corr, f_adf, x_adf = [], [], [], []
        for p in scored:
            ra, rb = fret[:, p["a"]], fret[:, p["b"]]
            if not (np.isfinite(ra).all() and np.isfinite(rb).all()):
                continue
            if ra.std() <= 0 or rb.std() <= 0:
                continue
            c = float(np.corrcoef(ra, rb)[0, 1])
            if not math.isfinite(c):
                continue
            _, resid = ols(fwd[:, p["a"]], fwd[:, p["b"]])
            s = adf(resid)
            if s is None:
                continue
            f_corr.append(p["corr"]); x_corr.append(c)
            f_adf.append(p["adf"]);   x_adf.append(s)
        if len(f_corr) >= 30:
            rc = spearman(f_corr, x_corr)
            ra_ = spearman(f_adf, x_adf)
            # H018's own binary framing: does the top decile of trailing
            # co-movement stay above the median forward? Within a block the
            # null is exactly 50% by construction, which is what makes this
            # version of the test interpretable at all.
            order = np.argsort(f_corr)[::-1]
            top = order[:max(3, len(order) // 10)]
            med = float(np.median(x_corr))
            hits = int(sum(1 for i in top if x_corr[i] > med))
            # The above-median bar is EASY and flatters the result. Two stricter
            # framings, because "stays in the top decile" is what a product
            # surface would actually have to promise. Their nulls are the
            # decile/tercile share itself (10% / 33%), not 50%.
            fwd_order = np.argsort(x_corr)[::-1]
            top_fwd = set(fwd_order[:max(3, len(fwd_order) // 10)].tolist())
            ter_fwd = set(fwd_order[:max(3, len(fwd_order) // 3)].tolist())
            d_hits = int(sum(1 for i in top if int(i) in top_fwd))
            t_hits = int(sum(1 for i in top if int(i) in ter_fwd))
            if rc is not None:
                persistence.append({"block": bkey, "rho": rc, "n": len(f_corr),
                                    "top_hits": hits, "top_n": len(top),
                                    "dec_hits": d_hits, "ter_hits": t_hits})
            if ra_ is not None:
                coint_persist.append({"block": bkey, "rho": ra_, "n": len(f_adf)})

        # ---- selection arms, all traded on identical rules
        by_adf = sorted(scored, key=lambda p: p["adf"])
        best = [p for p in by_adf if p["adf"] <= EG_CRIT["5%"]][:TOP_PAIRS]
        if len(best) < TOP_PAIRS:                    # not enough cointegrated
            best = by_adf[:TOP_PAIRS]
        worst = by_adf[-TOP_PAIRS:]
        randsel = rng.sample(scored, TOP_PAIRS)

        for arm, sel in (("coint", best), ("worst", worst), ("random", randsel)):
            port = np.zeros(t1 - t0)
            for p in sel:
                d, tr = trade_pair(p, logc, close, t0, t1, cost_bps)
                port += d / len(sel)
                for t in tr:
                    # Entry DAY is the cross-sectional cluster: trades opened
                    # the same session share the market's move that session.
                    t["day"] = int(dates[t.pop("ei")])
                    res[arm]["trades"][bkey].append(t)
            res[arm]["daily"][bkey].extend(port.tolist())

        blocks += 1
        if verbose:
            nb = len(res["coint"]["trades"][bkey])
            print(f"  block {blocks:2d} {bkey}  candidates={len(scored):5d} "
                  f"cointegrated={sum(1 for p in scored if p['adf'] <= EG_CRIT['5%']):4d} "
                  f"trades={nb:3d}", file=sys.stderr)
        start += TRADE

    return res, persistence, coint_persist, blocks


# ------------------------------------------------------------------ reporting

def design_effect(values, groups):
    """DEFF = cluster-robust variance of the mean / naive iid variance (CR0).

    DEFF k means the naive CI is sqrt(k) too narrow and the effective sample
    is n/k — the honest denominator for any 'n=938 trades' claim. Groups can
    be entry days (cross-sectional pseudo-replication) or quarters (regime).
    """
    x = np.asarray(values, float)
    n = len(x)
    if n < 3:
        return None, None
    s2 = float(x.var(ddof=1))
    if s2 <= 0:
        return None, None
    resid = defaultdict(float)
    xbar = x.mean()
    for v, g in zip(x, groups):
        resid[g] += v - xbar
    var_cl = sum(e * e for e in resid.values()) / (n * n)
    deff = var_cl / (s2 / n)
    return float(deff), float(n / max(deff, 1e-12))


def arm_stats(arm):
    trades, blocks_of = [], []
    for bk, v in arm["trades"].items():
        for t in v:
            trades.append(t)
            blocks_of.append(bk)
    daily = [d for v in arm["daily"].values() for d in v]
    if not trades:
        return None
    rets = np.asarray([t["ret"] for t in trades], float)
    dv = np.asarray(daily, float)
    ann = math.sqrt(252) * dv.mean() / dv.std() if dv.std() > 0 else 0.0
    by_block_ret = {k: [t["ret"] for t in v] for k, v in arm["trades"].items()}
    lo, hi = block_bootstrap(by_block_ret, "mean")
    wlo, whi = block_bootstrap(by_block_ret, "winrate")
    wins = int((rets > 0).sum())

    # Day-level clustering: the finer decomposition of the same non-independence
    # the quarter blocks capture coarsely. Days resampled as whole clusters.
    days_of = [t["day"] for t in trades]
    by_day_ret = defaultdict(list)
    for t in trades:
        by_day_ret[t["day"]].append(t["ret"])
    dlo, dhi = block_bootstrap(by_day_ret, "mean")
    deff_day, neff_day = design_effect(rets, days_of)
    deff_block, neff_block = design_effect(rets, blocks_of)

    return {
        "n_trades": len(trades),
        "mean_ret": float(rets.mean()),
        "mean_ci": (lo, hi),
        "win_rate": wins / len(trades),
        "win_ci": (wlo, whi),
        "win_wilson": wilson(wins, len(trades)),
        "median_ret": float(np.median(rets)),
        "sharpe": ann,
        "total_ret": float(dv.sum()),
        "exits": {e: sum(1 for t in trades if t["exit"] == e)
                  for e in ("revert", "stop", "window_end", "delisted")},
        "avg_bars": float(np.mean([t["bars"] for t in trades])),
        "n_entry_days": len(by_day_ret),
        "day_ci": (dlo, dhi),
        "deff_day": deff_day, "n_eff_day": neff_day,
        "deff_block": deff_block, "n_eff_block": neff_block,
    }


def print_arm(label, s):
    if s is None:
        print(f"{label:<28} no trades")
        return
    lo, hi = s["mean_ci"]
    ci = f"[{lo*100:+.3f}%, {hi*100:+.3f}%]" if lo is not None else "(few blocks)"
    wlo, whi = s["win_ci"]
    wci = f"[{wlo*100:.1f}%, {whi*100:.1f}%]" if wlo is not None else "(few blocks)"
    print(f"{label:<28} {s['n_trades']:6d} {s['mean_ret']*100:+8.3f}% {ci:<24} "
          f"{s['win_rate']*100:6.1f}% {wci:<20} {s['sharpe']:+6.2f}")


def report(res, persistence, coint_persist, blocks, cost_bps):
    print(f"\n{'='*112}")
    print(f"PAIRS TRADING — walk-forward, {FORMATION}d formation / {TRADE}d trade, "
          f"non-overlapping, {blocks} blocks, cost {cost_bps:.1f}bps/side")
    print("=" * 112)
    print(f"{'ARM':<28} {'trades':>6} {'mean/trade':>9} {'95% CI (block bootstrap)':<24} "
          f"{'win rate':>8} {'95% CI':<20} {'Sharpe':>6}")
    print("-" * 112)
    stats = {}
    for arm, label in (("coint", "COINTEGRATED (selected)"),
                       ("random", "RANDOM same-sector (null)"),
                       ("worst", "LEAST cointegrated (null)")):
        stats[arm] = arm_stats(res[arm])
        print_arm(label, stats[arm])
    print("-" * 112)

    c, r = stats["coint"], stats["random"]
    if c and r:
        edge = (c["mean_ret"] - r["mean_ret"]) * 100
        print(f"SELECTION EDGE over random same-sector pairs: {edge:+.3f}% per trade")
        lo, hi = c["mean_ci"]
        if lo is not None:
            verdict = ("POSITIVE and the CI excludes zero" if lo > 0 else
                       "NOT distinguishable from zero — the CI contains it")
            print(f"Cointegrated arm mean return is {verdict}.")

    # Clustering audit: trades are NOT independent draws. Same-day entries
    # share the market's move (cross-sectional pseudo-replication); same-quarter
    # entries share a regime. DEFF says how much a naive n overstates evidence.
    print("\nClustering audit (design effect = naive-CI shrinkage factor; n_eff = n/DEFF):")
    for arm, label in (("coint", "cointegrated"), ("random", "random"),
                       ("worst", "least-coint")):
        s = stats[arm]
        if not s or s["deff_day"] is None:
            continue
        dlo, dhi = s["day_ci"]
        dci = (f"[{dlo*100:+.3f}%, {dhi*100:+.3f}%]"
               if dlo is not None else "(few days)")
        print(f"  {label:<13} {s['n_trades']:4d} trades on {s['n_entry_days']:3d} entry days | "
              f"day-clustered CI {dci:<22} DEFF(day) {s['deff_day']:5.2f} -> "
              f"n_eff {s['n_eff_day']:6.0f} | DEFF(quarter) "
              f"{s['deff_block'] if s['deff_block'] is not None else float('nan'):5.2f} -> "
              f"n_eff {s['n_eff_block'] if s['n_eff_block'] is not None else float('nan'):6.0f}")
    if c:
        ex = c["exits"]
        tot = max(1, sum(ex.values()))
        print("Exit mix (cointegrated arm): " + ", ".join(
            f"{k} {v} ({100*v/tot:.0f}%)" for k, v in ex.items()))
        print(f"Average holding period: {c['avg_bars']:.1f} sessions "
              f"(trade window is {TRADE})")
        print(f"Legs that stopped printing mid-trade: {ex['delisted']} — near zero "
              f"because this database has almost no delistings, NOT because the")
        print(f"  strategy avoids them. Tail risk is understated; see the module docstring.")

    # Concentration: a strategy whose entire P&L lives in two quarters is a
    # regime bet wearing a backtest's clothes. The block bootstrap already
    # penalizes this, but the reader should see it directly.
    trades = res["coint"]["trades"]
    if trades:
        per = {k: float(np.mean([t["ret"] for t in v])) for k, v in trades.items() if v}
        ranked = sorted(per.items(), key=lambda kv: -kv[1])
        pos = sum(1 for v in per.values() if v > 0)
        print(f"\nPer-block mean return, cointegrated arm — {pos}/{len(per)} blocks positive")
        print("  best:  " + ", ".join(f"{k} {v*100:+.2f}%" for k, v in ranked[:4]))
        print("  worst: " + ", ".join(f"{k} {v*100:+.2f}%" for k, v in ranked[-4:]))
        allr = np.asarray([t["ret"] for v in trades.values() for t in v], float)
        w, l = allr[allr > 0], allr[allr <= 0]
        if len(w) and len(l):
            print(f"  winners {len(w)} avg {w.mean()*100:+.2f}% | "
                  f"losers {len(l)} avg {l.mean()*100:+.2f}% -> the positive mean comes "
                  f"from SIZE, not frequency")
    return stats


def report_h018(persistence, coint_persist):
    print(f"\n{'='*112}")
    print("H018 (CORR63) — does trailing co-movement rank persist 63 sessions forward?")
    print("=" * 112)
    if not persistence:
        print("  insufficient blocks to test")
        return None
    rhos = [p["rho"] for p in persistence]
    by_block = {p["block"]: [p["rho"]] for p in persistence}
    lo, hi = block_bootstrap(by_block, "mean")
    hits = sum(p["top_hits"] for p in persistence)
    tot = sum(p["top_n"] for p in persistence)
    hb = defaultdict(list)
    for p in persistence:
        hb[p["block"]].extend([1.0] * p["top_hits"] +
                              [0.0] * (p["top_n"] - p["top_hits"]))
    hlo, hhi = block_bootstrap(hb, "mean")

    print(f"  Spearman(formation corr rank, forward corr rank), per block:")
    print(f"     mean rho = {np.mean(rhos):+.3f}   median = {np.median(rhos):+.3f}   "
          f"blocks = {len(rhos)}")
    if lo is not None:
        print(f"     95% CI (block bootstrap) = [{lo:+.3f}, {hi:+.3f}]")
    print(f"     blocks with rho > 0: {sum(1 for r in rhos if r > 0)}/{len(rhos)}")
    print()
    print(f"  H018's own binary framing — top-decile trailing co-movement stays")
    print(f"  above the forward median (null is exactly 50% within a block):")
    acc = hits / tot if tot else 0
    wlo, whi = wilson(hits, tot)
    print(f"     {hits}/{tot} = {acc*100:.1f}%   Wilson [{wlo*100:.1f}%, {whi*100:.1f}%]"
          + (f"   block-bootstrap CI [{hlo*100:.1f}%, {hhi*100:.1f}%]"
             if hlo is not None else ""))
    print(f"     ledgered H018 point estimate was 73.1%, CI [67.1%, 79.5%]")
    print(f"     NOTE: above-median is an EASY bar and is NOT what H018 measured.")
    print(f"     The two strict framings below are the comparable ones:")
    for label, key, null in (("stays in TOP DECILE", "dec_hits", 0.10),
                             ("stays in TOP TERCILE", "ter_hits", 1 / 3)):
        k = sum(p[key] for p in persistence)
        n = sum(p["top_n"] for p in persistence)
        sb = defaultdict(list)
        for p in persistence:
            sb[p["block"]].extend([1.0] * p[key] + [0.0] * (p["top_n"] - p[key]))
        slo, shi = block_bootstrap(sb, "mean")
        ci = f"[{slo*100:.1f}%, {shi*100:.1f}%]" if slo is not None else "(few blocks)"
        print(f"       {label:<22} {k}/{n} = {k/n*100:5.1f}%  block CI {ci:<18} "
              f"null {null*100:.0f}%")

    if coint_persist:
        crhos = [p["rho"] for p in coint_persist]
        cb = {p["block"]: [p["rho"]] for p in coint_persist}
        clo, chi = block_bootstrap(cb, "mean")
        print()
        print("  Q2 — does COINTEGRATION rank persist as well as correlation rank?")
        print(f"     Spearman(formation ADF rank, forward ADF rank): "
              f"mean rho = {np.mean(crhos):+.3f}"
              + (f"   95% CI [{clo:+.3f}, {chi:+.3f}]" if clo is not None else ""))
        print(f"     vs correlation-rank persistence mean rho = {np.mean(rhos):+.3f}")
    dec = sum(p["dec_hits"] for p in persistence) / tot if tot else 0
    ter = sum(p["ter_hits"] for p in persistence) / tot if tot else 0
    return {"mean_rho": float(np.mean(rhos)), "rho_ci": (lo, hi),
            "top_acc": acc, "top_k": hits, "top_n": tot,
            "dec_acc": dec, "ter_acc": ter,
            "top_ci": (hlo, hhi), "blocks": len(rhos),
            "coint_rho": float(np.mean([p["rho"] for p in coint_persist]))
                         if coint_persist else None}


def verdict(stats_by_cost, h018):
    print(f"\n{'='*112}")
    print("VERDICT")
    print("=" * 112)
    if h018:
        lo, hi = h018["top_ci"]
        persists = h018["mean_rho"] > 0 and (h018["rho_ci"][0] or 0) > 0
        print(f"Q1  Trailing co-movement rank {'DOES' if persists else 'does NOT reliably'} "
              f"persist 63 sessions forward "
              f"(mean Spearman rho {h018['mean_rho']:+.3f} over {h018['blocks']} blocks).")
        print(f"    Binary framings: above-median {h018['top_acc']*100:.1f}% (easy bar, "
              f"null 50%); stays top-tercile {h018['ter_acc']*100:.1f}% (null 33%); "
              f"stays top-decile {h018['dec_acc']*100:.1f}% (null 10%).")
        print(f"    None of these is the identical statistic H018 ledgered (that was "
              f"SPY-correlation tiering, not pair co-movement), so read them as "
              f"corroborating the mechanism, not as a restatement of 73.1%.")
    if h018 and h018["coint_rho"] is not None:
        better = h018["coint_rho"] > h018["mean_rho"]
        print(f"Q2  Cointegration rank persists {'BETTER' if better else 'WORSE'} than "
              f"correlation rank ({h018['coint_rho']:+.3f} vs {h018['mean_rho']:+.3f}) — "
              f"the stronger condition is {'worth' if better else 'not worth'} the machinery.")
    print("Q3  Economics after costs:")
    for cost, st in sorted(stats_by_cost.items()):
        c = st["coint"]
        if not c:
            continue
        lo, hi = c["mean_ci"]
        sign = ("profitable, CI excludes zero" if lo is not None and lo > 0
                else "NOT profitable at conventional confidence")
        print(f"      {cost:>4.1f} bps/side: {c['mean_ret']*100:+.3f}% per trade, "
              f"Sharpe {c['sharpe']:+.2f} -> {sign}")
    print()
    print("The ledger decision follows from Q3, not Q1. A persistence statistic that")
    print("no position can harvest is a fact about the data, not a product.")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--snapshot", default=DEFAULT_SNAPSHOT,
                    help="pinned universe snapshot (.npz); preferred over the DB")
    ap.add_argument("--write-snapshot", action="store_true",
                    help="freeze the DB universe to --snapshot, print its hash, exit")
    ap.add_argument("--from-db", action="store_true",
                    help="read the live DB even when the snapshot exists")
    ap.add_argument("--no-verify", action="store_true",
                    help="skip the pinned-hash check on the snapshot")
    ap.add_argument("--cost-bps", type=float, default=None,
                    help="single cost level; default sweeps 0/2.5/5/10")
    ap.add_argument("--quick", action="store_true", help="4 sectors only")
    ap.add_argument("--json", help="write machine-readable results here")
    args = ap.parse_args()

    if args.write_snapshot:
        print("loading survivorship-clean universe from DB...", file=sys.stderr)
        dates, close, dvol, syms, sectors = load_universe(args.db)
        digest = write_snapshot(args.snapshot, dates, close, dvol, syms, sectors)
        print(f"wrote {args.snapshot}")
        print(f"  {close.shape[1]} symbols, {close.shape[0]} sessions, "
              f"{os.path.getsize(args.snapshot) / 1e6:.1f} MB")
        print(f"  content sha256 = {digest}")
        print("Pin this hash as SNAPSHOT_SHA256 in this file.")
        print("Do NOT commit the .npz: it is license-classified and repro/.gitignore refuses it.")
        return

    if not args.from_db and os.path.exists(args.snapshot):
        print(f"loading pinned universe snapshot {args.snapshot}...", file=sys.stderr)
        dates, close, dvol, syms, sectors, digest = load_snapshot(
            args.snapshot, verify=not args.no_verify)
        print(f"  snapshot content sha256 = {digest}", file=sys.stderr)
    else:
        print("loading survivorship-clean universe from DB (unpinned — the live "
              "DB keeps growing; published numbers come from the snapshot)...",
              file=sys.stderr)
        dates, close, dvol, syms, sectors = load_universe(args.db)
    if args.quick:
        close, dvol, syms, sectors = quick_filter(close, dvol, syms, sectors)
    print(f"  {close.shape[1]} symbols with SIC sectors, {close.shape[0]} sessions "
          f"({datetime.datetime.utcfromtimestamp(int(dates[0])).date()} to "
          f"{datetime.datetime.utcfromtimestamp(int(dates[-1])).date()})", file=sys.stderr)
    with np.errstate(invalid="ignore", divide="ignore"):
        logc = np.log(close)

    costs = [args.cost_bps] if args.cost_bps is not None else [0.0, 2.5, 5.0, 10.0]
    stats_by_cost, h018 = {}, None
    for i, cb in enumerate(costs):
        print(f"walk-forward @ {cb} bps/side...", file=sys.stderr)
        res, pers, cpers, blocks = run(close, dvol, logc, dates, sectors, cb,
                                       verbose=(i == 0))
        stats_by_cost[cb] = report(res, pers, cpers, blocks, cb)
        if i == 0:
            h018 = report_h018(pers, cpers)

    verdict(stats_by_cost, h018)

    if args.json:
        with open(args.json, "w", encoding="utf-8") as f:
            json.dump({"h018": h018,
                       "by_cost": {str(k): v for k, v in stats_by_cost.items()}},
                      f, indent=2, default=str)
        print(f"\nwrote {args.json}")


if __name__ == "__main__":
    main()

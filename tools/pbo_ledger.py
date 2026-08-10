#!/usr/bin/env python3
"""Probability of Backtest Overfitting for SignalDeck's 48 research rules.

CSCV (Combinatorially Symmetric Cross-Validation) after Bailey, Borwein,
Lopez de Prado & Zhu (2014), "The Probability of Backtest Overfitting".

Bonferroni, Hansen SPA and Romano-Wolf StepM all grade INDIVIDUAL RULES: given
this rule's returns, is its edge distinguishable from luck? PBO grades the
SELECTION PROCEDURE: when you pick the best of 48 on one slice of history, how
often does that pick land below the median on the slice you did not look at?
A PBO near 0.5 means the ranking carries no out-of-sample information at all --
the search is manufacturing winners, not finding them.

WHICH STATISTIC MATTERS. The live loop (internal/pipeline/researchloop.go)
selects on a Wilson lower bound of weekly hit rate, so the "wilson" run is the
one that audits the procedure actually in use; "sharpe" is the literature's
default and is reported for comparability. They can disagree sharply -- the
SPA study already found the Wilson ranking inverts the return ranking.

Reads the panel from spa_ledger.build_panel() (the same corpus the Go loop
grades against). READ-ONLY: writes tools/pbo_ledger_result.json and nothing else.

    python tools/pbo_ledger.py [--dedupe-mirrors] [--blocks N]
"""

import itertools
import json
import math
import os
import sys

import numpy as np
import scipy.stats

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
OUT = os.path.join(HERE, "pbo_ledger_result.json")

BLOCKS = 16  # S; C(16,8) = 12,870 train/test splits
SWEEP = (8, 12, 16)  # PBO is unstable in S -- report the sweep, not one number
Z = 1.96


# --------------------------------------------------------------- statistics
# Each takes a (rows, N) block and returns (N,). All results are finite.

def sharpe(block):
    """Per-column mean / sd (ddof=1). Not annualised. Zero-variance -> 0.0."""
    block = np.asarray(block, dtype=float)
    if block.shape[0] < 2:
        return np.zeros(block.shape[1])
    sd = block.std(axis=0, ddof=1)
    out = np.zeros(block.shape[1])
    ok = np.isfinite(sd) & (sd > 0)
    np.divide(block.mean(axis=0), sd, out=out, where=ok)
    return out


def mean_return(block):
    """Per-column arithmetic mean."""
    return np.asarray(block, dtype=float).mean(axis=0)


def wilson_stat(block):
    """Per-column Wilson lower bound on P(return > 0).

    A 0.0 entry means the rule did not fire that week (build_panel fills
    non-firing weeks with 0.0) -- that is out-of-market, NOT a loss, so it is
    excluded from the denominator. A column that never fired scores 0.0.
    """
    block = np.asarray(block, dtype=float)
    n = np.count_nonzero(block, axis=0).astype(float)
    wins = (block > 0).sum(axis=0).astype(float)
    return _wilson(wins, n)


def _wilson(wins, n, z=Z):
    """Vectorised lower limit of the Wilson score interval."""
    n = np.asarray(n, dtype=float)
    wins = np.asarray(wins, dtype=float)
    out = np.zeros(n.shape, dtype=float)
    ok = n > 0
    if not np.any(ok):
        return out
    nn, w = n[ok], wins[ok]
    p = w / nn
    denom = 1.0 + z * z / nn
    centre = (p + z * z / (2 * nn)) / denom
    margin = (z / denom) * np.sqrt(p * (1 - p) / nn + z * z / (4 * nn * nn))
    out[ok] = np.clip(centre - margin, 0.0, 1.0)
    return out


def wilson_lower_bound(wins, n, z=Z):
    """Scalar Wilson lower bound. wilson_lower_bound(50, 100) == 0.403830."""
    if n <= 0:
        return 0.0
    return float(_wilson(np.array([float(wins)]), np.array([float(n)]), z)[0])


# --------------------------------------------------------------------- CSCV

def cscv(M, stat, S=BLOCKS):
    """Combinatorially symmetric cross-validation.

    M is (T, N): T periods in ascending time order, N strategy configurations.
    Splits T into S equal contiguous blocks, then for every one of C(S, S/2)
    ways to choose half the blocks as training, picks the in-sample best
    configuration and records where it ranks out-of-sample on the complement.

    Deterministic -- combinations are enumerated, never sampled.
    """
    if S <= 0 or S % 2 != 0:
        raise ValueError(f"S must be a positive even number of blocks, got {S}")
    M = np.asarray(M, dtype=float)
    T, N = M.shape
    if T < S:
        raise ValueError(f"need at least S={S} rows, got {T}")

    n_trimmed = T % S
    per = (T - n_trimmed) // S
    # drop the OLDEST rows so the blocks divide evenly; keep the recent history
    blocks = M[n_trimmed:].reshape(S, per, N)

    combos = list(itertools.combinations(range(S), S // 2))
    n_comb = len(combos)

    logits = np.empty(n_comb)
    ranks = np.empty(n_comb)
    selected = np.empty(n_comb, dtype=int)
    is_perf = np.empty(n_comb)
    oos_perf = np.empty(n_comb)
    oos_return = np.empty(n_comb)

    index = np.arange(S)
    in_train = np.empty(S, dtype=bool)
    for i, combo in enumerate(combos):
        in_train[:] = False
        in_train[list(combo)] = True
        test = blocks[index[~in_train]].reshape(-1, N)
        is_stat = stat(blocks[index[in_train]].reshape(-1, N))
        oos_stat = stat(test)

        best = int(np.argmax(is_stat))
        # ascending mid-rank: 1 = worst out-of-sample, N = best
        w = float(scipy.stats.rankdata(oos_stat)[best])
        w_bar = w / (N + 1)  # keeps the logit finite for every possible rank

        logits[i] = math.log(w_bar / (1.0 - w_bar))
        ranks[i] = w_bar
        selected[i] = best
        is_perf[i] = is_stat[best]
        oos_perf[i] = oos_stat[best]
        # always a RETURN, never the selection statistic: a Wilson bound is
        # non-negative by construction, so P(loss) measured on it is always 0
        oos_return[i] = test[:, best].mean()

    if np.ptp(is_perf) > 0:
        slope, intercept = np.polyfit(is_perf, oos_perf, 1)
    else:
        slope, intercept = 0.0, float(oos_perf.mean())

    return {
        "pbo": float(np.mean(logits < 0)),
        "n_blocks": int(S),
        "n_combinations": int(n_comb),
        "n_trimmed": int(n_trimmed),
        "logits": logits,
        "ranks": ranks,
        "selected": selected,
        "is_perf": is_perf,
        "oos_perf": oos_perf,
        "oos_return": oos_return,
        "degradation_slope": float(slope),
        "degradation_intercept": float(intercept),
        "prob_loss": float(np.mean(oos_return < 0)),
    }


# --------------------------------------------------------------------- live

def drop_mirrors(M, ids):
    """Drop each rule that is the exact negation of an earlier one.

    The 48-rule grid is 24 exact mirror pairs (every follow_pressure rule has an
    inverse_pressure twin with correlation -1.0). Detected numerically rather
    than by parsing descr, so it cannot drift from the data.
    """
    keep = np.ones(M.shape[1], dtype=bool)
    dropped = []
    for i in range(M.shape[1]):
        if not keep[i]:
            continue
        for j in range(i + 1, M.shape[1]):
            if keep[j] and np.allclose(M[:, i], -M[:, j]):
                keep[j] = False
                dropped.append(ids[j])
    return M[:, keep], [c for c, k in zip(ids, keep) if k], dropped


def main():
    from spa_ledger import build_panel

    S = BLOCKS
    if "--blocks" in sys.argv:
        S = int(sys.argv[sys.argv.index("--blocks") + 1])

    R, meta = build_panel()
    ids = [str(c) for c in R.columns]
    M = R.to_numpy(dtype=float)

    dropped = []
    deduped = "--dedupe-mirrors" in sys.argv
    if deduped:
        M, ids, dropped = drop_mirrors(M, ids)

    by_id = {m["id"]: m for m in meta}
    results, shape = {}, None
    for name, fn in (("sharpe", sharpe), ("mean_return", mean_return), ("wilson", wilson_stat)):
        r = cscv(M, fn, S)
        shape = shape or r
        counts = np.bincount(r["selected"], minlength=M.shape[1])
        top = [[ids[k], int(counts[k])] for k in np.argsort(-counts)[:5] if counts[k] > 0]
        results[name] = {
            "pbo": r["pbo"],
            "prob_loss": r["prob_loss"],
            "degradation_slope": r["degradation_slope"],
            "degradation_intercept": r["degradation_intercept"],
            "median_logit": float(np.median(r["logits"])),
            "most_selected": top,
            # A single PBO is not a claim: on a pure-noise null its across-panel sd
            # is ~0.2 (measured in test_pbo.py), because the C(S,S/2) splits share
            # blocks and carry far less information than their count suggests.
            # Read the sweep, not the third decimal.
            "pbo_by_blocks": {str(s): (r["pbo"] if s == S else cscv(M, fn, s)["pbo"])
                              for s in SWEEP},
        }

    report = {
        "panel": {
            "n_weeks": int(M.shape[0]),
            "n_rules": int(M.shape[1]),
            "n_trimmed": shape["n_trimmed"],
            "n_blocks": shape["n_blocks"],
            "n_combinations": shape["n_combinations"],
            "deduped_mirrors": deduped,
            "dropped": dropped,
        },
        "results": results,
    }
    with open(OUT, "w") as fh:
        json.dump(report, fh, indent=2)

    p = report["panel"]
    print(f"panel: {p['n_weeks']} weeks x {p['n_rules']} rules, "
          f"{p['n_blocks']} blocks of {(p['n_weeks'] - p['n_trimmed']) // p['n_blocks']} weeks "
          f"({p['n_trimmed']} oldest trimmed), {p['n_combinations']:,} splits")
    if deduped:
        print(f"        mirrors deduped: dropped {len(dropped)} exact negations")
    sweep_hdr = "".join(f"{'PBO S=' + str(s):>10}" for s in SWEEP)
    print(f"\n{'statistic':<14}{'P(loss)':>10}{'degradation':>13}{'median logit':>14}{sweep_hdr}")
    for name, r in results.items():
        cells = "".join(f"{r['pbo_by_blocks'][str(s)]:>10.3f}" for s in SWEEP)
        print(f"{name:<14}{r['prob_loss']:>10.3f}{r['degradation_slope']:>13.3f}"
              f"{r['median_logit']:>14.3f}{cells}")
    print("  (PBO's across-panel sd under a pure-noise null is ~0.2 -- read the sweep, "
          "not the third decimal)")

    for name, r in results.items():
        print(f"\nmost-selected under {name}:")
        for rid, count in r["most_selected"]:
            wl = by_id.get(rid, {}).get("ledger_wilson_lower")
            wl = f"{wl:.4f}" if isinstance(wl, float) else "n/a"
            print(f"  {rid:<14}{count:>6} / {p['n_combinations']}  ledger_wilson_lower={wl}")


if __name__ == "__main__":
    main()

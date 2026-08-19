"""Is the daily IC distinguishable from zero? Three dependence-aware methods.

The forensic claim (t=1.38 at h=21, t=0.56 at h=42) came from ONE estimator: collapse
the daily IC series into n/h non-overlapping blocks and t-test the block means. A
headline scientific result should not rest on a single dependence assumption, so this
re-derives it three independent ways and reports the sensitivity.

Why dependence correction is mandatory here: an h-day forward return means day t and
day t+1 share h-1 days of the same outcome window. Consecutive ICs are mechanically
autocorrelated, so a naive t over 1,025 days overstates precision by roughly sqrt(h).

METHODS
  A  non-overlapping blocks   average IC within consecutive h-day blocks, t over blocks.
                              Fewest assumptions; discards within-block information.
  B  moving-block bootstrap   resample contiguous blocks of length L with replacement to
                              rebuild a series of the same length; percentile interval.
                              Keeps local dependence without assuming a parametric form.
  C  Newey-West HAC           t = mean / HAC-SE, Bartlett kernel, lag = h-1, the smallest
                              lag that spans the overlap the labels actually create.

All three answer the same question. Agreement across them is the evidence; a result
that survives only one is an artifact of that estimator.

SEARCH PERIOD ONLY. The 2025-03-01 holdout is sealed and is never read here.

    python ic_inference.py            # both horizons, all methods, sensitivity
"""
import numpy as np
import pandas as pd

SPLIT = pd.Timestamp("2025-03-01")
SEED = 0
N_BOOT = 5000


def daily_ic(h):
    """Spearman(prob, relative-outcome) per day, search period only."""
    p = pd.read_parquet(f"preds_{h}_extremes10_ens_lh.parquet")
    p = p[p["day"] < SPLIT].dropna(subset=["y"])
    ic = p.groupby("day").apply(
        lambda g: g["prob"].corr(g["y"], method="spearman") if len(g) > 30 else np.nan,
        include_groups=False).dropna()
    return ic.sort_index()


def method_a_blocks(ic, h):
    """Non-overlapping block means, t over blocks."""
    v = ic.to_numpy()
    bid = np.arange(len(v)) // h
    means = np.array([v[bid == b].mean() for b in np.unique(bid)])
    n = len(means)
    if n < 2:
        return dict(t=np.nan, lo=np.nan, hi=np.nan, n=n)
    se = means.std(ddof=1) / np.sqrt(n)
    t = means.mean() / se if se > 0 else np.nan
    # Student-t critical value without scipy: normal approx is optimistic at n~24,
    # so use a small lookup for the 97.5th percentile at the df we actually have.
    crit = {2: 4.30, 3: 3.18, 5: 2.57, 10: 2.23, 20: 2.09, 30: 2.04, 50: 2.01}
    df = n - 1
    tc = next(c for k, c in sorted(crit.items()) if df <= k) if df <= 50 else 1.96
    return dict(t=t, lo=means.mean() - tc * se, hi=means.mean() + tc * se, n=n)


def method_b_moving_block(ic, L, n_boot=N_BOOT, seed=SEED):
    """Moving-block bootstrap: resample contiguous length-L blocks with replacement."""
    v = ic.to_numpy()
    n = len(v)
    if n < 2 * L:
        return dict(lo=np.nan, hi=np.nan, L=L, blocks=0)
    starts_max = n - L
    k = int(np.ceil(n / L))
    rng = np.random.default_rng(seed)
    idx = rng.integers(0, starts_max + 1, size=(n_boot, k))
    offs = np.arange(L)
    draws = v[(idx[:, :, None] + offs[None, None, :]).reshape(n_boot, -1)[:, :n]].mean(1)
    lo, hi = np.percentile(draws, [2.5, 97.5])
    return dict(lo=lo, hi=hi, L=L, blocks=k)


def method_c_hac(ic, lag):
    """Newey-West HAC standard error, Bartlett kernel."""
    v = ic.to_numpy()
    n = len(v)
    d = v - v.mean()
    g0 = (d @ d) / n
    s = g0
    for L in range(1, lag + 1):
        if L >= n:
            break
        s += 2.0 * (1.0 - L / (lag + 1.0)) * (d[L:] @ d[:-L]) / n
    se = np.sqrt(s / n) if s > 0 else np.nan
    t = v.mean() / se if se and se > 0 else np.nan
    return dict(t=t, se=se, lo=v.mean() - 1.96 * se, hi=v.mean() + 1.96 * se, lag=lag)


def report(h):
    ic = daily_ic(h)
    m = ic.mean()
    print(f"\n{'='*74}\nh={h}   days={len(ic)}   mean IC={m:+.5f}   sd={ic.std(ddof=1):.4f}")
    print(f"{'-'*74}")

    a = method_a_blocks(ic, h)
    print(f"  A  non-overlapping blocks (L={h})   n={a['n']:>3}  "
          f"t={a['t']:+.2f}   95% CI [{a['lo']:+.4f}, {a['hi']:+.4f}]")

    b = method_b_moving_block(ic, h)
    print(f"  B  moving-block bootstrap (L={h})   {N_BOOT} draws, seed={SEED}      "
          f"95% CI [{b['lo']:+.4f}, {b['hi']:+.4f}]")

    c = method_c_hac(ic, h - 1)
    print(f"  C  Newey-West HAC (lag={c['lag']})        "
          f"t={c['t']:+.2f}   95% CI [{c['lo']:+.4f}, {c['hi']:+.4f}]")

    print(f"\n  block-length sensitivity (method B):")
    for L in (max(2, h // 2), h, 2 * h, 3 * h):
        s = method_b_moving_block(ic, L)
        if np.isnan(s["lo"]):
            continue
        excl = "EXCLUDES 0" if (s["lo"] > 0 or s["hi"] < 0) else "contains 0"
        print(f"      L={L:>3}  95% CI [{s['lo']:+.4f}, {s['hi']:+.4f}]  {excl}")

    verdicts = [
        (a["lo"] > 0 or a["hi"] < 0),
        (b["lo"] > 0 or b["hi"] < 0),
        (c["lo"] > 0 or c["hi"] < 0),
    ]
    print(f"\n  METHODS EXCLUDING ZERO: {sum(verdicts)}/3 -> "
          f"{'RESOLVED' if all(verdicts) else 'UNRESOLVED'}")
    return dict(h=h, days=len(ic), mean=m, a=a, b=b, c=c, agree=sum(verdicts))


if __name__ == "__main__":
    out = [report(h) for h in (21, 42)]
    print(f"\n{'='*74}")
    for r in out:
        print(f"h={r['h']:>3}  mean IC {r['mean']:+.5f}  methods excluding zero: "
              f"{r['agree']}/3")

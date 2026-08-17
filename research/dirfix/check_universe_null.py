"""A label's baseline must come from the SAME universe the label is graded on.

`make_labels(..., "relative")` defines y as "beat the day's CROSS-SECTIONAL MEDIAN",
and that median is taken over the FULL PANEL -- every symbol with a forward return
that day, ~2,750 names. But predictions are only ever made on the STRICT universe
(close >= $5, 21-day dollar volume >= $10M, 252 days of history), ~634 names.

Liquid names are not a random half of the panel. They beat the broad median on their
own, so the honest null for a STRICT-universe row is NOT 0.5 -- it is the STRICT
universe's own rate of beating the panel median. Graded against 0.5, every decile of
the model looks positive and the short leg looks impossible, which is the exact
opposite of the truth.

This is not hypothetical: it produced a confident wrong reading of the decile response
curve on 2026-08-16 before the baseline was corrected.

    python check_universe_null.py
"""
import sys

import pandas as pd

SPLIT = pd.Timestamp("2025-03-01")
NAIVE_NULL = 0.5
TOL = 0.01          # "the naive null is fine" would mean within a point of 0.5

failures = []

for h in (21, 42):
    p = pd.read_parquet(f"preds_{h}_extremes10_ens_lh.parquet")
    p = p[p["day"] < SPLIT].dropna(subset=["y"])
    strict_null = float(p["y"].mean())
    gap = strict_null - NAIVE_NULL

    print(f"\n=== h={h} ===  {len(p):,} STRICT-universe rows over {p['day'].nunique()} days")
    print(f"  P(STRICT row beats the FULL-PANEL median) = {strict_null:.4f}")
    print(f"  naive null                                 = {NAIVE_NULL:.4f}")
    print(f"  gap                                        = {gap:+.4f}")

    # 1. The mismatch must be real and material, else this guard is theatre.
    if abs(gap) <= TOL:
        failures.append(f"h={h}: STRICT null {strict_null:.4f} is within {TOL} of 0.5 -- "
                        "either the universe filter stopped biting or the label changed; "
                        "re-derive before trusting any skill number")
    else:
        print(f"  -> grading these rows against {NAIVE_NULL} would MISSTATE skill by "
              f"{gap:+.2%} before the model does anything")

    # 2. The bias must be stable, not an artifact of one stretch.
    q = p.groupby(pd.qcut(p["day"].rank(method="dense"), 4, labels=False))["y"].mean()
    print(f"  by quarter: " + "  ".join(f"{v:.4f}" for v in q))
    if not all(v > NAIVE_NULL + TOL for v in q):
        failures.append(f"h={h}: the STRICT-universe premium is not present in every "
                        f"sub-period ({list(round(v, 4) for v in q)}); a single constant "
                        "null cannot describe it")

    # 3. The premium must be a UNIVERSE effect, not a model effect: it must hold for
    #    rows the model ranks LOW just as much as rows it ranks high.
    lo = p[p["prob"] < p["prob"].quantile(0.25)]["y"].mean()
    hi = p[p["prob"] > p["prob"].quantile(0.75)]["y"].mean()
    print(f"  bottom-quartile-prob rows also beat the panel median {lo:.4f} of the time "
          f"(top quartile {hi:.4f})")
    if lo <= NAIVE_NULL:
        failures.append(f"h={h}: rows the model likes LEAST do not clear 0.5 ({lo:.4f}), "
                        "so the premium may be model skill rather than a universe effect")

print()
if failures:
    for f in failures:
        print("FAIL " + f)
    sys.exit(1)
print("OK  the STRICT universe carries a large, stable, model-independent premium over "
      "the full-panel median.\n    Any skill read against 0.5 is wrong by that premium; "
      "grade against the universe's own rate.")

"""Combine cross-sectional features into ONE score, and grade it out-of-sample.

WHY
Individual cross-sectional features rank (dollar_vol_21 t=+11.5, vol_21 t=-6.1,
mom_252_21 t=+5.7, rev_1 t=+5.6 over 1,883 days, all surviving Bonferroni).
The ensemble's combined probability does NOT (IC -0.0269, t = -1.23). So the
raw material carries ordering signal and the combination discards it.

This builds the combination the honest way and reports what it is worth.

DISCIPLINE
  * Weights are fitted on a TRAIN period and graded on a later TEST period that
    the fit never saw. An in-sample IC here would be meaningless — the features
    were selected by looking at the whole history.
  * An EMBARGO gap sits between train and test so the last training label does
    not overlap the first test feature.
  * IC is per-day Spearman, averaged across days, with the t-stat computed on
    the across-day standard error. ~1,000 names share one market move.
  * The naive equal-weight blend is reported beside the fitted one. A fit that
    cannot beat equal weights has not earned its parameters.

Read-only. Writes nothing to the database.
"""
import argparse
import json
import math

import numpy as np
import pandas as pd

from xsection import (DB, build_features, cross_sectional_rank, daily_ic,
                      load_panel, _norm_ppf)

# Sign-corrected feature set: every entry is stated so that HIGHER means
# "expected to outperform", using the direction measured in xsection.py.
# Signs are frozen here rather than re-fitted per period, so a sign that only
# works in-sample cannot quietly flip to rescue the score.
SIGNED = {
    "dollar_vol_21": +1,   # liquidity
    "vol_21": -1,          # low-volatility effect
    "mom_252_21": +1,      # 12-1 momentum
    "rev_1": +1,           # short-term reversal (already negated in build)
    "vol_surprise": +1,
    "mom_5": -1,
}


def stats(ic, label, n_tests=1):
    n = len(ic)
    if n < 20:
        return {"name": label, "days": n, "verdict": "INSUFFICIENT DAYS"}
    mean = float(ic.mean())
    se = float(ic.std(ddof=1) / math.sqrt(n))
    t = mean / se if se else 0.0
    crit = abs(_norm_ppf(1 - 0.025 / n_tests)) if n_tests > 1 else 1.96
    # Information ratio OF THE IC SERIES (mean IC / sd IC, annualised). This is
    # NOT a portfolio Sharpe and must never be quoted as one: it ignores costs,
    # capacity, and the fact that a rank ordering is not a position. Grinold's
    # fundamental law puts the achievable portfolio IR nearer IC*sqrt(breadth),
    # which for IC 0.05 over ~300 names is ~0.9 BEFORE costs. Labelling this
    # "ann IR" as though it were 6.1 would be off by most of an order of
    # magnitude in the flattering direction.
    ic_ir = (mean / float(ic.std(ddof=1)) * math.sqrt(252)) if ic.std(ddof=1) else 0.0
    breadth = 300.0
    implied_port_ir = mean * math.sqrt(breadth) * math.sqrt(252) / math.sqrt(252)
    return {"name": label, "days": n, "mean_ic": round(mean, 5),
            "t_stat": round(t, 2), "hit_days_pct": round(100 * float((ic > 0).mean()), 1),
            "ic_series_IR_annualised": round(ic_ir, 2),
            "implied_portfolio_IR_pre_cost": round(implied_port_ir, 2),
            "significant": abs(t) > crit,
            "crit_t": round(crit, 2)}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=DB)
    ap.add_argument("--horizon", type=int, default=1)
    ap.add_argument("--embargo", type=int, default=10, help="days between train and test")
    ap.add_argument("--out", default="xscore_result.json")
    args = ap.parse_args()

    close, vol, _ = load_panel(args.db)
    fwd = close.shift(-args.horizon) / close - 1
    raw = build_features(close, vol)

    # LEAKAGE-FREE SELECTION. The SIGNED map above was hand-picked by reading
    # full-history ICs from xsection.py -- which INCLUDES the test period. Using
    # it here would leak the answer into the feature choice even though the
    # weights are train-only. So every feature build_features produces is
    # carried forward, and both its SIGN and its WEIGHT come from the train
    # period alone. SIGNED is kept only as documentation of the prior.
    ranked = {name: cross_sectional_rank(f) for name, f in raw.items()}

    days = close.index
    cut = int(len(days) * 0.7)
    train_days = days[:cut]
    test_days = days[cut + args.embargo:]
    print(f"train {train_days.min().date()}..{train_days.max().date()} "
          f"({len(train_days)} days)   embargo {args.embargo}d   "
          f"test {test_days.min().date()}..{test_days.max().date()} ({len(test_days)} days)")

    # Fit weights = each feature's mean IC over the TRAIN period only. This is
    # the standard IC-weighting scheme: features that ranked better in-sample
    # get more of the composite, and nothing about the test period informs it.
    weights = {}
    print(f"\n{'feature':<16} {'train IC':>10} {'weight':>9}")
    print("-" * 38)
    for name, f in ranked.items():
        ic_tr = daily_ic(f.loc[train_days], fwd.loc[train_days])
        weights[name] = float(ic_tr.mean()) if len(ic_tr) >= 20 else 0.0
    tot = sum(abs(w) for w in weights.values()) or 1.0
    for name in ranked:
        weights[name] /= tot
        print(f"{name:<16} {weights[name] * tot:>+10.5f} {weights[name]:>+9.3f}")

    # Composite scores, both re-ranked cross-sectionally so the output is
    # itself a per-day ordering rather than an arbitrary scale.
    fitted = sum(ranked[n] * w for n, w in weights.items())
    equal = sum(ranked[n] for n in ranked) / len(ranked)
    fitted_r = cross_sectional_rank(fitted)
    equal_r = cross_sectional_rank(equal)

    out = []
    for label, series, dayset in [
        ("fitted composite  [TRAIN, in-sample]", fitted_r, train_days),
        ("equal-weight      [TRAIN, in-sample]", equal_r, train_days),
        ("fitted composite  [TEST, out-of-sample]", fitted_r, test_days),
        ("equal-weight      [TEST, out-of-sample]", equal_r, test_days),
    ]:
        idx = series.index.intersection(dayset)
        out.append(stats(daily_ic(series.loc[idx], fwd.loc[idx]), label))

    print(f"\n{'':<42} {'days':>5} {'mean IC':>9} {'t':>7} {'>0%':>6} {'IC-IR':>7} {'portIR':>7}")
    print("-" * 82)
    for r in out:
        if "mean_ic" not in r:
            print(f"{r['name']:<42} {r['days']:>5}  {r['verdict']}")
            continue
        print(f"{r['name']:<42} {r['days']:>5} {r['mean_ic']:>+9.5f} "
              f"{r['t_stat']:>+7.2f} {r['hit_days_pct']:>5.1f}% "
              f"{r['ic_series_IR_annualised']:>7.2f} {r['implied_portfolio_IR_pre_cost']:>7.2f}")

    with open(args.out, "w") as fh:
        json.dump({"horizon_days": args.horizon, "embargo_days": args.embargo,
                   "weights": weights, "results": out}, fh, indent=2)
    print(f"\nwrote {args.out}")

    oos = next(r for r in out if r["name"].startswith("fitted") and "TEST" in r["name"])
    print()
    if oos.get("significant") and oos.get("mean_ic", 0) > 0:
        print(f"VERDICT: the composite ranking carries out-of-sample signal "
              f"(IC {oos['mean_ic']:+.5f}, t {oos['t_stat']:+.2f}).")
        print("Beta calibration will now preserve an ordering that is worth preserving.")
    else:
        print("VERDICT: the composite does NOT rank out-of-sample. The features "
              "rank individually; this combination does not carry that through.")


if __name__ == "__main__":
    main()

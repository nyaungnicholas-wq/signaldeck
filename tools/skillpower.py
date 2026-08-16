"""Refuse a skill verdict that the DAY COUNT cannot support.

The registry publishes `directional-ensemble (1d)` as "FAILED - significantly worse
than the naive baseline" at -13.0pp. That number comes from 2,479 graded rows which
are only 16 distinct days, and the null it is compared against is estimated on those
same 16 days.

Two things break if you count rows:

1. Rows within a day are NOT independent. One market move drives the whole
   cross-section, so 155 rows on one day carry roughly one day of evidence, not 155.
2. The null is a random variable too. Comparing accuracy to it as if it were known
   exactly ignores half the sampling error and manufactures significance.

So resample whole DAYS and test the paired difference (hits - null_hits). If the
interval contains zero, the verdict is withheld - in EITHER direction. A guard that
can only refuse good news is not a guard, it is a thumb on the scale.
"""
from __future__ import annotations

import random

__all__ = ["skill_resolvable", "verdict_supported_by_intervals"]


def verdict_supported_by_intervals(acc, acc_ci, null_acc, null_ci):
    """Does a skill verdict survive once the NULL's own interval is honored?

    accuracy_registry.py is rigorous about the model's interval: day-clustered
    Wilson, a measured design effect, an effective_n far below the row count, and a
    multiplicity-corrected z. It then computes an interval for the null as well and
    publishes it as `null_ci` -- and `verdict_for` compares against `null_acc`, the
    null's POINT estimate, ignoring the interval it just computed:

        if hi < null_acc - VERDICT_EPS: return "FAILED ..."

    For directional-ensemble (1d) that is 0.539 < 0.564 -> FAILED, while the two
    published intervals are acc [0.335, 0.539] and null [0.386, 0.727]. They overlap
    across [0.386, 0.539]. A difference whose two sides overlap that heavily is not
    something the sample can resolve in either direction.

    Returns {supported, overlap_lo, overlap_hi, reason}. This works entirely from the
    row's OWN published numbers, so it always describes the same population, universe
    and null the verdict was read off -- no re-derivation, nothing to drift.

    Non-overlap is sufficient for a difference, not necessary: two overlapping
    intervals can still differ significantly under a paired test, because acc and
    null share rows and are positively correlated. So this refuses a verdict it
    cannot support and never asserts one -- it can only ever withhold.
    """
    if None in (acc, null_acc) or not acc_ci or not null_ci:
        return {"supported": None, "overlap_lo": None, "overlap_hi": None,
                "reason": "no published interval for accuracy and/or the null"}
    a_lo, a_hi = float(acc_ci[0]), float(acc_ci[1])
    n_lo, n_hi = float(null_ci[0]), float(null_ci[1])
    lo, hi = max(a_lo, n_lo), min(a_hi, n_hi)
    if lo > hi:
        return {"supported": True, "overlap_lo": None, "overlap_hi": None, "reason": ""}
    return {
        "supported": False, "overlap_lo": lo, "overlap_hi": hi,
        "reason": (f"accuracy {acc:.4f} [{a_lo:.4f}, {a_hi:.4f}] and its null "
                   f"{null_acc:.4f} [{n_lo:.4f}, {n_hi:.4f}] OVERLAP across "
                   f"[{lo:.4f}, {hi:.4f}]: the verdict compares the accuracy interval "
                   f"to the null's point estimate and ignores the null's own published "
                   f"interval, so the stated skill of {acc - null_acc:+.4f} is not "
                   f"resolved by this sample"),
    }


def _percentile(sorted_vals, q):
    """Linear-interpolated percentile of an already-sorted list. q in [0, 100]."""
    if not sorted_vals:
        return 0.0
    if len(sorted_vals) == 1:
        return float(sorted_vals[0])
    pos = (q / 100.0) * (len(sorted_vals) - 1)
    lo = int(pos)
    hi = min(lo + 1, len(sorted_vals) - 1)
    frac = pos - lo
    return float(sorted_vals[lo] * (1.0 - frac) + sorted_vals[hi] * frac)


def _refuse(days, reason):
    return {"resolvable": False, "skill": 0.0, "ci_lo": 0.0, "ci_hi": 0.0,
            "days": days, "reason": reason}


def skill_resolvable(day_tallies, n_boot=2000, seed=0):
    """Is (accuracy - null) distinguishable from zero, blocking by day?

    day_tallies: sequence of (n, hits, null_hits), one tuple per DAY.
    Returns {resolvable, skill, ci_lo, ci_hi, days, reason}. Deterministic.
    """
    tallies = [(int(n), float(h), float(nh)) for n, h, nh in day_tallies]
    nd = len(tallies)
    if nd == 0:
        return _refuse(0, "no graded days: nothing to judge")
    if nd < 2:
        return _refuse(nd, "1 day of evidence can never support a significance verdict")
    total_n = sum(t[0] for t in tallies)
    if total_n <= 0:
        return _refuse(nd, f"0 graded rows over {nd} day(s): nothing to judge")

    skill = (sum(t[1] for t in tallies) - sum(t[2] for t in tallies)) / total_n

    rng = random.Random(seed)
    draws = []
    for _ in range(n_boot):
        picked = [tallies[rng.randrange(nd)] for _ in range(nd)]
        dn = sum(t[0] for t in picked)
        if dn <= 0:                      # all-empty resample; contributes nothing
            continue
        draws.append((sum(t[1] for t in picked) - sum(t[2] for t in picked)) / dn)
    if not draws:
        return _refuse(nd, f"no non-empty resample over {nd} day(s): nothing to judge")

    draws.sort()
    ci_lo, ci_hi = _percentile(draws, 2.5), _percentile(draws, 97.5)
    if ci_lo > 0 or ci_hi < 0:
        return {"resolvable": True, "skill": skill, "ci_lo": ci_lo, "ci_hi": ci_hi,
                "days": nd, "reason": ""}
    return {"resolvable": False, "skill": skill, "ci_lo": ci_lo, "ci_hi": ci_hi,
            "days": nd,
            "reason": (f"skill {skill:+.4f} over {nd} day(s) has a 95% day-blocked "
                       f"interval [{ci_lo:+.4f}, {ci_hi:+.4f}] that contains zero: "
                       f"the day count cannot support a significance verdict")}

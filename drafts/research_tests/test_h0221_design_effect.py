# TARGETS: new file -> drafts/research_tests/test_h0221_design_effect.py
# APPLY:   git apply round2-drafts/patches/h0221-design-effect-fix.patch
#          (from the repo root; this file ships with the h0221.py fix it guards)
# RUN:     .venv/Scripts/python.exe -m pytest drafts/research_tests/test_h0221_design_effect.py -q
#
# WHAT THIS GUARDS
#   research/eighty/h0221.py:compute_design_effect used an ANOVA/ICC form whose
#   within-cluster sum of squares is exactly 0 when every month is unanimous, so
#   `if wss == 0: return 1.0` handed the MOST pseudoreplicated sample possible
#   the MINIMAL correction. The fix mirrors tools/accuracy_registry.design_effect
#   instead of inventing a third estimator, and test_matches_the_grader below is
#   what keeps "mirror" true rather than aspirational.
#
#   These tests import nothing from research/ directly -- the `hyp` fixture in
#   conftest.py execs only the file's defs, so the function under test is
#   byte-for-byte the one in research/eighty/h0221.py.

import datetime as dt

import pytest

# conftest.py puts tools/ on sys.path.
from accuracy_registry import design_effect as grader_design_effect


def _calls(months, per_month, hits_per_month):
    """One call per (month, day). hits_per_month(m) -> how many of that month's
    calls are hits, so pooled p and cluster sizes are both exactly controlled."""
    out = []
    for m in range(1, months + 1):
        h = hits_per_month(m)
        for d in range(1, per_month + 1):
            out.append({"date": dt.date(2024, m, d), "hit": 1 if d <= h else 0})
    return out


def _agreement_ladder(agree, months=12, per_month=20):
    """Pooled p pinned at 0.5, within-month agreement = `agree`. Half the months
    lean up, half lean down, so only the INTRA-cluster correlation varies."""
    hi = round(per_month * agree)
    return _calls(months, per_month, lambda m: hi if m % 2 else per_month - hi)


def test_monotone_non_decreasing_in_intra_cluster_correlation(hyp):
    """The whole job of a design effect: rise as calls inside a cluster stop
    being independent. The old estimator peaked at 90% agreement and COLLAPSED
    to 1.0 at 100% -- maximal clustering, minimal correction."""
    f = hyp("h0221").compute_design_effect
    ladder = [0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 1.0]
    deffs = [f(_agreement_ladder(a)) for a in ladder]
    for (a0, d0), (a1, d1) in zip(zip(ladder, deffs), zip(ladder[1:], deffs[1:])):
        assert d1 >= d0 - 1e-9, (
            f"deff fell from {d0:.4f} at {a0:.0%} agreement to {d1:.4f} at "
            f"{a1:.0%}: more clustering must never buy a smaller correction. "
            f"ladder={list(zip(ladder, deffs))}")
    # and it must actually MOVE -- a constant 1.0 would pass monotonicity.
    assert deffs[-1] > 10 * deffs[0], deffs


def test_unanimous_clusters_score_about_the_mean_cluster_size(hyp):
    """20 calls a month, every month unanimous => each month carries the
    information of ~1 observation, so deff ~ 20, not 1."""
    f = hyp("h0221").compute_design_effect
    deff = f(_agreement_ladder(1.0, months=12, per_month=20))
    assert deff == pytest.approx(20 * 12 / 11, rel=1e-9), deff


def test_degenerate_all_same_label_is_the_worst_case_not_the_best(hyp):
    """p == 0 or p == 1 leaves no between-cluster variance to measure, but it is
    the most perfectly clustered sample there is. The honest reading is one
    independent observation per cluster (n / k), not 1.0."""
    f = hyp("h0221").compute_design_effect
    for label in (0, 1):
        calls = _calls(12, 20, lambda m: 20 if label else 0)
        assert f(calls) == pytest.approx(240 / 12), label
    # single-call months are the one case where n / k really is 1.0
    assert f([{"date": dt.date(2024, m, 1), "hit": 1} for m in range(1, 13)]) == 1.0


def test_never_below_one(hyp, rng):
    """deff < 1 NARROWS the interval -- the failure mode that manufactures
    significance out of correlated data."""
    f = hyp("h0221").compute_design_effect
    for _ in range(300):
        calls = [{"date": dt.date(2024, int(rng.integers(1, 13)), int(rng.integers(1, 29))),
                  "hit": int(rng.integers(0, 2))}
                 for _ in range(int(rng.integers(0, 120)))]
        assert f(calls) >= 1.0


def test_matches_the_grader(hyp, rng):
    """The mirror claim, made executable: on the SAME (n, hits) tallies this
    function and tools/accuracy_registry.design_effect must return the same
    number. A registry verdict and a research verdict may not disagree about
    the same data."""
    f = hyp("h0221").compute_design_effect
    for _ in range(300):
        months = int(rng.integers(2, 13))
        sizes = [int(rng.integers(1, 29)) for _ in range(months)]  # <= 28: real calendar days
        hits = [int(rng.integers(0, s + 1)) for s in sizes]
        calls = []
        for i, (s, h) in enumerate(zip(sizes, hits), start=1):
            calls += [{"date": dt.date(2024, i, d + 1), "hit": 1 if d < h else 0}
                      for d in range(s)]
        expected = grader_design_effect(list(zip(sizes, hits)))
        assert expected is not None
        assert f(calls) == pytest.approx(expected, rel=1e-12, abs=1e-12)

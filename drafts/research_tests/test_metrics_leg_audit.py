# TARGETS: new file -> drafts/research_tests/test_metrics_leg_audit.py
# APPLY:   .venv/Scripts/python.exe -m pytest drafts/research_tests/ -q  (repo root)
#
# UNDER TEST: tools/leg_audit.py -- brier, log_loss, auc, day_clustered_stats,
# dedup_one_per_symbol_day, prequential_majority, leg_probability, admitted.
#
# SCOPE NOTE: research/eighty/*.py contains NO Brier, NO calibration and NO
# Wilson interval (verified: `grep -lic brier|calibrat|wilson research/eighty/*.py`
# returns nothing). The brief asks for Brier-bound and decomposition tests
# "chosen by how much each influences a published number" -- that math lives in
# tools/leg_audit.py, which grades every ensemble leg and prints the verdict
# table. So the probabilistic-metric tests point there. Nothing is modified.

import math

import pytest

import leg_audit as la  # on sys.path via conftest's TOOLS shim
from conftest import brier_decomposition, quantize, reliability_bins


# ---------------------------------------------------------------- brier bounds

def test_brier_is_bounded_in_unit_interval(rng):
    """BS in [0,1] for any p in [0,1], y in {0,1}. 2000 random draws."""
    for _ in range(2000):
        n = int(rng.integers(1, 40))
        ps = [float(x) for x in rng.uniform(0, 1, size=n)]
        ys = [int(x) for x in rng.integers(0, 2, size=n)]
        b = la.brier(ps, ys)
        assert 0.0 <= b <= 1.0, f"brier={b} outside [0,1] for {ps}/{ys}"


def test_brier_attains_both_bounds():
    """Not just 'inside the range' -- the extremes must be reachable.

    A function that returned a constant 0.5 would pass a bounds-only test.
    """
    assert la.brier([1.0, 0.0, 1.0], [1, 0, 1]) == 0.0
    assert la.brier([0.0, 1.0, 0.0], [1, 0, 1]) == 1.0
    assert la.brier([0.5] * 8, [1, 0] * 4) == pytest.approx(0.25)


def test_brier_matches_independent_reference(rng):
    """Cross-check against a second implementation written from the definition."""
    for _ in range(200):
        n = int(rng.integers(2, 60))
        ps = [float(x) for x in rng.uniform(0, 1, size=n)]
        ys = [int(x) for x in rng.integers(0, 2, size=n)]
        ref = sum((p - y) ** 2 for p, y in zip(ps, ys)) / n
        assert la.brier(ps, ys) == pytest.approx(ref, abs=1e-12)


def test_brier_murphy_decomposition_is_exact(calibrated_probs):
    """BS == reliability - resolution + uncertainty (Murphy 1973).

    This is the identity the whole 'is the leg calibrated or just lucky'
    argument rests on. If it does not hold, brier_skill is not interpretable.
    """
    ps, ys = calibrated_probs(20000)
    ps = quantize(ps, 0.05)  # exact identity requires finitely many forecasts
    d = brier_decomposition(ps, ys)
    lhs = la.brier(ps, ys)
    rhs = d["reliability"] - d["resolution"] + d["uncertainty"]
    assert lhs == pytest.approx(rhs, abs=1e-10), f"{lhs} != {rhs} ({d})"


def test_calibrated_forecaster_has_near_zero_reliability_term(calibrated_probs):
    """By construction P(y=1|p)=p, so the reliability (miscalibration) term ~0.

    Sanity anchor: if this drifts, the generator or the decomposition is wrong,
    and every calibration claim built on them is unsafe.
    """
    ps, ys = calibrated_probs(50000)
    d = brier_decomposition(quantize(ps, 0.05), ys)
    assert d["reliability"] < 0.001, d
    assert d["resolution"] > 0.05, d  # a real forecaster DOES resolve


def test_uncertainty_term_equals_base_rate_variance(calibrated_probs):
    ps, ys = calibrated_probs(5000)
    base = sum(ys) / len(ys)
    d = brier_decomposition(quantize(ps), ys)
    assert d["uncertainty"] == pytest.approx(base * (1 - base), abs=1e-12)


# ------------------------------------------------------- calibration curve

def test_calibration_bins_are_monotone(calibrated_probs):
    """Observed hit rate must rise with the forecast bin, for a calibrated model.

    Allows one inversion for sampling noise; a broken calibrator inverts many.
    """
    ps, ys = calibrated_probs(60000)
    curve = reliability_bins(ps, ys, nbins=10)
    assert len(curve) == 10
    rates = [obs for _, _, _, obs in curve]
    inversions = sum(1 for a, b in zip(rates, rates[1:]) if b < a - 1e-9)
    assert inversions == 0, f"non-monotone reliability curve: {rates}"
    # and it must track the diagonal, not merely be increasing
    for _, n, mean_p, obs in curve:
        assert abs(mean_p - obs) < 0.02, f"bin p={mean_p:.3f} obs={obs:.3f} n={n}"


def test_anti_calibrated_model_is_rejected_by_the_same_check(calibrated_probs):
    """Negative control: the monotonicity check must FAIL on a flipped model.

    Without this, the test above could be passing for the wrong reason.
    """
    ps, ys = calibrated_probs(20000)
    flipped = [1.0 - p for p in ps]
    rates = [obs for _, _, _, obs in reliability_bins(flipped, ys, nbins=10)]
    inversions = sum(1 for a, b in zip(rates, rates[1:]) if b < a - 1e-9)
    assert inversions >= 8, f"flipped model should be badly non-monotone: {rates}"


# ------------------------------------------------------------ skill controls

def test_shuffled_labels_destroy_brier_skill(calibrated_probs, rng):
    """Permutation control: break the p<->y link and NO positive skill may survive.

    brier_skill = 1 - BS/BS_ref, exactly as tools/leg_audit.py:238 computes it.

    Note the expected value is NOT zero. Once p is independent of y,
        E[BS] = E[(p-y)^2] = Var(p) + base(1-base)
    so skill -> -Var(p)/[base(1-base)]: a spread-out forecast that has lost its
    signal is strictly WORSE than the constant base rate, because it is still
    making confident calls. For p ~ U(0.02, 0.98) that is
    -(0.96^2/12)/0.25 = -0.3072. Asserting the analytic value (rather than
    "about zero") is what makes this test able to fail for a real reason.
    """
    ps, ys = calibrated_probs(20000)
    base = sum(ys) / len(ys)
    ref = la.brier([base] * len(ys), ys)

    real_skill = 1 - la.brier(ps, ys) / ref
    assert real_skill > 0.15, real_skill  # the honest signal is genuinely there

    var_p = sum((p - sum(ps) / len(ps)) ** 2 for p in ps) / len(ps)
    predicted = -var_p / (base * (1 - base))

    skills = []
    for _ in range(30):
        shuffled = list(ys)
        rng.shuffle(shuffled)  # a permutation preserves the base rate exactly
        skills.append(1 - la.brier(ps, shuffled) / la.brier([base] * len(ys), shuffled))
    mean_skill = sum(skills) / len(skills)

    assert mean_skill == pytest.approx(predicted, abs=0.02), (
        f"shuffled skill {mean_skill:.4f} != analytic {predicted:.4f}")
    assert max(skills) < 0.0, f"a shuffled control showed POSITIVE skill: {max(skills)}"
    assert max(skills) < real_skill


def test_shuffled_labels_destroy_auc(calibrated_probs, rng):
    ps, ys = calibrated_probs(6000)
    assert la.auc(ps, ys) > 0.6
    aucs = []
    for _ in range(30):
        s = list(ys)
        rng.shuffle(s)
        aucs.append(la.auc(ps, s))
    assert abs(sum(aucs) / len(aucs) - 0.5) < 0.02


# -------------------------------------------------------------- auc symmetry

def test_auc_symmetry_under_joint_flip(rng):
    """AUC(1-p, 1-y) == AUC(p, y). Flipping both the score and the label
    relabels which class is 'positive' and must not change discrimination."""
    for _ in range(300):
        n = int(rng.integers(4, 50))
        ps = [float(x) for x in rng.uniform(0, 1, size=n)]
        ys = [int(x) for x in rng.integers(0, 2, size=n)]
        a = la.auc(ps, ys)
        b = la.auc([1 - p for p in ps], [1 - y for y in ys])
        if a is None:
            assert b is None
        else:
            assert a == pytest.approx(b, abs=1e-12)


def test_auc_of_reversed_score_is_one_minus(rng):
    for _ in range(300):
        n = int(rng.integers(4, 50))
        ps = [float(x) for x in rng.uniform(0, 1, size=n)]
        ys = [int(x) for x in rng.integers(0, 2, size=n)]
        a = la.auc(ps, ys)
        if a is not None:
            assert la.auc([-p for p in ps], ys) == pytest.approx(1 - a, abs=1e-12)


def test_auc_all_ties_is_exactly_half():
    assert la.auc([0.5] * 10, [1, 0] * 5) == 0.5


def test_auc_single_class_is_none_not_half():
    """'Undefined' must not be silently reported as 'measured chance'."""
    assert la.auc([0.1, 0.9, 0.4], [1, 1, 1]) is None
    assert la.auc([0.1, 0.9, 0.4], [0, 0, 0]) is None


# ------------------------------------------------------------- log loss

def test_log_loss_is_finite_at_the_boundaries():
    """eps-clipping must keep a confidently-wrong forecast finite, not inf."""
    v = la.log_loss([0.0], [1])
    assert math.isfinite(v) and v > 30
    assert math.isfinite(la.log_loss([1.0], [0]))


def test_log_loss_is_minimised_by_the_truth(rng):
    ps, ys = [], []
    for _ in range(4000):
        y = int(rng.integers(0, 2))
        ys.append(y)
        ps.append(0.9 if y else 0.1)
    truth = la.log_loss(ps, ys)
    assert truth < la.log_loss([0.5] * len(ys), ys)
    assert truth < la.log_loss([1 - p for p in ps], ys)


# ------------------------------------------------- empty / degenerate input

@pytest.mark.parametrize("fn", ["brier", "log_loss"])
def test_empty_input_is_nan_not_zero(fn):
    """Zero would read as a perfect score. NaN is the honest answer."""
    assert math.isnan(getattr(la, fn)([], []))


def test_brier_rejects_mismatched_lengths():
    """EDGE CASE: len(ps) != len(ys).

    brier() zips (silently truncating to the shorter list) but divides by
    len(ps). A truncated grade is therefore reported as an ARTIFICIALLY GOOD
    score rather than an error: 10 forecasts graded against 1 label returns
    0.025, which would read as near-perfect calibration.
    """
    with pytest.raises((ValueError, AssertionError, ZeroDivisionError)):
        la.brier([0.5] * 10, [1])


def test_day_clustered_stats_single_day_reports_no_interval():
    s = la.day_clustered_stats([(7, 10)])
    assert s["days"] == 1 and s["acc"] == 0.7
    assert s["se"] is None and s["lo"] is None and s["hi"] is None


def test_day_clustered_se_exceeds_naive_binomial_se():
    """The whole point of the day-clustered SE: it must be WIDER than the
    row-level binomial SE when rows inside a day are correlated. If it were not,
    every confidence interval in the leg audit would be too tight."""
    per_day = [(9, 10)] * 10 + [(1, 10)] * 10  # strong between-day dispersion
    s = la.day_clustered_stats(per_day)
    n = sum(t for _, t in per_day)
    naive = math.sqrt(s["acc"] * (1 - s["acc"]) / n)
    assert s["se"] > naive, f"clustered se {s['se']} <= naive {naive}"


def test_day_clustered_stats_all_correct_and_all_wrong():
    assert la.day_clustered_stats([(10, 10), (10, 10)])["acc"] == 1.0
    assert la.day_clustered_stats([(0, 10), (0, 10)])["acc"] == 0.0


# -------------------------------------------- look-ahead / as-of discipline

def test_prequential_majority_never_uses_the_current_day(rng):
    """LOOK-AHEAD GUARD (the bug class this repo has been burned by).

    The null baseline for day D must be computable from days < D only.
    Proof by perturbation: change the labels on the LAST day and every earlier
    day's baseline must be byte-identical. If any earlier value moves, the
    function peeked forward.
    """
    rows = [(EPOCH_DAY + d, int(rng.integers(0, 2)))
            for d in range(40) for _ in range(5)]
    base = la.prequential_majority(rows)

    last_day = max(d for d, _ in rows)
    mutated = [(d, (1 - up) if d == last_day else up) for d, up in rows]
    after = la.prequential_majority(mutated)

    for day in base:
        if day == last_day:
            continue
        assert base[day] == after[day], (
            f"day {day} baseline changed when only day {last_day} labels moved "
            f"-> look-ahead: {base[day]} != {after[day]}")


def test_prequential_majority_omits_the_first_day():
    """Day 1 has no prior; inventing a baseline for it is hindsight."""
    rows = [(1, 1), (1, 0), (2, 1), (2, 1)]
    out = la.prequential_majority(rows)
    assert 1 not in out
    assert 2 in out


EPOCH_DAY = 20000


# ------------------------------------------------------- independence rule

def test_dedup_keeps_exactly_one_row_per_symbol_day():
    rows = [{"symbol_id": 1, "day": 5, "ts": 300},
            {"symbol_id": 1, "day": 5, "ts": 100},
            {"symbol_id": 1, "day": 5, "ts": 200},
            {"symbol_id": 2, "day": 5, "ts": 50}]
    out = la.dedup_one_per_symbol_day(rows)
    assert len(out) == 2
    assert sorted((r["symbol_id"], r["ts"]) for r in out) == [(1, 100), (2, 50)]


def test_dedup_is_stable_under_input_order(rng):
    rows = [{"symbol_id": s, "day": d, "ts": s * 1000 + d * 10 + k}
            for s in range(4) for d in range(3) for k in range(3)]
    a = la.dedup_one_per_symbol_day(list(rows))
    shuffled = list(rows)
    rng.shuffle(shuffled)
    b = la.dedup_one_per_symbol_day(shuffled)
    key = lambda rs: sorted((r["symbol_id"], r["day"], r["ts"]) for r in rs)
    assert key(a) == key(b)


def test_dedup_survives_a_repeated_row_object():
    """EDGE CASE: the same dict appearing twice in the input.

    dedup_one_per_symbol_day filters by id(), so an aliased row is kept twice
    and the 'one observation per symbol-day' independence rule is violated --
    which inflates n and narrows every interval built on it.
    """
    r = {"symbol_id": 1, "day": 5, "ts": 100}
    out = la.dedup_one_per_symbol_day([r, r])
    assert len(out) == 1, (
        f"aliased row survived twice ({len(out)} rows) -- id()-based filtering "
        "double-counts a repeated row object")


# --------------------------------------------------- leg probability mapping

def test_leg_probability_is_clamped_to_unit_interval():
    for v in (-99.0, -1.0, 0.0, 1.0, 99.0):
        assert 0.0 <= la.leg_probability("pressure", {"PressureScore": v}) <= 1.0
        assert 0.0 <= la.leg_probability("sentiment", {"SentimentScore": v}) <= 1.0


def test_leg_probability_is_monotone_in_the_score(rng):
    for leg, field in (("pressure", "PressureScore"), ("sentiment", "SentimentScore")):
        xs = sorted(float(x) for x in rng.uniform(-3, 3, size=50))
        vals = [la.leg_probability(leg, {field: x}) for x in xs]
        assert all(b >= a - 1e-12 for a, b in zip(vals, vals[1:])), (leg, vals)


def test_leg_probability_neutral_score_maps_to_half():
    assert la.leg_probability("pressure", {"PressureScore": 0.0}) == pytest.approx(0.5)
    assert la.leg_probability("sentiment", {"SentimentScore": 0.0}) == pytest.approx(0.5)


def test_absent_leg_is_none_not_imputed_to_half():
    """A leg that said nothing and a leg that said 'coin flip' are different."""
    assert la.leg_probability("pressure", {}) is None
    assert la.leg_lift("pressure", {}) is None


def test_admitted_requires_strictly_positive_lift():
    assert la.admitted(0.001) is True
    assert la.admitted(0.0) is False
    assert la.admitted(-0.5) is False
    assert la.admitted(None) is False


def test_pearson_zero_variance_is_none_not_zero():
    assert la.pearson([1.0, 1.0, 1.0], [1.0, 2.0, 3.0]) is None
    assert la.pearson([1.0], [2.0]) is None
    assert la.pearson([1.0, 2.0, 3.0], [2.0, 4.0, 6.0]) == pytest.approx(1.0)

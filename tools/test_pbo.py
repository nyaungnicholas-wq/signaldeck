#!/usr/bin/env python3
"""Spec + self-check for tools/pbo_ledger.py (CSCV / Probability of Backtest Overfitting).

Pure synthetic panels — never touches data/signaldeck.db, so this runs in ~seconds.
Runnable directly (python tools/test_pbo.py) or under pytest.

The three panels below are the whole point:
  null     -> PBO must sit at ~0.5 (selection on noise is a coin flip)
  skill    -> PBO must collapse to ~0 (the good strategy stays good out-of-sample)
  overfit  -> PBO must go high (the in-sample winner is the out-of-sample loser)
A rank-direction bug passes the null panel and fails skill/overfit. That asymmetry
is deliberate: the null alone has no teeth.
"""

import math
from itertools import combinations

import numpy as np

from pbo_ledger import cscv, mean_return, sharpe, wilson_lower_bound, wilson_stat

S = 16
NCOMB = math.comb(S, S // 2)  # 12870


# --------------------------------------------------------------------------- panels

def null_panel(seed=0, T=320, N=50):
    """No skill anywhere. Every column is iid noise."""
    rng = np.random.default_rng(seed)
    return rng.standard_normal((T, N)) * 0.01


def skill_panel(seed=1, T=320, N=50, good=7):
    """One column has a real, persistent edge (drift 2x the noise sd)."""
    rng = np.random.default_rng(seed)
    M = rng.standard_normal((T, N)) * 0.01
    M[:, good] += 0.02
    return M, good


def overfit_panel(seed=2, T=320, N=50):
    """Every column wins on its own random half of the blocks and loses on the rest.

    Column n pays +a on a randomly chosen S/2 blocks and -a on the complement, so
    whichever column looks best on a training half is, by construction, the column
    that does worst on that half's complement. Real in-sample separation, zero
    out-of-sample persistence -- overfitting in its purest form.

    Note the amplitude must NOT be what varies across columns: Sharpe is scale
    invariant, so a shared regime scaled per column gives every column an identical
    Sharpe and the selection degenerates to noise. The block pattern is what varies.
    """
    rng = np.random.default_rng(seed)
    rows = T - (T % S)
    per = rows // S
    M = np.empty((rows, N))
    for n in range(N):
        sign = np.full(S, -1.0)
        sign[rng.choice(S, size=S // 2, replace=False)] = 1.0
        M[:, n] = np.repeat(sign, per) * 0.01
    M += rng.standard_normal(M.shape) * 0.002
    return M


# --------------------------------------------------------------------------- tests

def test_shape_and_invariants():
    r = cscv(null_panel(), stat=sharpe, S=S)
    assert r["n_blocks"] == S
    assert r["n_combinations"] == NCOMB, r["n_combinations"]
    assert r["n_trimmed"] == 0
    for key in ("logits", "ranks", "selected", "is_perf", "oos_perf"):
        assert len(r[key]) == NCOMB, (key, len(r[key]))
    assert np.all(np.isfinite(r["logits"])), "logits must be finite (rank/(N+1) keeps them off 0 and 1)"
    assert np.all(r["ranks"] > 0) and np.all(r["ranks"] < 1), "relative ranks live strictly inside (0,1)"
    assert 0.0 <= r["pbo"] <= 1.0
    assert np.all(r["selected"] >= 0)


def test_trims_oldest_rows_to_equal_blocks():
    r = cscv(null_panel(T=342), stat=sharpe, S=S)
    assert r["n_trimmed"] == 342 % S == 6
    assert r["n_combinations"] == NCOMB


def test_deterministic():
    a = cscv(null_panel(), stat=sharpe, S=S)
    b = cscv(null_panel(), stat=sharpe, S=S)
    assert a["pbo"] == b["pbo"]
    assert np.array_equal(a["logits"], b["logits"]), "CSCV enumerates combinations; nothing here may be random"


def test_null_panel_is_a_coin_flip():
    # One panel is a terrible estimate. Measured over 12 noise panels: mean 0.444,
    # sd 0.207, range 0.09-0.76 -- the 12,870 splits share blocks, so they carry
    # nothing like 12,870 observations' worth of information. "No skill -> PBO 0.5"
    # is a claim about the CENTRE, so average over panels to test it.
    vals = [cscv(null_panel(seed=s), stat=sharpe, S=S)["pbo"] for s in range(8)]
    mean = sum(vals) / len(vals)
    assert 0.35 < mean < 0.65, (
        f"selection on pure noise should centre PBO at ~0.5, got {mean:.3f} from "
        + ", ".join(f"{v:.3f}" for v in vals)
    )


def test_skill_panel_collapses_pbo():
    M, good = skill_panel()
    r = cscv(M, stat=sharpe, S=S)
    assert r["pbo"] < 0.05, f"a genuine edge should give PBO ~0, got {r['pbo']}"
    assert (r["selected"] == good).mean() > 0.95, "the edge column should win in-sample nearly always"
    assert r["prob_loss"] < 0.05, f"selected strategy should not lose OOS, got {r['prob_loss']}"


def test_overfit_panel_is_caught():
    r = cscv(overfit_panel(), stat=sharpe, S=S)
    assert r["pbo"] > 0.70, f"in-sample winner is out-of-sample loser; expected high PBO, got {r['pbo']}"
    assert r["degradation_slope"] < 0, "better in-sample must predict worse out-of-sample here"
    assert r["prob_loss"] > 0.50, f"selected strategy should usually lose OOS, got {r['prob_loss']}"


def test_pbo_ordering_across_panels():
    good = cscv(skill_panel()[0], stat=sharpe, S=S)["pbo"]
    none = cscv(null_panel(), stat=sharpe, S=S)["pbo"]
    bad = cscv(overfit_panel(), stat=sharpe, S=S)["pbo"]
    assert good < none < bad, f"PBO must order skill < null < overfit, got {good} / {none} / {bad}"


def test_statistic_choice_changes_nothing_structural():
    for stat in (sharpe, mean_return, wilson_stat):
        r = cscv(null_panel(), stat=stat, S=S)
        assert r["n_combinations"] == NCOMB
        assert 0.25 < r["pbo"] < 0.75, f"{stat.__name__} on noise should stay near 0.5, got {r['pbo']}"


def test_sharpe_handles_zero_variance():
    M = np.zeros((32, 3))
    M[:, 1] = 0.5  # constant, non-zero: sd == 0
    out = sharpe(M)
    assert np.all(np.isfinite(out)), "a constant column must not produce inf/nan"
    assert out[1] == 0.0


def test_wilson_lower_bound_pinned():
    # p_hat=0.5, n=100, z=1.96 -> 0.403830 (Wilson score interval, lower limit)
    assert abs(wilson_lower_bound(50, 100) - 0.403830) < 5e-5
    assert wilson_lower_bound(0, 10) == 0.0, "zero wins pins the lower bound at exactly 0"
    assert wilson_lower_bound(3, 0) == 0.0, "no trials -> no evidence -> 0"
    assert wilson_lower_bound(9, 10) > wilson_lower_bound(5, 10), "must be monotone in wins"
    assert wilson_lower_bound(50, 100) < 0.5, "a lower bound sits below the point estimate"


def test_wilson_stat_treats_zero_weeks_as_out_of_market():
    # spa_ledger fills non-firing weeks with 0.0. Those are NOT losses.
    block = np.array([[0.0], [0.0], [0.01], [-0.01], [0.01]])  # 3 firing weeks, 2 wins
    assert abs(wilson_stat(block)[0] - wilson_lower_bound(2, 3)) < 1e-12
    assert wilson_stat(np.zeros((5, 1)))[0] == 0.0, "a rule that never fired has no evidence"


def test_rejects_bad_block_counts():
    for bad in (15, 0, -2):
        try:
            cscv(null_panel(), stat=sharpe, S=bad)
        except ValueError:
            continue
        raise AssertionError(f"S={bad} must raise ValueError (S must be even and positive)")
    try:
        cscv(np.zeros((8, 5)), stat=sharpe, S=S)
    except ValueError:
        return
    raise AssertionError("fewer rows than blocks must raise ValueError")


def test_combination_count_matches_itertools():
    assert NCOMB == len(list(combinations(range(S), S // 2)))


# --------------------------------------------------------------------------- runner

def main():
    tests = [v for k, v in sorted(globals().items()) if k.startswith("test_") and callable(v)]
    failed = 0
    for t in tests:
        try:
            t()
            print(f"  ok   {t.__name__}")
        except Exception as exc:  # noqa: BLE001 - self-check reports, does not re-raise
            failed += 1
            print(f"  FAIL {t.__name__}: {exc}")
    print(f"\n{len(tests) - failed}/{len(tests)} passed")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())

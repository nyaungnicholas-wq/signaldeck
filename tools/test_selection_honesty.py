"""Tests for the one-sided-book guard. Both polarities of the defect are pinned
here, because the second one LOOKS like a win and is the easier to ship."""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from selection_honesty import verdict, ONE_SIDED_AGREEMENT, SELECTION_TOL  # noqa: E402


def test_refuses_the_short_everything_book():
    """Live 2026-08-15 shape: acc 43.3% against a 56.5% null == 1 - null."""
    v = verdict(0.4333, 0.5652, 0.95, 0.05)
    assert v["publishable"] is False
    assert v["one_sided"] is True
    assert "DOWN" in v["reason"] and "complement" in v["reason"]


def test_refuses_the_inverted_long_everything_book():
    """The same defect wearing a win: acc 61.4% against a 60.1% null.

    A book calling UP on everything in a rising market. Measured on the
    2025-2026 holdout at 1d. Accuracy alone reads as success.
    """
    v = verdict(0.6137, 0.6010, 0.96, 0.92)
    assert v["publishable"] is False
    assert "UP" in v["reason"] and "the null itself" in v["reason"]


def test_publishes_a_genuine_cross_section():
    v = verdict(0.60, 0.50, 0.62, 0.48)
    assert v["publishable"] is True
    assert v["one_sided"] is False


def test_publishes_a_one_sided_book_that_genuinely_beats_its_null():
    """One-sidedness alone is not the defect -- being PINNED to the null is.

    Refusing on agreement alone would be red-by-construction, and a guard that
    always fires is one everybody learns to ignore.
    """
    v = verdict(0.75, 0.55, 0.97, 0.98)
    assert v["publishable"] is True
    assert v["one_sided"] is True


def test_boundary_is_the_measured_separation():
    # healthy days measured 0.75-0.86, broken 0.95-1.00
    assert 0.86 < ONE_SIDED_AGREEMENT < 0.95
    # just inside the tolerance refuses, just outside publishes
    assert verdict(0.5652 - SELECTION_TOL / 2, 0.5652, 0.99, 0.9)["publishable"] is False
    assert verdict(0.5652 + 0.10, 0.5652, 0.99, 0.9)["publishable"] is True


def test_missing_inputs_are_not_judged():
    assert verdict(None, 0.5, 0.95, 0.5) is None
    assert verdict(0.5, None, 0.95, 0.5) is None
    assert verdict(0.5, 0.5, None, 0.5) is None


def _band_db(path, probs_per_day):
    """A prediction_outcomes fixture: one row per (symbol, day), no dedup collisions."""
    import sqlite3
    con = sqlite3.connect(path)
    con.execute("CREATE TABLE prediction_outcomes (symbol_id INT, horizon TEXT, ts INT,"
                " prob REAL, up INT, fwd_return REAL, resolved_at INT,"
                " basis_epoch INT, settle_ts INT)")
    sid = 0
    for day, probs in enumerate(probs_per_day):
        settle = 1_780_000_000 + day * 86400
        for p in probs:
            sid += 1
            con.execute("INSERT INTO prediction_outcomes VALUES (?,?,?,?,?,?,?,?,?)",
                        (sid, "1d", settle, p, 1, 0.01, settle, None, settle))
    con.commit()
    con.close()


def test_high_conviction_band_admits_its_named_edge_and_excludes_just_inside():
    """The probabilities the published band names (0.65 / 0.35) are IN; 0.60 is OUT.

    Deliberately NOT claiming this pins `>=` against `>`. It cannot, and saying so
    matters more than the test looking stronger: |0.65-0.5| is 0.15000000000000002
    and |0.5-0.35| is 0.15000000000000002 -- both STRICTLY greater than the double
    nearest 0.15 -- while |0.60-0.5| is 0.09999999999999998, strictly less. Measured
    in Python and in SQLite: no probability lands exactly on the boundary, so both
    operators agree on every input and no fixture can separate them.

    That is a benign property rather than a gap. A row at the nominal edge always
    falls INSIDE by float geometry, in the direction the band intends, and it cannot
    flip. What this test does pin is that the criterion is applied at all, with the
    correct sense, at the values the registry actually publishes.
    """
    import selection_honesty as sh
    import tempfile, os
    d = tempfile.mkdtemp()
    try:
        db = os.path.join(d, "t.db")
        # per day: 12 boundary-high, 12 boundary-low, 12 inside (excluded) => 36 rows
        day = [0.65] * 12 + [0.35] * 12 + [0.60] * 12
        _band_db(db, [day, day])

        full = sh.day_tallies_by_horizon(db, 0.0)["1d"]
        band = sh.day_tallies_by_horizon(db, sh.HIGH_CONVICTION_EDGE)["1d"]

        assert [t[0] for t in full] == [36, 36], full
        assert [t[0] for t in band] == [24, 24], (
            "both 0.65 and 0.35 sit exactly on |p-0.5|=0.15 and must be INSIDE the "
            "band; 0.60 must be outside. got %r" % (band,))
    finally:
        import shutil; shutil.rmtree(d, ignore_errors=True)


def test_high_conviction_band_is_not_the_full_book():
    """The band row must be judged on its OWN rows.

    Borrowing the full book's tallies understates precisely the uncertainty under
    test: the subset is smaller, so its interval is wider, so it is MORE likely to
    be unresolved -- the direction that matters.
    """
    import selection_honesty as sh
    import skillpower
    import tempfile, os
    d = tempfile.mkdtemp()
    try:
        db = os.path.join(d, "t.db")
        day = [0.90] * 11 + [0.10] * 11 + [0.52] * 11
        _band_db(db, [day] * 4)

        full = sh.day_tallies_by_horizon(db, 0.0)["1d"]
        band = sh.day_tallies_by_horizon(db, sh.HIGH_CONVICTION_EDGE)["1d"]
        assert sum(t[0] for t in band) < sum(t[0] for t in full), (
            "band must be a strict subset, got band=%r full=%r" % (band, full))

        wide = skillpower.skill_resolvable(band)
        narrow = skillpower.skill_resolvable(full)
        assert wide["days"] == narrow["days"] == 4
        assert isinstance(wide["resolvable"], bool)
    finally:
        import shutil; shutil.rmtree(d, ignore_errors=True)


def main():
    """Runner for CI's script-style step.

    This file defines no unittest.TestCase, so `unittest discover` collects nothing
    from it. Without this block CI's fallback loop runs `python3 <file>`, which
    defines six functions, executes zero assertions and exits 0 -- a green step
    covering nothing. tools/test_runner_coverage.py pins the general rule.
    """
    failed = 0
    for name, fn in sorted(globals().items()):
        if not name.startswith("test_"):
            continue
        try:
            fn()
            print("ok   %s" % name)
        except AssertionError as e:
            failed += 1
            print("FAIL %s: %s" % (name, e))
    print("%d passed, %d failed" % (
        sum(1 for n in globals() if n.startswith("test_")) - failed, failed))
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())

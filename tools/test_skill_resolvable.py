"""The guard must refuse a skill VERDICT the day count cannot support.

The live defect: `directional-ensemble (1d)` is published as "FAILED - significantly
worse than the naive baseline" at -13.0pp, computed from 2,479 rows that are only 16
distinct days, with the null estimated on those same 16 days. Both sides carry sampling
error; comparing them as if the null were known exactly manufactures a verdict.
"""
import skillpower as sh


def days(spec):
    """[(n, hits, null_hits), ...]"""
    return [tuple(t) for t in spec]


def test_real_skill_is_resolvable():
    # 200 days, 100 rows each, 60 hits vs 50 null -> unmistakable
    r = sh.skill_resolvable(days([(100, 60, 50)] * 200))
    assert r["resolvable"] is True, r
    assert r["ci_lo"] > 0, r
    assert abs(r["skill"] - 0.10) < 1e-9, r


def test_exactly_null_is_not_resolvable():
    r = sh.skill_resolvable(days([(100, 50, 50)] * 200))
    assert r["resolvable"] is False, r
    assert r["ci_lo"] <= 0 <= r["ci_hi"], r


def test_many_rows_but_few_days_is_not_resolvable():
    """THE LIVE CASE. 16 days x 155 rows = 2,480 rows and a big apparent deficit,
    but the day-to-day spread is huge. Row-counting calls this significant; day
    blocking must not."""
    spec = [(155, h, 87) for h in (20, 130, 35, 120, 40, 115, 30, 125,
                                   45, 110, 25, 128, 38, 118, 33, 122)]
    r = sh.skill_resolvable(days(spec))
    assert r["days"] == 16, r
    assert r["resolvable"] is False, (
        "16 wildly disagreeing days cannot support a significance verdict")
    assert r["ci_lo"] <= 0 <= r["ci_hi"], r


def test_consistent_deficit_is_resolvable_even_when_negative():
    """A guard that can never confirm a NEGATIVE result is useless too."""
    r = sh.skill_resolvable(days([(100, 40, 50)] * 200))
    assert r["resolvable"] is True, r
    assert r["ci_hi"] < 0, r


def test_degenerate_inputs():
    assert sh.skill_resolvable([]) ["resolvable"] is False
    assert sh.skill_resolvable(days([(0, 0, 0)]))["resolvable"] is False
    assert sh.skill_resolvable(days([(100, 60, 50)]))["resolvable"] is False, \
        "one day is never enough"


def test_overlapping_intervals_do_not_support_a_verdict():
    """The live 1d row. Its own published numbers do not carry its own verdict."""
    r = sh.verdict_supported_by_intervals(
        0.43404598628479224, [0.3345970431881845, 0.539105266114977],
        0.563735377168213, [0.3857009819105808, 0.7267293279916269])
    assert r["supported"] is False, r
    assert abs(r["overlap_lo"] - 0.3857009819105808) < 1e-9, r
    assert abs(r["overlap_hi"] - 0.539105266114977) < 1e-9, r
    assert "OVERLAP" in r["reason"]


def test_separated_intervals_DO_support_a_verdict():
    """The live 1w high-conviction row -- the guard must NOT fire here.

    A guard that refuses every verdict is not a guard. This row's accuracy interval
    tops out at 0.456 and its null interval starts at 0.485: a clean gap. An earlier
    pass claimed this row's -27.7pp was really +1.52pp, but that came from a
    recomputation on a DIFFERENT population (13 days of a broader dedup) than the row
    grades. On its own population the verdict stands, and this test pins that.
    """
    r = sh.verdict_supported_by_intervals(
        0.3522727272727273, [0.2612, 0.4560], 0.6292613636363636, [0.4850, 0.7540])
    assert r["supported"] is True, r
    assert r["reason"] == "", r


def test_missing_intervals_withhold_rather_than_accuse():
    for args in (
        (0.5, None, 0.5, [0.4, 0.6]),
        (0.5, [0.4, 0.6], 0.5, None),
        (None, [0.4, 0.6], 0.5, [0.4, 0.6]),
    ):
        r = sh.verdict_supported_by_intervals(*args)
        assert r["supported"] is None, (args, r)
        assert "no published interval" in r["reason"]


def test_touching_intervals_count_as_overlap():
    """Exactly abutting is not separation."""
    r = sh.verdict_supported_by_intervals(0.40, [0.30, 0.50], 0.60, [0.50, 0.70])
    assert r["supported"] is False, r


def test_deterministic():
    d = days([(100, 55, 50)] * 40)
    assert sh.skill_resolvable(d) == sh.skill_resolvable(d), "must be seeded"


if __name__ == "__main__":
    for name, fn in sorted(globals().items()):
        if name.startswith("test_"):
            fn()
            print("ok", name)
    print("all passed")

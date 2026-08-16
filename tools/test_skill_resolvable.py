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


def test_deterministic():
    d = days([(100, 55, 50)] * 40)
    assert sh.skill_resolvable(d) == sh.skill_resolvable(d), "must be seeded"


if __name__ == "__main__":
    for name, fn in sorted(globals().items()):
        if name.startswith("test_"):
            fn()
            print("ok", name)
    print("all passed")

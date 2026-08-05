"""Acceptance check for tools/validate_signals.py — the advisory validation sidecar.

The sidecar re-grades the SAME inputs the published registry grades, using
dependence-aware statistics (stationary bootstrap, Hansen SPA) instead of the
Wilson + design-effect + Bonferroni path frozen on the pre-registration chain.

It is ADVISORY BY CONSTRUCTION. The published grading protocol is frozen on the
chain and byte-compared against prereg.MultiplicityRule in the Go daemon, so a
different statistic may not touch a published verdict. These tests pin that
boundary as hard as they pin the statistics.
"""
import os
import random
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import validate_signals as vs  # noqa: E402


def _series(days, n_per_day, acc, start=20000):
    """Per-day (day, n, hits) tallies at a fixed accuracy."""
    return [(start + d, n_per_day, round(n_per_day * acc)) for d in range(days)]


class TestBootstrapCI(unittest.TestCase):
    def test_null_signal_interval_covers_half(self):
        rows = _series(60, 40, 0.50)
        out = vs.bootstrap_ci(rows, horizon_days=1, reps=400, seed=7)
        self.assertIsNotNone(out["ci"])
        lo, hi = out["ci"]
        self.assertLessEqual(lo, 0.5)
        self.assertGreaterEqual(hi, 0.5)
        self.assertGreater(out["p_value"], 0.05)

    def test_strong_signal_excludes_half(self):
        rows = _series(60, 40, 0.72)
        out = vs.bootstrap_ci(rows, horizon_days=1, reps=400, seed=7)
        self.assertIsNotNone(out["ci"])
        self.assertGreater(out["ci"][0], 0.5)
        self.assertLess(out["p_value"], 0.05)

    def test_refuses_below_min_clusters(self):
        """Too few independent units is a refusal, not a wide interval."""
        out = vs.bootstrap_ci(_series(3, 40, 0.8), horizon_days=1, reps=200, seed=7)
        self.assertIsNone(out["ci"])
        self.assertIn("insufficient", out["withheld"].lower())

    def test_overlapping_windows_fold_into_blocks(self):
        """A 21d-horizon signal graded on 42 call days holds ~2 independent
        blocks, not 42. The sidecar must fold before it resamples."""
        rows = _series(42, 40, 0.8)
        out = vs.bootstrap_ci(rows, horizon_days=21, reps=200, seed=7)
        self.assertLessEqual(out["units"], 3)
        self.assertIsNone(out["ci"])  # below MIN_DISTINCT_BLOCKS -> refused

    def test_clustering_widens_versus_binomial(self):
        """The whole point: a clustered sample must not get a binomial interval.

        Each day is INDEPENDENTLY either a 90% day or a 10% day. Pooled accuracy
        is ~50%, same as a steady coin, but nearly all the variance lives
        BETWEEN days — so the honest interval is several times wider than the
        binomial one, which would assert 2400 independent trials.

        The randomness is deliberate: a deterministic 90/10 alternation is not a
        clustered sample, it is a predictable oscillation, and a block bootstrap
        is right to report almost no uncertainty about its mean.
        """
        rng = random.Random(4)
        rows = [(20000 + d, 40, 36 if rng.random() < 0.5 else 4) for d in range(60)]
        out = vs.bootstrap_ci(rows, horizon_days=1, reps=600, seed=7)
        width = out["ci"][1] - out["ci"][0]
        # A naive binomial over 2400 trials at p=0.5 is ~0.04 wide.
        self.assertGreater(width, 0.15)

    def test_deterministic_under_seed(self):
        rows = _series(60, 40, 0.62)
        a = vs.bootstrap_ci(rows, horizon_days=1, reps=300, seed=11)
        b = vs.bootstrap_ci(rows, horizon_days=1, reps=300, seed=11)
        self.assertEqual(a["ci"], b["ci"])
        self.assertEqual(a["p_value"], b["p_value"])


class TestSPA(unittest.TestCase):
    def test_all_null_family_survives_nothing(self):
        """20 genuinely null signals — the luckiest will look good, and SPA's
        whole job is to refuse to be impressed by the maximum of 20 draws.

        The hit counts are real binomial noise around 0.5, not a hand-written
        ramp: a ramp up to 57.6% over 2400 observations is a real edge, and a
        test that called it null would only be testing the fixture.
        """
        rng = random.Random(11)
        fam = {f"sig{i}": [(20000 + d, 40, sum(rng.random() < 0.5 for _ in range(40)))
                           for d in range(60)]
               for i in range(20)}
        out = vs.spa_family(fam, horizon_days=1, reps=300, seed=3)
        self.assertIsNotNone(out["spa_p"])
        self.assertGreater(out["spa_p"], 0.05)
        self.assertEqual(out["survivors"], [])

    def test_one_real_signal_survives(self):
        """Same null family, one planted edge — SPA must still find it."""
        rng = random.Random(12)
        fam = {f"sig{i}": [(20000 + d, 40, sum(rng.random() < 0.5 for _ in range(40)))
                           for d in range(60)]
               for i in range(19)}
        fam["real"] = _series(60, 40, 0.78)
        out = vs.spa_family(fam, horizon_days=1, reps=300, seed=3)
        self.assertLess(out["spa_p"], 0.05)
        self.assertIn("real", out["survivors"])
        self.assertEqual(out["best"], "real")

    def test_family_size_is_reported(self):
        fam = {f"sig{i}": _series(60, 40, 0.50) for i in range(5)}
        out = vs.spa_family(fam, horizon_days=1, reps=200, seed=3)
        self.assertEqual(out["family_size"], 5)

    def test_best_is_best_overall_not_luckiest_day(self):
        """`best` must rank on pooled accuracy. A model with one perfect day and
        a poor record otherwise must not outrank a steadily better one."""
        spiky = _series(60, 40, 0.40)
        spiky[7] = (spiky[7][0], 40, 40)          # one flawless day
        fam = {"spiky": spiky, "steady": _series(60, 40, 0.66)}
        out = vs.spa_family(fam, horizon_days=1, reps=200, seed=3)
        self.assertEqual(out["best"], "steady")

    def test_single_model_family_prices_no_multiplicity(self):
        """One model pays no family multiplicity — say so, don't emit a p-value
        that would read as a passed test."""
        out = vs.spa_family({"only": _series(60, 40, 0.7)}, horizon_days=1,
                            reps=200, seed=3)
        self.assertIsNone(out["spa_p"])
        self.assertIn("note", out)
        self.assertEqual(out["best"], "only")


class TestHorizonLabels(unittest.TestCase):
    """Horizons arrive as labels ('1d', '1w', '1w#pm'), never as bare integers.

    int('1w') raising and silently falling back to 1 was a real bug: it folded a
    seven-day forward window at one day and would have reported seven
    overlapping windows as seven independent observations.
    """

    def test_known_labels(self):
        self.assertEqual(vs._horizon_days("1d"), 1)
        self.assertEqual(vs._horizon_days("1w"), 7)

    def test_benchmark_suffix_is_stripped(self):
        self.assertEqual(vs._horizon_days("1w#pm"), 7)
        self.assertEqual(vs._horizon_days("1d#pm"), 1)

    def test_unknown_label_falls_back_to_one(self):
        self.assertEqual(vs._horizon_days("3fortnights"), 1)

    def test_weekly_record_folds_to_fewer_units(self):
        """70 daily calls at a 1w horizon are ~10 independent windows, not 70."""
        rng = random.Random(21)
        rows = [(20000 + d, 40, rng.randint(14, 34)) for d in range(70)]
        self.assertEqual(vs.bootstrap_ci(rows, 1, reps=200, seed=1)["units"], 70)
        self.assertLessEqual(vs.bootstrap_ci(rows, 7, reps=200, seed=1)["units"], 11)

    def test_folding_widens_only_when_windows_actually_overlap(self):
        """Folding is not a blanket widening — it corrects a specific error.

        For INDEPENDENT days it is variance-neutral (10 blocks of 7 days carries
        the same standard error as 70 days), and asserting it widens there would
        be testing a false claim. What it corrects is CORRELATED days: when a
        week's calls all resolve against the same forward window, the daily fold
        counts one observation seven times and reports a spuriously tight
        interval. Here every day inside a week shares that week's accuracy, so
        the daily fold sees 70 'observations' carrying only 10 distinct facts.
        """
        rng = random.Random(5)
        weekly = [rng.randint(14, 34) for _ in range(10)]
        rows = [(20000 + d, 40, weekly[d // 7]) for d in range(70)]
        dy = vs.bootstrap_ci(rows, 1, reps=600, seed=1)
        wk = vs.bootstrap_ci(rows, 7, reps=600, seed=1)
        self.assertGreater(wk["ci"][1] - wk["ci"][0], dy["ci"][1] - dy["ci"][0])


class TestAdvisoryBoundary(unittest.TestCase):
    """The sidecar may not become a second grader. These are the load-bearing tests."""

    def test_writes_only_its_own_table(self):
        db = os.path.join(tempfile.mkdtemp(), "t.db")
        con = sqlite3.connect(db)
        con.execute("CREATE TABLE regime_outcomes (id INTEGER PRIMARY KEY)")
        con.execute("CREATE TABLE prediction_outcomes (id INTEGER PRIMARY KEY)")
        con.commit()
        before = {r[0] for r in con.execute(
            "SELECT name FROM sqlite_master WHERE type='table'")}
        vs.ensure_schema(con)
        after = {r[0] for r in con.execute(
            "SELECT name FROM sqlite_master WHERE type='table'")}
        self.assertEqual(after - before, {vs.ADVISORY_TABLE})
        con.close()

    def test_verdict_column_is_advisory_only(self):
        """Every persisted verdict must be prefixed so it can never be read as
        a published registry verdict by a downstream consumer."""
        for v in ("SURVIVES", "REFUTED", "WITHHELD"):
            self.assertTrue(vs.advisory_verdict(v).startswith("ADVISORY:"))

    def test_does_not_import_or_mutate_multiplicity_state(self):
        """Reusing accuracy_registry's fetchers is correct; touching its frozen
        multiplicity state is not."""
        src = open(os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                "validate_signals.py"), encoding="utf-8").read()
        for banned in ("set_multiplicity", "set_family_floor", "_MULT",
                       "_FAMILY_FLOOR", "stamp_multiplicity"):
            self.assertNotIn(banned, src,
                             f"sidecar must not touch frozen grading state: {banned}")

    def test_reuses_registry_fetchers(self):
        """Single source of truth for the SQL — the sidecar must not re-derive
        its own version of the grading queries."""
        src = open(os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                "validate_signals.py"), encoding="utf-8").read()
        self.assertIn("accuracy_registry", src)
        self.assertNotIn("FROM prediction_outcomes", src)
        self.assertNotIn("FROM regime_outcomes", src)

    def test_opens_database_read_only(self):
        src = open(os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                "validate_signals.py"), encoding="utf-8").read()
        self.assertIn("mode=ro", src)


class TestPersistence(unittest.TestCase):
    def test_roundtrip(self):
        db = os.path.join(tempfile.mkdtemp(), "t.db")
        con = sqlite3.connect(db)
        vs.ensure_schema(con)
        row = {
            "signal": "directional/1", "family": "directional",
            "n": 2400, "units": 60, "acc": 0.62,
            "ci_lo": 0.55, "ci_hi": 0.69, "p_value": 0.004,
            "spa_p": 0.03, "family_size": 8,
            "verdict": vs.advisory_verdict("SURVIVES"),
            "method": "stationary-bootstrap+spa", "withheld": None,
        }
        vs.persist(con, [row], run_ts=1750000000)
        got = list(con.execute(
            f"SELECT signal, verdict, spa_p FROM {vs.ADVISORY_TABLE}"))
        self.assertEqual(len(got), 1)
        self.assertEqual(got[0][0], "directional/1")
        self.assertTrue(got[0][1].startswith("ADVISORY:"))
        con.close()

    def test_rerun_is_append_only_history(self):
        """Two runs must both survive — this is a track record, not a cache."""
        db = os.path.join(tempfile.mkdtemp(), "t.db")
        con = sqlite3.connect(db)
        vs.ensure_schema(con)
        row = {"signal": "s", "family": "f", "n": 1, "units": 1, "acc": 0.5,
               "ci_lo": None, "ci_hi": None, "p_value": None, "spa_p": None,
               "family_size": 1, "verdict": vs.advisory_verdict("WITHHELD"),
               "method": "m", "withheld": "insufficient blocks"}
        vs.persist(con, [row], run_ts=1750000000)
        vs.persist(con, [row], run_ts=1750086400)
        n = con.execute(f"SELECT COUNT(*) FROM {vs.ADVISORY_TABLE}").fetchone()[0]
        self.assertEqual(n, 2)
        con.close()


if __name__ == "__main__":
    unittest.main()

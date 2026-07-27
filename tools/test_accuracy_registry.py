#!/usr/bin/env python3
"""Tests for the accuracy registry — the script that decides whether a shipped
claim survives contact with the live record.

It had none until 2026-07-26, which is how it came to publish binomial intervals
over ~1,000 correlated symbols per day for eight months. The tests below pin the
day-resampling discipline so that cannot come back silently.

Run: python3 -m unittest discover -s tools -p 'test_*.py'
"""
import math
import os
import sqlite3
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from accuracy_registry import (  # noqa: E402
    MIN_DISTINCT_DAYS,
    SURVIVORSHIP_EPOCH_TS,
    clustered_ci,
    design_effect,
    grade_directional,
    grade_structural,
    prequential_null,
    verdict_for,
    wilson,
    wilson_eff,
)


class TestDesignEffect(unittest.TestCase):
    def test_uniform_days_carry_no_penalty(self):
        """Days that all behave identically have no between-day variance."""
        days = [(100, 60)] * 20
        self.assertAlmostEqual(design_effect(days), 1.0, places=6)

    def test_clustered_days_are_penalised(self):
        """Days that swing between mostly-right and mostly-wrong are the live shape."""
        days = [(500, 450)] * 11 + [(500, 25)] * 9
        deff = design_effect(days)
        self.assertGreater(deff, 50, f"deff {deff} does not reflect extreme clustering")

    def test_never_below_one(self):
        """A deff under 1 would make the interval narrower than independence."""
        days = [(100, 50), (100, 50), (100, 50)]
        self.assertGreaterEqual(design_effect(days), 1.0)

    def test_degenerate_proportion_is_worst_case(self):
        """All-right (or all-wrong) is the most perfectly clustered sample there is."""
        days = [(100, 100)] * 20
        self.assertAlmostEqual(design_effect(days), 100.0, places=6)  # n/k

    def test_unmeasurable_below_two_days(self):
        self.assertIsNone(design_effect([(100, 50)]))
        self.assertIsNone(design_effect([]))


class TestClusteredCI(unittest.TestCase):
    def test_withholds_below_the_day_floor(self):
        """Nine days is not enough to estimate between-day variance."""
        g = clustered_ci([(400, 300)] * (MIN_DISTINCT_DAYS - 1))
        self.assertIsNone(g["ci"])
        self.assertEqual(g["ci_method"], "withheld")
        self.assertIn("distinct days", g["ci_reason"])

    def test_publishes_at_and_above_the_floor(self):
        g = clustered_ci([(400, 300)] * MIN_DISTINCT_DAYS)
        self.assertIsNotNone(g["ci"])
        self.assertEqual(g["ci_method"], "day-clustered-wilson")

    def test_interval_is_wider_than_the_row_count_interval(self):
        """The whole point: a clustered sample cannot carry a row-count interval."""
        days = [(500, 450)] * 11 + [(500, 25)] * 9
        g = clustered_ci(days)
        pooled_lo, pooled_hi = wilson(g["hits"], g["n"])
        self.assertGreater(g["ci"][1] - g["ci"][0], (pooled_hi - pooled_lo) * 5)

    def test_effective_n_is_n_over_deff(self):
        days = [(500, 450)] * 11 + [(500, 25)] * 9
        g = clustered_ci(days)
        self.assertAlmostEqual(g["effective_n"], g["n"] / g["design_effect"], places=6)
        self.assertLess(g["effective_n"], g["n"])

    def test_point_estimate_is_untouched(self):
        """Correcting the interval must not move the accuracy itself."""
        days = [(500, 450)] * 11 + [(500, 25)] * 9
        g = clustered_ci(days)
        self.assertAlmostEqual(g["acc"], (11 * 450 + 9 * 25) / 10000, places=9)


class TestVerdict(unittest.TestCase):
    def test_no_interval_means_no_verdict(self):
        """A missing interval must never fall through to a point-estimate verdict."""
        v = verdict_for(0.82, None, None, 408, None, 0.82, distinct_days=1)
        self.assertIn("INSUFFICIENT DAYS", v)
        self.assertNotIn("HOLDING", v)

    def test_one_market_day_cannot_validate_a_claim(self):
        """The 2026-08-07 scenario: ~408 forecasts resolving on a single call day.

        Under the old row-count interval this produced a decisive HOLDING or
        DECAYED verdict on the platform's headline 82% claim. One market day is
        one observation, whatever its symbol count.
        """
        g = clustered_ci([(408, 335)])          # one day, 82.1% correct
        self.assertIsNone(g["ci"])
        v = verdict_for(g["acc"], None, None, g["n"], None, 0.82,
                        distinct_days=g["distinct_days"])
        self.assertIn("INSUFFICIENT DAYS", v)

    def test_interval_straddling_the_null_is_no_skill(self):
        v = verdict_for(0.487, 0.451, 0.524, 7115, 0.523, None, distinct_days=13)
        self.assertTrue(v.startswith("NO SKILL"), v)

    def test_interval_entirely_below_the_null_still_fails(self):
        """Widening intervals must not rescue a genuinely failing predictor."""
        v = verdict_for(0.481, 0.448, 0.514, 13058, 0.546, None, distinct_days=23)
        self.assertTrue(v.startswith("FAILED"), v)

    def test_sample_floor_still_applies(self):
        v = verdict_for(0.9, 0.8, 1.0, 10, 0.5, None, distinct_days=10)
        self.assertTrue(v.startswith("INSUFFICIENT"), v)


class TestSurvivorshipBoundary(unittest.TestCase):
    """Rows created before symbols.delisted_at existed (2026-07-24) were graded
    against a survivor-seeded universe. They must never enter a graded tally."""

    @staticmethod
    def _db():
        con = sqlite3.connect(":memory:")
        con.execute("""CREATE TABLE prediction_outcomes (
            symbol_id INTEGER, horizon TEXT, prob REAL, up INTEGER,
            ts INTEGER, resolved_at INTEGER)""")
        con.execute("""CREATE TABLE regime_outcomes (
            symbol_id INTEGER, kind TEXT, horizon_days INTEGER, day INTEGER,
            ts INTEGER, resolved_at INTEGER, correct INTEGER,
            historical_accuracy REAL)""")
        return con

    def test_pre_epoch_directional_rows_cannot_enter_a_tally(self):
        con = self._db()
        pre = SURVIVORSHIP_EPOCH_TS - 86400
        con.execute("INSERT INTO prediction_outcomes VALUES (1,'1d',0.8,1,?,?)",
                    (pre, pre + 86400))
        self.assertEqual(grade_directional(con), [])

    def test_directional_tally_counts_only_post_epoch_rows(self):
        con = self._db()
        pre = SURVIVORSHIP_EPOCH_TS - 86400
        con.execute("INSERT INTO prediction_outcomes VALUES (1,'1d',0.8,1,?,?)",
                    (pre, pre + 86400))
        # Epoch day itself is INCLUDED — the boundary is "at or after".
        for i in range(3):
            ts = SURVIVORSHIP_EPOCH_TS + i * 86400
            con.execute("INSERT INTO prediction_outcomes VALUES (?, '1d',0.8,1,?,?)",
                        (10 + i, ts, ts + 86400))
        rows = grade_directional(con)
        all_band = [r for r in rows if r["band"] == "all"]
        self.assertEqual(len(all_band), 1)
        self.assertEqual(all_band[0]["live_n"], 3)
        self.assertTrue(all_band[0]["survivorship_clean"])

    def test_pre_epoch_structural_rows_cannot_enter_a_tally(self):
        con = self._db()
        pre = SURVIVORSHIP_EPOCH_TS - 86400
        con.execute("INSERT INTO regime_outcomes VALUES (1,'oversold',21,?,?,?,1,0.82)",
                    (pre // 86400, pre, pre + 86400))
        self.assertEqual(grade_structural(con), [])

    def test_structural_tally_counts_only_post_epoch_rows(self):
        con = self._db()
        pre = SURVIVORSHIP_EPOCH_TS - 86400
        con.execute("INSERT INTO regime_outcomes VALUES (1,'oversold',21,?,?,?,1,0.82)",
                    (pre // 86400, pre, pre + 86400))
        for i in range(2):
            ts = SURVIVORSHIP_EPOCH_TS + i * 86400
            con.execute("INSERT INTO regime_outcomes VALUES (?, 'oversold',21,?,?,?,1,0.82)",
                        (10 + i, ts // 86400, ts, ts + 86400))
        rows = grade_structural(con)
        self.assertEqual(len(rows), 1)
        # Both the resolved tally AND the forecasts-recorded total exclude pre-epoch.
        self.assertEqual(rows[0]["live_n"], 2)
        self.assertEqual(rows[0]["forecasts_recorded"], 2)
        self.assertTrue(rows[0]["survivorship_clean"])


class TestPrequentialNull(unittest.TestCase):
    """The majority-class null must be graded OUT of sample. The old
    max(base, 1-base) was computed over the same window it benchmarked, which
    handed the null hindsight the model never had."""

    def test_null_is_out_of_sample(self):
        """A mid-window class flip must not be retroactively credited to the null.

        Eight all-down days then twelve all-up days. The hindsight null sees
        the whole window, calls the majority UP, and claims 60%. The
        prequential bettor guesses down through the flip and only crosses over
        once up-days actually outnumber down-days: day 1 is a coin flip (50),
        days 2-8 guess down and score 700, days 9-16 guess down into the flip
        and score 0, day 17 sits on a tied prior (50), days 18-20 finally
        guess up (300) — 1100/2000 = 55%.
        """
        days = [(100, 0)] * 8 + [(100, 100)] * 12  # chronological (n, ups)
        hindsight = max(12 / 20, 8 / 20)  # the old, clairvoyant null: 60%
        g = prequential_null(days)
        self.assertAlmostEqual(g["acc"], 1100 / 2000, places=9)
        self.assertLess(g["acc"], hindsight)

    def test_day_one_is_a_coin_flip(self):
        """With no prior days there is no majority to guess — 0.5, not 1.0."""
        g = prequential_null([(100, 100)])
        self.assertAlmostEqual(g["acc"], 0.5, places=9)

    def test_stationary_majority_is_still_credited(self):
        """When up-days really do run 100% throughout, the null must converge
        on that rate — the change removes hindsight, not the majority null."""
        g = prequential_null([(100, 100)] * 20)
        self.assertAlmostEqual(g["acc"], (50 + 19 * 100) / 2000, places=9)

    def test_grade_directional_publishes_an_out_of_sample_prequential_null(self):
        """End to end: two all-up days then three all-down days, two symbols
        per day. Hindsight null = 6/10 = 60%. Prequential: day 1 coin flip (1),
        day 2 guess up (2), days 3-4 guess up into the flip (0), day 5 tied
        prior (1) — 4/10 = 40%. The flip must NOT be retroactively credited to
        the prequential null; during the dual-null transition cycle the row
        publishes both and the verdict uses the stricter (higher) of the two."""
        con = TestSurvivorshipBoundary._db()
        for day in range(5):
            up = 1 if day < 2 else 0
            ts = SURVIVORSHIP_EPOCH_TS + day * 86400
            for sym in (1, 2):
                con.execute(
                    "INSERT INTO prediction_outcomes VALUES (?, '1d', 0.8, ?, ?, ?)",
                    (sym, up, ts, ts + 86400))
        rows = grade_directional(con)
        row = next(r for r in rows if r["band"] == "all")
        self.assertIn("prequential", row["null_method"])
        self.assertAlmostEqual(row["null_prequential"], 0.4, places=9)
        self.assertNotAlmostEqual(row["null_prequential"], 0.6, places=3)
        self.assertAlmostEqual(row["null_hindsight"], 0.6, places=9)
        self.assertEqual(row["null_acc"],
                         max(row["null_hindsight"], row["null_prequential"]))


class TestWilsonEff(unittest.TestCase):
    def test_matches_plain_wilson_at_deff_one(self):
        lo1, hi1 = wilson(600, 1000)
        lo2, hi2 = wilson_eff(0.6, 1000)
        self.assertAlmostEqual(lo1, lo2, places=6)
        self.assertAlmostEqual(hi1, hi2, places=6)

    def test_widens_as_effective_n_falls(self):
        narrow = wilson_eff(0.6, 1000)
        wide = wilson_eff(0.6, 50)
        self.assertGreater(wide[1] - wide[0], narrow[1] - narrow[0])

    def test_bounded_to_the_unit_interval(self):
        lo, hi = wilson_eff(0.99, 3)
        self.assertGreaterEqual(lo, 0.0)
        self.assertLessEqual(hi, 1.0)
        self.assertFalse(math.isnan(lo) or math.isnan(hi))


class TestSnapshotRoundTrip(unittest.TestCase):
    """The committed reproducibility snapshot must grade to exactly what the
    database grades to — that equivalence is the promise REPRODUCE.md makes."""

    @staticmethod
    def _seeded_db():
        con = TestSurvivorshipBoundary._db()
        for i in range(12):
            ts = SURVIVORSHIP_EPOCH_TS + i * 86400
            con.execute("INSERT INTO prediction_outcomes VALUES (?, '1d', 0.8, ?, ?, ?)",
                        (10 + i, i % 2, ts, ts + 86400))
            con.execute("INSERT INTO regime_outcomes VALUES (?, 'oversold', 21, ?, ?, ?, 1, 0.82)",
                        (10 + i, ts // 86400, ts, ts + 86400))
        return con

    def test_snapshot_grades_identically_to_db(self):
        import tempfile

        from accuracy_registry import (grade_directional_days, grade_structural_days,
                                       load_snapshot)
        from make_repro_snapshot import write_snapshot

        con = self._seeded_db()
        db_rows = grade_directional(con) + grade_structural(con)
        with tempfile.TemporaryDirectory() as td:
            write_snapshot(con, td)
            by_h, totals, per_day = load_snapshot(td)
            snap_rows = grade_directional_days(by_h) + grade_structural_days(totals, per_day)
        # The snapshot pins floats to %.10g (the datasetver canonical format);
        # everything else must round-trip exactly.
        for r in db_rows + snap_rows:
            if r.get("claimed") is not None:
                r["claimed"] = float("%.10g" % r["claimed"])
        self.assertEqual(db_rows, snap_rows)

    def test_tampered_snapshot_refuses_to_grade(self):
        """One edited cell must fail the manifest hash and abort the grade."""
        import tempfile

        from accuracy_registry import load_snapshot
        from make_repro_snapshot import write_snapshot

        con = self._seeded_db()
        with tempfile.TemporaryDirectory() as td:
            write_snapshot(con, td)
            path = os.path.join(td, "directional_days.csv")
            with open(path) as f:
                header, first, *rest = f.readlines()
            cells = first.strip().split(",")
            cells[3] = str(int(cells[3]) + 1)  # one extra "correct" tally
            with open(path, "w") as f:
                f.writelines([header, ",".join(cells) + "\n", *rest])
            with self.assertRaises(SystemExit):
                load_snapshot(td)


if __name__ == "__main__":
    unittest.main()

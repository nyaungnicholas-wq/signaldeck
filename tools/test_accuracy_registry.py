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
        # The hindsight null is retired everywhere — structural rows included.
        self.assertNotIn("null_hindsight", rows[0])


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
        per day. The retired hindsight null would have claimed 6/10 = 60%.
        Prequential: day 1 coin flip (1), day 2 guess up (2), days 3-4 guess
        up into the flip (0), day 5 tied prior (1) — 4/10 = 40%. The flip must
        NOT be retroactively credited to the null, and since the dual-null
        transition cycle closed (audits/2026-07-27-null-transition.md, zero
        verdict changes) the prequential null alone drives the verdict — no
        row may publish a hindsight column at all."""
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
        self.assertEqual(row["null_method"], "prequential-majority (walk-forward)")
        self.assertAlmostEqual(row["null_prequential"], 0.4, places=9)
        self.assertNotAlmostEqual(row["null_prequential"], 0.6, places=3)
        self.assertEqual(row["null_acc"], row["null_prequential"])
        for r in rows:
            self.assertNotIn("null_hindsight", r)


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


class TestXsfactorSnapshotRoundTrip(unittest.TestCase):
    """repro/xsfactor_inputs.csv must version EXACTLY the series
    tools/xsfactor_edge.py consumes — same universe, same length floor, same
    canonical bytes — or the committed manifest pins a different study than
    the one whose numbers shipped in daemon/internal/xsfactor."""

    # (symbol, market, active, n_bars). BBB is delisted but stays in the
    # --universe all study; TINY is below the MIN_VOL_CLOSES floor and CCC is
    # not a stock — neither may appear in the snapshot or the study.
    SPEC = [("AAA", "stocks", 1, 30), ("BBB", "stocks", 0, 25),
            ("TINY", "stocks", 1, 5), ("CCC", "crypto", 1, 30)]

    @classmethod
    def _bars_db(cls, path):
        con = sqlite3.connect(path)
        # write_snapshot builds every file, so the outcome tables must exist
        # (empty) alongside the bars universe under version here.
        con.execute("""CREATE TABLE prediction_outcomes (
            symbol_id INTEGER, horizon TEXT, prob REAL, up INTEGER,
            ts INTEGER, resolved_at INTEGER)""")
        con.execute("""CREATE TABLE regime_outcomes (
            symbol_id INTEGER, kind TEXT, horizon_days INTEGER, day INTEGER,
            ts INTEGER, resolved_at INTEGER, correct INTEGER,
            historical_accuracy REAL)""")
        con.execute("CREATE TABLE symbols (id INTEGER PRIMARY KEY, symbol TEXT,"
                    " market TEXT, active INTEGER)")
        con.execute("CREATE TABLE bars (symbol_id INTEGER, tf TEXT, ts INTEGER,"
                    " close REAL, volume REAL)")
        bars = {}
        for sid, (sym, market, active, n) in enumerate(cls.SPEC, start=1):
            con.execute("INSERT INTO symbols VALUES (?,?,?,?)", (sid, sym, market, active))
            rows = []
            for i in range(n):
                ts = (20000 + i) * 86400  # one bar per UTC day
                close = 10.0 + sid + i * 0.25
                vol = 1000.0 * sid + i
                con.execute("INSERT INTO bars VALUES (?,?,?,?,?)",
                            (sid, "1d", ts, close, vol))
                rows.append((ts // 86400, close, vol))
            bars[sym] = rows
        con.commit()
        return con, bars

    def test_snapshot_versions_exactly_what_the_study_loads(self):
        import tempfile

        import xsfactor_edge as xsf
        from make_repro_snapshot import g10, hash_records, write_snapshot

        with tempfile.TemporaryDirectory() as td:
            db_path = os.path.join(td, "bars.db")
            con, bars = self._bars_db(db_path)
            snap_dir = os.path.join(td, "repro")
            manifest = write_snapshot(con, snap_dir)
            con.close()

            import csv
            with open(os.path.join(snap_dir, "xsfactor_inputs.csv"), newline="") as f:
                recs = list(csv.reader(f))[1:]
            by_sym = {r[0]: r for r in recs}

            # Universe and floor are the STUDY's, not the exporter's opinion:
            # the snapshot lists exactly the series xsfactor_edge.load builds.
            study = {s.sym: s for s in xsf.load(db_path, "all")}
            self.assertEqual(set(by_sym), set(study))
            self.assertEqual(set(by_sym), {"AAA", "BBB"})

            for sym, s in study.items():
                row = by_sym[sym]
                self.assertEqual(int(row[2]), int(s.active))       # active flag
                self.assertEqual(int(row[3]), s.days[0])           # first day
                self.assertEqual(int(row[4]), s.days[-1])          # last day
                self.assertEqual(int(row[5]), len(s.days))         # n
                # Byte-level pin: the hash must equal one computed straight
                # from the ground-truth bars, not from any DB round trip.
                expected = hash_records(sym, "xsfactor-1d-day-close-volume",
                                        [[str(d), g10(c), g10(v)]
                                         for d, c, v in bars[sym]])
                self.assertEqual(row[6], expected)

            # The CSV read back through csv.reader re-hashes to the manifest
            # value — the same tamper-evidence contract the graded files carry.
            ent = next(e for e in manifest["files"]
                       if e["file"] == "xsfactor_inputs.csv")
            self.assertEqual(ent["kind"], "xsfactor-input-series-versions")
            self.assertEqual(ent["rows"], 2)
            self.assertEqual(
                hash_records("xsfactor_inputs.csv", ent["kind"], recs), ent["sha256"])


class TestFrozenSnapshotVerdicts(unittest.TestCase):
    """CI freeze of the prequential-only verdicts on the COMMITTED repro/ snapshot.

    The snapshot is fixed data, so grading it is a pure function of the
    registry's rules. Pinning every row's exact verdict here means any future
    change to null construction, the dedup discipline, the survivorship
    boundary, or the day-clustering floor that moves a published verdict
    fails CI loudly instead of drifting into the public registry unnoticed.
    A verdict change that is INTENDED must update this table in the same
    commit — which is exactly the review trail the freeze exists to force.
    """

    REPRO = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                         "repro")

    # (predictor, band) -> the exact verdict the committed snapshot grades to.
    EXPECTED = {
        ("directional-ensemble (1d)", "all"):
            "INSUFFICIENT (18/30)",
        ("directional-ensemble (1d, high conviction)", "|p-0.5|>=0.15"):
            "INSUFFICIENT (8/30)",
        # The six structural claims are backtests awaiting their first live
        # grade — PENDING until the horizon elapses, never a live verdict.
        ("liquidity21", "all"):
            "PENDING (first grade 2026-08-14, 0/30 resolved)",
        ("liquidity21-crypto", "all"):
            "PENDING (first grade 2026-08-14, 0/30 resolved)",
        ("trend21", "all"):
            "PENDING (first grade 2026-08-14, 0/30 resolved)",
        ("trend21-crypto", "all"):
            "PENDING (first grade 2026-08-14, 0/30 resolved)",
        ("trend63", "all"):
            "PENDING (first grade 2026-09-25, 0/30 resolved)",
        ("vol21", "all"):
            "PENDING (first grade 2026-08-14, 0/30 resolved)",
    }

    @classmethod
    def _rows(cls):
        from accuracy_registry import (grade_directional_days, grade_structural_days,
                                       load_snapshot)
        by_h, totals, per_day = load_snapshot(cls.REPRO)
        return grade_directional_days(by_h) + grade_structural_days(totals, per_day)

    def test_every_row_grades_to_its_frozen_verdict(self):
        """Exact row set AND exact verdict strings — no extras, no drift."""
        got = {(r["predictor"], r["band"]): r["verdict"] for r in self._rows()}
        self.assertEqual(got, self.EXPECTED)

    def test_directional_rows_are_prequential_only(self):
        """The hindsight null retired after its transition cycle; a row that
        publishes it again (or reads a verdict against it) must fail here."""
        directional = [r for r in self._rows() if r["family"] == "direction"]
        self.assertTrue(directional)
        for r in directional:
            self.assertNotIn("null_hindsight", r)
            self.assertIn("prequential", r["null_method"])
            self.assertEqual(r["null_acc"], r["null_prequential"])

    def test_no_frozen_row_slipped_past_the_sample_floor(self):
        """The snapshot record is below both evidence floors everywhere; if a
        rule change lets any row publish an interval or a live verdict off
        this data, the freeze above fails too — this pins the reason why."""
        for r in self._rows():
            self.assertIsNone(r["ci"], r["predictor"])
            self.assertLess(r["live_n"], MIN_INDEPENDENT_N, r["predictor"])


if __name__ == "__main__":
    unittest.main()

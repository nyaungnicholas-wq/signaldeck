#!/usr/bin/env python3
"""Tests for the accuracy registry — the script that decides whether a shipped
claim survives contact with the live record.

It had none until 2026-07-26, which is how it came to publish binomial intervals
over ~1,000 correlated symbols per day for eight months. The tests below pin the
day-resampling discipline so that cannot come back silently.

Run: python3 -m unittest discover -s tools -p 'test_*.py'
"""
import contextlib
import io
import math
import json
import tempfile
import os
import re
import random
import sqlite3
import subprocess
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))


def _repo_visible_bash():
    """Return a bash that can actually read files under this repo, or None.

    On Windows `shutil.which("bash")` finds C:\\Windows\\System32\\bash.exe --
    the WSL launcher -- before Git Bash, because System32 comes first on PATH.
    WSL has its own filesystem, so a `C:/Users/...` path does not exist there and
    a sourced prelude dies before it can hash anything. The symptom was
    `sha256_of` returning an empty string and the protocol-document gate failing
    on this machine only, while the shell function itself was correct.

    Candidates are probed, not guessed: whichever bash can stat a file in this
    repo is the one that can run the script under test.
    """
    import shutil
    probe = os.path.abspath(__file__).replace("\\", "/")
    candidates = [shutil.which("bash")]
    for env in ("ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"):
        root = os.environ.get(env)
        if root:
            candidates.append(os.path.join(root, "Git", "bin", "bash.exe"))
    for cand in candidates:
        if not cand or not os.path.exists(cand):
            continue
        try:
            r = subprocess.run([cand, "-c", f'test -f "{probe}"'], timeout=30)
        except (OSError, subprocess.SubprocessError):
            continue
        if r.returncode == 0:
            return cand
    return None

from accuracy_registry import (  # noqa: E402
    CALIBRATION_BINS,
    MIN_DISTINCT_BLOCKS as _MDB,
    REVISION_EPOCH_TS,
    NULL_AMENDMENT_EPOCH_TS,
    null_amendment_probe,
    apply_null_amendment_probe,
    apply_revision_gate,
    require_registered_grader,
    require_research_liveness,
    grader_registration_error,
    revision_gate,
    self_sha256,
    MIN_DISTINCT_BLOCKS,
    MIN_DISTINCT_DAYS,
    MAX_ALPHA,
    MULTIPLICITY_RULE,
    chain_looks,
    corrected_z,
    grade_with_multiplicity,
    multiplicity,
    set_multiplicity,
    MIN_INDEPENDENT_N,
    SURVIVORSHIP_EPOCH_TS,
    auto_retire_rule,
    auto_retire_rule_digest,
    clustered_ci,
    clustered_ci_blocks,
    design_effect,
    horizon_blocks,
    fetch_calibration_bins,
    fetch_chain_presence,
    grade_directional,
    main as registry_main,
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
        con.execute("""CREATE TABLE symbols (
            id INTEGER PRIMARY KEY, symbol TEXT, active INTEGER,
            delisted_at INTEGER)""")
        return con

    @staticmethod
    def _list(con, sid, active=1, delisted_at=None):
        con.execute("INSERT INTO symbols VALUES (?,?,?,?)",
                    (sid, f"S{sid}", active, delisted_at))

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
            self._list(con, 10 + i)
        rows = grade_directional(con)
        all_band = [r for r in rows if r["band"] == "all"]
        self.assertEqual(len(all_band), 1)
        self.assertEqual(all_band[0]["live_n"], 3)
        # Clean is EARNED here: every graded symbol's listing status resolves.
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
            self._list(con, 10 + i)
        rows = grade_structural(con)
        self.assertEqual(len(rows), 1)
        # Both the resolved tally AND the forecasts-recorded total exclude pre-epoch.
        self.assertEqual(rows[0]["live_n"], 2)
        self.assertEqual(rows[0]["forecasts_recorded"], 2)
        self.assertTrue(rows[0]["survivorship_clean"])
        # The hindsight null is retired everywhere — structural rows included.
        self.assertNotIn("null_hindsight", rows[0])

    def _one_directional_row(self, con):
        ts = SURVIVORSHIP_EPOCH_TS
        for i in range(3):
            con.execute("INSERT INTO prediction_outcomes VALUES (?, '1d',0.8,1,?,?)",
                        (10 + i, ts + i * 86400, ts + (i + 1) * 86400))
        return [r for r in grade_directional(con) if r["band"] == "all"][0]

    def test_inactive_symbol_without_a_delisting_date_is_not_clean(self):
        """The contaminating case: a symbol that stopped being tracked at an
        unknown moment. It must downgrade the row, not be assumed benign."""
        con = self._db()
        self._list(con, 10)
        self._list(con, 11)
        self._list(con, 12, active=0, delisted_at=None)
        row = self._one_directional_row(con)
        self.assertFalse(row["survivorship_clean"])
        self.assertIn("no delisted_at", row["survivorship_reason"])
        self.assertAlmostEqual(row["survivorship_coverage"], 2 / 3)

    def test_delisted_symbol_with_a_known_date_still_counts_as_resolvable(self):
        con = self._db()
        self._list(con, 10)
        self._list(con, 11)
        self._list(con, 12, active=0, delisted_at=SURVIVORSHIP_EPOCH_TS + 86400)
        self.assertTrue(self._one_directional_row(con)["survivorship_clean"])

    def test_symbol_missing_from_the_symbols_table_is_not_clean(self):
        con = self._db()
        self._list(con, 10)
        self._list(con, 11)
        row = self._one_directional_row(con)
        self.assertFalse(row["survivorship_clean"])
        self.assertIn("absent from the symbols table", row["survivorship_reason"])


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


class TestCalibrationBins(unittest.TestCase):
    """Reliability-diagram bins must obey the same independence and
    survivorship discipline as the graded rows — and must expose an
    anti-calibrated conviction tier as (predicted, realized, n) rather than
    hiding it inside a band average."""

    def test_bins_report_predicted_vs_realized(self):
        con = TestSurvivorshipBoundary._db()
        # Ten symbol-days at p=0.85 (a conviction-tier bin) of which only 2 go
        # up — the anti-calibrated shape the bins exist to expose.
        for i in range(10):
            ts = SURVIVORSHIP_EPOCH_TS + i * 86400
            con.execute("INSERT INTO prediction_outcomes VALUES (?, '1d', 0.85, ?, ?, ?)",
                        (10 + i, 1 if i < 2 else 0, ts, ts + 86400))
        cal = fetch_calibration_bins(con)
        bins = cal["horizons"]["1d"]
        self.assertEqual(len(bins), 1)
        b = bins[0]
        self.assertEqual(b["p_lo"], 0.8)
        self.assertEqual(b["p_hi"], 0.9)
        self.assertAlmostEqual(b["mean_predicted"], 0.85, places=9)
        self.assertAlmostEqual(b["realized_up_freq"], 0.2, places=9)
        self.assertEqual(b["n"], 10)
        self.assertEqual(b["distinct_days"], 10)

    def test_bins_dedupe_intraday_repeats(self):
        """Same (symbol, horizon, UTC-day) must count once, keeping the latest."""
        con = TestSurvivorshipBoundary._db()
        ts = SURVIVORSHIP_EPOCH_TS
        con.execute("INSERT INTO prediction_outcomes VALUES (1,'1d',0.72,1,?,?)",
                    (ts, ts + 86400))
        con.execute("INSERT INTO prediction_outcomes VALUES (1,'1d',0.78,1,?,?)",
                    (ts + 3600, ts + 86400))  # later same-day repeat wins
        bins = fetch_calibration_bins(con)["horizons"]["1d"]
        self.assertEqual(sum(b["n"] for b in bins), 1)
        self.assertAlmostEqual(bins[0]["mean_predicted"], 0.78, places=9)

    def test_bins_exclude_pre_epoch_rows(self):
        con = TestSurvivorshipBoundary._db()
        pre = SURVIVORSHIP_EPOCH_TS - 86400
        con.execute("INSERT INTO prediction_outcomes VALUES (1,'1d',0.9,1,?,?)",
                    (pre, pre + 86400))
        self.assertEqual(fetch_calibration_bins(con)["horizons"], {})

    def test_probability_one_lands_in_the_top_bin(self):
        """p=1.0 must clamp into the last bin, not fall out of range."""
        con = TestSurvivorshipBoundary._db()
        ts = SURVIVORSHIP_EPOCH_TS
        con.execute("INSERT INTO prediction_outcomes VALUES (1,'1d',1.0,1,?,?)",
                    (ts, ts + 86400))
        bins = fetch_calibration_bins(con)["horizons"]["1d"]
        self.assertEqual(len(bins), 1)
        self.assertAlmostEqual(bins[0]["p_hi"], 1.0, places=9)
        self.assertAlmostEqual(bins[0]["p_lo"], (CALIBRATION_BINS - 1) / CALIBRATION_BINS,
                               places=9)

    def test_benchmark_rows_never_enter_the_diagram(self):
        """The prequential-majority benchmark is a constant guess, not a
        probability model — it must not appear as a calibrated predictor."""
        con = TestSurvivorshipBoundary._db()
        ts = SURVIVORSHIP_EPOCH_TS
        con.execute("INSERT INTO prediction_outcomes VALUES (1,'1d',0.7,1,?,?)",
                    (ts, ts + 86400))
        con.execute("INSERT INTO prediction_outcomes VALUES (1,'1d#pm',1.0,1,?,?)",
                    (ts, ts + 86400))
        horizons = fetch_calibration_bins(con)["horizons"]
        self.assertEqual(set(horizons), {"1d"})


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
            by_h, totals, per_day, pre, naive, _proto = load_snapshot(td)
            snap_rows = (grade_directional_days(by_h)
                         + grade_structural_days(totals, per_day, pre, naive))
        # The snapshot pins floats to %.10g (the datasetver canonical format);
        # everything else must round-trip exactly.
        for r in db_rows + snap_rows:
            if r.get("claimed") is not None:
                r["claimed"] = float("%.10g" % r["claimed"])
        # A snapshot carries per-day tallies and no symbols table, so universe
        # completeness is UNMEASURED on that path — an honest difference from
        # the DB grade, not a drift in the numbers. Assert it explicitly, then
        # compare everything else exactly.
        for r in snap_rows:
            self.assertFalse(r["survivorship_clean"])
            self.assertIn("snapshot", r["survivorship_reason"])
        surv_keys = ("survivorship_clean", "survivorship_reason", "survivorship_coverage")
        for r in db_rows + snap_rows:
            for k in surv_keys:
                r.pop(k, None)
        self.assertEqual(db_rows, snap_rows)

    def test_snapshot_grading_target_equals_the_db_grading_target(self):
        """The freeze must survive the trip through the snapshot.

        test_snapshot_grades_identically_to_db compares whole rows, but every
        structural row on the committed record is PENDING, so a snapshot that
        recomputed C from the exported tallies could still match on `verdict`
        while grading against a different bar. This asserts C itself — and its
        provenance — per kind, which is the property the pre-registration is.
        """
        import tempfile

        from accuracy_registry import grade_structural, grade_structural_days, load_snapshot
        from make_repro_snapshot import write_snapshot

        con = self._seeded_db()
        db_c = {r["predictor"]: (float("%.10g" % r["claimed"]), r["claimed_source"])
                for r in grade_structural(con) if r["family"] == "structure"}
        self.assertTrue(db_c)
        with tempfile.TemporaryDirectory() as td:
            write_snapshot(con, td)
            _by_h, totals, per_day, pre, naive, _proto = load_snapshot(td)
            snap_c = {r["predictor"]: (float("%.10g" % r["claimed"]), r["claimed_source"])
                      for r in grade_structural_days(totals, per_day, pre, naive)
                      if r["family"] == "structure"}
        self.assertEqual(db_c, snap_c)

    def test_snapshot_missing_the_frozen_claims_refuses_to_grade(self):
        """Deleting the frozen target must abort, not fall back to the DB
        average — that fallback recomputes the number the freeze exists to
        pin, on the one path an outside reader can actually run."""
        import tempfile

        from accuracy_registry import load_snapshot
        from make_repro_snapshot import write_snapshot

        con = self._seeded_db()
        with tempfile.TemporaryDirectory() as td:
            write_snapshot(con, td)
            os.remove(os.path.join(td, "prereg_claims.csv"))
            with self.assertRaises(SystemExit):
                load_snapshot(td)
            # ...and the explicit legacy escape hatch still reads it.
            _by_h, _t, _p, pre, _n, _proto = load_snapshot(td, allow_legacy=True)
            self.assertEqual(pre, {})

    def test_tampered_snapshot_refuses_to_grade(self):
        """One edited cell must fail the manifest hash and abort the grade."""
        import tempfile

        from accuracy_registry import load_snapshot
        from make_repro_snapshot import write_snapshot

        con = self._seeded_db()
        with tempfile.TemporaryDirectory() as td:
            write_snapshot(con, td)
            path = os.path.join(td, "directional_days.csv")
            with open(path, encoding="utf-8") as f:
                header, first, *rest = f.readlines()
            cells = first.strip().split(",")
            cells[3] = str(int(cells[3]) + 1)  # one extra "correct" tally
            with open(path, "w", encoding="utf-8") as f:
                f.writelines([header, ",".join(cells) + "\n", *rest])
            with self.assertRaises(SystemExit):
                load_snapshot(td)

    def test_snapshot_without_a_registered_grader_publishes_no_verdict(self):
        """The reproduce path must fail closed on grader registration.

        require_registered_grader guarded only the DB path — the one nobody
        outside this machine can run — while --snapshot, the path REPRODUCE.md
        hands a third party, checked nothing and printed verdicts anyway. The
        pin now travels in the bundle: with no record naming the grader, every
        verdict field is DROPPED rather than downgraded.
        """
        import tempfile

        from accuracy_registry import load_snapshot, main
        from make_repro_snapshot import write_snapshot

        con = self._seeded_db()
        with tempfile.TemporaryDirectory() as td:
            write_snapshot(con, td)
            # the seeded chain has no grading-protocol record, so the exported
            # file is empty — the honest representation of "nothing names it".
            _b, _t, _p, _pre, _n, proto = load_snapshot(td)
            self.assertIsNone(proto)
            buf = io.StringIO()
            argv = sys.argv
            sys.argv = ["accuracy_registry.py", "--snapshot", td]
            try:
                with contextlib.redirect_stdout(buf):
                    main()
            finally:
                sys.argv = argv
            out = buf.getvalue()
            self.assertIn("UNREGISTERED GRADER", out)
            self.assertNotIn("VALIDATED", out)
            self.assertNotIn("HOLDING", out)

    def test_snapshot_naming_other_code_refuses_to_grade(self):
        """A pin that names a different binary is a hard refusal here, exactly
        as it is on the DB path — and the fix is to re-register the grader,
        never to re-pin graderSha256 at the drifted value."""
        import csv as _csv
        import json as _json
        import tempfile

        import make_repro_snapshot as mrs
        from accuracy_registry import load_snapshot

        con = self._seeded_db()
        with tempfile.TemporaryDirectory() as td:
            write = mrs.write_snapshot(con, td)
            del write
            recs = [["7", "0" * 64, "cafebabe", "30", "10", "10",
                     "0.05", MULTIPLICITY_RULE, "0"]]
            f = "grading_protocol.csv"
            with open(os.path.join(td, f), "w", newline="", encoding="utf-8") as fh:
                w = _csv.writer(fh)
                w.writerow(mrs.HEADERS[f])
                w.writerows(recs)
            man_path = os.path.join(td, "MANIFEST.json")
            man = _json.load(open(man_path, encoding="utf-8"))
            for e in man["files"]:
                if e["file"] == f:
                    e["sha256"] = mrs.hash_records(f, mrs.FILES[f], recs)
                    e["rows"] = 1
            _json.dump(man, open(man_path, "w", encoding="utf-8"), indent=1)
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
            with open(os.path.join(snap_dir, "xsfactor_inputs.csv"), newline="", encoding="utf-8") as f:
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
            "INSUFFICIENT (21/30)",
        ("directional-ensemble (1d, high conviction)", "|p-0.5|>=0.15"):
            "INSUFFICIENT (9/30)",
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
        cls._require_registered_snapshot()
        by_h, totals, per_day, pre, naive, _proto = load_snapshot(cls.REPRO)
        return (grade_directional_days(by_h)
                + grade_structural_days(totals, per_day, pre, naive))

    @classmethod
    def _require_registered_snapshot(cls):
        """Frozen verdicts can only be checked when verdicts are published.

        The shipped snapshot carries the grader pin (repro/grading_protocol.csv)
        and load_snapshot refuses when that pin names other code. When it
        refuses, these frozen-verdict tests have nothing to assert — and the
        right response is to fix the drift (re-register the grader on the chain
        through the normal pre-registration process and re-cut the snapshot),
        NOT to relax the pin so the strings come back. So: skip loudly.
        """
        import csv as _csv
        import accuracy_registry as _reg
        path = os.path.join(cls.REPRO, "grading_protocol.csv")
        if not os.path.exists(path):
            raise AssertionError(
                "the shipped snapshot has no grading_protocol.csv — the reproduce "
                "path would grade with no registered grader")
        with open(path, newline="", encoding="utf-8") as fh:
            rows = list(_csv.reader(fh))[1:]
        rec = None
        if rows:
            seq, sha, commit, n, days, blocks, alpha, rule, looks = rows[0]
            num = lambda v: int(v) if v != "" else None
            rec = {"_seq": int(seq), "graderSha256": sha, "graderCommit": commit or None,
                   "minIndependentN": num(n), "minDistinctDays": num(days),
                   "minDistinctBlocks": num(blocks),
                   "maxAlpha": float(alpha) if alpha != "" else None,
                   "multiplicityRule": rule or None, "_looks": num(looks) or 0}
        err = _reg.grader_registration_error(rec, path)
        if err:
            raise unittest.SkipTest(
                "shipped snapshot does not register this grader, so it publishes no "
                "verdicts to freeze: " + err)

    # kind -> (frozen target C, provenance). Pinned because every structural
    # row above is PENDING: the verdict string cannot detect a wrong C, so a
    # snapshot that recomputed the target from its own tallies would pass the
    # freeze unnoticed. These are the pre-registration chain's values.
    EXPECTED_CLAIMS = {
        "liquidity21": (0.595, "prereg chain"),
        "liquidity21-crypto": (0.795, "prereg chain"),
        "trend21": (0.731, "prereg chain"),
        "trend21-crypto": (0.934, "prereg chain"),
        "trend63": (0.7, "prereg chain"),
        "vol21": (0.558, "prereg chain"),
    }

    def test_structural_targets_come_from_the_frozen_chain(self):
        got = {r["predictor"]: (r["claimed"], r["claimed_source"])
               for r in self._rows() if r["family"] == "structure"}
        self.assertEqual(got, self.EXPECTED_CLAIMS)

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


class TestAutoRetireRule(unittest.TestCase):
    """The pre-registered FAILED-forward kill criterion (2026-07-26, committed
    while every directional verdict was still INSUFFICIENT). The flag — not a
    human reading the report — is what the daemon consumes, so the flag itself
    is pinned here, and the digest is pinned to the SAME constant the Go chain
    test pins (daemon/internal/prereg/retire_rule_test.go), keeping the
    enforced rule and the chained rule provably one rule."""

    # sha256 of the canonical rule string, frozen at registration. Changing the
    # rule legitimately requires updating this pin AND the Go pin in the same
    # commit — and the prereg chain appends an AMENDMENT for the new hash.
    FROZEN_DIGEST = "d02c33740989cde5be82ffce1cec4e1b25fdec41f06dc8a669ac2a79d00bccd5"

    def test_digest_matches_the_chained_constant(self):
        self.assertEqual(auto_retire_rule_digest(), self.FROZEN_DIGEST)
        rule = auto_retire_rule()
        self.assertEqual(rule["sha256"], self.FROZEN_DIGEST)
        self.assertEqual(rule["minIndependentN"], MIN_INDEPENDENT_N)
        self.assertEqual(rule["minDistinctDays"], MIN_DISTINCT_DAYS)

    def test_failed_row_publishes_the_retire_flag(self):
        """A row meeting both evidence floors with its effective-N Wilson upper
        bound below the prequential null must grade FAILED and carry
        retire=true — the exact criterion the chain froze."""
        con = TestSurvivorshipBoundary._db()
        # 20 days x 10 symbols, model calls up (prob .8) into an all-down tape:
        # always wrong, while the prequential bettor converges on "down".
        for day in range(20):
            ts = SURVIVORSHIP_EPOCH_TS + day * 86400
            for sym in range(10):
                con.execute(
                    "INSERT INTO prediction_outcomes VALUES (?, '1d', 0.8, 0, ?, ?)",
                    (sym + 1, ts, ts + 86400))
        row = next(r for r in grade_directional(con) if r["band"] == "all")
        self.assertGreaterEqual(row["live_n"], MIN_INDEPENDENT_N)
        self.assertGreaterEqual(row["distinct_days"], MIN_DISTINCT_DAYS)
        self.assertTrue(row["verdict"].startswith("FAILED"), row["verdict"])
        self.assertLess(row["ci"][1], row["null_acc"])
        self.assertTrue(row["retire"])

    def test_insufficient_row_does_not_retire(self):
        """Below the evidence floors nothing is condemned — INSUFFICIENT rows
        must never carry the flag, or the rule fires before its own gate."""
        con = TestSurvivorshipBoundary._db()
        for day in range(3):
            ts = SURVIVORSHIP_EPOCH_TS + day * 86400
            con.execute("INSERT INTO prediction_outcomes VALUES (?, '1d', 0.8, 0, ?, ?)",
                        (day + 1, ts, ts + 86400))
        row = next(r for r in grade_directional(con) if r["band"] == "all")
        self.assertTrue(row["verdict"].startswith("INSUFFICIENT"), row["verdict"])
        self.assertFalse(row["retire"])

    def test_structural_rows_carry_no_retire_flag(self):
        """The rule is registered for the directional family only; structural
        kinds answer to DECAYED against their frozen claims instead."""
        con = TestSurvivorshipBoundary._db()
        for i in range(2):
            ts = SURVIVORSHIP_EPOCH_TS + i * 86400
            con.execute("INSERT INTO regime_outcomes VALUES (?, 'oversold', 21, ?, ?, ?, 1, 0.82)",
                        (10 + i, ts // 86400, ts, ts + 86400))
        for r in grade_structural(con):
            self.assertNotIn("retire", r)


class TestClaimComesFromTheChain(unittest.TestCase):
    """The grading target must be READ from the pre-registration chain.

    Recomputing it from regime_outcomes let a conviction-mix-weighted average
    move the target after outcomes were visible without breaking a hash. These
    tests fail if that path ever comes back.
    """

    @staticmethod
    def _db_with_chain(spec_claim=0.731, db_acc=0.8177):
        con = TestSurvivorshipBoundary._db()
        con.execute("""CREATE TABLE prereg_records (
            seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, kind TEXT,
            spec_json TEXT, spec_hash TEXT, prev_hash TEXT, entry_hash TEXT, note TEXT)""")
        spec = json.dumps({"kind": "oversold", "bands": [
            {"minConviction": 0.0, "claimedAccuracy": spec_claim},
            {"minConviction": 0.9, "claimedAccuracy": 0.972}]})
        con.execute("INSERT INTO prereg_records (ts, kind, spec_json, spec_hash, prev_hash,"
                    " entry_hash, note) VALUES (1,'oversold',?, 'hash-a','','e1','')", (spec,))
        for i in range(2):
            ts = SURVIVORSHIP_EPOCH_TS + i * 86400
            con.execute("INSERT INTO regime_outcomes VALUES (?, 'oversold', 21, ?, ?, ?, 1, ?)",
                        (10 + i, ts // 86400, ts, ts + 86400, db_acc))
        return con

    def test_target_is_the_min_conviction_band_not_the_db_average(self):
        row = grade_structural(self._db_with_chain())[0]
        self.assertEqual(row["claimed"], 0.731)
        self.assertEqual(row["claimed_source"], "prereg chain")
        self.assertEqual(row["claimed_spec_hash"], "hash-a")
        # The old recomputed number survives only as a labelled diagnostic.
        self.assertAlmostEqual(row["claimed_db_avg"], 0.8177)

    def test_amendment_supersedes_the_original_claim(self):
        con = self._db_with_chain()
        amended = json.dumps({"kind": "oversold",
                              "bands": [{"minConviction": 0.0, "claimedAccuracy": 0.700}]})
        con.execute("INSERT INTO prereg_records (ts, kind, spec_json, spec_hash, prev_hash,"
                    " entry_hash, note) VALUES (2,'oversold',?, 'hash-b','e1','e2','AMENDMENT')",
                    (amended,))
        self.assertEqual(grade_structural(con)[0]["claimed"], 0.700)

    def test_no_chain_record_is_disclosed_not_hidden(self):
        """Falling back to the DB average is allowed only if the row SAYS so."""
        con = TestSurvivorshipBoundary._db()
        ts = SURVIVORSHIP_EPOCH_TS
        con.execute("INSERT INTO regime_outcomes VALUES (1, 'oversold', 21, ?, ?, ?, 1, 0.82)",
                    (ts // 86400, ts, ts + 86400))
        row = grade_structural(con)[0]
        self.assertIn("NO CHAIN RECORD", row["claimed_source"])
        self.assertIsNone(row["claimed_spec_hash"])


class TestNaivePersistenceNull(unittest.TestCase):
    """A structural predictor must be gradable AGAINST the frozen naive-
    persistence null, not only against its own backtest claim. Without a
    baseline the verdict path can only ever say HOLDING/DECAYED — six of eight
    predictors could not receive a failing verdict at all."""

    @staticmethod
    def _db():
        con = sqlite3.connect(":memory:")
        con.execute("""CREATE TABLE prediction_outcomes (
            symbol_id INTEGER, horizon TEXT, prob REAL, up INTEGER,
            ts INTEGER, resolved_at INTEGER)""")
        con.execute("""CREATE TABLE regime_outcomes (
            symbol_id INTEGER, kind TEXT, horizon_days INTEGER, day INTEGER,
            ts INTEGER, resolved_at INTEGER, correct INTEGER,
            historical_accuracy REAL, actual TEXT, naive_label TEXT)""")
        return con

    @staticmethod
    def _seed(con, days=15, per_day=4, model_hits=4, naive=lambda i, j: "up", stride=21):
        """Seed `days` call-days of resolved outcomes with a frozen baseline.

        stride spaces the call days by a full 21-day horizon by default, so each
        one is its own NON-OVERLAPPING forward block — the unit structural
        grading now clusters on. Pass stride=1 to seed the overlapping case.
        """
        sid = 0
        for d in range(days):
            ts = SURVIVORSHIP_EPOCH_TS + d * stride * 86400
            for j in range(per_day):
                sid += 1
                correct = 1 if j < model_hits else 0
                actual = "up" if correct else "down"
                con.execute("INSERT INTO regime_outcomes VALUES "
                            "(?, 'trend21', 21, ?, ?, ?, ?, 0.82, ?, ?)",
                            (sid, ts // 86400, ts, ts + 86400, correct, actual,
                             naive(d, j)))

    def test_partial_null_coverage_refuses_a_skill_verdict(self):
        """A null frozen for only some of the graded rows is not a matched null.

        The uncovered rows are self-selected — the baseline is incomputable
        exactly where the state was degenerate or the history thin — so
        comparing the model's accuracy on all rows against the null's accuracy
        on a subset is a comparison between two samples. It must refuse, not
        qualify."""
        con = self._db()
        self._seed(con, naive=lambda d, j: None if j == 0 else "up")
        rows = {r["predictor"]: r for r in grade_structural(con)}
        row = rows["trend21"]
        self.assertAlmostEqual(row["null_coverage"], 0.75)
        self.assertIn("PARTIAL BASELINE", row["verdict"])
        # No skill verdict may leak through the partial-coverage gate.
        for banned in ("NO SKILL", "VALIDATED", "FAILED"):
            self.assertNotIn(banned, row["verdict"])
        # The benchmark row carries the same coverage, so the gap is legible
        # from the baseline's own row too.
        self.assertAlmostEqual(rows["trend21#persist"]["null_coverage"], 0.75)

    def test_full_null_coverage_publishes_coverage_and_still_grades(self):
        con = self._db()
        self._seed(con)
        row = [r for r in grade_structural(con) if r["predictor"] == "trend21"][0]
        self.assertEqual(row["null_coverage"], 1.0)
        self.assertIn("SKILL", row["verdict"])

    def test_persistence_benchmark_row_is_emitted_and_drives_the_verdict(self):
        con = self._db()
        # The null guesses "up" every time, so it scores exactly what the model
        # scores here — the stickiness case this repo's liquidity caveat warns
        # about. The verdict must therefore be NO SKILL, never HOLDING.
        self._seed(con)
        rows = {r["predictor"]: r for r in grade_structural(con)}
        self.assertIn("trend21#persist", rows)
        bench = rows["trend21#persist"]
        self.assertEqual(bench["family"], "structure-benchmark")
        self.assertEqual(bench["live_n"], rows["trend21"]["live_n"])
        self.assertAlmostEqual(bench["live_acc"], rows["trend21"]["live_acc"])
        self.assertAlmostEqual(rows["trend21"]["skill"], 0.0)
        self.assertTrue(rows["trend21"]["verdict"].startswith("NO SKILL"),
                        rows["trend21"]["verdict"])
        # The claim comparison survives beside it — nothing was removed.
        self.assertIsNotNone(rows["trend21"]["claim_verdict"])

    def test_model_worse_than_the_null_can_now_FAIL(self):
        con = self._db()
        # Model right 1/4 of the time; the frozen "always down" null is right 3/4.
        self._seed(con, model_hits=1, naive=lambda d, j: "down")
        rows = {r["predictor"]: r for r in grade_structural(con)}
        self.assertLess(rows["trend21"]["skill"], 0)
        self.assertTrue(rows["trend21"]["verdict"].startswith("FAILED"),
                        rows["trend21"]["verdict"])

    def test_missing_baseline_is_disclosed_not_silently_skipped(self):
        con = self._db()
        self._seed(con, naive=lambda d, j: None)
        row = [r for r in grade_structural(con) if r["predictor"] == "trend21"][0]
        self.assertIsNone(row["null_acc"])
        self.assertEqual(row["null_n"], 0)
        self.assertIn("NO BASELINE", row["verdict"])
        # A NULL baseline is dropped from the benchmark, never scored as a miss.
        self.assertNotIn("trend21#persist", [r["predictor"] for r in grade_structural(con)])


class TestChainPresenceIsRead(unittest.TestCase):
    """The summary's two anteriority claims must be READ, never asserted.

    A locally recomputed digest proves the script's constants hash to
    themselves; it proves nothing about whether the rule was chained before the
    evidence arrived. Same for the frozen null: a paragraph describing a
    baseline that no column carries is the failure mode the chain exists to
    catch. These tests pin the negative case, which is what a reader needs.
    """

    @staticmethod
    def _bare_db(path):
        """A DB with neither a naive_label column nor a chained retire rule."""
        con = sqlite3.connect(path)
        con.execute("""CREATE TABLE prediction_outcomes (
            symbol_id INTEGER, horizon TEXT, prob REAL, up INTEGER,
            ts INTEGER, resolved_at INTEGER)""")
        con.execute("""CREATE TABLE regime_outcomes (
            symbol_id INTEGER, kind TEXT, horizon_days INTEGER, day INTEGER,
            ts INTEGER, resolved_at INTEGER, correct INTEGER,
            historical_accuracy REAL, actual TEXT)""")
        con.execute("""CREATE TABLE prereg_records (
            seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, kind TEXT,
            spec_json TEXT, spec_hash TEXT, prev_hash TEXT, entry_hash TEXT, note TEXT)""")
        ts = SURVIVORSHIP_EPOCH_TS
        con.execute("INSERT INTO regime_outcomes VALUES (1,'oversold',21,?,?,?,1,0.82,'up')",
                    (ts // 86400, ts, ts + 86400))
        con.commit()
        return con

    def test_absent_baseline_and_absent_chain_read_false(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "t.db")
            self._bare_db(path).close()
            con = sqlite3.connect(path)
            pres = fetch_chain_presence(con)
            con.close()
        self.assertFalse(pres["null_frozen"])
        self.assertFalse(pres["retire_rule_chained"])
        self.assertIsNone(pres["retire_rule_chain_seq"])

    def test_chained_rule_under_other_thresholds_is_not_chained(self):
        """A retire record whose spec_hash is not THIS rule's digest fails."""
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "t.db")
            con = self._bare_db(path)
            con.execute("INSERT INTO prereg_records (ts, kind, spec_json, spec_hash,"
                        " prev_hash, entry_hash, note)"
                        " VALUES (1,'auto-retire-rule','{}','stale-digest','','e1','')")
            con.commit()
            con.close()
            con = sqlite3.connect(path)
            pres = fetch_chain_presence(con)
            con.close()
        self.assertFalse(pres["retire_rule_chained"])
        self.assertEqual(pres["retire_rule_chain_hash"], "stale-digest")

    def test_matching_digest_reads_chained(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "t.db")
            con = self._bare_db(path)
            con.execute("INSERT INTO prereg_records (ts, kind, spec_json, spec_hash,"
                        " prev_hash, entry_hash, note)"
                        " VALUES (1,'auto-retire-rule','{}',?,'','e1','')",
                        (auto_retire_rule_digest(),))
            con.commit()
            con.close()
            con = sqlite3.connect(path)
            pres = fetch_chain_presence(con)
            con.close()
        self.assertTrue(pres["retire_rule_chained"])
        self.assertEqual(pres["retire_rule_chain_seq"], 1)

    def test_column_present_but_never_populated_is_not_frozen(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "t.db")
            con = self._bare_db(path)
            con.execute("ALTER TABLE regime_outcomes ADD COLUMN naive_label TEXT")
            con.commit()
            con.close()
            con = sqlite3.connect(path)
            pres = fetch_chain_presence(con)
            con.close()
        self.assertFalse(pres["null_frozen"])
        self.assertEqual(pres["null_coverage"], 0.0)

    def test_report_prints_the_negatives_and_publishes_false_flags(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "t.db")
            con = self._bare_db(path)
            # The grader now refuses to run at all unless the chain names this
            # exact file under these exact thresholds, so the end-to-end report
            # fixture has to carry a matching grading-protocol record.
            con.execute("INSERT INTO prereg_records (ts, kind, spec_json, spec_hash,"
                        " prev_hash, entry_hash, note)"
                        " VALUES (1,'grading-protocol',?,'h','','e0','')",
                        (json.dumps({"grader": "tools/accuracy_registry.py",
                                     "graderCommit": "0" * 40,
                                     "graderSha256": self_sha256(),
                                     "minIndependentN": MIN_INDEPENDENT_N,
                                     "minDistinctDays": MIN_DISTINCT_DAYS,
                                     "minDistinctBlocks": _MDB,
                                     "maxAlpha": MAX_ALPHA,
                                     "multiplicityRule": MULTIPLICITY_RULE}),))
            con.commit()
            con.close()
            out_json = os.path.join(d, "registry.json")
            buf = io.StringIO()
            argv = sys.argv
            sys.argv = ["accuracy_registry.py", "--db", path, "--json", out_json]
            try:
                with contextlib.redirect_stdout(buf):
                    registry_main()
            finally:
                sys.argv = argv
            text = buf.getvalue()
            with open(out_json, encoding="utf-8") as f:
                payload = json.load(f)
        self.assertIn("Structural null: NOT FROZEN", text)
        self.assertIn("Auto-retire rule: NOT ON CHAIN", text)
        self.assertNotIn("Structural null: NAIVE PERSISTENCE", text)
        self.assertIs(payload["null_frozen"], False)
        self.assertIs(payload["retire_rule_chained"], False)
        self.assertIsNone(payload["retire_rule_chain_seq"])



class TestHorizonBlockClustering(unittest.TestCase):
    """A 21-day structural call made on 21 consecutive days is ONE forward
    window, not 21. Grading must cluster on non-overlapping horizon blocks."""

    def test_consecutive_call_days_collapse_into_one_block(self):
        rows = [(1000 + d, 10, 7) for d in range(21)]
        blocks = horizon_blocks(rows, 21)
        self.assertEqual(len(blocks), 1)
        self.assertEqual(blocks[0][1:], (210, 147))

    def test_anchors_are_always_at_least_a_horizon_apart(self):
        """Property: for ANY day set and ANY horizon, consecutive block anchors
        are >= h days apart, so no two blocks share a forward window."""
        rng = random.Random(20260727)
        for _ in range(300):
            h = rng.randint(1, 30)
            span = rng.randint(1, 200)
            days = rng.sample(range(1000, 1000 + span),
                              k=rng.randint(1, min(40, span)))
            rows = [(d, rng.randint(1, 9), rng.randint(0, 9)) for d in days]
            blocks = horizon_blocks(rows, h)
            anchors = [b for b, _n, _hh in blocks]
            self.assertEqual(anchors, sorted(anchors))
            for a, b in zip(anchors, anchors[1:]):
                self.assertGreaterEqual(b - a, h)
            self.assertEqual(sum(n for _b, n, _hh in blocks),
                             sum(n for _d, n, _hh in rows))

    def test_horizon_one_is_the_identity(self):
        rows = [(1000 + d, 10, 7) for d in range(21)]
        self.assertEqual([(d, n, h) for d, n, h in horizon_blocks(rows, 1)], rows)

    def test_overlapping_days_cannot_buy_an_interval(self):
        """30 consecutive call days clear the old 10-DAY gate but hold barely
        two independent forward windows — no interval may be published."""
        rows = [(1000 + d, 40, 30) for d in range(30)]
        g = clustered_ci_blocks(rows, 21)
        self.assertEqual(g["distinct_days"], 30)
        self.assertGreaterEqual(g["distinct_days"], MIN_DISTINCT_DAYS)
        self.assertLess(g["distinct_blocks"], MIN_DISTINCT_BLOCKS)
        self.assertIsNone(g["ci"])
        self.assertIn("horizon blocks", g["ci_reason"])

    def test_spaced_windows_publish_a_block_clustered_interval(self):
        rows = [(1000 + d * 21, 40, 30) for d in range(MIN_DISTINCT_BLOCKS)]
        g = clustered_ci_blocks(rows, 21)
        self.assertEqual(g["distinct_blocks"], MIN_DISTINCT_BLOCKS)
        self.assertEqual(g["ci_method"], "block-clustered-wilson")
        self.assertIsNotNone(g["ci"])
        self.assertEqual(g["block_span"], MIN_DISTINCT_BLOCKS)

    def test_blocking_never_narrows_the_interval(self):
        """Pooling overlapping days can only shrink the effective sample."""
        rows = [(1000 + d, 40, 28 + (d % 3)) for d in range(63)]
        by_day = clustered_ci([(n, h) for _d, n, h in rows])
        by_block = clustered_ci_blocks(rows, 21)
        self.assertAlmostEqual(by_day["acc"], by_block["acc"])  # accuracy untouched
        self.assertLessEqual(by_block["effective_n"] or 0, by_day["effective_n"])

    def test_structural_row_publishes_the_block_evidence(self):
        con = TestNaivePersistenceNull._db()
        TestNaivePersistenceNull._seed(con, days=12)
        row = [r for r in grade_structural(con) if r["predictor"] == "trend21"][0]
        self.assertEqual(row["horizon_days"], 21)
        self.assertEqual(row["distinct_blocks"], 12)
        self.assertEqual(row["distinct_days"], 12)
        self.assertEqual(row["ci_method"], "block-clustered-wilson")

    def test_overlapping_structural_record_yields_no_verdict(self):
        con = TestNaivePersistenceNull._db()
        TestNaivePersistenceNull._seed(con, days=30, stride=1)
        row = [r for r in grade_structural(con) if r["predictor"] == "trend21"][0]
        self.assertGreaterEqual(row["live_n"], MIN_INDEPENDENT_N)
        self.assertIn("INSUFFICIENT BLOCKS", row["verdict"])
        self.assertIsNone(row["ci"])


class TestChainedProtocolMatchesThisGrader(unittest.TestCase):
    """The chained grading protocol must name the gates this grader applies.

    PREREGISTRATION.md makes the CHAIN authoritative wherever prose and chain
    disagree, so the one machine-readable artifact a verifier checks has to
    describe the gate that actually decides verdicts. The Go side asserts the
    same equality from the other direction (daemon/internal/prereg/
    prereg_test.go); this mirror means neither language can be edited alone
    without a failure that names which side moved.
    """

    GO_PROTOCOL = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                               "daemon", "internal", "prereg", "prereg.go")

    def _go_field(self, name: str) -> int:
        src = open(self.GO_PROTOCOL, encoding="utf-8").read()
        m = re.search(rf"^\s*{name}:\s*(\d+),\s*$", src, re.M)
        self.assertIsNotNone(
            m, f"GradingProtocol() no longer registers {name} — the chain would pin a "
               f"protocol that omits a gate this grader enforces")
        return int(m.group(1))

    def test_gates_agree_across_languages(self):
        for go_name, py_name, py_val in (
            ("MinIndependentN", "MIN_INDEPENDENT_N", MIN_INDEPENDENT_N),
            ("MinDistinctDays", "MIN_DISTINCT_DAYS", MIN_DISTINCT_DAYS),
            ("MinDistinctBlocks", "MIN_DISTINCT_BLOCKS", MIN_DISTINCT_BLOCKS),
        ):
            self.assertEqual(
                self._go_field(go_name), py_val,
                f"{py_name} in this grader and {go_name} in the chained protocol disagree — "
                f"the registered gate and the executed gate have diverged")


class TestGraderMustBeRegistered(unittest.TestCase):
    """The grader has to prove it is the grader the chain froze.

    PREREGISTRATION.md §2 promises the grading script is pinned by commit and
    content SHA-256 and that an edited grader re-digests differently. Nothing
    enforced that from this side: the script had no self-hash at all, so it
    would publish verdicts under a protocol naming entirely different bytes.
    Every assertion here is a REFUSAL — none of them can let a verdict through.
    """

    @staticmethod
    def _con(**overrides):
        con = sqlite3.connect(":memory:")
        con.execute("""CREATE TABLE prereg_records (
            seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, kind TEXT,
            spec_json TEXT, spec_hash TEXT, prev_hash TEXT, entry_hash TEXT, note TEXT)""")
        rec = {"grader": "tools/accuracy_registry.py",
               "graderCommit": "0" * 40,
               "graderSha256": self_sha256(),
               "minIndependentN": MIN_INDEPENDENT_N,
               "minDistinctDays": MIN_DISTINCT_DAYS,
               "minDistinctBlocks": _MDB,
               "maxAlpha": MAX_ALPHA,
               "multiplicityRule": MULTIPLICITY_RULE}
        rec.update(overrides)
        for k, v in list(rec.items()):
            if v is None:
                del rec[k]
        con.execute("INSERT INTO prereg_records (ts, kind, spec_json, spec_hash, prev_hash,"
                    " entry_hash, note) VALUES (1,'grading-protocol',?,'h','','e1','')",
                    (json.dumps(rec),))
        return con

    def test_matching_record_is_accepted(self):
        rec = require_registered_grader(self._con())
        self.assertEqual(rec["graderSha256"], self_sha256())

    def test_digest_mismatch_refuses(self):
        with self.assertRaises(SystemExit) as cm:
            require_registered_grader(self._con(graderSha256="de" * 32))
        msg = str(cm.exception)
        self.assertIn("UNREGISTERED GRADER", msg)
        # Both digests must be printed, or the operator cannot tell WHICH
        # version drifted — the chain's or the file's.
        self.assertIn("de" * 32, msg)
        self.assertIn(self_sha256(), msg)

    def test_missing_record_refuses(self):
        con = sqlite3.connect(":memory:")
        con.execute("""CREATE TABLE prereg_records (
            seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, kind TEXT,
            spec_json TEXT, spec_hash TEXT, prev_hash TEXT, entry_hash TEXT, note TEXT)""")
        with self.assertRaises(SystemExit) as cm:
            require_registered_grader(con)
        self.assertIn("UNREGISTERED GRADER", str(cm.exception))

    def test_protocol_without_min_distinct_blocks_refuses(self):
        """MIN_DISTINCT_BLOCKS is the gate that decides STRUCTURAL verdicts.

        A chained protocol that froze only the day floor left the strictest gate
        in this grader unregistered, and therefore free to move between
        registration and grading — the exact leak the freeze exists to close.
        """
        with self.assertRaises(SystemExit) as cm:
            require_registered_grader(self._con(minDistinctBlocks=None))
        self.assertIn("minDistinctBlocks", str(cm.exception))

    def test_loosened_threshold_in_the_chain_refuses(self):
        with self.assertRaises(SystemExit) as cm:
            require_registered_grader(self._con(minIndependentN=5))
        self.assertIn("minIndependentN", str(cm.exception))


class TestRevisionGate(unittest.TestCase):
    """A verdict whose rows name no resolvable code is not publishable.

    The daemon read its own vcs.revision from the day it shipped and stored it
    nowhere per-row, so "what code produced this number" was unanswerable for
    every ledger row in the database. The stamp fixes that forward; this gate
    is what makes the stamp load-bearing rather than decorative.
    """

    @staticmethod
    def _con(rev):
        con = sqlite3.connect(":memory:")
        con.execute("""CREATE TABLE regime_outcomes (
            symbol_id INTEGER, kind TEXT, ts INTEGER, revision TEXT)""")
        con.execute("""CREATE TABLE prediction_ledger (
            predicted_at INTEGER, horizon TEXT, revision TEXT)""")
        con.execute("INSERT INTO regime_outcomes VALUES (1,'trend21',?,?)",
                    (REVISION_EPOCH_TS + 86400, rev))
        return con

    def test_dirty_binary_blocks_the_verdict(self):
        gate = revision_gate(self._con("a" * 40 + "+dirty"))
        self.assertIn("trend21", gate["structure"])
        rows = [{"family": "structure", "predictor": "trend21", "verdict": "HOLDING"}]
        refusals = apply_revision_gate(rows, gate)
        self.assertNotIn("verdict", rows[0])
        self.assertTrue(refusals)
        self.assertIn("+dirty", refusals[0])

    def test_unstamped_post_epoch_row_blocks_the_verdict(self):
        gate = revision_gate(self._con(None))
        self.assertEqual(gate["structure"]["trend21"], ["(unstamped)"])

    def test_revision_absent_from_the_repo_blocks_the_verdict(self):
        # A well-formed 40-hex string that is not a commit here.
        gate = revision_gate(self._con("f" * 40))
        self.assertIn("trend21", gate["structure"])

    def test_pre_epoch_rows_are_exempt_and_not_invented(self):
        """Rows frozen before the column existed keep NULL, honestly.

        Their code is unrecoverable; refusing every historical verdict on that
        basis would be theatre, and inventing a revision would be worse. The
        boundary is the epoch, and the summary discloses it.
        """
        con = sqlite3.connect(":memory:")
        con.execute("""CREATE TABLE regime_outcomes (
            symbol_id INTEGER, kind TEXT, ts INTEGER, revision TEXT)""")
        con.execute("""CREATE TABLE prediction_ledger (
            predicted_at INTEGER, horizon TEXT, revision TEXT)""")
        con.execute("INSERT INTO regime_outcomes VALUES (1,'trend21',?,NULL)",
                    (REVISION_EPOCH_TS - 86400,))
        gate = revision_gate(con)
        self.assertEqual(gate["structure"], {})

    def test_missing_column_is_reported_as_unchecked_not_clean(self):
        con = sqlite3.connect(":memory:")
        con.execute("CREATE TABLE regime_outcomes (symbol_id INTEGER, kind TEXT, ts INTEGER)")
        gate = revision_gate(con)
        self.assertFalse(gate["checked"])

    def test_missing_column_fails_closed_and_strips_every_verdict(self):
        """The branch that can check the LEAST must not be the one that
        publishes the most. A database with no revision column attributes no
        row to any build, so no row keeps a verdict."""
        con = sqlite3.connect(":memory:")
        con.execute("CREATE TABLE regime_outcomes (symbol_id INTEGER, kind TEXT, ts INTEGER)")
        gate = revision_gate(con)
        rows = [{"family": "structure", "predictor": "trend21", "verdict": "HOLDING"},
                {"family": "structure", "predictor": "trend21#persist",
                 "verdict": "HOLDING"},
                {"family": "direction", "predictor": "ensemble 1d", "verdict": "HOLDING",
                 "claim_verdict": "HOLDING"}]
        refusals = apply_revision_gate(rows, gate)
        for r in rows:
            self.assertNotIn("verdict", r)
            self.assertNotIn("claim_verdict", r)
        self.assertEqual(len(refusals), 3)
        # Kept for diagnostics, but it no longer buys anyone a verdict.
        self.assertFalse(gate["checked"])


class TestNullAmendmentProbe(unittest.TestCase):
    """The store is supposed to refuse a post-amendment write with no frozen
    naive baseline. Whether that guard is RUNNING is a fact about the deployed
    binary, so it is read off the live table rather than off the code path."""

    @staticmethod
    def _con(ts_offset, naive="up"):
        con = sqlite3.connect(":memory:")
        con.execute("""CREATE TABLE regime_outcomes (
            symbol_id INTEGER, kind TEXT, ts INTEGER, naive_label TEXT)""")
        con.execute("INSERT INTO regime_outcomes VALUES (1,'trend21',?,?)",
                    (NULL_AMENDMENT_EPOCH_TS + ts_offset, naive))
        return con

    def test_clean_table_refuses_nothing(self):
        probe = null_amendment_probe(self._con(86400))
        self.assertEqual(probe["count"], 0)
        rows = [{"family": "structure", "predictor": "trend21", "verdict": "HOLDING"}]
        self.assertEqual(apply_null_amendment_probe(rows, probe), [])
        self.assertIn("verdict", rows[0])

    def test_pre_epoch_null_is_left_alone(self):
        probe = null_amendment_probe(self._con(-86400, naive=None))
        self.assertEqual(probe["count"], 0)

    def test_post_epoch_null_refuses_the_affected_kind(self):
        con = self._con(4200, naive=None)
        probe = null_amendment_probe(con)
        self.assertEqual(probe["count"], 1)
        self.assertEqual(probe["kinds"], ["trend21"])
        self.assertEqual(probe["newest_ts"], NULL_AMENDMENT_EPOCH_TS + 4200)
        rows = [{"family": "structure", "predictor": "trend21", "verdict": "HOLDING",
                 "claim_verdict": "HOLDING"},
                {"family": "structure", "predictor": "trend21#persist",
                 "verdict": "HOLDING"},
                {"family": "structure", "predictor": "vol21", "verdict": "HOLDING"},
                {"family": "direction", "predictor": "ensemble 1d", "verdict": "HOLDING"}]
        refusals = apply_null_amendment_probe(rows, probe)
        self.assertEqual(len(refusals), 2)
        self.assertNotIn("verdict", rows[0])
        self.assertNotIn("claim_verdict", rows[0])
        self.assertNotIn("verdict", rows[1])
        # Only the contaminated kind is refused; the gate can only withhold.
        self.assertIn("verdict", rows[2])
        self.assertIn("verdict", rows[3])

    def test_absent_column_is_handled_by_the_not_frozen_path(self):
        con = sqlite3.connect(":memory:")
        con.execute("CREATE TABLE regime_outcomes (symbol_id INTEGER, kind TEXT, ts INTEGER)")
        self.assertEqual(null_amendment_probe(con)["count"], 0)


class TestMultiplicityCorrection(unittest.TestCase):
    """The published interval must price the family AND the looks.

    Every assertion here is about the correction being one-directional. A
    divisor that could ever NARROW an interval would be a loosening wearing a
    correction's clothes, which is the failure mode this whole mechanism exists
    to avoid.
    """

    def tearDown(self):
        set_multiplicity(1, 1)  # leave the module as found

    def test_single_test_case_reproduces_the_old_hard_coded_z(self):
        z, divisor, alpha = corrected_z(1, 1)
        self.assertEqual(divisor, 1)
        self.assertAlmostEqual(alpha, MAX_ALPHA)
        self.assertAlmostEqual(z, 1.959964, places=5)

    def test_divisor_prices_both_the_family_and_the_looks(self):
        self.assertEqual(corrected_z(10, 7)[1], 70)
        self.assertEqual(corrected_z(10, 7)[2], MAX_ALPHA / 70)

    def test_more_families_or_more_looks_only_widen(self):
        base = corrected_z(1, 1)[0]
        family = corrected_z(10, 1)[0]
        looks = corrected_z(1, 10)[0]
        both = corrected_z(10, 10)[0]
        self.assertGreater(family, base)
        self.assertGreater(looks, base)
        self.assertGreater(both, family)
        self.assertGreater(both, looks)

    def test_correction_can_never_narrow_below_the_uncorrected_interval(self):
        for fam, looks in [(0, 0), (1, 1), (-5, -5), (3, 1), (1, 40)]:
            lo_c, hi_c = wilson_eff(0.6, 100, z=corrected_z(fam, looks)[0])
            lo_u, hi_u = wilson_eff(0.6, 100, z=corrected_z(1, 1)[0])
            self.assertLessEqual(lo_c, lo_u + 1e-12)
            self.assertGreaterEqual(hi_c, hi_u - 1e-12)

    def test_point_estimate_is_untouched_by_the_divisor(self):
        days = [(40, 24)] * 12
        set_multiplicity(1, 1)
        a = clustered_ci(days)
        set_multiplicity(12, 30)
        b = clustered_ci(days)
        self.assertEqual(a["acc"], b["acc"])
        self.assertEqual(a["n"], b["n"])
        self.assertEqual(a["effective_n"], b["effective_n"])
        # ...and the interval it publishes is strictly wider.
        self.assertLess(b["ci"][0], a["ci"][0])
        self.assertGreater(b["ci"][1], a["ci"][1])

    def test_a_verdict_can_only_be_lost_never_gained(self):
        """A row that VALIDATED at one look must not VALIDATE under many."""
        days = [(30, 17)] * 12  # ~56.7% against a 50% null
        set_multiplicity(1, 1)
        g = clustered_ci(days)
        one = verdict_for(g["acc"], g["ci"][0], g["ci"][1], g["n"], 0.5, None,
                          distinct_days=g["distinct_days"])
        self.assertTrue(one.startswith("VALIDATED"), one)
        set_multiplicity(12, 400)
        g = clustered_ci(days)
        many = verdict_for(g["acc"], g["ci"][0], g["ci"][1], g["n"], 0.5, None,
                           distinct_days=g["distinct_days"])
        self.assertTrue(many.startswith("NO SKILL"), many)

    def test_family_size_is_measured_from_the_rows_actually_published(self):
        rows = grade_with_multiplicity(lambda: [{"predictor": str(i)} for i in range(9)], 4)
        self.assertEqual(len(rows), 9)
        for r in rows:
            self.assertEqual(r["family_size"], 9)
            self.assertEqual(r["looks"], 4)
            self.assertEqual(r["divisor"], 36)
            self.assertAlmostEqual(r["corrected_alpha"], MAX_ALPHA / 36)

    def test_look_counter_cannot_be_refunded_by_a_truncated_chain(self):
        con = sqlite3.connect(":memory:")
        con.execute("""CREATE TABLE prereg_records (
            seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, kind TEXT,
            spec_json TEXT, spec_hash TEXT, prev_hash TEXT, entry_hash TEXT, note TEXT)""")
        # Six looks taken, five of the records lost to a truncated chain: the
        # counter the surviving record carries still charges for all six.
        con.execute("INSERT INTO prereg_records (ts, kind, spec_json, spec_hash, prev_hash,"
                    " entry_hash, note) VALUES (1,'grading-look',?,'h','','e','')",
                    (json.dumps({"counter": 6, "gradedAt": "2026-08-07T06:00:00"}),))
        self.assertEqual(chain_looks(con), 6)

    def test_look_counter_counts_records_when_counters_are_unreadable(self):
        con = sqlite3.connect(":memory:")
        con.execute("""CREATE TABLE prereg_records (
            seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, kind TEXT,
            spec_json TEXT, spec_hash TEXT, prev_hash TEXT, entry_hash TEXT, note TEXT)""")
        for i in range(3):
            con.execute("INSERT INTO prereg_records (ts, kind, spec_json, spec_hash, prev_hash,"
                        " entry_hash, note) VALUES (1,'grading-look','not json','h','','e','')")
        self.assertEqual(chain_looks(con), 3)

    def test_absent_chain_reads_zero_rather_than_raising(self):
        con = sqlite3.connect(":memory:")
        self.assertEqual(chain_looks(con), 0)

    def test_the_registered_rule_matches_the_daemons_frozen_string(self):
        """One rule, two languages. A drift here means the grader refuses."""
        go = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                          "daemon", "internal", "prereg", "prereg.go")
        src = open(go, encoding="utf-8").read()
        start = src.index("const MultiplicityRule =")
        end = src.index("\n\n", start)
        literal = "".join(re.findall(r'"((?:[^"\\]|\\.)*)"', src[start:end]))
        self.assertEqual(literal, MULTIPLICITY_RULE,
                         "prereg.MultiplicityRule and MULTIPLICITY_RULE have diverged — the "
                         "grader would refuse to grade against its own chain")

    def test_an_unregistered_multiplicity_rule_refuses_to_grade(self):
        rec = {"_seq": 1, "graderSha256": self_sha256(), "graderCommit": "0" * 40,
               "minIndependentN": MIN_INDEPENDENT_N, "minDistinctDays": MIN_DISTINCT_DAYS,
               "minDistinctBlocks": MIN_DISTINCT_BLOCKS, "maxAlpha": MAX_ALPHA,
               "multiplicityRule": "divisor is always 1"}
        err = grader_registration_error(rec, "a test chain")
        self.assertIsNotNone(err)
        self.assertIn("multiplicityRule", err)


class ProtocolDocumentGateTest(unittest.TestCase):
    """The publishing path must refuse an unregistered PROTOCOL DOCUMENT.

    require_registered_grader() already refuses when the chain does not name the
    running grader. The document PREREGISTRATION.md is the other half of the same
    commitment — §0 makes the chain authoritative over the prose — and until the
    gate below existed, a document describing a protocol no record froze could
    still front a published verdict. These tests pin the gate's SHAPE, not its
    verdict: they assert it is present, reads the digest fresh, and routes into
    the existing fail-closed refusal path rather than warning.
    """

    def setUp(self):
        self.script = os.path.join(
            os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
            "ops", "accuracy-registry.sh")
        self.src = open(self.script, encoding="utf-8").read()

    def test_publishing_path_recomputes_the_document_digest(self):
        # The digest must be computed FROM THE FILE on every publish, never read
        # from a cache or a variable set earlier. This asserted the literal
        # `shasum -a 256` call until sha256_of replaced it: shasum ships with
        # macOS and is absent under Git Bash on Windows, where the hardcoded call
        # made the whole publishing path unrunnable. The requirement is fresh
        # computation, not one particular hashing binary, so pin the behaviour.
        self.assertRegex(
            self.src, r'doc_hash=\$\((?:sha256_of|shasum[^)]*)\s+"\$SD/PREREGISTRATION\.md"',
            "the publishing path does not recompute PREREGISTRATION.md's digest from the "
            "file, so a document that drifted from its chain record could still front a verdict")
        self.assertIn("kind = 'prereg-document'", self.src,
                      "the gate does not read the newest prereg-document record to compare against")

    def test_digest_helper_hashes_file_contents(self):
        """sha256_of must hash the FILE, not echo a name or reuse a cached value.

        Pinning the behaviour above only helps if the helper it now allows
        actually computes a digest, so run it and compare against hashlib.
        """
        import hashlib
        import subprocess
        bash = _repo_visible_bash()
        if not bash:
            self.skipTest("no bash that can see the repo path")
        target = os.path.normpath(
            os.path.join(os.path.dirname(self.script), "..", "PREREGISTRATION.md"))
        if not os.path.exists(target):
            self.skipTest("PREREGISTRATION.md not present")
        # bash treats backslashes in a double-quoted string as escapes, so a
        # Windows path must be handed over with forward slashes.
        target_sh = target.replace("\\", "/")
        with open(target, "rb") as f:
            want = hashlib.sha256(f.read()).hexdigest()
        # Source only the interpreter-detection + helper block, then call it.
        # Running the whole script would grade the database, and starting the
        # slice any earlier picks up the SD= line, whose ${BASH_SOURCE[0]} is
        # unbound under `bash -c` and aborts the prelude under `set -u`.
        prelude = self.src[self.src.index('PY=""'):self.src.index('LOG="$SD/logs')]
        got = subprocess.run(
            [bash, "-c", prelude + f'\nsha256_of "{target_sh}"'],
            capture_output=True, text=True, timeout=60).stdout.strip()
        self.assertEqual(want, got,
                         "sha256_of does not reproduce the file's SHA-256, so the "
                         "protocol-document gate would compare against a wrong digest")

    def test_a_mismatched_document_takes_the_existing_refusal_path(self):
        """It must set refusal_reason — the same fail-closed branch the grader
        outage uses — not print a warning and continue."""
        gate = self.src[self.src.index("PROTOCOL-DOCUMENT REGISTRATION"):]
        gate = gate[:gate.index("SURVIVORSHIP BOUND")]
        self.assertIn("UNREGISTERED PROTOCOL DOCUMENT", gate)
        self.assertEqual(2, gate.count("refusal_reason="),
                         "the gate must refuse on BOTH failure modes: no prereg-document record "
                         "at all, and a record whose specHash is not the document's digest")
        self.assertNotIn("|| true", gate,
                         "the gate is best-effort; an unregistered protocol document must block "
                         "publication, not be swallowed")


if __name__ == "__main__":
    unittest.main()


class TestBoundaryNullCannotManufactureAFailure(unittest.TestCase):
    """A verdict may never be produced by a difference smaller than the
    arithmetic that computed it.

    Found 2026-07-27. The Wilson upper bound at p = 1 is EXACTLY 1 in real
    arithmetic — centre + half = (1 + z²/2n + z²/2n) / (1 + z²/n) — and comes
    back as 0.9999999999999999 in float64. verdict_for compared `hi < null_acc`
    with no tolerance, so against a null of 1.0 the strict inequality was TRUE
    for every possible record, INCLUDING a perfect one: a predictor that scored
    1.0 against a null of 1.0 (skill exactly 0.0) was published as "FAILED —
    significantly worse than the naive baseline".

    That verdict is not merely wrong on one row. FAILED is the verdict that sets
    retire=true, which the daemon's model-health worker reads to stop publishing
    a horizon — so against a 100% null the outcome was algebraically constant
    regardless of the model's accuracy. The live registry HAS published a null
    of 100.0% (README, 2026-07-26 run); only the 30-observation floor
    short-circuited before the comparison was reached.
    """

    def test_wilson_upper_bound_is_exactly_one_at_p_one(self):
        _, hi = wilson(40, 40)
        self.assertEqual(hi, 1.0,
                         "Wilson's upper bound at p=1 is algebraically 1; a float "
                         "epsilon below it turns every comparison against a 100% null")

    def test_wilson_lower_bound_is_exactly_zero_at_p_zero(self):
        lo, _ = wilson(0, 40)
        self.assertEqual(lo, 0.0)

    def test_perfect_record_tying_a_perfect_null_is_not_a_failure(self):
        # The interval is the one the clustered path actually produces for a
        # perfect record at effective n = 15, not a hand-written 1.0.
        lo, hi = wilson_eff(1.0, 15)
        v = verdict_for(acc=1.0, lo=lo, hi=hi, n=60,
                        null_acc=1.0, claimed=None, null_coverage=1.0)
        self.assertNotIn("FAILED", v, v)
        self.assertIn("NO SKILL", v, v)

    def test_epsilon_gap_cannot_manufacture_either_verdict(self):
        """Symmetric: the tolerance must not hand out VALIDATED either."""
        eps = 1e-13
        self.assertIn("NO SKILL", verdict_for(acc=0.6, lo=0.5 + eps, hi=0.9, n=60,
                                              null_acc=0.5, claimed=None, null_coverage=1.0))
        self.assertIn("NO SKILL", verdict_for(acc=0.4, lo=0.1, hi=0.5 - eps, n=60,
                                              null_acc=0.5, claimed=None, null_coverage=1.0))

    def test_a_real_gap_still_grades(self):
        """Nothing was loosened: a genuinely separated interval still decides."""
        self.assertIn("FAILED", verdict_for(acc=0.30, lo=0.20, hi=0.40, n=60,
                                            null_acc=0.55, claimed=None, null_coverage=1.0))
        self.assertIn("VALIDATED", verdict_for(acc=0.70, lo=0.62, hi=0.80, n=60,
                                               null_acc=0.55, claimed=None, null_coverage=1.0))


class TestGraderRefusesWithoutResearchLiveness(unittest.TestCase):
    """The liveness precondition belongs to the GRADER, not to one caller.

    ops/accuracy-registry.sh ran tools/research_liveness.py before the grader and
    refused to publish on failure — but the artifact it protects,
    data/accuracy_registry.json, is what README.md and /accuracy read, and it can
    also be produced by invoking the grader directly. That is how the registry
    dated 2026-07-27T01:01 came to exist while the liveness check was failing on
    two claims (a narrated 48-rule grid search with no judgment ledger, and a
    pre-registration that never forecast).
    """

    @staticmethod
    def _db(with_claim: bool):
        con = sqlite3.connect(":memory:")
        con.execute("""CREATE TABLE worker_runs (
            id INTEGER PRIMARY KEY, worker TEXT, status TEXT,
            started_at INTEGER, finished_at INTEGER, detail TEXT)""")
        if with_claim:
            con.execute(
                "INSERT INTO worker_runs VALUES (1,'research-loop','ok',?,?,?)",
                (SURVIVORSHIP_EPOCH_TS, SURVIVORSHIP_EPOCH_TS + 60,
                 "searched a 48-rule grid over 166285 observations — "
                 "NOTHING survived Bonferroni correction. That is a result, not a failure"))
        return con

    def test_narrated_search_without_a_ledger_refuses_the_grade(self):
        con = self._db(with_claim=True)
        with contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit) as cm:
                require_research_liveness(con, ":memory:")
        msg = str(cm.exception)
        self.assertIn("RESEARCH-LOOP LIVENESS FAILED", msg)
        self.assertIn("48-rule grid", msg)

    def test_clean_database_grades_normally(self):
        con = self._db(with_claim=False)
        require_research_liveness(con, ":memory:")  # must not raise

    def test_a_database_that_narrated_nothing_is_not_a_violation(self):
        """No worker_runs table means no narration to corroborate — not a refusal.

        Distinguishing this from an unreadable ledger matters: the check exists
        precisely because 'absent' and 'unreadable' must not be confused, so only
        the exact missing-worker_runs case passes and every other query failure
        still refuses.
        """
        con = sqlite3.connect(":memory:")
        require_research_liveness(con, ":memory:")  # must not raise

#!/usr/bin/env python3
"""Tests for the research-loop liveness check.

The check exists because worker_runs narrated "searched a 48-rule grid …
NOTHING survived Bonferroni correction" against a database with zero
research_loop_runs rows and no research_loop_judgments table at all. These tests
pin the three ways that narration can fail to be corroborated, and pin the
check's read-only character: it may refuse, never repair.

Run: python3 -m unittest discover -s tools -p 'test_*.py'
"""
import calendar
import os
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from research_liveness import (  # noqa: E402
    PREREG_GRACE_DAYS, check_liveness, check_prereg_forecasts, main)

DAY = "2026-07-25"
TS = calendar.timegm((2026, 7, 25, 12, 0, 0, 0, 0, 0))
NARRATION = ("searched a 48-rule grid over 166285 observations — NOTHING "
             "survived Bonferroni correction. That is a result, not a failure")


def make_db(path, *, judgments_table=True, runs_row=True, judged=48,
            n_judgments=48, narrated=True, status="ok"):
    conn = sqlite3.connect(path)
    conn.executescript("""
      CREATE TABLE worker_runs (
        id INTEGER PRIMARY KEY, worker TEXT NOT NULL, started_at INTEGER NOT NULL,
        finished_at INTEGER, status TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '');
      CREATE TABLE research_loop_runs (
        day TEXT PRIMARY KEY, ran_at INTEGER NOT NULL, grid_size INTEGER NOT NULL,
        divisor INTEGER NOT NULL, corrected_alpha REAL NOT NULL,
        obs_count INTEGER NOT NULL, obs_ts_from INTEGER NOT NULL,
        obs_ts_to INTEGER NOT NULL, survivors INTEGER NOT NULL,
        judged INTEGER NOT NULL DEFAULT 0, refusal_reason TEXT NOT NULL DEFAULT '',
        git_rev TEXT NOT NULL DEFAULT '');
    """)
    if judgments_table:
        conn.execute("""CREATE TABLE research_loop_judgments (
            day TEXT NOT NULL, rule_id TEXT NOT NULL, status TEXT NOT NULL,
            wilson_lower REAL NOT NULL, p0 REAL NOT NULL, divisor INTEGER NOT NULL,
            grid_size INTEGER NOT NULL, weeks INTEGER NOT NULL,
            rejected_by TEXT NOT NULL DEFAULT '', obs_window TEXT NOT NULL DEFAULT '',
            PRIMARY KEY (day, rule_id))""")
        for i in range(n_judgments):
            conn.execute(
                "INSERT INTO research_loop_judgments VALUES (?,?,?,?,?,?,?,?,?,?)",
                (DAY, f"rule-{i}", "killed", 0.4, 0.5, 480, 48, 30, "bonferroni", ""))
    conn.execute(
        "INSERT INTO worker_runs (id, worker, started_at, finished_at, status, detail) "
        "VALUES (?,?,?,?,?,?)",
        (139413, "research-loop", TS, TS + 300, status,
         NARRATION if narrated else "skip — already ran today"))
    if runs_row:
        conn.execute("INSERT INTO research_loop_runs VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
                     (DAY, TS, 48, 480, 0.001, 166285, TS - 99, TS, 0, judged, "", "abc"))
    conn.commit()
    conn.close()


class LivenessCase(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.path = os.path.join(self.dir.name, "signaldeck.db")

    def violations(self, **kw):
        if os.path.exists(self.path):
            os.remove(self.path)
        make_db(self.path, **kw)
        conn = sqlite3.connect(f"file:{self.path}?mode=ro", uri=True)
        try:
            return check_liveness(conn)
        finally:
            conn.close()


class TestHonestLedger(LivenessCase):
    def test_complete_record_passes(self):
        self.assertEqual(self.violations(), [])
        self.assertEqual(main(["--db", self.path]), 0)

    def test_skip_row_claims_nothing(self):
        """'skip — already ran today' narrates no search, so needs no ledger."""
        self.assertEqual(
            self.violations(narrated=False, judgments_table=False, runs_row=False), [])

    def test_failed_run_is_not_a_claim(self):
        """Only status='ok' rows assert a completed, corrected search."""
        self.assertEqual(
            self.violations(status="error", judgments_table=False, runs_row=False), [])


class TestMismatchModes(LivenessCase):
    def test_missing_judgments_table(self):
        """The measured 2026-07-27 state: narration with no ledger table."""
        v = self.violations(judgments_table=False)
        self.assertEqual([x.mode for x in v], ["missing-table"])
        self.assertEqual(v[0].run_id, 139413)
        self.assertIn("48-rule", str(v[0]))
        self.assertEqual(main(["--db", self.path]), 1)

    def test_missing_or_unjudged_run_row(self):
        self.assertEqual([x.mode for x in self.violations(runs_row=False)],
                         ["missing-run"])
        self.assertEqual([x.mode for x in self.violations(judged=0)], ["missing-run"])

    def test_judgment_count_must_equal_grid_size(self):
        v = self.violations(n_judgments=47)
        self.assertEqual([x.mode for x in v], ["judgment-count"])
        self.assertIn("holds 47 row(s)", v[0].explanation)
        self.assertEqual([x.mode for x in self.violations(n_judgments=49)],
                         ["judgment-count"])


SPEC = ('{"kind":"%s","question":"…","horizonDays":21,'
        '"resolution":"…","registeredAt":"2026-07-27"}')

# The shape of prereg_records seq 37, a schedule-only amendment: it changes no
# claim, forecasts nothing, and names horizonDays only inside a table of the
# OTHER kinds whose dates it corrects. A substring test over the blob read that
# as a forecast commitment of kind "gradability-correction" — a kind nothing
# writes a regime_outcomes row for — so one grace day later the obligation was
# unsatisfiable and the publishing path refused every night.
AMENDMENT_SPEC = (
    '{"kind":"%s","correctionType":"schedule-only","claimsChanged":false,'
    '"correctedDates":[{"kind":"trend21","horizonDays":21,'
    '"firstPossibleVerdict":"2027-02-22"}]}')


def make_prereg_db(path, *, kind="filingsdrift21", outcomes=0, refusal=False,
                   dq=False, process_only=False, no_horizon=False,
                   amendment=False, reregister=False):
    """A database holding only the pre-registration surface of the check."""
    conn = sqlite3.connect(path)
    conn.executescript("""
      CREATE TABLE worker_runs (
        id INTEGER PRIMARY KEY, worker TEXT NOT NULL, started_at INTEGER NOT NULL,
        finished_at INTEGER, status TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '');
      CREATE TABLE prereg_records (
        seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, kind TEXT NOT NULL,
        spec_json TEXT NOT NULL, spec_hash TEXT NOT NULL DEFAULT '',
        prev_hash TEXT NOT NULL DEFAULT '', entry_hash TEXT NOT NULL DEFAULT '',
        note TEXT NOT NULL DEFAULT '');
      CREATE TABLE regime_outcomes (
        id INTEGER PRIMARY KEY, symbol_id INTEGER NOT NULL, kind TEXT NOT NULL,
        ts INTEGER NOT NULL, day INTEGER NOT NULL, horizon_days INTEGER NOT NULL,
        regime TEXT NOT NULL, conviction REAL NOT NULL,
        historical_accuracy REAL NOT NULL, rank REAL NOT NULL);
      CREATE TABLE research_loop_runs (
        day TEXT PRIMARY KEY, ran_at INTEGER NOT NULL, grid_size INTEGER NOT NULL,
        divisor INTEGER NOT NULL, corrected_alpha REAL NOT NULL,
        obs_count INTEGER NOT NULL, obs_ts_from INTEGER NOT NULL,
        obs_ts_to INTEGER NOT NULL, survivors INTEGER NOT NULL,
        judged INTEGER NOT NULL DEFAULT 0, refusal_reason TEXT NOT NULL DEFAULT '',
        git_rev TEXT NOT NULL DEFAULT '');
      CREATE TABLE dq_events (
        id INTEGER PRIMARY KEY, symbol_id INTEGER, ts INTEGER NOT NULL,
        kind TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '');
    """)
    if process_only:
        conn.execute("INSERT INTO prereg_records (ts, kind, spec_json) VALUES (?,?,?)",
                     (TS, "grading-protocol", '{"grader":"tools/accuracy_registry.py"}'))
    elif no_horizon:
        conn.execute("INSERT INTO prereg_records (ts, kind, spec_json) VALUES (?,?,?)",
                     (TS, kind, '{"kind":"%s","question":"…"}' % kind))
    elif amendment:
        conn.execute("INSERT INTO prereg_records (ts, kind, spec_json) VALUES (?,?,?)",
                     (TS, kind, AMENDMENT_SPEC % kind))
    else:
        conn.execute("INSERT INTO prereg_records (ts, kind, spec_json) VALUES (?,?,?)",
                     (TS, kind, SPEC % kind))
    if reregister:
        # A later re-registration of the same kind must not restart the clock.
        conn.execute("INSERT INTO prereg_records (ts, kind, spec_json) VALUES (?,?,?)",
                     (TS + 30 * 86400, kind, SPEC % kind))
    for i in range(outcomes):
        conn.execute("INSERT INTO regime_outcomes VALUES (?,?,?,?,?,?,?,?,?,?)",
                     (i + 1, 1, kind, TS, TS // 86400, 21, "up", 0.6, 0.5, 0.1))
    if refusal:
        conn.execute("INSERT INTO research_loop_runs VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
                     (DAY, TS, 0, 0, 0.0, 12, 0, 0, 0, 0,
                      "only 12 observations, need 2000", "abc"))
    if dq:
        conn.execute("INSERT INTO dq_events (ts, kind, detail) VALUES (?,?,?)",
                     (TS + 3600, "filingsdrift_no_freeze",
                      "3 fresh 10-Q/10-K and 0 calls frozen"))
    conn.commit()
    conn.close()


class PreregCase(unittest.TestCase):
    """A registration is only a constraint if never running is DETECTABLE."""

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.path = os.path.join(self.dir.name, "prereg.db")

    def violations(self, *, after_days=PREREG_GRACE_DAYS + 1, **kw):
        if os.path.exists(self.path):
            os.remove(self.path)
        make_prereg_db(self.path, **kw)
        conn = sqlite3.connect(f"file:{self.path}?mode=ro", uri=True)
        try:
            return check_prereg_forecasts(conn, now_ts=TS + int(after_days * 86400))
        finally:
            conn.close()


class TestPreregMustForecast(PreregCase):
    def test_registered_and_never_forecast_fails(self):
        """The measured 2026-07-27 state: seq 10, filingsdrift21, zero forecasts."""
        v = self.violations()
        self.assertEqual([x.mode for x in v], ["prereg-never-forecast"])
        self.assertIn("filingsdrift21", v[0].explanation)
        self.assertIn("0 rows", v[0].explanation)

    def test_a_kind_that_forecast_passes(self):
        self.assertEqual(self.violations(outcomes=1), [])

    def test_grace_window_is_not_yet_evidence(self):
        """A registration made today has not yet missed a daily cycle."""
        self.assertEqual(self.violations(after_days=PREREG_GRACE_DAYS - 0.5), [])

    def test_stated_refusal_is_honest_silence(self):
        """Silence WITH a reason on record is a result; silence without is not."""
        self.assertEqual(self.violations(refusal=True), [])
        self.assertEqual(self.violations(dq=True), [])

    def test_process_records_commit_to_no_forecast(self):
        """grading-protocol / prereg-document name no horizon and freeze nothing."""
        self.assertEqual(self.violations(process_only=True), [])
        self.assertEqual(self.violations(no_horizon=True), [])

    def test_an_amendment_that_tabulates_other_horizons_forecasts_nothing(self):
        """seq 37: a schedule-only correction is not a forecast commitment.

        It carries horizonDays deep inside correctedDates, never at the top
        level, because the horizons it lists belong to the kinds it corrects.
        Reading that as its own commitment made the check unsatisfiable and took
        the accuracy registry offline; a kind nothing forecasts can never clear
        an obligation to have forecast.
        """
        self.assertEqual(
            self.violations(kind="gradability-correction", amendment=True), [])

    def test_a_genuine_registration_is_still_caught(self):
        """The stricter predicate must not blunt the check it belongs to."""
        v = self.violations()
        self.assertEqual([x.mode for x in v], ["prereg-never-forecast"])

    def test_reregistration_does_not_restart_the_clock(self):
        v = self.violations(reregister=True, after_days=PREREG_GRACE_DAYS + 1)
        self.assertEqual([x.mode for x in v], ["prereg-never-forecast"])
        self.assertEqual(v[0].run_id, 1, "the FIRST registration is the commitment")

    def test_check_liveness_includes_the_prereg_verdict(self):
        """The publishing path calls check_liveness; both obligations run there."""
        if os.path.exists(self.path):
            os.remove(self.path)
        make_prereg_db(self.path)
        conn = sqlite3.connect(f"file:{self.path}?mode=ro", uri=True)
        try:
            modes = [v.mode for v in check_liveness(conn)]
        finally:
            conn.close()
        self.assertEqual(modes, ["prereg-never-forecast"])
        self.assertEqual(main(["--db", self.path]), 1)


class TestRefusalOnly(LivenessCase):
    def test_check_never_writes(self):
        """Its only outcome is refusal — it must not repair what it reports."""
        make_db(self.path, judgments_table=False)
        before = open(self.path, "rb").read()
        self.assertEqual(main(["--db", self.path]), 1)
        self.assertEqual(open(self.path, "rb").read(), before)
        conn = sqlite3.connect(self.path)
        tables = {r[0] for r in conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table'")}
        conn.close()
        self.assertNotIn("research_loop_judgments", tables)

    def test_unreadable_db_is_a_distinct_exit_code(self):
        self.assertEqual(main(["--db", os.path.join(self.dir.name, "nope.db")]), 2)


if __name__ == "__main__":
    unittest.main()

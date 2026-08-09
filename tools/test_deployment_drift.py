#!/usr/bin/env python3
"""Tests for the deployment-drift gate (tools/deployment_drift.py).

The gate's only job is to answer a yes/no about the LIVE database, so the tests
build fixture databases in both states and assert the exit code. The negative
case matters most: a gate that cannot pass is not a gate, it is an outage.

Run: python3 -m unittest discover -s tools -p 'test_*.py'
"""
import os
import sqlite3
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import deployment_drift as dd  # noqa: E402

REPO = dd.REPO


def build_db(path, *, naive_label, judgments, quarantine, revision, row_revision=None):
    """row_revision is the per-row stamp; None means "same as the meta stamp",
    the state a correctly-deployed stamping binary produces."""
    con = sqlite3.connect(path)
    con.executescript("""
        CREATE TABLE regime_outcomes (
          id INTEGER PRIMARY KEY, symbol_id INTEGER, kind TEXT, day INTEGER,
          ts INTEGER, naive_label INTEGER, revision TEXT,
          -- superseded_by mirrors the real schema. check_null_coverage excludes
          -- superseded rows because store.UnmatchedNullCount does; a fixture
          -- without the column tests a table the daemon never writes.
          superseded_by INTEGER);
        CREATE TABLE worker_runs (
          id INTEGER PRIMARY KEY, worker TEXT, started_at INTEGER,
          revision TEXT NOT NULL DEFAULT '');
        CREATE TABLE prediction_ledger (
          seq INTEGER PRIMARY KEY, predicted_at INTEGER, symbol_id INTEGER,
          horizon TEXT, bar_ts INTEGER, revision TEXT);
        CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);
    """)
    stamp = revision if row_revision is None else row_revision
    ts = dd.NULL_AMENDMENT_EPOCH_TS + 3600
    con.execute("INSERT INTO regime_outcomes VALUES (1, 7, 'trend21', 20260, ?, ?, ?, NULL)",
                (ts, 1 if naive_label else None, stamp))
    con.execute("INSERT INTO worker_runs VALUES (1, 'forecast', ?, ?)", (ts, stamp))
    con.execute("INSERT INTO prediction_ledger VALUES (1, ?, 7, '1d', ?, ?)",
                (ts, ts, stamp))
    con.execute("INSERT INTO meta VALUES (?, ?)", (dd.BUILD_REV_META_KEY, revision))
    if judgments:
        con.execute(f"CREATE TABLE {dd.JUDGMENTS_TABLE} (id INTEGER PRIMARY KEY)")
    if quarantine:
        con.execute(f"""CREATE TABLE {dd.QUARANTINE_TABLE} (
            outcome_id INTEGER PRIMARY KEY, symbol_id INTEGER, kind TEXT,
            day INTEGER, frozen_ts INTEGER)""")
        con.execute(f"""CREATE TABLE {dd.QUARANTINE_MANIFEST_TABLE} (
            id INTEGER PRIMARY KEY, digest TEXT, n_rows INTEGER, frozen_ts INTEGER)""")
        con.execute(f"INSERT INTO {dd.QUARANTINE_MANIFEST_TABLE} VALUES (1, ?, 0, ?)",
                    (dd.quarantine_digest([]), ts))
    con.commit()
    con.close()


def head_commit():
    return subprocess.run(["git", "rev-parse", "HEAD"], cwd=REPO,
                          capture_output=True, text=True, check=True).stdout.strip()


class DeploymentDriftTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.db = os.path.join(self.tmp.name, "t.db")
        self.addCleanup(self.tmp.cleanup)

    def coverage_check(self, repo):
        """check_null_coverage against self.db, closing the connection.

        Windows will not remove the temp dir while a sqlite handle is open, so an
        unclosed connection turns a passing test into a cleanup error.
        """
        con = sqlite3.connect(self.db)
        try:
            return dd.check_null_coverage(con, repo)
        finally:
            con.close()

    def test_all_mechanisms_present_passes(self):
        build_db(self.db, naive_label=True, judgments=True, quarantine=True,
                 revision=head_commit())
        status, checks = dd.run(self.db, REPO)
        self.assertEqual(status, 0, [c for c in checks if not c["ok"]])

    def test_unmatched_null_baseline_fails(self):
        build_db(self.db, naive_label=False, judgments=True, quarantine=True,
                 revision=head_commit())
        status, checks = dd.run(self.db, REPO)
        self.assertEqual(status, 1)
        cov = next(c for c in checks if c["name"] == "matched-null-baseline")
        self.assertFalse(cov["ok"])
        self.assertEqual(cov["measured"]["coverage"], 0.0)

    def test_ungoverned_kinds_and_quarantined_rows_do_not_fail_the_check(self):
        """The regression this check shipped with, and the reason it is here.

        check_null_coverage counted EVERY post-epoch label-less row. The write
        guard only refuses one whose kind is in store.structuralNullKinds, and the
        frozen quarantine exempts a fixed historical set — so rows the daemon was
        ENTITLED to write were reported as proof the deployed binary had drifted.
        Measured on the live database 2026-08-09 that was 1,297 rows, of which
        1,165 were quarantined and all 132 remaining were filingsdrift21, a kind
        with no null resolver. Go's UnmatchedNullCount returned 0; this returned
        "Publication must be refused".

        Both exempt shapes are planted here with NULL naive_label. Neither may
        fail the check, and a governed, unquarantined one still must.
        """
        build_db(self.db, naive_label=True, judgments=True, quarantine=True,
                 revision=head_commit())
        con = sqlite3.connect(self.db)
        ts = dd.NULL_AMENDMENT_EPOCH_TS + 7200
        # (a) an ungoverned kind: outside structuralNullKinds by design.
        con.execute("INSERT INTO regime_outcomes VALUES "
                    "(2, 7, 'filingsdrift21', 20260, ?, NULL, 'r', NULL)", (ts,))
        # (b) a GOVERNED kind, but frozen in the quarantine manifest.
        con.execute("INSERT INTO regime_outcomes VALUES "
                    "(3, 7, 'trend21', 20260, ?, NULL, 'r', NULL)", (ts,))
        con.execute(f"INSERT INTO {dd.QUARANTINE_TABLE} VALUES (3, 7, 'trend21', 20260, ?)",
                    (ts,))
        # (c) a superseded governed row — also excluded, as the Go counter does.
        con.execute("INSERT INTO regime_outcomes VALUES "
                    "(4, 7, 'trend21', 20260, ?, NULL, 'r', 1)", (ts,))
        con.commit()
        con.close()

        cov = self.coverage_check(REPO)
        self.assertTrue(cov["ok"], cov["evidence"])
        self.assertEqual(cov["measured"]["unmatched_outside_quarantine"], 0)
        self.assertEqual(cov["measured"]["quarantined"], 1)
        self.assertNotIn("filingsdrift21", cov["measured"]["kinds"])

        # And the gate still bites: a governed, unquarantined, label-less row.
        con = sqlite3.connect(self.db)
        con.execute("INSERT INTO regime_outcomes VALUES "
                    "(5, 7, 'vol21', 20260, ?, NULL, 'r', NULL)", (ts,))
        con.commit()
        con.close()
        cov = self.coverage_check(REPO)
        self.assertFalse(cov["ok"], "a governed unquarantined NULL must still fail")
        self.assertEqual(cov["measured"]["unmatched_outside_quarantine"], 1)
        self.assertIn("vol21:1", cov["evidence"])

    def test_null_kind_set_is_read_from_go_and_never_guessed(self):
        """An unreadable kind set must fail loudly, never default to something.

        A silently-empty set would make the SQL match no rows, so the check would
        pass forever while measuring nothing — the dead-gate shape this whole file
        exists to catch.
        """
        kinds = dd.structural_null_kinds(REPO)
        self.assertIn("trend21", kinds)
        self.assertNotIn("filingsdrift21", kinds)
        with self.assertRaises(Exception):
            dd.structural_null_kinds(self.tmp.name)  # no daemon/ tree here
        cov = self.coverage_check(self.tmp.name)
        self.assertFalse(cov["ok"])

    def test_missing_judgment_ledger_and_quarantine_fail(self):
        build_db(self.db, naive_label=True, judgments=False, quarantine=False,
                 revision=head_commit())
        status, checks = dd.run(self.db, REPO)
        self.assertEqual(status, 1)
        failed = {c["name"] for c in checks if not c["ok"]}
        self.assertEqual(failed, {"research-loop-judgment-ledger",
                                  "null-quarantine-manifest"})

    def test_deployed_revision_without_holdout_constant_fails(self):
        # The first commit in this repository cannot contain a constant added
        # later, which is exactly the drift shape the gate exists to catch.
        first = subprocess.run(["git", "rev-list", "--max-parents=0", "HEAD"],
                               cwd=REPO, capture_output=True, text=True,
                               check=True).stdout.split()[0]
        build_db(self.db, naive_label=True, judgments=True, quarantine=True,
                 revision=first)
        status, checks = dd.run(self.db, REPO)
        self.assertEqual(status, 1)
        rev = next(c for c in checks if c["name"] == "deployed-code-revision")
        self.assertFalse(rev["ok"])

    def test_dirty_build_is_refused(self):
        build_db(self.db, naive_label=True, judgments=True, quarantine=True,
                 revision=head_commit() + "+dirty")
        _, checks = dd.run(self.db, REPO)
        rev = next(c for c in checks if c["name"] == "deployed-code-revision")
        self.assertFalse(rev["ok"])
        self.assertIn("DIRTY", rev["evidence"])

    def test_edited_quarantine_membership_breaks_the_digest(self):
        build_db(self.db, naive_label=True, judgments=True, quarantine=True,
                 revision=head_commit())
        con = sqlite3.connect(self.db)
        con.execute(f"INSERT INTO {dd.QUARANTINE_TABLE} VALUES (1, 7, 'trend21', 20260, 0)")
        con.commit()
        con.close()
        status, checks = dd.run(self.db, REPO)
        self.assertEqual(status, 1)
        q = next(c for c in checks if c["name"] == "null-quarantine-manifest")
        self.assertFalse(q["ok"])

    def test_unstamped_rows_fail_even_when_meta_names_the_right_commit(self):
        # The live drift shape: meta names HEAD, HEAD contains every mechanism,
        # and not one row written since boot carries the stamp.
        build_db(self.db, naive_label=True, judgments=True, quarantine=True,
                 revision=head_commit(), row_revision="")
        status, checks = dd.run(self.db, REPO)
        self.assertEqual(status, 1)
        rev = next(c for c in checks if c["name"] == "deployed-code-revision")
        self.assertTrue(rev["ok"], "the commit-level check still passes — that is the gap")
        stamp = next(c for c in checks if c["name"] == "row-revision-stamp")
        self.assertFalse(stamp["ok"])
        for t in ("worker_runs", "prediction_ledger", "regime_outcomes"):
            self.assertEqual(stamp["measured"]["tables"][t]["coverage"], 0.0)

    def test_rows_stamped_with_other_code_fail(self):
        first = subprocess.run(["git", "rev-list", "--max-parents=0", "HEAD"],
                               cwd=REPO, capture_output=True, text=True,
                               check=True).stdout.split()[0]
        build_db(self.db, naive_label=True, judgments=True, quarantine=True,
                 revision=head_commit(), row_revision=first)
        _, checks = dd.run(self.db, REPO)
        stamp = next(c for c in checks if c["name"] == "row-revision-stamp")
        self.assertFalse(stamp["ok"])

    def test_stamped_rows_pass_and_coverage_is_reported(self):
        build_db(self.db, naive_label=True, judgments=True, quarantine=True,
                 revision=head_commit())
        status, checks = dd.run(self.db, REPO)
        self.assertEqual(status, 0, [c for c in checks if not c["ok"]])
        stamp = next(c for c in checks if c["name"] == "row-revision-stamp")
        self.assertEqual(stamp["measured"]["tables"]["prediction_ledger"]["coverage"], 1.0)

    def test_missing_revision_column_fails(self):
        build_db(self.db, naive_label=True, judgments=True, quarantine=True,
                 revision=head_commit())
        con = sqlite3.connect(self.db)
        con.execute("ALTER TABLE prediction_ledger DROP COLUMN revision")
        con.commit()
        con.close()
        _, checks = dd.run(self.db, REPO)
        stamp = next(c for c in checks if c["name"] == "row-revision-stamp")
        self.assertFalse(stamp["ok"])
        self.assertIn("error", stamp["measured"]["tables"]["prediction_ledger"])

    def test_gate_never_writes_to_the_database(self):
        build_db(self.db, naive_label=False, judgments=False, quarantine=False,
                 revision=head_commit())
        def snapshot():
            with open(self.db, "rb") as f:
                return os.stat(self.db).st_mtime_ns, f.read()
        before = snapshot()
        dd.run(self.db, REPO)
        after = snapshot()
        self.assertEqual(before, after)


if __name__ == "__main__":
    unittest.main()

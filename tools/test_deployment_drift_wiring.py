#!/usr/bin/env python3
"""Tests pin the SHAPE of the DEPLOYMENT DRIFT gate in ops/accuracy-registry.sh:
- Present, before the grader, refuses on any non-zero exit, filed as a heartbeat failure.
- The revision-boot signal of deployment_drift.newest_boot_ts that fixed finding A21
  of audits/2026-08-12-reaudit.md (a revision change is an exact boot; the quiet-gap
  heuristic alone spanned twelve binaries during rapid deploys).

These tests ensure the gate runs the tool against the live database, sits after both
liveness checks and before the grader, treats any non-zero exit as refusal, does not
swallow the exit status, and files drift refusal as a grader failure. They also verify
the newest_boot_ts function correctly identifies boot timestamps from revision changes
and quiet gaps, ignoring unstamped rows and handling empty tables.
"""
import os, re, sqlite3, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import deployment_drift  # noqa: E402

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCRIPT = os.path.join(REPO, "ops", "accuracy-registry.sh")

class DeploymentDriftGateTest(unittest.TestCase):
    def setUp(self):
        with open(SCRIPT, encoding="utf-8") as fh:
            self.src = fh.read()
        start = self.src.index("DEPLOYMENT DRIFT")
        end = self.src.index("PROTOCOL-DOCUMENT REGISTRATION", start)
        self.gate = self.src[start:end]

    def test_gate_runs_the_tool_against_the_live_database(self):
        """Gate must invoke deployment_drift with the correct database path."""
        self.assertIn('"$SD/tools/deployment_drift.py" --db "$SD/data/signaldeck.db"', self.gate)

    def test_gate_sits_after_both_liveness_checks_and_before_the_grader(self):
        """Gate must appear after research_liveness and anchor_liveness, before accuracy_registry."""
        research = self.src.index("tools/research_liveness.py")
        anchor = self.src.index("tools/anchor_liveness.py")
        drift = self.src.index("DEPLOYMENT DRIFT")
        grader = self.src.index('tools/accuracy_registry.py" --json')
        self.assertTrue(research < anchor < drift < grader)

    def test_any_non_zero_exit_refuses_publication(self):
        """Non-zero exit must set refusal_reason and not be a warning (fail-open would miss exit 2)."""
        self.assertRegex(self.gate, r'if \[ "\$drift_status" -ne 0 \]')
        self.assertEqual(1, self.gate.count("refusal_reason="))
        self.assertIn('refusal_reason="deployment drift check failed (exit $drift_status)', self.gate)
        self.assertNotIn("WARN", self.gate, msg="Fail-open on exit 2 would let missing script or renamed flag turn gate into warning (see REMEDIATION_2026-08-03.md)")

    def test_gate_is_not_swallowed(self):
        """Gate must not use || true to ignore failures."""
        self.assertNotIn("|| true", self.gate)

    def test_drift_refusal_is_filed_as_a_grader_failure(self):
        """Drift refusal must be logged as --failure so grader health sees failure.

        THE ASSERTION IS UNCHANGED; the regex is not. It used to require the
        whole `case` on ONE line ([^\\n]* twice), which pinned the formatting
        rather than the behaviour -- the block was reflowed to multiple lines on
        2026-09-13 and this failed while the classification it guards was
        identical. A test that breaks on a line wrap is a test someone
        eventually deletes.

        The distinction being protected is real and worth restating: --refused
        is a HEALTHY grader saying no on evidence, --failure means it never
        produced a verdict. Filing the second as the first leaves grader health
        green while nothing has graded.
        """
        case = re.search(r'case "\$refusal_reason" in(.*?)esac', self.src, re.S)
        self.assertIsNotNone(case, "the hb_mode classification block is gone entirely")
        failure_arm = re.search(r'^(.*?)hb_mode=--failure', case.group(1), re.S).group(1)
        self.assertIn('"deployment drift"*', failure_arm,
                      msg="--refused heartbeat would leave grader health green while grader never ran")

    def test_an_unrunnable_gate_is_also_a_grader_failure(self):
        """CHECK UNAVAILABLE belongs in the same arm as drift and liveness.

        Added 2026-09-13 with the prefix itself. ops/accuracy-registry.sh and
        ops/grade.sh now write "CHECK UNAVAILABLE: ..." whenever a gate could
        not produce a verdict -- a missing collapsecheck binary, a renamed flag,
        a crashed selection_honesty, a staged registry that would not move into
        place. Every one of those means the grade did not happen, which is the
        --failure class, and tools/docs_gate.py --contract release refuses to
        ship a build whose evidence state is one of them.

        Classified as --refused instead, an outage would read as a measured
        scientific refusal on the public surface: grader health green, a
        refusal published as though something had been found out about a model.
        """
        case = re.search(r'case "\$refusal_reason" in(.*?)esac', self.src, re.S)
        self.assertIsNotNone(case)
        failure_arm = re.search(r'^(.*?)hb_mode=--failure', case.group(1), re.S).group(1)
        self.assertIn('"CHECK UNAVAILABLE"*', failure_arm,
                      msg="a gate that could not RUN would be filed as a healthy refusal")

class RevisionBootSignalTest(unittest.TestCase):
    def _con(self, rows):
        con = sqlite3.connect(":memory:")
        con.execute("CREATE TABLE worker_runs (started_at INTEGER NOT NULL, revision TEXT)")
        con.executemany("INSERT INTO worker_runs VALUES (?, ?)", rows)
        return con

    def test_revision_change_is_a_boot_without_any_quiet_gap(self):
        """Revision change alone defines boot; quiet-gap heuristic would return older boot (A21 failure)."""
        T0 = 1_700_000_000
        rows = [(T0 + i*60, "aaaa1111") for i in range(10)] + [(T0 + 10*60 + i*60, "bbbb2222") for i in range(10)]
        con = self._con(rows)
        boot = deployment_drift.newest_boot_ts(con)
        self.assertEqual(boot, T0 + 10*60, msg="Quiet-gap heuristic alone returned older boot and stamp ratio read 4.7% (see A21 in audits/2026-08-12-reaudit.md)")
        self.assertNotEqual(boot, T0)

    def test_later_quiet_gap_beats_the_revision_signal(self):
        """Quiet gap of same revision defines boot; revision change within gap ignored."""
        T0 = 1_700_000_000
        rows = [(T0 + i*60, "bbbb2222") for i in range(10)] + [(T0 + 10*60 + 1000 + i*60, "bbbb2222") for i in range(5)]
        con = self._con(rows)
        boot = deployment_drift.newest_boot_ts(con)
        # The last row of the first burst is at T0 + 9*60, the first row after
        # the gap at T0 + 10*60 + 1000 — a 1060 s hole, wider than the 900 s
        # BOOT_QUIET_SECS, so the heuristic sees a boot the revision signal
        # cannot: both bursts carry the same revision, whose MIN is still T0.
        self.assertEqual(boot, T0 + 10*60 + 1000, msg="Quiet gap of 1060s beats revision signal for same-revision redeploy")

    def test_unstamped_rows_do_not_define_the_revision(self):
        """NULL/revision rows ignored when picking newest revision; boot from first stamped row."""
        T0 = 1_700_000_000
        rows = [(T0 + i*60, "bbbb2222") for i in range(5)] + [(T0 + 5*60 + i*60, None) for i in range(5)]
        con = self._con(rows)
        boot = deployment_drift.newest_boot_ts(con)
        self.assertEqual(boot, T0, msg="NULL/empty revisions ignored when picking newest revision; no quiet gap")

    def test_empty_table_has_no_boot(self):
        """Empty worker_runs returns None for boot timestamp."""
        con = self._con([])
        boot = deployment_drift.newest_boot_ts(con)
        self.assertIsNone(boot)

if __name__ == "__main__":
    unittest.main()
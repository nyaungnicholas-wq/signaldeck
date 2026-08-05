#!/usr/bin/env python3
"""Tests for tools/live_accuracy.py — the single source of truth for the live record.

WHY THIS EXISTS. On 2026-08-04 the repository published FOUR mutually
contradictory live directional records at once: 48.1%/54.6% (n=13,058),
48.0%/54.4% (n=12,696), 46.7% (n=8,191) and 46.3%/52.9% (n=2,257). Each was
correct on the day it was written and none of them said so, because every one
was typed by hand into a different document. The fix is that no document types
the number at all: one generator reads data/accuracy_registry.json, one partial
holds the table, and every document includes that partial verbatim.

These tests pin the three properties that make that fix load-bearing:
  1. the generator reads the AUTHORITATIVE snapshot, including the refusal path
     (a REFUSED registry with zero rows must fall back to the last successful
     grade and SAY it is stale, never silently publish an empty table);
  2. no verdict is derived from an interval while intervals are withheld;
  3. the CI scan actually fails on a superseded literal in a live-claim
     document, and actually passes when that document is marked historical.

Run: python3 -m unittest discover -s tools -p 'test_*.py'
     python3 tools/test_live_accuracy.py
"""
import json
import os
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCRIPT = os.path.join(REPO, "tools", "live_accuracy.py")

BEGIN = "<!-- BEGIN GENERATED live_accuracy -->"
END = "<!-- END GENERATED live_accuracy -->"

# A registry in the state the real one is in: the top-level grade REFUSED, zero
# published rows, and the last successful grade preserved under
# stale_last_registry. Numbers are the real 2026-08-03T23:06:30 snapshot.
FIXTURE = {
    "status": "REFUSED",
    "generated": "2026-08-04T14:05:05",
    "graded_at": "2026-08-03T23:06:30",
    "refused_since": "2026-08-04T14:05:05",
    "last_successful_grade_age": "15.0h",
    "refusal_reason": "grader exited 1",
    "refusal_stderr": "UNREGISTERED GRADER: ...",
    "rows": [],
    "stale_last_registry": {
        "generated": "2026-08-03T23:06:30",
        "graded_at": "2026-08-03T23:06:30",
        "family_size": 13,
        "looks": 8,
        "divisor": 104,
        "corrected_alpha": 0.0004807692307692308,
        "ci_z": 3.4912482953673902,
        "survivorship_epoch": "2026-07-24",
        "rows": [
            {"predictor": "directional-ensemble (1d)", "family": "direction",
             "band": "all", "live_n": 2257, "live_acc": 0.46256092157731504,
             "ci": None, "ci_method": "withheld", "distinct_days": 9,
             "null_prequential": 0.5287992910943731, "skill": -0.06623836951705803,
             "retire": False, "survivorship_clean": False},
            {"predictor": "prequential-majority (1d)", "family": "benchmark",
             "band": "all", "live_n": 1644, "live_acc": 0.555352798053528,
             "ci": None, "ci_method": "withheld", "distinct_days": 6,
             "null_prequential": 0.5109489051094891, "skill": 0.0444038929440389,
             "verdict": "INSUFFICIENT DAYS (6/10 distinct days) - no interval, so no verdict",
             "retire": False, "survivorship_clean": False},
            {"predictor": "trend21", "family": "structure", "band": "all",
             "claimed": 0.731, "live_n": 0, "live_acc": None, "ci": None,
             "distinct_days": 0, "null_prequential": None, "skill": None,
             "forecasts_recorded": 2918, "retire": False,
             "note": "claim is backtested, not yet a live record"},
        ],
    },
}


def run(*args, cwd=None):
    return subprocess.run([sys.executable, SCRIPT, *args], cwd=cwd or REPO,
                          capture_output=True, text=True)


class TmpRepo:
    """A throwaway tree with a registry fixture and a docs/ dir."""

    def __enter__(self):
        self.d = tempfile.TemporaryDirectory()
        root = self.d.name
        os.makedirs(os.path.join(root, "data"))
        os.makedirs(os.path.join(root, "partials"))
        self.registry = os.path.join(root, "data", "accuracy_registry.json")
        self.partial = os.path.join(root, "partials", "live_accuracy.md")
        with open(self.registry, "w", encoding="utf-8") as f:
            json.dump(FIXTURE, f)
        self.root = root
        return self

    def __exit__(self, *a):
        self.d.cleanup()

    def doc(self, name, text):
        p = os.path.join(self.root, name)
        with open(p, "w", encoding="utf-8") as f:
            f.write(text)
        return p

    def read(self, p):
        with open(p, encoding="utf-8") as f:
            return f.read()

    def gen(self, *extra):
        return run("--registry", self.registry, "--out", self.partial, *extra)


class TestGenerator(unittest.TestCase):
    def test_script_exists(self):
        self.assertTrue(os.path.exists(SCRIPT), "tools/live_accuracy.py is missing")

    def test_falls_back_to_last_successful_grade_and_says_it_is_stale(self):
        with TmpRepo() as t:
            r = t.gen("--write")
            self.assertEqual(r.returncode, 0, r.stderr)
            md = t.read(t.partial)
            # The live rows from the last successful grade are present.
            self.assertIn("46.3%", md)
            self.assertIn("2,257", md)
            self.assertIn("52.9%", md)
            # And the staleness is stated, not hidden.
            self.assertIn("2026-08-03T23:06:30", md)
            low = md.lower()
            self.assertTrue("refused" in low or "stale" in low,
                            "a REFUSED registry must be declared stale in the partial")
            self.assertIn("grader exited 1", md)

    def test_never_publishes_an_empty_table_from_a_refused_registry(self):
        with TmpRepo() as t:
            t.gen("--write")
            md = t.read(t.partial)
            self.assertIn("directional-ensemble (1d)", md)

    def test_no_interval_verdict_while_intervals_are_withheld(self):
        with TmpRepo() as t:
            t.gen("--write")
            md = t.read(t.partial)
            self.assertIn("withheld", md.lower())
            # A verdict word must not be attached to a withheld-interval row.
            for line in md.splitlines():
                if "directional-ensemble (1d)" in line:
                    for banned in ("FAILED", "PASSED", "significant", "SIGNIFICANT"):
                        self.assertNotIn(banned, line,
                                         "verdict published while the interval is withheld: %r" % line)

    def test_backtested_only_rows_are_not_mixed_into_the_live_table(self):
        with TmpRepo() as t:
            t.gen("--write")
            md = t.read(t.partial)
            table, _, rest = md.partition("### Backtested claims")
            self.assertNotIn("trend21", table,
                             "a row with no live record must not sit in the live table")
            self.assertIn("73.1%", rest)  # the registered claim is still disclosed

    def test_partial_is_delimited_by_include_markers(self):
        with TmpRepo() as t:
            t.gen("--write")
            md = t.read(t.partial)
            self.assertTrue(md.startswith(BEGIN), "partial must open with the include marker")
            self.assertTrue(md.rstrip().endswith(END), "partial must close with the include marker")

    def test_write_is_deterministic(self):
        with TmpRepo() as t:
            t.gen("--write")
            a = t.read(t.partial)
            t.gen("--write")
            self.assertEqual(a, t.read(t.partial))

    def test_check_fails_when_the_partial_drifts(self):
        with TmpRepo() as t:
            t.gen("--write")
            self.assertEqual(t.gen("--check").returncode, 0)
            with open(t.partial, "a", encoding="utf-8") as f:
                f.write("\nthe live record is 48.1%\n")
            self.assertEqual(t.gen("--check").returncode, 1,
                             "--check must fail when partials/live_accuracy.md drifts from the registry")


class TestInject(unittest.TestCase):
    def test_injects_between_markers_and_leaves_the_rest_alone(self):
        with TmpRepo() as t:
            t.gen("--write")
            doc = t.doc("DOC.md", "# Doc\n\nintro\n\n%s\nold junk\n%s\n\ntail\n" % (BEGIN, END))
            r = t.gen("--inject", doc)
            self.assertEqual(r.returncode, 0, r.stderr)
            got = t.read(doc)
            self.assertIn("46.3%", got)
            self.assertNotIn("old junk", got)
            self.assertIn("intro", got)
            self.assertIn("tail", got)

    def test_check_fails_when_an_included_block_drifts(self):
        with TmpRepo() as t:
            t.gen("--write")
            doc = t.doc("DOC.md", "%s\nstale\n%s\n" % (BEGIN, END))
            self.assertEqual(t.gen("--check", "--inject", doc).returncode, 1)
            t.gen("--inject", doc)
            self.assertEqual(t.gen("--check", "--inject", doc).returncode, 0)


class TestSupersededScan(unittest.TestCase):
    """The CI gate: a superseded live-record literal in a live-claim doc fails."""

    def test_scan_fails_on_a_superseded_literal(self):
        with TmpRepo() as t:
            doc = t.doc("CLAIMS.md", "The ensemble retired at 48.1% against a 54.6% baseline.\n")
            r = t.gen("--scan", doc)
            self.assertEqual(r.returncode, 1, "scan must reject a superseded live-record literal")
            self.assertIn("48.1", r.stdout + r.stderr)

    def test_scan_passes_when_the_doc_is_marked_historical(self):
        with TmpRepo() as t:
            doc = t.doc("CLAIMS.md",
                        "<!-- SUPERSEDED-SNAPSHOT: historical record, not the live record -->\n"
                        "The ensemble retired at 48.1% against a 54.6% baseline.\n")
            self.assertEqual(t.gen("--scan", doc).returncode, 0,
                             "a doc marked SUPERSEDED-SNAPSHOT is history and must pass")

    def test_scan_ignores_numbers_inside_the_generated_block(self):
        with TmpRepo() as t:
            t.gen("--write")
            doc = t.doc("CLAIMS.md", "%s\nlive 46.3%% vs 52.9%%\n%s\n" % (BEGIN, END))
            self.assertEqual(t.gen("--scan", doc).returncode, 0)

    def test_scan_does_not_fire_on_unrelated_percentages(self):
        with TmpRepo() as t:
            doc = t.doc("BANDS.md",
                        "Bands: `<0.5 -> 59.5%` `0.5-0.8 -> 73.9%` `0.8-0.9 -> 80.1%`\n"
                        "coverage floor 70.1%, store 73.4%\n")
            r = t.gen("--scan", doc)
            self.assertEqual(r.returncode, 0,
                             "calibration bands and coverage floors are not live records: %s" % r.stdout)

    def test_scan_fails_on_a_superseded_sample_size(self):
        with TmpRepo() as t:
            doc = t.doc("CLAIMS.md", "46.7% over 8,191 independent symbol-days\n")
            self.assertEqual(t.gen("--scan", doc).returncode, 1)


if __name__ == "__main__":
    unittest.main()

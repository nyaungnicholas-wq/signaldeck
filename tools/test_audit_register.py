#!/usr/bin/env python3
"""Tests for tools/audit_register.py.

Run with:  python3 -m unittest discover -s tools -p 'test_*.py'

Each failure mode of the register gets a synthetic audit tree, so the checks are
exercised independently of the real audits/ directory (which is expected to be
failing — that failing state is itself a finding, not a broken test).
"""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import audit_register as ar  # noqa: E402

HEADER = (
    "| id | area | severity | impact | evidence (measured) | status |\n"
    "|---|---|---|---|---|---|\n"
)


def audit(*rows: str) -> str:
    return "# Re-audit\n\nSome prose.\n\n" + HEADER + "".join(rows) + "\n## Next section\n"


def row(fid: str, status: str, impact: str = "A gate was wrong.", evidence: str = "Measured 14.7x design effect.") -> str:
    return f"| **{fid}** | statistics | HIGH | {impact} | {evidence} | {status} |\n"


class TreeCase(unittest.TestCase):
    def run_on(self, files: dict[str, str]) -> list[str]:
        with tempfile.TemporaryDirectory() as d:
            for name, text in files.items():
                with open(os.path.join(d, name), "w", encoding="utf-8") as fh:
                    fh.write(text)
            return ar.check(ar.load(d))


class TestNormalizeStatus(TreeCase):
    def test_strips_bold_parentheticals_and_trailing_clause(self):
        self.assertEqual(ar.normalize_status("**fixed**"), "fixed")
        self.assertEqual(ar.normalize_status("**fixed** (18 tests added)"), "fixed")
        self.assertEqual(ar.normalize_status("could-not-verify (relayed)"), "could-not-verify")
        self.assertEqual(ar.normalize_status("proposed (doc fix)"), "proposed")
        self.assertEqual(
            ar.normalize_status("**diagnosed — needs a redeploy (operator action, see §4)**"),
            "diagnosed",
        )
        self.assertEqual(ar.normalize_status("**confirmed, not fixed**"), "confirmed, not fixed")


class TestCleanTree(TreeCase):
    def test_all_resolved_with_evidence_passes(self):
        files = {
            "2026-01-01-reaudit.md": audit(row("A1", "**fixed**"), row("A2", "refuted")),
            "2026-04-01-reaudit.md": audit(row("A1", "accepted-risk"), row("A2", "open")),
        }
        self.assertEqual(self.run_on(files), [])

    def test_non_reaudit_files_are_ignored(self):
        files = {
            "2026-01-01-reaudit.md": audit(row("A1", "**fixed**")),
            "2026-04-01-null-transition.md": audit(row("A1", "banana")),
        }
        self.assertEqual(self.run_on(files), [])

    def test_tables_without_the_findings_header_are_ignored(self):
        text = (
            "# Re-audit\n\n| row | before | after |\n|---|---|---|\n"
            "| **A1** | bad | good |\n\n" + HEADER + row("A2", "**fixed**")
        )
        self.assertEqual(self.run_on({"2026-01-01-reaudit.md": text}), [])


class TestVocabulary(TreeCase):
    def test_status_outside_vocabulary_fails(self):
        v = self.run_on({"2026-01-01-reaudit.md": audit(row("A1", "**confirmed, not fixed**"))})
        self.assertEqual(len(v), 1)
        self.assertIn("outside the closed vocabulary", v[0])
        self.assertIn("A1", v[0])

    def test_relayed_and_proposed_and_diagnosed_all_fail(self):
        files = {
            "2026-01-01-reaudit.md": audit(
                row("A1", "could-not-verify (relayed)"),
                row("A2", "proposed"),
                row("A3", "**diagnosed — needs a redeploy (operator action)**"),
            )
        }
        v = self.run_on(files)
        self.assertEqual(len(v), 3)
        self.assertTrue(all("outside the closed vocabulary" in x for x in v))


class TestFixedNeedsEvidence(TreeCase):
    def test_fixed_without_test_sha_or_number_fails(self):
        files = {
            "2026-01-01-reaudit.md": audit(
                row("A1", "**fixed**", impact="The gate was wrong.", evidence="I looked at it and fixed it.")
            )
        }
        v = self.run_on(files)
        self.assertEqual(len(v), 1)
        self.assertIn("cite no test name, commit sha, or measured number", v[0])

    def test_test_name_alone_satisfies_it(self):
        files = {
            "2026-01-01-reaudit.md": audit(
                row("A1", "**fixed**", impact="Gate was wrong.", evidence="Guarded by test_no_lookahead now.")
            )
        }
        self.assertEqual(self.run_on(files), [])

    def test_commit_sha_alone_satisfies_it(self):
        files = {
            "2026-01-01-reaudit.md": audit(
                row("A1", "**fixed**", impact="Gate was wrong.", evidence="Fixed in commit fd9b658.")
            )
        }
        self.assertEqual(self.run_on(files), [])

    def test_unfixed_rows_are_not_asked_for_fix_evidence(self):
        files = {
            "2026-01-01-reaudit.md": audit(
                row("A1", "open", impact="Gate is wrong.", evidence="Not yet measured.")
            )
        }
        self.assertEqual(self.run_on(files), [])


class TestAgingRule(TreeCase):
    def test_open_row_in_an_older_audit_fails(self):
        files = {
            "2026-01-01-reaudit.md": audit(row("A1", "open")),
            "2026-04-01-reaudit.md": audit(row("A1", "**fixed**")),
        }
        v = self.run_on(files)
        self.assertEqual(len(v), 1)
        self.assertIn("older than the newest audit 2026-04-01", v[0])
        self.assertIn("2026-01-01-reaudit.md:A1", v[0])

    def test_open_row_in_the_newest_audit_is_allowed(self):
        files = {
            "2026-01-01-reaudit.md": audit(row("A1", "**fixed**")),
            "2026-04-01-reaudit.md": audit(row("A1", "open")),
        }
        self.assertEqual(self.run_on(files), [])

    def test_out_of_vocabulary_status_in_an_older_audit_reports_both_violations(self):
        files = {
            "2026-01-01-reaudit.md": audit(row("A1", "proposed")),
            "2026-04-01-reaudit.md": audit(row("A1", "**fixed**")),
        }
        v = self.run_on(files)
        self.assertEqual(len(v), 2)
        self.assertTrue(any("outside the closed vocabulary" in x for x in v))
        self.assertTrue(any("older than the newest audit" in x for x in v))


class TestMainExitCodes(TreeCase):
    def test_empty_directory_exits_non_zero(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(ar.main(["--audits-dir", d]), 1)

    def test_clean_tree_exits_zero_and_dirty_tree_exits_one(self):
        with tempfile.TemporaryDirectory() as d:
            with open(os.path.join(d, "2026-01-01-reaudit.md"), "w", encoding="utf-8") as fh:
                fh.write(audit(row("A1", "**fixed**")))
            self.assertEqual(ar.main(["--audits-dir", d]), 0)
            with open(os.path.join(d, "2026-02-01-reaudit.md"), "w", encoding="utf-8") as fh:
                fh.write(audit(row("A1", "proposed")))
            self.assertEqual(ar.main(["--audits-dir", d]), 1)

    def test_real_repo_audits_are_clean(self):
        """The repo's own audits/ tree must register clean.

        This assertion is INVERTED from what it was. It used to pin the tree as
        FAILING — A11 'confirmed, not fixed', four 'could-not-verify (relayed)'
        rows, and 'proposed'/'diagnosed' rows in the 07-27 audit — and its own
        docstring set the condition for flipping it: "If this ever starts passing
        it must be because the findings were resolved, not because the
        vocabulary was widened."

        That is what happened on 2026-08-12. All eleven were re-verified against
        the running system and marked **fixed** with the evidence in their rows
        (A10 now 401s, A11 answers in 0.0013s, A13's own repro is caught by the
        live VENDOR_PATTERN, A2's binary carries the symbols it had 0 of, and so
        on). CLOSED_VOCABULARY is untouched — the assertion below re-checks that
        in the same breath, so the pinning cannot be satisfied by widening it.

        Keeping the test pointed at the CLEAN state is strictly more useful than
        pinning a broken one: it now fails the moment a finding is recorded with
        a status outside the vocabulary, or carried past the next audit.
        """
        self.assertEqual(
            tuple(ar.CLOSED_VOCABULARY), ("fixed", "refuted", "accepted-risk", "open"),
            "the vocabulary was widened — resolve findings instead")
        repo_audits = os.path.join(
            os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "audits"
        )
        if not os.path.isdir(repo_audits):
            self.skipTest("audits/ not present")
        violations = ar.check(ar.load(repo_audits))
        self.assertEqual(violations, [], "the audit register must stay clean: "
                         + "; ".join(violations))


if __name__ == "__main__":
    unittest.main()

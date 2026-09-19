#!/usr/bin/env python3
"""This file exposes the ledger_revision_reachability.py self-check to unittest discovery.

The tool's __selfcheck builds a temporary git repo and sqlite ledger, verifies
orphaned commits are UNREACHABLE, fake shas are UNRESOLVABLE, pre-epoch rows
are ignored, and a clean ledger prints LEDGER REVISIONS OK. A self-check that
nobody runs is not evidence; this test ensures CI runs it on every build.
"""
import os
import subprocess
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "ledger_revision_reachability.py")


class TestSelfcheck(unittest.TestCase):
    def test_selfcheck_prints_token_and_exits_zero(self):
        p = subprocess.run([sys.executable, TOOL, "__selfcheck"], capture_output=True, text=True, cwd=HERE)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("SELFCHECK OK", p.stdout, p.stdout + p.stderr)

    def test_help_names_the_epoch_and_exit_codes(self):
        p = subprocess.run([sys.executable, TOOL, "--help"], capture_output=True, text=True, cwd=HERE)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("__selfcheck", p.stdout)
        self.assertIn("Exit codes", p.stdout)


if __name__ == "__main__":
    unittest.main()

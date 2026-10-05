"""VERIFY-VACUOUS: verify.py must not print PASS for checks that never ran (no --site = INCOMPLETE)."""
import sys
import os
import unittest
import tempfile
import io
import contextlib
import shutil
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import verify_public_record as vpr


class TestVerdict(unittest.TestCase):
    def _copy_certs(self, repo_dir):
        tsa_dir = os.path.join(repo_dir, "tsa")
        os.makedirs(tsa_dir, exist_ok=True)
        ops_tsa = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "ops", "tsa")
        for name in ("freetsa-cacert.pem", "freetsa-tsa.crt", "digicert-trusted-root-g4.pem"):
            shutil.copy2(os.path.join(ops_tsa, name), os.path.join(tsa_dir, name))

    def _run_main(self, args):
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            rc = vpr.main(args)
        output = stdout.getvalue()
        return rc, output

    def test_no_site_is_incomplete_not_pass(self):
        with tempfile.TemporaryDirectory() as repo:
            self._copy_certs(repo)
            rc, out = self._run_main([repo])
            self.assertEqual(rc, 3)
            lines = [ln for ln in out.splitlines() if ln.strip()]
            self.assertTrue(lines[-1].startswith("VERIFY RESULT: INCOMPLETE"))
            self.assertNotIn("VERIFY RESULT: PASS", out)
            self.assertTrue(any(ln.startswith("SKIP chain:") for ln in out.splitlines()))

    def test_a_fail_still_fails(self):
        with tempfile.TemporaryDirectory() as repo:
            rc, out = self._run_main([repo])
            self.assertEqual(rc, 1)
            lines = [ln for ln in out.splitlines() if ln.strip()]
            self.assertTrue(lines[-1].startswith("VERIFY RESULT: FAIL"))

    def test_pass_needs_every_check_to_run(self):
        def fake_verify(repo, site, n_stmts, openssl, rep):
            rep("PASS", "statements: ok")
        with mock.patch.object(vpr, "verify", side_effect=fake_verify):
            with tempfile.TemporaryDirectory() as repo:
                rc, out = self._run_main([repo])
                self.assertEqual(rc, 0)
                lines = [ln for ln in out.splitlines() if ln.strip()]
                self.assertTrue(lines[-1].startswith("VERIFY RESULT: PASS"))

    def test_skip_alone_is_not_pass(self):
        def fake_verify(repo, site, n_stmts, openssl, rep):
            rep("PASS", "x")
            rep("SKIP", "chain: y")
        with mock.patch.object(vpr, "verify", side_effect=fake_verify):
            with tempfile.TemporaryDirectory() as repo:
                rc, out = self._run_main([repo])
                self.assertEqual(rc, 3)
                lines = [ln for ln in out.splitlines() if ln.strip()]
                self.assertTrue(lines[-1].startswith("VERIFY RESULT: INCOMPLETE"))

    def test_nothing_checked_is_not_pass(self):
        def fake_verify(repo, site, n_stmts, openssl, rep):
            rep("WARN", "w")
        with mock.patch.object(vpr, "verify", side_effect=fake_verify):
            with tempfile.TemporaryDirectory() as repo:
                rc, out = self._run_main([repo])
                self.assertEqual(rc, 3)
                lines = [ln for ln in out.splitlines() if ln.strip()]
                self.assertTrue(lines[-1].startswith("VERIFY RESULT: INCOMPLETE"))

    def test_history_without_git_is_a_skip(self):
        # A ZIP download has no .git: the append-only checks did not run, so a
        # --site run must not end PASS on them.
        with tempfile.TemporaryDirectory() as repo:
            rep = vpr.Report(quiet=True)
            vpr.check_history(repo, [], rep)
            self.assertEqual((rep.n["SKIP"], rep.n["PASS"]), (1, 0))


if __name__ == "__main__":
    unittest.main()
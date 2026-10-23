"""ops/revalidate-structural.sh under the licence switch: the revalidation's accuracies are
Alpaca-derived, so ops/revalidation-status.json (public) carries them only with
SIGNALDECK_PUBLISH_DERIVED=1; otherwise it is a figure-free stamp and the full snapshot
goes to data/ (gitignored)."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

STUB = '''import argparse, datetime, json
ap = argparse.ArgumentParser()
ap.add_argument("--json")
a = ap.parse_args()
json.dump({"generated": datetime.datetime.now(datetime.timezone.utc).isoformat(),
           "arms": {"active_only": {"accuracy": 0.8364, "n": 18258},
                    "survivorship_clean": {"accuracy": 0.8304, "n": 75654}}},
          open(a.json, "w", encoding="utf-8"))
'''


@unittest.skipUnless(shutil.which("bash"), "needs bash")
class RevalidationLicence(unittest.TestCase):
    def run_script(self, value: str | None) -> tuple[dict, dict]:
        tmp = os.path.realpath(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp, True)
        for d in ("ops", "tools"):
            os.makedirs(os.path.join(tmp, d))
        shutil.copy(os.path.join(ROOT, "ops", "revalidate-structural.sh"), os.path.join(tmp, "ops"))
        shutil.copy(os.path.join(ROOT, "tools", "check_revalidation.py"), os.path.join(tmp, "tools"))
        with open(os.path.join(tmp, "tools", "revalidate_structural.py"), "w", encoding="utf-8") as f:
            f.write(STUB)
        env = dict(os.environ, SIGNALDECK_PYTHON=sys.executable.replace(os.sep, "/"))
        env.pop("SIGNALDECK_PUBLISH_DERIVED", None)
        if value is not None:
            env["SIGNALDECK_PUBLISH_DERIVED"] = value
        r = subprocess.run([shutil.which("bash"), "ops/revalidate-structural.sh", "-force"],
                           cwd=tmp, env=env, capture_output=True, timeout=120, stdin=subprocess.DEVNULL)
        log = os.path.join(tmp, "logs", "revalidation.out.log")
        log_text = ""
        if os.path.exists(log):
            with open(log, encoding="utf-8", errors="replace") as f:
                log_text = f.read()
        self.assertEqual(r.returncode, 0, log_text + r.stderr.decode("utf-8", "replace"))
        with open(os.path.join(tmp, "ops", "revalidation-status.json"), encoding="utf-8") as f:
            public = json.load(f)
        with open(os.path.join(tmp, "data", "revalidation-status.json"), encoding="utf-8") as f:
            private = json.load(f)
        return public, private

    def test_without_permission_ops_gets_a_figure_free_stamp(self) -> None:
        for value in (None, "0", "true", " 1"):
            with self.subTest(value=value):
                public, private = self.run_script(value)
                self.assertNotIn("arms", public)
                self.assertNotIn("0.83", json.dumps(public))
                self.assertTrue(public.get("withheld"))
                self.assertEqual(public["generated"], private["generated"])
                self.assertIn("arms", private, "the full snapshot must still be kept privately")

    def test_written_permission_publishes_the_snapshot(self) -> None:
        public, private = self.run_script("1")
        self.assertEqual(public, private)
        self.assertNotIn("withheld", public)


class RevalidationGateSelfcheck(unittest.TestCase):
    def test_selfcheck(self) -> None:
        # CI runs the gate on the committed file only; its built-in assertions
        # (including the withheld-stamp cases) run here.
        sys.path.insert(0, os.path.join(ROOT, "tools"))
        self.addCleanup(sys.path.remove, os.path.join(ROOT, "tools"))
        import check_revalidation
        check_revalidation._selfcheck()


if __name__ == "__main__":
    unittest.main()

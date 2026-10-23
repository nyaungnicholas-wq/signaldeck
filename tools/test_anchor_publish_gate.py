"""LICENCE GATE (23 Oct 2026) in ops/anchor-publish.sh.

The public anchors repo used to receive, on every run, the accuracy registry,
a README track record rendered from it and PREREGISTRATION.md. All three carry
accuracy statistics computed from Alpaca market data, which Alpaca licenses for
personal use only and forbids publishing, derivations included. The publisher
now refreshes them only with SIGNALDECK_PUBLISH_DERIVED=1; the hashes still go.

This runs the REAL script in a sandbox: a copy of ops/ and tools/, a scratch
anchors clone, no database, and the daemon URL rewritten to a dead port so the
run can never reach the live daemon (its /api/ledger/verify can write an anchor).
"""

from __future__ import annotations

import glob
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LIVE_API = 'API="http://127.0.0.1:8322"'
OLD = {"README.md": "OLD README\n", "accuracy_registry.json": "OLD REGISTRY\n",
       "PREREGISTRATION.md": "OLD PROTOCOL\n"}
REGISTRY = '{"status":"REFUSED","rows":[],"refusal_reason":"fixture"}\n'


def _write(path: str, text: str) -> None:
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(text)


def _read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


@unittest.skipUnless(shutil.which("bash") and shutil.which("git"), "needs bash and git")
class AnchorPublishLicenceGate(unittest.TestCase):
    def setUp(self) -> None:
        # realpath: Windows hands out 8.3 short names (NICHOL~1), which bash
        # cannot open.
        self.tmp = os.path.realpath(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.sd = os.path.join(self.tmp, "sd")
        for d in ("ops/tsa", "tools", "data", "logs"):
            os.makedirs(os.path.join(self.sd, d))
        src = _read(os.path.join(ROOT, "ops", "anchor-publish.sh"))
        self.assertEqual(src.count(LIVE_API), 1, "the daemon URL moved; the sandbox would hit the live daemon")
        _write(os.path.join(self.sd, "ops", "anchor-publish.sh"), src.replace(LIVE_API, 'API="http://127.0.0.1:9"'))
        for name in ("lib-portable.sh", "anchors-VERIFY.md"):
            shutil.copy(os.path.join(ROOT, "ops", name), os.path.join(self.sd, "ops", name))
        for p in glob.glob(os.path.join(ROOT, "ops", "tsa", "*")):
            shutil.copy(p, os.path.join(self.sd, "ops", "tsa"))
        for p in glob.glob(os.path.join(ROOT, "tools", "*.py")):
            shutil.copy(p, os.path.join(self.sd, "tools"))
        _write(os.path.join(self.sd, "data", "accuracy_registry.json"), REGISTRY)
        _write(os.path.join(self.sd, "PREREGISTRATION.md"), "NEW PROTOCOL\n")
        self.repo = os.path.join(self.tmp, "anchors")
        os.makedirs(self.repo)
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        for name, text in OLD.items():
            _write(os.path.join(self.repo, name), text)

    def publish(self, **env: str) -> str:
        def sh(p: str) -> str:  # bash reads a backslash as an escape, not a separator
            return p.replace(os.sep, "/")
        e = dict(os.environ)
        e.pop("SIGNALDECK_PUBLISH_DERIVED", None)
        e.update(SIGNALDECK_ANCHOR_REPO=sh(self.repo), SIGNALDECK_DB=sh(os.path.join(self.tmp, "absent.db")),
                 SD_PY=sh(sys.executable), **env)
        # which(), not a bare "bash": on Windows CreateProcess searches System32
        # first and would start WSL's bash, which cannot see these paths.
        subprocess.run([shutil.which("bash"), sh(os.path.join(self.sd, "ops", "anchor-publish.sh"))], env=e,
                       timeout=900, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        return _read(os.path.join(self.sd, "logs", "anchor-publish.log"))

    def test_derived_files_are_not_refreshed_by_default(self) -> None:
        log = self.publish()
        self.assertIn("not refreshed (Alpaca-derived statistics", log)
        for name, text in OLD.items():
            self.assertEqual(_read(os.path.join(self.repo, name)), text, name + " was refreshed without permission")

    def test_written_permission_refreshes_them(self) -> None:
        # Positive control: the gate, not a broken sandbox, kept them above.
        log = self.publish(SIGNALDECK_PUBLISH_DERIVED="1")
        self.assertNotIn("not refreshed", log)
        self.assertEqual(_read(os.path.join(self.repo, "accuracy_registry.json")), REGISTRY)
        self.assertEqual(_read(os.path.join(self.repo, "PREREGISTRATION.md")), "NEW PROTOCOL\n")
        self.assertIn("GRADING REFUSED", _read(os.path.join(self.repo, "README.md")))


if __name__ == "__main__":
    unittest.main()

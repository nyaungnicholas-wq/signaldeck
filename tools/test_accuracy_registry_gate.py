"""Guards the licence gate in ops/accuracy-registry.sh: README.md, partials and the INCLUDES documents carry Alpaca-derived grades and are regenerated only with SIGNALDECK_PUBLISH_DERIVED=1."""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _write(path: str, text: str) -> None:
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(text)


def _read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


@unittest.skipUnless(shutil.which("bash"), "needs bash")
class AccuracyRegistryLicenceGate(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = os.path.realpath(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, True)

        self.sd = os.path.join(self.tmp, "sd")
        os.makedirs(os.path.join(self.sd, "partials"))
        os.makedirs(os.path.join(self.sd, "logs"))
        _write(os.path.join(self.sd, "partials", "INCLUDES.txt"), "")

        src = _read(os.path.join(ROOT, "ops", "accuracy-registry.sh"))
        start_marker = "# LICENCE GATE (23 Oct 2026), the same switch as ops/anchor-publish.sh"
        end_marker = "# H9: page on VERDICT TRANSITIONS"
        self.assertEqual(src.count(start_marker), 1, "gate markers moved")
        self.assertEqual(src.count(end_marker), 1, "gate markers moved")
        start_idx = src.index(start_marker)
        end_idx = src.index(end_marker)
        self.block = src[start_idx:end_idx]

    def run_block(self, **env: str) -> tuple[str, str, str]:
        def sh(p: str) -> str:
            return p.replace(os.sep, "/")

        sd_sh = sh(self.sd)
        harness_path = os.path.join(self.tmp, "harness.sh")
        harness = (
            f'SD="{sd_sh}"\n'
            f'LOG="$SD/logs/accuracy-registry.log"\n'
            'stubpy() { printf \'%s\\n\' "$*" >> "$SD/calls.txt"; cat > /dev/null; }\n'
            'PY=stubpy\n'
            f'{self.block}\n'
            'echo "docs_stale=$docs_stale" >> "$SD/result.txt"\n'
        )
        _write(harness_path, harness)

        e = dict(os.environ)
        e.pop("SIGNALDECK_PUBLISH_DERIVED", None)
        e.update(**env)

        result = subprocess.run(
            [shutil.which("bash"), sh(harness_path)],
            env=e,
            timeout=120,
            capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr.decode("utf-8", "replace"))

        calls = _read(os.path.join(self.sd, "calls.txt")) if os.path.exists(os.path.join(self.sd, "calls.txt")) else ""
        log = _read(os.path.join(self.sd, "logs", "accuracy-registry.log")) if os.path.exists(os.path.join(self.sd, "logs", "accuracy-registry.log")) else ""
        result_txt = _read(os.path.join(self.sd, "result.txt")) if os.path.exists(os.path.join(self.sd, "result.txt")) else ""
        return calls, log, result_txt

    def test_default_regenerates_nothing(self) -> None:
        calls, log, result = self.run_block()
        self.assertEqual(calls, "", "a public document was regenerated without Alpaca's written permission")
        self.assertIn("licence: README.md", log)
        self.assertIn("docs_stale=0", result)

    def test_written_permission_regenerates(self) -> None:
        calls, log, result = self.run_block(SIGNALDECK_PUBLISH_DERIVED="1")
        self.assertIn("live_accuracy.py --write", calls)
        self.assertIn("deck_facts.py --inject", calls)
        self.assertTrue(any(line.startswith("- ") for line in calls.splitlines()))
        self.assertNotIn("licence:", log)


if __name__ == "__main__":
    unittest.main()
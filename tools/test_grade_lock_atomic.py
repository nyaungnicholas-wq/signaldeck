"""The graders' lock must be an exclusive test-and-set on every host.

2026-11-13: CI's tools job failed test_two_overlapping_graders_publish_one_whole_grade
3 times in 5 with BOTH graders publishing and neither logging a lock line. The
runner image ubuntu-26.04 ships uutils coreutils, whose `mkdir` let two racing
calls on one path both succeed in 59 of 200 trials. The lock is now taken by
`take_lock` (os.mkdir, the mkdir(2) syscall). This races two copies of each
script's take_lock directly and requires that at most one wins, every round.
"""
from __future__ import annotations

import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
ROUNDS = 40


def _take_lock_fn(script: Path) -> str:
    text = script.read_text(encoding="utf-8").replace("\r\n", "\n")
    start = text.index("take_lock() {")
    return text[start:text.index("\n}\n", start) + 3]


class GradeLockIsExclusive(unittest.TestCase):
    def test_two_racing_take_locks_never_both_win(self):
        # The PATH lookup, passed explicitly: on Windows a bare "bash" starts the
        # WSL launcher (CreateProcess searches System32 first).
        bash = shutil.which("bash")
        if bash is None:
            self.skipTest("bash not available")
        for script in (REPO / "ops" / "grade.sh", REPO / "ops" / "accuracy-registry.sh"):
            fn = _take_lock_fn(script)
            with self.subTest(script=script.name), tempfile.TemporaryDirectory() as d:
                base = Path(d).resolve().as_posix()
                body = (
                    f'PY="{Path(sys.executable).as_posix()}"\n{fn}\n'
                    f'both=0\n'
                    f'for i in $(seq 1 {ROUNDS}); do\n'
                    f'  LOCK="{base}/L$i"; rm -f "{base}/a" "{base}/b"\n'
                    f'  ( take_lock && echo x > "{base}/a" ) &\n'
                    f'  ( take_lock && echo x > "{base}/b" ) &\n'
                    f'  wait\n'
                    f'  [ -s "{base}/a" ] && [ -s "{base}/b" ] && both=$((both + 1))\n'
                    f'  [ -d "$LOCK" ] || echo "NO LOCK DIR round $i"\n'
                    f'done\n'
                    f'echo "both=$both"\n'
                )
                proc = subprocess.run([bash, "-s"], input=body.encode("utf-8"),
                                      capture_output=True, timeout=600)
                out = (proc.stdout + proc.stderr).decode("utf-8", "replace")
                self.assertIn("both=0", out, out)
                self.assertNotIn("NO LOCK DIR", out, out)


if __name__ == "__main__":
    unittest.main(verbosity=2)

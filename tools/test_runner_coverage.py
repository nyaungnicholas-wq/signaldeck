"""Every tools/test_*.py must be reachable by one of CI's two runners.

CI runs tests two ways (.github/workflows/ci.yml):

  1. `python3 -m unittest discover -s tools -p 'test_*.py'` -- collects ONLY files
     that define a unittest.TestCase subclass.
  2. a bash loop that skips any file defining a TestCase and runs the rest as
     `python3 <file>` -- which executes only what an `if __name__ == "__main__"`
     block invokes.

A file with NEITHER a TestCase NOR a __main__ block falls through both. CI runs it,
Python defines the functions, the process exits 0, and ZERO assertions execute. The
step reports success. ci.yml:207-217 already documents this failure mode for
test_verify_backup.py, but the remediation only added runner #2 -- it never gated the
"no runner at all" case, so two files were still invisible when this was written:

    tools/test_selection_honesty.py   6 tests, 0 executed
    tools/test_anchor_liveness.py     6 tests, 0 executed

Both passed under pytest, so nothing was failing -- which is exactly why it survived.
The guards they protect (one-sided-book refusal; "an unreadable DB is inconclusive,
not a pass") could have regressed undetected indefinitely.

This gate is static on purpose: it asserts a runner EXISTS. Executing every test file
to count assertions would make this file quadratic in the suite and recursive on
itself. "Has no runner" is the defect that actually occurred and the one worth pinning.
"""
import glob
import os
import re
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TESTCASE_RE = re.compile(r"^[ \t]*class[ \t]+[A-Za-z_]\w*\([^)]*TestCase", re.M)
MAIN_RE = re.compile(r"^if __name__\s*==\s*[\"']__main__[\"']\s*:", re.M)


def _sources():
    for path in sorted(glob.glob(os.path.join(HERE, "test_*.py"))):
        with open(path, encoding="utf-8") as fh:
            yield os.path.basename(path), fh.read()


class TestEveryTestFileHasARunner(unittest.TestCase):
    def test_no_test_file_is_invisible_to_both_runners(self):
        orphans = [name for name, src in _sources()
                   if not TESTCASE_RE.search(src) and not MAIN_RE.search(src)]
        self.assertEqual(orphans, [], (
            "these files define tests that NO CI runner executes -- they need either a "
            "unittest.TestCase subclass or an `if __name__ == \"__main__\"` block that "
            "runs them: " + ", ".join(orphans)))

    def test_script_style_main_actually_calls_something(self):
        """A __main__ block that does nothing satisfies the letter and not the point."""
        inert = []
        for name, src in _sources():
            if TESTCASE_RE.search(src):
                continue
            m = MAIN_RE.search(src)
            if not m:
                continue                      # already reported by the test above
            body = src[m.end():].strip()
            if not body or re.fullmatch(r"pass|\.\.\.", body):
                inert.append(name)
        self.assertEqual(inert, [], (
            "script-style test files whose __main__ block runs nothing: "
            + ", ".join(inert)))

    def test_this_gate_can_actually_fail(self):
        """A gate nobody has seen fail is a gate nobody should trust."""
        self.assertTrue(TESTCASE_RE.search("class Foo(unittest.TestCase):\n"))
        self.assertIsNone(TESTCASE_RE.search("def test_x():\n    pass\n"))
        self.assertTrue(MAIN_RE.search('if __name__ == "__main__":\n    main()\n'))
        self.assertIsNone(MAIN_RE.search("def main():\n    pass\n"))


if __name__ == "__main__":
    unittest.main()

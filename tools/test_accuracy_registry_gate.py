"""Guards the licence gate in ops/accuracy-registry.sh: README.md, partials and the INCLUDES documents carry Alpaca-derived grades and are regenerated only with SIGNALDECK_PUBLISH_DERIVED=1."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _write(path: str, text: str) -> None:
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(text)


def _read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


def _sh(p: str) -> str:
    return p.replace(os.sep, "/")


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
        for name in ("calls.txt", "result.txt", os.path.join("logs", "accuracy-registry.log")):
            path = os.path.join(self.sd, name)
            if os.path.exists(path):
                os.remove(path)
        sd_sh = _sh(self.sd)
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
            [shutil.which("bash"), _sh(harness_path)],
            env=e,
            timeout=120,
            capture_output=True,
            stdin=subprocess.DEVNULL,
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

    def test_any_value_but_exactly_1_withholds(self) -> None:
        # Fail closed: only the exact value "1" records Alpaca's permission. A
        # `-n` or `!= 0` test would let each of these regenerate the documents.
        for value in ("0", "true", "yes", " 1", "1 ", ""):
            with self.subTest(value=value):
                calls, log, _ = self.run_block(SIGNALDECK_PUBLISH_DERIVED=value)
                self.assertEqual(calls, "", f"SIGNALDECK_PUBLISH_DERIVED={value!r} regenerated a public document")
                self.assertIn("licence:", log)


FIGURE_BLOCK = (
    "<!-- LIVE-ACCURACY:BEGIN -->\n## Live accuracy (auto-updated)\n\n"
    "| trend21 | 22220 | 79.6% |\n<!-- LIVE-ACCURACY:END -->\n")


@unittest.skipUnless(shutil.which("bash"), "needs bash")
class AccuracyRegistryRefusalPathLicence(unittest.TestCase):
    """The refusal path. README.md and STRATEGY_DECK.md section 8 are public and
    Alpaca-derived, so a refused cycle writes only the licence-withheld section
    unless SIGNALDECK_PUBLISH_DERIVED is exactly 1. The registry envelope
    (data/, gitignored) is written either way."""

    def setUp(self) -> None:
        self.tmp = os.path.realpath(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.sd = os.path.join(self.tmp, "sd")
        for d in ("partials", "logs", "data", "tools"):
            os.makedirs(os.path.join(self.sd, d))
        _write(os.path.join(self.sd, "partials", "INCLUDES.txt"), "")
        # The real generator, so --readme runs end to end against this
        # temporary README instead of only being recorded.
        shutil.copy(os.path.join(ROOT, "tools", "live_accuracy.py"), os.path.join(self.sd, "tools"))
        self.readme = os.path.join(self.sd, "README.md")

        src = _read(os.path.join(ROOT, "ops", "accuracy-registry.sh"))
        start_marker = "# REFUSAL PATH — the whole point of this branch"
        end_marker = 'rm -f "$PREV_BACKUP"\n\n# The grade is real'
        self.assertEqual(src.count(start_marker), 1, "refusal-path markers moved")
        self.assertEqual(src.count(end_marker), 1, "refusal-path markers moved")
        self.block = src[src.index(start_marker):src.index(end_marker)]

        sys.path.insert(0, os.path.join(ROOT, "tools"))
        self.addCleanup(sys.path.remove, os.path.join(ROOT, "tools"))
        import live_accuracy
        self.la = live_accuracy

    def run_block(self, value: str | None) -> tuple[str, str, str]:
        _write(self.readme, "# SignalDeck\n\n" + FIGURE_BLOCK + "\n## Run it\n")
        for name in ("calls.txt", os.path.join("logs", "accuracy-registry.log"),
                     os.path.join("data", "accuracy_registry.json")):
            path = os.path.join(self.sd, name)
            if os.path.exists(path):
                os.remove(path)
        harness = (
            f'SD="{_sh(self.sd)}"\n'
            'export PYTHONUTF8=1  # as ops/accuracy-registry.sh does\n'
            'LOG="$SD/logs/accuracy-registry.log"\n'
            'OUT="$SD/data/accuracy_registry.json"\n'
            'PREV="$SD/data/prev.json"; PREV_BACKUP="$SD/data/prev.bak"\n'
            'STDERR_CAPTURE="$SD/data/stderr.txt"\n'
            'refusal_reason="publication gate: contains 15 collapsed cross-section(s) of 35 day(s): 2026-10-01"\n'
            f'REALPY="{_sh(sys.executable)}"\n'
            'stubpy() {\n'
            '  if [ "$1" = - ]; then "$REALPY" "$@"; return; fi\n'
            '  printf \'%s\\n\' "$*" >> "$SD/calls.txt"\n'
            '  case "$*" in *live_accuracy.py\\ --readme*)\n'
            '    cp "$SD/README.md" "$SD/readme-before-reset.md"; "$REALPY" "$@" ;; esac\n'
            '}\n'
            'PY=stubpy\n'
            'report_uncommitted_docs() { :; }; notify_remote() { :; }; sd_notify() { :; }\n'
            f'{self.block}\n'
        )
        path = os.path.join(self.tmp, "harness.sh")
        _write(path, harness)
        e = dict(os.environ)
        e.pop("SIGNALDECK_PUBLISH_DERIVED", None)
        if value is not None:
            e["SIGNALDECK_PUBLISH_DERIVED"] = value
        result = subprocess.run([shutil.which("bash"), _sh(path)], env=e, timeout=120,
                                capture_output=True, stdin=subprocess.DEVNULL)
        self.assertEqual(result.returncode, 0, result.stderr.decode("utf-8", "replace"))
        with open(os.path.join(self.sd, "data", "accuracy_registry.json"), encoding="utf-8") as f:
            self.assertEqual(json.load(f)["status"], "REFUSED", "the private envelope must always be written")
        calls_p = os.path.join(self.sd, "calls.txt")
        calls = _read(calls_p) if os.path.exists(calls_p) else ""
        return calls, _read(os.path.join(self.sd, "logs", "accuracy-registry.log")), _read(self.readme)

    def test_without_permission_readme_carries_only_the_withheld_section(self) -> None:
        for value in (None, "0", "true", " 1"):
            with self.subTest(value=value):
                calls, log, readme = self.run_block(value)
                self.assertEqual(self.la.readme_block(readme).strip("\n"), self.la.README_LICENCE_SECTION,
                                 "a refused cycle left README carrying something other than the withheld section")
                for leak in ("GRADING REFUSED", "collapsed", "79.6%"):
                    self.assertNotIn(leak, readme)
                self.assertIn("live_accuracy.py --readme", calls)
                # The refusal notice itself must never be written, not merely
                # overwritten afterwards: a failed --readme would leave it.
                self.assertEqual(_read(os.path.join(self.sd, "readme-before-reset.md")),
                                 "# SignalDeck\n\n" + FIGURE_BLOCK + "\n## Run it\n",
                                 "the refusal path wrote README before the withheld reset")
                self.assertNotIn("deck_facts.py", calls, "section 8 was re-injected without permission")
                self.assertIn("licence: STRATEGY_DECK.md section 8", log)

    def test_written_permission_publishes_the_refusal(self) -> None:
        calls, log, readme = self.run_block("1")
        self.assertIn("GRADING REFUSED", readme)
        self.assertNotIn("79.6%", readme)
        self.assertIn("deck_facts.py --inject", calls)
        self.assertNotIn("--readme", calls)
        self.assertNotIn("licence:", log)


if __name__ == "__main__":
    unittest.main()

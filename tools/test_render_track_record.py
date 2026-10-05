"""Checks for tools/render_track_record.py, the jq-free public README renderer.

WHY THIS EXISTS. ops/anchor-publish.sh rendered the public track-record README
with a 25-line jq program. jq is NOT installed under the Git Bash that runs the
scheduled tasks on this machine (`command -v jq` fails), so the whole publish
step has been dead since 2026-07-27 — logs/anchor-publish.log stops there, and
the public anchors repo that daemon/internal/pipeline/prereg.go names as the
chain head has received nothing since. The render moved to Python, which the
repo already uses everywhere for exactly this reason, so these checks pin the
output shape the public repo depends on.
"""

import json
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "tools" / "render_track_record.py"

REGISTRY = {
    "generated": "2026-08-09T21:05:15Z",
    "min_independent_n": 30,
    "survivorship_epoch": "2026-07-27",
    "null_policy": "prequential majority",
    "rows": [
        {
            "predictor": "flagship-1d",
            "family": "directional",
            "band": "1d",
            "live_n": 753,
            "live_acc": 0.4838,
            "ci": [0.4481, 0.5196],
            "null_prequential": 0.5194,
            "verdict": "FAILED",
            "note": "retired | not emitting",
        },
        {
            # Every optional field absent: family, ci, note, and a null accuracy.
            # A row the grader could not score must still render, or the public
            # record quietly drops its worst entries.
            "predictor": "pipe|name",
            "band": "1w",
            "live_acc": None,
            "null_prequential": None,
            "verdict": "INSUFFICIENT",
            "ci_method": "wilson (n<30)",
            # Explicit JSON nulls, not missing keys: `.get(k, default)` returns
            # the default only when the KEY is absent, so these used to render
            # the literal string "None" into the public table.
            "note": None,
            "live_n": None,
        },
    ],
}


def render(registry):
    path = ROOT / "tools" / ".render_track_record_fixture.json"
    path.write_text(json.dumps(registry), encoding="utf-8")
    try:
        out = subprocess.run(
            [sys.executable, str(SCRIPT), str(path)],
            capture_output=True, text=True, encoding="utf-8", cwd=str(ROOT),
        )
        return out
    finally:
        path.unlink(missing_ok=True)


class TestRenderTrackRecord(unittest.TestCase):
    def test_renders_every_row_including_unscored_ones(self):
        out = render(REGISTRY)
        self.assertEqual(out.returncode, 0, f"exit {out.returncode}: {out.stderr}")
        md = out.stdout
        self.assertIn("# SignalDeck — Public Track Record", md)
        self.assertIn("| Predictor | Family | Band | n | Accuracy | 95% CI | Prequential null | Verdict | Note |", md)
        # Both rows present — the FAILED one and the unscorable one.
        self.assertIn("flagship-1d", md)
        self.assertIn("FAILED", md)
        self.assertIn("INSUFFICIENT", md)
        # Header metadata is carried through verbatim.
        self.assertIn("2026-08-09T21:05:15Z", md)
        self.assertIn("2026-07-27", md)

    def test_null_accuracy_renders_as_a_dash_not_a_zero(self):
        # A withheld number shown as 0.0% is a measurement that was never taken.
        out = render(REGISTRY)
        body = [ln for ln in out.stdout.splitlines() if ln.startswith("| pipe")]
        self.assertEqual(len(body), 1, f"expected one row for the unscored predictor, got {body}")
        self.assertIn("—", body[0])
        self.assertNotIn("0.0%", body[0])
        # And a JSON null never leaks as the string "None" into the public table.
        self.assertNotIn("None", body[0])
        # And the CI falls back to the stated method rather than inventing bounds.
        self.assertIn("wilson (n<30)", body[0])

    def test_pipes_in_values_are_escaped_so_the_table_survives(self):
        out = render(REGISTRY)
        body = [ln for ln in out.stdout.splitlines() if "pipe" in ln]
        self.assertTrue(body, "the row with a pipe in its name vanished")
        self.assertIn(r"pipe\|name", body[0])

    def test_empty_registry_is_refused_rather_than_published(self):
        # This replaces `jq -e '.rows | length > 0'`: publishing an empty
        # registry would silently replace the public record with nothing.
        out = render({"generated": "x", "rows": []})
        self.assertNotEqual(out.returncode, 0, "an empty registry must not render")

    def test_runs_against_the_real_registry(self):
        # The contract is "renders IFF there are rows", and both halves are
        # asserted here against whatever state the live registry is actually in.
        #
        # This asserted exit 0 unconditionally, which put it in direct conflict
        # with test_empty_registry_is_refused_rather_than_published above. The
        # publication gate in tools/accuracy_registry.py writes
        # {"status": "REFUSED", "rows": []} ON PURPOSE when the graded window is
        # statistically unusable, and that state persists for WEEKS while the
        # offending window ages out — so one of the two tests had to be red the
        # whole time. A suite that is red by design is a suite whose next real
        # failure nobody investigates.
        #
        # ops/anchor-publish.sh uses the renderer's non-zero exit AS its emptiness
        # guard and aborts the publish on it, so exiting 0 on an empty registry
        # would silently ship an empty public track record. That is why the
        # refused branch asserts failure rather than skipping.
        real = ROOT / "data" / "accuracy_registry.json"
        if not real.exists():
            self.skipTest("data/accuracy_registry.json not present")
        payload = json.loads(real.read_text(encoding="utf-8"))
        out = subprocess.run(
            [sys.executable, str(SCRIPT), str(real)],
            capture_output=True, text=True, encoding="utf-8", cwd=str(ROOT),
        )
        if not payload.get("rows") and payload.get("status") == "REFUSED":
            # A REFUSED envelope is a publication decision, not an empty registry
            # (release ledger 2026-09-08, F1/F17): it renders a refusal notice with
            # no figures and exits 0 so the anchor publish carries the refusal.
            self.assertEqual(out.returncode, 0, "a REFUSED envelope must render a refusal notice")
            self.assertIn("GRADING REFUSED", out.stdout)
            self.assertNotRegex(out.stdout, r"\d+\.\d+%", "a refusal must print no accuracy figure")
            return
        elif not payload.get("rows"):
            self.assertNotEqual(
                out.returncode, 0,
                f"registry is {payload.get('status', 'rows-empty')} and must NOT render, "
                f"but the renderer exited 0")
            return
        self.assertEqual(out.returncode, 0, f"exit {out.returncode}: {out.stderr}")
        rows = [ln for ln in out.stdout.splitlines() if ln.startswith("| ") and "---" not in ln]
        self.assertGreater(len(rows), 1, "the real registry rendered no data rows")



class TestSD30RenderTrackRecord(unittest.TestCase):
    """The public anchors README withholds SD-30 rows from the same Go switch,
    reversed here in a throwaway tree so the committed default does not matter."""

    REGISTRY = {
        "generated": "2026-10-02T00:00:00Z", "min_independent_n": 30,
        "survivorship_epoch": "2026-07-24", "null_policy": "prequential majority",
        "rows": [
            {"predictor": "directional-ensemble (1d)", "family": "direction", "band": "all",
             "live_n": 1052, "live_acc": 0.5047, "ci": None, "ci_method": "withheld",
             "null_prequential": 0.5932, "verdict": "INSUFFICIENT DAYS (3/10 credible days)",
             "note": "live forward record"},
            {"predictor": "trend21", "family": "structure", "band": "all", "live_n": 11954,
             "live_acc": 0.7833, "ci": None, "ci_method": "withheld", "null_prequential": 0.7864,
             "verdict": "INSUFFICIENT BLOCKS (2/10)", "note": None},
        ],
    }

    def _render(self, flag, registry=None):
        go = (ROOT / "daemon" / "internal" / "publication" / "withhold.go").read_text(encoding="utf-8")
        self.reason = re.search(r'^const SD30Reason = "([^"]+)"', go, re.M).group(1)
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            (tmp / "tools").mkdir()
            for name in ("render_track_record.py", "live_accuracy.py"):
                shutil.copy(ROOT / "tools" / name, tmp / "tools" / name)
            pub = tmp / "daemon" / "internal" / "publication"
            pub.mkdir(parents=True)
            (pub / "withhold.go").write_text(
                re.sub(r"^var SD30Withheld = (true|false)", "var SD30Withheld = %s" % str(flag).lower(),
                       go, flags=re.M), encoding="utf-8")
            (tmp / "reg.json").write_text(json.dumps(registry or self.REGISTRY), encoding="utf-8")
            return subprocess.run([sys.executable, str(tmp / "tools" / "render_track_record.py"),
                                   str(tmp / "reg.json")], capture_output=True, text=True,
                                  encoding="utf-8", cwd=str(tmp))

    def _row(self, out, name):
        rows = [ln for ln in out.stdout.splitlines() if ln.startswith("| %s " % name)]
        self.assertEqual(len(rows), 1, out.stdout)
        return rows[0]

    def test_flag_on_withholds_directional_row(self):
        out = self._render(True)
        self.assertEqual(out.returncode, 0, out.stderr)
        row = self._row(out, "directional-ensemble (1d)")
        self.assertIn("withheld (SD-30)", row)
        self.assertIn(self.reason, row)
        for banned in ("50.5%", "59.3%", "INSUFFICIENT DAYS"):
            self.assertNotIn(banned, row)
        self.assertIn("78.3%", self._row(out, "trend21"))
        # The raw registry beside the README still holds them: say so (SD30-ANCHORS-JSON).
        self.assertIn("is published unaltered", out.stdout)

    def test_flag_on_with_nothing_withheld_prints_no_note(self):
        # A window re-registered hours ago holds no directional row (2026-10-04):
        # nothing is withheld, so the README must not say figures are withheld.
        rows = [r for r in self.REGISTRY["rows"] if r.get("family") not in ("direction", "benchmark")]
        self.assertTrue(rows)
        out = self._render(True, registry=dict(self.REGISTRY, rows=rows))
        self.assertEqual(out.returncode, 0, out.stderr)
        self.assertNotIn("is published unaltered", out.stdout)

    def test_flag_off_restores_directional_row(self):
        out = self._render(False)
        self.assertEqual(out.returncode, 0, out.stderr)
        row = self._row(out, "directional-ensemble (1d)")
        self.assertIn("50.5%", row)
        self.assertIn("INSUFFICIENT DAYS (3/10", row)
        self.assertNotIn("SD-30", out.stdout)


if __name__ == "__main__":
    unittest.main()

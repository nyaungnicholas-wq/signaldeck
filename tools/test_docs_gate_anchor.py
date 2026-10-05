#!/usr/bin/env python3
"""Marker-convention contract for tools/docs_gate.py's generated-region anchor.

WHY THIS EXISTS. The anchor check added on 2026-08-05 (`_region_is_anchored`)
only ever ran on markers written `<!-- BEGIN GENERATED: name -->`, with a colon.
Every marker actually emitted into this repository's documents is written
`<!-- BEGIN GENERATED live_accuracy -->`, no colon — see tools/live_accuracy.py
BEGIN/END. `grep -rn "BEGIN GENERATED:" --include=*.md .` matched nothing, so on
real documents the anchor never executed and a legitimately generated region got
no exemption at all: the gate stayed green only because that generated block
happens to contain no line pairing a percentage with a live-accuracy phrase.

These tests pin both halves of the repair: the convention the generators emit
must anchor, and a hand-typed marker pair must still be rejected in EITHER
convention.

Reuses tools/test_docs_gate.py's fixture rather than building a second one.
"""

from __future__ import annotations

import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from test_docs_gate import GateFixture  # noqa: E402

# A live-accuracy figure inside the generated block. Without it this file would
# prove nothing: with no percentage-plus-phrase line, an unrecognised region and
# a correctly anchored one both leave the gate green.
PARTIAL = (
    "<!-- BEGIN GENERATED live_accuracy -->\n"
    "\n"
    "Live accuracy 44.3% over 3,002 graded forecasts.\n"
    "\n"
    "<!-- END GENERATED live_accuracy -->\n"
)


class SpaceFormMarkerTest(unittest.TestCase):
    def setUp(self) -> None:
        self.fx = GateFixture()
        self.fx.owner = self
        self.addCleanup(self.fx.cleanup)
        # Mirror the real repository: live_accuracy is an EXTERNAL partial,
        # written by tools/live_accuracy.py, embedded with space-form markers.
        reg = self.fx.read_registry()
        reg["external_partials"] = ["live_accuracy", "live_accuracy.md"]
        self.fx.write_registry(reg)
        self.fx.write_doc("partials/live_accuracy.md", PARTIAL)

    def test_space_form_region_anchors_and_is_exempt(self):
        """FAILS before the fix: the markers parse as prose, so the 44.3% inside
        the generated block is reported as a hardcoded figure."""
        self.fx.add_strategy_doc("EMB.md", f"# Embedded\n\n{PARTIAL}")
        r = self.fx.check()
        self.assertEqual(
            r.returncode, 0,
            "a faithfully embedded space-form generated region was not "
            f"exempt:\n{r.stdout}\n{r.stderr}")

    def test_space_form_forged_region_is_still_rejected(self):
        """The 2026-08-05 exploit, retyped in the convention the fix accepts:
        a hand-typed marker pair around a fabricated figure must NOT exempt it."""
        self.fx.add_strategy_doc(
            "FORGE.md",
            "# Forged\n\n"
            "<!-- BEGIN GENERATED live_accuracy -->\n"
            "Our live accuracy is 91%.\n"
            "<!-- END GENERATED live_accuracy -->\n")
        r = self.fx.check()
        self.assertEqual(r.returncode, 1,
                         f"a hand-typed marker pair exempted 91%:\n{r.stdout}")
        codes = {v["check"] for v in json.loads(r.stdout)["violations"]}
        self.assertIn("single-source-of-truth", codes,
                      f"the anchor check did not report it; codes were {codes}")

    def test_a_missing_separator_is_not_a_marker(self):
        """`BEGIN GENERATEDlive_accuracy` must not open a region — otherwise the
        looser pattern hands out an exemption for a typo."""
        self.fx.add_strategy_doc(
            "TYPO.md",
            "# Typo\n\n"
            "<!-- BEGIN GENERATEDlive_accuracy -->\n"
            "Our live accuracy is 91%.\n"
            "<!-- END GENERATEDlive_accuracy -->\n")
        r = self.fx.check()
        self.assertEqual(r.returncode, 1, f"a malformed marker exempted 91%:\n{r.stdout}")
        codes = {v["check"] for v in json.loads(r.stdout)["violations"]}
        self.assertIn("no-hardcoded-live-accuracy", codes,
                      f"the 91% was not scanned as ordinary prose; codes were {codes}")


if __name__ == "__main__":
    unittest.main(verbosity=1)

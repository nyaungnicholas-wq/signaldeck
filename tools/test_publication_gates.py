"""Negative controls for the container publication wrapper, ops/grade.sh.

WHY THESE EXIST

ops/grade.sh decides whether a graded accuracy registry may become the file the
daemon serves. Until 2026-09-13 three separate ways of not knowing the answer
were spelled the same way as "yes":

  * the collapse gate published on exit 2, which is what a MISSING collapsecheck
    binary, a renamed flag and a bad --db path all return;
  * selection_honesty.py's exit code was discarded with `|| log ...`, and since
    its main() returns `1 if refused else 0` while an unhandled exception also
    exits 1, a traceback and a clean run that refused a row were the same byte;
  * the success heartbeat was written unchecked and followed unconditionally by
    `log "grade OK"; exit 0`.

Each of these is tested here by INDUCING it, not by reading the source. Every
scenario asserts the same two things unless it says otherwise:

    grade.sh exits non-zero, AND the published registry still holds the
    PREVIOUS content -- no ungated rows reached the file the daemon reads.

The happy path and the row-refusal path are here too, and they matter as much:
a gate that withholds everything is not honest, it is just broken, and the
row-refusal case is the one a careless "fail closed" rewrite would break.

ISOLATION. Nothing here touches the repo's data/ directory, the live database or
the live registry. Every run builds a throwaway sandbox under a TemporaryDirectory
with its own stub tools, its own sqlite file and its own PATH, and grade.sh is
driven entirely through the SIGNALDECK_* environment variables it already reads.

Run: .venv/Scripts/python.exe tools/test_publication_gates.py
"""

from __future__ import annotations

import json
import os
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
GRADE_SH = REPO / "ops" / "grade.sh"
REAL_PUBGATE = REPO / "tools" / "publication_gate.py"

PREV_REGISTRY = {
    "generated": "2026-09-01T00:00:00",
    "graded_at": "2026-09-01T00:00:00",
    "rows": [{"predictor": "PREVIOUSLY-PUBLISHED", "family": "direction"}],
}

# A registry shaped like the real one: one directional row with breadth
# tallies, which tools/publication_gate.py therefore requires an honesty block
# on, and one benchmark row which is exempt.
GOOD_ROWS = [
    {
        "predictor": "directional-ensemble (1d)",
        "family": "direction",
        "live_acc": 0.51,
        "null_acc": 0.50,
        "breadth": {"mean_daily_agreement": 0.9},
    },
    {
        "predictor": "prequential-majority (1d)",
        "family": "benchmark",
        "breadth": {"mean_daily_agreement": 1.0},
    },
]

HONESTY_OK = {
    "publishable": True,
    "one_sided": False,
    "reason": "",
    "result": {"schema": "skill/1"},
    "source": "tools/selection_honesty.py",
}


def _py(body: str) -> str:
    return "#!/usr/bin/env python3\n" + textwrap.dedent(body)


class Sandbox:
    """A throwaway /data + /app + PATH that ops/grade.sh can be pointed at."""

    def __init__(self) -> None:
        self.dir = Path(tempfile.mkdtemp(prefix="sd-pubgate-"))
        self.tools = self.dir / "tools"
        self.bin = self.dir / "bin"
        self.data = self.dir / "data"
        for d in (self.tools, self.bin, self.data):
            d.mkdir(parents=True)

        self.out = self.data / "accuracy_registry.json"
        self.db = self.data / "signaldeck.db"
        self.log = self.data / "logs" / "grade.log"

        self.out.write_text(json.dumps(PREV_REGISTRY), encoding="utf-8")

        # The protocol-document gate at the top of grade.sh must PASS, or every
        # scenario would refuse for that reason instead of the one under test.
        prereg = self.dir / "PREREGISTRATION.md"
        prereg.write_text("# protocol\n", encoding="utf-8")
        import hashlib

        digest = hashlib.sha256(prereg.read_bytes()).hexdigest()
        con = sqlite3.connect(self.db)
        con.execute("CREATE TABLE prereg_records (seq INTEGER, kind TEXT, spec_hash TEXT)")
        con.execute(
            "INSERT INTO prereg_records VALUES (1, 'prereg-document', ?)", (digest,)
        )
        con.commit()
        con.close()

        shutil.copy(REAL_PUBGATE, self.tools / "publication_gate.py")
        self.write_tool("backfill_delistings.py", _py("import sys; sys.exit(0)\n"))
        self.grader_writes(GOOD_ROWS, honesty=None)
        self.honesty_merges(HONESTY_OK, exit_code=0)
        self.heartbeat(exit_code=0)
        self.collapsecheck(exit_code=0)

    # -- stub factories ----------------------------------------------------
    def write_tool(self, name: str, body: str) -> None:
        (self.tools / name).write_text(body, encoding="utf-8")

    def grader_writes(self, rows, honesty=None, exit_code: int = 0, raw: str | None = None) -> None:
        """Stand in for tools/accuracy_registry.py (pinned; never invoked for real)."""
        payload = {
            "generated": "2026-09-13T12:00:00",
            "graded_at": "2026-09-13T12:00:00",
            "rows": rows,
        }
        if honesty is not None:
            for r in payload["rows"]:
                if r.get("family") == "direction":
                    r["honesty"] = honesty
        literal = raw if raw is not None else json.dumps(payload)
        self.write_tool(
            "accuracy_registry.py",
            _py(
                f"""
                import sys
                path = sys.argv[sys.argv.index("--json") + 1]
                open(path, "w", encoding="utf-8").write({literal!r})
                sys.exit({exit_code})
                """
            ),
        )

    def honesty_merges(self, honesty, exit_code: int = 0, crash: bool = False) -> None:
        if crash:
            body = _py(
                """
                import sys
                print("Traceback (most recent call last):", file=sys.stderr)
                raise RuntimeError("selection_honesty blew up halfway through the merge")
                """
            )
        else:
            body = _py(
                f"""
                import json, sys
                path = sys.argv[sys.argv.index("--json") + 1]
                reg = json.load(open(path, encoding="utf-8"))
                for r in reg.get("rows", []):
                    if r.get("family") == "direction":
                        r["honesty"] = {honesty!r}
                json.dump(reg, open(path, "w", encoding="utf-8"))
                sys.exit({exit_code})
                """
            )
        self.write_tool("selection_honesty.py", body)

    def heartbeat(self, exit_code: int = 0) -> None:
        self.write_tool(
            "grader_heartbeat.py",
            _py(f"import sys; print(' '.join(sys.argv[1:])); sys.exit({exit_code})\n"),
        )

    def collapsecheck(self, exit_code: int = 0, stdout: str = "", present: bool = True) -> None:
        """collapsecheck is resolved from PATH, so absence is a real scenario."""
        target = self.bin / "collapsecheck"
        if not present:
            if target.exists():
                target.unlink()
            return
        # A shell stub: grade.sh is POSIX sh and calls the bare name.
        target.write_text(
            f'#!/bin/sh\nprintf "%s\\n" {stdout!r}\nexit {exit_code}\n',
            encoding="utf-8",
        )
        target.chmod(0o755)

    # -- driving grade.sh --------------------------------------------------
    def run(self):
        env = dict(os.environ)
        env.update(
            SIGNALDECK_DB=str(self.db),
            SIGNALDECK_REGISTRY=str(self.out),
            SIGNALDECK_TOOLS=str(self.tools),
            SIGNALDECK_APP=str(self.dir),
            SIGNALDECK_PYTHON=sys.executable,
            SIGNALDECK_GRADE_LOG=str(self.log),
            PATH=str(self.bin) + os.pathsep + env.get("PATH", ""),
        )
        proc = subprocess.run(
            ["sh", str(GRADE_SH)],
            env=env, capture_output=True, text=True, timeout=180,
        )
        return proc

    def published(self):
        return json.loads(self.out.read_text(encoding="utf-8"))

    def still_previous(self) -> bool:
        try:
            return self.published() == PREV_REGISTRY
        except Exception:
            return False

    def cleanup(self) -> None:
        shutil.rmtree(self.dir, ignore_errors=True)


class PublicationGateTests(unittest.TestCase):
    def setUp(self) -> None:
        if not REAL_PUBGATE.exists():
            self.skipTest(f"{REAL_PUBGATE} not present")
        self.sb = Sandbox()
        self.addCleanup(self.sb.cleanup)

    def assertWithheld(self, proc, because: str) -> None:
        combined = proc.stdout + proc.stderr
        self.assertNotEqual(
            proc.returncode, 0,
            f"grade.sh exited 0 on {because}\n{combined}",
        )
        self.assertTrue(
            self.sb.still_previous(),
            f"{because}: ungated rows reached the published registry.\n"
            f"published={self.sb.out.read_text(encoding='utf-8')[:400]}",
        )

    # -- the control: this must PUBLISH ------------------------------------
    def test_happy_path_publishes(self):
        proc = self.sb.run()
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertFalse(self.sb.still_previous(), "a clean grade did not publish")
        self.assertEqual(len(self.sb.published()["rows"]), 2)

    # -- the other control: row refusals are a RESULT, not an outage --------
    def test_row_level_refusal_still_publishes(self):
        """selection_honesty exits 1 for refused ROWS. That is a finding to
        publish, and a 'fail closed' rewrite that withheld here would suppress
        the disclosure the tool exists to make."""
        self.sb.honesty_merges(
            {**HONESTY_OK, "publishable": False, "one_sided": True,
             "reason": "accuracy is the one-sided base rate"},
            exit_code=1,
        )
        proc = self.sb.run()
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertFalse(self.sb.still_previous())
        row = self.sb.published()["rows"][0]
        self.assertFalse(row["honesty"]["publishable"])

    # -- F04: a crash must not read as a row refusal ------------------------
    def test_selection_honesty_crash_is_not_a_row_refusal(self):
        self.sb.honesty_merges(None, crash=True)
        proc = self.sb.run()
        self.assertWithheld(proc, "selection_honesty crashing mid-merge")
        self.assertIn("CHECK UNAVAILABLE", self.sb.log.read_text(encoding="utf-8"))

    def test_missing_honesty_block_withholds(self):
        """The merge 'succeeded' (exit 0) but produced nothing. Only an artifact
        check can see this; the return code says everything is fine."""
        self.sb.honesty_merges(HONESTY_OK, exit_code=0)
        self.sb.write_tool("selection_honesty.py", _py("import sys; sys.exit(0)\n"))
        proc = self.sb.run()
        self.assertWithheld(proc, "a merge that wrote no honesty block")

    def test_wrong_honesty_source_withholds(self):
        self.sb.honesty_merges({**HONESTY_OK, "source": "somewhere-else"})
        proc = self.sb.run()
        self.assertWithheld(proc, "an honesty block from the wrong tool")

    def test_incomplete_honesty_keys_withhold(self):
        h = {k: v for k, v in HONESTY_OK.items() if k != "result"}
        self.sb.honesty_merges(h)
        proc = self.sb.run()
        self.assertWithheld(proc, "an honesty block missing its result envelope")

    # -- F03: every way of not knowing must withhold -----------------------
    def test_missing_collapsecheck_binary_withholds(self):
        self.sb.collapsecheck(present=False)
        proc = self.sb.run()
        self.assertWithheld(proc, "collapsecheck missing from the image")

    def test_collapsecheck_exit_2_withholds_without_alleging_collapse(self):
        self.sb.collapsecheck(exit_code=2, stdout="")
        proc = self.sb.run()
        self.assertWithheld(proc, "collapsecheck returning undetermined")
        log = self.sb.log.read_text(encoding="utf-8")
        self.assertIn("CHECK UNAVAILABLE", log)
        self.assertIn("NOT a finding about any model", log)

    def test_collapsecheck_exit_1_is_a_measured_refusal(self):
        self.sb.collapsecheck(exit_code=1, stdout="the graded window contains 3 collapsed cross-section(s)")
        proc = self.sb.run()
        self.assertWithheld(proc, "a measured collapse")
        log = self.sb.log.read_text(encoding="utf-8")
        self.assertIn("publication gate:", log)
        self.assertNotIn("CHECK UNAVAILABLE", log)

    def test_bad_flag_exits_2_and_withholds(self):
        """argparse exits 2 on an unknown flag. ops/research-liveness.sh shipped
        exactly that and the check 'had never produced a verdict'."""
        self.sb.collapsecheck(exit_code=2, stdout="unknown flag --registry")
        proc = self.sb.run()
        self.assertWithheld(proc, "collapsecheck rejecting its flags")

    # -- malformed / crashed grader ----------------------------------------
    def test_malformed_registry_json_withholds(self):
        self.sb.grader_writes(GOOD_ROWS, raw="{not json")
        proc = self.sb.run()
        self.assertWithheld(proc, "a grader that wrote malformed JSON")

    def test_grader_nonzero_withholds(self):
        self.sb.grader_writes(GOOD_ROWS, exit_code=3)
        proc = self.sb.run()
        self.assertWithheld(proc, "a grader that exited non-zero")

    def test_empty_rows_withholds(self):
        self.sb.grader_writes([])
        proc = self.sb.run()
        self.assertWithheld(proc, "a registry with no rows")

    # -- F05: the heartbeat ------------------------------------------------
    def test_failed_success_heartbeat_is_not_grade_ok(self):
        """The registry is published, but the daemon cannot see a fresh success,
        so /api/accuracy will serve REFUSED_STALE over it. That divergence must
        not be reported as 'grade OK'."""
        self.sb.heartbeat(exit_code=1)
        proc = self.sb.run()
        self.assertNotEqual(proc.returncode, 0, "a failed heartbeat reported grade OK")
        log = self.sb.log.read_text(encoding="utf-8")
        self.assertIn("GRADE INCOMPLETE", log)
        self.assertNotIn("grade OK", log)

    # -- interrupted finalization ------------------------------------------
    def test_staging_file_never_becomes_the_published_one(self):
        """A refused run must leave no staged artifact behind that a later
        reader or a careless retry could mistake for a published registry."""
        self.sb.collapsecheck(exit_code=1, stdout="collapsed")
        self.sb.run()
        self.assertTrue(self.sb.still_previous())
        leftovers = list(self.sb.data.glob("*.staging*"))
        self.assertEqual(leftovers, [], f"staged artifact left behind: {leftovers}")

    # -- protocol gate still fails closed ----------------------------------
    def test_unregistered_protocol_document_withholds(self):
        (self.sb.dir / "PREREGISTRATION.md").write_text("# tampered\n", encoding="utf-8")
        proc = self.sb.run()
        self.assertWithheld(proc, "a protocol document the chain does not pin")


if __name__ == "__main__":
    unittest.main(verbosity=2)

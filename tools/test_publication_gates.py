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
import threading
import time
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
GRADE_SH = REPO / "ops" / "grade.sh"


def _find_posix_shell() -> str | None:
    """Locate a POSIX shell that can run ops/grade.sh.

    THE DEFECT (audit F05, 2026-09-20). The launcher was a bare
    subprocess.run(["sh", GRADE_SH]). On a Windows host with no `sh` on PATH
    that raises FileNotFoundError, and the documented command --

        .venv/Scripts/python.exe -m unittest discover -s tools -p 'test_*.py'

    -- ended in 22 WinError 2 ERRORS. Those are 22 negative controls that did
    not run, reported in the same column as 22 production defects; the audit had
    to say so explicitly to stop them being counted as findings.

    So the shell is resolved explicitly, with the places it actually lives on a
    Windows box checked after PATH, and SIGNALDECK_TEST_SH left as the override.
    When nothing is found the tests SKIP with the command to fix it -- never
    silently, and never as a pass.
    """
    override = os.environ.get("SIGNALDECK_TEST_SH", "").strip()
    if override:
        return override if (Path(override).exists() or shutil.which(override)) else None
    for name in ("sh", "bash", "dash", "busybox"):
        found = shutil.which(name)
        if found:
            return found
    # Git for Windows ships a POSIX sh but does not put it on PATH; it is the
    # shell this repository's own tooling already runs under.
    for candidate in (
        r"C:\Program Files\Git\usr\bin\sh.exe",
        r"C:\Program Files\Git\bin\sh.exe",
        r"C:\Program Files (x86)\Git\usr\bin\sh.exe",
        os.path.expandvars(r"%LOCALAPPDATA%\Programs\Git\usr\bin\sh.exe"),
    ):
        if Path(candidate).exists():
            return candidate
    return None


POSIX_SH = _find_posix_shell()

NO_SH_REASON = (
    "no POSIX shell found, so ops/grade.sh cannot be driven on this host. These "
    "are the wrapper's negative controls and they have NOT run. Install Git for "
    "Windows (which ships sh.exe), or set SIGNALDECK_TEST_SH to a shell. On "
    "Linux/macOS /bin/sh is always present, and the container runs busybox sh -- "
    "these tests exercising a real POSIX shell is the point, so a Windows PATH "
    "fix here does NOT prove the container's behaviour."
)
REAL_PUBGATE = REPO / "tools" / "publication_gate.py"
REAL_BUILDMANIFEST = REPO / "tools" / "build_manifest.py"

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

# The block tools/selection_honesty.py really merges: verdict()'s three fields,
# plus the `resolvability` dict and the typed `result` record skillschema.build()
# stamps, plus calls_up and the source. It used to read
# `"result": {"schema": "skill/1"}` -- a placeholder no version of the producer
# has ever written. That was invisible while publication_gate.py only asked
# whether the KEY existed (audit F01); once the gate started checking that a
# result is the typed record it claims to be, the stand-in failed, correctly.
# A fixture that cannot pass the real gate is not testing the real pipeline.
HONESTY_OK = {
    "publishable": True,
    "one_sided": False,
    "reason": "",
    "resolvability": {"supported": True, "reason": "",
                      "overlap_lo": 0.501, "overlap_hi": 0.519},
    "result": {
        "schema_version": 1,
        "status": "SUPPORTED",
        "reason_code": "OK",
        "reason": "",
        "predictor": "directional-ensemble",
        "metric": "accuracy",
    },
    "calls_up": 0.4812797032572157,
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
        shutil.copy(REAL_BUILDMANIFEST, self.tools / "build_manifest.py")
        self.write_tool("backfill_delistings.py", _py("import sys; sys.exit(0)\n"))
        # Every file the manifest pins has to exist before it is emitted, or it
        # records them as missing and refuses -- which is the behaviour tested
        # further down, not the baseline.
        #
        # Seeded from build_manifest's OWN artifact list rather than a copy of
        # it. When the trust boundary widened (audit R03: the two modules
        # selection_honesty imports, the wrapper and the entrypoint), a
        # hardcoded list here would have made every scenario in this file error
        # at construction -- which is exactly what it did before this loop
        # replaced it. The fixture follows the boundary; it does not restate it.
        self.write_tool("live_accuracy.py", _py("import sys; sys.exit(0)\n"))
        self.seed_pinned_artifacts()
        self.grader_writes(GOOD_ROWS, honesty=None)
        self.honesty_merges(HONESTY_OK, exit_code=0)
        self.heartbeat(exit_code=0)
        self.collapsecheck(exit_code=0)
        self.freeze_manifest = False
        self.emit_manifest()

    # -- stub factories ----------------------------------------------------
    def write_tool(self, name: str, body: str) -> None:
        (self.tools / name).write_text(body, encoding="utf-8")

    def seed_pinned_artifacts(self) -> None:
        """Create a placeholder for every artifact the manifest requires that
        the scenario has not already provided.

        The REAL ops/grade.sh is not copied in -- it is executed from the
        repository by the launcher, and the manifest verifies it at the path it
        RUNS from, so a placeholder under the sandbox's usr/local/bin is what
        binds here. The point of this fixture is the wrapper's decision logic,
        not the wrapper's own provenance.
        """
        import importlib.util

        spec = importlib.util.spec_from_file_location(
            "_sandbox_build_manifest", self.tools / "build_manifest.py")
        bm = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(bm)
        for rel in bm.SOURCE_ARTIFACTS:
            # `emit` runs on the build host and reads the REPOSITORY layout;
            # `verify` runs in the image and reads the IMAGE layout, where the
            # wrapper and entrypoint sit under usr/local/bin. Both must exist
            # and must match, or emit records the artifact as missing and
            # refuses -- which is a real behaviour, tested elsewhere, not the
            # baseline every other scenario needs.
            body = f"sandbox placeholder for {rel}\n"
            for p in {self.dir / rel, bm.artifact_path(rel, self.dir, self.dir)}:
                if p.exists():
                    continue
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text(body, encoding="utf-8")

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

    def emit_manifest(self) -> None:
        """Emit a manifest over the sandbox, as ops/docker-build.sh does on the host.

        Re-emitted on every run() by default. The stub factories above are how a
        scenario is CONFIGURED -- rewriting selection_honesty.py to make it crash
        is setting up the test, not tampering with a shipped image -- and a
        manifest frozen at construction would flag every one of them as a swapped
        artifact, which is a true statement about the wrong thing. Deliberate
        tampering sets freeze_manifest and keeps the stale manifest.
        """
        self.manifest = self.dir / "build-manifest.json"
        subprocess.run(
            [sys.executable, str(self.tools / "build_manifest.py"),
             "emit", "--repo", str(self.dir), "--out", str(self.manifest)],
            capture_output=True, text=True, check=True, timeout=60,
        )
        # AND THEN SEAL. The real pipeline does not stop at emit: the host
        # emits, the Dockerfile runs `seal` inside the image, and since
        # 2026-09-16 verify refuses an unsealed manifest outright - it pins no
        # binary, so a swapped signaldeckd would pass unnoticed. A fixture that
        # stopped at emit modelled half a build, and every scenario below then
        # reported CHECK UNAVAILABLE instead of the behaviour under test.
        binroot = self.dir / "usr" / "local" / "bin"
        binroot.mkdir(parents=True, exist_ok=True)
        for name in ("signaldeckd", "collapsecheck"):
            stub = binroot / name
            if not stub.exists():
                stub.write_text(f"ELF-ish {name}\n", encoding="utf-8")
        subprocess.run(
            [sys.executable, str(self.tools / "build_manifest.py"),
             "seal", "--manifest", str(self.manifest), "--root", str(self.dir)],
            capture_output=True, text=True, check=True, timeout=60,
        )
        # This sandbox is not a git checkout, so emit recorded no revision and
        # no resolvability. A real build host records both; say so here, or the
        # provenance check refuses every scenario for a reason that has nothing
        # to do with the gate under test. forge_revision() overrides this.
        m = json.loads(self.manifest.read_text(encoding="utf-8"))
        m.update(revision="a" * 40, revision_resolvable=True, dirty=False)
        self.manifest.write_text(json.dumps(m), encoding="utf-8")

    def tamper(self, rel: str, text: str = "SWAPPED AFTER THE BUILD\n") -> None:
        """Change a pinned file and KEEP the manifest that predates it."""
        (self.dir / rel).write_text(text, encoding="utf-8")
        self.freeze_manifest = True

    def forge_revision(self, rev: str = "0" * 40) -> None:
        self.freeze_manifest = True
        m = json.loads(self.manifest.read_text(encoding="utf-8"))
        m["revision"] = rev
        self.manifest.write_text(json.dumps(m), encoding="utf-8")

    # -- driving grade.sh --------------------------------------------------
    def run(self):
        # The manifest describes the artifacts as they are about to be graded,
        # which is exactly what ops/docker-build.sh produces on the host right
        # before a build. Frozen only where a test is deliberately simulating a
        # deployment whose bytes drifted away from its manifest.
        if not self.freeze_manifest and self.manifest.exists():
            self.emit_manifest()
        env = dict(os.environ)
        env.update(
            SIGNALDECK_DB=str(self.db),
            SIGNALDECK_REGISTRY=str(self.out),
            SIGNALDECK_TOOLS=str(self.tools),
            SIGNALDECK_APP=str(self.dir),
            SIGNALDECK_PYTHON=sys.executable,
            SIGNALDECK_GRADE_LOG=str(self.log),
            SIGNALDECK_BUILD_MANIFEST=str(self.manifest),
            # The sandbox pins its stub binaries under its own tree, so
            # verify must resolve them there rather than at the real /.
            SIGNALDECK_BIN_ROOT=str(self.dir),
            PATH=str(self.bin) + os.pathsep + env.get("PATH", ""),
        )
        if not POSIX_SH:
            raise unittest.SkipTest(NO_SH_REASON)
        proc = subprocess.run(
            [POSIX_SH, str(GRADE_SH)],
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
        # SKIP, LOUDLY, never a silent pass: a missing shell means these
        # negative controls did not run, and the reason says how to fix it.
        if not POSIX_SH:
            self.skipTest(NO_SH_REASON)
        if not REAL_PUBGATE.exists():
            self.skipTest(f"{REAL_PUBGATE} not present")
        self.sb = Sandbox()
        self.addCleanup(self.sb.cleanup)

    def assertWithheld(self, proc, because: str) -> None:
        """grade.sh refused, and no ungated row reached the served file.

        This used to be `still_previous()` -- the published file must be byte
        identical to what was there before. That was the right INVARIANT
        expressed as the wrong CHECK, and it stopped being true when the
        wrapper started writing a refusal envelope (audit F10): leaving $OUT
        untouched meant a seeded volume went on serving the last rows with
        nothing saying the newest grade was withheld, and a fresh volume was
        left with no file at all, which reads as an outage rather than a
        refusal. The dev-box path has written that envelope since 2026-08-04.

        So the invariant is asserted directly: whatever is on disk, it carries
        NO ROWS from the refused grade. Either shape satisfies it -- the
        previous registry untouched, or a REFUSED envelope with an empty rows
        list and the last successful grade kept nested.
        """
        combined = proc.stdout + proc.stderr
        self.assertNotEqual(
            proc.returncode, 0,
            f"grade.sh exited 0 on {because}\n{combined}",
        )
        if self.sb.still_previous():
            return
        try:
            published = json.loads(self.sb.out.read_text(encoding="utf-8"))
        except (OSError, ValueError) as e:
            self.fail(f"{because}: the published registry is unreadable ({e})")
        self.assertEqual(
            published.get("status"), "REFUSED",
            f"{because}: the published file is neither the previous registry nor a "
            f"refusal envelope.\npublished={str(published)[:400]}",
        )
        self.assertEqual(
            published.get("rows"), [],
            f"{because}: ungated rows reached the published registry.\n"
            f"published={str(published)[:400]}",
        )
        self.assertEqual(
            published.get("stale_last_registry"), PREV_REGISTRY,
            f"{because}: the last successful grade was lost rather than kept nested.",
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

    # -- F01 (2026-09-20): a SKELETON block is not a merged block ------------
    def test_null_valued_honesty_block_withholds(self):
        """The half-written shape the audit walked through the old gate: every
        required key present, every one of them null. That is what a merge that
        allocated the block and then died leaves behind, and it used to publish
        exactly like a completed grade."""
        self.sb.honesty_merges(
            {"source": "tools/selection_honesty.py", "publishable": None,
             "one_sided": None, "reason": None, "result": None},
        )
        proc = self.sb.run()
        self.assertWithheld(proc, "an honesty block whose every value is null")

    def test_refusal_with_no_reason_withholds(self):
        """A refusal IS the disclosure. publishable=false with nothing saying
        what was refused is a truncated write, not a finding."""
        self.sb.honesty_merges({**HONESTY_OK, "publishable": False, "reason": ""},
                               exit_code=1)
        proc = self.sb.run()
        self.assertWithheld(proc, "a refusal that names no reason")

    def test_non_finite_agreement_withholds(self):
        """json.load takes the bare NaN literal, and 1e400 becomes inf with no
        literal at all. Neither is a rate, so neither may be published."""
        for literal, what in (("NaN", "the bare NaN literal"),
                              ("1e400", "an exponent that overflows to inf")):
            with self.subTest(literal=literal):
                sb = Sandbox()
                self.addCleanup(sb.cleanup)
                sb.grader_writes(GOOD_ROWS, raw=(
                    '{"generated":"2026-09-13T12:00:00",'
                    '"graded_at":"2026-09-13T12:00:00","rows":['
                    '{"predictor":"directional-ensemble (1d)","family":"direction",'
                    '"live_acc":0.51,"null_acc":0.50,'
                    '"breadth":{"mean_daily_agreement":' + literal + "}}]}"))
                sb.honesty_merges(HONESTY_OK)
                self.assertWithheld(sb.run(), f"a registry carrying {what}")

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
        proc = self.sb.run()
        # The staged artifact is the subject here; the published file is
        # asserted through the shared invariant, which since the refusal
        # envelope landed (audit F10) is "no rejected row reached it" rather
        # than "the bytes did not change".
        self.assertWithheld(proc, "a refused collapse gate")
        leftovers = list(self.sb.data.glob("*.staging*"))
        self.assertEqual(leftovers, [], f"staged artifact left behind: {leftovers}")

    # -- F06: the container was applying one gate fewer than the dev box ----
    def test_swapped_grader_is_caught_by_the_build_manifest(self):
        """The gap this closes. tools/deployment_drift.py cannot run in the
        image (it shells out to git; .dockerignore excludes .git), so a stale or
        substituted binary the dev-box publish refuses on was still graded here.
        Content hashing crosses that boundary where git cannot."""
        self.sb.tamper("tools/accuracy_registry.py")
        proc = self.sb.run()
        self.assertWithheld(proc, "a grader whose bytes are not the reviewed ones")
        log = self.sb.log.read_text(encoding="utf-8")
        self.assertIn("BUILD MANIFEST MISMATCH", log)
        self.assertIn("accuracy_registry.py", log)

    def test_swapped_protocol_document_is_caught(self):
        self.sb.tamper("PREREGISTRATION.md")
        proc = self.sb.run()
        self.assertWithheld(proc, "a protocol document that is not the reviewed one")

    def test_missing_manifest_is_an_outage_not_an_accusation(self):
        """A missing manifest means the check could not RUN. It must withhold,
        and it must NOT be reported as a mismatch -- publishing an outage as an
        accusation about the deployment is the same error as the reverse."""
        self.sb.manifest.unlink()
        proc = self.sb.run()
        self.assertWithheld(proc, "a missing build manifest")
        log = self.sb.log.read_text(encoding="utf-8")
        self.assertIn("CHECK UNAVAILABLE", log)
        self.assertNotIn("BUILD MANIFEST MISMATCH", log)

    def test_manifest_that_pins_nothing_does_not_verify_everything(self):
        """An emptied manifest would otherwise pass by checking zero artifacts --
        the exact fail-open shape this whole gate exists to close."""
        self.sb.freeze_manifest = True
        self.sb.manifest.write_text(json.dumps({
            "schema": "signaldeck/build-manifest/1",
            "revision": "deadbeef", "source_artifacts": {}, "binary_artifacts": {},
        }), encoding="utf-8")
        proc = self.sb.run()
        self.assertWithheld(proc, "a manifest pinning no artifacts")
        self.assertIn("CHECK UNAVAILABLE", self.sb.log.read_text(encoding="utf-8"))

    def test_a_forged_revision_label_neither_rescues_nor_breaks_anything(self):
        """GIT_REV is caller-supplied and TRUSTED, which is why the binding is to
        CONTENT. Relabelling an honest image must still publish; relabelling a
        tampered one must still be caught."""
        self.sb.forge_revision()
        proc = self.sb.run()
        self.assertEqual(proc.returncode, 0,
                         "a relabelled but honest image was refused\n" + proc.stdout + proc.stderr)

        sb2 = Sandbox()
        self.addCleanup(sb2.cleanup)
        sb2.forge_revision()
        sb2.tamper("tools/selection_honesty.py")
        proc2 = sb2.run()
        self.assertNotEqual(proc2.returncode, 0, "a forged label rescued swapped bytes")
        # Same invariant as assertWithheld, on the other sandbox: either the
        # previous registry is untouched or a REFUSED envelope carrying no rows
        # replaced it. Never the swapped grade's figures.
        if not sb2.still_previous():
            published = json.loads(sb2.out.read_text(encoding="utf-8"))
            self.assertEqual(published.get("status"), "REFUSED", published)
            self.assertEqual(published.get("rows"), [], published)

    def test_manifest_verify_never_claims_the_revision_was_verified(self):
        """The manifest REPORTS the revision and must never claim to have
        verified it: it repeats what the build host wrote into it, and a label
        the caller supplied is not evidence. Inventing resolvability is the
        failure mode this replaces.

        The wording changed on 2026-09-16 and the reason is worth keeping. This
        used to read "nothing in the image can resolve a commit", which was true
        until the image began shipping this repository's commit objects so the
        hash-pinned grader could attribute historical rows (verified in a real
        container: a historical commit resolves, an invented sha does not). The
        premise expired; the claim it protects did not. What the manifest binds
        is BYTES - the commit store answers a different question.
        """
        proc = self.sb.run()
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        log = self.sb.log.read_text(encoding="utf-8")
        self.assertIn("RECORDED (this manifest reports it, it does not verify it)", log)
        self.assertNotIn("revision verified", log)

    # -- protocol gate still fails closed ----------------------------------
    def test_unregistered_protocol_document_withholds(self):
        (self.sb.dir / "PREREGISTRATION.md").write_text("# tampered\n", encoding="utf-8")
        proc = self.sb.run()
        self.assertWithheld(proc, "a protocol document the chain does not pin")


# ──────────────────────────────────────────────────────────────────────────────
# R02 (2026-09-20): one writer at a time, and the bytes that pass are the bytes
# that publish.
#
# ${OUT}.staging was a single fixed name, removed at startup and again at
# cleanup, with no lock. Two overlapping runs -- the image's schedule and an
# operator by hand -- could write it concurrently, delete each other's staged
# artifact mid-check, or publish bytes the other run's gates had inspected.
#
# Reproduced with two REAL invocations sharing one $OUT and one $DB, made to
# overlap by a slow grader stub, never against the live grader.
# ──────────────────────────────────────────────────────────────────────────────
class StagingLockTests(unittest.TestCase):
    def setUp(self) -> None:
        if not POSIX_SH:
            self.skipTest(NO_SH_REASON)
        if not REAL_PUBGATE.exists():
            self.skipTest(f"{REAL_PUBGATE} not present")
        self.sb = Sandbox()
        self.addCleanup(self.sb.cleanup)

    def lockdir(self) -> Path:
        return Path(str(self.sb.out) + ".lock")

    def test_a_second_grader_declines_while_one_holds_the_lock(self):
        """A live lock means another grader is mid-cycle. Declining is neither
        an outage nor a refusal: it writes no heartbeat and no envelope,
        because the run that holds the lock is about to write both."""
        lock = self.lockdir()
        lock.mkdir()
        # This test process is alive, so the lock is demonstrably not stale.
        (lock / "pid").write_text(str(os.getpid()), encoding="utf-8")
        (lock / "started").write_text(str(int(time.time())), encoding="utf-8")

        proc = self.sb.run()
        combined = proc.stdout + proc.stderr
        self.assertEqual(proc.returncode, 0, combined)
        self.assertIn("grade SKIPPED", combined)
        self.assertTrue(self.sb.still_previous(),
                        "a declining run must not touch the published registry")
        self.assertTrue(lock.exists(), "a declining run must not break a live lock")

    def test_a_stale_lock_from_a_killed_container_is_broken(self):
        lock = self.lockdir()
        lock.mkdir()
        # A pid that cannot be running, and a start time far outside the grace.
        (lock / "pid").write_text("999999", encoding="utf-8")
        (lock / "started").write_text(str(int(time.time()) - 86400), encoding="utf-8")

        proc = self.sb.run()
        combined = proc.stdout + proc.stderr
        self.assertIn("breaking a stale grade lock", combined)
        self.assertEqual(proc.returncode, 0, combined)
        self.assertFalse(self.sb.still_previous(), "the grade should have published")

    def test_a_held_lock_is_not_broken_merely_for_being_old(self):
        """A slow grade on a large database is not a stale lock. Both
        conditions -- owner gone AND past the grace period -- or neither.

        The holder has to be a process grade.sh's own `kill -0` can SEE. In the
        container that is automatic: the lock holder and this script share a PID
        namespace. On a Windows host driving Git's sh.exe they do not -- MSYS
        reports this Python interpreter's Win32 pid as gone -- so the holder is
        spawned through the same shell rather than faked with os.getpid(), which
        would test the host's process table and not the lock.
        """
        holder = subprocess.Popen([POSIX_SH, "-c", "sleep 60"])
        self.addCleanup(holder.kill)
        # The pid grade.sh will probe is the one the SHELL knows about.
        shell_pid = subprocess.run(
            [POSIX_SH, "-c", "echo $PPID"], capture_output=True, text=True,
        ).stdout.strip()
        lock = self.lockdir()
        lock.mkdir()
        (lock / "pid").write_text(str(holder.pid), encoding="utf-8")
        (lock / "started").write_text(str(int(time.time()) - 86400), encoding="utf-8")

        proc = self.sb.run()
        combined = proc.stdout + proc.stderr
        if "breaking a stale grade lock" in combined:
            self.skipTest(
                "this host's sh cannot see the holder process (pid "
                f"{holder.pid}; shell sees ppid {shell_pid}), so liveness cannot "
                "be exercised here. The container shares one PID namespace, where "
                "kill -0 is exactly the right probe; "
                "test_a_stale_lock_from_a_killed_container_is_broken covers the "
                "other half and does run."
            )
        self.assertIn("grade SKIPPED", combined)
        self.assertTrue(self.sb.still_previous())

    def test_a_lock_with_no_pid_file_is_still_breakable(self):
        """The wedge: a process killed between mkdir and writing its pid leaves
        a lock nobody owns. If that cannot be broken, every later run declines
        forever and the grader looks dead -- the exact failure this file exists
        to end."""
        lock = self.lockdir()
        lock.mkdir()
        (lock / "started").write_text(str(int(time.time()) - 86400), encoding="utf-8")
        # no pid file at all
        proc = self.sb.run()
        combined = proc.stdout + proc.stderr
        self.assertIn("breaking a stale grade lock", combined)
        self.assertEqual(proc.returncode, 0, combined)

    def test_a_lock_with_neither_pid_nor_start_time_is_breakable(self):
        lock = self.lockdir()
        lock.mkdir()
        proc = self.sb.run()
        self.assertIn("breaking a stale grade lock", proc.stdout + proc.stderr)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)

    def test_a_fresh_pidless_lock_is_left_alone(self):
        """...but only once it is past the grace period. A lock created a
        second ago is a run that has not finished writing its pid yet."""
        lock = self.lockdir()
        lock.mkdir()
        (lock / "started").write_text(str(int(time.time())), encoding="utf-8")
        proc = self.sb.run()
        self.assertIn("grade SKIPPED", proc.stdout + proc.stderr)
        self.assertTrue(self.sb.still_previous())

    def test_the_lock_is_released_on_a_refusal(self):
        """A refused cycle must not leave the lock behind, or the next run
        declines forever and the grader looks dead."""
        self.sb.collapsecheck(exit_code=1, stdout="2 collapsed cross-section(s) of 9 day(s):")
        proc = self.sb.run()
        self.assertNotEqual(proc.returncode, 0)
        self.assertFalse(self.lockdir().exists(), "the lock outlived a refused cycle")

    def test_the_lock_is_released_on_success(self):
        proc = self.sb.run()
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertFalse(self.lockdir().exists(), "the lock outlived a clean grade")

    def test_cleanup_removes_only_this_runs_staging_file(self):
        """The old code's startup `rm -f ${OUT}.staging` deleted whatever
        another run had staged. A foreign staging file must survive."""
        foreign = Path(str(self.sb.out) + ".staging.999999")
        foreign.write_text('{"not":"mine"}', encoding="utf-8")
        proc = self.sb.run()
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertTrue(foreign.exists(),
                        "cleanup deleted another run's staged registry")
        self.assertEqual(foreign.read_text(encoding="utf-8"), '{"not":"mine"}')

    def test_two_overlapping_graders_publish_one_whole_grade(self):
        """THE RACE, driven. Two real invocations share $OUT and $DB; each
        grader stub sleeps so their windows overlap, and each writes a
        distinguishable payload. Exactly one may publish, and what lands must
        be one grade entire -- never a mix, and never the other run's bytes
        under this run's approval."""
        other = Sandbox()
        self.addCleanup(other.cleanup)
        # Same published registry, same database: one deployment, two runs.
        other.out = self.sb.out
        other.db = self.sb.db
        other.log = self.sb.log

        def payload(marker):
            rows = json.loads(json.dumps(GOOD_ROWS))
            rows[0]["predictor"] = f"directional-ensemble (1d) [{marker}]"
            return json.dumps({"generated": f"2026-09-13T12:00:0{marker}",
                               "graded_at": f"2026-09-13T12:00:0{marker}",
                               "marker": marker, "rows": rows})

        for sb, marker in ((self.sb, "1"), (other, "2")):
            # A slow grader widens the window the old code raced in.
            body = "\n".join([
                "",
                "import sys, time",
                "time.sleep(1.5)",
                'path = sys.argv[sys.argv.index("--json") + 1]',
                "open(path, 'w', encoding='utf-8').write(%r)" % payload(marker),
                "",
            ])
            sb.write_tool("accuracy_registry.py", _py(body))
            sb.honesty_merges(HONESTY_OK)

        results = {}

        def go(name, sb):
            results[name] = sb.run()

        threads = [threading.Thread(target=go, args=(n, s))
                   for n, s in (("a", self.sb), ("b", other))]
        for t in threads:
            t.start()
        for t in threads:
            t.join(timeout=240)

        self.assertEqual(len(results), 2, "one of the invocations never returned")
        combined = "".join(p.stdout + p.stderr for p in results.values())
        skipped = sum("grade SKIPPED" in (p.stdout + p.stderr) for p in results.values())
        self.assertEqual(skipped, 1,
                         "exactly one run must decline the lock:\n" + combined)

        published = json.loads(self.sb.out.read_text(encoding="utf-8"))
        # Whole, and one grade's worth: the marker, the timestamp and the row
        # label must all come from the SAME run.
        self.assertIn("marker", published, "the previous registry was published instead")
        m = published["marker"]
        self.assertIn(m, ("1", "2"))
        self.assertTrue(published["generated"].endswith(m))
        self.assertIn(f"[{m}]", published["rows"][0]["predictor"])
        self.assertEqual(len(published["rows"]), len(GOOD_ROWS))
        # And no staging debris from either run.
        leftovers = sorted(p.name for p in self.sb.data.glob("*.staging*"))
        self.assertEqual(leftovers, [], f"staging files left behind: {leftovers}")


# ──────────────────────────────────────────────────────────────────────────────
# F10 (2026-09-20): a measured refusal is not an outage.
#
# Every refusal path in grade.sh funnelled through grader_heartbeat.py
# --failure, including collapsecheck exit 1 -- which means the grader RAN,
# measured the window and declined on the evidence. The native helper has
# carried --refused for exactly that since it was written, and the dev-box
# script has always used it, so the same result was a FINDING on one box and an
# OUTAGE on the other.
#
# The heartbeat stub echoes its argv, so the mode reaches the log.
# ──────────────────────────────────────────────────────────────────────────────
class RefusalClassificationTests(unittest.TestCase):
    def setUp(self) -> None:
        if not POSIX_SH:
            self.skipTest(NO_SH_REASON)
        if not REAL_PUBGATE.exists():
            self.skipTest(f"{REAL_PUBGATE} not present")
        self.sb = Sandbox()
        self.addCleanup(self.sb.cleanup)

    def modeOf(self, proc) -> str:
        combined = proc.stdout + proc.stderr
        self.assertNotEqual(proc.returncode, 0, "this scenario must refuse:\n" + combined)
        if "--refused" in combined:
            return "--refused"
        if "--failure" in combined:
            return "--failure"
        self.fail("no heartbeat mode reached the log:\n" + combined)

    def envelope(self) -> dict:
        return json.loads(self.sb.out.read_text(encoding="utf-8"))

    # -- the measured refusal ----------------------------------------------
    def test_collapse_refusal_records_a_healthy_grader(self):
        self.sb.collapsecheck(
            exit_code=1,
            stdout="the graded window contains 18 collapsed cross-section(s) of 82 day(s):")
        proc = self.sb.run()
        self.assertEqual(self.modeOf(proc), "--refused",
                         "a measured collapse was recorded as a grader outage")
        env = self.envelope()
        self.assertEqual(env["status"], "REFUSED")
        self.assertIn("collapsed cross-section", env["refusal_reason"])
        self.assertEqual(env["rows"], [], "a refusal must publish no rows")

    # -- the outages, which must NOT be dressed as findings -----------------
    def test_collapse_check_outage_records_a_failure(self):
        self.sb.collapsecheck(exit_code=2)
        self.assertEqual(self.modeOf(self.sb.run()), "--failure")

    def test_missing_collapsecheck_records_a_failure(self):
        self.sb.collapsecheck(present=False)
        self.assertEqual(self.modeOf(self.sb.run()), "--failure")

    def test_grader_crash_records_a_failure(self):
        self.sb.grader_writes(GOOD_ROWS, exit_code=3)
        self.assertEqual(self.modeOf(self.sb.run()), "--failure")

    def test_incomplete_post_processing_records_a_failure(self):
        """publication_gate INCOMPLETE is a post-processing failure, not a
        finding about a model -- it is a CHECK UNAVAILABLE, and classifies
        with the outages."""
        self.sb.honesty_merges(None, crash=True)
        self.assertEqual(self.modeOf(self.sb.run()), "--failure")

    # -- the envelope, on both shapes of volume -----------------------------
    def test_refusal_envelope_on_a_seeded_volume_keeps_the_last_grade_nested(self):
        self.sb.collapsecheck(exit_code=1, stdout="3 collapsed cross-section(s) of 9 day(s):")
        self.sb.run()
        env = self.envelope()
        self.assertEqual(env["rows"], [])
        self.assertEqual(env["stale_last_registry"], PREV_REGISTRY,
                         "the last successful grade must be kept, nested, never lost")
        # and never at the top level, where it would read as a fresh grade
        self.assertNotEqual(env.get("generated"), PREV_REGISTRY["generated"])

    def test_refusal_envelope_on_a_fresh_volume(self):
        """No registry has ever been published here. The old code left the
        path absent, which /api/accuracy reads as 'registry unavailable' -- an
        outage -- rather than as the refusal it is."""
        self.sb.out.unlink()
        self.sb.collapsecheck(exit_code=1, stdout="3 collapsed cross-section(s) of 9 day(s):")
        self.sb.run()
        self.assertTrue(self.sb.out.exists(),
                        "a fresh volume was left with no registry at all")
        env = self.envelope()
        self.assertEqual(env["status"], "REFUSED")
        self.assertEqual(env["rows"], [])
        self.assertIsNone(env["stale_last_registry"])
        self.assertTrue(env["refused_since"])

    def test_refused_since_survives_a_retry(self):
        """A retry into an already-refused registry must not reset the
        outage's age to zero."""
        self.sb.collapsecheck(exit_code=1, stdout="3 collapsed cross-section(s) of 9 day(s):")
        self.sb.run()
        first = self.envelope()
        time.sleep(1.1)
        self.sb.run()
        second = self.envelope()
        self.assertEqual(second["refused_since"], first["refused_since"])
        self.assertNotEqual(second["generated"], first["generated"])
        # and refusal must not nest inside refusal
        self.assertEqual(second["stale_last_registry"], PREV_REGISTRY)

    def test_a_refusal_never_publishes_the_rejected_figures(self):
        self.sb.collapsecheck(exit_code=1, stdout="3 collapsed cross-section(s) of 9 day(s):")
        self.sb.run()
        text = self.sb.out.read_text(encoding="utf-8")
        self.assertNotIn("directional-ensemble (1d)", text,
                         "the rejected rows reached the published registry")
        self.assertNotIn("0.51", text, "a rejected accuracy reached the published registry")


if __name__ == "__main__":
    unittest.main(verbosity=2)

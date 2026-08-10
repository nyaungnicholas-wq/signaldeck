#!/usr/bin/env python3
"""BLOCKED-4 dry run: prove the docs-gate fix closes the gate WITHOUT touching the repo.

TARGETS: nothing. This file is a proof harness, not a patch. It is the evidence
behind drafts/pending-approval/docs-gate/RUNBOOK.md and it writes only into a
throwaway mirror directory.

HOW TO RUN (safe to run before approval — the repo is read-only to this script):

    cd "C:/Users/Nicholas_N/Desktop/claude code/signaldeck"
    python drafts/pending-approval/docs-gate/dryrun_mirror.py
    # optional, avoids the live DB entirely:
    python drafts/pending-approval/docs-gate/dryrun_mirror.py \
        --db data/backups/signaldeck-20260806-131007.db

What it does:
  1. copies STRATEGY_DECK.md + partials/live_accuracy.md into a temp mirror;
  2. writes a mirror ops/docs-registry.json with controls_evidence and
     deck_facts added to `external_partials`;
  3. runs each generator ONCE with `--write <mirror partial> --inject <mirror doc>`
     — the same single invocation the runbook prescribes;
  4. calls tools/docs_gate.py's own check function against the mirror and asserts
     it returns ZERO violations;
  5. asserts the two half-measures still FAIL, so a green result cannot be an
     artefact of the harness:
       - registry declared, partial files absent  -> 2 violations
       - partial files present, registry unchanged -> 2 violations
     (5b is the shape of the repair recorded as reverted in
      audits/completion-2026-08-06/completion_state.json
      "repairs_attempted_and_reverted"[0].)

Read-only against the database: deck_facts.py opens it `file:...?mode=ro`.
"""
from __future__ import annotations

import argparse
import json
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

NAMES = ("controls_evidence", "deck_facts")


def build_mirror(repo: Path, mirror: Path, db: str | None) -> None:
    (mirror / "partials").mkdir(parents=True, exist_ok=True)
    (mirror / "ops").mkdir(parents=True, exist_ok=True)
    shutil.copy(repo / "STRATEGY_DECK.md", mirror / "STRATEGY_DECK.md")
    shutil.copy(repo / "partials" / "live_accuracy.md",
                mirror / "partials" / "live_accuracy.md")
    write_registry(repo, mirror, declared=True)

    doc = str(mirror / "STRATEGY_DECK.md")
    run([sys.executable, str(repo / "tools" / "controls_evidence.py"),
         "--repo", str(repo),
         "--write", str(mirror / "partials" / "controls_evidence.md"),
         "--inject", doc], repo)
    cmd = [sys.executable, str(repo / "tools" / "deck_facts.py")]
    if db:
        cmd += ["--db", db]
    run(cmd + ["--write", str(mirror / "partials" / "deck_facts.md"),
               "--inject", doc], repo)


def write_registry(repo: Path, mirror: Path, declared: bool) -> dict:
    reg = json.loads((repo / "ops" / "docs-registry.json").read_text(encoding="utf-8"))
    ext = ["live_accuracy", "live_accuracy.md"]
    if declared:
        for n in NAMES:
            ext += [n, f"{n}.md"]
    reg["external_partials"] = ext
    # STRATEGY_DECK.md is the only document the mirror holds; the check skips
    # registered documents that are not present, but keeping the map honest
    # means a miscopy shows up as a missing file rather than a silent pass.
    reg["strategy_docs"] = {"STRATEGY_DECK.md": reg["strategy_docs"]["STRATEGY_DECK.md"]}
    (mirror / "ops" / "docs-registry.json").write_text(
        json.dumps(reg, indent=2), encoding="utf-8", newline="\n")
    return reg


def run(cmd: list[str], cwd: Path) -> None:
    p = subprocess.run(cmd, cwd=str(cwd), capture_output=True, text=True)
    if p.returncode != 0:
        raise SystemExit("FAILED %s\n%s%s" % (" ".join(cmd), p.stdout, p.stderr))


def violations(repo: Path, target: Path, registry: dict) -> list[dict]:
    sys.path.insert(0, str(repo / "tools"))
    import docs_gate  # noqa: E402 — imported from the repo under test, on purpose
    return docs_gate.check_no_hardcoded_live_accuracy(target, registry)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo", default=".", help="signaldeck root (default: cwd)")
    ap.add_argument("--db", default=None,
                    help="database for deck_facts.py (default: its own default, "
                         "opened read-only). Point at data/backups/*.db to leave "
                         "the live file alone.")
    args = ap.parse_args()
    repo = Path(args.repo).resolve()

    tmp = Path(tempfile.mkdtemp(prefix="docsgate-dryrun-"))
    try:
        mirror = tmp / "mirror"
        build_mirror(repo, mirror, args.db)
        reg_yes = write_registry(repo, mirror, declared=True)

        fixed = violations(repo, mirror, reg_yes)
        assert not fixed, "the full fix still leaves violations: %s" % [
            v["message"] for v in fixed]

        # 5a: declared, but nobody wrote the partial files.
        bare = tmp / "declared-only"
        (bare / "partials").mkdir(parents=True)
        (bare / "ops").mkdir(parents=True)
        shutil.copy(mirror / "STRATEGY_DECK.md", bare / "STRATEGY_DECK.md")
        shutil.copy(mirror / "partials" / "live_accuracy.md",
                    bare / "partials" / "live_accuracy.md")
        half_a = violations(repo, bare, reg_yes)
        assert len(half_a) == 2, half_a

        # 5b: files written and document injected, but the registry never declared
        # the names — the reverted repair, inverted.
        reg_no = write_registry(repo, mirror, declared=False)
        half_b = violations(repo, mirror, reg_no)
        assert len(half_b) == 2, half_b

        print("PASS  full fix -> 0 violations; "
              "registry-only -> %d; partials-only -> %d" % (len(half_a), len(half_b)))
        print("mirror deck diff vs repo (this is what --inject would write):")
        subprocess.run(["git", "diff", "--no-index", "--stat", "--",
                        str(repo / "STRATEGY_DECK.md"),
                        str(mirror / "STRATEGY_DECK.md")], cwd=str(repo))
        return 0
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Bind a container image to the source that was reviewed, without git.

THE GAP THIS CLOSES

ops/grade.sh's own header records it: ops/accuracy-registry.sh runs
tools/deployment_drift.py before the grader and REFUSES publication on any
non-zero exit, but two of that tool's checks shell out to git against the
deployed revision, and there is no .git in the image (.dockerignore excludes
it). So "the container applies one gate fewer than the dev box: a stale binary
the dev-box publish refuses on is still graded here."

The obvious patch -- record the git revision in the image -- is worth nothing on
its own. ops/docker-build.sh passes `--build-arg GIT_REV="$rev"` and says so in
as many words: "That path is TRUSTED, not checked." A label the caller supplies
and the image repeats back proves only that someone typed it.

WHAT CAN ACTUALLY BE VERIFIED WITHOUT GIT: content. The files that decide a
verdict -- the pinned grader, the protocol document, the post-processors -- are
copied into the image byte-for-byte from the build context. Hashing them on the
build HOST, where git exists and the tree can be compared to a revision, and
re-hashing them INSIDE the container at grade time, binds the running artifact
to the reviewed source through something the caller cannot forge by editing a
build argument. Forge GIT_REV and ship different tools/ and the hashes disagree.

WHAT THIS DELIBERATELY DOES NOT CLAIM. Inside the container the revision is
RECORDED, never VERIFIED -- nothing in the image can resolve a commit. `verify`
says so on every run. Reporting "revision abc123 verified" from a label would be
the same dishonesty as the fail-open gates this replaces, and the instruction
for this work names it exactly: verify the artifact binding without inventing
resolvability.

WHAT IT NOW REFUSES, added 2026-09-16. `verify` used to check whatever keys the
manifest carried and reject only a completely empty artifact map, so a manifest
pinning one unrelated file, none of the required sources, no binaries, sealed
false, dirty true and unresolvable returned ok with checked=1. It now requires
the FULL expected source and binary sets, a manifest that was actually sealed
inside the image, and a build whose provenance is neither dirty nor unresolvable
-- the last waived only by SIGNALDECK_ALLOW_DIRTY_BUILD, the override the daemon
already uses for the same question, and then said aloud in the reason.

TWO SEALING POINTS

  emit   on the build host, before `docker build`. Hashes the source files and
         records the revision, its dirty flag, and whether git could resolve it.
  seal   inside the image, after the binaries exist. Adds their hashes, which
         the host could not know because they are compiled during the build.

  verify at grade time, in the container. Re-hashes everything and compares.

Usage:
    python3 tools/build_manifest.py emit   --repo . --out build-manifest.json
    python3 tools/build_manifest.py seal   --manifest /app/build-manifest.json --root /
    python3 tools/build_manifest.py verify --manifest /app/build-manifest.json --root /app
    python3 tools/build_manifest.py --selfcheck
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

SCHEMA = "signaldeck/build-manifest/1"

# The files whose BYTES decide a verdict. Copied verbatim by the Dockerfile, so
# a hash taken on the build host is exactly what the image will hold.
#
# tools/accuracy_registry.py is first for a reason: its sha256 is pinned in the
# pre-registration chain and it refuses to run when its own bytes change. This
# manifest is a second, independent place that notices -- the grader's guard
# protects the grader, and this protects everything around it.
# THE TRUST BOUNDARY, stated because it was previously only implied (audit R03).
#
# COVERED: every file whose bytes can change what gets published, or whether
# anything gets published at all. That is wider than "the grader": it includes
# the two modules selection_honesty IMPORTS -- skillpower decides `supported`
# and skillschema builds the typed `result` record, so swapping either rewrites
# a verdict without touching a file this manifest used to pin -- and the wrapper
# and entrypoint, which decide whether the gates run and what happens when one
# refuses. ops/grade.sh alone can publish a registry no gate approved.
#
# NOT COVERED, and this is not a gap to be closed by adding hashes: an operator
# who can replace the image can replace the manifest and this verifier along
# with it, and a self-contained check cannot survive that. Only an attestation
# signed outside the build -- an image digest a registry vouches for -- does.
# The value here is against DRIFT and against a swap INSIDE a running container,
# which is what it has always caught and all it should ever claim.
#
# tools/accuracy_registry.py is first for a reason: its sha256 is pinned in the
# pre-registration chain and it refuses to run when its own bytes change. This
# manifest is a second, independent place that notices -- the grader's guard
# protects the grader, and this protects everything around it.
SOURCE_ARTIFACTS = (
    "tools/accuracy_registry.py",
    "tools/selection_honesty.py",
    # Imported by selection_honesty.py, so their bytes decide a verdict exactly
    # as much as its own do.
    "tools/skillpower.py",
    "tools/skillschema.py",
    "tools/publication_gate.py",
    "tools/grader_heartbeat.py",
    "tools/live_accuracy.py",
    # Measures the survivorship bound published beside the numbers.
    "tools/backfill_delistings.py",
    # The wrapper that decides whether any of the above reaches the served file,
    # and the entrypoint that decides whether the wrapper ever runs.
    "ops/grade.sh",
    "ops/docker-entrypoint.sh",
    "PREREGISTRATION.md",
)

# Artifacts the image does NOT keep at their repository path. Verified where
# they actually RUN: pinning /app/ops/grade.sh while /usr/local/bin/grade.sh is
# the file the schedule executes would check a copy nothing invokes.
RELOCATED_ARTIFACTS = {
    "ops/grade.sh": "usr/local/bin/grade.sh",
    "ops/docker-entrypoint.sh": "usr/local/bin/docker-entrypoint.sh",
}


def artifact_path(rel: str, root: Path, bin_root: Path) -> Path:
    """Where `rel` lives at VERIFY time (inside the image)."""
    moved = RELOCATED_ARTIFACTS.get(rel)
    return (bin_root / moved) if moved else (root / rel)

# Compiled during the image build, so the host cannot know them at emit time.
# Sealed by the `seal` step and re-checked at grade time, which catches a binary
# swapped inside a running container.
BINARY_ARTIFACTS = (
    "usr/local/bin/signaldeckd",
    "usr/local/bin/collapsecheck",
)


def sha256_file(path: Path) -> str | None:
    h = hashlib.sha256()
    try:
        with open(path, "rb") as fh:
            for chunk in iter(lambda: fh.read(65536), b""):
                h.update(chunk)
    except OSError:
        return None
    return h.hexdigest()


def _git(repo: Path, *args: str) -> str | None:
    try:
        out = subprocess.run(
            ["git", "-C", str(repo), *args],
            capture_output=True, text=True, timeout=30,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if out.returncode != 0:
        return None
    return out.stdout.strip()


def emit(repo: Path) -> dict:
    """Run on the BUILD HOST, where git exists."""
    revision = _git(repo, "rev-parse", "HEAD")
    status = _git(repo, "status", "--porcelain")
    artifacts: dict[str, str] = {}
    missing: list[str] = []
    for rel in SOURCE_ARTIFACTS:
        digest = sha256_file(repo / rel)
        if digest is None:
            missing.append(rel)
        else:
            artifacts[rel] = digest
    return {
        "schema": SCHEMA,
        # RECORDED, not proof of anything. `verify` never upgrades this.
        "revision": revision or "",
        "revision_resolvable": revision is not None,
        "dirty": bool(status) if status is not None else None,
        "source_artifacts": artifacts,
        "source_artifacts_missing": missing,
        "binary_artifacts": {},
        "sealed": False,
    }


def seal(manifest: dict, root: Path) -> dict:
    """Run INSIDE the image, after the binaries have been copied in."""
    out = dict(manifest)
    binaries: dict[str, str] = {}
    missing: list[str] = []
    for rel in BINARY_ARTIFACTS:
        digest = sha256_file(root / rel)
        if digest is None:
            missing.append(rel)
        else:
            binaries[rel] = digest
    out["binary_artifacts"] = binaries
    out["binary_artifacts_missing"] = missing
    out["sealed"] = True
    return out


def verify(manifest: dict, root: Path, bin_root: Path) -> dict:
    """Re-hash and compare. Never claims the revision was checked.

    `usable` in the result separates "the check RAN and reached a conclusion
    about the bytes" from "the manifest was too broken to check anything". The
    caller reports those differently and must not have to infer which it got by
    matching words in the reason text.

    WHAT THIS REFUSED TO NOTICE UNTIL 2026-09-16. It checked whatever keys the
    manifest happened to carry, and rejected only a completely EMPTY artifact
    map. A manifest pinning one unrelated file, none of the required sources, no
    binaries at all, sealed false, dirty true and revision_resolvable false
    returned ok with checked=1 — a provenance guard signing off on an image that
    bound nothing it was supposed to bind. The contract is not "check what you
    were given"; it is "check what you were supposed to be given".
    """
    if manifest.get("schema") != SCHEMA:
        return {"ok": False, "checked": 0, "usable": False,
                "reason": f"manifest schema is {manifest.get('schema')!r}, expected {SCHEMA!r}"}

    # The expected sets, before a single hash is taken. A file that is not
    # pinned cannot be compared, so an absent pin is indistinguishable from a
    # passing one unless it is refused here.
    missing_src = [r for r in SOURCE_ARTIFACTS if r not in (manifest.get("source_artifacts") or {})]
    if missing_src:
        return {"ok": False, "checked": 0, "usable": False,
                "reason": "manifest does not pin every file that decides a verdict, so it cannot "
                          "bind them: " + ", ".join(missing_src)}
    # Sealing before the binary set, because an unsealed manifest's empty
    # binary map is a consequence of that and the clearer sentence to read.
    if manifest.get("sealed") is not True:
        return {"ok": False, "checked": 0, "usable": False,
                "reason": "manifest was never sealed inside the image, so no compiled binary is "
                          "pinned and a swapped one would pass unnoticed"}
    missing_bin = [r for r in BINARY_ARTIFACTS if r not in (manifest.get("binary_artifacts") or {})]
    if missing_bin:
        return {"ok": False, "checked": 0, "usable": False,
                "reason": "manifest does not pin every compiled artifact: " + ", ".join(missing_bin)}
    if manifest.get("binary_artifacts_missing"):
        return {"ok": False, "checked": 0, "usable": False,
                "reason": "manifest was sealed with missing binary artifacts: "
                          + ", ".join(manifest["binary_artifacts_missing"])}

    mismatches: list[str] = []
    absent: list[str] = []
    checked = 0

    for rel, want in sorted((manifest.get("source_artifacts") or {}).items()):
        # Hash where the file RUNS, not where it was authored: the wrapper and
        # entrypoint are copied to /usr/local/bin inside the image.
        got = sha256_file(artifact_path(rel, root, bin_root))
        checked += 1
        if got is None:
            absent.append(rel)
        elif got != want:
            mismatches.append(f"{rel} is {got[:12]}, manifest pins {want[:12]}")

    for rel, want in sorted((manifest.get("binary_artifacts") or {}).items()):
        got = sha256_file(bin_root / rel)
        checked += 1
        if got is None:
            absent.append(rel)
        elif got != want:
            mismatches.append(f"{rel} is {got[:12]}, manifest pins {want[:12]}")

    # An EMPTY manifest must not pass. A forged or truncated one whose artifact
    # maps are empty would otherwise verify perfectly by checking nothing --
    # which is the fail-open shape this whole file exists to close.
    if checked == 0:
        return {"ok": False, "checked": 0, "usable": False,
                "reason": "manifest pins no artifacts at all, so it binds nothing"}
    if manifest.get("source_artifacts_missing"):
        return {"ok": False, "checked": checked, "usable": False,
                "reason": "manifest was emitted with missing source artifacts: "
                          + ", ".join(manifest["source_artifacts_missing"])}
    if absent:
        return {"ok": False, "checked": checked, "usable": True,
                "reason": "pinned artifact(s) not present in this image: " + ", ".join(absent)}
    if mismatches:
        return {"ok": False, "checked": checked, "usable": True,
                "reason": "SHIPPED BYTES DO NOT MATCH THE REVIEWED SOURCE: " + "; ".join(mismatches)}

    # PROVENANCE, which is a statement about the build rather than about the
    # bytes — so the check RAN (usable) and the answer is a deployment fault.
    # A dirty tree names source that exists on no commit; a revision git could
    # not resolve on the build HOST names source nobody can produce. Either way
    # the grade would be tied to something unreproducible.
    #
    # SIGNALDECK_ALLOW_DIRTY_BUILD waives both. It is the same override the
    # daemon already uses for the same question (cmd/signaldeckd/main.go), and
    # reusing it keeps one knob instead of inventing a second one to forget.
    waived = []
    if manifest.get("dirty"):
        waived.append("the build tree was dirty")
    if manifest.get("revision_resolvable") is not True:
        waived.append("git could not resolve the revision on the build host")
    if waived:
        if "SIGNALDECK_ALLOW_DIRTY_BUILD" not in os.environ:
            return {"ok": False, "checked": checked, "usable": True,
                    "reason": "BUILD PROVENANCE IS NOT ESTABLISHED: " + "; ".join(waived)
                              + ". The bytes match the manifest, but the manifest names a build "
                                "nobody can reproduce"}
        return {"ok": True, "checked": checked,
                "usable": True,
                "reason": "PROVENANCE ACCEPTED UNDER SIGNALDECK_ALLOW_DIRTY_BUILD: "
                          + "; ".join(waived)}
    return {"ok": True, "checked": checked, "usable": True, "reason": ""}


def _describe(manifest: dict) -> str:
    rev = manifest.get("revision") or "(none)"
    dirty = manifest.get("dirty")
    # "RECORDED, not verified" is still exactly right, but the REASON changed and
    # a provenance message that misdescribes the image is the kind of small lie
    # this file exists to prevent. The image now DOES carry git and a commit-only
    # object store, because the pinned grader needs to resolve the revision on
    # every historical row. What this manifest still cannot do is verify the
    # revision, for the original reason: it reports what the build host wrote
    # into it, and a label the caller supplied is not evidence. The commit store
    # answers "does that commit exist"; only the hashes below answer "are these
    # the bytes that were reviewed".
    bits = [f"revision {rev[:12]} RECORDED (this manifest reports it, it does not verify it)"]
    if dirty:
        bits.append("built from a DIRTY tree")
    if not manifest.get("revision_resolvable", False):
        bits.append("git could not resolve it on the build host")
    if not manifest.get("sealed", False):
        bits.append("manifest was never sealed, so no binary is pinned")
    return "; ".join(bits)


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("mode", nargs="?", choices=["emit", "seal", "verify"])
    ap.add_argument("--repo", default=".")
    ap.add_argument("--root", default="/app")
    ap.add_argument("--bin-root", default="/")
    ap.add_argument("--manifest", default="")
    ap.add_argument("--out", default="")
    ap.add_argument("--selfcheck", action="store_true")
    a = ap.parse_args(argv)

    if a.selfcheck:
        _selfcheck()
        print("BUILDMANIFEST SELFCHECK OK")
        return 0
    if not a.mode:
        ap.error("a mode is required unless --selfcheck is given")

    if a.mode == "emit":
        m = emit(Path(a.repo))
        text = json.dumps(m, indent=1, sort_keys=True)
        if a.out:
            Path(a.out).write_text(text + "\n", encoding="utf-8")
        else:
            print(text)
        print(f"BUILDMANIFEST EMIT pinned={len(m['source_artifacts'])} "
              f"missing={len(m['source_artifacts_missing'])} rev={m['revision'][:12] or '(none)'}",
              file=sys.stderr)
        return 1 if m["source_artifacts_missing"] else 0

    if not a.manifest:
        ap.error(f"--manifest is required for {a.mode}")
    try:
        manifest = json.loads(Path(a.manifest).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as e:
        # EXIT 2, NOT 1. "I could not run" and "I ran and the bytes disagree"
        # are different findings and the caller reports them differently: the
        # first is a check outage, the second is a deployment fault naming a
        # file. Collapsing them would publish an outage as an accusation, which
        # is the same error as publishing an accusation as an outage.
        print(f"BUILDMANIFEST {a.mode.upper()} UNAVAILABLE unreadable manifest: {e}")
        return 2

    if a.mode == "seal":
        sealed = seal(manifest, Path(a.root))
        Path(a.manifest).write_text(json.dumps(sealed, indent=1, sort_keys=True) + "\n",
                                    encoding="utf-8")
        print(f"BUILDMANIFEST SEAL binaries={len(sealed['binary_artifacts'])} "
              f"missing={len(sealed.get('binary_artifacts_missing') or [])}")
        return 1 if sealed.get("binary_artifacts_missing") else 0

    res = verify(manifest, Path(a.root), Path(a.bin_root))
    # A manifest that is structurally unusable (wrong schema, pins nothing,
    # emitted incomplete) means the check could not be performed -- exit 2. A
    # manifest that WAS usable and disagreed with the bytes on disk is a
    # measured mismatch -- exit 1. The caller keeps them apart.
    #
    # It used to be a substring match on the reason text, so every failure
    # reason added later silently joined whichever class its wording happened to
    # match. verify() now says which it is.
    unusable = (not res["ok"]) and not res["usable"]
    status = "OK" if res["ok"] else ("UNAVAILABLE" if unusable else "FAIL")
    line = f"BUILDMANIFEST VERIFY {status} checked={res['checked']} {_describe(manifest)}"
    if res["reason"]:
        line += f" -- {res['reason']}"
    print(line)
    if res["ok"]:
        return 0
    return 2 if unusable else 1


# ──────────────────────────────────────────────────────────────────────────────

def _selfcheck() -> None:
    with tempfile.TemporaryDirectory() as td:
        root = Path(td) / "app"
        (root / "tools").mkdir(parents=True)
        (root / "usr/local/bin").mkdir(parents=True)
        # emit() reads the REPOSITORY layout; verify() reads the IMAGE layout,
        # where the wrapper and entrypoint live under usr/local/bin. Both are
        # written, because a fixture that only had the repo copy would never
        # exercise the relocation and would pass while verify checked a file
        # nothing runs.
        for rel in SOURCE_ARTIFACTS:
            body = f"content of {rel}\n"
            for p in {root / rel, artifact_path(rel, root, root)}:
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text(body, encoding="utf-8")
        for rel in BINARY_ARTIFACTS:
            (root / rel).write_text(f"ELF-ish {rel}\n", encoding="utf-8")

        m = emit(root)
        assert len(m["source_artifacts"]) == len(SOURCE_ARTIFACTS), m
        assert m["source_artifacts_missing"] == [], m

        # AN UNSEALED MANIFEST NO LONGER VERIFIES AT ALL. It pins no binary, so
        # a swapped signaldeckd inside a running container would have gone
        # unnoticed while the check reported ok.
        r = verify(m, root, root)
        assert not r["ok"] and not r["usable"] and "never sealed" in r["reason"], r

        sealed = seal(m, root)
        assert sealed["sealed"] and len(sealed["binary_artifacts"]) == len(BINARY_ARTIFACTS), sealed

        # A real build host records a resolvable revision and a clean tree. This
        # temp directory is not a git repository, so emit() recorded neither;
        # state them here rather than letting the provenance check fire on every
        # case below for a reason that has nothing to do with what is being
        # tested. The provenance cases set them back deliberately.
        m = dict(sealed, revision="a" * 40, revision_resolvable=True, dirty=False)

        # a clean image verifies
        r = verify(m, root, root)
        assert r["ok"] and r["usable"], r

        # one byte changed anywhere is caught
        (root / "tools/accuracy_registry.py").write_text("tampered\n", encoding="utf-8")
        r = verify(m, root, root)
        assert not r["ok"] and "DO NOT MATCH" in r["reason"], r
        (root / "tools/accuracy_registry.py").write_text(
            "content of tools/accuracy_registry.py\n", encoding="utf-8")
        assert verify(m, root, root)["ok"]

        # a missing pinned artifact is caught
        (root / "PREREGISTRATION.md").unlink()
        r = verify(m, root, root)
        assert not r["ok"] and "not present" in r["reason"], r
        (root / "PREREGISTRATION.md").write_text("content of PREREGISTRATION.md\n",
                                                 encoding="utf-8")

        # A FORGED LABEL CHANGES NOTHING. Rewriting the revision does not make a
        # tampered image verify, and does not make a clean one fail: the binding
        # is to bytes, not to what the caller typed.
        forged = dict(m, revision="0" * 40)
        assert verify(forged, root, root)["ok"], "a relabelled but honest image must still verify"
        (root / "tools/selection_honesty.py").write_text("swapped\n", encoding="utf-8")
        assert not verify(forged, root, root)["ok"], "a forged label must not rescue swapped bytes"
        (root / "tools/selection_honesty.py").write_text(
            "content of tools/selection_honesty.py\n", encoding="utf-8")

        # ── R03: the widened boundary, exercised ──────────────────────────────
        # Each of these could rewrite a published verdict without touching any
        # file the manifest pinned before 2026-09-20.
        for rel, where in (
            # imported by selection_honesty: decides `supported` and builds the
            # typed `result` record
            ("tools/skillpower.py", root / "tools/skillpower.py"),
            ("tools/skillschema.py", root / "tools/skillschema.py"),
            # measures the survivorship bound published beside the numbers
            ("tools/backfill_delistings.py", root / "tools/backfill_delistings.py"),
            # THE WRAPPER: it alone can publish a registry no gate approved, and
            # it is verified at the path it RUNS from, not at its repo path
            ("ops/grade.sh", root / "usr/local/bin/grade.sh"),
            ("ops/docker-entrypoint.sh", root / "usr/local/bin/docker-entrypoint.sh"),
        ):
            original = where.read_text(encoding="utf-8")
            where.write_text("swapped after the build\n", encoding="utf-8")
            r = verify(m, root, root)
            assert not r["ok"] and r["usable"] and rel in r["reason"], (rel, r)
            where.write_text(original, encoding="utf-8")
            assert verify(m, root, root)["ok"], rel

        # A relocated artifact present only at its REPOSITORY path is not
        # present in the image. Pinning /app/ops/grade.sh while the schedule
        # runs /usr/local/bin/grade.sh would check a copy nothing invokes.
        moved = root / "usr/local/bin/grade.sh"
        body = moved.read_text(encoding="utf-8")
        moved.unlink()
        r = verify(m, root, root)
        assert not r["ok"] and "ops/grade.sh" in r["reason"], r
        moved.write_text(body, encoding="utf-8")
        assert verify(m, root, root)["ok"]

        # MISSING REQUIRED EVIDENCE: a manifest that simply omits the wrapper
        # must not verify by checking what is left.
        thin = dict(m, source_artifacts={
            k: v for k, v in m["source_artifacts"].items() if k != "ops/grade.sh"})
        r = verify(thin, root, root)
        assert not r["ok"] and "ops/grade.sh" in r["reason"], r

        # an empty manifest binds nothing and must NOT pass
        empty = dict(m, source_artifacts={}, binary_artifacts={})
        r = verify(empty, root, root)
        assert not r["ok"] and not r["usable"], r

        # wrong schema is refused
        r = verify(dict(m, schema="something/else"), root, root)
        assert not r["ok"] and "schema" in r["reason"], r

        # a swapped binary is caught
        (root / "usr/local/bin/collapsecheck").write_text("STALE BINARY\n", encoding="utf-8")
        r = verify(m, root, root)
        assert not r["ok"] and r["usable"] and "collapsecheck" in r["reason"], r
        (root / "usr/local/bin/collapsecheck").write_text(
            "ELF-ish usr/local/bin/collapsecheck\n", encoding="utf-8")
        assert verify(m, root, root)["ok"]

        # THE MANIFEST THE 2026-09-15 AUDIT BUILT, verbatim in shape: correct
        # schema, one unrelated file that really is on disk, none of the files
        # that decide a verdict, no binaries, never sealed, dirty, unresolvable.
        # It returned ok with checked=1 — a provenance guard signing off on an
        # image pinning nothing it was supposed to pin.
        (root / "tools/unrelated.py").write_text("not an artifact\n", encoding="utf-8")
        audit = {
            "schema": SCHEMA,
            "revision": "",
            "revision_resolvable": False,
            "dirty": True,
            "source_artifacts": {"tools/unrelated.py": sha256_file(root / "tools/unrelated.py")},
            "source_artifacts_missing": [],
            "binary_artifacts": {},
            "sealed": False,
        }
        r = verify(audit, root, root)
        assert not r["ok"] and not r["usable"], r

        # each half of that, on its own, against an otherwise perfect manifest
        r = verify(dict(m, sealed=False), root, root)
        assert not r["ok"] and not r["usable"] and "never sealed" in r["reason"], r
        short = dict(m, source_artifacts={k: v for k, v in m["source_artifacts"].items()
                                          if k != SOURCE_ARTIFACTS[0]})
        r = verify(short, root, root)
        assert not r["ok"] and not r["usable"] and SOURCE_ARTIFACTS[0] in r["reason"], r
        r = verify(dict(m, binary_artifacts={}), root, root)
        assert not r["ok"] and not r["usable"], r

        # PROVENANCE. The bytes are right and the build still is not: these are
        # deployment faults, so the check RAN (usable) and the answer is no.
        for bad in (dict(m, dirty=True), dict(m, revision_resolvable=False)):
            r = verify(bad, root, root)
            assert not r["ok"] and r["usable"] and "PROVENANCE" in r["reason"], r

        # and the one override, which says so in the reason rather than passing
        # silently. os.environ is restored whatever happens.
        os.environ["SIGNALDECK_ALLOW_DIRTY_BUILD"] = "1"
        try:
            r = verify(dict(m, dirty=True), root, root)
            assert r["ok"] and "PROVENANCE ACCEPTED" in r["reason"], r
        finally:
            del os.environ["SIGNALDECK_ALLOW_DIRTY_BUILD"]

        # an unsealed manifest says so rather than implying binaries were checked
        assert "never sealed" in _describe(dict(m, sealed=False))
        # and the revision is never described as verified
        # The revision is RECORDED and is NEVER described as verified. The exact
        # wording is pinned because this one line is what a reader takes the
        # image's provenance from, and it has already had to change once: it
        # used to say "no git in this image", which stopped being true when the
        # commit store was added for the grader's revision gate.
        assert "RECORDED" in _describe(m)
        assert "does not verify it" in _describe(m)
        assert "verified" not in _describe(m)


if __name__ == "__main__":
    sys.exit(main())

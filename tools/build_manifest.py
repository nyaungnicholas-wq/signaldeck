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
SOURCE_ARTIFACTS = (
    "tools/accuracy_registry.py",
    "tools/selection_honesty.py",
    "tools/publication_gate.py",
    "tools/grader_heartbeat.py",
    "tools/live_accuracy.py",
    "PREREGISTRATION.md",
)

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
    """Re-hash and compare. Never claims the revision was checked."""
    if manifest.get("schema") != SCHEMA:
        return {"ok": False, "checked": 0,
                "reason": f"manifest schema is {manifest.get('schema')!r}, expected {SCHEMA!r}"}

    mismatches: list[str] = []
    absent: list[str] = []
    checked = 0

    for rel, want in sorted((manifest.get("source_artifacts") or {}).items()):
        got = sha256_file(root / rel)
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
        return {"ok": False, "checked": 0,
                "reason": "manifest pins no artifacts at all, so it binds nothing"}
    if manifest.get("source_artifacts_missing"):
        return {"ok": False, "checked": checked,
                "reason": "manifest was emitted with missing source artifacts: "
                          + ", ".join(manifest["source_artifacts_missing"])}
    if absent:
        return {"ok": False, "checked": checked,
                "reason": "pinned artifact(s) not present in this image: " + ", ".join(absent)}
    if mismatches:
        return {"ok": False, "checked": checked,
                "reason": "SHIPPED BYTES DO NOT MATCH THE REVIEWED SOURCE: " + "; ".join(mismatches)}
    return {"ok": True, "checked": checked, "reason": ""}


def _describe(manifest: dict) -> str:
    rev = manifest.get("revision") or "(none)"
    dirty = manifest.get("dirty")
    bits = [f"revision {rev[:12]} RECORDED (not verifiable here: no git in this image)"]
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
    unusable = (not res["ok"]) and (
        res["checked"] == 0 or "schema" in res["reason"] or "emitted with missing" in res["reason"]
    )
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
        for rel in SOURCE_ARTIFACTS:
            p = root / rel
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(f"content of {rel}\n", encoding="utf-8")
        m = emit(root)
        assert len(m["source_artifacts"]) == len(SOURCE_ARTIFACTS), m
        assert m["source_artifacts_missing"] == [], m

        # a clean image verifies
        r = verify(m, root, root)
        assert r["ok"], r

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

        # an empty manifest binds nothing and must NOT pass
        empty = dict(m, source_artifacts={}, binary_artifacts={})
        r = verify(empty, root, root)
        assert not r["ok"] and "binds nothing" in r["reason"], r

        # wrong schema is refused
        r = verify(dict(m, schema="something/else"), root, root)
        assert not r["ok"] and "schema" in r["reason"], r

        # sealing adds the binaries, and a swapped binary is then caught
        (root / "usr/local/bin/collapsecheck").write_text("ELF-ish\n", encoding="utf-8")
        (root / "usr/local/bin/signaldeckd").write_text("ELF-ish daemon\n", encoding="utf-8")
        sealed = seal(m, root)
        assert sealed["sealed"] and len(sealed["binary_artifacts"]) == len(BINARY_ARTIFACTS), sealed
        assert verify(sealed, root, root)["ok"]
        (root / "usr/local/bin/collapsecheck").write_text("STALE BINARY\n", encoding="utf-8")
        r = verify(sealed, root, root)
        assert not r["ok"] and "collapsecheck" in r["reason"], r

        # an unsealed manifest says so rather than implying binaries were checked
        assert "never sealed" in _describe(m)
        # and the revision is never described as verified
        assert "not verifiable here" in _describe(sealed)
        assert "verified" not in _describe(sealed).replace("not verifiable", "")


if __name__ == "__main__":
    sys.exit(main())

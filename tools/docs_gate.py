#!/usr/bin/env python3
"""
Documentation publication gate for SignalDeck.

WHY THIS EXISTS. On 2026-08-04 a P0 truth-stop froze ten strategy documents
because four of them published mutually inconsistent live-accuracy figures, three
asserted survivorship control that ALPHA_WORKFLOW.md §B2 measured open, and one
cited a kill switch living in a *different repository*. Every one of those was a
number or a status a human typed into markdown by hand, with nothing in the
repository able to contradict it.

This gate makes that class of defect fail a build instead of reaching a reader.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sqlite3
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

# ──────────────────────────────────────────────────────────────────────────────
# Constants
# ──────────────────────────────────────────────────────────────────────────────

VALID_STATUSES = {"DRAFT", "ACTIVE", "FROZEN", "NOT_AUTHORITATIVE", "SUPERSEDED"}
VALID_BACKTEST_DATA = {"none", "pre-survivorship-fix", "post-survivorship-fix"}
DEFAULT_MIN_DELIST_RATE = 0.02

LIVE_ACCURACY_PHRASES = [
    "live accuracy",
    "live record",
    "live directional",
    "directional accuracy",
    "hit rate",
    "win rate",
    "realized accuracy",
    "accuracy of",
    "graded at",
    "accuracy is",
    "accuracy was",
]

FORBIDDEN_PHRASES = [
    (r"\bguaranteed\b", "guaranteed"),
    (r"\bsurefire\b", "surefire"),
    (r"\bzero risk\b", "zero risk"),
]

PERCENTAGE_RE = re.compile(r"\d+(?:\.\d+)?\s*%")
GENERATED_BEGIN_RE = re.compile(r"^<!--\s*BEGIN GENERATED:\s*(\S+)\s*-->$")
GENERATED_END_RE = re.compile(r"^<!--\s*END GENERATED:\s*(\S+)\s*-->$")
LIVE_ACCURACY_PARTIAL_BEGIN_RE = re.compile(r"^<!--\s*LIVE-ACCURACY-PARTIAL:BEGIN\s*-->$")
LIVE_ACCURACY_PARTIAL_END_RE = re.compile(r"^<!--\s*LIVE-ACCURACY-PARTIAL:END\s*-->$")
TABLE_ROW_RE = re.compile(r"^\s*\|")
TABLE_SEPARATOR_RE = re.compile(r"^\s*\|[\s\-\:|]+\|\s*$")
PROOF_LINK_RE = re.compile(r"\]\((proofs/|repro/)[^)]+\)")
CODE_REF_RE = re.compile(r"\b\w+\.\w+:\d+\b")

# ──────────────────────────────────────────────────────────────────────────────
# Violation helpers
# ──────────────────────────────────────────────────────────────────────────────

def make_violation(check: str, file: str, line: int, message: str, **extra: Any) -> dict:
    """Create a violation record with exactly the required keys."""
    v = {"check": check, "file": file, "line": line, "message": message}
    # Allow extra keys for internal use (like line_text)
    v.update(extra)
    return v

def apply_allowlist(violations: list[dict], allow: dict[str, list[dict]]) -> list[dict]:
    """Suppress violations that match an allowlist entry with a non-empty reason."""
    if not allow:
        return violations
    kept = []
    for v in violations:
        check_id = v["check"]
        file_path = v["file"]
        line_text = v.get("line_text", "")
        suppressed = False
        for entry in allow.get(check_id, []):
            if entry.get("file") != file_path:
                continue
            reason = entry.get("reason", "")
            if not reason or not reason.strip():
                continue  # reasonless entry suppresses nothing
            line_contains = entry.get("line_contains", "")
            if line_contains and line_contains not in line_text:
                continue
            suppressed = True
            break
        if not suppressed:
            kept.append(v)
    return kept

# ──────────────────────────────────────────────────────────────────────────────
# File I/O helpers
# ──────────────────────────────────────────────────────────────────────────────

def normalize_newlines(text: str) -> str:
    """Replace \\r\\n and \\r with \\n for newline-insensitive comparisons."""
    return text.replace("\r\n", "\n").replace("\r", "\n")

def read_json(path: Path) -> tuple[dict | None, list[dict]]:
    """Read JSON file. Returns (data, violations)."""
    violations = []
    if not path.exists():
        violations.append(make_violation("integrity-snapshot", str(path), 0,
            f"Missing {path}. Run `python tools/docs_gate.py write-integrity` on the machine holding the database."))
        return None, violations
    try:
        with path.open("r", encoding="utf-8") as f:
            return json.load(f), violations
    except json.JSONDecodeError as e:
        violations.append(make_violation("integrity-snapshot", str(path), e.lineno,
            f"Invalid JSON in {path}: {e}. Run `python tools/docs_gate.py write-integrity` on the machine holding the database."))
        return None, violations

def write_json(path: Path, data: dict) -> None:
    """Write JSON deterministically."""
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8", newline="\n") as f:
        json.dump(data, f, indent=2, sort_keys=False)
        f.write("\n")

def read_text_lines(path: Path) -> list[str]:
    """Read file as list of lines (without trailing newlines)."""
    if not path.exists():
        return []
    with path.open("r", encoding="utf-8") as f:
        return [line.rstrip("\n") for line in f]

def write_text(path: Path, content: str) -> None:
    """Write text with LF newlines."""
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8", newline="\n") as f:
        f.write(content)

def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(8192), b""):
            h.update(chunk)
    return h.hexdigest()

# ──────────────────────────────────────────────────────────────────────────────
# Check implementations
# ──────────────────────────────────────────────────────────────────────────────

def check_integrity_snapshot(repo: Path, registry: dict) -> tuple[list[dict], dict | None]:
    """Check ops/data-integrity.json exists and is valid JSON."""
    violations = []
    snapshot_path = repo / "ops" / "data-integrity.json"
    snapshot, snap_violations = read_json(snapshot_path)
    violations.extend(snap_violations)
    return violations, snapshot

def check_grader_status(repo: Path, snapshot: dict) -> list[dict]:
    """Check grader status is OK (case-insensitive)."""
    violations = []
    grader = snapshot.get("grader", {})
    status = str(grader.get("status", "")).strip().upper()
    if status != "OK":
        reason = grader.get("refusal_reason")
        msg = f"Grader status is '{grader.get('status')}', not OK."
        if reason:
            msg += f" Refusal reason: {reason}"
        msg += " Resolve the grader refusal, then run `python tools/docs_gate.py write-integrity` and commit ops/data-integrity.json."
        violations.append(make_violation("grader-status", "ops/data-integrity.json", 0, msg))
    return violations

def check_docs_index(repo: Path, registry: dict) -> list[dict]:
    """Validate strategy_docs entries and check for unregistered .md files in repo root."""
    violations = []
    strategy_docs = registry.get("strategy_docs", {})
    exempt = set(registry.get("exempt", []))

    # Check each registered doc
    for filename, meta in strategy_docs.items():
        filepath = repo / filename
        if not filepath.exists():
            violations.append(make_violation("docs-index", filename, 0,
                f"Registered strategy document {filename} does not exist in repo root. Add the file or remove it from ops/docs-registry.json."))
            continue

        # Required fields
        for field in ("owner", "version", "data_as_of", "status", "backtest_data"):
            value = meta.get(field)
            if not isinstance(value, str) or not value.strip():
                violations.append(make_violation("docs-index", filename, 0,
                    f"Missing or empty required field '{field}' in ops/docs-registry.json for {filename}. Provide a non-empty string."))

        # data_as_of must be valid ISO date
        data_as_of = meta.get("data_as_of", "").strip()
        if data_as_of:
            try:
                datetime.fromisoformat(data_as_of).date()
            except ValueError:
                violations.append(make_violation("docs-index", filename, 0,
                    f"Invalid data_as_of '{data_as_of}' for {filename}. Must be ISO date (YYYY-MM-DD)."))

        # status must be valid
        status = meta.get("status", "").strip()
        if status and status not in VALID_STATUSES:
            violations.append(make_violation("docs-index", filename, 0,
                f"Invalid status '{status}' for {filename}. Must be one of: {', '.join(sorted(VALID_STATUSES))}."))

        # backtest_data must be valid
        backtest = meta.get("backtest_data", "").strip()
        if backtest and backtest not in VALID_BACKTEST_DATA:
            violations.append(make_violation("docs-index", filename, 0,
                f"Invalid backtest_data '{backtest}' for {filename}. Must be one of: {', '.join(sorted(VALID_BACKTEST_DATA))}."))

    # Check for unregistered .md files in repo root (non-recursive)
    for entry in repo.iterdir():
        if entry.is_file() and entry.suffix == ".md":
            name = entry.name
            if name not in strategy_docs and name not in exempt:
                violations.append(make_violation("docs-index", name, 0,
                    f"Strategy document {name} exists in repo root but is not registered in ops/docs-registry.json and not in exempt list. Add it to strategy_docs or exempt."))

    return violations

# A percentage that states an interval's CONFIDENCE LEVEL, or the coin-flip
# NULL a document is arguing against, is not an accuracy claim. Both fired on
# the gate's first run over the real repository — `95% CI (block)` in a table
# header, `the day-clustered 95% interval on live accuracy`, and `the baseline
# is the prequential majority, never 50%`. None of the three states a result.
# A gate that flags the statistics vocabulary of the documents it guards is a
# gate someone switches off, so these are scrubbed BEFORE the accuracy rule
# looks at the line. Scrubbing is per-token, never per-line: a real figure
# sitting beside a confidence level on the same line still counts.
CONFIDENCE_PCT_RE = re.compile(
    r"\d+(?:\.\d+)?\s*%\s*(?:CI\b|confidence|credible|interval)"
    r"|(?:\bCI\b|confidence|credible|interval)[^%]{0,20}?\d+(?:\.\d+)?\s*%",
    re.IGNORECASE)
COIN_FLIP_NULL_RE = re.compile(r"\b50(?:\.0+)?\s*%")
NULL_CONTEXT_WORDS = ("baseline", "null", "never", "coin", "chance", "random")


def scrub_non_accuracy_percentages(line: str) -> str:
    """Remove percentage tokens that cannot be an accuracy claim."""
    scrubbed = CONFIDENCE_PCT_RE.sub(" ", line)
    if any(w in line.lower() for w in NULL_CONTEXT_WORDS):
        scrubbed = COIN_FLIP_NULL_RE.sub(" ", scrubbed)
    return scrubbed


def check_no_hardcoded_live_accuracy(repo: Path, registry: dict) -> list[dict]:
    """Scan strategy docs for hardcoded live-accuracy percentages outside generated regions."""
    violations = []
    strategy_docs = registry.get("strategy_docs", {})

    for filename in strategy_docs:
        filepath = repo / filename
        if not filepath.exists():
            continue
        lines = read_text_lines(filepath)

        # First pass: scan for regions and check well-formedness (change 3)
        # We'll collect well-formed region line ranges (start, end) for generated content
        # and also check for any malformed region markers.
        wellformed_regions = []  # list of (start_line, end_line) inclusive
        current_begin = None
        current_begin_line = 0
        current_name = None
        for i, line in enumerate(lines, 1):
            begin_match = GENERATED_BEGIN_RE.match(line)
            live_begin_match = LIVE_ACCURACY_PARTIAL_BEGIN_RE.match(line)
            end_match = GENERATED_END_RE.match(line)
            live_end_match = LIVE_ACCURACY_PARTIAL_END_RE.match(line)

            if current_begin is None:
                if begin_match:
                    current_begin = "generated"
                    current_begin_line = i
                    current_name = begin_match.group(1)
                elif live_begin_match:
                    current_begin = "live_accuracy"
                    current_begin_line = i
                    current_name = "live_accuracy"
            else:
                # We're inside a region, look for a matching end
                if current_begin == "generated" and end_match:
                    end_name = end_match.group(1)
                    if end_name == current_name:
                        # Well-formed region
                        wellformed_regions.append((current_begin_line, i))
                        current_begin = None
                        current_name = None
                    else:
                        # Mismatched names
                        violations.append(make_violation("single-source-of-truth", filename, current_begin_line,
                            f"Generated region mismatch: BEGIN '{current_name}' closed by END '{end_name}' on line {i}."))
                        current_begin = None
                        current_name = None
                elif current_begin == "live_accuracy" and live_end_match:
                    wellformed_regions.append((current_begin_line, i))
                    current_begin = None
                    current_name = None
                else:
                    # Look for another begin inside a region (nesting)
                    if begin_match or live_begin_match:
                        violations.append(make_violation("single-source-of-truth", filename, i,
                            f"Nested generated region starts on line {i} while already inside region opened on line {current_begin_line}."))
                        current_begin = None
                        current_name = None

        # If we exited the loop while still inside a region
        if current_begin is not None:
            violations.append(make_violation("single-source-of-truth", filename, current_begin_line,
                f"Unterminated generated region opened on line {current_begin_line}."))
            # No well-formed regions from this BEGIN onward (fail closed)

        # Second pass: check for hardcoded live accuracy outside well-formed regions
        for i, line in enumerate(lines, 1):
            # Determine if this line is inside any well-formed region
            in_generated = False
            for start, end in wellformed_regions:
                if start <= i <= end:
                    in_generated = True
                    break
            if in_generated:
                continue

            # Check for percentage + live-accuracy context, on a line whose
            # STATISTICS VOCABULARY has been scrubbed out first (see below).
            if PERCENTAGE_RE.search(scrub_non_accuracy_percentages(line)):
                line_lower = line.lower()
                for phrase in LIVE_ACCURACY_PHRASES:
                    if phrase in line_lower:
                        violations.append(make_violation("no-hardcoded-live-accuracy", filename, i,
                            f"Hardcoded live-accuracy figure on line {i}. Move the figure into a generated partial (run `python tools/docs_gate.py build` and commit partials/live_record.md) or remove the claim.", line_text=line))
                        break  # One violation per line is enough

    return violations

def check_forbidden_claims(repo: Path, registry: dict) -> list[dict]:
    """Check for forbidden phrases and unverified 'verified' in table rows."""
    violations = []
    strategy_docs = registry.get("strategy_docs", {})

    for filename in strategy_docs:
        filepath = repo / filename
        if not filepath.exists():
            continue
        lines = read_text_lines(filepath)

        for i, line in enumerate(lines, 1):
            line_lower = line.lower()

            # Forbidden phrases with word boundaries
            for pattern, phrase in FORBIDDEN_PHRASES:
                if re.search(pattern, line_lower):
                    violations.append(make_violation("forbidden-claims", filename, i,
                        f"Forbidden claim '{phrase}' on line {i}. Remove the claim or qualify it with evidence.", line_text=line))

            # Check table rows for 'verified' without proof
            stripped = line.strip()
            if TABLE_ROW_RE.match(stripped) and not TABLE_SEPARATOR_RE.match(stripped):
                if re.search(r"\bverified\b", line_lower):
                    # Change 5: blank out negated forms
                    test_line = line_lower
                    # Remove patterns that negate "verified"
                    test_line = re.sub(r"\b(?:not|never|no)\s+(?:yet\s+)?(?:been\s+)?verified\b", "", test_line)
                    test_line = re.sub(r"\bunverified\b", "", test_line)
                    if re.search(r"\bverified\b", test_line):
                        has_proof = bool(PROOF_LINK_RE.search(line) or CODE_REF_RE.search(line))
                        if not has_proof:
                            violations.append(make_violation("forbidden-claims", filename, i,
                                f"Table row contains 'verified' without a proof link on line {i}. Add a markdown link to proofs/ or repro/, or a code reference like file.py:123.", line_text=line))

    return violations

def generate_live_record(snapshot: dict) -> str:
    """Generate live_record.md content deterministically from snapshot."""
    lines = [
        "# Live Record",
        "",
    ]

    grader = snapshot.get("grader", {})
    status = str(grader.get("status", "")).strip()
    graded_at = grader.get("graded_at")
    refused_since = grader.get("refused_since")
    refusal_reason = grader.get("refusal_reason")
    rows = grader.get("rows", 0)

    if status.upper() == "OK":
        lines.append(f"Graded at: {graded_at}")
        lines.append(f"Graded rows: {rows}")
        lines.append("")
        lines.append("Live accuracy figures are published only when the grader status is OK.")
    else:
        lines.append(f"Grader status: {status}")
        if refusal_reason:
            lines.append(f"Refusal reason: {refusal_reason}")
        if refused_since:
            lines.append(f"Refused since: {refused_since}")
        lines.append("")
        lines.append("No live-accuracy figure is publishable while the grader is refusing.")

    body = "\n".join(lines) + "\n"
    # Change 1: wrap in BEGIN/END markers
    return (
        f"<!-- BEGIN GENERATED: live_record.md -->\n"
        "<!-- GENERATED by tools/docs_gate.py from ops/data-integrity.json -->\n"
        "<!-- DO NOT EDIT. Regenerate: python tools/docs_gate.py build -->\n"
        "\n"
        f"{body}"
        "<!-- END GENERATED: live_record.md -->\n"
    )

def generate_partial(name: str, snapshot: dict) -> str:
    """Generate a partial by name. Only live_record.md has defined content."""
    if name == "live_record.md":
        return generate_live_record(snapshot)
    # Minimal deterministic stub for unknown partials
    body = f"# {name}\n\nNo generator defined for this partial.\n"
    return (
        f"<!-- BEGIN GENERATED: {name} -->\n"
        "<!-- GENERATED by tools/docs_gate.py from ops/data-integrity.json -->\n"
        "<!-- DO NOT EDIT. Regenerate: python tools/docs_gate.py build -->\n"
        "\n"
        f"{body}"
        f"<!-- END GENERATED: {name} -->\n"
    )

def check_single_source_of_truth(repo: Path, registry: dict, snapshot: dict) -> list[dict]:
    """Check that declared partials exist and match current generation."""
    violations = []
    partials = registry.get("partials", [])

    for name in partials:
        partial_path = repo / "partials" / name
        if not partial_path.exists():
            violations.append(make_violation("single-source-of-truth", f"partials/{name}", 0,
                f"Declared partial partials/{name} is missing. Run `python tools/docs_gate.py build` and commit the generated file."))
            continue

        # Generate expected content
        expected = generate_partial(name, snapshot)
        actual = partial_path.read_text(encoding="utf-8")
        # Change 2: newline-insensitive comparison
        if normalize_newlines(expected) != normalize_newlines(actual):
            violations.append(make_violation("single-source-of-truth", f"partials/{name}", 0,
                f"Partial partials/{name} is stale (does not match current generation). Run `python tools/docs_gate.py build` and commit the updated file."))

    return violations

def check_data_integrity(repo: Path, registry: dict, snapshot: dict) -> list[dict]:
    """Facts only the database can answer, read from the committed snapshot.

    Every rule here corresponds to a defect that was live on 2026-08-04: an
    empty point-in-time universe (look-ahead in every cross-sectional
    denominator), a delisting count so low the universe was effectively
    survivor-seeded, and backtest tables computed on that universe with nothing
    in the document saying so.
    """
    violations = []
    thresholds = registry.get("thresholds", {})
    min_delist_rate = thresholds.get("min_delist_rate_per_year", DEFAULT_MIN_DELIST_RATE)

    # universe_membership_rows > 0
    universe_rows = snapshot.get("universe_membership_rows", 0)
    if not isinstance(universe_rows, (int, float)) or universe_rows <= 0:
        violations.append(make_violation(
            "data-integrity", "ops/data-integrity.json", 0,
            "universe_membership holds no rows, so every cross-sectional rank is "
            "computed against a universe that includes names not yet listed — "
            "look-ahead. Populate it (phase P3B), then run "
            "`python tools/docs_gate.py write-integrity` and commit the snapshot."))

    # Delisting rate. A universe that never loses a name is a survivor-seeded
    # universe, and every backtest over it is biased upward.
    delisting = snapshot.get("delisting") or {}
    rate = delisting.get("rate_per_year")
    if not isinstance(rate, (int, float)):
        violations.append(make_violation(
            "data-integrity", "ops/data-integrity.json", 0,
            "The snapshot records no delisting rate_per_year. Run "
            "`python tools/docs_gate.py write-integrity` on the machine holding "
            "the database and commit ops/data-integrity.json."))
    elif rate < min_delist_rate:
        violations.append(make_violation(
            "data-integrity", "ops/data-integrity.json", 0,
            f"Delisting rate {rate:.4f}/year is below the {min_delist_rate:.4f} "
            f"floor ({delisting.get('stocks_delisted')} delisted of "
            f"{delisting.get('stocks_total')} over "
            f"{delisting.get('span_years')} years). A universe that loses names "
            "this slowly is survivor-seeded; real broad US delisting runs several "
            "percent per year. Backfill delistings (phase P3A), then re-run "
            "`python tools/docs_gate.py write-integrity`."))

    # A backtest computed before the survivorship repair must SAY so. The freeze
    # banners already satisfy this; the rule exists so a NEW document cannot
    # quietly publish old-universe numbers once the banners come off.
    strategy_docs = registry.get("strategy_docs", {})
    for filename in sorted(strategy_docs):
        meta = strategy_docs[filename]
        if not isinstance(meta, dict):
            continue
        if meta.get("backtest_data") != "pre-survivorship-fix":
            continue
        filepath = repo / filename
        if not filepath.exists():
            continue
        content = filepath.read_text(encoding="utf-8", errors="replace")
        labelled = ("pre-survivorship-fix" in content
                    or "FROZEN" in content
                    or "NOT AUTHORITATIVE" in content)
        if not labelled:
            violations.append(make_violation(
                "data-integrity", filename, 0,
                f"{filename} is registered as backtest_data=pre-survivorship-fix "
                "but carries no visible label saying so. Add the literal marker "
                "`pre-survivorship-fix`, or a FROZEN / NOT AUTHORITATIVE banner, "
                "or re-run the backtest on the repaired universe and change the "
                "registry entry to post-survivorship-fix."))

    return violations


# ──────────────────────────────────────────────────────────────────────────────
# Registry loading
# ──────────────────────────────────────────────────────────────────────────────

class GateError(Exception):
    """The gate itself cannot run. Exit 2, never exit 0.

    Kept distinct from a violation on purpose: "the documents are wrong" (1) and
    "the gate could not look" (2) must never be the same signal. A gate that
    passes because it was handed an empty registry is worse than no gate, since
    it reports green.
    """


def load_registry(repo: Path) -> dict:
    path = repo / "ops" / "docs-registry.json"
    if not path.exists():
        raise GateError(f"missing {path} — the gate has nothing to enforce")
    try:
        with path.open("r", encoding="utf-8") as f:
            registry = json.load(f)
    except json.JSONDecodeError as e:
        raise GateError(f"{path} is not valid JSON: {e}") from e
    if not isinstance(registry, dict):
        raise GateError(f"{path} must contain a JSON object")

    docs = registry.get("strategy_docs")
    if not isinstance(docs, dict) or not docs:
        raise GateError(
            f"{path} declares no strategy_docs. A gate that passes because it "
            "was handed nothing reports green on a broken repository.")
    for key, typ, name in (("exempt", list, "a list"),
                           ("partials", list, "a list"),
                           ("allow", dict, "an object")):
        if key in registry and not isinstance(registry[key], typ):
            raise GateError(f"{path}: `{key}` must be {name}")
    return registry


# ──────────────────────────────────────────────────────────────────────────────
# Modes
# ──────────────────────────────────────────────────────────────────────────────

def mode_check(repo: Path, as_json: bool) -> int:
    registry = load_registry(repo)
    violations: list[dict] = []

    # Source-only checks always run — they need nothing but the tree.
    violations += check_docs_index(repo, registry)
    violations += check_no_hardcoded_live_accuracy(repo, registry)
    violations += check_forbidden_claims(repo, registry)

    # The two database-derived checks read the committed snapshot. A missing or
    # unparseable snapshot is a VIOLATION, never a skip: the CI split (answer
    # where the database lives, verify where it does not) must not be able to
    # degrade into silence.
    snap_violations, snapshot = check_integrity_snapshot(repo, registry)
    violations += snap_violations
    if snapshot is not None:
        violations += check_grader_status(repo, snapshot)
        violations += check_data_integrity(repo, registry, snapshot)
        violations += check_single_source_of_truth(repo, registry, snapshot)

    violations = apply_allowlist(violations, registry.get("allow", {}) or {})
    # Deterministic order, so two runs of one input produce identical output.
    violations.sort(key=lambda v: (v.get("check", ""), v.get("file", ""),
                                   v.get("line", 0), v.get("message", "")))

    if as_json:
        print(json.dumps({"violations": violations}))
    elif not violations:
        print("docs-gate: clean")
    else:
        for v in violations:
            where = v.get("file") or "-"
            line = v.get("line") or 0
            loc = f"{where}:{line}" if line else where
            print(f"docs-gate: {v['check']}: {loc}: {v['message']}")
        print(f"docs-gate: {len(violations)} violation(s)", file=sys.stderr)
    return 1 if violations else 0


def mode_build(repo: Path) -> int:
    registry = load_registry(repo)
    snapshot_path = repo / "ops" / "data-integrity.json"
    if not snapshot_path.exists():
        print(f"docs_gate build: missing {snapshot_path}; refusing to generate a "
              "live record from nothing", file=sys.stderr)
        return 2
    try:
        with snapshot_path.open("r", encoding="utf-8") as f:
            snapshot = json.load(f)
    except json.JSONDecodeError as e:
        print(f"docs_gate build: {snapshot_path} is not valid JSON: {e}", file=sys.stderr)
        return 2

    written = []
    for name in registry.get("partials", []) or []:
        content = generate_partial(name, snapshot)
        path = repo / "partials" / name
        write_text(path, content)
        written.append((name, sha256_file(path)))

    stamp = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    log = [f"docs_gate build {stamp}",
           f"source: ops/data-integrity.json (generated {snapshot.get('generated')})",
           f"grader status: {(snapshot.get('grader') or {}).get('status')}"]
    log += [f"wrote partials/{n}  sha256={d}" for n, d in written]
    if not written:
        log.append("no partials declared in ops/docs-registry.json")
    write_text(repo / "logs" / "docs_build.log", "\n".join(log) + "\n")
    for line in log:
        print(line)
    return 0


def mode_write_integrity(repo: Path) -> int:
    """Answer, on the machine that has the database, what CI cannot ask.

    data/ is gitignored and signaldeck.db is ~4 GB, so a runner can never query
    it. Same split as ops/ledger-provenance.sh: compute here, commit the answer,
    verify there.
    """
    db_path = repo / "data" / "signaldeck.db"
    reg_path = repo / "data" / "accuracy_registry.json"
    out_path = repo / "ops" / "data-integrity.json"
    warnings: list[str] = []

    grader = {"status": "MISSING", "graded_at": None, "refused_since": None,
              "refusal_reason": f"{reg_path} not found", "rows": 0}
    if reg_path.exists():
        try:
            with reg_path.open("r", encoding="utf-8") as f:
                reg = json.load(f)
            # A SUCCESSFUL grade omits `status` entirely -- only a refusal sets
            # it (status: REFUSED). Copying the absent key verbatim recorded
            # None, while check_grader_status demands exactly "OK", so a
            # successful grade failed this gate identically to a refused one.
            # The gate could never pass on a success; it had only ever been run
            # against refusals, because until 2026-08-04T18:23:15 there was no
            # successful grade for it to see. Derive the status the registry
            # implies instead of copying a key that is absent by design.
            status = reg.get("status")
            if status is None:
                status = "OK" if (reg.get("rows") and not reg.get("refusal_reason")) else "MISSING"
            grader = {
                "status": status,
                "graded_at": reg.get("graded_at"),
                "refused_since": reg.get("refused_since"),
                "refusal_reason": reg.get("refusal_reason"),
                "rows": len(reg.get("rows") or []),
            }
        except (OSError, json.JSONDecodeError, AttributeError) as e:
            warnings.append(f"accuracy_registry.json unreadable: {e}")

    universe_rows = 0
    delisting = {"stocks_total": 0, "stocks_delisted": 0,
                 "span_years": 0.0, "rate_per_year": 0.0}
    if not db_path.exists():
        warnings.append(f"{db_path} not found")
    else:
        try:
            con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=30)
        except sqlite3.Error as e:
            con = None
            warnings.append(f"cannot open database: {e}")
        if con is not None:
            def scalar(sql, default=0):
                try:
                    row = con.execute(sql).fetchone()
                    return row[0] if row and row[0] is not None else default
                except sqlite3.Error as e:
                    warnings.append(f"{sql.strip()[:60]}: {e}")
                    return default
            universe_rows = scalar("SELECT COUNT(*) FROM universe_membership")
            total = scalar("SELECT COUNT(*) FROM symbols WHERE market='stocks'")
            delisted = scalar("SELECT COUNT(*) FROM symbols "
                              "WHERE market='stocks' AND delisted_at IS NOT NULL")
            oldest = scalar("SELECT MIN(added_at) FROM symbols WHERE market='stocks'")
            now = int(datetime.now(timezone.utc).timestamp())
            span = max(0.5, (now - int(oldest)) / 31557600.0) if oldest else 0.5
            rate = (delisted / total / span) if total and span else 0.0
            delisting = {"stocks_total": int(total), "stocks_delisted": int(delisted),
                         "span_years": round(span, 3), "rate_per_year": round(rate, 4)}
            con.close()

    # Preserve a previously recorded repair date; inventing today's date on every
    # run would silently move the boundary that labels old backtests.
    fix_date = datetime.now(timezone.utc).date().isoformat()
    if out_path.exists():
        try:
            with out_path.open("r", encoding="utf-8") as f:
                fix_date = json.load(f).get("survivorship_fix_date") or fix_date
        except (OSError, json.JSONDecodeError, AttributeError):
            pass

    out = {
        "generated": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "source_db": "data/signaldeck.db",
        "grader": grader,
        "universe_membership_rows": int(universe_rows),
        "delisting": delisting,
        "survivorship_fix_date": fix_date,
    }
    if warnings:
        out["warnings"] = warnings
    write_json(out_path, out)
    print(f"docs_gate: wrote {out_path}")
    for w in warnings:
        print(f"docs_gate: WARNING {w}", file=sys.stderr)
    return 0


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(
        prog="docs_gate.py",
        description="Fail a build when a strategy document states what the "
                    "repository's own measurements contradict.")
    ap.add_argument("mode", choices=["check", "build", "write-integrity"])
    ap.add_argument("--repo", default=str(Path(__file__).resolve().parent.parent),
                    help="repository root (default: parent of tools/)")
    ap.add_argument("--json", action="store_true",
                    help="check only: emit {\"violations\": [...]} on stdout")
    args = ap.parse_args(argv)
    repo = Path(args.repo).resolve()

    try:
        if args.mode == "check":
            return mode_check(repo, args.json)
        if args.mode == "build":
            return mode_build(repo)
        return mode_write_integrity(repo)
    except GateError as e:
        print(f"docs_gate: {e}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())

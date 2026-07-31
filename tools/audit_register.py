#!/usr/bin/env python3
"""Machine-checked register of the audit findings tables in `audits/*-reaudit.md`.

`ops/REVIEW_LIFECYCLE.md` promises, in prose, that findings are "tracked to
fixed/refuted — never left 'proposed' past the next audit". Nothing read that
column, so the promise was unenforceable: a known-open defect could age out of
view between audits while the lifecycle document claimed otherwise. This script
turns the prose into an invariant.

It parses the pipe-delimited findings table of every `audits/*-reaudit.md`
(header `| id | area | severity | impact | evidence (measured) | status |`,
rows starting `| **A1** |`) into a normalized register and exits non-zero when:

  (a) a status falls outside the closed vocabulary
      {fixed, refuted, accepted-risk, open};
  (b) a row marked `fixed` cites no test name, commit sha, or measured number
      anywhere in its impact/evidence cells — a fix with no evidence is a claim;
  (c) a row is still unresolved in an audit file older than the newest audit
      file's date — the aging rule the Cadence paragraph already states.

The script reads markdown only and writes nothing. It is EXPECTED to fail on a
tree with unresolved older findings; that failing state is the finding. Widening
the vocabulary to make it pass would delete the check, not satisfy it.
"""

from __future__ import annotations

import argparse
import os
import re
import sys
from dataclasses import dataclass

CLOSED_VOCABULARY = ("fixed", "refuted", "accepted-risk", "open")
# Statuses that mean the finding is not yet put to rest. `open` is a legal
# vocabulary word but is still unresolved for the aging rule.
RESOLVED = frozenset({"fixed", "refuted", "accepted-risk"})

HEADER_CELLS = ["id", "area", "severity", "impact", "evidence (measured)", "status"]
ROW_RE = re.compile(r"^\|\s*\*\*(A\d+)\*\*\s*\|")
FILE_DATE_RE = re.compile(r"(\d{4}-\d{2}-\d{2})-reaudit\.md$")

# Evidence that a "fixed" row is anchored in something checkable.
TEST_NAME_RE = re.compile(r"\b(?:test_\w+|Test[A-Z]\w+)\b")
SHA_RE = re.compile(r"\b[0-9a-f]{7,40}\b")
NUMBER_RE = re.compile(r"\d")


@dataclass(frozen=True)
class Finding:
    audit: str
    audit_date: str
    fid: str
    area: str
    severity: str
    impact: str
    evidence: str
    status_raw: str
    status: str


def normalize_status(raw: str) -> str:
    """Reduce a prose status cell to its bare claim.

    `**fixed** (18 tests added)` -> `fixed`
    `**diagnosed — needs a redeploy (operator action, see §4)**` -> `diagnosed`
    `could-not-verify (relayed)` -> `could-not-verify`
    """
    s = raw.replace("**", "").replace("*", "")
    s = re.sub(r"\([^)]*\)", " ", s)
    s = re.split(r"[—–-]{1,2}\s", s)[0]
    return " ".join(s.lower().split()).strip(" .;")


def split_row(line: str) -> list[str]:
    # Cells may contain escaped pipes inside inline code (`strings foo \| grep`),
    # so split only on unescaped delimiters.
    cells = re.split(r"(?<!\\)\|", line.strip())
    if cells and cells[0].strip() == "":
        cells = cells[1:]
    if cells and cells[-1].strip() == "":
        cells = cells[:-1]
    return [c.strip() for c in cells]


def parse_findings(text: str, audit: str, audit_date: str) -> list[Finding]:
    """Extract the findings table rows. Rows outside a matching header are ignored."""
    findings: list[Finding] = []
    in_table = False
    for line in text.splitlines():
        cells = split_row(line) if line.lstrip().startswith("|") else []
        if cells and [c.lower() for c in cells] == HEADER_CELLS:
            in_table = True
            continue
        if not line.lstrip().startswith("|"):
            in_table = False
            continue
        if not in_table or not ROW_RE.match(line.strip()):
            continue
        if len(cells) < 6:
            continue
        findings.append(
            Finding(
                audit=audit,
                audit_date=audit_date,
                fid=cells[0].replace("**", "").strip(),
                area=cells[1],
                severity=cells[2],
                # Status is always the last cell and evidence the one before it;
                # a stray unescaped pipe would otherwise silently shift columns.
                impact=" | ".join(cells[3:-2]),
                evidence=cells[-2],
                status_raw=cells[-1],
                status=normalize_status(cells[-1]),
            )
        )
    return findings


def cites_evidence(f: Finding) -> bool:
    blob = f"{f.impact} {f.evidence}"
    return bool(TEST_NAME_RE.search(blob) or SHA_RE.search(blob) or NUMBER_RE.search(blob))


def check(findings: list[Finding]) -> list[str]:
    violations: list[str] = []
    if not findings:
        return violations
    newest = max(f.audit_date for f in findings)

    for f in sorted(findings, key=lambda x: (x.audit_date, x.fid)):
        where = f"{f.audit}:{f.fid}"
        if f.status not in CLOSED_VOCABULARY:
            violations.append(
                f"{where}: status {f.status_raw!r} is outside the closed vocabulary "
                f"{{{', '.join(CLOSED_VOCABULARY)}}}"
            )
        if f.status == "fixed" and not cites_evidence(f):
            violations.append(
                f"{where}: marked fixed but its impact/evidence cells cite no test name, "
                f"commit sha, or measured number"
            )
        if f.status not in RESOLVED and f.audit_date < newest:
            violations.append(
                f"{where}: still unresolved ({f.status_raw!r}) in an audit dated "
                f"{f.audit_date}, older than the newest audit {newest} — "
                f"ops/REVIEW_LIFECYCLE.md Cadence forbids carrying a finding past the next audit"
            )
    return violations


def load(audits_dir: str) -> list[Finding]:
    findings: list[Finding] = []
    if not os.path.isdir(audits_dir):
        return findings
    for name in sorted(os.listdir(audits_dir)):
        m = FILE_DATE_RE.search(name)
        if not m:
            continue
        path = os.path.join(audits_dir, name)
        with open(path, encoding="utf-8") as fh:
            findings.extend(parse_findings(fh.read(), name, m.group(1)))
    return findings


def main(argv: list[str] | None = None) -> int:
    repo_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--audits-dir", default=os.path.join(repo_root, "audits"))
    args = ap.parse_args(argv)

    findings = load(args.audits_dir)
    if not findings:
        print(f"audit_register: no findings tables found in {args.audits_dir}", file=sys.stderr)
        return 1

    audits = sorted({f.audit for f in findings})
    print(f"audit_register: {len(findings)} findings across {len(audits)} audits")
    for a in audits:
        rows = [f for f in findings if f.audit == a]
        unresolved = [f.fid for f in rows if f.status not in RESOLVED]
        print(f"  {a}: {len(rows)} findings, unresolved: {', '.join(unresolved) or 'none'}")

    violations = check(findings)
    if violations:
        print("\naudit_register: FAIL", file=sys.stderr)
        for v in violations:
            print(f"  - {v}", file=sys.stderr)
        print(
            "\nFix the findings or record a status inside the closed vocabulary with real "
            "evidence. Do NOT widen the vocabulary to clear this.",
            file=sys.stderr,
        )
        return 1

    print("audit_register: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())

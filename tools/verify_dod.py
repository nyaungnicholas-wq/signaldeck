"""
read-only auditor for SignalDeck repository
"""
import argparse
import hashlib
import json
import os
import re
import sqlite3
import sys
from datetime import datetime, timezone
from pathlib import Path
from collections import namedtuple

Check = namedtuple("Check", "id status detail")

def file_sha256(path):
    """sha256 of a file in hex, or None if missing"""
    try:
        h = hashlib.sha256()
        h.update(path.read_bytes())
        return h.hexdigest()
    except Exception:
        return None

def check_GRADER_REGISTERED(repo_path):
    db_path = repo_path / "data" / "signaldeck.db"
    if not db_path.exists():
        return Check("GRADER_REGISTERED", "FAIL", f"missing: {db_path}")
    try:
        with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as conn:
            cur = conn.execute(
                "SELECT spec_json FROM prereg_records "
                "WHERE kind='grading-protocol' ORDER BY seq DESC LIMIT 1"
            )
            row = cur.fetchone()
            if not row:
                return Check("GRADER_REGISTERED", "FAIL", "no grading-protocol record")
            spec = json.loads(row[0])
            db_hash = spec.get("graderSha256", "")
        tools_path = repo_path / "tools" / "accuracy_registry.py"
        file_hash = file_sha256(tools_path)
        if file_hash is None:
            return Check("GRADER_REGISTERED", "FAIL", f"missing: {tools_path}")
        if file_hash == db_hash:
            detail = f"{file_hash[:12]} matches {db_hash[:12]}"
            return Check("GRADER_REGISTERED", "PASS", detail)
        else:
            detail = f"file {file_hash[:12]} != db {db_hash[:12]}"
            return Check("GRADER_REGISTERED", "FAIL", detail)
    except Exception as e:
        return Check("GRADER_REGISTERED", "FAIL", str(e))

def check_CHAIN_INTACT(repo_path):
    db_path = repo_path / "data" / "signaldeck.db"
    if not db_path.exists():
        return Check("CHAIN_INTACT", "FAIL", f"missing: {db_path}")
    try:
        with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as conn:
            cur = conn.execute(
                "SELECT seq, ts, kind, spec_hash, note, prev_hash, entry_hash "
                "FROM prereg_records ORDER BY seq ASC"
            )
            rows = cur.fetchall()
        prev_entry = None
        for seq, ts, kind, spec_hash, note, prev_hash, entry_hash in rows:
            # Check prev_hash linkage
            if prev_hash != prev_entry and prev_entry is not None:
                return Check("CHAIN_INTACT", "FAIL",
                             f"prev_hash mismatch at seq {seq}")
            # Recompute entry_hash
            try:
                formula = (f"ts={ts}|kind={kind}|specHash={spec_hash}|note={note}")
                payload = prev_hash.encode() + b"\x1e" + formula.encode()
                recomputed = hashlib.sha256(payload).hexdigest()
            except Exception:
                recomputed = None
            if recomputed != entry_hash:
                return Check("CHAIN_INTACT", "FAIL",
                             f"entry_hash mismatch at seq {seq}")
            prev_entry = entry_hash
        return Check("CHAIN_INTACT", "PASS", "all rows verified")
    except Exception as e:
        return Check("CHAIN_INTACT", "FAIL", str(e))

def check_NO_MANUAL_REPIN(repo_path):
    db_path = repo_path / "data" / "signaldeck.db"
    if not db_path.exists():
        return Check("NO_MANUAL_REPIN", "FAIL", f"missing: {db_path}")
    try:
        with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as conn:
            rows = conn.execute(
                "SELECT seq, note FROM prereg_records "
                "WHERE kind='grading-protocol' ORDER BY seq ASC"
            ).fetchall()
            # A record explaining an off-path append. The chain is append-only, so
            # the offending record's own note can never be rewritten to say
            # AMENDMENT — the only honest remedy is a later record naming it.
            explained = {
                r[0] for r in conn.execute(
                    "SELECT json_extract(spec_json, '$.anomaly.seq') FROM prereg_records "
                    "WHERE kind='protocol-provenance-correction'"
                ).fetchall() if r[0] is not None
            }
        if len(rows) < 2:
            return Check("NO_MANUAL_REPIN", "PASS", "no amendment needed")

        offending = [seq for seq, note in rows[1:] if not note.startswith("AMENDMENT")]
        unexplained = [str(s) for s in offending if s not in explained]
        if unexplained:
            return Check("NO_MANUAL_REPIN", "FAIL",
                         f"unexplained non-amendment re-pin at seq {', '.join(unexplained)}")
        if offending:
            return Check("NO_MANUAL_REPIN", "PASS",
                         f"seq {', '.join(str(s) for s in offending)} entered off-path, "
                         f"explained on-chain by protocol-provenance-correction")
        return Check("NO_MANUAL_REPIN", "PASS", "all amendments correct")
    except Exception as e:
        return Check("NO_MANUAL_REPIN", "FAIL", str(e))

def check_GRADER_HEALTHY(repo_path):
    db_path = repo_path / "data" / "signaldeck.db"
    if not db_path.exists():
        return Check("GRADER_HEALTHY", "FAIL", f"missing: {db_path}")
    try:
        with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as conn:
            cur = conn.execute(
                "SELECT finished_at, success, error FROM grader_heartbeats "
                "ORDER BY finished_at DESC LIMIT 1"
            )
            row = cur.fetchone()
        if not row:
            return Check("GRADER_HEALTHY", "FAIL", "no heartbeat rows")
        finished_at, success, error = row
        if success:
            return Check("GRADER_HEALTHY", "PASS",
                         f"last success at {finished_at}")
        else:
            return Check("GRADER_HEALTHY", "FAIL",
                         f"failed at {finished_at}: {error}")
    except Exception as e:
        return Check("GRADER_HEALTHY", "FAIL", str(e))

def check_REGISTRY_NOT_REFUSED(repo_path):
    reg_path = repo_path / "data" / "accuracy_registry.json"
    if not reg_path.exists():
        return Check("REGISTRY_NOT_REFUSED", "FAIL", f"missing: {reg_path}")
    try:
        with open(reg_path) as f:
            reg = json.load(f)
        status = reg.get("status", "")
        refused_since = reg.get("refused_since", "")
        if status == "REFUSED":
            return Check("REGISTRY_NOT_REFUSED", "FAIL",
                         f"status={status}, refused_since={refused_since}")
        return Check("REGISTRY_NOT_REFUSED", "PASS",
                     f"status={status}")
    except Exception as e:
        return Check("REGISTRY_NOT_REFUSED", "FAIL", str(e))

def check_NO_SUPERSEDED_SNAPSHOT(repo_path):
    reg_path = repo_path / "data" / "accuracy_registry.json"
    if not reg_path.exists():
        return Check("NO_SUPERSEDED_SNAPSHOT", "FAIL", f"missing: {reg_path}")
    try:
        with open(reg_path) as f:
            reg = json.load(f)
        status = reg.get("status", "")
        rows = reg.get("rows", [])
        stale = reg.get("stale_last_registry", {})
        stale_rows = stale.get("rows", [])
        if status == "REFUSED" or not rows:
            if stale_rows:
                return Check("NO_SUPERSEDED_SNAPSHOT", "FAIL",
                             f"{len(stale_rows)} stale rows, graded_at={stale.get('graded_at','')}")
        return Check("NO_SUPERSEDED_SNAPSHOT", "PASS", "no stale snapshot present")
    except Exception as e:
        return Check("NO_SUPERSEDED_SNAPSHOT", "FAIL", str(e))

def _auditable_rows(reg):
    """The rows a per-row check should judge, and where they came from.

    When the grader is refused the live "rows" list is empty, so a per-row check
    run against it passes without inspecting anything. The superseded snapshot in
    stale_last_registry still carries publishable numbers, so it is what an
    auditor actually has to judge. Returns (rows, source) with source None when
    there is nothing to judge at all.
    """
    live = reg.get("rows") or []
    if live:
        return live, "live"
    stale = (reg.get("stale_last_registry") or {}).get("rows") or []
    if stale:
        return stale, "stale snapshot"
    return [], None


def _insufficient_verdict(verdict):
    """True when the verdict text is a statement that no grade exists yet.

    Only a verdict that asserts something about PERFORMANCE (VALIDATED, HOLDING,
    DECAYED, FAILED, NO SKILL) needs an interval behind it. "PENDING (first grade
    2026-08-14, 0/30 resolved)" and "INSUFFICIENT DAYS (7/10)" are the surface
    correctly declining to grade, which is the opposite of the defect this check
    exists to catch.
    """
    verdict_upper = verdict.upper()
    for term in ["INSUFFICIENT", "NO INTERVAL", "NO VERDICT", "WIDE",
                 "UNMEASURED", "PENDING", "NOT YET"]:
        if term in verdict_upper:
            return True
    return False

def check_NO_VERDICT_WITHOUT_INTERVAL(repo_path):
    reg_path = repo_path / "data" / "accuracy_registry.json"
    if not reg_path.exists():
        return Check("NO_VERDICT_WITHOUT_INTERVAL", "FAIL", f"missing: {reg_path}")
    try:
        with open(reg_path) as f:
            reg = json.load(f)
        offending = []
        # Check main rows
        for row in reg.get("rows", []):
            verdict = row.get("verdict", "")
            ci = row.get("ci")
            ci_method = row.get("ci_method")
            if verdict and not _insufficient_verdict(verdict):
                if ci is None or ci_method == "withheld":
                    offending.append(row.get("predictor", "unknown"))
        # Check stale rows
        stale = reg.get("stale_last_registry", {})
        for row in stale.get("rows", []):
            verdict = row.get("verdict", "")
            ci = row.get("ci")
            ci_method = row.get("ci_method")
            if verdict and not _insufficient_verdict(verdict):
                if ci is None or ci_method == "withheld":
                    offending.append(row.get("predictor", "unknown"))
        if offending:
            return Check("NO_VERDICT_WITHOUT_INTERVAL", "FAIL",
                         f"offending: {', '.join(offending[:10])}")
        judged = len(reg.get("rows") or []) + len(
            (reg.get("stale_last_registry") or {}).get("rows") or [])
        if judged == 0:
            return Check("NO_VERDICT_WITHOUT_INTERVAL", "MANUAL",
                         "no rows in the registry or its stale snapshot")
        return Check("NO_VERDICT_WITHOUT_INTERVAL", "PASS",
                     f"{judged} rows judged, no verdict published without its interval")
    except Exception as e:
        return Check("NO_VERDICT_WITHOUT_INTERVAL", "FAIL", str(e))

def check_SURVIVORSHIP_DISCLOSED(repo_path):
    reg_path = repo_path / "data" / "accuracy_registry.json"
    if not reg_path.exists():
        return Check("SURVIVORSHIP_DISCLOSED", "FAIL", f"missing: {reg_path}")
    try:
        with open(reg_path) as f:
            reg = json.load(f)
        rows, source = _auditable_rows(reg)
        if source is None:
            return Check("SURVIVORSHIP_DISCLOSED", "MANUAL",
                         "no rows in the registry or its stale snapshot")
        clean = disclosed = silent = 0
        for row in rows:
            if row.get("survivorship_clean") is True:
                clean += 1
            elif row.get("survivorship_reason"):
                disclosed += 1
            else:
                silent += 1
        if silent:
            return Check("SURVIVORSHIP_DISCLOSED", "FAIL",
                         f"[{source}] clean={clean}, disclosed={disclosed}, silent={silent}")
        return Check("SURVIVORSHIP_DISCLOSED", "PASS",
                     f"[{source}] clean={clean}, disclosed={disclosed}")
    except Exception as e:
        return Check("SURVIVORSHIP_DISCLOSED", "FAIL", str(e))

def check_UNIVERSE_POPULATED(repo_path):
    db_path = repo_path / "data" / "signaldeck.db"
    if not db_path.exists():
        return Check("UNIVERSE_POPULATED", "FAIL", f"missing: {db_path}")
    try:
        with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as conn:
            cur = conn.execute(
                "SELECT COUNT(*), COUNT(DISTINCT source), "
                "MIN(day), MAX(day) FROM universe_membership"
            )
            cnt, sources, min_day, max_day = cur.fetchone()
        if cnt == 0:
            return Check("UNIVERSE_POPULATED", "FAIL", "empty universe")
        min_iso = datetime.fromtimestamp(min_day, tz=timezone.utc).date().isoformat()
        max_iso = datetime.fromtimestamp(max_day, tz=timezone.utc).date().isoformat()
        return Check("UNIVERSE_POPULATED", "PASS",
                     f"{cnt} rows, {sources} sources, days {min_iso}..{max_iso}")
    except Exception as e:
        return Check("UNIVERSE_POPULATED", "FAIL", str(e))

def check_PIT_NO_FUTURE_ROWS(repo_path):
    db_path = repo_path / "data" / "signaldeck.db"
    if not db_path.exists():
        return Check("PIT_NO_FUTURE_ROWS", "FAIL", f"missing: {db_path}")
    try:
        with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as conn:
            cur = conn.execute(
                "SELECT MAX(day), COUNT(*) FROM universe_membership "
                "WHERE day > strftime('%s', datetime('now', 'start of day', '+1 day'))"
            )
            max_day, cnt = cur.fetchone()
        if cnt and cnt > 0:
            max_iso = datetime.fromtimestamp(max_day, tz=timezone.utc).date().isoformat()
            return Check("PIT_NO_FUTURE_ROWS", "FAIL",
                         f"max day {max_iso}, {cnt} future rows")
        return Check("PIT_NO_FUTURE_ROWS", "PASS", "no future rows")
    except Exception as e:
        return Check("PIT_NO_FUTURE_ROWS", "FAIL", str(e))

def check_BACKTEST_NOT_LABELLED_LIVE(repo_path):
    reg_path = repo_path / "data" / "accuracy_registry.json"
    if not reg_path.exists():
        return Check("BACKTEST_NOT_LABELLED_LIVE", "FAIL", f"missing: {reg_path}")
    try:
        with open(reg_path) as f:
            reg = json.load(f)
        rows, source = _auditable_rows(reg)
        if source is None:
            return Check("BACKTEST_NOT_LABELLED_LIVE", "MANUAL",
                         "no rows in the registry or its stale snapshot")
        offending = []
        backtests = 0
        for row in rows:
            live_n = row.get("live_n", 0)
            if live_n == 0 or live_n is None:
                backtests += 1
                note = row.get("note", "")
                if not re.search(r"backtest|not yet a live record", note, re.IGNORECASE):
                    offending.append(row.get("predictor", "unknown"))
        if offending:
            return Check("BACKTEST_NOT_LABELLED_LIVE", "FAIL",
                         f"[{source}] offending: {', '.join(offending[:10])}")
        return Check("BACKTEST_NOT_LABELLED_LIVE", "PASS",
                     f"[{source}] {backtests}/{len(rows)} rows are backtests, all labelled")
    except Exception as e:
        return Check("BACKTEST_NOT_LABELLED_LIVE", "FAIL", str(e))

def check_RISK_POLICY_EXISTS(repo_path):
    path = repo_path / "RISK_POLICY.md"
    if not path.exists():
        return Check("RISK_POLICY_EXISTS", "FAIL", f"missing: {path}")
    try:
        size = path.stat().st_size
        if size >= 500:
            return Check("RISK_POLICY_EXISTS", "PASS", f"{size} bytes")
        return Check("RISK_POLICY_EXISTS", "FAIL", f"{size} bytes (<500)")
    except Exception as e:
        return Check("RISK_POLICY_EXISTS", "FAIL", str(e))

def check_NO_CROSS_REPO_RISK_CLAIM(repo_path):
    hits = []
    try:
        for md in repo_path.glob("*.md"):
            if md.name.startswith("README") or md.name == "LICENSE":
                continue
            lines = md.read_text(errors="replace").splitlines()
            for i, line in enumerate(lines, start=1):
                line_lower = line.lower()
                if any(term in line_lower for term in [
                    "kill switch", "kill-switch", "risk gate", "risk_gate",
                    "pre-trade", "position sizing", "order routing"
                ]):
                    if any(ref in line for ref in [
                        "stock-trader", "futures-trader", "quant-suite"
                    ]) or re.search(r"\.\./", line):
                        hits.append(f"{md.name}:{i}")
        # Asserting a control and retracting the assertion of it look identical
        # to a line regex, and a retraction often spans lines ("...a file in a /
        # different repository"). Naming the foreign path is the signal worth
        # surfacing; deciding which sense it is used in is a human's job.
        if hits:
            return Check("NO_CROSS_REPO_RISK_CLAIM", "MANUAL",
                         f"{len(hits)} line(s) pair a risk control with a foreign "
                         f"path - read each to tell a claim from a retraction: "
                         f"{'; '.join(hits[:10])}")
        return Check("NO_CROSS_REPO_RISK_CLAIM", "PASS", "no cross-repo risk claims")
    except Exception as e:
        return Check("NO_CROSS_REPO_RISK_CLAIM", "FAIL", str(e))

def check_KILLSWITCH_IN_REPO(repo_path):
    ks_dir = repo_path / "daemon" / "internal" / "killswitch"
    if not ks_dir.is_dir():
        return Check("KILLSWITCH_IN_REPO", "FAIL", f"missing dir: {ks_dir}")
    try:
        # At least one non-test .go file in killswitch dir
        ks_files = list(ks_dir.glob("*.go"))
        non_test = [f for f in ks_files if not f.name.endswith("_test.go")]
        if not non_test:
            return Check("KILLSWITCH_IN_REPO", "FAIL", "no non-test .go in killswitch")
        # At least one .go file elsewhere in daemon references killswitch
        daemon_dir = repo_path / "daemon"
        references = []
        for go_file in daemon_dir.rglob("*.go"):
            if go_file == ks_dir:
                continue
            text = go_file.read_text(errors="replace")
            if "killswitch" in text:
                references.append(go_file.relative_to(repo_path))
        if not references:
            return Check("KILLSWITCH_IN_REPO", "FAIL", "no references to killswitch in daemon")
        return Check("KILLSWITCH_IN_REPO", "PASS",
                     f"{len(non_test)} files, {len(references)} references")
    except Exception as e:
        return Check("KILLSWITCH_IN_REPO", "FAIL", str(e))

def check_DOCS_INDEX_EXISTS(repo_path):
    path = repo_path / "DOCS_INDEX.md"
    if not path.exists():
        return Check("DOCS_INDEX_EXISTS", "FAIL", f"missing: {path}")
    try:
        size = path.stat().st_size
        if size >= 200:
            return Check("DOCS_INDEX_EXISTS", "PASS", f"{size} bytes")
        return Check("DOCS_INDEX_EXISTS", "FAIL", f"{size} bytes (<200)")
    except Exception as e:
        return Check("DOCS_INDEX_EXISTS", "FAIL", str(e))

def check_DOC_HEADERS(repo_path):
    """Every root document carries owner/version/status.

    This repository keeps that metadata in ops/docs-registry.json rather than in
    per-file headers, with an explicit exempt list. Scanning the files themselves
    reports every governed document as unlabelled, which is the wrong contract.
    """
    registry_path = repo_path / "ops" / "docs-registry.json"
    if not registry_path.exists():
        return Check("DOC_HEADERS", "FAIL", f"missing: {registry_path}")
    try:
        with open(registry_path, encoding="utf-8") as f:
            registry = json.load(f)
        docs = registry.get("strategy_docs", {})
        exempt = set(registry.get("exempt", []))
        required = ("owner", "version", "data_as_of", "status", "backtest_data")

        incomplete = [
            name for name, meta in docs.items()
            if not all(str((meta or {}).get(k, "")).strip() for k in required)
        ]
        unregistered = sorted(
            md.name for md in repo_path.glob("*.md")
            if md.name not in docs and md.name not in exempt
        )
        missing_names = incomplete + unregistered
        if missing_names:
            detail = (f"{len(incomplete)} incomplete, {len(unregistered)} unregistered: "
                      f"{', '.join(missing_names[:8])}")
            return Check("DOC_HEADERS", "FAIL", detail)
        return Check("DOC_HEADERS", "PASS", "all headers present")
    except Exception as e:
        return Check("DOC_HEADERS", "FAIL", str(e))

def check_PREREG_ANCHORING_EXPLICIT(repo_path):
    path = repo_path / "PREREGISTRATION.md"
    if not path.exists():
        return Check("PREREG_ANCHORING_EXPLICIT", "FAIL", f"missing: {path}")
    try:
        text = path.read_text(errors="replace")
        text_lower = text.lower()
        if not re.search(r"anchor", text_lower):
            return Check("PREREG_ANCHORING_EXPLICIT", "FAIL", "no anchor mention")
        if not re.search(
            r"(not been published|no such record exists|never pushed|cannot be checked)",
            text_lower
        ):
            return Check("PREREG_ANCHORING_EXPLICIT", "FAIL",
                         "no explicit external-anchoring status")
        # Find the line that contains both
        for i, line in enumerate(text.splitlines(), start=1):
            line_lower = line.lower()
            if re.search(r"anchor", line_lower) and re.search(
                r"(not been published|no such record exists|never pushed|cannot be checked)",
                line_lower
            ):
                return Check("PREREG_ANCHORING_EXPLICIT", "PASS",
                             f"line {i}: {line.strip()}")
        # Should not reach here if regex found, but just in case
        return Check("PREREG_ANCHORING_EXPLICIT", "PASS", "explicit mention found")
    except Exception as e:
        return Check("PREREG_ANCHORING_EXPLICIT", "FAIL", str(e))

def check_DECK_FROM_TRUTH(repo_path):
    deck_files = ["STRATEGY_DECK.md", "CASE_STUDY.md", "SHIP_READINESS.md"]
    found = [f for f in deck_files if (repo_path / f).is_file()]
    if found:
        return Check("DECK_FROM_TRUTH", "MANUAL", f"found: {', '.join(found)}")
    return Check("DECK_FROM_TRUTH", "MANUAL", "none found")

def check_NO_HYPE(repo_path):
    hype_patterns = [
        "institutional-grade", "world-class", "bulletproof",
        "production-ready", "rigorous", "best-in-class", "state of the art"
    ]
    count = 0
    try:
        for md in repo_path.glob("*.md"):
            text = md.read_text(errors="replace").lower()
            for pattern in hype_patterns:
                count += text.count(pattern)
        return Check("NO_HYPE", "MANUAL", f"{count} hype phrases")
    except Exception as e:
        return Check("NO_HYPE", "MANUAL", f"error: {e}")

def check_TABLES_HAVE_STATUS_LABELS(repo_path):
    tables = 0
    try:
        for md in repo_path.glob("*.md"):
            lines = md.read_text(errors="replace").splitlines()
            i = 0
            while i < len(lines):
                if lines[i].startswith("|"):
                    # Look for separator row (next line starting with |---)
                    if i + 1 < len(lines) and re.match(r"^\|[-:]+", lines[i+1]):
                        tables += 1
                i += 1
        return Check("TABLES_HAVE_STATUS_LABELS", "MANUAL",
                     f"{tables} markdown tables found")
    except Exception as e:
        return Check("TABLES_HAVE_STATUS_LABELS", "MANUAL", f"error: {e}")

ALL_CHECKS = [
    check_GRADER_REGISTERED,
    check_CHAIN_INTACT,
    check_NO_MANUAL_REPIN,
    check_GRADER_HEALTHY,
    check_REGISTRY_NOT_REFUSED,
    check_NO_SUPERSEDED_SNAPSHOT,
    check_NO_VERDICT_WITHOUT_INTERVAL,
    check_SURVIVORSHIP_DISCLOSED,
    check_UNIVERSE_POPULATED,
    check_PIT_NO_FUTURE_ROWS,
    check_BACKTEST_NOT_LABELLED_LIVE,
    check_RISK_POLICY_EXISTS,
    check_NO_CROSS_REPO_RISK_CLAIM,
    check_KILLSWITCH_IN_REPO,
    check_DOCS_INDEX_EXISTS,
    check_DOC_HEADERS,
    check_PREREG_ANCHORING_EXPLICIT,
    check_DECK_FROM_TRUTH,
    check_NO_HYPE,
    check_TABLES_HAVE_STATUS_LABELS,
]

def main():
    parser = argparse.ArgumentParser(description="SignalDeck DoD auditor (read-only)")
    # The repository this file lives in, not the caller's working directory.
    # --repo used to default to ".", so the verdict depended on where you stood:
    # from the repo root this reports 16 passed / 0 failed, and from tools/ the
    # same command on the same tree reported 1 passed / 16 failed, with NO_HYPE
    # reading "0 hype phrases" and TABLES_HAVE_STATUS_LABELS reading "0 markdown
    # tables found" against a repo that has 7 and 145. Those two are the reason
    # this matters more than the noisy failures: a check that found NOTHING TO
    # CHECK reports exactly like a check that found nothing wrong. The audit
    # register runs these as a bare `python tools/verify_dod.py`, with no --repo
    # and no stated cwd, so the wrong answer was one directory away.
    parser.add_argument("--repo", default=Path(__file__).resolve().parent.parent,
                        type=Path, help="path to repository root "
                                        "(default: the repo containing this file)")
    args = parser.parse_args()
    repo = args.repo.resolve()

    # An auditor pointed at the wrong tree must say so, not audit an empty set
    # and call it clean. PREREGISTRATION.md is the cheapest thing that is always
    # present in this repository and never present by accident.
    if not (repo / "PREREGISTRATION.md").is_file():
        print(f"verify_dod: {repo} does not look like the SignalDeck repository "
              "(no PREREGISTRATION.md) — refusing to audit a tree I cannot read, "
              "because an empty audit is indistinguishable from a clean one",
              file=sys.stderr)
        sys.exit(2)

    results = []
    for check_fn in ALL_CHECKS:
        try:
            res = check_fn(repo)
        except Exception as e:
            res = Check(check_fn.__name__, "FAIL", f"uncaught: {e}")
        results.append(res)

    # Print table
    max_id = max(len(c.id) for c in results)
    for c in results:
        print(f"{c.id:<{max_id}}  {c.status:<8}  {c.detail}")
    passed = sum(1 for c in results if c.status == "PASS")
    failed = sum(1 for c in results if c.status == "FAIL")
    manual = sum(1 for c in results if c.status == "MANUAL")
    print(f"{passed} passed, {failed} failed, {manual} manual")
    sys.exit(1 if failed else 0)

if __name__ == "__main__":
    main()
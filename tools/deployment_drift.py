#!/usr/bin/env python3
"""Refuse publication when a mechanism the pre-registration chain CLAIMS is not
observable in the live database.

Every other honesty gate in this repo reads the source tree: it asks whether the
code at HEAD implements the discipline PREREGISTRATION.md describes. None of them
asks the only question that decides whether a published number was actually
produced under that discipline — is the mechanism RUNNING? The two answers came
apart on this machine and nothing noticed: HEAD implements a matched
naive-persistence baseline, a research-loop judgment ledger, a frozen
unmatched-null quarantine and a blind holdout era, while the binary that wrote
today's rows predates all four. The registry then graded as though the frozen
null existed.

So this check reads the DATABASE, not the tree, and it runs in the publishing
path before the grader. Five facts, each measured and printed with its number:

  (a) naive_label coverage over post-epoch regime_outcomes rows. The store's
      write guard makes a post-epoch NULL baseline impossible; a NULL therefore
      proves the guard is not running.
  (b) the research-loop judgment ledger table exists. Absent, every narrated
      "NOTHING survived" is unverifiable.
  (c) the unmatched-null quarantine table and its manifest exist AND the
      manifest digest still matches the membership. Absent, the exemption the
      chain registers as frozen was never frozen.
  (d) the revision the running daemon stamped into meta resolves to a commit
      whose daemon/internal/researchx/discover.go contains PreregHoldoutEra.
      That constant is the blind-era commitment; a deployed binary without it
      graded on the era it promised never to look at.
  (e) the rows frozen since the newest daemon boot carry that same revision as
      their per-row stamp. (d) certifies a commit; (e) certifies the ROWS. A
      binary that contains the constant but stopped stamping passes (d) with
      every row unstamped, which is the live state here (0/251110
      prediction_ledger rows, 0/3104 worker_runs rows).

It is strictly restrictive. It writes nothing, backfills nothing, relaxes no
threshold, and can only SUPPRESS a verdict — never create or raise one. The
measured 0% null coverage it prints is a number that was previously invisible to
every reader of the registry.

Exit codes: 0 = every claimed mechanism is observable; 1 = at least one is not
(the caller must refuse to publish); 2 = the check itself could not run.

Run: python3 tools/deployment_drift.py [--db PATH] [--repo PATH] [--json OUT]
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sqlite3
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_DB = os.path.join(REPO, "data", "signaldeck.db")

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
# The epoch is imported, never restated: a second copy of this constant is a
# second place for the gate and the grader to disagree about which rows are
# in scope, and the grader's copy is the one that decides verdicts.
from accuracy_registry import (  # noqa: E402
    NULL_AMENDMENT_EPOCH,
    NULL_AMENDMENT_EPOCH_TS,
)

# store.NullAmendmentEpoch — the same instant, in the form the Go digest hashes.
GO_EPOCH = 1785110400

JUDGMENTS_TABLE = "research_loop_judgments"
QUARANTINE_TABLE = "regime_outcome_quarantine"
QUARANTINE_MANIFEST_TABLE = "regime_outcome_quarantine_manifest"
BUILD_REV_META_KEY = "lineage_build_rev"
HOLDOUT_PATH = "daemon/internal/researchx/discover.go"

# The git pathspec for source the DAEMON BINARY actually builds from.
#
# daemon/ is a Go module holding 14 main packages; exactly one of them is the
# daemon. `go list -deps ./cmd/signaldeckd` resolves to internal/** and
# cmd/signaldeckd and nothing else, so a change to sdmaint, caldiag, prereg-amend
# or any other command cannot alter a byte the daemon executes. Scoping the drift
# check at "daemon/" made every such edit demand a rebuild that would fix nothing.
#
# If a new package is ever added that the daemon imports, it goes under internal/
# and is covered automatically. A new standalone command is correctly ignored.
DAEMON_BINARY_PATHS = (
    "daemon/internal/",
    "daemon/cmd/signaldeckd/",
    "daemon/go.mod",
    "daemon/go.sum",
)
HOLDOUT_CONST = "PreregHoldoutEra"

# Tables that carry the per-row revision stamp, with the column naming the
# instant the row was frozen. These are the rows a verdict is built out of.
STAMPED_TABLES = (
    ("worker_runs", "started_at"),
    ("prediction_ledger", "predicted_at"),
    ("regime_outcomes", "ts"),
)

# A daemon boot is the first worker start after a quiet stretch. 900s is far
# longer than any worker cadence that runs continuously and far shorter than a
# restart gap, so the boundary is not sensitive to the exact value.
BOOT_QUIET_SECS = 900


def table_exists(con: sqlite3.Connection, name: str) -> bool:
    return con.execute(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", (name,)
    ).fetchone() is not None


def quarantine_digest(members: list[str]) -> str:
    """Recompute store.quarantineDigest over the live membership.

    Byte-identical to the Go writer (daemon/internal/store/regimeoutcomes.go):
    a domain-separated prefix carrying the epoch and the row count, then each
    canonical member separated by 0x1e.
    """
    h = hashlib.sha256()
    h.update(f"regime-outcome-null-quarantine|epoch={GO_EPOCH}|n={len(members)}".encode())
    for m in members:
        h.update(b"\x1e")
        h.update(m.encode())
    return h.hexdigest()


# The Go declarations this check must agree with. Read, never retyped.
REGIMEOUTCOMES_PATH = os.path.join("daemon", "internal", "store", "regimeoutcomes.go")
STRUCTREGIME_DIR = os.path.join("daemon", "internal", "structregime")
NULL_KINDS_VAR = "structuralNullKinds"


def structural_null_kinds(repo: str) -> list[str]:
    """The kind strings store.structuralNullKinds covers, READ FROM THE GO SOURCE.

    A second hand-maintained copy of this set is exactly the defect
    check_null_coverage below exists to undo, so the same rule as the imported
    epoch applies: one source, or two places to disagree. check_deployed_revision
    already reads Go source for its constant; this is that precedent, not a new
    mechanism.

    Every failure to resolve RAISES. A silently-empty set would make the SQL match
    no rows and the check pass forever while measuring nothing — the dead-gate
    failure mode this whole file was written to catch.
    """
    with open(os.path.join(repo, REGIMEOUTCOMES_PATH), encoding="utf-8") as fh:
        src = fh.read()
    m = re.search(r"var\s+%s\s*=\s*map\[[^\]]+\]bool\s*\{(.*?)\n\}" % NULL_KINDS_VAR,
                  src, re.S)
    if not m:
        raise RuntimeError(
            f"cannot find `var {NULL_KINDS_VAR} = map[...]bool{{...}}` in "
            f"{REGIMEOUTCOMES_PATH} — the write guard's kind set moved or was "
            "renamed, and this check will not mirror a set it cannot read")
    idents = re.findall(r"structregime\.(Kind\w+)\s*:\s*true", m.group(1))
    if not idents:
        raise RuntimeError(
            f"{NULL_KINDS_VAR} in {REGIMEOUTCOMES_PATH} yielded no kind identifiers")
    values: dict[str, str] = {}
    srcdir = os.path.join(repo, STRUCTREGIME_DIR)
    for fn in sorted(os.listdir(srcdir)):
        if not fn.endswith(".go"):
            continue
        with open(os.path.join(srcdir, fn), encoding="utf-8") as fh:
            for name, val in re.findall(r"\b(Kind\w+)\s+Kind\s*=\s*\"([^\"]+)\"", fh.read()):
                values[name] = val
    missing = [i for i in idents if i not in values]
    if missing:
        raise RuntimeError(
            f"cannot resolve {', '.join(missing)} to a kind string under "
            f"{STRUCTREGIME_DIR} — refusing to guess")
    return sorted(values[i] for i in idents)


def check_null_coverage(con: sqlite3.Connection, repo: str) -> dict:
    """(a) Matched-baseline coverage over the rows the amendment governs.

    "The rows the amendment governs" is narrower than "every post-epoch row", and
    this function used to conflate the two. The authoritative definition is the Go
    counter store.UnmatchedNullCount, whose own comment records why: the write
    guard refuses a label-less row only when structuralNullKinds covers its kind,
    while the counter counted EVERY kind — so rows the writer was ENTITLED to write
    were read as proof that "the deployed binary differs from source". On the live
    store that false alarm blocked all structural grading until Go was fixed.

    This Python copy never got that fix, and reproduced the identical false alarm
    one layer up where the consequence is worse: it prints "Publication must be
    refused". Measured 2026-08-09 on the live database — 1,297 post-epoch rows
    carry no naive_label, 1,165 of them frozen in the quarantine manifest, and all
    132 remaining are kind filingsdrift21, which has no null resolver and is
    deliberately outside the guard. Go returned 0; this returned FAIL.

    The predicate is now UnmatchedNullCount's, condition for condition: the kind
    restriction, the superseded_by exclusion, and the frozen quarantine. The
    quarantine is not an escape hatch — it is a fixed, hash-chained set that check
    (c) independently verifies against its digest, its members still grade as
    NO BASELINE, and a new label-less row of a governed kind still fails here.
    """
    try:
        kinds = structural_null_kinds(repo)
    except (OSError, RuntimeError) as e:
        return {"name": "matched-null-baseline", "ok": False,
                "evidence": f"cannot read the write guard's kind set from the Go source "
                            f"({e}) — this check will not guess which kinds it governs"}
    ph = ",".join("?" * len(kinds))
    governed = (f"FROM regime_outcomes o WHERE o.ts >= ? AND o.superseded_by IS NULL "
                f"AND o.kind IN ({ph})")
    unquarantined = " AND o.naive_label IS NULL"
    # Whether the quarantine EXISTS is check (c)'s question, not this one. When
    # the table is absent there is simply nothing exempted, so the clause is
    # dropped rather than raising — otherwise a missing quarantine would fail two
    # checks and this one would report it as a schema error it is not.
    if table_exists(con, QUARANTINE_TABLE):
        unquarantined += (f" AND o.id NOT IN (SELECT outcome_id FROM {QUARANTINE_TABLE})")
    args = [NULL_AMENDMENT_EPOCH_TS, *kinds]
    try:
        total, matched = con.execute(
            "SELECT COUNT(*), COALESCE(SUM(o.naive_label IS NOT NULL), 0) " + governed,
            args).fetchone()
        unmatched = con.execute(
            "SELECT COUNT(*) " + governed + unquarantined, args).fetchone()[0]
    except sqlite3.OperationalError as e:
        return {"name": "matched-null-baseline", "ok": False,
                "evidence": f"regime_outcomes is missing a column this check needs ({e}) — "
                            "the deployed schema predates the null amendment"}
    all_rows = con.execute("SELECT COUNT(*) FROM regime_outcomes").fetchone()[0]
    quarantined = total - matched - unmatched
    cov = (matched / total) if total else None
    ok = unmatched == 0
    ev = (f"naive_label present on {matched}/{total} GOVERNED regime_outcomes rows "
          f"at/after {NULL_AMENDMENT_EPOCH.isoformat()} "
          f"({'n/a' if cov is None else f'{cov * 100:.1f}%'}); "
          f"{quarantined} frozen in the quarantine manifest; {unmatched} unmatched "
          f"outside it; kinds governed: {', '.join(kinds)}; "
          f"{all_rows} rows in the table overall")
    if not ok:
        by_kind = con.execute(
            "SELECT o.kind, COUNT(*) " + governed + unquarantined +
            " GROUP BY o.kind ORDER BY COUNT(*) DESC", args).fetchall()
        ev += (" — " + ", ".join(f"{k}:{n}" for k, n in by_kind) +
               "; the store's write guard (store.NullAmendmentEpoch) refuses such a "
               "row, so the binary that wrote them is not the binary at HEAD")
    return {"name": "matched-null-baseline", "ok": ok, "evidence": ev,
            "measured": {"governed_rows": total, "with_baseline": matched,
                         "quarantined": quarantined,
                         "unmatched_outside_quarantine": unmatched,
                         "coverage": cov, "rows_total": all_rows, "kinds": kinds}}


def check_judgment_ledger(con: sqlite3.Connection) -> dict:
    """(b) The research loop's judgment ledger exists to be read."""
    present = table_exists(con, JUDGMENTS_TABLE)
    n = con.execute(f"SELECT COUNT(*) FROM {JUDGMENTS_TABLE}").fetchone()[0] if present else 0
    ev = (f"table {JUDGMENTS_TABLE} present with {n} row(s)" if present
          else f"table {JUDGMENTS_TABLE} ABSENT — every narrated grid search is "
               "unverifiable, and tools/research_liveness.py has no ledger to hold "
               "the narration against")
    return {"name": "research-loop-judgment-ledger", "ok": present, "evidence": ev,
            "measured": {"present": present, "rows": n}}


def check_quarantine(con: sqlite3.Connection) -> dict:
    """(c) The frozen unmatched-null exemption exists and still matches its digest."""
    have_q = table_exists(con, QUARANTINE_TABLE)
    have_m = table_exists(con, QUARANTINE_MANIFEST_TABLE)
    if not (have_q and have_m):
        missing = [t for t, p in ((QUARANTINE_TABLE, have_q),
                                  (QUARANTINE_MANIFEST_TABLE, have_m)) if not p]
        return {"name": "null-quarantine-manifest", "ok": False,
                "evidence": f"table(s) {', '.join(missing)} ABSENT — the chain's "
                            "null-quarantine record freezes a set the database "
                            "cannot produce, so the exemption is unbounded in practice",
                "measured": {"quarantine_table": have_q, "manifest_table": have_m}}
    row = con.execute(
        f"SELECT digest, n_rows FROM {QUARANTINE_MANIFEST_TABLE} WHERE id = 1").fetchone()
    if row is None:
        return {"name": "null-quarantine-manifest", "ok": False,
                "evidence": "the quarantine tables exist but no manifest row was ever "
                            "frozen — nothing pins the exempt membership",
                "measured": {"quarantine_table": True, "manifest_table": True,
                             "manifest_row": False}}
    stored_digest, stored_n = row
    members = [f"{i}:{s}:{k}:{d}" for i, s, k, d in con.execute(
        f"SELECT outcome_id, symbol_id, kind, day FROM {QUARANTINE_TABLE} "
        "ORDER BY outcome_id")]
    live = quarantine_digest(members)
    ok = live == stored_digest and len(members) == stored_n
    ev = (f"manifest pins {stored_n} row(s) at digest {stored_digest[:12]}…; live "
          f"membership is {len(members)} row(s) hashing {live[:12]}…")
    if not ok:
        ev += " — the exempt set was extended, deleted from, or edited after freezing"
    return {"name": "null-quarantine-manifest", "ok": ok, "evidence": ev,
            "measured": {"manifest_digest": stored_digest, "manifest_n": stored_n,
                         "live_digest": live, "live_n": len(members)}}


def check_deployed_revision(con: sqlite3.Connection, repo: str) -> dict:
    """(d) The revision the running daemon stamped contains the blind-era constant.

    worker_runs proves a daemon is alive and writing; meta's build-revision row is
    the stamp THAT daemon wrote at startup. Read together they name the code that
    produced today's rows — which is the only version whose contents matter, and
    is not HEAD just because HEAD is what a reviewer has open.
    """
    newest = con.execute("SELECT worker, MAX(started_at) FROM worker_runs").fetchone()
    newest_ts = newest[1] if newest else None
    try:
        rev = con.execute("SELECT v FROM meta WHERE k = ?", (BUILD_REV_META_KEY,)).fetchone()
    except sqlite3.OperationalError:
        rev = None
    rev = rev[0] if rev else ""
    if not rev or rev == "unknown":
        return {"name": "deployed-code-revision", "ok": False,
                "evidence": "the running daemon recorded no build revision "
                            f"(meta.{BUILD_REV_META_KEY} absent or 'unknown'), so the code "
                            "that wrote the live rows cannot be resolved at all",
                "measured": {"revision": rev, "newest_worker_run_ts": newest_ts}}
    if rev.endswith("+dirty"):
        return {"name": "deployed-code-revision", "ok": False,
                "evidence": f"the running daemon is a DIRTY build ({rev}) — its contents "
                            "exist in no commit, so no mechanism can be shown to be in it",
                "measured": {"revision": rev, "newest_worker_run_ts": newest_ts}}
    try:
        src = subprocess.run(["git", "show", f"{rev}:{HOLDOUT_PATH}"], cwd=repo,
                             capture_output=True, text=True, check=True).stdout
    except (OSError, subprocess.CalledProcessError) as e:
        return {"name": "deployed-code-revision", "ok": False,
                "evidence": f"cannot read {HOLDOUT_PATH} at the deployed revision {rev} "
                            f"({e}) — a revision this repository cannot resolve is not a "
                            "revision anyone can verify",
                "measured": {"revision": rev, "newest_worker_run_ts": newest_ts}}
    present = HOLDOUT_CONST in src
    ev = (f"deployed revision {rev[:8]} (newest worker_runs start {newest_ts}) "
          f"{'contains' if present else 'DOES NOT contain'} {HOLDOUT_CONST} in {HOLDOUT_PATH}")
    if not present:
        ev += (" — the running binary predates the blind-holdout commitment the "
               "pre-registration chain describes")
    return {"name": "deployed-code-revision", "ok": present, "evidence": ev,
            "measured": {"revision": rev, "newest_worker_run_ts": newest_ts,
                         "holdout_const_present": present}}


def check_daemon_code_drift(con: sqlite3.Connection, repo: str) -> dict:
    """(f) The running daemon contains every COMMITTED change to daemon/ source.

    check_deployed_revision asks whether one named constant is present in the
    deployed revision. That is a real question, and it is not this one: a daemon
    twenty commits stale still contains a constant added thirty commits ago, so
    it answers 'ok' about a process the repository has already moved past.

    Measured 2026-08-16: the daemon had run revision 1cfa982a for 3,086 runs over
    four days while two commits changed 25 files under daemon/ — among them
    internal/health/health.go, whose repair ('16 checks that could not fail') was
    therefore verified in the tree and absent from the process. The health surface
    actually running was the one its own comment describes as calling a worker
    healthy while it ran on time and failed every time. Every deployment gate
    reported ok, because none of them asked this.

    The predicate is not age, it is movement: commits touching the daemon BINARY's
    own source between the deployed revision and HEAD are by definition fixes that
    are not running. Changes under tools/, docs, or research do not implicate the
    binary and are not counted, and neither are *_test.go changes — a test-only
    commit does not alter what the daemon executes, and this gate has a history of
    refusing publication on a false diagnosis (audits/SIGNALDECKFIX_2026-08-12.md).

    Nor does every path under daemon/. That directory is a Go MODULE holding 14
    separate main packages, of which exactly one — cmd/signaldeckd — is the daemon;
    the other 13 (sdmaint, caldiag, prereg-amend, collapsecheck, …) are standalone
    tools run on demand, and nothing imports them. `go list -deps ./cmd/signaldeckd`
    resolves to internal/** and cmd/signaldeckd only, so editing any other command
    cannot change a single byte the daemon executes. Scoping this check at daemon/
    made every such edit demand a rebuild-and-restart that would fix nothing — the
    same false diagnosis the paragraph above was written about, arriving by a
    different door. On a gate whose entire job is catching genuinely undeployed
    fixes, a false positive is not a harmless over-report: it is how the next real
    one gets waved through.
    """
    try:
        rev = con.execute("SELECT v FROM meta WHERE k = ?", (BUILD_REV_META_KEY,)).fetchone()
    except sqlite3.OperationalError:
        rev = None
    rev = (rev[0] if rev else "")
    base = rev[:-len("+dirty")] if rev.endswith("+dirty") else rev
    if not base or base == "unknown":
        return {"name": "daemon-code-drift", "ok": False,
                "evidence": "the running daemon recorded no build revision, so the "
                            "daemon code it is missing cannot be computed at all",
                "measured": {"revision": rev}}
    try:
        files = subprocess.run(["git", "diff", "--name-only", f"{base}..HEAD", "--",
                                *DAEMON_BINARY_PATHS],
                               cwd=repo, capture_output=True, text=True, check=True).stdout
        log = subprocess.run(["git", "log", "--oneline", f"{base}..HEAD", "--",
                              *DAEMON_BINARY_PATHS],
                             cwd=repo, capture_output=True, text=True, check=True).stdout
    except (OSError, subprocess.CalledProcessError) as e:
        return {"name": "daemon-code-drift", "ok": False,
                "evidence": f"cannot compare the deployed revision {base[:8]} against HEAD "
                            f"({e}) — a revision this repository cannot resolve cannot be "
                            "shown to be current either",
                "measured": {"revision": rev}}
    changed = [p for p in files.splitlines() if p.strip()]
    source = [p for p in changed if not p.endswith("_test.go")]
    commits = [c for c in log.splitlines() if c.strip()]
    if not source:
        detail = "no daemon commits between it and HEAD" if not changed else (
            f"the {len(changed)} changed path(s) under daemon/ are all tests")
        return {"name": "daemon-code-drift", "ok": True,
                "evidence": f"deployed revision {base[:8]} runs current daemon source "
                            f"({detail})",
                "measured": {"revision": rev, "commits": 0, "source_files_changed": 0,
                             "test_only_files_changed": len(changed)}}
    named = ", ".join(c.split(" ", 1)[0] for c in commits[:6])
    return {"name": "daemon-code-drift", "ok": False,
            "evidence": (f"the running daemon is revision {base[:8]}, and {len(commits)} "
                         f"commit(s) touching daemon/ have landed since it, changing "
                         f"{len(source)} non-test source file(s) [{named}]. Those fixes are "
                         "verified in the tree and NOT in the running process — rebuild and "
                         "restart the daemon before treating any of them as live"),
            "measured": {"revision": rev, "commits": len(commits),
                         "source_files_changed": len(source),
                         "commit_list": commits[:20], "source_files": source[:40]}}


def newest_boot_ts(con: sqlite3.Connection) -> int | None:
    """The start of the current uptime session, read off worker_runs.

    worker_runs is the only table that records when the daemon was alive, so
    the boot instant is inferred from it: the newest worker start that follows
    a quiet stretch (or the very first row). Every row frozen after that
    instant was written by the binary meta names, which is what makes the
    comparison below a statement about the DEPLOYED code rather than about
    history in general.
    """
    row = con.execute(
        "SELECT MAX(started_at) FROM ("
        "  SELECT started_at, started_at - LAG(started_at) OVER (ORDER BY started_at) AS gap"
        "  FROM worker_runs) WHERE gap IS NULL OR gap >= ?", (BOOT_QUIET_SECS,)).fetchone()
    quiet_boot = row[0] if row else None

    # A REVISION CHANGE IS A BOOT, EXACTLY -- and it beats the quiet-gap guess.
    #
    # The gap heuristic only sees a boot when the fleet went silent for
    # BOOT_QUIET_SECS. Deploys closer together than that are invisible to it, so
    # it keeps pointing at an older boot and the window then spans SEVERAL
    # binaries. Measured 2026-08-12 during a run of rapid deploys: the heuristic
    # returned 18:42 while the running revision had only started writing at
    # 21:10, so twelve binaries' rows sat inside the window and the stamp ratio
    # read 4.7%. The check then reported "the deployed binary is not stamping the
    # rows it writes" -- which was FALSE. Every one of those rows carried the
    # stamp of whichever binary actually wrote it, and the running one was
    # stamping correctly.
    #
    # A row stamped with a DIFFERENT revision is positive evidence that a
    # different binary was running, so the current revision's first row is an
    # exact lower bound on the current uptime session. Take the LATER of the two:
    # the heuristic still covers the case the revision signal cannot see, a
    # redeploy that produces the SAME revision (a rebuild of one commit).
    cur = con.execute(
        "SELECT MIN(started_at) FROM worker_runs WHERE revision = ("
        "  SELECT revision FROM worker_runs WHERE COALESCE(revision,'') <> ''"
        "  ORDER BY started_at DESC LIMIT 1)").fetchone()
    rev_boot = cur[0] if cur and cur[0] is not None else None

    candidates = [t for t in (quiet_boot, rev_boot) if t is not None]
    return max(candidates) if candidates else None


def check_row_revision_stamp(con: sqlite3.Connection) -> dict:
    """(e) The rows written by the RUNNING daemon carry its revision stamp.

    Check (d) reads one meta row and greps one constant in the source at that
    commit. A binary that contains the constant but whose per-row stamping
    regressed passes it with every row unstamped — which is the exact live
    state of this database (0/251110 prediction_ledger rows, 0/3104
    worker_runs rows). "The right code is deployed" and "the deployed code is
    doing the thing" are different claims, and only the second one can be
    checked against the rows a verdict is actually built out of.

    So: for every row frozen at or after the newest daemon boot, the stamp must
    be non-empty AND equal to meta's recorded build revision. The measured
    coverage fraction is printed whether it passes or fails; a missing column,
    an empty stamp, or a stamp naming other code all FAIL. This can only
    suppress a verdict — it writes nothing and relaxes nothing.
    """
    rev = con.execute("SELECT v FROM meta WHERE k = ?", (BUILD_REV_META_KEY,)).fetchone()
    rev = rev[0] if rev else ""
    boot = newest_boot_ts(con)
    measured: dict = {"revision": rev, "boot_ts": boot, "tables": {}}
    if boot is None:
        return {"name": "row-revision-stamp", "ok": False,
                "evidence": "worker_runs holds no rows, so no daemon boot can be located "
                            "and no row can be attributed to the deployed binary",
                "measured": measured}
    if not rev or rev == "unknown":
        return {"name": "row-revision-stamp", "ok": False,
                "evidence": f"meta.{BUILD_REV_META_KEY} is absent or 'unknown', so there is "
                            "no revision for the row stamps to be certified against",
                "measured": measured}
    parts, ok = [], True
    for table, ts_col in STAMPED_TABLES:
        try:
            total, stamped = con.execute(
                f"SELECT COUNT(*), COALESCE(SUM(COALESCE(revision, '') = ?), 0) "
                f"FROM {table} WHERE {ts_col} >= ?", (rev, boot)).fetchone()
        except sqlite3.OperationalError as e:
            measured["tables"][table] = {"error": str(e)}
            parts.append(f"{table}: no revision column ({e})")
            ok = False
            continue
        cov = (stamped / total) if total else None
        measured["tables"][table] = {"post_boot_rows": total, "stamped": stamped,
                                     "coverage": cov}
        parts.append(f"{table} {stamped}/{total}"
                     + ("" if cov is None else f" ({cov * 100:.1f}%)"))
        if total and stamped != total:
            ok = False
    ev = (f"rows frozen at/after the newest daemon boot ({boot}) carrying revision "
          f"{rev[:8]}: " + "; ".join(parts))
    if not ok:
        ev += (" — the deployed binary is not stamping the rows it writes, so no row "
               "here can be attributed to the code the certificate names. Fix by "
               "DEPLOYING a binary that stamps; never by writing the stamp onto "
               "existing rows, which would assert provenance nobody observed")
    return {"name": "row-revision-stamp", "ok": ok, "evidence": ev, "measured": measured}


def run(db: str, repo: str) -> tuple[int, list[dict]]:
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    try:
        checks = [
            check_null_coverage(con, repo),
            check_judgment_ledger(con),
            check_quarantine(con),
            check_deployed_revision(con, repo),
            check_daemon_code_drift(con, repo),
            check_row_revision_stamp(con),
        ]
    finally:
        con.close()
    failed = [c for c in checks if not c["ok"]]
    return (1 if failed else 0), checks


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--repo", default=REPO)
    ap.add_argument("--json", help="write the measured facts to this path")
    args = ap.parse_args()

    if not os.path.exists(args.db):
        print(f"deployment-drift: database {args.db} not found", file=sys.stderr)
        return 2
    try:
        status, checks = run(args.db, args.repo)
    except sqlite3.Error as e:
        print(f"deployment-drift: check could not run: {e}", file=sys.stderr)
        return 2

    print("DEPLOYMENT DRIFT — is each chain-claimed mechanism observable in the live DB?")
    for c in checks:
        print(f"  [{'ok  ' if c['ok'] else 'FAIL'}] {c['name']}: {c['evidence']}")
    if args.json:
        with open(args.json, "w", encoding="utf-8") as f:
            json.dump({"ok": status == 0, "checks": checks}, f, indent=1)
    if status:
        names = ", ".join(c["name"] for c in checks if not c["ok"])
        print(f"\nDEPLOYMENT DRIFT: {names} — claimed in the pre-registration chain, not "
              "observable in the live database. Publication must be refused: the registry "
              "would otherwise grade as though these mechanisms were in force. Fix by "
              "DEPLOYING the code that implements them — never by backfilling the rows or "
              "relaxing the gate.", file=sys.stderr)
    return status


if __name__ == "__main__":
    sys.exit(main())

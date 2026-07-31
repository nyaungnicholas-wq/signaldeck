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


def check_null_coverage(con: sqlite3.Connection) -> dict:
    """(a) Matched-baseline coverage over the rows the amendment governs."""
    try:
        total, matched = con.execute(
            "SELECT COUNT(*), COALESCE(SUM(naive_label IS NOT NULL), 0) "
            "FROM regime_outcomes WHERE ts >= ?", (NULL_AMENDMENT_EPOCH_TS,)).fetchone()
    except sqlite3.OperationalError as e:
        return {"name": "matched-null-baseline", "ok": False,
                "evidence": f"regime_outcomes has no naive_label column at all ({e}) — "
                            "the deployed schema predates the null amendment"}
    all_rows = con.execute("SELECT COUNT(*) FROM regime_outcomes").fetchone()[0]
    cov = (matched / total) if total else None
    ok = total == 0 or matched == total
    ev = (f"naive_label present on {matched}/{total} regime_outcomes rows at/after "
          f"{NULL_AMENDMENT_EPOCH.isoformat()} "
          f"({'n/a' if cov is None else f'{cov * 100:.1f}%'}); "
          f"{all_rows} rows in the table overall")
    if not ok:
        ev += (" — the store's write guard (store.NullAmendmentEpoch) refuses such a "
               "row, so the binary that wrote them is not the binary at HEAD")
    return {"name": "matched-null-baseline", "ok": ok, "evidence": ev,
            "measured": {"post_epoch_rows": total, "with_baseline": matched,
                         "coverage": cov, "rows_total": all_rows}}


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
    return row[0] if row else None


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
            check_null_coverage(con),
            check_judgment_ledger(con),
            check_quarantine(con),
            check_deployed_revision(con, repo),
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

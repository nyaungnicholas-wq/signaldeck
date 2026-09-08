#!/usr/bin/env python3
"""Record whether the accuracy grade actually happened.

WHY THIS EXISTS. On 2026-08-03 the Windows scheduled task for the accuracy
grader reported success -- exit code 0, LastTaskResult 0 -- while the grader had
been refusing for 33 hours. Every signal the operating system offers describes
the WRAPPER: a process started, ran, and exited cleanly. None of them describes
the WORK. Nothing in the database recorded that distinction, so nothing could
alarm on it.

WHAT THIS IS NOT. It does not decide whether the run succeeded. That rule
already exists, once, in ops/accuracy-registry.sh: the grade counts only when
the grader exits 0 AND the registry's `generated` timestamp advances, because
"exit 0 with an unmoved timestamp is the same outage wearing a success code".
Re-deriving that here would give the system two copies of its most important
refusal rule and a way for them to disagree. So the runner decides and this
records.

The daemon reads these rows through store.GraderStale, and /api/accuracy refuses
to publish when the newest one is failed, missing, or older than its window.

Usage, from ops/accuracy-registry.sh:

    python tools/grader_heartbeat.py --success            # after a clean grade
    python tools/grader_heartbeat.py --failure --error "grader exited 3"
    python tools/grader_heartbeat.py --refused --error "publication gate: ..."
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sqlite3
import sys
from datetime import datetime, timezone

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
DEFAULT_DB = os.path.join(REPO, "data", "signaldeck.db")
DEFAULT_REGISTRY = os.path.join(REPO, "data", "accuracy_registry.json")
DEFAULT_GRADER = os.path.join(HERE, "accuracy_registry.py")
TASK = "accuracy_registry"

SCHEMA = """CREATE TABLE IF NOT EXISTS grader_heartbeats (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task TEXT NOT NULL,
  success INTEGER NOT NULL CHECK (success IN (0,1)),
  finished_at TEXT NOT NULL,
  grader_sha256 TEXT,
  rows_evaluated INTEGER,
  error TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))"""


def _utc_now() -> str:
    now = datetime.now(timezone.utc)
    return now.strftime("%Y-%m-%dT%H:%M:%S.") + f"{now.microsecond // 1000:03d}Z"


def _registry_rows(path: str) -> int | None:
    """How many rows the current registry publishes, for context on the row."""
    try:
        with open(path, encoding="utf-8") as f:
            return len(json.load(f).get("rows") or []) or None
    except (OSError, json.JSONDecodeError, AttributeError):
        return None


def _grader_sha256(path: str) -> str:
    """The grader's digest, so a heartbeat names WHICH grader ran."""
    try:
        with open(path, "rb") as f:
            return hashlib.sha256(f.read()).hexdigest()
    except OSError:
        return ""


def write_heartbeat(db: str, success: bool, rows: int | None, sha: str, error: str) -> int:
    """Append one heartbeat row. Returns a process exit code.

    A heartbeat that cannot be written is itself worth reporting: the check that
    watches this table would otherwise read "no heartbeat recorded" and blame
    the grader for what is actually a database problem.
    """
    try:
        con = sqlite3.connect(db, timeout=120)  # the daemon holds long write transactions; 30s lost a heartbeat on 2026-09-08
    except sqlite3.Error as e:
        print(f"grader_heartbeat: cannot open {db}: {e}", file=sys.stderr)
        return 2
    try:
        with con:
            con.execute(SCHEMA)
            con.execute(
                "INSERT INTO grader_heartbeats "
                "(task, success, finished_at, grader_sha256, rows_evaluated, error) "
                "VALUES (?,?,?,?,?,?)",
                (TASK, 1 if success else 0, _utc_now(), sha or None, rows, error or None))
    except sqlite3.Error as e:
        print(f"grader_heartbeat: write failed: {e}", file=sys.stderr)
        return 2
    finally:
        con.close()
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--registry", default=DEFAULT_REGISTRY)
    ap.add_argument("--grader", default=DEFAULT_GRADER)
    mode = ap.add_mutually_exclusive_group(required=True)
    mode.add_argument("--success", action="store_true",
                      help="the runner confirmed a real grade")
    mode.add_argument("--failure", action="store_true",
                      help="the runner refused; --error says why")
    mode.add_argument("--refused", action="store_true",
                      help="the grader RAN and the publication gate refused to publish; recorded as a healthy heartbeat (success=1, error='REFUSED: <reason>') so the health check sees a live grader; the registry envelope, not this row, carries the refusal to /api/accuracy")
    ap.add_argument("--error", default="", help="the runner's refusal reason")
    args = ap.parse_args()

    # A publication refusal is NOT a grader failure — the grader produced a grade and the gate declined to publish it;
    # filing it as success=0 kept ops/check-grader-health.ps1 red for every day of a refusal window (weeks),
    # which is a check nobody reads.
    if (args.failure or args.refused) and not args.error.strip():
        ap.error("--failure / --refused require --error explaining the refusal")

    success = args.success or args.refused
    if args.refused:
        error = f"REFUSED: {args.error.strip()}"
    else:
        error = args.error

    rc = write_heartbeat(args.db, success, _registry_rows(args.registry),
                         _grader_sha256(args.grader), error)
    label = "ok" if args.success else ("ok (publication REFUSED)" if args.refused else "FAILED")
    print(f"grader_heartbeat: recorded {label}" + (f" ({args.error})" if args.error else ""))
    return rc


if __name__ == "__main__":
    sys.exit(main())
"""External timestamping must be measurable, and the check must be able to PASS.

Nothing measured whether ledger anchors ever left this machine. Measured
2026-08-12: ops/anchor-publish.sh had not run since 2026-07-27, while the daemon
kept signing anchors locally (ledger_anchors held 10 rows, newest 2026-08-10).
Local anchors carry none of the third-party-timestamp guarantee the project
claims, and the gap was invisible for 16 days.

These pin the four verdicts. The OK cases matter as much as the failures: a
check that can only go red is one nobody reads (the ops/check-grader-health.ps1
lesson), and one that can only go green measures nothing.
"""

import sqlite3
import subprocess
import sys
import time
from pathlib import Path

TOOL = Path(__file__).resolve().parent / "anchor_liveness.py"


def _db(tmp_path, *, anchors: int, published_hours_ago=None) -> str:
    p = tmp_path / "t.db"
    con = sqlite3.connect(p)
    con.execute("CREATE TABLE ledger_anchors (created_at INTEGER)")
    con.execute("CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT)")
    con.execute("CREATE TABLE dq_events (ts INTEGER, kind TEXT, detail TEXT)")
    now = int(time.time())
    for _ in range(anchors):
        con.execute("INSERT INTO ledger_anchors VALUES (?)", (now - 3600,))
    if published_hours_ago is not None:
        con.execute(
            "INSERT INTO meta VALUES ('anchor_last_published', ?)",
            (str(now - int(published_hours_ago * 3600)),),
        )
    con.commit()
    con.close()
    return str(p)


def _run(db, *extra):
    return subprocess.run(
        [sys.executable, str(TOOL), "--db", db, *extra],
        capture_output=True, text=True,
    )


def test_no_anchors_is_not_a_failure(tmp_path):
    """Nothing signed yet means nothing is owed publication."""
    r = _run(_db(tmp_path, anchors=0), "--json")
    assert r.returncode == 0, r.stdout + r.stderr
    assert '"NO ANCHORS"' in r.stdout


def test_signed_but_never_published_fails(tmp_path):
    """The live 2026-08-12 state: anchors exist only on this machine."""
    r = _run(_db(tmp_path, anchors=3), "--json")
    assert r.returncode == 1, r.stdout + r.stderr
    assert '"NEVER PUBLISHED"' in r.stdout


def test_recent_publish_passes(tmp_path):
    """The check MUST be able to go green, or it measures nothing."""
    r = _run(_db(tmp_path, anchors=3, published_hours_ago=1), "--json")
    assert r.returncode == 0, r.stdout + r.stderr
    assert '"OK"' in r.stdout


def test_stale_publish_fails(tmp_path):
    r = _run(_db(tmp_path, anchors=3, published_hours_ago=100), "--json")
    assert r.returncode == 1, r.stdout + r.stderr
    assert '"STALE"' in r.stdout


def test_unreadable_db_is_inconclusive_not_a_pass(tmp_path):
    """Exit 2, never 0: a check that cannot read must not report success."""
    r = _run(str(tmp_path / "nope.db"))
    assert r.returncode == 2, r.stdout + r.stderr


def test_dq_event_only_on_failure(tmp_path):
    bad = _db(tmp_path, anchors=3)
    _run(bad, "--emit-dq-event")
    rows = sqlite3.connect(bad).execute(
        "SELECT COUNT(*), MAX(kind) FROM dq_events").fetchone()
    assert rows == (1, "anchor_publish_stale"), rows

    good_dir = tmp_path / "good"
    good_dir.mkdir()
    good = _db(good_dir, anchors=3, published_hours_ago=1)
    _run(good, "--emit-dq-event")
    n = sqlite3.connect(good).execute("SELECT COUNT(*) FROM dq_events").fetchone()[0]
    assert n == 0, "a passing check must not write a data-quality event"

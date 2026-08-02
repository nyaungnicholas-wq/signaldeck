"""Check for tools/verify_backup.py.

The defect this pins down (re-audit 2026-08-02, finding F-1): the backup written
2026-08-01 passed `PRAGMA quick_check` and held 14 prediction_ledger rows against
261,164 in the previous one. It also held 3.36M `bars` rows, which sails past
restore-rehearsal.sh's MIN_BARS_ROWS=1000 floor. Structure was fine; content was
gone. A backup verifier that only asks "is this a well-formed SQLite file?"
reports that backup as healthy.

So the assertions here are all about CONTENT, and every fixture is deliberately
structurally valid. A verifier that passes these cannot be a quick_check wrapper.

Run: python tools/test_verify_backup.py
"""

import os
import sqlite3
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from verify_backup import verify  # noqa: E402


def build(path, *, bars=5000, ledger=100, anchor=100, with_anchor_table=True):
    """Write a structurally valid SQLite DB with the given content counts."""
    c = sqlite3.connect(path)
    c.execute("CREATE TABLE bars (id INTEGER PRIMARY KEY, sym TEXT)")
    c.executemany(
        "INSERT INTO bars (sym) VALUES (?)", [("AAPL",)] * bars
    )
    c.execute("CREATE TABLE prediction_ledger (seq INTEGER PRIMARY KEY, h TEXT)")
    c.executemany(
        "INSERT INTO prediction_ledger (h) VALUES (?)", [("deadbeef",)] * ledger
    )
    if with_anchor_table:
        c.execute(
            "CREATE TABLE ledger_anchors "
            "(seq INTEGER PRIMARY KEY, ledger_count INTEGER, created_at INTEGER)"
        )
        if anchor is not None:
            c.execute(
                "INSERT INTO ledger_anchors (ledger_count, created_at) VALUES (?, ?)",
                (anchor, 1785555294),
            )
    c.commit()
    c.close()
    # Every fixture must be structurally sound, or the test proves nothing.
    c = sqlite3.connect(path)
    assert c.execute("PRAGMA quick_check").fetchone()[0] == "ok"
    c.close()
    return path


def main():
    tmp = tempfile.mkdtemp(prefix="sd_backup_check_")
    p = lambda n: os.path.join(tmp, n)  # noqa: E731

    # 1. A healthy backup passes.
    ok, problems = verify(build(p("good.db")))
    assert ok, f"healthy backup rejected: {problems}"

    # 2. THE REGRESSION. Ledger gutted to 14 rows while its own newest anchor
    #    still commits to 100. Bars are far above any sane floor, and
    #    quick_check is ok. This is the Aug 1 file in miniature.
    ok, problems = verify(build(p("gutted.db"), bars=3_361_790 // 1000, ledger=14, anchor=100))
    assert not ok, "gutted ledger accepted — this is exactly finding F-1"
    assert any("ledger" in s.lower() for s in problems), problems

    # 3. Ledger gutted AND the anchor rows gone with it, so the file cannot
    #    incriminate itself. Must still fail: a backup of a system that anchors
    #    its ledger has no business arriving with zero anchors.
    ok, problems = verify(build(p("noanchor.db"), ledger=14, anchor=None))
    assert not ok, "missing anchors accepted"
    assert any("anchor" in s.lower() for s in problems), problems

    # 4. Truncated bars still caught (the one thing the old floor did do).
    ok, problems = verify(build(p("tinybars.db"), bars=10))
    assert not ok, "truncated bars accepted"
    assert any("bars" in s.lower() for s in problems), problems

    # 5. An empty ledger is not excused by an empty anchor table.
    ok, problems = verify(build(p("empty.db"), ledger=0, anchor=None))
    assert not ok, "empty ledger accepted"

    # 6. A backup may legitimately have MORE ledger rows than its newest anchor
    #    (rows written after the last anchor). That must not be an error.
    ok, problems = verify(build(p("ahead.db"), ledger=140, anchor=100))
    assert ok, f"ledger ahead of anchor wrongly rejected: {problems}"

    # 7. A file that is not a database at all fails rather than raising.
    bad = p("garbage.db")
    with open(bad, "wb") as f:
        f.write(b"this is not a sqlite file")
    ok, problems = verify(bad)
    assert not ok, "non-database accepted"

    # 8. A missing file fails rather than raising.
    ok, problems = verify(p("does_not_exist.db"))
    assert not ok, "missing file accepted"

    print("ALL BACKUP VERIFY CHECKS PASSED")


if __name__ == "__main__":
    main()

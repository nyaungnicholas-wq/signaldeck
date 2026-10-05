"""Checks for verify_backup.py's LIVE-DB STALENESS comparison.

Separate file from test_verify_backup.py on purpose. That file exposes a main()
rather than a TestCase, and ops/selfimprove-loop.ps1 branches on exactly that:
it runs `python -m unittest <mod>` when the source matches `class \\w+\\(.*TestCase`
and `python <file>` otherwise. Adding a TestCase to that file would flip it to
the unittest branch and silently stop running its main() checks. A new file
keeps both, and this one is visible to all three runners — pytest, CI's
`python3 -m unittest discover -s tools -p 'test_*.py'`, and the selfimprove loop.

The defect pinned here: the live comparison used to end in
`except Exception: pass`, so any failure to read the live DB skipped the check
and left `problems` empty — the backup then reported VERIFIED without ever
having been compared. ops/signaldeck-backup-offline.sh passes --live on every
run, and the live DB is a multi-GB SQLite under continuous write load, so
"could not read it" is an ordinary outcome rather than a rare edge.
"""

import os
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from verify_backup import verify  # noqa: E402


def build_db(path, *, bars=2000, ledger=100, anchor=100):
    """A structurally valid backup: every non-staleness check must pass, so a
    failure in these tests can only come from the staleness comparison."""
    c = sqlite3.connect(path)
    c.execute("CREATE TABLE bars (id INTEGER PRIMARY KEY, sym TEXT)")
    c.executemany("INSERT INTO bars (sym) VALUES (?)", [("AAPL",)] * bars)
    c.execute("CREATE TABLE prediction_ledger (id INTEGER PRIMARY KEY)")
    c.executemany("INSERT INTO prediction_ledger DEFAULT VALUES", [()] * ledger)
    c.execute("CREATE TABLE ledger_anchors (seq INTEGER PRIMARY KEY, ledger_count INTEGER)")
    c.execute("INSERT INTO ledger_anchors (ledger_count) VALUES (?)", (anchor,))
    c.commit()
    c.close()


class VerifyBackupStalenessTest(unittest.TestCase):
    def test_missing_live_db_is_a_problem_not_a_skip(self):
        """A REQUESTED check that cannot run must be reported. Skipping it
        silently is how a never-compared backup reports VERIFIED."""
        with tempfile.TemporaryDirectory() as d:
            backup = os.path.join(d, "backup.db")
            build_db(backup)
            ok, problems = verify(backup, live_db=os.path.join(d, "nope.db"))
            self.assertFalse(ok, f"missing live db must fail closed; problems={problems}")
            self.assertTrue(
                any("missing" in p for p in problems),
                f"expected a problem naming the missing live db; problems={problems}",
            )

    def test_unreadable_live_db_is_a_problem_not_a_skip(self):
        """A corrupt or locked live DB must surface. This is the branch that
        used to be swallowed, and it is the likely one in production."""
        with tempfile.TemporaryDirectory() as d:
            backup = os.path.join(d, "backup.db")
            build_db(backup)
            live = os.path.join(d, "live.db")
            with open(live, "wb") as fh:
                fh.write(b"not a database")
            ok, problems = verify(backup, live_db=live)
            self.assertFalse(ok, f"unreadable live db must fail closed; problems={problems}")
            self.assertTrue(
                any("could not run" in p for p in problems),
                f"expected a problem saying the staleness check could not run; problems={problems}",
            )

    def test_stale_backup_is_detected(self):
        """The 2026-08-01 defect: a structurally perfect backup holding a
        fraction of the rows. quick_check passes; only this comparison catches it."""
        with tempfile.TemporaryDirectory() as d:
            backup = os.path.join(d, "backup.db")
            build_db(backup, ledger=10, anchor=10)
            live = os.path.join(d, "live.db")
            build_db(live, ledger=1000, anchor=1000)
            ok, problems = verify(backup, live_db=live)
            self.assertFalse(ok, f"stale backup must fail; problems={problems}")
            self.assertTrue(
                any("too stale" in p for p in problems),
                f"expected a staleness problem; problems={problems}",
            )

    def test_fresh_backup_passes(self):
        """It must not fire on a good backup. A check that cries wolf is a
        check that gets ignored, which is the same outcome as not having one."""
        with tempfile.TemporaryDirectory() as d:
            backup = os.path.join(d, "backup.db")
            build_db(backup, ledger=1000, anchor=1000)
            live = os.path.join(d, "live.db")
            build_db(live, ledger=1000, anchor=1000)
            ok, problems = verify(backup, live_db=live)
            self.assertTrue(ok, f"fresh backup must pass; problems={problems}")
            self.assertEqual(problems, [], f"expected no problems; got {problems}")

    def test_no_live_db_requested_still_passes(self):
        """"Not requested" must stay distinct from "requested but failed".
        Only the latter is a problem; conflating them would make --live
        mandatory by accident."""
        with tempfile.TemporaryDirectory() as d:
            backup = os.path.join(d, "backup.db")
            build_db(backup)
            ok, problems = verify(backup)
            self.assertTrue(ok, f"no live db requested must still pass; problems={problems}")


if __name__ == "__main__":
    unittest.main()

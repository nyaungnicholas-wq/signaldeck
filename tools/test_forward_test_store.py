#!/usr/bin/env python3
"""store() must never lose a computed session to a lock, and must never
pretend it wrote one.

On 2026-09-02 the forward test computed its first eligible session ever
(2026-08-31, n=19, excess -0.247%) and lost it: sqlite3 raised "database is
locked", store() propagated, and the process exited before --verdict, so
nothing was recorded and the run reported failure with the session gone.
These three tests pin the three behaviours that fix requires.
"""
import sqlite3
import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import forward_test as ft


ROW = {"session": "2026-08-31", "n_bets": 55, "book_pct": -1.372,
       "bench_pct": -1.113, "excess_pct": -0.259, "eligible": 1}


class StoreRetry(unittest.TestCase):
    def setUp(self):
        self.conn = sqlite3.connect(":memory:")
        self.addCleanup(self.conn.close)

    def test_retries_a_lock_then_succeeds(self):
        """A lock that clears must end with the row written, not an exception."""
        real = ft._store_once
        calls = {"n": 0}

        def flaky(conn, rows):
            calls["n"] += 1
            if calls["n"] < 3:
                raise sqlite3.OperationalError("database is locked")
            return real(conn, rows)

        with mock.patch.object(ft, "_store_once", flaky), \
             mock.patch.object(ft.time, "sleep") as slept:
            ft.store(self.conn, [ROW])

        self.assertEqual(calls["n"], 3, "should have retried twice then written")
        self.assertTrue(slept.called, "must back off between attempts")
        stored = list(self.conn.execute(
            "SELECT session, n_bets, eligible FROM forward_test_daily"))
        self.assertEqual(stored, [("2026-08-31", 55, 1)])

    def test_exhausted_lock_raises_loudly_and_names_the_loss(self):
        """Giving up must be LOUD. A silent return is the original bug."""
        with mock.patch.object(ft, "_store_once",
                               side_effect=sqlite3.OperationalError("database is locked")), \
             mock.patch.object(ft.time, "sleep"):
            with self.assertRaises(SystemExit) as cm:
                ft.store(self.conn, [ROW])
        msg = str(cm.exception)
        self.assertIn("1 computed session", msg)
        self.assertIn("DISCARDED", msg)

    def test_non_lock_error_is_not_retried(self):
        """A schema error is not a lock; retrying it just hides it."""
        calls = {"n": 0}

        def boom(conn, rows):
            calls["n"] += 1
            raise sqlite3.OperationalError("no such column: banana")

        with mock.patch.object(ft, "_store_once", boom), \
             mock.patch.object(ft.time, "sleep"):
            with self.assertRaises(sqlite3.OperationalError):
                ft.store(self.conn, [ROW])
        self.assertEqual(calls["n"], 1, "a non-lock error must propagate at once")

    def test_busy_timeout_is_generous_enough_to_outlast_a_write_burst(self):
        self.assertGreaterEqual(ft.WRITE_BUSY_TIMEOUT_MS, 60_000)


if __name__ == "__main__":
    unittest.main()

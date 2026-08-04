"""Acceptance check for tools/structural_liveness.py.

The property under test is narrow and specific: an unresolved structural outcome
that has passed BOTH resolution gates is evidence the resolver stopped working,
and today nothing distinguishes that from correctly waiting — both print
"resolved 0".

The hard part is not detecting overdue rows. It is not crying wolf about the two
legitimate reasons a row stays unresolved forever:
  * a DEGENERATE window (tied median / NaN) that the worker declines to grade,
  * a QUARANTINED row with no frozen naive baseline, which is ungradable by
    doctrine.
A check that fires on those is noise, and noise is how a real outage gets
ignored. So the tests below spend most of their effort on the false-positive
side.
"""
import os
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import structural_liveness as sl  # noqa: E402

DAY = 86400
NOW = 1_800_000_000          # fixed clock; every fixture is relative to it
H = 21                       # horizon days used throughout


def _db(path=None):
    con = sqlite3.connect(path or ":memory:")
    con.executescript("""
        CREATE TABLE regime_outcomes (
            id INTEGER PRIMARY KEY, symbol_id INTEGER, kind TEXT, ts INTEGER,
            day INTEGER, horizon_days INTEGER, regime TEXT, conviction REAL,
            historical_accuracy REAL, rank INTEGER, resolved_at INTEGER,
            actual TEXT, correct INTEGER, naive_label TEXT, revision TEXT,
            basis_epoch INTEGER);
        CREATE TABLE regime_outcome_quarantine (
            outcome_id INTEGER, symbol_id INTEGER, kind TEXT, day INTEGER,
            frozen_ts INTEGER);
        CREATE TABLE bars (
            symbol_id INTEGER, tf TEXT, ts INTEGER, open REAL, high REAL,
            low REAL, close REAL, volume REAL);
    """)
    return con


def _call(con, oid, kind="trend21", ts=None, horizon=H, resolved=False,
          symbol_id=1, naive="downtrend"):
    """Freeze one outcome. Default ts is old enough to clear the calendar gate."""
    if ts is None:
        ts = NOW - int(horizon * 1.45 * DAY) - 10 * DAY
    con.execute(
        "INSERT INTO regime_outcomes (id, symbol_id, kind, ts, day, horizon_days,"
        " regime, conviction, resolved_at, naive_label) VALUES (?,?,?,?,?,?,?,?,?,?)",
        (oid, symbol_id, kind, ts, ts // DAY, horizon, "uptrend", 0.9,
         (ts + horizon * DAY) if resolved else None, naive))
    return ts


def _bars(con, symbol_id, after_ts, n):
    """n daily bars strictly after after_ts.

    Call this ONCE per symbol. Calling it per outcome in a loop stacks duplicate
    timestamps and inflates the forward-bar count, which silently opens the very
    gate a 'still waiting' fixture is trying to hold shut.
    """
    con.executemany(
        "INSERT INTO bars (symbol_id, tf, ts, close, volume) VALUES (?,'1d',?,1.0,1.0)",
        [(symbol_id, after_ts + (i + 1) * DAY) for i in range(n)])


class TestOverdueDetection(unittest.TestCase):
    def test_row_past_both_gates_is_overdue(self):
        con = _db()
        ts = _call(con, 1)
        _bars(con, 1, ts, H + 5)
        self.assertEqual([r["id"] for r in sl.overdue_rows(con, NOW)], [1])

    def test_calendar_gate_not_yet_passed_is_not_overdue(self):
        con = _db()
        ts = _call(con, 1, ts=NOW - 5 * DAY)
        _bars(con, 1, ts, H + 5)      # bars exist, calendar does not
        self.assertEqual(sl.overdue_rows(con, NOW), [])

    def test_bar_gate_not_yet_passed_is_not_overdue(self):
        """The live case today: calendar elapsed, forward bars have not printed."""
        con = _db()
        ts = _call(con, 1)
        _bars(con, 1, ts, H - 10)
        self.assertEqual(sl.overdue_rows(con, NOW), [])

    def test_resolved_row_is_never_overdue(self):
        con = _db()
        ts = _call(con, 1, resolved=True)
        _bars(con, 1, ts, H + 5)
        self.assertEqual(sl.overdue_rows(con, NOW), [])

    def test_grace_period_suppresses_a_row_that_just_became_due(self):
        """The worker runs every 6h. A row due ten minutes ago is not an outage."""
        con = _db()
        ts = NOW - int(H * 1.45 * DAY) - 600
        _call(con, 1, ts=ts)
        _bars(con, 1, ts, H + 5)
        self.assertEqual(sl.overdue_rows(con, NOW), [])
        # ...but the same row well past the grace window is.
        self.assertEqual(
            [r["id"] for r in sl.overdue_rows(con, NOW + sl.GRACE_DAYS * DAY + DAY)],
            [1])

    def test_bars_before_the_call_do_not_count(self):
        """Forward bars means AFTER the call. History must not satisfy the gate."""
        con = _db()
        ts = _call(con, 1)
        con.executemany(
            "INSERT INTO bars (symbol_id, tf, ts, close, volume) VALUES (?,'1d',?,1.0,1.0)",
            [(1, ts - (i + 1) * DAY) for i in range(100)])
        self.assertEqual(sl.overdue_rows(con, NOW), [])

    def test_intraday_bars_do_not_count(self):
        con = _db()
        ts = _call(con, 1)
        con.executemany(
            "INSERT INTO bars (symbol_id, tf, ts, close, volume) VALUES (?,'1h',?,1.0,1.0)",
            [(1, ts + (i + 1) * 3600) for i in range(500)])
        self.assertEqual(sl.overdue_rows(con, NOW), [])

    def test_bars_of_another_symbol_do_not_count(self):
        con = _db()
        ts = _call(con, 1, symbol_id=7)
        _bars(con, 99, ts, H + 5)
        self.assertEqual(sl.overdue_rows(con, NOW), [])


class TestFalsePositives(unittest.TestCase):
    """The reasons a row may legitimately never resolve."""

    def test_quarantined_rows_are_excluded(self):
        con = _db()
        ts = _call(con, 1)
        _bars(con, 1, ts, H + 5)
        con.execute("INSERT INTO regime_outcome_quarantine (outcome_id) VALUES (1)")
        self.assertEqual(sl.overdue_rows(con, NOW), [])

    def test_missing_quarantine_table_is_tolerated(self):
        """A database predating the quarantine migration must still be checkable."""
        con = _db()
        con.execute("DROP TABLE regime_outcome_quarantine")
        ts = _call(con, 1)
        _bars(con, 1, ts, H + 5)
        self.assertEqual([r["id"] for r in sl.overdue_rows(con, NOW)], [1])

    def test_degenerate_tail_alongside_healthy_grading_is_not_an_outage(self):
        """A resolver that graded 50 and declined 2 tied windows is WORKING.

        Failing here would fire forever on an honest non-grade, and a check that
        is always red is a check nobody reads.
        """
        con = _db()
        ts = _call(con, 1, resolved=True)
        _bars(con, 1, ts, H + 5)
        for i in range(1, 50):
            _call(con, i + 1, resolved=True)
        for i in (100, 101):
            _call(con, i)
        st = sl.per_kind_status(con, NOW)["trend21"]
        self.assertEqual(st["verdict"], "OK")

    def test_dead_arm_is_an_outage(self):
        """Overdue rows and NOTHING ever resolved for that kind: the arm is dead."""
        con = _db()
        ts = _call(con, 1)
        _bars(con, 1, ts, H + 5)
        for i in range(1, 30):
            _call(con, i + 1)
        st = sl.per_kind_status(con, NOW)["trend21"]
        self.assertEqual(st["verdict"], "DEAD-ARM")

    def test_mostly_ungraded_is_an_outage_even_with_a_few_resolved(self):
        """A resolver grading 2 of 40 is not 'a degenerate tail'."""
        con = _db()
        ts = _call(con, 1, resolved=True)
        _bars(con, 1, ts, H + 5)
        _call(con, 2, resolved=True)
        for i in range(38):
            _call(con, 100 + i)
        st = sl.per_kind_status(con, NOW)["trend21"]
        self.assertEqual(st["verdict"], "STALLED")

    def test_kind_with_nothing_due_yet_is_waiting_not_broken(self):
        """Today's real state for every structural kind."""
        con = _db()
        ts = _call(con, 1)
        _bars(con, 1, ts, H - 15)
        for i in range(1, 20):
            _call(con, i + 1)
        st = sl.per_kind_status(con, NOW)["trend21"]
        self.assertEqual(st["verdict"], "WAITING")
        self.assertEqual(st["overdue"], 0)


class TestExitCodes(unittest.TestCase):
    def test_healthy_database_exits_zero(self):
        path = os.path.join(tempfile.mkdtemp(), "ok.db")
        con = _db(path)
        ts = _call(con, 1)
        _bars(con, 1, ts, H - 15)
        for i in range(1, 20):
            _call(con, i + 1)
        con.commit()
        con.close()
        self.assertEqual(sl.main(["--db", path, "--now", str(NOW)]), 0)

    def test_dead_arm_exits_one(self):
        path = os.path.join(tempfile.mkdtemp(), "dead.db")
        con = _db(path)
        ts = _call(con, 1)
        _bars(con, 1, ts, H + 5)
        for i in range(1, 30):
            _call(con, i + 1)
        con.commit()
        con.close()
        self.assertEqual(sl.main(["--db", path, "--now", str(NOW)]), 1)

    def test_missing_database_exits_two(self):
        self.assertEqual(sl.main(["--db", "/nonexistent/nope.db"]), 2)

    def test_json_mode_returns_the_same_exit_code_as_table_mode(self):
        """The exit code must come from the data, not from the render path.

        Accumulating `failing` inside the table-printing loop makes --json
        either crash or silently return success, which would turn the CI hook
        into a no-op exactly when it matters.
        """
        path = os.path.join(tempfile.mkdtemp(), "json.db")
        con = _db(path)
        ts = _call(con, 1)
        _bars(con, 1, ts, H + 5)
        for i in range(1, 30):
            _call(con, i + 1)
        con.commit()
        con.close()
        for argv in (["--db", path, "--now", str(NOW)],
                     ["--db", path, "--now", str(NOW), "--json"]):
            self.assertEqual(sl.main(argv), 1, argv)

    def test_json_mode_is_valid_json(self):
        import io
        import json as _json
        from contextlib import redirect_stdout

        path = os.path.join(tempfile.mkdtemp(), "j2.db")
        con = _db(path)
        _call(con, 1)
        con.commit()
        con.close()
        buf = io.StringIO()
        with redirect_stdout(buf):
            sl.main(["--db", path, "--now", str(NOW), "--json"])
        self.assertIn("kinds", _json.loads(buf.getvalue()))

    def test_never_writes_to_the_database(self):
        """This is a check, not a repair. It must not touch a single row."""
        path = os.path.join(tempfile.mkdtemp(), "ro.db")
        con = _db(path)
        ts = _call(con, 1)
        _bars(con, 1, ts, H + 5)
        for i in range(1, 30):
            _call(con, i + 1)
        con.commit()
        con.close()
        before = open(path, "rb").read()
        sl.main(["--db", path, "--now", str(NOW)])
        self.assertEqual(open(path, "rb").read(), before)

    def test_opens_database_read_only(self):
        src = open(os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                "structural_liveness.py"), encoding="utf-8").read()
        self.assertIn("mode=ro", src)


if __name__ == "__main__":
    unittest.main()

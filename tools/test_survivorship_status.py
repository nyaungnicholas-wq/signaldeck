#!/usr/bin/env python3
"""Tests for tools/survivorship_status.py -- survivorship-status v1 (2026-10-23).

WHY. The pinned grader (tools/accuracy_registry.py, never edited) counts a graded
symbol with active=0 and no delisted_at as "listing status unresolvable", and
symbols.active is the lean WATCH flag, so that count is mostly still-trading
names outside the watch set. The new measure sits beside the registry and keeps
watch status, listing evidence and effective-dated exits apart. These tests pin:
  1. every class on a fixture whose answers are known by construction,
     including a conflicting stamp that must be REPORTED, not resolved;
  2. no look-ahead at a historical as_of: a later exit, a later SEC row and
     the current watch flag are all invisible;
  3. the registry-rule cross-tab equals the pinned registry's own count;
  4. the tool never writes to the database;
  5. ops/accuracy-registry.sh runs it after the pinned grade, and its failure
     neither blocks the grade nor leaves a stale file behind.

Run: python -m unittest discover -s tools -p 'test_*.py'
"""
import datetime as dt
import hashlib
import json
import os
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import accuracy_registry  # noqa: E402
import survivorship_status as ss  # noqa: E402

def _sha(path):
    with open(path, "rb") as fh:
        return hashlib.sha256(fh.read()).hexdigest()


def T(y, m, d, h=0):
    return int(dt.datetime(y, m, d, h, tzinfo=dt.timezone.utc).timestamp())

WEEKDAYS = []
d = dt.date(2026, 9, 1)
while d <= dt.date(2026, 10, 22):
    if d.weekday() < 5:
        WEEKDAYS.append(d)
    d += dt.timedelta(days=1)

AS_OF = dt.date(2026, 10, 22)
ADDED_AT = T(2026, 1, 1)

def build_db(path):
    con = sqlite3.connect(path)
    con.executescript("""
        CREATE TABLE symbols(id INTEGER PRIMARY KEY, symbol TEXT, market TEXT, name TEXT, active INTEGER, added_at INTEGER, stream INTEGER DEFAULT 0, delisted_at INTEGER);
        CREATE TABLE bars(symbol_id INTEGER, tf TEXT, ts INTEGER, close REAL, volume REAL);
        CREATE TABLE companies(cik INTEGER, ticker TEXT, name TEXT, exchange TEXT, sic TEXT, sic_desc TEXT, updated_ts INTEGER);
        CREATE TABLE prediction_outcomes(symbol_id INTEGER, horizon TEXT, ts INTEGER, up INTEGER, resolved_at INTEGER);
        CREATE TABLE regime_outcomes(symbol_id INTEGER, kind TEXT, ts INTEGER, correct INTEGER, resolved_at INTEGER);
    """)
    syms = [
        (1, 'SPY', 'stocks', 'SPY', 1, ADDED_AT, 0, None),
        (2, 'WAT', 'stocks', 'WAT', 1, ADDED_AT, 0, None),
        (3, 'UNW', 'stocks', 'UNW', 0, ADDED_AT, 0, None),
        (4, 'GONE', 'stocks', 'GONE', 0, ADDED_AT, 0, T(2026, 9, 10)),
        (5, 'SILENT', 'stocks', 'SILENT', 0, ADDED_AT, 0, None),
        (6, 'NOBAR', 'stocks', 'NOBAR', 0, ADDED_AT, 0, None),
        (7, 'CONF', 'stocks', 'Conf Corp', 0, ADDED_AT, 0, T(2026, 9, 10)),
        (8, 'PRINT', 'stocks', 'PRINT', 0, ADDED_AT, 0, T(2026, 9, 10)),
        (9, 'BTC/USD', 'crypto', 'BTC/USD', 0, ADDED_AT, 0, T(2026, 10, 22)),
        (10, 'LATE', 'stocks', 'LATE', 0, ADDED_AT, 0, T(2026, 10, 15)),
        (11, 'ATC.220816', 'stocks', 'ATC pre-2022-08-16 listing (delisted; ticker later reused)', 0, ADDED_AT, 0, T(2022, 8, 16)),
        (12, 'OTCX', 'stocks', 'OTCX', 0, ADDED_AT, 0, T(2026, 9, 10)),
    ]
    con.executemany("INSERT INTO symbols VALUES (?,?,?,?,?,?,?,?)", syms)
    for sid, sym, *_ in syms:
        if sid in (1, 2, 3, 8, 9):
            days = WEEKDAYS
        elif sid == 4:
            days = [d for d in WEEKDAYS if d <= dt.date(2026, 9, 10)]
        elif sid == 5:
            days = [d for d in WEEKDAYS if d <= dt.date(2026, 9, 15)]
        elif sid in (7, 12):
            days = [d for d in WEEKDAYS if d <= dt.date(2026, 9, 10)]
        elif sid == 10:
            days = [d for d in WEEKDAYS if d <= dt.date(2026, 10, 15)]
        else:
            days = []  # NOBAR and the archived ATC row never print
        for d in days:
            con.execute("INSERT INTO bars VALUES (?, '1d', ?, 100.0, 1000.0)", (sid, T(d.year, d.month, d.day, 4)))
    con.executemany("INSERT INTO companies VALUES (?,?,?,?,?,?,?)", [
        (1, 'CONF', 'Conf Corp', 'NYSE', None, None, T(2026, 10, 1)),
        (2, 'OTCX', 'OTCX', 'OTC', None, None, T(2026, 10, 1)),
    ])
    epoch_pred = accuracy_registry.population_epoch_ts('prediction_outcomes')
    epoch_reg = accuracy_registry.population_epoch_ts('regime_outcomes')
    for sid in range(1, 11):
        con.execute("INSERT INTO prediction_outcomes VALUES (?, '1d', ?, 1, ?)", (sid, T(2026, 10, 6, 14), T(2026, 10, 7, 14)))
        con.execute("INSERT INTO regime_outcomes VALUES (?, 'trend21', ?, 1, ?)", (sid, T(2026, 8, 3, 14), T(2026, 8, 20, 14)))
    con.execute("INSERT INTO prediction_outcomes VALUES (11, '1d', ?, 1, ?)", (T(2026, 9, 1, 14), T(2026, 9, 2, 14)))
    con.commit()
    con.close()

class TestSurvivorshipStatus(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.db = os.path.join(self.tmp, 'test.db')
        build_db(self.db)
        self.con = ss.connect_ro(self.db)

    def tearDown(self):
        self.con.close()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_current_universe_counts(self):
        r = ss.evaluate(self.con, AS_OF, sessions=5, today=AS_OF)
        u = r['scopes']['universe']
        self.assertEqual(u['counts'], {
            'RETIRED_SOURCE': 1, 'UNKNOWN': 4, 'EXITED_DATED': 4,
            'WATCHED': 2, 'UNWATCHED_TRADING': 1, 'TRADING': 0
        })

    def test_unknown_reasons_name_the_evidence(self):
        r = ss.evaluate(self.con, AS_OF, sessions=5, today=AS_OF)
        unk = {u['symbol']: u for u in r['scopes']['universe']['unknown']}
        self.assertIn('SEC companies maps CONF to NYSE', ' '.join(unk['CONF']['reasons']))
        self.assertIn('dated after the exit day', ' '.join(unk['PRINT']['reasons']))
        self.assertIn('no 1d bar since 2026-09-15', ' '.join(unk['SILENT']['reasons']))
        self.assertIn('no 1d bar at or before as_of', ' '.join(unk['NOBAR']['reasons']))

    def test_exit_kinds(self):
        r = ss.evaluate(self.con, AS_OF, sessions=5, today=AS_OF)
        self.assertEqual(r['scopes']['universe']['exit_kinds'],
                         {'source_retired': 1, 'unattributed': 2, 'ticker_reuse_archive': 1, 'otc_after_exit': 1})

    def test_historical_as_of_hides_later_exits_and_the_watch_flag(self):
        r = ss.evaluate(self.con, dt.date(2026, 10, 10), sessions=5, today=AS_OF)
        self.assertEqual(r['mode'], 'historical')
        u = r['scopes']['universe']
        self.assertEqual(u['counts'], {
            'RETIRED_SOURCE': 0, 'UNKNOWN': 4, 'EXITED_DATED': 3,
            'WATCHED': 0, 'UNWATCHED_TRADING': 0, 'TRADING': 5
        })
        self.assertEqual(u['watch'], {'not_point_in_time': 12})

    def test_historical_sec_row_after_as_of_is_not_evidence(self):
        con_w = sqlite3.connect(self.db)
        con_w.execute("UPDATE companies SET updated_ts=? WHERE ticker='CONF'", (T(2026, 10, 20),))
        con_w.commit()
        con_w.close()
        r = ss.evaluate(self.con, dt.date(2026, 10, 10), sessions=5, today=AS_OF)
        u = r['scopes']['universe']
        self.assertEqual(u['counts']['EXITED_DATED'], 4)
        self.assertEqual(u['counts']['UNKNOWN'], 3)

    def test_graded_population_follows_the_registry_epochs(self):
        r = ss.evaluate(self.con, AS_OF, sessions=5, today=AS_OF)
        self.assertEqual(r['scopes']['graded:prediction_outcomes']['symbols'], 10)
        self.assertEqual(r['scopes']['graded:regime_outcomes']['symbols'], 10)
        r2 = ss.evaluate(self.con, dt.date(2026, 10, 5), sessions=5, today=AS_OF)
        self.assertEqual(r2['scopes']['graded:prediction_outcomes']['symbols'], 0)

    def test_registry_rule_crosstab_matches_the_pinned_registry(self):
        r = ss.evaluate(self.con, AS_OF, sessions=5, today=AS_OF)
        for table in ('prediction_outcomes', 'regime_outcomes'):
            m = accuracy_registry.measure_universe_completeness(self.con, table)
            reg = r['scopes']['graded:' + table]['registry_v1_inactive_undated']
            self.assertEqual(reg['total'], m['symbols_inactive_undated'])
        self.assertEqual(r['scopes']['graded:prediction_outcomes']['registry_v1_inactive_undated']['total'], 3)
        self.assertEqual(r['scopes']['graded:prediction_outcomes']['registry_v1_inactive_undated']['by_class'],
                         {'UNWATCHED_TRADING': 1, 'UNKNOWN': 2})

    def test_never_writes_the_database(self):
        before = _sha(self.db)
        out = os.path.join(self.tmp, 'report.json')
        rc = subprocess.run([sys.executable, os.path.join(HERE, 'survivorship_status.py'),
                             '--db', self.db, '--as-of', '2026-10-10', '--json', out], capture_output=True)
        self.assertEqual(rc.returncode, 0)
        after = _sha(self.db)
        self.assertEqual(before, after)
        self.assertFalse(os.path.exists(self.db + '-wal'))
        self.assertFalse(os.path.exists(self.db + '-journal'))
        with open(out) as f:
            self.assertEqual(json.load(f)['measure_id'], 'survivorship-status v1 (2026-10-23)')
        with self.assertRaises(sqlite3.Error):
            self.con.execute("CREATE TABLE x(a)")

    def test_future_as_of_is_refused(self):
        with self.assertRaises(ValueError):
            ss.evaluate(self.con, dt.date(2026, 10, 23), today=AS_OF)

    def test_session_window_is_counted_in_sessions(self):
        r = ss.evaluate(self.con, AS_OF, sessions=5, today=AS_OF)
        self.assertEqual(r['session_calendar'], {'source': 'SPY 1d bars', 'cutoff_day': '2026-10-16'})

    def test_watch_status_is_kept_apart_from_a_dated_exit(self):
        """A held name can be watched AND exited (CRNX, MVO live): the exit wins
        the class, the watch flag is still counted as watched."""
        con_w = sqlite3.connect(self.db)
        con_w.execute("INSERT INTO symbols VALUES (13, 'HELD', 'stocks', 'HELD', 1, ?, 0, ?)",
                      (ADDED_AT, T(2026, 9, 10)))
        con_w.commit()
        con_w.close()
        u = ss.evaluate(self.con, AS_OF, sessions=5, today=AS_OF)['scopes']['universe']
        self.assertEqual(u['counts']['EXITED_DATED'], 5)
        self.assertEqual(u['counts']['WATCHED'], 2)
        self.assertEqual(u['watch']['watched'], 3)


ACCURACY_SH = os.path.join(os.path.dirname(HERE), "ops", "accuracy-registry.sh")


def _bash():
    """Git's bash on Windows (never the WSL launcher in System32), else PATH bash."""
    for c in (os.environ.get("SIGNALDECK_TEST_BASH"), r"C:\Program Files\Git\bin\bash.exe",
              shutil.which("bash")):
        if c and os.path.exists(c) and "system32" not in c.lower():
            return c
    return None


class AccuracyRegistryWiringTest(unittest.TestCase):
    """ops/accuracy-registry.sh runs the measure AFTER the pinned grade, and its
    failure can neither block the grade nor leave a stale file to be published."""

    def setUp(self):
        with open(ACCURACY_SH, encoding="utf-8") as fh:
            self.src = fh.read()
        a = self.src.index("# SURVIVORSHIP STATUS v1")
        self.block = self.src[a:self.src.index("# REFUSAL PATH", a)]

    def test_runs_after_the_grade_and_before_both_publication_paths(self):
        grader = self.src.index('tools/accuracy_registry.py" --json "$STAGE"')
        mine = self.src.index('tools/survivorship_status.py"')
        refusal = self.src.index("# REFUSAL PATH")
        first_write = self.src.index('tools/live_accuracy.py" --write')
        self.assertTrue(grader < mine < refusal < first_write)
        self.assertEqual(1, self.src.count('tools/survivorship_status.py"'))

    def _run(self, stub_body):
        bash = _bash()
        if not bash:
            self.skipTest("no bash on this host: the wiring block was NOT exercised")
        with tempfile.TemporaryDirectory() as d:
            sd = d.replace("\\", "/")
            os.makedirs(os.path.join(d, "data"))
            os.makedirs(os.path.join(d, "tools"))
            status = os.path.join(d, "data", "survivorship_status.json")
            with open(status, "w", encoding="utf-8") as fh:
                fh.write('{"as_of": "yesterday"}')
            # "$PY" "$SD/tools/survivorship_status.py" ... with PY=bash runs this
            # stand-in as a bash script, so no python or database is needed.
            with open(os.path.join(d, "tools", "survivorship_status.py"), "w", newline="\n") as fh:
                fh.write(stub_body)
            script = ('set -uo pipefail\nSD="%s"; LOG="$SD/log"; PY="%s"; refusal_reason="kept"\n'
                      '%s\necho "AFTER reason=$refusal_reason"\n'
                      % (sd, bash.replace("\\", "/"), self.block.replace("\r\n", "\n")))
            p = subprocess.run([bash, "-c", script], capture_output=True, text=True, timeout=60)
            log = kept = None
            if os.path.exists(os.path.join(d, "log")):
                with open(os.path.join(d, "log"), encoding="utf-8") as fh:
                    log = fh.read()
            if os.path.exists(status):
                with open(status, encoding="utf-8") as fh:
                    kept = fh.read()
            return p, log or "", kept

    def test_a_failing_measure_is_logged_removed_and_changes_nothing(self):
        p, log, kept = self._run("exit 3\n")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("AFTER reason=kept", p.stdout, "the run must go on with the grade's state intact")
        self.assertIn("WARN: survivorship_status.py failed (exit 3)", log)
        self.assertIsNone(kept, "yesterday's measurement must not be published as today's")

    def test_a_successful_measure_keeps_its_file(self):
        p, log, kept = self._run('echo \'{"as_of": "today"}\' > "$2"\n')
        self.assertIn("AFTER reason=kept", p.stdout)
        self.assertNotIn("WARN", log)
        self.assertEqual(kept.strip(), '{"as_of": "today"}')


if __name__ == "__main__":
    unittest.main()
"""SignalDeck paused crypto on 2026-10-21. cmd/signaldeckd pauseCrypto sets the 7
crypto symbols active=0 and leaves delisted_at NULL. tools/accuracy_registry.py
measure_universe_completeness (read-only, never edited: its sha256 is pinned)
counts a graded symbol that is active=0 with delisted_at NULL as UNRESOLVABLE,
which would flip survivorship_clean to false on every published row. The fix is
that `sdmaint void-retired-crypto -apply` stamps delisted_at (a dated exit) on
the paused crypto symbols. This module pins that contract: an inactive crypto
symbol is clean WITH delisted_at and not clean without it, for both populations
the registry measures (prediction_outcomes from GRADING_EPOCH_TS,
regime_outcomes from SURVIVORSHIP_EPOCH_TS). It also pins the one consumer
guard the stamp needed: tools/deck_facts.py counts STOCK delistings only, so
the crypto stamps do not inflate STRATEGY_DECK section 8's delisting figures.
Run: .venv/Scripts/python.exe -m unittest tools.test_crypto_survivorship -v
(from the repo root).
"""
import os
import shutil
import sqlite3
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import accuracy_registry  # noqa: E402
import deck_facts  # noqa: E402
import test_deck_facts  # noqa: E402


class CryptoPauseSurvivorshipTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.db = os.path.join(self.tmp, "reg.db")
        con = sqlite3.connect(self.db)
        con.executescript("""
            CREATE TABLE symbols (
                id INTEGER PRIMARY KEY,
                symbol TEXT,
                market TEXT,
                active INTEGER,
                delisted_at INTEGER
            );
            CREATE TABLE prediction_outcomes (
                symbol_id INTEGER,
                horizon TEXT,
                ts INTEGER,
                up INTEGER,
                resolved_at INTEGER
            );
            CREATE TABLE regime_outcomes (
                symbol_id INTEGER,
                kind TEXT,
                ts INTEGER,
                correct INTEGER,
                resolved_at INTEGER
            );
        """)
        con.executemany(
            "INSERT INTO symbols VALUES (?,?,?,?,?)",
            [
                (1, 'AAPL', 'stocks', 1, None),
                (2, 'BTC/USD', 'crypto', 0, None),
            ],
        )
        grading_ts = accuracy_registry.GRADING_EPOCH_TS + 86400
        survivorship_ts = accuracy_registry.SURVIVORSHIP_EPOCH_TS + 86400
        con.executemany(
            "INSERT INTO prediction_outcomes VALUES (?,?,?,?,?)",
            [
                (1, '1d', grading_ts, 1, grading_ts + 86400),
                (2, '1d', grading_ts, 1, grading_ts + 86400),
            ],
        )
        con.executemany(
            "INSERT INTO regime_outcomes VALUES (?,?,?,?,?)",
            [
                (1, 'trend21', survivorship_ts, 1, survivorship_ts + 86400),
                (2, 'trend21', survivorship_ts, 1, survivorship_ts + 86400),
            ],
        )
        con.commit()
        self.con = con

    def tearDown(self):
        self.con.close()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def measure(self, table):
        return accuracy_registry.measure_universe_completeness(self.con, table)

    def test_paused_crypto_without_delisted_at_is_not_clean(self):
        for table in ("prediction_outcomes", "regime_outcomes"):
            with self.subTest(table=table):
                m = self.measure(table)
                self.assertFalse(m["clean"])
                self.assertEqual(m["symbols_graded"], 2)
                self.assertEqual(m["symbols_inactive_undated"], 1)
                self.assertIn("no delisted_at", m["reason"])

    def test_paused_crypto_with_delisted_at_is_clean(self):
        stamp = accuracy_registry.GRADING_EPOCH_TS + 30 * 86400
        self.con.execute("UPDATE symbols SET delisted_at = ? WHERE id = 2", (stamp,))
        self.con.commit()
        for table in ("prediction_outcomes", "regime_outcomes"):
            with self.subTest(table=table):
                m = self.measure(table)
                self.assertTrue(m["clean"])
                self.assertEqual(m["symbols_resolvable"], 2)
                self.assertEqual(m["symbols_inactive_undated"], 0)
                self.assertEqual(m["coverage"], 1.0)
                self.assertIsNone(m["reason"])


class DeckFactsCountsStockDelistingsOnlyTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.path = os.path.join(self.tmp, "fixture.db")
        test_deck_facts.build_fixture(self.path)

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_crypto_stamp_does_not_move_the_delisting_total(self):
        con = sqlite3.connect(self.path)
        before = deck_facts.measure(con)["delistings"]["total"]
        self.assertEqual(before, 4)
        con.execute(
            "UPDATE symbols SET delisted_at = ? WHERE id = 6",
            (test_deck_facts.ts(2026, 10, 21),),
        )
        con.commit()
        after = deck_facts.measure(con)["delistings"]["total"]
        con.close()
        self.assertEqual(
            after,
            before,
            "a crypto source-retired stamp was counted as a stock delisting",
        )


if __name__ == "__main__":
    unittest.main()
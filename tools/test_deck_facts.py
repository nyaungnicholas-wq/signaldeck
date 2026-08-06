#!/usr/bin/env python3
"""Regression channel for tools/deck_facts.py.

STRATEGY_DECK.md §8 stated its data measurements as hand-typed figures with a
date stamp. On 2026-08-05, one day after they were typed, every one was stale:
universe_membership by 0.8M rows, delisted_at by a factor of 2.6, and the
survivorship window ratio by enough to invert its conclusion. deck_facts.py
measures them instead; these tests pin the arithmetic and the CLI contract on a
fixture whose answers are known by construction, so they run on a clean
checkout with no data/.

    python3 -m unittest discover -s tools -p 'test_*.py'
"""
import calendar
import os
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
SCRIPT = os.path.join(HERE, "deck_facts.py")
DAY = 86400

sys.path.insert(0, HERE)
import deck_facts  # noqa: E402


def ts(y, m, d):
    return calendar.timegm((y, m, d, 0, 0, 0, 0, 0, 0))


def build_fixture(path):
    """A database whose every answer is known by construction.

    universe_membership: 6 rows over 3 days covering 3 distinct symbols.
    delisted_at:         4 stamps — 2019 (outside both windows), 2021 (earlier
                         window), 2024 x2 (recent window) -> recent is 200% of
                         earlier, the ratio §8.2 reports.
    bars:                SPY prints all 10 sessions and so defines the calendar;
                         AAA prints 8 of its 10 -> 18/20 symbol-days = 90.00%.
                         BTC prints 5 of 5 calendar days -> crypto 100%.
    """
    con = sqlite3.connect(path)
    con.executescript("""
        CREATE TABLE symbols (id INTEGER PRIMARY KEY, symbol TEXT, market TEXT,
                              delisted_at INTEGER);
        CREATE TABLE bars (symbol_id INTEGER, tf TEXT, ts INTEGER);
        CREATE TABLE universe_membership (day INTEGER, symbol_id INTEGER,
                                          source TEXT);
    """)
    con.executemany("INSERT INTO symbols VALUES (?,?,?,?)", [
        (1, "SPY", "stocks", None),
        (2, "AAA", "stocks", ts(2021, 6, 1)),
        (3, "BBB", "stocks", ts(2024, 6, 1)),
        (4, "CCC", "stocks", ts(2024, 7, 1)),
        (5, "DDD", "stocks", ts(2019, 6, 1)),
        (6, "BTC", "crypto", None),
    ])
    con.executemany("INSERT INTO bars VALUES (?,'1d',?)",
                    [(1, d * DAY) for d in range(10)] +
                    [(2, d * DAY) for d in [0, 1, 4, 5, 6, 7, 8, 9]] +
                    [(6, d * DAY) for d in range(5)])
    con.executemany("INSERT INTO universe_membership VALUES (?,?,'bars-1d')",
                    [(0, 1), (0, 2), (0, 3), (DAY, 1), (DAY, 2), (2 * DAY, 1)])
    con.commit()
    con.close()


class DeckFactsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.db = os.path.join(self.tmp, "fixture.db")
        build_fixture(self.db)
        con = sqlite3.connect(self.db)
        self.facts = deck_facts.measure(con)
        con.close()

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def run_cli(self, *args):
        return subprocess.run(
            [sys.executable, SCRIPT, "--db", self.db] + list(args),
            cwd=REPO, capture_output=True, text=True)

    def doc(self, body):
        path = os.path.join(self.tmp, "DOC.md")
        with open(path, "w", encoding="utf-8", newline="") as fh:
            fh.write(body)
        return path

    # --- measurement -----------------------------------------------------

    def test_universe_counts_rows_days_and_distinct_symbols(self):
        u = self.facts["universe"]
        self.assertEqual(u["rows"], 6, u)
        self.assertEqual(u["days"], 3, u)
        self.assertEqual(u["symbols"], 3, u)

    def test_universe_records_its_sources_rather_than_asserting_one(self):
        # §8.3 states "every row carries source = 'bars-1d'". That is a claim
        # about the data, so it is measured, not typed.
        self.assertEqual(list(self.facts["universe"]["sources"]), ["bars-1d"])

    def test_delistings_split_by_window_and_the_ratio_between_them(self):
        d = self.facts["delistings"]
        self.assertEqual(d["total"], 4, d)
        self.assertEqual(d["earlier"], 1, d)   # 2021 only; 2019 is outside
        self.assertEqual(d["recent"], 2, d)    # both 2024 stamps
        self.assertAlmostEqual(d["recent_pct_of_earlier"], 200.0, places=2)

    def test_windows_are_the_ones_the_deck_compares(self):
        d = self.facts["delistings"]
        self.assertEqual(d["earlier_window"], "2020-2022", d)
        self.assertEqual(d["recent_window"], "2023-2025", d)

    def test_bar_completeness_is_reused_not_reimplemented(self):
        # §8.3's point-in-time claim is bounded by the bar history, and
        # tools/bars_completeness.py already measures it. A second
        # implementation would be a second answer.
        b = self.facts["bars"]
        self.assertEqual(b["calendar_sessions"], 10, b)
        self.assertEqual(b["stocks_symbols"], 2, b)
        self.assertEqual(b["stocks_expected_symbol_days"], 20, b)
        self.assertEqual(b["stocks_actual_symbol_days"], 18, b)
        self.assertAlmostEqual(b["stocks_coverage_pct"], 90.0, places=2)
        self.assertEqual(b["trailing_gap_symbols"], 0, b)
        self.assertAlmostEqual(b["crypto_coverage_pct"], 100.0, places=2)

    def test_coverage_splits_by_listing_status(self):
        # A blended figure is uninterpretable while dead names are imported in
        # bulk: SPY (listed) prints 10/10, AAA (delisted) 8/10. Blended 90% is
        # neither cohort's answer.
        b = self.facts["bars"]
        self.assertEqual(b["stocks_live_symbols"], 1, b)
        self.assertAlmostEqual(b["stocks_live_coverage_pct"], 100.0, places=2)
        self.assertEqual(b["stocks_delisted_symbols"], 1, b)
        self.assertAlmostEqual(b["stocks_delisted_coverage_pct"], 80.0, places=2)

    def test_zero_earlier_delistings_does_not_divide_by_zero(self):
        con = sqlite3.connect(self.db)
        con.execute("UPDATE symbols SET delisted_at=NULL WHERE symbol='AAA'")
        con.commit()
        facts = deck_facts.measure(con)
        con.close()
        self.assertEqual(facts["delistings"]["earlier"], 0)
        self.assertIsNone(facts["delistings"]["recent_pct_of_earlier"])
        self.assertIn(deck_facts.BEGIN, deck_facts.render(facts))

    # --- rendering -------------------------------------------------------

    def test_block_is_marker_delimited(self):
        block = deck_facts.render(self.facts)
        self.assertTrue(block.startswith(deck_facts.BEGIN), block[:80])
        self.assertTrue(block.rstrip().endswith(deck_facts.END), block[-80:])
        self.assertEqual(deck_facts.BEGIN,
                         "<!-- BEGIN GENERATED deck_facts -->")
        self.assertEqual(deck_facts.END, "<!-- END GENERATED deck_facts -->")

    def test_block_carries_the_measured_figures(self):
        block = deck_facts.render(self.facts)
        for token in ["90.0", "200"]:      # coverage %, window ratio
            self.assertIn(token, block, block)
        self.assertIn("deck_facts.py", block)

    def test_render_is_deterministic(self):
        # A wall-clock stamp in the block would make --check fail forever and
        # churn the diff on every run. Everything dated must come from the data.
        self.assertEqual(deck_facts.render(self.facts),
                         deck_facts.render(self.facts))
        with open(SCRIPT, encoding="utf-8") as fh:
            src = fh.read()
        for banned in ["time.time(", "datetime.now(", "datetime.utcnow(",
                       "date.today("]:
            self.assertNotIn(banned, src,
                             "block must not carry a wall-clock stamp")

    # --- CLI -------------------------------------------------------------

    def test_write_emits_the_block_to_a_path(self):
        out = os.path.join(self.tmp, "partial.md")
        r = self.run_cli("--write", out)
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(out, encoding="utf-8") as fh:
            self.assertIn(deck_facts.BEGIN, fh.read())

    def test_inject_splices_between_the_markers_and_keeps_the_rest(self):
        path = self.doc("before\n%s\nstale\n%s\nafter\n"
                        % (deck_facts.BEGIN, deck_facts.END))
        r = self.run_cli("--inject", path)
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
        self.assertIn("before", text)
        self.assertIn("after", text)
        self.assertNotIn("stale", text)
        self.assertEqual(text.count(deck_facts.BEGIN), 1, text)

    def test_inject_without_the_marker_pair_fails_loudly(self):
        r = self.run_cli("--inject", self.doc("no markers here\n"))
        self.assertNotEqual(r.returncode, 0)

    def test_check_passes_on_a_fresh_block_and_fails_on_a_stale_one(self):
        path = self.doc("x\n%s\n%s\ny\n" % (deck_facts.BEGIN, deck_facts.END))
        self.assertEqual(self.run_cli("--inject", path).returncode, 0)
        self.assertEqual(self.run_cli("--check", "--inject", path).returncode, 0)

        with open(path, encoding="utf-8") as fh:
            fresh = fh.read()
        with open(path, "w", encoding="utf-8", newline="") as fh:
            fh.write(fresh.replace("90.0", "99.9"))
        r = self.run_cli("--check", "--inject", path)
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)

    def test_check_does_not_write(self):
        path = self.doc("x\n%s\nstale\n%s\ny\n"
                        % (deck_facts.BEGIN, deck_facts.END))
        self.run_cli("--check", "--inject", path)
        with open(path, encoding="utf-8") as fh:
            self.assertIn("stale", fh.read())

    def test_a_missing_database_is_an_error_not_a_zero(self):
        r = subprocess.run(
            [sys.executable, SCRIPT, "--db",
             os.path.join(self.tmp, "nope.db"), "--write",
             os.path.join(self.tmp, "o.md")],
            cwd=REPO, capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0)

    def test_the_database_is_opened_read_only(self):
        with open(SCRIPT, encoding="utf-8") as fh:
            self.assertIn("mode=ro", fh.read())


class DeckIsGeneratedTest(unittest.TestCase):
    """The deck must carry the block, and no sibling check may fight it."""

    def test_strategy_deck_includes_the_generated_block(self):
        path = os.path.join(REPO, "STRATEGY_DECK.md")
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
        self.assertIn(deck_facts.BEGIN, text)
        self.assertIn(deck_facts.END, text)

    def test_the_deck_checker_exempts_the_generated_block(self):
        # check_strategy_deck.py polices hand-written prose with a number
        # allowlist. Measured figures are not hand-written, and no allowlist can
        # be kept current with a database.
        path = os.path.join(REPO, "tools", "check_strategy_deck.py")
        with open(path, encoding="utf-8") as fh:
            self.assertIn("deck_facts", fh.read())


if __name__ == "__main__":
    unittest.main()

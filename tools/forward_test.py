#!/usr/bin/env python3
"""Grade the pre-registered forward test, one SESSION at a time.

Registered hypothesis (prereg kind forward-test-registration, testId
confluence-long-liquid-2026-08): the confluence LONG book, restricted to entries
at or above $20, produces a positive DAY-WEIGHTED mean excess return over a
matched same-session universe benchmark.

THE UNIT IS THE SESSION, NEVER THE TRADE. Bets placed within one session share a
single market call, so averaging over trades counts that call once per position
and manufactures significance out of position count. That is not a stylistic
preference: in sample this book reads +0.332% excess per trade and -0.435% per
session, and one session (2026-07-15) supplies 290 of 791 episodes. Nothing in
this file may average over trades.

Dry run is the default and writes nothing. --commit is the only way to store.
"""

import argparse
import math
import sqlite3
import statistics
import sys

TEST_ID = "confluence-long-liquid-2026-08"
MIN_BETS_PER_SESSION = 5
MIN_SESSIONS = 60

SCHEMA = """
CREATE TABLE IF NOT EXISTS forward_test_daily(
  test_id    TEXT NOT NULL,
  session    TEXT NOT NULL,
  n_bets     INTEGER NOT NULL,
  book_pct   REAL NOT NULL,
  bench_pct  REAL NOT NULL,
  excess_pct REAL NOT NULL,
  eligible   INTEGER NOT NULL,
  PRIMARY KEY (test_id, session))
"""

# The registered population. Episode-deduped so a setup that persists for days is
# ONE bet, not one per day.
BOOK_SQL = """
WITH ep AS (
  SELECT symbol_id, direction, episode_ts, MIN(ts) t
    FROM confluence_outcomes
   WHERE fwd_return IS NOT NULL AND ungradable IS NULL AND episode_ts IS NOT NULL
   GROUP BY symbol_id, direction, episode_ts
)
SELECT date(o.ts,'unixepoch') d, COUNT(*) n, AVG(o.direction*o.fwd_return)*100 book_pct
  FROM ep JOIN confluence_outcomes o ON o.symbol_id=ep.symbol_id AND o.ts=ep.t
 WHERE o.direction=1 AND o.entry_px >= 20 AND date(o.ts,'unixepoch') > ?
 GROUP BY d
"""

# The book must beat holding its own universe, not zero. Same session, same
# price floor, equal weighted.
BENCH_SQL = """
WITH b AS (
  SELECT symbol_id, date(ts,'unixepoch') d, close,
         LEAD(close) OVER (PARTITION BY symbol_id ORDER BY ts) nxt
    FROM bars WHERE tf='1d'
)
SELECT d, AVG(nxt/close-1)*100 bench_pct
  FROM b WHERE close >= 20 AND nxt IS NOT NULL AND d > ?
 GROUP BY d
"""


def open_db(path, write):
    """Read-only unless writing. The daemon holds the single writer; a research
    tool that opens read-write contends for that lock for no reason."""
    if write:
        conn = sqlite3.connect(path)
        conn.execute("PRAGMA busy_timeout = 15000")
        return conn
    return sqlite3.connect(f"file:{path}?mode=ro", uri=True)


def compute(conn, start):
    """Return one row per session. Pure read: writes nothing, so a dry run and a
    commit run compute identically and can never disagree."""
    book = {d: (n, pct) for d, n, pct in conn.execute(BOOK_SQL, (start,))}
    bench = {d: pct for d, pct in conn.execute(BENCH_SQL, (start,))}
    rows, skipped = [], []
    for sess in sorted(book):
        n_bets, book_pct = book[sess]
        if sess not in bench:
            # Never zero-fill. A session with no benchmark cannot be graded
            # against one, and silently calling that benchmark 0.0% would credit
            # the book with the market's whole move.
            skipped.append(sess)
            continue
        bench_pct = bench[sess]
        rows.append({
            "session": sess,
            "n_bets": n_bets,
            "book_pct": book_pct,
            "bench_pct": bench_pct,
            "excess_pct": book_pct - bench_pct,
            "eligible": 1 if n_bets >= MIN_BETS_PER_SESSION else 0,
        })
    return rows, skipped


def store(conn, rows):
    conn.execute(SCHEMA)
    conn.executemany(
        """INSERT INTO forward_test_daily
             (test_id,session,n_bets,book_pct,bench_pct,excess_pct,eligible)
           VALUES(:test_id,:session,:n_bets,:book_pct,:bench_pct,:excess_pct,:eligible)
           ON CONFLICT(test_id,session) DO UPDATE SET
             n_bets=excluded.n_bets, book_pct=excluded.book_pct,
             bench_pct=excluded.bench_pct, excess_pct=excluded.excess_pct,
             eligible=excluded.eligible""",
        [dict(r, test_id=TEST_ID) for r in rows])
    conn.commit()


def verdict(conn):
    """Read the stored record and report. Returns the printable text so the
    self-check can assert on exactly what a reader sees."""
    try:
        stored = list(conn.execute(
            "SELECT excess_pct, eligible FROM forward_test_daily WHERE test_id=?",
            (TEST_ID,)))
    except sqlite3.OperationalError:
        return "no forward_test_daily table yet — nothing has been graded"

    excess = [e for e, ok in stored if ok]
    excluded = sum(1 for _, ok in stored if not ok)
    n = len(excess)
    if n == 0:
        return f"0 eligible sessions ({excluded} excluded as under {MIN_BETS_PER_SESSION} bets)"

    mean = statistics.fmean(excess)
    out = [f"eligible sessions: {n}/{MIN_SESSIONS}",
           f"excluded (under {MIN_BETS_PER_SESSION} bets): {excluded}",
           f"mean daily excess: {mean:+.4f}%"]

    if n < MIN_SESSIONS:
        # The refusal IS the tool. Reporting a direction here is how a null
        # result gets quietly talked into a positive one over a long window.
        out.append(f"INSUFFICIENT EVIDENCE - {n}/{MIN_SESSIONS} sessions")
        out.append("no pass/fail may be stated until the registered floor is met")
        return "\n".join(out)

    sd = statistics.stdev(excess) if n > 1 else 0.0
    # z rather than t: past n=60 the difference is immaterial and the registered
    # rule names 1.645.
    lower = mean - 1.645 * sd / math.sqrt(n)
    out.append(f"one-sided 95% lower bound: {lower:+.4f}%")
    out.append("PASS" if (mean > 0 and lower > 0) else "REFUTED")
    return "\n".join(out)


def demo():
    """Self-check on a database this function builds itself.

    Asserts RELATIONSHIPS, never hand-computed percentages — an arithmetic
    expectation typed by hand is just a second place for the same mistake."""
    conn = sqlite3.connect(":memory:")
    conn.execute("""CREATE TABLE confluence_outcomes(
        symbol_id INT, ts INT, direction INT, entry_px REAL,
        fwd_return REAL, episode_ts INT, ungradable TEXT)""")
    conn.execute("CREATE TABLE bars(symbol_id INT, tf TEXT, ts INT, close REAL)")

    d0, day = 1728000000, 86400
    # session 0: 6 bets -> eligible. session 1: 4 bets -> ineligible.
    # session 2: 6 bets but NO bars -> must be skipped entirely.
    for sess, count in ((0, 6), (1, 4), (2, 6)):
        for i in range(count):
            conn.execute(
                "INSERT INTO confluence_outcomes VALUES(?,?,?,?,?,?,NULL)",
                (i, d0 + sess * day, 1, 50.0, 0.01 * (i + 1), d0 + sess * day))
    # Benchmark bars exist for sessions 0 and 1 only (session 2 has no NEXT bar).
    for i in range(4):
        for sess in range(3):
            conn.execute("INSERT INTO bars VALUES(?,'1d',?,?)",
                         (i, d0 + sess * day, 40.0 + sess))

    rows, skipped = compute(conn, "")
    by_sess = {r["session"]: r for r in rows}

    assert len(rows) == 2, f"expected 2 graded sessions, got {len(rows)}"
    assert len(skipped) == 1, f"expected 1 skipped session, got {skipped}"
    # (d) the benchmark-less session is ABSENT, not zero-filled
    assert skipped[0] not in by_sess, "session without a benchmark was stored anyway"

    elig = [r for r in rows if r["eligible"]]
    inelig = [r for r in rows if not r["eligible"]]
    # (a) the 4-bet session is recorded but not eligible
    assert len(elig) == 1 and len(inelig) == 1, "eligibility split is wrong"
    assert inelig[0]["n_bets"] == 4, "the 4-bet session should be the ineligible one"
    # (b) excess is book minus benchmark, everywhere
    for r in rows:
        assert abs(r["excess_pct"] - (r["book_pct"] - r["bench_pct"])) < 1e-9, \
            f"excess is not book-minus-bench on {r['session']}"

    store(conn, rows)
    text = verdict(conn)
    # (a cont.) the ineligible session must not move the statistic
    assert statistics.fmean([r["excess_pct"] for r in elig]) == \
        statistics.fmean([e for e, ok in conn.execute(
            "SELECT excess_pct,eligible FROM forward_test_daily") if ok]), \
        "an ineligible session leaked into the mean"
    # (c) under the floor, refuse — and say neither PASS nor REFUTED
    assert "INSUFFICIENT EVIDENCE" in text, f"expected a refusal, got:\n{text}"
    assert "PASS" not in text and "REFUTED" not in text, \
        f"stated a verdict below the evidence floor:\n{text}"
    print("selftest ok")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--db", default="data/signaldeck.db")
    ap.add_argument("--start", default="",
                    help="grade sessions strictly after this YYYY-MM-DD")
    ap.add_argument("--commit", action="store_true", help="write (default: dry run)")
    ap.add_argument("--verdict", action="store_true", help="report the standing")
    ap.add_argument("--selftest", action="store_true")
    a = ap.parse_args()

    if a.selftest:
        demo()
        return

    conn = open_db(a.db, a.commit)
    rows, skipped = compute(conn, a.start)
    for s in skipped:
        print(f"WARNING: session {s} has book rows but no benchmark — skipped, not zero-filled")
    print(f"{len(rows)} session(s) computed"
          f"{'' if a.commit else ' (dry run, nothing written)'}")
    for r in rows:
        print(f"  {r['session']}  n={r['n_bets']:<4} book={r['book_pct']:+.3f}%"
              f"  bench={r['bench_pct']:+.3f}%  excess={r['excess_pct']:+.3f}%"
              f"{'' if r['eligible'] else '  [ineligible]'}")
    if a.commit:
        store(conn, rows)
    if a.verdict:
        print()
        print(verdict(conn))
    return 0


if __name__ == "__main__":
    sys.exit(main())

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
import datetime
import hashlib
import json
import math
import sqlite3
import statistics
import sys

TEST_ID = "confluence-long-liquid-2026-08"
MIN_BETS_PER_SESSION = 5
MIN_SESSIONS = 60

# The UTC date of the registration record (prereg seq 87, 2026-08-23T00:14:36Z).
# The window opens at the first session STRICTLY AFTER it.
#
# This lives in the code rather than in a caller's flag because --start defaulted
# to "" and the predicate is `date(...) > ?`: every stored date is > "", so a bare
# `--commit` would have written all 24 PRE-REGISTRATION sessions into the record
# and counted them toward the 60-session floor forever — including 2026-07-15,
# the session the spec itself names as contributing 290 of 791 in-sample episodes
# at +1.5% excess. The registered rule is "No backfill, ever"; nothing enforced it.
REGISTERED_START = "2026-08-23"

# Bonferroni family size, from the record's own knownWeakness: five price buckets
# crossed with two directions, best cell kept. The registered decision rule says
# the lower bound is "Bonferroni-corrected across the registered family", and a
# bare one-sided 95% z of 1.645 is not that — at family 10 the one-sided alpha is
# 0.005 and z is 2.576, a half-width 1.57x wider. Using 1.645 would have made PASS
# materially easier to reach than the pre-registration permits.
FAMILY_SIZE = 10
CORRECTED_Z = 2.576

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


# --- The frozen claim must be able to refuse its own grader ----------------
#
# prereg.HashEntry signs ts, kind, spec_hash and note. It does NOT sign
# spec_json, and nothing in the repo ever recomputed spec_hash from the bytes it
# summarises, so an edited frozen claim verified intact. Worse, the numbers this
# file grades with were hand-copied out of the record: claim and grader could
# drift apart in silence and the chain would attest to neither.
#
# This does not change what is signed. Changing HashEntry would invalidate all
# 88 existing entry hashes -- including this record's -- and the construction is
# itself registered (kind protocol-provenance-correction). Instead the grader
# refuses to run unless the record still hashes to what it claims AND still says
# what this file assumes.
REGISTRATION_KIND = "forward-test-registration"
CHAIN_SEP = b""  # record separator, as in the prediction ledger


def _entry_hash(prev_hash, ts, kind, spec_hash, note):
    payload = f"ts={ts}|kind={kind}|specHash={spec_hash}|note={note}"
    return hashlib.sha256(prev_hash.encode() + CHAIN_SEP + payload.encode()).hexdigest()


def verify_registration(conn):
    """Return the frozen spec, or raise SystemExit naming what no longer holds.

    A hash that only covers itself proves the row was not edited. It does not
    prove the grader still implements it, which is why the constant checks below
    build their expected phrase FROM the constant: changing MIN_SESSIONS is what
    turns this red.
    """
    rows = conn.execute(
        "SELECT seq, ts, kind, spec_json, spec_hash, prev_hash, entry_hash, note "
        "FROM prereg_records WHERE kind = ?", (REGISTRATION_KIND,)).fetchall()
    if len(rows) != 1:
        raise SystemExit(f"REFUSING to grade: expected exactly 1 {REGISTRATION_KIND} "
                         f"record on the chain, found {len(rows)}")
    seq, ts, kind, spec_json, spec_hash, prev_hash, entry_hash, note = rows[0]

    got = hashlib.sha256(spec_json.encode()).hexdigest()
    if got != spec_hash:
        raise SystemExit(f"REFUSING to grade: prereg seq {seq} spec_json does not hash to its "
                         f"spec_hash (stored {spec_hash}, recomputed {got}). The frozen claim "
                         f"has been edited since it was filed.")

    chain = _entry_hash(prev_hash, ts, kind, spec_hash, note)
    if chain != entry_hash:
        raise SystemExit(f"REFUSING to grade: prereg seq {seq} entry_hash does not match the "
                         f"chain (stored {entry_hash}, recomputed {chain}).")

    spec = json.loads(spec_json)
    if spec.get("testId") != TEST_ID:
        raise SystemExit(f"REFUSING to grade: this file grades {TEST_ID!r}, the record "
                         f"registers {spec.get('testId')!r}")

    # The window boundary belongs to the record's own UTC date. Every confluence
    # bucket is stamped at exactly 00:00:00 UTC, so a bucket dated the same day
    # as the filing sits BEFORE the record; comparing in local time admitted one
    # such session once already. Derived here, never hand-copied again.
    filed = datetime.datetime.fromtimestamp(ts, datetime.timezone.utc).strftime("%Y-%m-%d")
    if filed != REGISTERED_START:
        raise SystemExit(f"REFUSING to grade: REGISTERED_START is {REGISTERED_START!r} but "
                         f"prereg seq {seq} was filed {filed} UTC. The boundary is the record's.")

    for phrase, field in ((f"{MIN_SESSIONS} distinct eligible sessions", "minimumEvidence"),
                          (f"at least {MIN_BETS_PER_SESSION} bets", "minimumEvidence"),
                          ("Bonferroni", "decisionRule")):
        if phrase not in (spec.get(field) or ""):
            raise SystemExit(f"REFUSING to grade: the registered {field} does not say "
                             f"{phrase!r}. The grader and the claim have drifted.")

    # CORRECTED_Z must BE the correction the record requires, not a number that
    # happens to sit next to the word Bonferroni.
    want = statistics.NormalDist().inv_cdf(1 - 0.05 / FAMILY_SIZE)
    if abs(want - CORRECTED_Z) > 0.01:
        raise SystemExit(f"REFUSING to grade: CORRECTED_Z is {CORRECTED_Z} but one-sided 95% "
                         f"Bonferroni at family {FAMILY_SIZE} is {want:.3f}")
    return spec


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
        # The date predicate is NOT optional. Without it a row written by a
        # careless --commit would count toward the floor and the mean forever,
        # and the registered rule is "No backfill, ever".
        stored = list(conn.execute(
            "SELECT excess_pct, eligible FROM forward_test_daily "
            "WHERE test_id=? AND session > ?",
            (TEST_ID, REGISTERED_START)))
    except sqlite3.OperationalError:
        return "no forward_test_daily table yet - nothing has been graded"

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
    # The registered rule says the lower bound is "Bonferroni-corrected across the
    # registered family". A bare one-sided 95% z of 1.645 is not corrected at all;
    # at family 10 the one-sided alpha is 0.005 and z is 2.576. Using 1.645 here
    # would have made PASS reachable on evidence the pre-registration does not
    # accept — the correction is the price of having gone looking through ten
    # cells for the one that looked best.
    lower = mean - CORRECTED_Z * sd / math.sqrt(n)
    out.append(f"lower bound (one-sided 95%, Bonferroni x{FAMILY_SIZE}, z={CORRECTED_Z}): {lower:+.4f}%")
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

    # 2026-08-25, i.e. INSIDE the registered window. The fixture dates are not
    # arbitrary: verdict() filters on REGISTERED_START, so a fixture built before
    # the window would be silently excluded and every assertion below would pass
    # vacuously against an empty result.
    d0, day = 1787616000, 86400
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

    # (e) a PRE-REGISTRATION session cannot reach the statistic. The registered
    # rule is "No backfill, ever", and the failure mode is silent: a backfilled
    # row counts toward the mean and the 60-session floor with nothing marking it.
    before = int(REGISTERED_START.replace("-", ""))  # sanity: sessions sort as strings
    assert before > 0
    n_before = len([e for e, ok in conn.execute(
        "SELECT excess_pct, eligible FROM forward_test_daily") if ok])
    conn.execute(
        "INSERT INTO forward_test_daily VALUES (?,?,?,?,?,?,?)",
        (TEST_ID, "2026-07-15", 290, 1.5, 0.0, 1.5, 1))
    conn.commit()
    after = verdict(conn)
    assert f"eligible sessions: {n_before}/" in after, (
        "a pre-registration session (2026-07-15) reached the statistic — "
        f"the backfill filter is not holding:\n{after}")
    # (f) the grader must refuse a claim that no longer hashes to its record, and
    # refuse itself if it has drifted from the claim. Both directions matter: the
    # hash catches an edited row, the phrase checks catch an edited grader.
    conn.execute("""CREATE TABLE prereg_records(
        seq INTEGER PRIMARY KEY, ts INT, kind TEXT, spec_json TEXT,
        spec_hash TEXT, prev_hash TEXT, entry_hash TEXT, note TEXT)""")
    filed_ts = int(datetime.datetime(2026, 8, 23, 0, 14, 36,
                                     tzinfo=datetime.timezone.utc).timestamp())
    spec_json = json.dumps({
        "kind": REGISTRATION_KIND, "testId": TEST_ID,
        "minimumEvidence": (f"{MIN_SESSIONS} distinct eligible sessions before any verdict. "
                            f"A session is eligible only if it carries at least "
                            f"{MIN_BETS_PER_SESSION} bets."),
        "decisionRule": "PASS only if the one-sided 95% lower bound, Bonferroni-corrected "
                        "across the registered family, is greater than zero."})
    spec_hash = hashlib.sha256(spec_json.encode()).hexdigest()
    prev_hash = "f7736107"
    note = "initial registration"
    conn.execute("INSERT INTO prereg_records VALUES(87,?,?,?,?,?,?,?)",
                 (filed_ts, REGISTRATION_KIND, spec_json, spec_hash, prev_hash,
                  _entry_hash(prev_hash, filed_ts, REGISTRATION_KIND, spec_hash, note), note))

    verify_registration(conn)  # the honest record must pass, or the rest proves nothing

    def refuses(what):
        try:
            verify_registration(conn)
        except SystemExit:
            return True
        raise AssertionError(f"verify_registration accepted {what}")

    # mutation 1: edit the frozen claim, leave every hash alone
    tampered = spec_json.replace(f"{MIN_SESSIONS} distinct", "3 distinct")
    conn.execute("UPDATE prereg_records SET spec_json=? WHERE seq=87", (tampered,))
    refuses("a spec_json that no longer hashes to its spec_hash")

    # mutation 2: re-hash the edited claim so spec_hash agrees again. The chain
    # hash must now disagree -- this is the step a naive verifier would pass.
    conn.execute("UPDATE prereg_records SET spec_hash=? WHERE seq=87",
                 (hashlib.sha256(tampered.encode()).hexdigest(),))
    refuses("a re-hashed claim whose entry_hash no longer matches the chain")

    # mutation 3: a fully re-signed row -- every hash internally consistent, and
    # the evidence floor quietly lowered from 60 to 3. Only the constant-vs-claim
    # check can catch this one, which is why it exists.
    th = hashlib.sha256(tampered.encode()).hexdigest()
    conn.execute("UPDATE prereg_records SET entry_hash=? WHERE seq=87",
                 (_entry_hash(prev_hash, filed_ts, REGISTRATION_KIND, th, note),))
    refuses("a re-signed claim whose evidence floor no longer matches the grader")

    # restore, and confirm the check is not simply always-red
    conn.execute("UPDATE prereg_records SET spec_json=?, spec_hash=?, entry_hash=? WHERE seq=87",
                 (spec_json, spec_hash,
                  _entry_hash(prev_hash, filed_ts, REGISTRATION_KIND, spec_hash, note)))
    verify_registration(conn)

    print("selftest ok")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--db", default="data/signaldeck.db")
    ap.add_argument("--start", default=REGISTERED_START,
                    help=f"grade sessions strictly after this YYYY-MM-DD "
                         f"(default and only committable value: {REGISTERED_START})")
    ap.add_argument("--commit", action="store_true", help="write (default: dry run)")
    ap.add_argument("--verdict", action="store_true", help="report the standing")
    ap.add_argument("--selftest", action="store_true")
    a = ap.parse_args()

    if a.selftest:
        demo()
        return

    # A dry run may look anywhere — that is how you inspect the in-sample body.
    # A COMMIT may not. Writing a pre-registration session into the record puts
    # it in the mean and the 60-session floor permanently, and the one that would
    # be written first is 2026-07-15, which the spec itself names as supplying 290
    # of 791 in-sample episodes at +1.5% excess. Refusing here is cheaper than
    # explaining later why the record contains days the registration excluded.
    if a.commit and a.start != REGISTERED_START:
        print(f"REFUSING to commit with --start {a.start!r}: the registration "
              f"(prereg seq 87) opens the window strictly after {REGISTERED_START}. "
              f"Re-run without --start, or drop --commit to inspect.", file=sys.stderr)
        return 2

    conn = open_db(a.db, a.commit)
    verify_registration(conn)  # refuses if the frozen claim or this grader moved
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

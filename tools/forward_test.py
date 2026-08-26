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
  JOIN symbols sy ON sy.id = o.symbol_id AND sy.market = 'stocks'
 WHERE o.direction=1 AND o.entry_px >= 20 AND date(o.ts,'unixepoch') > ?
 GROUP BY d
"""

# The book must beat holding its own universe, not zero. Same session, same
# price floor, equal weighted.
#
# The stale-feed screen (registered at prereg seq 90) is keyed on the SESSION
# DATE, in the same spelling on both sides. It used to compare two different
# folds of two different clocks: the bucket came from date(b.ts,'unixepoch')
# while the join key came from (b.ts - 18000)/86400. Daily stock bars are
# stamped 04:00:00 UTC, so that second expression floors to the day BEFORE the
# bucket on every single row -- verified against live data: bucket 2026-08-25
# resolved to stale-day 2026-08-24, 606 rows, and 2026-08-24 to 2026-08-23, 619
# rows. Every benchmark name was therefore screened against the wrong session's
# staleness, in both directions: a name stale on the session being graded stayed
# in, and a name stale the day before was dropped from a session it was fine on.
#
# dq_events carries real wall-clock timestamps, so it still needs the -18000
# fold to land on its ET session date; bars are already stamped per session, so
# their date IS the key. Both sides now produce the same 'YYYY-MM-DD' string and
# the unit mismatch cannot come back silently. 18000 is the repo's canonical
# trading-day offset (store.TradingDay); it is EST year-round and that known
# approximation is deliberately left alone here rather than forked into a
# second definition, which is the defect this comment exists to prevent.
BENCH_SQL = """
WITH stale_feed AS (
  SELECT DISTINCT symbol_id, date(ts - 18000, 'unixepoch') AS sd
    FROM dq_events WHERE kind = 'stale' AND symbol_id IS NOT NULL
),
b AS (
  SELECT b.symbol_id, b.ts, date(b.ts,'unixepoch') d, b.close,
         LEAD(b.close) OVER (PARTITION BY b.symbol_id ORDER BY b.ts) nxt
    FROM bars b
    JOIN symbols sy ON sy.id = b.symbol_id AND sy.market = 'stocks'
   WHERE b.tf='1d'
)
SELECT d, AVG(nxt/close-1)*100 bench_pct, COUNT(*) n_names
  FROM b
 WHERE close >= 20 AND nxt IS NOT NULL AND d > ?
   -- KNOWN ASYMMETRY, LEFT IN DELIBERATELY. This winsorises the BENCHMARK at
   -- +/-30% and nothing winsorises the book, so a registered excess return
   -- compares a capped leg to an uncapped one. It is not removed here because
   -- it is REGISTERED (prereg seq 90, screensAdded.extremeMove), and because
   -- seq 89 -- filed 76 minutes EARLIER -- considered this exact screen and
   -- rejected it: "Their direction is therefore KNOWN, and filing them would be
   -- selecting a benchmark with a result in view." seq 90 then filed it anyway.
   -- That contradiction is on the chain and cannot be edited away.
   --
   -- Measured 2026-08-25 over the in-sample body, so the direction is known a
   -- second time: cap ON -0.1340% mean excess, 10/19 beat; cap OFF -0.1581%,
   -- 9/19. Removing it makes the book look WORSE, which is exactly why removing
   -- it now would still be a benchmark change made with a result in view.
   -- Resolving this is an AMENDMENT and a judgement about what the record may
   -- say, not a code fix. Do not quietly delete this line.
   AND ABS(nxt/close - 1) <= 0.30
   AND NOT EXISTS (SELECT 1 FROM stale_feed f
                    WHERE f.symbol_id = b.symbol_id AND f.sd = b.d)
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
# The breadth floor arrived as an AMENDMENT, not in the original claim, so the
# binding runs both ways: this grader may apply the floor only while the chain
# carries the amendment, and must apply exactly the number the amendment names.
BENCH_FLOOR_KIND = "forward-test-benchmark-floor"
MIN_BENCHMARK_NAMES = 100
CHAIN_SEP = b""  # record separator, as in the prediction ledger


def _entry_hash(prev_hash, ts, kind, spec_hash, note):
    payload = f"ts={ts}|kind={kind}|specHash={spec_hash}|note={note}"
    return hashlib.sha256(prev_hash.encode() + CHAIN_SEP + payload.encode()).hexdigest()


def _verified_spec(conn, kind):
    """Return the parsed spec of the one record of `kind`, or raise.

    Both hashes are recomputed here rather than trusted: spec_hash from the
    spec_json bytes actually stored, and entry_hash from the chain payload. A
    record that fails either has been edited since it was filed.
    """
    rows = conn.execute(
        "SELECT seq, ts, kind, spec_json, spec_hash, prev_hash, entry_hash, note "
        "FROM prereg_records WHERE kind = ?", (kind,)).fetchall()
    if len(rows) != 1:
        raise SystemExit(f"REFUSING to grade: expected exactly 1 {kind} record on the "
                         f"chain, found {len(rows)}")
    seq, ts, k, spec_json, spec_hash, prev_hash, entry_hash, note = rows[0]

    got = hashlib.sha256(spec_json.encode()).hexdigest()
    if got != spec_hash:
        raise SystemExit(f"REFUSING to grade: prereg seq {seq} ({kind}) spec_json does not "
                         f"hash to its spec_hash (stored {spec_hash}, recomputed {got}). "
                         f"The frozen claim has been edited since it was filed.")

    chain = _entry_hash(prev_hash, ts, k, spec_hash, note)
    if chain != entry_hash:
        raise SystemExit(f"REFUSING to grade: prereg seq {seq} ({kind}) entry_hash does not "
                         f"match the chain (stored {entry_hash}, recomputed {chain}).")
    return seq, ts, json.loads(spec_json)


def verify_registration(conn):
    """Return the frozen spec, or raise SystemExit naming what no longer holds.

    A hash that only covers itself proves the row was not edited. It does not
    prove the grader still implements it, which is why the constant checks below
    build their expected phrase FROM the constant: changing MIN_SESSIONS is what
    turns this red.
    """
    seq, ts, spec = _verified_spec(conn, REGISTRATION_KIND)

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

    # The breadth floor is not in the registration -- it was added by amendment --
    # so applying it without the amendment on the chain would be grading by a rule
    # nobody registered, which is the same defect as ignoring one that was.
    _, _, amd = _verified_spec(conn, BENCH_FLOOR_KIND)
    phrase = f"at least {MIN_BENCHMARK_NAMES} distinct symbols"
    if phrase not in (amd.get("change") or ""):
        raise SystemExit(f"REFUSING to grade: this grader applies a benchmark floor of "
                         f"{MIN_BENCHMARK_NAMES}, but the amendment on the chain does not say "
                         f"{phrase!r}. The floor must be the one that was registered.")
    return spec


def compute(conn, start):
    """Return one row per session. Pure read: writes nothing, so a dry run and a
    commit run compute identically and can never disagree."""
    book = {d: (n, pct) for d, n, pct in conn.execute(BOOK_SQL, (start,))}
    bench = {d: (pct, n) for d, pct, n in conn.execute(BENCH_SQL, (start,))}
    rows, skipped = [], []
    for sess in sorted(book):
        n_bets, book_pct = book[sess]
        if sess not in bench:
            # Never zero-fill. A session with no benchmark cannot be graded
            # against one, and silently calling that benchmark 0.0% would credit
            # the book with the market's whole move.
            skipped.append(sess)
            continue
        bench_pct, bench_n = bench[sess]
        rows.append({
            "session": sess,
            "n_bets": n_bets,
            "book_pct": book_pct,
            "bench_pct": bench_pct,
            "excess_pct": book_pct - bench_pct,
            # Two floors, both registered, both recorded the same way. A session
            # the book under-populates and a session whose BENCHMARK is too thin
            # to mean anything are equally ungradable, and neither is discarded --
            # they are stored with eligible=0 so the record shows what was seen
            # and refused, rather than silently omitting it.
            "eligible": 1 if (n_bets >= MIN_BETS_PER_SESSION
                              and bench_n >= MIN_BENCHMARK_NAMES) else 0,
            "bench_n": bench_n,
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
    # The population is restricted to market='stocks' and excludes symbols the
    # feed auditor flagged stale that day, so both tables must exist and every id
    # the fixture touches must be present -- a missing symbols row would silently
    # drop rows from both legs and fail the assertions for the wrong reason.
    conn.execute("CREATE TABLE symbols(id INT PRIMARY KEY, market TEXT)")
    conn.execute("CREATE TABLE dq_events(kind TEXT, symbol_id INT, ts INT)")
    for i in list(range(MIN_BENCHMARK_NAMES + 40)) + [900, 901, 902]:
        conn.execute("INSERT INTO symbols VALUES(?,'stocks')", (i,))

    # 2026-08-25, i.e. INSIDE the registered window. The fixture dates are not
    # arbitrary: verdict() filters on REGISTERED_START, so a fixture built before
    # the window would be silently excluded and every assertion below would pass
    # vacuously against an empty result.
    d0, day = 1787616000, 86400
    BAR_STAMP = 14400  # 04:00:00 UTC, where production stamps a daily stock bar
    # session 0: 6 bets -> eligible. session 1: 4 bets -> ineligible.
    # session 2: 6 bets but NO bars -> must be skipped entirely.
    for sess, count in ((0, 6), (1, 4), (2, 6)):
        for i in range(count):
            conn.execute(
                "INSERT INTO confluence_outcomes VALUES(?,?,?,?,?,?,NULL)",
                (i, d0 + sess * day, 1, 50.0, 0.01 * (i + 1), d0 + sess * day))
    # Benchmark bars exist for sessions 0 and 1 only (session 2 has no NEXT bar).
    # The benchmark carries MIN_BENCHMARK_NAMES + 20 symbols so the registered
    # breadth floor is CLEARED here; the thin case is exercised separately below,
    # because a fixture that fails both floors at once cannot tell them apart.
    # BAR_STAMP is not decoration. Production stamps daily stock bars at
    # 04:00:00 UTC, and a fixture that stamped them at midnight could not
    # reproduce the stale-feed fold bug: date(ts) and (ts-18000)/86400 disagreed
    # by a day on every real row while the fixture's own rows looked fine. A
    # fixture must be stamped the way production is or it certifies nothing.
    for i in range(MIN_BENCHMARK_NAMES + 20):
        for sess in range(3):
            conn.execute("INSERT INTO bars VALUES(?,'1d',?,?)",
                         (i, d0 + sess * day + BAR_STAMP, 40.0 + sess))

    # session 3: 6 bets, but only 3 symbols in its benchmark -- the shape the
    # amendment exists to refuse. Three crypto names on a weekend is not a
    # benchmark, and averaging against one would be worse than not grading.
    for i in range(6):
        conn.execute("INSERT INTO confluence_outcomes VALUES(?,?,?,?,?,?,NULL)",
                     (i, d0 + 3 * day, 1, 50.0, 0.02, d0 + 3 * day))
    for i in range(3):
        for sess in (3, 4):
            conn.execute("INSERT INTO bars VALUES(?,'1d',?,?)",
                         (900 + i, d0 + sess * day + BAR_STAMP, 40.0 + sess))

    rows, skipped = compute(conn, "")
    by_sess = {r["session"]: r for r in rows}

    thin = by_sess[sorted(by_sess)[-1]]
    assert thin["n_bets"] >= MIN_BETS_PER_SESSION, "the thin session should clear the BET floor"
    assert thin["bench_n"] < MIN_BENCHMARK_NAMES, "the thin session should fail the BREADTH floor"
    assert not thin["eligible"], (
        "a session with enough bets but a 3-name benchmark was marked eligible -- "
        "the registered breadth floor is not being applied")

    assert len(rows) == 3, f"expected 3 graded sessions, got {len(rows)}"
    assert len(skipped) == 1, f"expected 1 skipped session, got {skipped}"
    # (d) the benchmark-less session is ABSENT, not zero-filled
    assert skipped[0] not in by_sess, "session without a benchmark was stored anyway"

    elig = [r for r in rows if r["eligible"]]
    inelig = [r for r in rows if not r["eligible"]]
    # (a) the 4-bet session and the thin-benchmark session are both recorded and
    # both ineligible, for two DIFFERENT registered reasons
    assert len(elig) == 1 and len(inelig) == 2, "eligibility split is wrong"
    assert any(r["n_bets"] == 4 for r in inelig), "the 4-bet session should be ineligible"
    # (b) excess is book minus benchmark, everywhere
    for r in rows:
        assert abs(r["excess_pct"] - (r["book_pct"] - r["bench_pct"])) < 1e-9, \
            f"excess is not book-minus-bench on {r['session']}"

    # (g) the registered stale-feed screen must drop a name on the session it was
    # actually stale on, and ONLY that session. Both directions, because the bug
    # this replaces was a silent day shift rather than a missing screen: the join
    # key floored to the day BEFORE the bucket on every row, so the screen was
    # fully wired, fully green, and pointed one session off. Asserting only that
    # a stale name disappears would have passed against the broken version too.
    sess0 = sorted(by_sess)[0]
    base_n = by_sess[sess0]["bench_n"]

    def bench_n_with(*events):
        """bench_n for session 0 given exactly these (symbol, ts) stale events.

        One symbol is flagged per probe rather than several at once: the old
        code also cut exactly ONE name here, just the wrong one, so a count over
        a mixed fixture passes against the bug it is supposed to catch. Identity
        has to be isolated, and with only a count available that means one
        probe per symbol."""
        conn.execute("DELETE FROM dq_events")
        for sym, ts in events:
            conn.execute("INSERT INTO dq_events VALUES('stale',?,?)", (sym, ts))
        return {r["session"]: r for r in compute(conn, "")[0]}[sess0]["bench_n"]

    # stale DURING session 0 (an ET-afternoon event) -> that name must be cut.
    assert bench_n_with((0, d0 + 18 * 3600)) == base_n - 1, (
        f"a name flagged stale during {sess0} stayed in its benchmark — the screen "
        f"and the session bucket disagree on which day they mean")
    # stale the day BEFORE session 0 -> that name must SURVIVE session 0. This is
    # the half the old code failed: it cut exactly this name instead of the one
    # above, which is why a name-count assertion could not tell them apart.
    assert bench_n_with((1, d0 - day + 18 * 3600)) == base_n, (
        f"a name flagged stale the day BEFORE {sess0} was cut from it — the screen "
        f"is keyed one session early")

    # An ET-EVENING event belongs to the session that just closed, not the next
    # one: 2026-08-25 21:00 ET is 2026-08-26 01:00 UTC, and folding the raw UTC
    # date would file it a day late.
    assert bench_n_with((0, d0 + day + 1 * 3600)) == base_n - 1, (
        "an event at 01:00 UTC (21:00 ET the previous day) did not screen the ET "
        "session it belongs to — the -18000 fold is not being applied to dq_events")
    assert bench_n_with() == base_n, \
        "removing every stale event did not restore the benchmark — the screen is sticky"

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

    # The breadth floor lives in a SEPARATE amendment record. Both must verify.
    amd_json = json.dumps({
        "kind": BENCH_FLOOR_KIND,
        "change": (f"ADDS one eligibility criterion. A session counts only if its "
                   f"benchmark carries at least {MIN_BENCHMARK_NAMES} distinct symbols, "
                   f"IN ADDITION to the registered requirement of at least "
                   f"{MIN_BETS_PER_SESSION} bets.")})
    amd_hash = hashlib.sha256(amd_json.encode()).hexdigest()
    amd_prev = "b58e6f3e"
    amd_note = "AMENDMENT - benchmark breadth floor"
    conn.execute("INSERT INTO prereg_records VALUES(88,?,?,?,?,?,?,?)",
                 (filed_ts + 60, BENCH_FLOOR_KIND, amd_json, amd_hash, amd_prev,
                  _entry_hash(amd_prev, filed_ts + 60, BENCH_FLOOR_KIND, amd_hash, amd_note),
                  amd_note))

    verify_registration(conn)  # both honest records must pass, or the rest proves nothing

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

    # mutation 4: the grader may not apply a floor the chain does not carry. This
    # is the direction that would otherwise go unnoticed -- grading by a stricter
    # rule than the one registered is still grading by an unregistered rule.
    conn.execute("DELETE FROM prereg_records WHERE seq=88")
    refuses("a benchmark floor with no amendment on the chain")

    # mutation 5: an amendment naming a DIFFERENT floor than the grader applies
    other = amd_json.replace(f"at least {MIN_BENCHMARK_NAMES} distinct",
                             f"at least {MIN_BENCHMARK_NAMES * 2} distinct")
    oh = hashlib.sha256(other.encode()).hexdigest()
    conn.execute("INSERT INTO prereg_records VALUES(88,?,?,?,?,?,?,?)",
                 (filed_ts + 60, BENCH_FLOOR_KIND, other, oh, amd_prev,
                  _entry_hash(amd_prev, filed_ts + 60, BENCH_FLOOR_KIND, oh, amd_note),
                  amd_note))
    refuses("an amendment whose floor differs from the one the grader applies")

    conn.execute("DELETE FROM prereg_records WHERE seq=88")
    conn.execute("INSERT INTO prereg_records VALUES(88,?,?,?,?,?,?,?)",
                 (filed_ts + 60, BENCH_FLOOR_KIND, amd_json, amd_hash, amd_prev,
                  _entry_hash(amd_prev, filed_ts + 60, BENCH_FLOOR_KIND, amd_hash, amd_note),
                  amd_note))
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

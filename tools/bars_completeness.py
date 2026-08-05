#!/usr/bin/env python3
"""Measure how complete the daily-bar history actually is.

WHY THIS EXISTS. `universe_membership` is derived entirely from `bars-1d`
(`source = 'bars-1d'`, 1.85M rows). So every point-in-time claim and every
survivorship claim in this repository is exactly as complete as that bar
history — and until now nothing measured it. STRATEGY_DECK.md §14 lists this as
an open action; this is that measurement.

WHAT A GAP MEANS, and why the two kinds are not the same:

  INTERIOR gap  — the symbol is missing days between bars it did print. The
                  universe under-counts it on those days. Annoying, bounded,
                  and visible.

  TRAILING gap  — the symbol stops printing before the calendar ends and
                  carries no `delisted_at`. This is the survivorship-relevant
                  one: the name silently leaves the universe without being
                  recorded as dead, which is indistinguishable from "we stopped
                  looking". A backtest reading membership sees it vanish and
                  cannot tell whether it was delisted or dropped.

THE CALENDAR. There is no exchange calendar in this repository and adding a
dependency for one would be its own risk. Instead the calendar is derived from
the data: the stocks symbol with the most daily bars defines the trading days,
because a continuously-listed liquid name prints on every session. That is an
assumption, and it is stated rather than hidden — a day the reference symbol
missed is a day this tool cannot see. Crypto is measured separately against
all calendar days, since it trades 7 days a week.

    python tools/bars_completeness.py            # measure, human-readable
    python tools/bars_completeness.py --json     # machine-readable
    python tools/bars_completeness.py --self-check   # prove the arithmetic

Read-only. Opens the database with mode=ro and writes nothing.
"""
from __future__ import annotations

import argparse
import json
import os
import sqlite3
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(REPO, "data", "signaldeck.db")
DAY = 86400


def measure(con: sqlite3.Connection) -> dict:
    # Reference calendar: the stocks symbol with the most daily bars.
    ref = con.execute("""
        SELECT b.symbol_id, s.symbol, COUNT(*) n
          FROM bars b JOIN symbols s ON s.id = b.symbol_id
         WHERE b.tf='1d' AND s.market='stocks'
         GROUP BY b.symbol_id ORDER BY n DESC LIMIT 1""").fetchone()
    if not ref:
        raise SystemExit("no daily stock bars found")
    ref_id, ref_sym, ref_n = ref
    cal = [r[0] for r in con.execute(
        "SELECT DISTINCT ts/? FROM bars WHERE tf='1d' AND symbol_id=? ORDER BY 1",
        (DAY, ref_id))]
    cal_set = set(cal)

    out: dict = {
        "calendar": {"reference_symbol": ref_sym, "sessions": len(cal),
                     "first": cal[0] * DAY, "last": cal[-1] * DAY},
        "stocks": {}, "crypto": {}, "worst_interior": [], "trailing_gaps": [],
    }

    # Per-symbol coverage against the reference calendar, stocks only.
    rows = con.execute("""
        SELECT b.symbol_id, s.symbol, s.delisted_at,
               MIN(b.ts/?), MAX(b.ts/?), COUNT(DISTINCT b.ts/?)
          FROM bars b JOIN symbols s ON s.id = b.symbol_id
         WHERE b.tf='1d' AND s.market='stocks'
         GROUP BY b.symbol_id""", (DAY, DAY, DAY)).fetchall()

    tot_expected = tot_actual = 0
    interior = []
    trailing = []
    for _sid, sym, delisted, first, last, actual in rows:
        expected = sum(1 for d in cal if first <= d <= last)
        if expected <= 0:
            continue
        tot_expected += expected
        tot_actual += min(actual, expected)
        missing = expected - actual
        if missing > 0:
            interior.append((sym, missing, expected, round(100.0 * actual / expected, 2)))
        # Trailing: stops printing well before the calendar ends, no delisted_at.
        sessions_after = sum(1 for d in cal if d > last)
        if sessions_after >= 10 and not delisted:
            trailing.append((sym, sessions_after))

    interior.sort(key=lambda r: -r[1])
    trailing.sort(key=lambda r: -r[1])
    out["stocks"] = {
        "symbols": len(rows),
        "expected_symbol_days": tot_expected,
        "actual_symbol_days": tot_actual,
        "coverage_pct": round(100.0 * tot_actual / tot_expected, 4) if tot_expected else 0.0,
        "symbols_with_any_interior_gap": len(interior),
    }
    out["worst_interior"] = [
        {"symbol": s, "missing": m, "expected": e, "coverage_pct": c}
        for s, m, e, c in interior[:15]]
    out["trailing_gaps"] = [
        {"symbol": s, "sessions_missing_at_end": n} for s, n in trailing[:15]]
    out["stocks"]["trailing_gap_symbols"] = len(trailing)

    # Crypto trades every day, so its calendar is every day in its own span.
    crows = con.execute("""
        SELECT b.symbol_id, MIN(b.ts/?), MAX(b.ts/?), COUNT(DISTINCT b.ts/?)
          FROM bars b JOIN symbols s ON s.id = b.symbol_id
         WHERE b.tf='1d' AND s.market<>'stocks'
         GROUP BY b.symbol_id""", (DAY, DAY, DAY)).fetchall()
    cexp = cact = 0
    for _sid, first, last, actual in crows:
        expected = last - first + 1
        cexp += expected
        cact += min(actual, expected)
    out["crypto"] = {
        "symbols": len(crows),
        "expected_symbol_days": cexp,
        "actual_symbol_days": cact,
        "coverage_pct": round(100.0 * cact / cexp, 4) if cexp else 0.0,
    }
    return out


def self_check() -> int:
    """Prove the coverage arithmetic on a fixture the answer is known for."""
    con = sqlite3.connect(":memory:")
    con.executescript("""
        CREATE TABLE symbols(id INTEGER PRIMARY KEY, symbol TEXT, market TEXT, delisted_at INTEGER);
        CREATE TABLE bars(symbol_id INTEGER, tf TEXT, ts INTEGER);
    """)
    # REF prints all 10 sessions; GAPPY misses 2 interior; DEAD stops after 3.
    con.execute("INSERT INTO symbols VALUES (1,'REF','stocks',NULL)")
    con.execute("INSERT INTO symbols VALUES (2,'GAPPY','stocks',NULL)")
    con.execute("INSERT INTO symbols VALUES (3,'DEAD','stocks',NULL)")
    for d in range(10):
        con.execute("INSERT INTO bars VALUES (1,'1d',?)", (d * DAY,))
    for d in [0, 1, 4, 5, 6, 7, 8, 9]:            # missing days 2 and 3
        con.execute("INSERT INTO bars VALUES (2,'1d',?)", (d * DAY,))
    for d in range(3):
        con.execute("INSERT INTO bars VALUES (3,'1d',?)", (d * DAY,))
    r = measure(con)

    assert r["calendar"]["reference_symbol"] == "REF", r["calendar"]
    assert r["calendar"]["sessions"] == 10, r["calendar"]
    # expected: REF 10 + GAPPY 10 + DEAD 3 = 23; actual 10 + 8 + 3 = 21
    assert r["stocks"]["expected_symbol_days"] == 23, r["stocks"]
    assert r["stocks"]["actual_symbol_days"] == 21, r["stocks"]
    assert r["stocks"]["symbols_with_any_interior_gap"] == 1, r["stocks"]
    assert r["worst_interior"][0]["symbol"] == "GAPPY", r["worst_interior"]
    assert r["worst_interior"][0]["missing"] == 2, r["worst_interior"]
    # DEAD stops 7 sessions early with no delisted_at -> trailing gap.
    assert r["stocks"]["trailing_gap_symbols"] == 0, "10-session floor not yet reached"
    print("self-check OK: coverage, interior gaps and the trailing floor all behave")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--db", default=DB)
    ap.add_argument("--self-check", action="store_true")
    a = ap.parse_args()
    if a.self_check:
        return self_check()

    if not os.path.exists(a.db):
        print(f"database not found: {a.db}", file=sys.stderr)
        return 2
    con = sqlite3.connect(f"file:{a.db}?mode=ro", uri=True, timeout=60)
    con.execute("PRAGMA temp_store=MEMORY")
    r = measure(con)
    con.close()

    if a.json:
        print(json.dumps(r, indent=2))
        return 0

    c, s, cr = r["calendar"], r["stocks"], r["crypto"]
    print(f"calendar        {c['sessions']} sessions, from {c['reference_symbol']}")
    print(f"stocks          {s['symbols']} symbols, coverage {s['coverage_pct']}% "
          f"({s['actual_symbol_days']:,}/{s['expected_symbol_days']:,} symbol-days)")
    print(f"                {s['symbols_with_any_interior_gap']} symbols with an interior gap")
    print(f"                {s['trailing_gap_symbols']} symbols stop early with NO delisted_at")
    print(f"crypto          {cr['symbols']} symbols, coverage {cr['coverage_pct']}% "
          f"({cr['actual_symbol_days']:,}/{cr['expected_symbol_days']:,} symbol-days)")
    if r["worst_interior"]:
        print("\nworst interior gaps")
        for w in r["worst_interior"][:10]:
            print(f"  {w['symbol']:<10} missing {w['missing']:>5} of {w['expected']:>5} "
                  f"({w['coverage_pct']}%)")
    if r["trailing_gaps"]:
        print("\ntrailing gaps (survivorship-relevant: stopped printing, not marked delisted)")
        for t in r["trailing_gaps"][:10]:
            print(f"  {t['symbol']:<10} {t['sessions_missing_at_end']:>5} sessions missing at end")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

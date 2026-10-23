#!/usr/bin/env python3
"""survivorship-status v1 (2026-10-23): the listing evidence for every graded symbol, as of a date.

WHY THIS EXISTS BESIDE THE PINNED REGISTRY. tools/accuracy_registry.py (sha256
pinned in the pre-registration chain, never edited) publishes survivorship_clean
from one rule in measure_universe_completeness: a graded symbol with
`active = 0 AND delisted_at IS NULL` is "listing status unresolvable". But
symbols.active is the lean WATCH flag, not a listing status. The 13:15 PT
weekday sweep (ops/signaldeck-refresh.sh) sets it to 1 on every stock and the
prune cuts it back to ~320 names by 60-day dollar volume plus user_symbols. So
the registry's undated-inactive count is mostly still-trading names that are
simply outside the watch set, and the published flag flips with whether the
14:05 grade overlaps the sweep window. That count is NOT a count of delistings.

This measure does not replace or edit the registry, and does not change its
inputs or verdicts; ops/accuracy-registry.sh runs it only AFTER the pinned grade
and a failure here changes nothing there. It reads the same graded population
(accuracy_registry.population_epoch_ts per table) and keeps four things apart:
watch status (symbols.active), universe membership (the graded population),
listing evidence (1d bars, SEC companies), and effective-dated exits
(symbols.delisted_at). Its goal is an honest classification, NOT a clean flag.

CLASSES (decision list over one symbol; the first rule that matches wins)
  RETIRED_SOURCE     market='crypto' with a dated exit (delisted_at <= as_of):
                     the stamp written by `sdmaint void-retired-crypto` records a
                     retired DATA SOURCE, not a listing event.
  UNKNOWN (conflict) a dated exit, but other evidence disagrees: a 1d bar dated
                     after the exit day, or SEC companies mapping the ticker to a
                     national exchange (NYSE, Nasdaq, CBOE, TXSE). Reported with
                     its reasons and never resolved here.
  EXITED_DATED       delisted_at <= as_of with no conflicting evidence.
  WATCHED            active=1 (current as-of only; see LOOK-AHEAD).
  UNWATCHED_TRADING  active=0, no dated exit, a 1d bar within N sessions of
                     as_of: excluded from the lean watch set, still trading.
  TRADING            historical as-of only: no dated exit and a 1d bar within N
                     sessions. Watch status is not recorded point-in-time, so it
                     is not split into WATCHED / UNWATCHED_TRADING.
  UNKNOWN            no dated exit and no 1d bar within N sessions (or no 1d bar
                     at all, or no symbols row). Silence is not an exit and a
                     missing notice is not "still listed".
delisted_at = 0 is read as "no exit" (the Go convention, store.go); the registry
reads only NULL as undated, so its rule is reproduced verbatim in
registry_v1_inactive_undated for comparison.

EXIT KINDS (only where the database holds evidence)
  source_retired        crypto, as above.
  ticker_reuse_archive  the archived pre-reuse row split off by
                        `sdmaint split-reused-tickers` (e.g. ATC.220816).
  otc_after_exit        SEC companies maps the ticker to OTC: consistent with an
                        exchange delisting followed by OTC trading, or with a
                        reused ticker. Not proof of either.
  unattributed          everything else. No Form 25, merger or corporate-action
                        record is persisted, so delisting, merger, ticker change
                        and exchange transfer cannot be told apart and none is
                        inferred.
The exit SOURCE is not recorded (symbols.delisted_at has no source column);
stamp_shapes reports only what the value looks like.

LOOK-AHEAD. Only bars with ts <= as_of, exits with delisted_at <= as_of, graded
rows with ts and resolved_at <= as_of, and SEC rows with updated_ts <= as_of are
used. Exits are EFFECTIVE-dated: the write time of a stamp is not persisted, so a
historical run sees a stamp written later whose effective date is <= as_of (the
delisting detector dates an exit at the last bar, ~42 days before it can know).
symbols.active and companies are current snapshots, which is why watch status
and SEC evidence are used only when they can be known at as_of.

Run:  python tools/survivorship_status.py [--as-of YYYY-MM-DD] [--sessions 5]
          [--db data/signaldeck.db] [--json data/survivorship_status.json]
The database is opened read-only (mode=ro, PRAGMA query_only); nothing is written
to it. Exit 0 with a report, 1 when the database cannot be read.
"""
from __future__ import annotations

import argparse
import collections
import datetime as dt
import json
import os
import pathlib
import re
import sqlite3
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import accuracy_registry  # noqa: E402  read-only reuse: population rule, default DB, sha

MEASURE, VERSION, VERSION_DATE = "survivorship-status", "v1", "2026-10-23"
MEASURE_ID = "%s %s (%s)" % (MEASURE, VERSION, VERSION_DATE)
NATIONAL_EXCHANGES = ("NYSE", "Nasdaq", "CBOE", "TXSE")
GRADED_TABLES = ("prediction_outcomes", "regime_outcomes")
SESSION_REFERENCE = "SPY"
DAY = 86400

DEFINITIONS = {
    "RETIRED_SOURCE": "market='crypto' with delisted_at <= as_of: a retired data source, not a listing event",
    "UNKNOWN": "no dated exit and no 1d bar within N sessions of as_of, OR a dated exit contradicted by "
               "other evidence (reasons listed per symbol); never resolved silently",
    "EXITED_DATED": "delisted_at <= as_of with no contradicting evidence",
    "WATCHED": "active=1 (current as-of only)",
    "UNWATCHED_TRADING": "active=0, no dated exit, a 1d bar within N sessions of as_of (lean-mode exclusion)",
    "TRADING": "historical as-of: no dated exit and a 1d bar within N sessions; watch status is not point-in-time",
}
CLASSES = tuple(DEFINITIONS)


def connect_ro(db):
    """Read-only connection that cannot create, write or migrate the file."""
    uri = pathlib.Path(db).resolve().as_uri() + "?mode=ro"
    con = sqlite3.connect(uri, uri=True, timeout=30)
    con.execute("PRAGMA query_only=1")
    return con


def _has_table(con, name):
    return con.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?",
                       (name,)).fetchone() is not None


def _day(ts):
    return dt.datetime.fromtimestamp(ts, dt.timezone.utc).strftime("%Y-%m-%d") if ts else None


def session_cutoff_day(con, as_of_ts, sessions):
    """Epoch-day of the Nth most recent session <= as_of, from SPY's 1d bars."""
    row = con.execute("SELECT id FROM symbols WHERE symbol=? AND market='stocks'",
                      (SESSION_REFERENCE,)).fetchone()
    if row:
        ts = [t for (t,) in con.execute(
            "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT ?",
            (row[0], as_of_ts, sessions))]
        if len(ts) == sessions:
            return ts[-1] // DAY, "%s 1d bars" % SESSION_REFERENCE
    # ponytail: calendar-day approximation (5 sessions per 7 days + holiday slack)
    # only when the reference symbol has no history; a market calendar if it matters.
    return as_of_ts // DAY - (sessions * 7 // 5 + 3), "calendar-day approximation (no %s 1d bars)" % SESSION_REFERENCE


def classify_symbol(sym, last_bar_ts, post_exit, sec, *, current, cutoff_day, as_of_ts):
    """(class, exit_kind, reasons) for one symbol. Pure: every input is already as-of."""
    if sym is None:
        return "UNKNOWN", None, ["graded symbol_id has no symbols row"]
    dl = sym["delisted_at"]
    if dl and dl <= as_of_ts:
        if sym["market"] == "crypto":
            return "RETIRED_SOURCE", "source_retired", []
        reasons = []
        if post_exit[0]:
            reasons.append("conflict: %d 1d bar(s) dated after the exit day %s, last %s"
                           % (post_exit[0], _day(dl), _day(post_exit[1])))
        if sec and sec["exchange"] in NATIONAL_EXCHANGES:
            reasons.append("conflict: SEC companies maps %s to %s (%s) while delisted_at is %s"
                           % (sym["symbol"], sec["exchange"], sec["name"], _day(dl)))
        if reasons:
            return "UNKNOWN", None, reasons
        if re.search(r"\.\d{6}$", sym["symbol"]) or "ticker later reused" in (sym["name"] or ""):
            kind = "ticker_reuse_archive"
        elif sec and sec["exchange"] == "OTC":
            kind = "otc_after_exit"
        else:
            kind = "unattributed"
        return "EXITED_DATED", kind, []
    if current and sym["active"] == 1:
        return "WATCHED", None, []
    if last_bar_ts is not None and last_bar_ts // DAY >= cutoff_day:
        return ("UNWATCHED_TRADING" if current else "TRADING"), None, []
    if last_bar_ts is None:
        return "UNKNOWN", None, ["no 1d bar at or before as_of and no dated exit"]
    return "UNKNOWN", None, ["no 1d bar since %s (outside the session window) and no dated exit"
                             % _day(last_bar_ts)]


def evaluate(con, as_of, sessions=5, today=None):
    """The full report as a dict. `today` (a date) decides current vs historical."""
    today = today or dt.datetime.now(dt.timezone.utc).date()
    if as_of > today:
        raise ValueError("as_of %s is in the future (today %s)" % (as_of, today))
    if sessions < 1:
        raise ValueError("sessions must be >= 1, got %d" % sessions)
    current = as_of == today
    as_of_ts = int(dt.datetime(as_of.year, as_of.month, as_of.day, tzinfo=dt.timezone.utc).timestamp()) + DAY - 1
    cutoff_day, calendar = session_cutoff_day(con, as_of_ts, sessions)

    syms = {r[0]: {"id": r[0], "symbol": r[1], "market": r[2], "name": r[3], "active": r[4],
                   "added_at": r[5], "delisted_at": r[6]}
            for r in con.execute("SELECT id, symbol, market, name, active, added_at, delisted_at FROM symbols")}
    sec = {}
    if _has_table(con, "companies"):
        for ticker, exch, name, upd in con.execute("SELECT ticker, exchange, name, updated_ts FROM companies"):
            # an SEC row updated after as_of is not knowable at as_of; an undated one
            # is knowable only today, because the table is a current snapshot
            if not ticker or (upd is None and not current) or (upd or 0) > as_of_ts:
                continue
            prev = sec.get(ticker.upper())
            if prev is None or (exch in NATIONAL_EXCHANGES and prev["exchange"] not in NATIONAL_EXCHANGES):
                sec[ticker.upper()] = {"exchange": exch or "", "name": name or ""}

    scopes = collections.OrderedDict()
    for table in GRADED_TABLES:
        if _has_table(con, table):
            ids = {i for (i,) in con.execute(
                "SELECT DISTINCT symbol_id FROM %s WHERE resolved_at IS NOT NULL AND resolved_at <= ? "
                "AND ts >= ? AND ts <= ?" % table,
                (as_of_ts, accuracy_registry.population_epoch_ts(table), as_of_ts))}
            scopes["graded:" + table] = ids
    scopes["universe"] = {i for i, s in syms.items() if (s["added_at"] or 0) <= as_of_ts}

    cache = {}

    def judge(sid):
        if sid not in cache:
            sym = syms.get(sid)
            last = post = None
            if sym is not None:
                last = con.execute("SELECT MAX(ts) FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=?",
                                   (sid, as_of_ts)).fetchone()[0]
                post = (0, None)
                dl = sym["delisted_at"]
                if dl and dl <= as_of_ts:
                    # a bar ON the exit day is the last session (imported stamps are
                    # 00:00Z dates, bars 04:00/05:00Z), so only later days conflict
                    post = con.execute("SELECT COUNT(*), MAX(ts) FROM bars WHERE symbol_id=? AND tf='1d' "
                                       "AND ts >= ? AND ts <= ?",
                                       (sid, (dl // DAY + 1) * DAY, as_of_ts)).fetchone()
            s = sec.get(sym["symbol"].upper()) if sym else None
            cls, kind, reasons = classify_symbol(sym, last, post, s, current=current,
                                                 cutoff_day=cutoff_day, as_of_ts=as_of_ts)
            shape = None
            if kind and sym["market"] != "crypto":
                dl = sym["delisted_at"]
                if con.execute("SELECT 1 FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?", (sid, dl)).fetchone():
                    shape = "equals_last_1d_bar_ts"
                elif dl % DAY == 0:
                    shape = "midnight_utc_date"
                else:
                    shape = "other"
            cache[sid] = (cls, kind, reasons, last, shape, sym)
        return cache[sid]

    out_scopes = collections.OrderedDict()
    for name, ids in scopes.items():
        counts = collections.Counter()
        kinds, shapes, watch = collections.Counter(), collections.Counter(), collections.Counter()
        reg_total, reg_by = 0, collections.Counter()
        unknown = []
        for sid in sorted(ids):
            cls, kind, reasons, last, shape, sym = judge(sid)
            counts[cls] += 1
            if kind:
                kinds[kind] += 1
            if shape:
                shapes[shape] += 1
            if sym is not None:
                watch["not_point_in_time" if not current else
                      ("watched" if sym["active"] == 1 else "unwatched")] += 1
                # the pinned registry's rule, verbatim: NULL-only, active read at run time
                if sym["delisted_at"] is None and sym["active"] == 0:
                    reg_total += 1
                    reg_by[cls] += 1
            if cls == "UNKNOWN":
                unknown.append({"symbol": sym["symbol"] if sym else None, "symbol_id": sid,
                                "market": sym["market"] if sym else None,
                                "active": sym["active"] if sym else None,
                                "delisted_at": _day(sym["delisted_at"]) if sym else None,
                                "last_1d_bar": _day(last), "reasons": reasons})
        out_scopes[name] = {
            "symbols": len(ids),
            "counts": {c: counts.get(c, 0) for c in CLASSES},
            "watch": dict(watch),
            "exit_kinds": dict(kinds),
            "stamp_shapes": dict(shapes),
            "registry_v1_inactive_undated": {"total": reg_total, "by_class": dict(reg_by)},
            "unknown": unknown,
        }

    return {
        "measure": MEASURE, "version": VERSION, "version_date": VERSION_DATE, "measure_id": MEASURE_ID,
        "generated": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "as_of": as_of.isoformat(), "as_of_ts": as_of_ts,
        "mode": "current" if current else "historical",
        "sessions": sessions,
        "session_calendar": {"source": calendar, "cutoff_day": _day(cutoff_day * DAY)},
        "pinned_registry": {
            "file": "tools/accuracy_registry.py", "sha256": accuracy_registry.self_sha256(),
            "relation": "separate measure run after the pinned grade; the registry's inputs, "
                        "verdicts and survivorship_clean are unchanged by it",
            "population_epoch_ts": {t: accuracy_registry.population_epoch_ts(t) for t in GRADED_TABLES},
        },
        "definitions": DEFINITIONS,
        "rules": {
            "order": "RETIRED_SOURCE, UNKNOWN (conflict), EXITED_DATED, WATCHED, "
                     "UNWATCHED_TRADING|TRADING, UNKNOWN; first match wins",
            "delisted_at_zero": "read as no exit (Go convention); the registry reads only NULL as undated",
            "conflicts": "a 1d bar dated after the exit day, or SEC companies mapping the ticker to one of "
                         + ", ".join(NATIONAL_EXCHANGES),
            "exit_kinds": "source_retired, ticker_reuse_archive, otc_after_exit, unattributed; no Form 25, "
                          "merger or corporate-action record is persisted, so none of those is inferred",
            "exit_source": "not recorded: symbols.delisted_at has no source column",
            "look_ahead": "bars, exits, graded rows and SEC rows dated after as_of are excluded; exits are "
                          "effective-dated (stamp write times are not persisted); watch status is used "
                          "only for the current date",
        },
        "scopes": out_scopes,
    }


def summary(report):
    parts = []
    for name, s in report["scopes"].items():
        nz = " ".join("%s=%d" % (c, n) for c, n in s["counts"].items() if n)
        parts.append("%s [%d]: %s" % (name, s["symbols"], nz or "empty"))
    return "%s as of %s (%s): %s" % (report["measure_id"], report["as_of"], report["mode"], "; ".join(parts))


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--db", default=accuracy_registry.DEFAULT_DB)
    ap.add_argument("--as-of", type=dt.date.fromisoformat, default=None, help="YYYY-MM-DD, default today (UTC)")
    ap.add_argument("--sessions", type=int, default=5)
    ap.add_argument("--json", default=None, help="write the report here (atomically); default stdout")
    args = ap.parse_args(argv)
    today = dt.datetime.now(dt.timezone.utc).date()
    try:
        con = connect_ro(args.db)
        try:
            report = evaluate(con, args.as_of or today, args.sessions, today=today)
        finally:
            con.close()
    except (sqlite3.Error, ValueError) as e:
        print("survivorship_status: cannot evaluate (%s): %s" % (args.db, e), file=sys.stderr)
        return 1
    text = json.dumps(report, indent=1, sort_keys=False) + "\n"
    if args.json:
        tmp = args.json + ".tmp"
        with open(tmp, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(text)
        os.replace(tmp, args.json)
        print(summary(report) + " -> " + args.json)
    else:
        sys.stdout.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())

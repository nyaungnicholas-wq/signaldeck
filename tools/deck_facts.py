#!/usr/bin/env python3
"""Measure STRATEGY_DECK.md §8's data figures from the database.

Why: §8 stated `universe_membership`, the `delisted_at` windows and bar-history
completeness as hand-typed figures with a date stamp. On 2026-08-05, one day
after they were typed, every one was already stale — the universe by 0.8M rows,
`delisted_at` by a factor of 2.6, and the survivorship window ratio by enough to
invert its conclusion (15.7% typed, 180% measured). FC1 taught that lesson about
the accuracy record and the fix reached only the accuracy record. This is that
fix, applied to §8.

    python3 tools/deck_facts.py                          # print the block
    python3 tools/deck_facts.py --inject STRATEGY_DECK.md # splice it in
    python3 tools/deck_facts.py --check --inject STRATEGY_DECK.md   # CI gate

Read-only: opens the database with mode=ro and writes nothing to it.
"""
from __future__ import annotations

import argparse
import io
import os
import sqlite3
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import bars_completeness  # noqa: E402  — the bar figures have ONE implementation

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(REPO, "data", "signaldeck.db")

BEGIN = "<!-- BEGIN GENERATED deck_facts -->"
END = "<!-- END GENERATED deck_facts -->"
# The canonical rendering. tools/docs_gate.py enforces that every document's
# generated region equals this file, so it is not an optional convenience: a
# stale partial makes docs_gate fail and blame a hand-edit that never happened.
PARTIAL = os.path.join(REPO, "partials", "deck_facts.md")

# The two windows §8.2 compares: FC3 was narrowed to under-coverage in the
# recent one, and the comparison between them is the disclosure.
EARLIER_WINDOW = (2020, 2022)
RECENT_WINDOW = (2023, 2025)


def _window(con, lo, hi):
    return con.execute(
        "SELECT COUNT(*) FROM symbols WHERE delisted_at IS NOT NULL AND "
        "CAST(strftime('%Y', delisted_at, 'unixepoch') AS INTEGER) "
        "BETWEEN ? AND ?", (lo, hi)).fetchone()[0]


def measure(con):
    """Every §8 figure, from an open connection. Aggregates only — the real
    database is ~5 GB and nothing here may pull a table into Python."""
    rows, days, syms, last_day = con.execute(
        "SELECT COUNT(*), COUNT(DISTINCT day), COUNT(DISTINCT symbol_id), "
        "MAX(day) FROM universe_membership").fetchone()
    # §8.3 asserts every row carries source = 'bars-1d'. That is a claim about
    # the data, so it is measured rather than repeated.
    sources = sorted(r[0] for r in
                     con.execute("SELECT DISTINCT source FROM universe_membership"))

    total = con.execute(
        "SELECT COUNT(*) FROM symbols WHERE delisted_at IS NOT NULL").fetchone()[0]
    earlier = _window(con, *EARLIER_WINDOW)
    recent = _window(con, *RECENT_WINDOW)

    b = bars_completeness.measure(con)
    return {
        "universe": {"rows": rows, "days": days, "symbols": syms,
                     "sources": sources, "last_day": last_day},
        "delistings": {
            "total": total,
            "earlier_window": "%d-%d" % EARLIER_WINDOW, "earlier": earlier,
            "recent_window": "%d-%d" % RECENT_WINDOW, "recent": recent,
            "recent_pct_of_earlier": (round(100.0 * recent / earlier, 1)
                                      if earlier else None),
        },
        "bars": {
            "calendar_sessions": b["calendar"]["sessions"],
            "calendar_reference_symbol": b["calendar"]["reference_symbol"],
            "stocks_symbols": b["stocks"]["symbols"],
            "stocks_expected_symbol_days": b["stocks"]["expected_symbol_days"],
            "stocks_actual_symbol_days": b["stocks"]["actual_symbol_days"],
            "stocks_coverage_pct": b["stocks"]["coverage_pct"],
            "stocks_delisted_symbols": b["stocks"]["delisted_symbols"],
            "stocks_delisted_coverage_pct": b["stocks"]["delisted_coverage_pct"],
            "stocks_live_symbols": b["stocks"]["live_symbols"],
            "stocks_live_coverage_pct": b["stocks"]["live_coverage_pct"],
            "trailing_gap_symbols": b["stocks"]["trailing_gap_symbols"],
            "crypto_symbols": b["crypto"]["symbols"],
            "crypto_coverage_pct": b["crypto"]["coverage_pct"],
        },
    }


def _n(x):
    return "{:,}".format(x)


def render(facts):
    """The block. Deterministic: every date comes from the data, never the
    clock, or --check would fail forever and churn the diff on every run."""
    u, d, b = facts["universe"], facts["delistings"], facts["bars"]
    ratio = ("**%.1f%%** of the %s count" % (d["recent_pct_of_earlier"],
                                             d["earlier_window"])
             if d["recent_pct_of_earlier"] is not None
             else "not comparable — no delistings recorded in %s"
                  % d["earlier_window"])
    asof = (time.strftime("%Y-%m-%d", time.gmtime(u["last_day"]))
            if u["last_day"] else "n/a")
    return "\n".join([
        BEGIN, "",
        "Measured from `data/signaldeck.db` by `tools/deck_facts.py`. Do not edit "
        "by hand — CI fails when this block no longer matches the database. The "
        "universe reaches **%s**, the last observation day it holds." % asof,
        "",
        "| Measurement | Value |",
        "|---|---|",
        "| `universe_membership` rows | %s |" % _n(u["rows"]),
        "| — observation days | %s |" % _n(u["days"]),
        "| — distinct symbols | %s |" % _n(u["symbols"]),
        "| — `source` values present | %s |" % (
            ", ".join("`%s`" % s for s in u["sources"]) or "none"),
        "| `symbols.delisted_at` stamps | %s |" % _n(d["total"]),
        "| — delisted %s | %s |" % (d["earlier_window"], _n(d["earlier"])),
        "| — delisted %s | %s |" % (d["recent_window"], _n(d["recent"])),
        "| — recent window against earlier | %s |" % ratio,
        "| Daily-bar calendar (from `%s`) | %s sessions |" % (
            b["calendar_reference_symbol"], _n(b["calendar_sessions"])),
        "| Stock bar coverage | %.2f%% — %s of %s symbol-days over %s symbols |" % (
            b["stocks_coverage_pct"], _n(b["stocks_actual_symbol_days"]),
            _n(b["stocks_expected_symbol_days"]), _n(b["stocks_symbols"])),
        "| — still-listed names only | %.2f%% over %s symbols |" % (
            b["stocks_live_coverage_pct"], _n(b["stocks_live_symbols"])),
        "| — names carrying `delisted_at` only | %.2f%% over %s symbols |" % (
            b["stocks_delisted_coverage_pct"], _n(b["stocks_delisted_symbols"])),
        "| — symbols that stop printing early with no `delisted_at` | %s |" % (
            _n(b["trailing_gap_symbols"])),
        "| Crypto bar coverage | %.2f%% over %s symbols |" % (
            b["crypto_coverage_pct"], _n(b["crypto_symbols"])),
        "",
        "The membership derives entirely from the daily-bar history, so it is "
        "point-in-time only to the extent that history is complete: the stock "
        "coverage row is the bound under every point-in-time claim in this deck. "
        "**Read the two cohort rows before the blended one.** They answer "
        "different questions — the still-listed row is whether the live universe "
        "has holes, the delisted row is how densely the imported dead names were "
        "ever sampled — and while dead names are being imported the blended "
        "figure moves with the import rather than with data quality. The symbols "
        "that stop printing with no `delisted_at` are the survivorship-relevant "
        "ones: they leave the universe without being recorded as dead, which is "
        "indistinguishable from having stopped looking.",
        "", END,
    ])


def current_block(text):
    """The block a document currently carries, or None if unmarked."""
    i, j = text.find(BEGIN), text.find(END)
    if i < 0 or j < 0 or j < i:
        return None
    return text[i:j + len(END)]


def inject(path, block):
    """Splice block between the marker pair in path. Returns False if absent."""
    with io.open(path, encoding="utf-8") as fh:
        text = fh.read()
    i, j = text.find(BEGIN), text.find(END)
    if i < 0 or j < 0 or j < i:
        print("%s: no %s / %s marker pair" % (path, BEGIN, END), file=sys.stderr)
        return False
    out = text[:i] + block + text[j + len(END):]
    if out != text:
        with io.open(path, "w", encoding="utf-8", newline="") as fh:
            fh.write(out)
    return True


def partial_matches(block):
    """True when PARTIAL already holds this exact block.

    A missing partial reads as drift, not as a crash: --inject creates it, and
    --check must say so rather than dying on the operator's first run.
    """
    try:
        with io.open(PARTIAL, encoding="utf-8") as fh:
            return fh.read().rstrip("\n") == block.rstrip("\n")
    except OSError:
        return False


def main():
    ap = argparse.ArgumentParser(description="Generate §8's measured figures.")
    ap.add_argument("--db", default=DB, help="database (default: %s)" % DB)
    ap.add_argument("--write", default=None, metavar="PATH",
                    help="write the block to PATH")
    ap.add_argument("--inject", nargs="*", default=[], metavar="DOC",
                    help="splice the block into each DOC's marker pair")
    ap.add_argument("--check", action="store_true",
                    help="with --inject, compare only: exit 1 on drift, "
                         "write nothing")
    args = ap.parse_args()

    if not os.path.exists(args.db):
        print("database not found: %s" % args.db, file=sys.stderr)
        return 2
    # Forward slashes: SQLite's URI parser is the one consumer on this path that
    # does not take a Windows separator.
    con = sqlite3.connect("file:%s?mode=ro" % args.db.replace(os.sep, "/"),
                          uri=True, timeout=60)
    try:
        con.execute("PRAGMA temp_store=MEMORY")
        block = render(measure(con))
    finally:
        con.close()

    docs = [p.strip() for p in args.inject if p.strip()]

    # The partial is the canonical rendering of the CANONICAL database, so only
    # a run against that database may write or police it. Without this guard
    # tools/test_deck_facts.py — which injects five times from a fixture db —
    # overwrote partials/deck_facts.md with fixture-scale numbers (6 membership
    # rows, a universe reaching 1970-01-03) and docs_gate then failed on a repo
    # that had merely run its own test suite. A test may not edit the artifact
    # it is testing.
    canonical = os.path.abspath(args.db) == os.path.abspath(DB)

    if args.check:
        rc = 0
        if docs and canonical and not partial_matches(block):
            print("%s: no longer matches the database — run "
                  "python3 tools/deck_facts.py --inject %s" % (PARTIAL, " ".join(docs)),
                  file=sys.stderr)
            rc = 1
        for doc in docs:
            with io.open(doc, encoding="utf-8") as fh:
                cur = current_block(fh.read())
            if cur is None:
                print("%s: no %s / %s marker pair" % (doc, BEGIN, END),
                      file=sys.stderr)
                rc = 1
            elif cur != block:
                print("%s: the measured figures no longer match the database — "
                      "run python3 tools/deck_facts.py --inject %s"
                      % (doc, doc), file=sys.stderr)
                rc = 1
        return rc

    if args.write:
        with io.open(args.write, "w", encoding="utf-8", newline="") as fh:
            fh.write(block + "\n")
    if docs and canonical:
        # The partial is refreshed by the SAME command that injects, so the two
        # cannot diverge. They did: --inject updated STRATEGY_DECK.md and left
        # partials/deck_facts.md stale, --check reported clean because it only
        # ever compared documents, and tools/docs_gate.py was the only thing that
        # noticed — with a message blaming a hand-edit. tools/live_accuracy.py
        # already binds its partial this way; this is that behaviour, here.
        with io.open(PARTIAL, "w", encoding="utf-8", newline="") as fh:
            fh.write(block + "\n")
    for doc in docs:
        if not inject(doc, block):
            return 1
    if not args.write and not docs:
        print(block)
    return 0


if __name__ == "__main__":
    sys.exit(main())

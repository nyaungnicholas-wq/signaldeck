#!/usr/bin/env python3
"""Ticker-reuse split planner for the SignalDeck repository.

Why this exists
---------------
An exchange can recycle a ticker symbol, causing a new company's bars to land on the dead
company's row due to symbols' UNIQUE(symbol, market) constraint. This tool detects the
defect class, computes a precise accounting of affected rows, and emits a JSON plan that the
existing Go command `sdmaint split-reused-tickers` applies through the single-writer
connection in internal/store. This script is strictly READ-ONLY and never writes to the
database.
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import sqlite3
import sys

DEFAULT_DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                          "data", "signaldeck.db")

def connect(path: str) -> sqlite3.Connection:
    """Open a read-only connection with dual locks: URI mode=ro and PRAGMA query_only=ON."""
    con = sqlite3.connect(f"file:{path}?mode=ro", uri=True, timeout=10)
    con.execute("PRAGMA query_only=ON")
    return con

def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--db", default=DEFAULT_DB, help="Path to signaldeck.db")
    ap.add_argument("--out", help="Write JSON plan to this file; without it, print human summary to stdout")
    ap.add_argument("--ticker", action="append", default=[], metavar="SYM=VAL",
                    help="Override historical ticker for a symbol (repeatable)")
    ap.add_argument("--name", action="append", default=[], metavar="SYM=VAL",
                    help="Provide historical issuer name for a symbol (repeatable)")
    ap.add_argument("--max-nudge-days", type=int, default=5,
                    help="Max gap/later days to classify as nudge (default 5)")
    ap.add_argument("--min-reuse-gap-days", type=int, default=30,
                    help="Min gap_days to classify as reuse (default 30)")
    ap.add_argument("--min-reuse-days", type=int, default=5,
                    help="Min later_days to classify as reuse (default 5)")
    args = ap.parse_args()

    # Parse --ticker and --name overrides into dicts
    ticker_overrides: dict[str, str] = {}
    for t in args.ticker:
        sym, val = t.split("=", 1)
        ticker_overrides[sym.upper()] = val
    name_overrides: dict[str, str] = {}
    for n in args.name:
        sym, val = n.split("=", 1)
        name_overrides[sym.upper()] = val

    if not os.path.exists(args.db):
        print(f"database not found: {args.db}", file=sys.stderr)
        return 2

    con = connect(args.db)
    try:
        # Detection query: symbols with delisted_at > 0 and 1d bars after boundary
        hits = con.execute("""
            SELECT s.id, s.symbol, s.market, s.name, s.added_at, s.delisted_at,
                   COUNT(*) AS later_days, MIN(b.ts) AS first_after, MAX(b.ts) AS last_after
            FROM bars b JOIN symbols s ON s.id = b.symbol_id
            WHERE b.tf='1d' AND s.delisted_at > 0
              AND b.ts > (s.delisted_at/86400)*86400 + 86399
            GROUP BY s.id
        """).fetchall()

        # Classify hits
        nudge_list, split_list, review_list = [], [], []
        for row in hits:
            sid, symbol, market, name, added_at, delisted_at, later_days, first_after, last_after = row
            gap_days = (first_after - delisted_at) // 86400
            # A nudge must be judged on the LAST later bar, not the first.
            # `to` is last_after, and Go's NudgeDelisting gates on
            # (to - delisted_at)/86400 > maxGapDays — so classifying on the
            # FIRST bar let one settlement-lag print disarm reuse detection for
            # the whole symbol. Proven on a synthetic case: one bar at +1 day
            # plus four reuse bars at +90..+93 was emitted as
            # "NUDGE ZZZ (gap=1, later=5)" — a 93-day ticker reuse dressed as a
            # settlement lag. Go refuses it, but nudges run AFTER the splits
            # have each committed, so the run aborts with universe_membership
            # left stale. Span is what decides.
            span_days = (last_after - delisted_at) // 86400
            if span_days <= args.max_nudge_days and later_days <= args.max_nudge_days:
                nudge_list.append({
                    "symbol_id": sid, "symbol": symbol,
                    "from": delisted_at, "to": last_after,
                    "gap_days": gap_days, "span_days": span_days,
                    "later_days": later_days
                })
            elif gap_days >= args.min_reuse_gap_days and later_days >= args.min_reuse_days:
                split_list.append({
                    "symbol_id": sid, "symbol": symbol, "market": market,
                    "delisted_at": delisted_at, "first_after": first_after,
                    "gap_days": gap_days, "later_days": later_days,
                    "live_name": name, "historical_added_at": added_at
                })
            else:
                review_list.append({
                    "symbol_id": sid, "symbol": symbol,
                    "delisted_at": delisted_at,
                    "gap_days": gap_days, "later_days": later_days,
                    "why": f"gap={gap_days} days, later={later_days} bars"
                })

        # Gather schema info for per-table accounting and unclassified guard
        schema_tables = [row[0] for row in con.execute(
            "SELECT name FROM sqlite_master WHERE type='table'").fetchall()
        ]
        symbol_tables = set()
        for tbl in schema_tables:
            try:
                cols = [row[1] for row in con.execute(f"PRAGMA table_info({tbl})").fetchall()]
                if "symbol_id" in cols:
                    symbol_tables.add(tbl)
            except sqlite3.OperationalError:
                continue

        # Known categories
        era_tables = {"bars", "breakouts", "confluence_setups", "dq_events", "forecasts",
                      "news", "rankings", "regime_state", "research_weeks", "tv_ratings"}
        spliced_latest = {"research_weeks", "forecasts", "regime_state", "confluence_setups"}
        fit_tables = {"expectancy", "symbol_models"}
        universe_membership = {"universe_membership"}
        no_ts_survivors = {"tv_exchange"}
        news_symbols = {"news_symbols"}

        # Build split plans
        reuse_splits = []
        max_symbol_id = con.execute("SELECT MAX(id) FROM symbols").fetchone()[0]

        for hit in split_list:
            sid = hit["symbol_id"]
            symbol = hit["symbol"]
            delisted_at = hit["delisted_at"]
            boundary = (delisted_at // 86400) * 86400 + 86399
            # Historical ticker: symbol~YYYY (tilde cannot appear in US equity symbol)
            delisted_date = dt.datetime.fromtimestamp(delisted_at, dt.timezone.utc)
            default_historical_ticker = f"{symbol}~{delisted_date.year}"
            historical_ticker = ticker_overrides.get(symbol, default_historical_ticker)
            historical_name = name_overrides.get(symbol, "")
            needs_operator: list[str] = []
            name_evidence: list[str] = []

            if not historical_name:
                needs_operator.append("historical_name")
            # Name evidence from news (up to 5 distinct headlines)
            try:
                evidence_rows = con.execute("""
                    SELECT DISTINCT headline FROM news
                    WHERE symbol_id=? AND ts<=? ORDER BY ts LIMIT 5
                """, (sid, boundary)).fetchall()
                name_evidence = [row[0] for row in evidence_rows if row[0]]
            except sqlite3.OperationalError:
                pass

            # Live_added_at: first 1d bar after boundary
            try:
                live_added_at = con.execute("""
                    SELECT MIN(ts) FROM bars
                    WHERE symbol_id=? AND tf='1d' AND ts > ?
                """, (sid, boundary)).fetchone()[0]
            except sqlite3.OperationalError:
                live_added_at = None

            # Preflight: live_added_at must sit on the LIVE side of the boundary. It is written
            # straight into symbols.added_at, and store.TradableAt tests added_at <= ts, so a
            # zero or pre-boundary value would hand the live security back for every historical
            # date -- the exact survivorship defect the point-in-time universe work closed.
            live_added_at_after = live_added_at is not None and live_added_at > boundary
            if not live_added_at_after:
                print(f"symbol {symbol}: live_added_at {live_added_at} is not after boundary {boundary}",
                      file=sys.stderr)
                con.close()
                return 3

            # Preflight: the ledger hashes symbol_id inside its canonical payload, so re-pointing
            # even one ledger row breaks entry_hash and every link after it. This must be zero;
            # it is asserted rather than assumed.
            ledger_rows = con.execute("""
                SELECT COUNT(*) FROM prediction_ledger WHERE symbol_id=?
            """, (sid,)).fetchone()[0]
            if ledger_rows > 0:
                print(f"symbol {symbol}: prediction_ledger has {ledger_rows} rows (non-zero -> break entry_hash)", file=sys.stderr)
                con.close()
                return 3

            # Preflight: historical_ticker_free
            ticker_exists = con.execute("""
                SELECT 1 FROM symbols WHERE symbol=? AND market=? LIMIT 1
            """, (historical_ticker, hit["market"])).fetchone()
            if ticker_exists:
                print(f"symbol {symbol}: historical ticker {historical_ticker} already exists", file=sys.stderr)
                con.close()
                return 3

            # Active and delisted rows (invariant check)
            active_delisted = con.execute("""
                SELECT COUNT(*) FROM symbols WHERE active=1 AND delisted_at>0
            """).fetchone()[0]

            # Per-table accounting
            tables = {}
            unclassified_tables: dict[str, int] = {}
            totals = {"total": 0, "move": 0, "stay": 0, "delete": 0, "rebuild": 0}
            # Counted over EVERY symbol_id table before any categorisation, so the accounting
            # below can be checked against the database rather than against itself.
            swept_total = 0

            for tbl in sorted(symbol_tables):
                total = con.execute(f"SELECT COUNT(*) FROM {tbl} WHERE symbol_id=?", (sid,)).fetchone()[0]
                if total == 0:
                    continue
                swept_total += total

                move = stay = delete = rebuild = 0
                predicate = ""

                if tbl in era_tables:
                    # ERA: move rows with ts <= boundary
                    move = con.execute(f"SELECT COUNT(*) FROM {tbl} WHERE symbol_id=? AND ts<=?", (sid, boundary)).fetchone()[0]
                    predicate = f"ts<={boundary}"
                    if tbl in spliced_latest:
                        # Also delete rows with ts > boundary from the live id
                        delete = con.execute(f"SELECT COUNT(*) FROM {tbl} WHERE symbol_id=? AND ts>?", (sid, boundary)).fetchone()[0]
                        category = "era+spliced-latest"
                    else:
                        stay = total - move
                        category = "era"
                elif tbl in fit_tables:
                    # No timestamp to partition by, and no honest attribution: these were
                    # computed FROM both securities. Deleted wholesale; both workers rebuild
                    # them from bars on their next pass.
                    delete = total
                    predicate = f"symbol_id={sid} (every row)"
                    category = "fit"
                elif tbl in universe_membership:
                    # Derived from bars, so it is re-derived rather than re-pointed. Moving it
                    # would also miss the live security's own days, which are absent today only
                    # because delisted_at precedes them.
                    rebuild = total
                    predicate = "derived from bars-1d; rebuilt by sdmaint split-reused-tickers"
                    category = "rebuild"
                elif tbl in no_ts_survivors:
                    # Resolved against the security trading under the ticker today, which is the
                    # one keeping the row.
                    stay = total
                    predicate = "no time column; stays with the live row"
                    category = "no-ts"
                elif tbl in news_symbols:
                    # Partition by article ts (via news table)
                    move = con.execute("""
                        SELECT COUNT(*) FROM news_symbols
                        WHERE symbol_id=? AND news_id IN (
                            SELECT id FROM news WHERE ts<=?
                        )
                    """, (sid, boundary)).fetchone()[0]
                    stay = total - move
                    predicate = f"news_id IN (SELECT id FROM news WHERE ts<={boundary})"
                    category = "news_symbols"
                else:
                    # Unclassified: row exists in symbol_id column but not in any category
                    unclassified_tables[tbl] = total
                    continue

                tables[tbl] = {
                    "total": total, "move": move, "stay": stay,
                    "delete": delete, "rebuild": rebuild,
                    "predicate": predicate, "category": category
                }
                totals["total"] += total
                totals["move"] += move
                totals["stay"] += stay
                totals["delete"] += delete
                totals["rebuild"] += rebuild

            # A schema that grew a table the applier does not know about makes this plan an
            # UNDERCOUNT, not a smaller plan: those rows would keep pointing at the live row
            # while describing the dead company. Refuse rather than silently drop them.
            if unclassified_tables:
                print(f"symbol {symbol}: {len(unclassified_tables)} table(s) carry symbol_id rows "
                      f"that no category covers: {unclassified_tables}", file=sys.stderr)
                print("the applier would leave these behind -- classify them before planning",
                      file=sys.stderr)
                con.close()
                return 3

            # SELF-CHECK. Every row the sweep found must land in exactly one bucket: the
            # per-table split has to partition, the totals have to be the sums, and the grand
            # total has to equal what the sweep counted. Nothing lost, nothing double-counted.
            for tbl, data in tables.items():
                assert data["total"] == data["move"] + data["stay"] + data["delete"] + data["rebuild"], \
                    f"{symbol}/{tbl} does not partition: {data}"
            for key in ("total", "move", "stay", "delete", "rebuild"):
                assert totals[key] == sum(d[key] for d in tables.values()), \
                    f"{symbol}: {key} total disagrees with its per-table sum"
            assert totals["total"] == totals["move"] + totals["stay"] + totals["delete"] + totals["rebuild"], \
                f"{symbol}: totals do not partition: {totals}"
            assert totals["total"] == swept_total, \
                f"{symbol}: accounted {totals['total']} rows but the sweep found {swept_total}"

            # Build split entry
            split_entry = {
                "symbol_id": sid, "symbol": symbol, "historical_ticker": historical_ticker,
                "historical_name": historical_name, "delisted_at": delisted_at,
                "live_added_at": live_added_at, "gap_days": hit["gap_days"],
                "later_days": hit["later_days"], "market": hit["market"],
                "boundary": boundary,
                "live_name": hit["live_name"], "historical_added_at": hit["historical_added_at"],
                "name_evidence": name_evidence, "needs_operator": needs_operator,
                "tables": tables, "totals": totals,
                "unclassified_tables": unclassified_tables,
                "preflight": {
                    "prediction_ledger_rows": ledger_rows,
                    "historical_ticker_free": not ticker_exists,
                    "live_added_at_after_boundary": live_added_at_after,
                    "active_and_delisted_rows": active_delisted,
                    "max_symbol_id": max_symbol_id
                }
            }
            reuse_splits.append(split_entry)

    finally:
        con.close()

    # Build final plan
    plan = {
        "generated": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "db": os.path.abspath(args.db),
        "reuse_splits": reuse_splits,
        "stamp_nudges": nudge_list,
        "needs_review": review_list
    }

    # Output
    if args.out:
        with open(args.out, "w", encoding="utf-8") as f:
            json.dump(plan, f, indent=2)
            f.write("\n")
        print(f"plan written to {args.out}", file=sys.stderr)
    else:
        # Human summary
        if reuse_splits or nudge_list or review_list:
            for split in reuse_splits:
                boundary_date = dt.datetime.fromtimestamp(split["boundary"], dt.timezone.utc).strftime("%Y-%m-%d")
                print(f"Symbol: {split['symbol']} (id {split['symbol_id']})")
                print(f"  Gap: {split['gap_days']} days, later bars: {split['later_days']}")
                print(f"  Boundary: {split['boundary']} (UTC {boundary_date})")
                print(f"  Historical ticker: {split['historical_ticker']}")
                if split["historical_name"]:
                    print(f"  Historical name: {split['historical_name']}")
                else:
                    print(f"  Historical name: (needs operator)")
                if split["needs_operator"]:
                    print(f"  Needs operator: {', '.join(split['needs_operator'])}")
                print("  Tables:")
                for tbl, data in split["tables"].items():
                    print(f"    {tbl:25s} total={data['total']:5d} move={data['move']:5d} "
                          f"stay={data['stay']:5d} delete={data['delete']:5d} rebuild={data['rebuild']:5d}")
                print(f"    {'TOTAL':25s} total={split['totals']['total']:5d} move={split['totals']['move']:5d} "
                      f"stay={split['totals']['stay']:5d} delete={split['totals']['delete']:5d} rebuild={split['totals']['rebuild']:5d}")
                print()
            if nudge_list:
                print("Stamp nudges:")
                for nudge in nudge_list:
                    print(f"  {nudge['symbol']} (id {nudge['symbol_id']}): "
                          f"{nudge['from']} -> {nudge['to']} (gap={nudge['gap_days']}, later={nudge['later_days']})")
                print()
            if review_list:
                print("Needs review:")
                for rev in review_list:
                    print(f"  {rev['symbol']} (id {rev['symbol_id']}): {rev['why']}")
                print()
            print("Next command:")
            print(f"  ./bin/sdmaint split-reused-tickers -db {args.db} -plan <plan.json> -dry-run")
        else:
            print("no ticker reuse detected")

    return 0

if __name__ == "__main__":
    sys.exit(main())
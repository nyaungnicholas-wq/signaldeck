#!/usr/bin/env python3
"""Tell "the structural resolver is waiting" apart from "it has silently died".

WHY THIS EXISTS
---------------
RegimeOutcomeWorker grades a frozen regime_outcomes row only once BOTH gates
open: the calendar gate (ts + horizon_days*1.45 days elapsed) and the bar gate
(horizon_days newer daily bars have actually printed). Until then it logs
"resolved 0" and exits green, which is correct.

The problem is that a resolver which has STOPPED working logs exactly the same
line. On 2026-08-17 the first structural outcomes come due; if nothing grades
them, the daemon stays green and the failure is invisible until someone happens
to query the table. Two weeks of silence looks identical to two weeks of
progress.

So this check asks the one question the worker's own summary cannot: is there a
row that has passed BOTH gates and still has no verdict? That row is not
waiting. Something is wrong with it.

WHAT IT REFUSES TO CRY WOLF ABOUT
---------------------------------
Two kinds of row legitimately never resolve, and a check that fires on them is
noise - which is how a real outage gets ignored:

  * QUARANTINED rows carry no frozen naive baseline and are ungradable by
    doctrine. They are excluded outright.
  * DEGENERATE windows (a tied median, a NaN) are declined by the worker on
    purpose - "no honest grade" is the right answer, not a bug. These cannot be
    identified from the database alone, so they are handled statistically: a
    handful of ungraded rows beside a healthy resolved population reads OK, and
    only a kind that has resolved NOTHING (DEAD-ARM) or has left most of its due
    rows ungraded (STALLED) is called a failure.

This check never writes. It does not repair a row, backfill a label, or grade
anything - it reads, and it reports.

Exit codes: 0 = every kind is waiting or grading normally; 1 = at least one kind
is DEAD-ARM or STALLED; 2 = the check itself could not run.

Run: python3 tools/structural_liveness.py [--db PATH] [--now UNIXTS] [--json]
"""
import argparse
import datetime as dt
import json
import os
import sqlite3
import sys
import time

DEFAULT_DB = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "data", "signaldeck.db")

# The worker ticks every 6h. A row that came due minutes ago is not an outage,
# and firing on it would make this check flap twice a day forever.
GRACE_DAYS = 2

# Above this share of due rows left ungraded, "degenerate tail" stops being a
# credible explanation. A resolver declining a tied median here and there is
# normal; one declining a fifth of everything is not deciding, it is failing.
STALL_RATIO = 0.20

FAILING = ("DEAD-ARM", "STALLED")


def _has_table(con: sqlite3.Connection, name: str) -> bool:
    return con.execute(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?",
        (name,)).fetchone() is not None


def overdue_rows(con: sqlite3.Connection, now: int) -> list[dict]:
    """Unresolved rows that cleared both gates plus the grace window.

    The forward-bar count is deliberately a correlated subquery rather than a
    join: the gate is "bars newer than THIS call", which differs for every row,
    and bars(symbol_id, tf, ts) is indexed so each one is a range scan.
    """
    # A database predating the quarantine migration must still be checkable, so
    # the exclusion is added only when the table is actually there. Probing
    # sqlite_master is deliberate: catching an OperationalError mid-query would
    # also swallow a genuine SQL fault.
    exclude = ""
    if _has_table(con, "regime_outcome_quarantine"):
        exclude = ("AND ro.id NOT IN "
                   "(SELECT outcome_id FROM regime_outcome_quarantine)")

    rows = con.execute(f"""
        SELECT ro.id, ro.symbol_id, ro.kind, ro.ts, ro.horizon_days,
               (SELECT COUNT(*) FROM bars b
                 WHERE b.symbol_id = ro.symbol_id AND b.tf = '1d'
                   AND b.ts > ro.ts) AS forward_bars
        FROM regime_outcomes ro
        WHERE ro.resolved_at IS NULL
          AND ro.ts + CAST(ro.horizon_days * 1.45 * 86400 AS INTEGER)
              + {GRACE_DAYS * 86400} <= ?
          AND (SELECT COUNT(*) FROM bars b
                WHERE b.symbol_id = ro.symbol_id AND b.tf = '1d'
                  AND b.ts > ro.ts) >= ro.horizon_days
          {exclude}
        ORDER BY ro.ts ASC""", (now,)).fetchall()

    out = []
    for oid, sym, kind, ts, hd, fwd in rows:
        out.append({
            "id": oid, "symbol_id": sym, "kind": kind, "ts": ts,
            "horizon_days": hd, "forward_bars": fwd,
            # Measured from the calendar due date, not from the end of grace:
            # "9 days overdue" should mean nine days past when it should have
            # graded, not nine days past when we agreed to start complaining.
            "days_overdue": (now - (ts + int(hd * 1.45 * 86400))) // 86400,
        })
    return out


def per_kind_status(con: sqlite3.Connection, now: int,
                    overdue: list[dict] | None = None) -> dict[str, dict]:
    """Per-kind counts and a verdict. Pass `overdue` to reuse a computed list."""
    if overdue is None:
        overdue = overdue_rows(con, now)

    kinds: dict[str, dict] = {}
    for kind, total, resolved in con.execute("""
            SELECT kind, COUNT(*),
                   SUM(CASE WHEN resolved_at IS NOT NULL THEN 1 ELSE 0 END)
            FROM regime_outcomes GROUP BY kind"""):
        kinds[kind] = {"kind": kind, "total": total,
                       "resolved": resolved or 0, "overdue": 0}

    for r in overdue:
        if r["kind"] in kinds:
            kinds[r["kind"]]["overdue"] += 1

    for s in kinds.values():
        od, res = s["overdue"], s["resolved"]
        if od == 0 and res == 0:
            s["verdict"] = "WAITING"      # nothing due yet - the expected state
        elif od == 0:
            s["verdict"] = "OK"
        elif res == 0:
            s["verdict"] = "DEAD-ARM"     # due rows, never graded one: no arm
        elif od / (od + res) > STALL_RATIO:
            s["verdict"] = "STALLED"      # too many ungraded to be a tied tail
        else:
            s["verdict"] = "OK"           # small degenerate tail, still grading
    return kinds


def _date(ts: int) -> str:
    return dt.datetime.fromtimestamp(ts, dt.timezone.utc).strftime("%Y-%m-%d")


def _print_report(con: sqlite3.Connection, status: dict[str, dict],
                  overdue: list[dict], now: int) -> None:
    # ASCII only: this prints to a cp1252 console on Windows.
    hdr = f"{'kind':<22}{'total':>7}{'resolved':>10}{'overdue':>9}  verdict"
    print(hdr)
    print("-" * len(hdr))
    for kind in sorted(status):
        s = status[kind]
        print(f"{s['kind']:<22}{s['total']:>7}{s['resolved']:>10}"
              f"{s['overdue']:>9}  {s['verdict']}")

    by_kind: dict[str, list[dict]] = {}
    for r in overdue:
        if status.get(r["kind"], {}).get("verdict") in FAILING:
            by_kind.setdefault(r["kind"], []).append(r)

    for kind in sorted(by_kind):
        rows = by_kind[kind]
        print(f"\n{kind}: {len(rows)} row(s) past both gates with no verdict"
              f" (showing up to 10)")
        print(f"  {'id':>8}{'symbol':>8}  {'called':<12}{'fwd_bars':>9}"
              f"{'days_late':>10}")
        for r in rows[:10]:
            print(f"  {r['id']:>8}{r['symbol_id']:>8}  {_date(r['ts']):<12}"
                  f"{r['forward_bars']:>9}{r['days_overdue']:>10}")

    # A quiet report should say WHY it is quiet. "Nothing is overdue" and "the
    # system is on schedule" are different claims, and only the second one tells
    # a reader that silence is expected rather than suspicious.
    if not overdue and all(s["verdict"] == "WAITING" for s in status.values()):
        row = con.execute("""
            SELECT MIN(ts + CAST(horizon_days * 1.45 * 86400 AS INTEGER))
            FROM regime_outcomes WHERE resolved_at IS NULL""").fetchone()
        if row and row[0]:
            days = (row[0] - now) / 86400.0
            print(f"\nNothing is due yet. The earliest unresolved call can first"
                  f" be graded on {_date(row[0])} ({days:+.1f} days).")
        else:
            print("\nNo unresolved structural outcomes exist.")


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(
        description="Check whether the structural resolver is waiting or dead. "
                    "Read-only; never writes to the database.")
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--now", type=int, default=None,
                    help="unix seconds; defaults to the current time")
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args(argv)

    if not os.path.exists(args.db):
        print(f"no such database: {args.db}", file=sys.stderr)
        return 2

    now = args.now if args.now is not None else int(time.time())

    try:
        con = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    except sqlite3.OperationalError as e:
        print(f"cannot open database read-only: {e}", file=sys.stderr)
        return 2

    try:
        # Computed once and threaded through: the forward-bar subquery is the
        # expensive part, and the report and the verdicts want the same answer.
        overdue = overdue_rows(con, now)
        status = per_kind_status(con, now, overdue)

        if args.json:
            print(json.dumps({"now": now, "kinds": status, "overdue": overdue},
                             indent=2))
        else:
            _print_report(con, status, overdue, now)

        # Computed from the data, NOT accumulated inside a print loop: the exit
        # code must be identical whether or not the table was rendered.
        return 1 if any(s["verdict"] in FAILING for s in status.values()) else 0
    finally:
        con.close()


if __name__ == "__main__":
    sys.exit(main())

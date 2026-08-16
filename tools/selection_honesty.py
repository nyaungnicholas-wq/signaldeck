#!/usr/bin/env python3
"""Refuse an accuracy that is just a one-sided book's own base rate.

THE DEFECT (measured on the live DB, 2026-08-15). The calibrator squashed the
whole cross-section below 0.5 -- 1w on 2026-08-03 had every one of 325 symbols
in [0.104, 0.381] -- so the hard `prob >= 0.5` threshold called DOWN on the
entire book. Live accuracy then landed on 1 - null by ARITHMETIC, not by
anti-skill: 43.3% against a 56.5% null, published as if it were 2,430 forecasts.

The same shape inverts, and that version LOOKS LIKE A WIN: a book calling UP on
everything in a rising market reads 61.4% against a 60.1% null. Accuracy alone
cannot tell the two apart from a real forecaster; you need the direction of the
calls and the distance to the null together.

WHY THIS IS A SEPARATE TOOL, not a patch to accuracy_registry.py: that grader's
sha256 is pinned in the database's pre-registration chain, and it refuses to run
when its own hash changes. That gate is correct -- the code deciding verdicts
must be the code the chain froze -- so this ships beside it instead. Nothing
here reads or changes a verdict or a retire flag; it is disclosure only.

Usage:
    python tools/selection_honesty.py                     # gate the live registry
    python tools/selection_honesty.py --json path.json    # gate a snapshot
Exit 1 if any directional row's accuracy is explained by a one-sided selection.
"""
from __future__ import annotations

import argparse
import json
import os
import sqlite3
import sys
import textwrap

from skillpower import skill_resolvable

ONE_SIDED_AGREEMENT = 0.90   # measured: healthy days 0.75-0.86, broken 0.95-1.00
SELECTION_TOL = 0.02         # "accuracy IS the null" to within 2pp
MIN_ROWS_PER_DAY = 20        # a day thinner than this is not a cross-section
HIGH_CONVICTION_EDGE = 0.15  # the registry's own band: |p-0.5| >= 0.15

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_DB = os.path.join(HERE, "..", "data", "signaldeck.db")
DEFAULT_JSON = os.path.join(HERE, "..", "data", "accuracy_registry.json")


def verdict(acc, null_acc, agreement, calls_up):
    """Return {publishable, one_sided, reason}. Pure; no I/O.

    One-sidedness ALONE is not the defect -- a concentrated book can be
    legitimately one-sided and still carry real skill. The defect is being
    one-sided AND pinned to the null (or its complement). Refusing on agreement
    alone would be red-by-construction, and a guard that always fires is a guard
    everyone learns to ignore.
    """
    if acc is None or null_acc is None or agreement is None:
        return None
    one_sided = agreement > ONE_SIDED_AGREEMENT
    near_null = abs(acc - null_acc) <= SELECTION_TOL
    near_complement = abs(acc - (1.0 - null_acc)) <= SELECTION_TOL
    if not (one_sided and (near_null or near_complement)):
        return {"publishable": True, "one_sided": one_sided, "reason": ""}
    side = "UP" if (calls_up is not None and calls_up > 0.5) else "DOWN"
    which = "the null itself" if near_null else "1 - null (its complement)"
    # Report the share pointing THAT way. Printing calls_up next to the word
    # DOWN reads as "DOWN 40.4%" when 40.4% is the UP share.
    pct = ("" if calls_up is None
           else f" on {max(calls_up, 1 - calls_up):.1%} of graded rows")
    return {
        "publishable": False,
        "one_sided": True,
        "reason": (
            f"accuracy {acc:.4f} IS {which} ({null_acc:.4f}) to within "
            f"{SELECTION_TOL:.2f}, on a book calling {side}{pct}, with mean daily "
            f"agreement {agreement:.3f}. That is ONE market call replicated "
            f"across the cross-section, not independent forecasts: the number "
            f"measures the base rate of a one-sided selection, not forecast "
            f"quality. Read day_bet instead."
        ),
    }


def calls_up_by_horizon(db_path):
    """Fraction of graded calls pointing UP, per horizon, from the live record.

    The registry publishes agreement but not DIRECTION, and agreement is the
    same number whether the book called up on everything or down on everything.
    Deduped to one row per (symbol, horizon, UTC day) to match the registry's
    independence collapse. Read-only: safe against the running daemon.
    """
    out = {}
    if not os.path.exists(db_path):
        return out
    con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    try:
        rows = con.execute("""
            WITH d AS (
              SELECT horizon, prob,
                     ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon,
                       date(ts,'unixepoch') ORDER BY ts DESC) rn
              FROM prediction_outcomes
              WHERE up IS NOT NULL AND prob IS NOT NULL
            )
            SELECT horizon, AVG(CASE WHEN prob >= 0.5 THEN 1.0 ELSE 0.0 END)
            FROM d WHERE rn = 1 GROUP BY horizon""").fetchall()
        out = {h: v for h, v in rows if v is not None}
    finally:
        con.close()
    return out


def day_tallies_by_horizon(db_path, min_edge=0.0):
    """Per-DAY (n, hits, null_hits) per horizon, for the day-blocked skill test.

    `min_edge` selects the conviction band the registry published: 0.0 is the "all"
    band, 0.15 is `|p-0.5|>=0.15`. A band row MUST be judged on its own rows - the
    high-conviction subset is smaller and its interval is wider, so borrowing the
    full book's tallies would understate exactly the uncertainty being tested.

    Same dedup as calls_up_by_horizon. null_hits uses the registry's own prequential
    majority: day d is called by the majority over days STRICTLY BEFORE d, so day 0
    contributes exactly zero lift. Read-only: safe against the running daemon.
    """
    out = {}
    if not os.path.exists(db_path):
        return out
    con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    try:
        rows = con.execute("""
            WITH d AS (
              SELECT horizon, date(settle_ts,'unixepoch') day, prob, up,
                     ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon,
                       date(settle_ts,'unixepoch') ORDER BY ts DESC) rn
              FROM prediction_outcomes
              WHERE up IS NOT NULL AND prob IS NOT NULL AND settle_ts IS NOT NULL
            )
            SELECT horizon, day, COUNT(*),
                   SUM(CASE WHEN (prob >= 0.5) = (up = 1) THEN 1 ELSE 0 END),
                   SUM(up)
            FROM d WHERE rn = 1 AND ABS(prob - 0.5) >= ?
            GROUP BY horizon, day ORDER BY horizon, day""", (min_edge,)).fetchall()
    finally:
        con.close()

    for h, _day, n, hits, ups in rows:
        acc = out.setdefault(h, {"tallies": [], "cum_up": 0, "cum_n": 0})
        if n < MIN_ROWS_PER_DAY:
            continue
        if acc["cum_n"] == 0:
            null_hits = hits          # no prior day: zero lift by construction
        else:
            null_hits = ups if (acc["cum_up"] / acc["cum_n"]) >= 0.5 else n - ups
        acc["tallies"].append((n, float(hits), float(null_hits)))
        acc["cum_up"] += ups
        acc["cum_n"] += n
    return {h: v["tallies"] for h, v in out.items()}


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--json", default=DEFAULT_JSON)
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--merge", action="store_true",
                    help="write the honesty block back into the registry JSON")
    a = ap.parse_args(argv)

    with open(a.json, encoding="utf-8") as fh:
        reg = json.load(fh)
    ups = calls_up_by_horizon(a.db)
    # one tally set per published conviction band, keyed the way the rows are
    tallies = {0.0: day_tallies_by_horizon(a.db, 0.0),
               HIGH_CONVICTION_EDGE: day_tallies_by_horizon(a.db, HIGH_CONVICTION_EDGE)}

    refused = 0
    merged = 0
    unsupported = 0
    for r in reg.get("rows", []):
        if r.get("family") != "direction":
            continue
        b = r.get("breadth") or {}
        name = r["predictor"]
        horizon = "1w" if "1w" in name else "1d"
        cu = ups.get(horizon)
        v = verdict(r.get("live_acc"), r.get("null_acc"),
                    b.get("mean_daily_agreement"), cu)

        # A SEPARATE failure from one-sidedness: the row may carry a significance
        # verdict ("FAILED - significantly worse than the naive baseline") that its
        # day count cannot support. Rows within a day are one market move, and the
        # null is estimated on the same short window, so both sides carry sampling
        # error. Test the PAIRED difference, blocking by day.
        edge = HIGH_CONVICTION_EDGE if "high conviction" in name else 0.0
        res = skill_resolvable(tallies[edge].get(horizon, []))
        if not res["resolvable"]:
            unsupported += 1
            print(f"  UNSUPPORTED  {name}: {res['reason']}")

        if a.merge and v is not None:
            v = dict(v, resolvability=res)
        if a.merge and v is not None:
            # Post-process the ARTIFACT, never the grader. accuracy_registry.py's
            # sha256 is pinned in the prereg chain and it refuses to run when its
            # own hash changes -- correctly, since the code deciding verdicts must
            # be the code the chain froze. Editing the JSON it already produced
            # changes no verdict and no hash.
            r["honesty"] = dict(v, calls_up=cu, source="tools/selection_honesty.py")
            merged += 1
        if v is None:
            print(f"  --  {name}: no breadth tallies, cannot judge")
            continue
        if v["publishable"]:
            # Surface one-sidedness even when the row is not refused: an
            # agreement of 0.92 still means the evidence is the day count.
            warn = "  [ONE-SIDED: evidence is the day count, not the row count]"                 if v["one_sided"] else ""
            print(f"  OK  {name}: acc {r['live_acc']:.4f} vs null "
                  f"{r['null_acc']:.4f}, agreement "
                  f"{b.get('mean_daily_agreement'):.3f}{warn}")
            continue
        refused += 1
        print(f"  REFUSED  {name}")
        for ln in textwrap.wrap(v["reason"], 96):
            print(f"           {ln}")
    if a.merge:
        # Post-process the ARTIFACT, never the grader. accuracy_registry.py's
        # sha256 is pinned in the prereg chain and it refuses to run when its
        # own hash changes -- correctly, since the code deciding verdicts must
        # be the code the chain froze. Rewriting the JSON it already emitted
        # changes no verdict, no threshold and no hash.
        with open(a.json, "w", encoding="utf-8") as fh:
            json.dump(reg, fh, indent=1)
        print(f"merged honesty into {merged} row(s) of {a.json}")
    print(f"\n{refused} row(s) refused: accuracy explained by a one-sided selection")
    print(f"{unsupported} row(s) carry a significance verdict their day count "
          f"cannot support")
    return 1 if refused else 0


if __name__ == "__main__":
    sys.exit(main())

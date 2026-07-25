#!/usr/bin/env python3
"""Accuracy registry — grade every predictor's CLAIM against its LIVE record.

Why this exists
---------------
Every predictor in SignalDeck ships an accuracy number. Some are backtested claims
that have never been graded on live forward data, and one of them — the directional
ensemble — has now accumulated enough live resolutions to show it is significantly
WORSE than a coin flip while still presenting itself as a prediction.

A claimed accuracy that nothing checks is not an accuracy, it is a decoration. This
enumerates every predictor and answers one question per row: does the live record
support the number being displayed?

It is strictly READ-ONLY (sqlite `mode=ro`) so it can run against the live database
while the daemon is writing.

Discipline enforced here, learned from the failures this repo already found:
  * INDEPENDENT observations only. Intraday predictions that map to the same forward
    move are collapsed to one row per (symbol, horizon, UTC-day), keeping the latest.
    Pooling them inflates n by ~60x and produces confident nonsense.
  * Per-BAND accuracy, never the population average. A low-conviction forecast quoting
    the all-decisions number is how "83%" ends up attached to a coin flip.
  * Wilson intervals, and a verdict driven by the interval — not the point estimate.
  * PENDING is a real verdict. A forecast whose horizon has not elapsed is not
    evidence, and saying so is the point.

Usage:  python3 tools/accuracy_registry.py [--db PATH] [--json OUT]
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import os
import sqlite3
import sys

DEFAULT_DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                          "data", "signaldeck.db")

# Below this many independent observations no verdict is claimed either way.
MIN_INDEPENDENT_N = 30

# Conviction bands. A predictor's accuracy is only meaningful within its band.
BANDS = [(0.0, 0.5, "all"), (0.5, 0.8, "conv>0.5"), (0.8, 0.9, "conv>0.8"), (0.9, 1.01, "conv>0.9")]


def wilson(k: int, n: int, z: float = 1.96) -> tuple[float, float]:
    """Wilson score interval — behaves at small n and near 0/1, unlike normal approx."""
    if n <= 0:
        return (0.0, 0.0)
    p = k / n
    d = 1 + z * z / n
    centre = (p + z * z / (2 * n)) / d
    half = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / d
    return (max(0.0, centre - half), min(1.0, centre + half))


def connect(path: str) -> sqlite3.Connection:
    if not os.path.exists(path):
        sys.exit(f"database not found: {path}")
    return sqlite3.connect(f"file:{path}?mode=ro", uri=True)


# --------------------------------------------------------------------------- #
# Directional ensemble — the one predictor with a real live record
# --------------------------------------------------------------------------- #

def grade_directional(con: sqlite3.Connection) -> list[dict]:
    """Grade prediction_outcomes on independent (symbol, horizon, UTC-day) rows."""
    rows = []
    q = """
    WITH dedup AS (
      SELECT symbol_id, horizon, prob, up,
             ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon, ts/86400
                                ORDER BY ts DESC) rn
      FROM prediction_outcomes
      WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND prob IS NOT NULL
    )
    SELECT horizon,
           COUNT(*),
           SUM(CASE WHEN (prob >= 0.5) = (up = 1) THEN 1 ELSE 0 END),
           AVG(CASE WHEN up = 1 THEN 1.0 ELSE 0.0 END)
    FROM dedup WHERE rn = 1 GROUP BY horizon
    """
    for horizon, n, hits, base in con.execute(q):
        # The honest null for a directional call is the best constant guess — always
        # predicting the majority class. Beating 50% means nothing if up-days are 55%.
        null_acc = max(base, 1 - base)
        lo, hi = wilson(hits, n)
        rows.append({
            "predictor": f"directional-ensemble ({horizon})",
            "family": "direction",
            "band": "all",
            "claimed": None,
            "live_n": n,
            "live_acc": hits / n if n else None,
            "ci": [lo, hi],
            "null_acc": null_acc,
            "skill": (hits / n - null_acc) if n else None,
            "verdict": verdict_for(hits / n if n else None, lo, hi, n, null_acc, None),
            "note": "live forward record; independent symbol-days",
        })

    # High-conviction slice — the tier a user would actually act on.
    q2 = """
    WITH dedup AS (
      SELECT symbol_id, horizon, prob, up,
             ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon, ts/86400
                                ORDER BY ts DESC) rn
      FROM prediction_outcomes
      WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND prob IS NOT NULL
    )
    SELECT COUNT(*), SUM(CASE WHEN (prob >= 0.5) = (up = 1) THEN 1 ELSE 0 END),
           AVG(CASE WHEN up = 1 THEN 1.0 ELSE 0.0 END)
    FROM dedup WHERE rn = 1 AND ABS(prob - 0.5) >= 0.15
    """
    n, hits, base = con.execute(q2).fetchone()
    if n:
        null_acc = max(base, 1 - base)
        lo, hi = wilson(hits, n)
        rows.append({
            "predictor": "directional-ensemble (high conviction)",
            "family": "direction",
            "band": "|p-0.5|>=0.15",
            "claimed": None,
            "live_n": n,
            "live_acc": hits / n,
            "ci": [lo, hi],
            "null_acc": null_acc,
            "skill": hits / n - null_acc,
            "verdict": verdict_for(hits / n, lo, hi, n, null_acc, None),
            "note": "the tier a user would actually trade",
        })
    return rows


# --------------------------------------------------------------------------- #
# Structural regime predictors — claims awaiting their first live grade
# --------------------------------------------------------------------------- #

def grade_structural(con: sqlite3.Connection) -> list[dict]:
    rows = []
    q = """
    SELECT kind, horizon_days,
           COUNT(*),
           SUM(CASE WHEN resolved_at IS NOT NULL THEN 1 ELSE 0 END),
           SUM(CASE WHEN correct = 1 THEN 1 ELSE 0 END),
           AVG(historical_accuracy),
           MIN(ts)
    FROM regime_outcomes
    GROUP BY kind, horizon_days ORDER BY kind
    """
    for kind, hd, total, resolved, correct, claimed, first_ts in con.execute(q):
        resolved = resolved or 0
        correct = correct or 0
        if resolved >= MIN_INDEPENDENT_N:
            lo, hi = wilson(correct, resolved)
            acc = correct / resolved
            v = verdict_for(acc, lo, hi, resolved, None, claimed)
            note = "live-graded"
        else:
            lo = hi = None
            acc = None
            # A horizon-day forecast cannot be graded before its horizon elapses.
            eligible = dt.date.fromtimestamp(first_ts) + dt.timedelta(days=hd)
            v = f"PENDING (first grade {eligible.isoformat()}, {resolved}/{MIN_INDEPENDENT_N} resolved)"
            note = "claim is backtested, not yet a live record"
        rows.append({
            "predictor": kind,
            "family": "structure",
            "band": "all",
            "claimed": claimed,
            "live_n": resolved,
            "live_acc": acc,
            "ci": [lo, hi] if lo is not None else None,
            "null_acc": None,
            "skill": None,
            "verdict": v,
            "note": note,
            "forecasts_recorded": total,
        })
    return rows


def verdict_for(acc, lo, hi, n, null_acc, claimed) -> str:
    """Verdicts come from the interval, never the point estimate."""
    if n < MIN_INDEPENDENT_N:
        return f"INSUFFICIENT ({n}/{MIN_INDEPENDENT_N})"
    # Against a stated null (direction): the whole interval must clear it.
    if null_acc is not None:
        if hi < null_acc:
            return "FAILED — significantly worse than the naive baseline"
        if lo > null_acc:
            return "VALIDATED — beats baseline"
        return "NO SKILL — indistinguishable from baseline"
    # Against a frozen claim (structure): has live accuracy decayed below it?
    if claimed is not None:
        if hi < claimed - 0.05:
            return f"DECAYED — live materially below the {claimed:.0%} claim"
        if lo >= claimed - 0.05:
            return f"HOLDING — live supports the {claimed:.0%} claim"
        return f"WIDE — cannot confirm or reject the {claimed:.0%} claim yet"
    return "UNGRADED"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--json", help="write the registry as JSON here")
    args = ap.parse_args()

    con = connect(args.db)
    rows = grade_directional(con) + grade_structural(con)

    print("=" * 104)
    print(f"SIGNALDECK ACCURACY REGISTRY — {dt.date.today()}")
    print("Every predictor, its claim, and what the live record actually supports.")
    print("=" * 104)
    hdr = "%-40s %8s %9s %9s %-19s %s"
    print(hdr % ("PREDICTOR", "CLAIM", "LIVE n", "LIVE ACC", "95% CI", "VERDICT"))
    print("-" * 104)
    for r in rows:
        claim = f"{r['claimed']:.1%}" if r["claimed"] is not None else "—"
        acc = f"{r['live_acc']:.1%}" if r["live_acc"] is not None else "—"
        ci = f"[{r['ci'][0]:.3f}, {r['ci'][1]:.3f}]" if r["ci"] else "—"
        print(hdr % (r["predictor"][:40], claim, f"{r['live_n']:,}", acc, ci, r["verdict"]))

    failed = [r for r in rows if r["verdict"].startswith("FAILED")]
    pending = [r for r in rows if r["verdict"].startswith("PENDING")]
    print()
    if failed:
        print("ACTION REQUIRED — these are shipping a prediction the live record contradicts:")
        for r in failed:
            print(f"  * {r['predictor']}: {r['live_acc']:.1%} over {r['live_n']:,} independent "
                  f"observations, entire CI below the {r['null_acc']:.1%} baseline.")
        print("    Retire, invert, or relabel as experimental. Do not display as a forecast.")
        print()
    if pending:
        print(f"{len(pending)} structural predictor(s) not yet gradable — their numbers are")
        print("backtest claims. They become real evidence on the dates shown above.")
        print()
    print(f"Independence rule: one observation per (symbol, horizon, UTC-day).")
    print(f"Verdict threshold: {MIN_INDEPENDENT_N} independent observations minimum.")

    if args.json:
        with open(args.json, "w") as f:
            json.dump({"generated": dt.datetime.now().isoformat(timespec="seconds"),
                       "min_independent_n": MIN_INDEPENDENT_N,
                       "rows": rows}, f, indent=1)
        print(f"\nwrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

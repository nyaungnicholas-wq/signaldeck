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

# Below this many DISTINCT UTC days no interval is published at all. Mirrors
# clusterstat.MinDistinctDays in the Go daemon, and exists for the same reason:
# a between-day variance estimated from three days is not a correction, it is a
# different way to be overconfident.
MIN_DISTINCT_DAYS = 10

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


def design_effect(days: list[tuple[int, int]]) -> float | None:
    """Measured clustering penalty over per-day (n, hits) tallies.

    Deduplicating to one row per (symbol, UTC-day) removes intraday
    pseudo-replication and leaves the larger problem untouched: on any given day
    ~1,000 symbols share ONE market move. A binomial interval over those rows
    asserts thousands of independent trials in a sample that holds a handful of
    days.

    This is the survey-linearization ("ultimate cluster") variance of a ratio
    estimator, which is what handles the very unequal day sizes here — one day
    holds 7 observations and the next holds 1,046. It is the same estimator as
    clusterstat.DesignEffect in the Go daemon, deliberately: a registry verdict
    and a canary decision must never disagree about the same numbers.

    Returns None when it cannot be measured; never returns below 1.0, because a
    value under 1 is sampling noise and using it would make the interval
    NARROWER than the independence assumption it was brought in to correct.
    """
    k = len(days)
    if k < 2:
        return None
    n = sum(dn for dn, _ in days)
    hits = sum(dh for _, dh in days)
    if n <= 0:
        return None
    p = hits / n
    # A degenerate proportion carries no between-day variance to measure, but it
    # is also the most perfectly clustered sample possible — every day is
    # internally uniform. The honest reading is the worst case, one independent
    # observation per day, not the flattering 1.0 the arithmetic would give.
    if p <= 0 or p >= 1:
        return n / k
    s = sum((dh - dn * p) ** 2 for dn, dh in days)
    cluster_var = k / ((k - 1) * n * n) * s
    binom_var = p * (1 - p) / n
    if binom_var <= 0 or cluster_var <= 0:
        return 1.0
    return max(1.0, cluster_var / binom_var)


def wilson_eff(p: float, eff_n: float, z: float = 1.96) -> tuple[float, float]:
    """Wilson interval at an EFFECTIVE sample size (n / design effect).

    Passing the raw row count here is the bug this function exists to prevent.
    """
    if eff_n <= 0:
        return (0.0, 1.0)
    p = min(1.0, max(0.0, p))
    d = 1 + z * z / eff_n
    centre = (p + z * z / (2 * eff_n)) / d
    half = z * math.sqrt(p * (1 - p) / eff_n + z * z / (4 * eff_n * eff_n)) / d
    return (max(0.0, centre - half), min(1.0, centre + half))


def clustered_ci(days: list[tuple[int, int]]) -> dict:
    """Grade per-day tallies into a publishable, day-resampled interval.

    Returns a dict carrying the interval AND the evidence behind it — distinct
    days, measured design effect, effective n — because "6,957 observations,
    effective 4,153 over 11 days" is the honest description and the row count
    alone is not.

    ci is None when the sample covers fewer than MIN_DISTINCT_DAYS days. That is
    a refusal, not a wide interval, and it must never be rendered as a number.
    """
    n = sum(dn for dn, _ in days)
    hits = sum(dh for _, dh in days)
    out = {
        "n": n,
        "hits": hits,
        "distinct_days": len(days),
        "acc": (hits / n) if n else None,
        "ci": None,
        "design_effect": None,
        "effective_n": None,
        "ci_method": "withheld",
    }
    if n <= 0:
        return out
    if len(days) < MIN_DISTINCT_DAYS:
        out["ci_reason"] = (f"withheld: {len(days)}/{MIN_DISTINCT_DAYS} distinct days — "
                            "too few to measure between-day variance")
        return out
    deff = design_effect(days)
    if deff is None:
        out["ci_reason"] = "withheld: design effect not measurable"
        return out
    eff = n / deff
    lo, hi = wilson_eff(hits / n, eff)
    out["ci"] = [lo, hi]
    out["design_effect"] = deff
    out["effective_n"] = eff
    out["ci_method"] = "day-clustered-wilson"
    return out


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
    # Per-DAY tallies, not per-horizon totals. The dedup below still collapses
    # intraday repeats to one row per (symbol, horizon, UTC-day); the day
    # grouping is what lets the interval resample days instead of rows.
    q = """
    WITH dedup AS (
      SELECT symbol_id, horizon, prob, up, ts,
             ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon, ts/86400
                                ORDER BY ts DESC) rn
      FROM prediction_outcomes
      WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND prob IS NOT NULL
    )
    SELECT horizon, ts/86400 AS day,
           COUNT(*),
           SUM(CASE WHEN (prob >= 0.5) = (up = 1) THEN 1 ELSE 0 END),
           SUM(CASE WHEN up = 1 THEN 1 ELSE 0 END),
           SUM(CASE WHEN ABS(prob - 0.5) >= 0.15 THEN 1 ELSE 0 END),
           SUM(CASE WHEN ABS(prob - 0.5) >= 0.15 AND (prob >= 0.5) = (up = 1) THEN 1 ELSE 0 END),
           SUM(CASE WHEN ABS(prob - 0.5) >= 0.15 AND up = 1 THEN 1 ELSE 0 END)
    FROM dedup WHERE rn = 1 GROUP BY horizon, day ORDER BY horizon, day
    """
    by_h: dict[str, list] = {}
    for horizon, day, n, hits, ups, hc_n, hc_hits, hc_ups in con.execute(q):
        by_h.setdefault(horizon, []).append((n, hits, ups, hc_n, hc_hits, hc_ups))

    def emit(name: str, band: str, days: list[tuple[int, int]], ups: int, note: str) -> None:
        g = clustered_ci(days)
        if not g["n"]:
            return
        # The honest null for a directional call is the best constant guess —
        # always predicting the majority class. Beating 50% means nothing if
        # up-days are 55%.
        base = ups / g["n"]
        null_acc = max(base, 1 - base)
        lo, hi = (g["ci"] if g["ci"] else (None, None))
        rows.append({
            "predictor": name,
            "family": "direction",
            "band": band,
            "claimed": None,
            "live_n": g["n"],
            "live_acc": g["acc"],
            "ci": g["ci"],
            "ci_method": g["ci_method"],
            "distinct_days": g["distinct_days"],
            "design_effect": g["design_effect"],
            "effective_n": g["effective_n"],
            "null_acc": null_acc,
            "skill": g["acc"] - null_acc,
            "verdict": verdict_for(g["acc"], lo, hi, g["n"], null_acc, None,
                                   distinct_days=g["distinct_days"]),
            "note": note,
        })

    for horizon, per_day in sorted(by_h.items()):
        emit(f"directional-ensemble ({horizon})", "all",
             [(d[0], d[1]) for d in per_day], sum(d[2] for d in per_day),
             "live forward record; independent symbol-days, day-resampled interval")

    # High-conviction slice — the tier a user would actually act on. Graded PER
    # HORIZON: the same symbol on the same day appears in both the 1d and the 1w
    # record, and pooling them counted one correlated call twice.
    for horizon, per_day in sorted(by_h.items()):
        days = [(d[3], d[4]) for d in per_day if d[3] > 0]
        if not days:
            continue
        emit(f"directional-ensemble ({horizon}, high conviction)", "|p-0.5|>=0.15",
             days, sum(d[5] for d in per_day), "the tier a user would actually trade")
    return rows


# --------------------------------------------------------------------------- #
# Structural regime predictors — claims awaiting their first live grade
# --------------------------------------------------------------------------- #

def grade_structural(con: sqlite3.Connection) -> list[dict]:
    rows = []
    # Totals and first-call time per predictor.
    q = """
    SELECT kind, horizon_days, COUNT(*), AVG(historical_accuracy), MIN(ts)
    FROM regime_outcomes GROUP BY kind, horizon_days ORDER BY kind
    """
    # Resolved outcomes tallied PER CALL-DAY. regime_outcomes is already unique
    # on (symbol_id, kind, day), so each row is one symbol-day — but ~870
    # symbols share each call day, and grading those as 870 independent trials
    # is how a single market day becomes a confident verdict on a 82% claim.
    qd = """
    SELECT kind, horizon_days, day, COUNT(*), SUM(CASE WHEN correct = 1 THEN 1 ELSE 0 END)
    FROM regime_outcomes WHERE resolved_at IS NOT NULL
    GROUP BY kind, horizon_days, day ORDER BY kind, day
    """
    per_day: dict[tuple, list[tuple[int, int]]] = {}
    for kind, hd, _day, n, hits in con.execute(qd):
        per_day.setdefault((kind, hd), []).append((n, hits or 0))

    for kind, hd, total, claimed, first_ts in con.execute(q):
        days = per_day.get((kind, hd), [])
        g = clustered_ci(days)
        resolved = g["n"]
        lo = hi = None
        acc = None
        extra = {}
        if resolved >= MIN_INDEPENDENT_N:
            acc = g["acc"]
            if g["ci"]:
                lo, hi = g["ci"]
            v = verdict_for(acc, lo, hi, resolved, None, claimed,
                            distinct_days=g["distinct_days"])
            note = "live-graded, day-resampled interval"
            extra = {
                "ci_method": g["ci_method"],
                "distinct_days": g["distinct_days"],
                "design_effect": g["design_effect"],
                "effective_n": g["effective_n"],
            }
        else:
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
            **extra,
        })
    return rows


def verdict_for(acc, lo, hi, n, null_acc, claimed, distinct_days=None) -> str:
    """Verdicts come from the interval, never the point estimate."""
    if n < MIN_INDEPENDENT_N:
        return f"INSUFFICIENT ({n}/{MIN_INDEPENDENT_N})"
    # No interval, no verdict. A sample spread over too few market days has no
    # measurable between-day variance, and the row count is not a substitute:
    # 408 forecasts resolving on one day are one market observation, however
    # many symbols they cover. Reading a verdict off the point estimate here is
    # exactly the failure the interval discipline exists to prevent.
    if lo is None or hi is None:
        if distinct_days is not None:
            return (f"INSUFFICIENT DAYS ({distinct_days}/{MIN_DISTINCT_DAYS} distinct days) — "
                    "no interval, so no verdict")
        return "NO INTERVAL — no verdict"
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
    print(f"Verdict threshold: {MIN_INDEPENDENT_N} independent observations minimum, "
          f"on at least {MIN_DISTINCT_DAYS} distinct UTC days.")
    print("Intervals resample DAYS, not rows: on any one day ~1,000 symbols share one")
    print("market move, so the row count overstates the evidence. Each graded row below")
    print("reports its measured design effect and effective n in the JSON output.")
    for r in rows:
        if r.get("design_effect"):
            print(f"  {r['predictor']}: n={r['live_n']:,} over {r['distinct_days']} days, "
                  f"design effect {r['design_effect']:.1f}x -> effective n {r['effective_n']:.0f}")

    if args.json:
        with open(args.json, "w") as f:
            json.dump({"generated": dt.datetime.now().isoformat(timespec="seconds"),
                       "min_independent_n": MIN_INDEPENDENT_N,
                       "rows": rows}, f, indent=1)
        print(f"\nwrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

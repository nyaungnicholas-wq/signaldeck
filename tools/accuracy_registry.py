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
  * PREQUENTIAL null. The majority-class baseline for each day is built from
    days strictly BEFORE it, never the graded window itself — a null computed
    in-sample gets hindsight the model never had.
  * SURVIVORSHIP boundary. symbols.delisted_at only exists since the 2026-07-24
    survivorship wave (store.go), so everything recorded before it was graded
    against a universe seeded from 2026 survivors. Pre-epoch rows never enter a
    tally here; every published row is stamped survivorship_clean accordingly.
  * DUAL NULLS for one transition cycle. The hindsight null (best constant
    guess over the finished sample) uses information not available at
    prediction time; the prequential null (follow the running majority,
    walk-forward) is the fair forward baseline and is <= the hindsight null by
    construction. Every directional row publishes BOTH, and the stricter
    (higher) one drives the verdict, so any verdict change across the switch
    is attributable to the null definition and not to data drift — the same
    discipline the A1 fix used publishing intervalMethod/designEffect beside
    the new intervals.

Usage:  python3 tools/accuracy_registry.py [--db PATH] [--json OUT]
        python3 tools/accuracy_registry.py --snapshot repro   # grade from the
            committed reproducibility snapshot instead of the (gitignored) DB,
            verifying every CSV against its manifest hash first. This is the
            path an outside reader uses — see REPRODUCE.md.
"""
from __future__ import annotations

import argparse
import csv
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

# The day symbols.delisted_at started being recorded (store.go survivorship
# wave). Rows created before this were graded against a survivor-seeded
# universe and are unfit for a published verdict — both graders filter them
# out at the SQL layer, which is what makes survivorship_clean true by
# construction on every row this script emits.
SURVIVORSHIP_EPOCH = dt.date(2026, 7, 24)
SURVIVORSHIP_EPOCH_TS = int(dt.datetime(2026, 7, 24, tzinfo=dt.timezone.utc).timestamp())


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


def prequential_null(days: list[tuple[int, int]]) -> dict:
    """Out-of-sample majority null over chronological per-day (n, ups) tallies.

    The old null was max(base, 1-base) with base computed over the SAME window
    being graded — hindsight the model never had, which retroactively credited
    the null with any mid-window class flip. Here the constant guess for day d
    is the majority class over days strictly BEFORE d, with expected accuracy
    0.5 when there is no prior evidence (day one, or a tied prior). The
    guess-sequence is graded through the same clustered_ci machinery as the
    model it benchmarks.
    """
    null_days: list[tuple[int, float]] = []
    prior_n = prior_ups = 0
    for n, ups in days:
        if prior_n == 0 or prior_ups * 2 == prior_n:
            hits = n / 2  # no majority to lean on yet — a coin flip
        elif prior_ups * 2 > prior_n:
            hits = float(ups)  # constant "up" guess
        else:
            hits = float(n - ups)  # constant "down" guess
        null_days.append((n, hits))
        prior_n += n
        prior_ups += ups
    return clustered_ci(null_days)


def connect(path: str) -> sqlite3.Connection:
    if not os.path.exists(path):
        sys.exit(f"database not found: {path}")
    return sqlite3.connect(f"file:{path}?mode=ro", uri=True)


# --------------------------------------------------------------------------- #
# Directional ensemble — the one predictor with a real live record
# --------------------------------------------------------------------------- #

def fetch_directional_days(con: sqlite3.Connection) -> dict[str, list[tuple]]:
    """Per-day tallies behind every directional grade, keyed by horizon.

    Each value is (day, n, correct, up_days, hc_n, hc_correct, hc_up_days) —
    exactly what the reproducibility snapshot (tools/make_repro_snapshot.py)
    exports, so a grade from the DB and a grade from the committed snapshot
    start from identical inputs.
    """
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
        AND ts >= ?  -- survivorship boundary: pre-epoch rows are survivor-seeded
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
    for horizon, day, n, hits, ups, hc_n, hc_hits, hc_ups in con.execute(q, (SURVIVORSHIP_EPOCH_TS,)):
        by_h.setdefault(horizon, []).append((day, n, hits, ups, hc_n, hc_hits, hc_ups))
    return by_h


def grade_directional(con: sqlite3.Connection) -> list[dict]:
    """Grade prediction_outcomes on independent (symbol, horizon, UTC-day) rows."""
    return grade_directional_days(fetch_directional_days(con))


def grade_directional_days(by_h: dict[str, list[tuple]]) -> list[dict]:
    """Grade directional per-day tallies from either the DB or a snapshot."""
    rows = []

    def emit(name: str, band: str, days: list[tuple[int, int, int]], note: str) -> None:
        g = clustered_ci([(n, hits) for n, hits, _ in days])
        if not g["n"]:
            return
        # The honest null for a directional call is the best constant guess a
        # bettor WITHOUT hindsight could have made — the prequential majority,
        # not the whole window's. Beating 50% still means nothing if up-days
        # run 55%, but the null only learns that rate as the days arrive.
        null_g = prequential_null([(n, ups) for n, _, ups in days])
        null_preq = null_g["acc"]
        # TRANSITION CYCLE: the retired hindsight null is published beside the
        # prequential one, and the STRICTER (higher) of the two drives the
        # verdict. Since the prequential null can only sit at or below the
        # hindsight null, verdicts cannot soften this cycle — so when the
        # hindsight column is dropped next release, any change is attributable
        # to the null definition alone, never to data drift.
        base = sum(ups for _, _, ups in days) / g["n"]
        null_hind = max(base, 1 - base)
        null_acc = max(null_hind, null_preq)
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
            "null_hindsight": null_hind,
            "null_prequential": null_preq,
            "null_acc": null_acc,
            "null_method": "transition-dual: verdict vs max(hindsight-majority, prequential-majority)",
            "null_ci": null_g["ci"],
            "skill": g["acc"] - null_acc,
            "verdict": verdict_for(g["acc"], lo, hi, g["n"], null_acc, None,
                                   distinct_days=g["distinct_days"]),
            "note": note,
            "survivorship_clean": True,
        })

    for horizon, per_day in sorted(by_h.items()):
        emit(f"directional-ensemble ({horizon})", "all",
             [(d[1], d[2], d[3]) for d in per_day],
             "live forward record; independent symbol-days, day-resampled interval")

    # High-conviction slice — the tier a user would actually act on. Graded PER
    # HORIZON: the same symbol on the same day appears in both the 1d and the 1w
    # record, and pooling them counted one correlated call twice.
    for horizon, per_day in sorted(by_h.items()):
        days = [(d[4], d[5], d[6]) for d in per_day if d[4] > 0]
        if not days:
            continue
        emit(f"directional-ensemble ({horizon}, high conviction)", "|p-0.5|>=0.15",
             days, "the tier a user would actually trade")
    return rows


# --------------------------------------------------------------------------- #
# Structural regime predictors — claims awaiting their first live grade
# --------------------------------------------------------------------------- #

def fetch_structural(con: sqlite3.Connection) -> tuple[list[tuple], dict[tuple, list[tuple]]]:
    """Claims plus per-day tallies behind every structural grade.

    Returns (totals, per_day): totals rows are
    (kind, horizon_days, forecasts_recorded, claimed_accuracy, first_ts) and
    per_day maps (kind, horizon_days) -> [(day, n, correct)] — the exact shape
    the reproducibility snapshot exports.
    """
    # Totals and first-call time per predictor.
    q = """
    SELECT kind, horizon_days, COUNT(*), AVG(historical_accuracy), MIN(ts)
    FROM regime_outcomes WHERE ts >= ?
    GROUP BY kind, horizon_days ORDER BY kind
    """
    # Resolved outcomes tallied PER CALL-DAY. regime_outcomes is already unique
    # on (symbol_id, kind, day), so each row is one symbol-day — but ~870
    # symbols share each call day, and grading those as 870 independent trials
    # is how a single market day becomes a confident verdict on a 82% claim.
    qd = """
    SELECT kind, horizon_days, day, COUNT(*), SUM(CASE WHEN correct = 1 THEN 1 ELSE 0 END)
    FROM regime_outcomes WHERE resolved_at IS NOT NULL AND ts >= ?
    GROUP BY kind, horizon_days, day ORDER BY kind, day
    """
    per_day: dict[tuple, list[tuple[int, int, int]]] = {}
    for kind, hd, day, n, hits in con.execute(qd, (SURVIVORSHIP_EPOCH_TS,)):
        per_day.setdefault((kind, hd), []).append((day, n, hits or 0))
    totals = [tuple(r) for r in con.execute(q, (SURVIVORSHIP_EPOCH_TS,))]
    return totals, per_day


def grade_structural(con: sqlite3.Connection) -> list[dict]:
    totals, per_day = fetch_structural(con)
    return grade_structural_days(totals, per_day)


def grade_structural_days(totals: list[tuple], per_day: dict[tuple, list[tuple]]) -> list[dict]:
    """Grade structural claims from either the DB or a snapshot."""
    rows = []
    for kind, hd, total, claimed, first_ts in totals:
        days = [(n, hits) for _day, n, hits in per_day.get((kind, hd), [])]
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
            "null_hindsight": None,
            "null_prequential": None,
            "null_acc": None,
            "skill": None,
            "verdict": v,
            "note": note,
            "forecasts_recorded": total,
            "survivorship_clean": True,
            **extra,
        })
    return rows


# --------------------------------------------------------------------------- #
# Reproducibility snapshot — grading without the (gitignored) database
# --------------------------------------------------------------------------- #

def load_snapshot(snap_dir: str):
    """Load grading inputs from a committed snapshot (see REPRODUCE.md).

    Every CSV is verified against its MANIFEST.json hash before a single row
    is graded — the same canonical scheme as daemon/internal/datasetver — so a
    tampered or hand-edited snapshot refuses to grade rather than quietly
    publishing different numbers.
    """
    from make_repro_snapshot import FILES, hash_records  # same tools/ dir

    man_path = os.path.join(snap_dir, "MANIFEST.json")
    if not os.path.exists(man_path):
        sys.exit(f"snapshot manifest not found: {man_path}")
    with open(man_path) as f:
        manifest = {e["file"]: e for e in json.load(f)["files"]}

    def read(fname: str) -> list[list[str]]:
        path = os.path.join(snap_dir, fname)
        if not os.path.exists(path):
            sys.exit(f"snapshot file missing: {path}")
        with open(path, newline="") as f:
            recs = list(csv.reader(f))[1:]  # drop the header row
        ent = manifest.get(fname)
        if ent is None:
            sys.exit(f"snapshot file not in manifest: {fname}")
        got = hash_records(fname, FILES[fname], recs)
        if got != ent["sha256"]:
            sys.exit(f"snapshot integrity failure: {fname} hashes {got}, manifest "
                     f"says {ent['sha256']} — refusing to grade a modified snapshot")
        return recs

    by_h: dict[str, list] = {}
    for horizon, *nums in read("directional_days.csv"):
        by_h.setdefault(horizon, []).append(tuple(int(x) for x in nums))

    per_day: dict[tuple, list] = {}
    for kind, hd, day, n, hits in read("structural_days.csv"):
        per_day.setdefault((kind, int(hd)), []).append((int(day), int(n), int(hits)))
    totals = [(kind, int(hd), int(total), float(claimed) if claimed else None, int(first_ts))
              for kind, hd, total, claimed, first_ts in read("structural_claims.csv")]
    return by_h, totals, per_day


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
    ap.add_argument("--snapshot", metavar="DIR",
                    help="grade from a committed reproducibility snapshot (repro/) "
                         "instead of the gitignored DB; verifies manifest hashes first")
    args = ap.parse_args()

    if args.snapshot:
        by_h, totals, per_day = load_snapshot(args.snapshot)
        rows = grade_directional_days(by_h) + grade_structural_days(totals, per_day)
        source = f"snapshot {args.snapshot} (manifest hashes verified)"
    else:
        con = connect(args.db)
        rows = grade_directional(con) + grade_structural(con)
        source = f"database {args.db}"

    print("=" * 104)
    print(f"SIGNALDECK ACCURACY REGISTRY — {dt.date.today()}")
    print("Every predictor, its claim, and what the live record actually supports.")
    print(f"Graded from {source}")
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
                  f"observations, entire CI below the {r['null_acc']:.1%} baseline "
                  f"(hindsight {r['null_hindsight']:.1%} / prequential {r['null_prequential']:.1%}).")
        print("    Retire, invert, or relabel as experimental. Do not display as a forecast.")
        print()
    if pending:
        print(f"{len(pending)} structural predictor(s) not yet gradable — their numbers are")
        print("backtest claims. They become real evidence on the dates shown above.")
        print()
    print(f"Independence rule: one observation per (symbol, horizon, UTC-day).")
    print(f"Verdict threshold: {MIN_INDEPENDENT_N} independent observations minimum, "
          f"on at least {MIN_DISTINCT_DAYS} distinct UTC days.")
    print(f"Survivorship boundary: rows before {SURVIVORSHIP_EPOCH.isoformat()} were graded "
          "against a survivor-seeded universe and are excluded from every tally above.")
    print("Intervals resample DAYS, not rows: on any one day ~1,000 symbols share one")
    print("market move, so the row count overstates the evidence. Each graded row below")
    print("reports its measured design effect and effective n in the JSON output.")
    print("Directional nulls are in a DUAL-NULL TRANSITION cycle: every row publishes the")
    print("retiring hindsight null (best constant guess over the finished sample) beside")
    print("the prequential null (each day's guess is the majority class over days strictly")
    print("before it). The stricter (higher) of the two drives the verdict, so when the")
    print("hindsight column is dropped next release, any verdict change is attributable to")
    print("the null definition alone — not data drift. Prequential <= hindsight by")
    print("construction, so FAILED verdicts can only soften across the switch, never sharpen.")
    for r in rows:
        if r.get("design_effect"):
            print(f"  {r['predictor']}: n={r['live_n']:,} over {r['distinct_days']} days, "
                  f"design effect {r['design_effect']:.1f}x -> effective n {r['effective_n']:.0f}")

    if args.json:
        with open(args.json, "w") as f:
            json.dump({"generated": dt.datetime.now().isoformat(timespec="seconds"),
                       "min_independent_n": MIN_INDEPENDENT_N,
                       "survivorship_epoch": SURVIVORSHIP_EPOCH.isoformat(),
                       "null_policy": ("transition cycle: null_hindsight and null_prequential "
                                       "published side by side on every row; the stricter "
                                       "(higher) drives the verdict. Hindsight column drops "
                                       "next release."),
                       "rows": rows}, f, indent=1)
        print(f"\nwrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

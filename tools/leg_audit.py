#!/usr/bin/env python3
"""Grade each ensemble leg SEPARATELY against the same honest null the registry uses.

WHY THIS EXISTS
---------------
The directional ensemble was retired whole, at 6.6pp below its majority-class
null, because nothing on the platform could say WHICH of its seven legs was
hurting. That was believed to be a data problem. It was not: every leg's
probability and its admission lift have been stored all along in
`predictions.components`, one JSON blob per prediction. This script reads them.

It is READ-ONLY and DIAGNOSTIC. It publishes nothing a grading surface would
publish. It reports what the evidence supports and refuses where it does not:
intervals are day-clustered, verdicts are read off the interval rather than the
point estimate, and a leg with too few independent days gets no verdict at all.
"""

from __future__ import annotations

import argparse
import json
import math
import sqlite3
import statistics
import sys
from collections import defaultdict

MIN_INDEPENDENT_N = 30
MIN_DISTINCT_DAYS = 10
Z = 1.96

# leg -> (component field, lift field). The probability conversion for the two
# legs that are NOT already a probability is applied in leg_probability.
LEGS = {
    "pressure": ("PressureScore", "PressureLift"),
    "expectancy": ("ExpectancyHitRate", "ExpectancyLift"),
    "forecast": ("ForecastProb", "ForecastLift"),
    "gbm": ("GBMProb", "GBMLift"),
    "meanrev": ("MeanRevProb", "MeanRevLift"),
    "sentiment": ("SentimentScore", "SentimentLift"),
    "alphax": ("AlphaXProb", "AlphaXLift"),
}

# Mirrors ensemble.SentimentScale. Kept here as a literal because this script
# reads a historical record: changing the live constant must NOT retroactively
# change how an already-stored score is interpreted.
SENTIMENT_SCALE = 0.15


def leg_probability(leg: str, comp: dict) -> float | None:
    """The stored component as a 0..1 up-probability, or None when absent.

    Absent means the leg did not run on that row. It is never imputed to 0.5:
    a leg that said nothing and a leg that said "coin flip" are different facts.
    """
    if leg not in LEGS:
        raise ValueError(f"unknown leg {leg!r}")
    field, _ = LEGS[leg]
    v = comp.get(field)
    if v is None:
        return None
    if leg == "pressure":
        return max(0.0, min(1.0, (float(v) + 1.0) / 2.0))
    if leg == "sentiment":
        return max(0.0, min(1.0, 0.5 + float(v) * SENTIMENT_SCALE))
    return float(v)


def leg_lift(leg: str, comp: dict) -> float | None:
    _, field = LEGS[leg]
    v = comp.get(field)
    return None if v is None else float(v)


def admitted(lift: float | None) -> bool:
    """Whether the leg entered the blend. Mirrors ensemble.admits in strict mode."""
    return lift is not None and lift > 0


def brier(ps: list[float], ys: list[int]) -> float:
    if not ps:
        return float("nan")
    return sum((p - y) ** 2 for p, y in zip(ps, ys)) / len(ps)


def log_loss(ps: list[float], ys: list[int], eps: float = 1e-15) -> float:
    if not ps:
        return float("nan")
    total = 0.0
    for p, y in zip(ps, ys):
        q = min(max(p, eps), 1 - eps)
        total -= math.log(q) if y == 1 else math.log(1 - q)
    return total / len(ps)


def auc(ps: list[float], ys: list[int]) -> float | None:
    """Mann-Whitney AUC, ties at 0.5. None when a class is missing.

    Undefined is reported as None rather than 0.5: "no discrimination measured"
    and "measured, and it was chance" are different claims.
    """
    pos = [p for p, y in zip(ps, ys) if y == 1]
    neg = [p for p, y in zip(ps, ys) if y != 1]
    if not pos or not neg:
        return None
    wins = 0.0
    for a in pos:
        for b in neg:
            wins += 1.0 if a > b else (0.5 if a == b else 0.0)
    return wins / (len(pos) * len(neg))


def prequential_majority(rows: list[tuple[int, int]]) -> dict[int, float]:
    """Per-day accuracy of the constant majority guess, learned from PRIOR days only.

    The first day has no prior and is therefore absent from the result: inventing
    a baseline for it would be hindsight, which is the exact failure this null
    exists to avoid.
    """
    by_day: dict[int, list[int]] = defaultdict(list)
    for day, up in rows:
        by_day[day].append(up)

    out: dict[int, float] = {}
    prior_n = 0
    prior_up = 0
    for day in sorted(by_day):
        today = by_day[day]
        if prior_n > 0:
            guess = 1 if (prior_up / prior_n) >= 0.5 else 0
            out[day] = sum(1 for u in today if u == guess) / len(today)
        prior_n += len(today)
        prior_up += sum(today)
    return out


def day_clustered_stats(per_day: list[tuple[int, int]]) -> dict:
    """Accuracy with a BETWEEN-DAY standard error.

    The binomial SE over rows understates the noise by roughly sqrt(rows-per-day)
    because rows inside one day share a market. This platform measured ~12.1 rows
    per symbol-day, so that is a factor of ~3.5.
    """
    n = sum(t for _, t in per_day)
    correct = sum(c for c, _ in per_day)
    days = len(per_day)
    acc = correct / n if n else float("nan")
    if days < 2:
        return {"n": n, "days": days, "acc": acc, "se": None, "lo": None, "hi": None}
    rates = [c / t for c, t in per_day if t]
    se = statistics.stdev(rates) / math.sqrt(len(rates))
    return {"n": n, "days": days, "acc": acc, "se": se,
            "lo": acc - Z * se, "hi": acc + Z * se}


def dedup_one_per_symbol_day(rows):
    """One observation per (symbol, UTC day) — the registry's independence rule.

    408 forecasts resolving on one day are ONE market observation.
    """
    best: dict[tuple, dict] = {}
    for r in rows:
        k = (r["symbol_id"], r["day"])
        if k not in best or r["ts"] < best[k]["ts"]:
            best[k] = r
    keep = {id(v) for v in best.values()}
    return [r for r in rows if id(r) in keep]


def pearson(xs: list[float], ys: list[float]) -> float | None:
    if len(xs) < 2:
        return None
    try:
        return statistics.correlation(xs, ys)
    except statistics.StatisticsError:
        return None  # zero variance in one series: undefined, not zero


def load_rows(con, horizon):
    cur = con.execute(
        """SELECT po.symbol_id, po.ts, po.up, p.components
             FROM prediction_outcomes po
             JOIN predictions p
               ON p.symbol_id = po.symbol_id AND p.horizon = po.horizon AND p.ts = po.ts
            WHERE po.resolved_at IS NOT NULL
              AND po.horizon = ?
              AND po.up IS NOT NULL
              AND p.components IS NOT NULL AND p.components != ''""",
        (horizon,),
    )
    rows = []
    for symbol_id, ts, up, comp_json in cur:
        try:
            comp = json.loads(comp_json)
        except (ValueError, TypeError):
            continue
        if not isinstance(comp, dict):
            continue
        rows.append({"symbol_id": symbol_id, "ts": ts, "day": ts // 86400,
                     "up": int(up), "comp": comp})
    return rows


def grade_leg(leg: str, rows: list[dict], baselines: dict[int, float]) -> dict:
    present = admitted_n = never_measured = 0
    per_day_hits: dict[int, list[int]] = defaultdict(list)
    ps: list[float] = []
    ys: list[int] = []

    for r in rows:
        p = leg_probability(leg, r["comp"])
        if p is None:
            continue
        present += 1
        lift = leg_lift(leg, r["comp"])
        if lift is None:
            never_measured += 1
        elif lift > 0:
            admitted_n += 1
        ps.append(p)
        ys.append(r["up"])
        if p == 0.5:
            continue  # no directional call was made; scoring it invents one
        if r["day"] not in baselines:
            continue  # no prequential null exists for the first day
        per_day_hits[r["day"]].append(1 if (p > 0.5) == (r["up"] == 1) else 0)

    per_day = [(sum(v), len(v)) for _, v in sorted(per_day_hits.items())]
    stats = day_clustered_stats(per_day) if per_day else {
        "n": 0, "days": 0, "acc": float("nan"), "se": None, "lo": None, "hi": None}

    graded_days = sorted(per_day_hits)
    baseline = (sum(baselines[d] for d in graded_days) / len(graded_days)) if graded_days else float("nan")

    base_rate = (sum(ys) / len(ys)) if ys else float("nan")
    b = brier(ps, ys)
    b_ref = brier([base_rate] * len(ys), ys) if ys else float("nan")
    skill = (1 - b / b_ref) if (b_ref and b_ref == b_ref and b_ref > 0) else None

    out = {
        "leg": leg, "present": present, "admitted": admitted_n,
        "never_measured": never_measured, "graded": stats["n"], "days": stats["days"],
        "acc": stats["acc"], "baseline": baseline,
        "lift": stats["acc"] - baseline if baseline == baseline else float("nan"),
        "lo": stats["lo"], "hi": stats["hi"],
        "brier": b, "brier_skill": skill, "log_loss": log_loss(ps, ys), "auc": auc(ps, ys),
    }
    out["verdict"] = _verdict(out)
    return out


def _verdict(s: dict) -> str:
    if s["graded"] < MIN_INDEPENDENT_N:
        return f"INSUFFICIENT ({s['graded']}/{MIN_INDEPENDENT_N})"
    if s["days"] < MIN_DISTINCT_DAYS:
        return f"INSUFFICIENT DAYS ({s['days']}/{MIN_DISTINCT_DAYS})"
    if s["lo"] is None or s["baseline"] != s["baseline"]:
        return "NO INTERVAL — no verdict"
    if s["hi"] < s["baseline"]:
        return "HURT"
    if s["lo"] > s["baseline"]:
        return "HELPED"
    return "NO MEASURED EDGE"


def correlations(rows: list[dict]) -> dict:
    names = list(LEGS)
    out = {}
    for i, a in enumerate(names):
        for b in names[i + 1:]:
            xs, ys = [], []
            for r in rows:
                pa, pb = leg_probability(a, r["comp"]), leg_probability(b, r["comp"])
                if pa is not None and pb is not None:
                    xs.append(pa)
                    ys.append(pb)
            out[f"{a}|{b}"] = {"n": len(xs),
                               "rho": pearson(xs, ys) if len(xs) >= MIN_INDEPENDENT_N else None}
    return out


def leave_one_out(rows: list[dict]) -> dict:
    """Does the leg ADD anything to an equal-weight blend of the others?

    A leg can score well alone and still contribute nothing, if the legs it
    duplicates already carried that information.
    """
    out = {}
    for leg in LEGS:
        with_hits = without_hits = n = 0
        for r in rows:
            p_leg = leg_probability(leg, r["comp"])
            if p_leg is None:
                continue
            others = [leg_probability(o, r["comp"]) for o in LEGS if o != leg]
            others = [p for p in others if p is not None]
            if not others:
                continue
            blend_without = sum(others) / len(others)
            blend_with = (sum(others) + p_leg) / (len(others) + 1)
            if blend_with == 0.5 or blend_without == 0.5:
                continue
            up = r["up"] == 1
            with_hits += (blend_with > 0.5) == up
            without_hits += (blend_without > 0.5) == up
            n += 1
        if n:
            out[leg] = {"n": n, "with": with_hits / n, "without": without_hits / n,
                        "delta": (with_hits - without_hits) / n}
        else:
            out[leg] = {"n": 0, "with": None, "without": None, "delta": None}
    return out


def audit(db_path: str, horizon: str) -> dict:
    try:
        con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
        con.execute("SELECT 1 FROM predictions LIMIT 1")
    except sqlite3.Error as e:
        print(f"leg_audit: cannot read {db_path}: {e}", file=sys.stderr)
        sys.exit(2)

    rows = load_rows(con, horizon)
    if not rows:
        return {"horizon": horizon, "rows": 0, "legs": {}, "correlations": {},
                "leave_one_out": {}, "note": "no resolved predictions with components"}

    deduped = dedup_one_per_symbol_day(rows)
    baselines = prequential_majority([(r["day"], r["up"]) for r in deduped])
    legs = {leg: grade_leg(leg, deduped, baselines) for leg in LEGS}
    return {
        "horizon": horizon,
        "rows_raw": len(rows),
        "rows_independent": len(deduped),
        "distinct_days": len({r["day"] for r in deduped}),
        "legs": legs,
        "correlations": correlations(deduped),
        "leave_one_out": leave_one_out(deduped),
    }


def _f(v, nd=4):
    if v is None:
        return "     -"
    if isinstance(v, float) and v != v:
        return "   nan"
    return f"{v:.{nd}f}"


def report(a: dict) -> None:
    print(f"\nPER-LEG LIVE LIFT AUDIT — horizon {a['horizon']}")
    if not a.get("legs"):
        print("  " + a.get("note", "no data"))
        return
    print(f"  {a['rows_raw']} resolved rows -> {a['rows_independent']} independent "
          f"(one per symbol-day) across {a['distinct_days']} distinct days\n")

    hdr = (f"  {'leg':11s} {'present':>8s} {'admit':>7s} {'unmeas':>7s} {'graded':>7s} "
           f"{'days':>5s} {'acc':>7s} {'null':>7s} {'lift':>8s} {'brierSk':>8s} "
           f"{'auc':>7s}  verdict")
    print(hdr)
    print("  " + "-" * (len(hdr) - 2))
    for leg, s in a["legs"].items():
        print(f"  {leg:11s} {s['present']:8d} {s['admitted']:7d} {s['never_measured']:7d} "
              f"{s['graded']:7d} {s['days']:5d} {_f(s['acc'])} {_f(s['baseline'])} "
              f"{_f(s['lift']):>8s} {_f(s['brier_skill']):>8s} {_f(s['auc']):>7s}  {s['verdict']}")

    print("\n  PAIRWISE CORRELATION (seven legs are not seven independent votes)")
    for pair, c in sorted(a["correlations"].items(),
                          key=lambda kv: -(abs(kv[1]["rho"]) if kv[1]["rho"] is not None else -1)):
        if c["rho"] is not None:
            print(f"    {pair:26s} rho={c['rho']:+.3f}  (n={c['n']})")
    thin = [p for p, c in a["correlations"].items() if c["rho"] is None]
    if thin:
        print(f"    (too few shared rows to correlate: {', '.join(thin)})")

    print("\n  LEAVE-ONE-OUT (does the leg ADD to an equal-weight blend of the others?)")
    for leg, c in sorted(a["leave_one_out"].items(),
                         key=lambda kv: (kv[1]["delta"] is None, -(kv[1]["delta"] or 0))):
        if c["delta"] is None:
            print(f"    {leg:11s} no overlapping rows")
        else:
            print(f"    {leg:11s} with={c['with']:.4f} without={c['without']:.4f} "
                  f"delta={c['delta']:+.4f}  (n={c['n']})")

    print("\n  WHAT THIS DOES AND DOES NOT SAY")
    print("    This is a DIAGNOSTIC over a larger, less filtered population than the")
    print("    accuracy registry, which additionally applies survivorship filtering and")
    print("    its own multiplicity correction. It is not a published verdict. A leg that")
    print("    looks good here still has to pass the registry's gates to be admitted, and")
    print("    verdicts here are read off the day-clustered interval, never the point")
    print("    estimate. 'unmeas' counts rows where the leg's lift was NEVER MEASURED —")
    print("    which is not the same as a measured lift of zero.")


def _self_check() -> None:
    assert leg_probability("pressure", {"PressureScore": 1.0}) == 1.0
    assert leg_probability("pressure", {"PressureScore": -1.0}) == 0.0
    assert leg_probability("pressure", {"PressureScore": 0.0}) == 0.5
    assert leg_probability("sentiment", {"SentimentScore": 1.0}) == 0.65
    assert leg_probability("sentiment", {"SentimentScore": -1.0}) == 0.35
    assert leg_probability("gbm", {"GBMProb": 0.7}) == 0.7
    assert leg_probability("gbm", {}) is None
    assert leg_probability("gbm", {"GBMProb": None}) is None
    try:
        leg_probability("nope", {})
        assert False, "expected ValueError"
    except ValueError:
        pass

    assert admitted(0.01) is True
    assert admitted(0.0) is False
    assert admitted(-0.1) is False
    assert admitted(None) is False

    assert brier([1.0, 0.0], [1, 0]) == 0.0
    assert abs(brier([0.5, 0.5], [1, 0]) - 0.25) < 1e-12
    assert log_loss([1.0, 1.0], [1, 1]) < 1e-9
    assert abs(auc([0.1, 0.4, 0.35, 0.8], [0, 0, 1, 1]) - 0.75) < 1e-12
    assert auc([0.2, 0.3], [1, 1]) is None
    assert abs(auc([0.9, 0.1], [0, 1]) - 0.0) < 1e-12

    base = prequential_majority([(1, 1), (1, 1), (2, 1), (2, 0), (3, 0)])
    assert 1 not in base
    assert abs(base[2] - 0.5) < 1e-12
    assert abs(base[3] - 0.0) < 1e-12

    s = day_clustered_stats([(5, 10), (5, 10), (5, 10)])
    assert s["n"] == 30 and s["days"] == 3
    assert abs(s["acc"] - 0.5) < 1e-12
    assert s["se"] == 0.0
    one = day_clustered_stats([(5, 10)])
    assert one["se"] is None and one["lo"] is None

    rows = [{"symbol_id": 1, "day": 5, "ts": 200}, {"symbol_id": 1, "day": 5, "ts": 100},
            {"symbol_id": 2, "day": 5, "ts": 150}, {"symbol_id": 1, "day": 6, "ts": 300}]
    kept = dedup_one_per_symbol_day(rows)
    assert len(kept) == 3
    assert {(r["symbol_id"], r["day"], r["ts"]) for r in kept} == {(1, 5, 100), (2, 5, 150), (1, 6, 300)}

    print("self-check OK")


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--db", default="data/signaldeck.db")
    ap.add_argument("--horizon", default="1d")
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--self-check", action="store_true")
    args = ap.parse_args()

    if args.self_check:
        _self_check()
        return

    a = audit(args.db, args.horizon)
    if args.json:
        print(json.dumps(a, indent=1, default=str))
    else:
        report(a)
    if any(s["verdict"] == "HURT" for s in a.get("legs", {}).values()):
        sys.exit(1)


if __name__ == "__main__":
    main()

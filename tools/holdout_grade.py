#!/usr/bin/env python3
"""Grade the directional forecaster on a SHA-pinned, day-clustered holdout.

WHY THIS EXISTS. Every headline figure this system has published about the
directional forecaster was computed over a window that silently included a
COLLAPSED cross-section: from 2026-07-27 to 2026-08-04 the model emitted 6-13
distinct probabilities across ~328 symbols per day, so the record was one
market-wide call repeated per symbol while n looked like 328 independent trials
a day. cmd/forecastmon measured 14 of the last 28 days in that state.

Grading that window produced a per-bucket table in which the <30% bucket looked
informative (n=3020, realized 45.5%). Restricted to post-collapse rows the same
bucket holds ELEVEN rows and the sign reverses. The conclusion was an artifact of
the collapse, not a finding.

So this tool does three things the old path did not:
  1. takes an EXPLICIT window and prints the SHA of the query it ran, so a
     published number can be traced to the exact rows behind it;
  2. deduplicates to one row per symbol per trading day before measuring
     anything;
  3. reports the day-clustered design effect, and refuses to pretend a two-day
     window has 649 independent observations.

Usage:
    python tools/holdout_grade.py --db data/signaldeck.db --start 2026-08-05 --end 2026-08-06
"""
import argparse
import hashlib
import json
import math
import sqlite3

Z = 1.959963984540054

# The frozen-holdout query. Pinned by SHA so a result can be tied to the rows it
# came from. Change this string and every previously published number is
# explicitly a different measurement, which is the point.
HOLDOUT_SQL = """
WITH dedup AS (
  SELECT symbol_id,
         date(ts,'unixepoch') AS d,
         prob,
         up,
         ROW_NUMBER() OVER (PARTITION BY symbol_id, date(ts,'unixepoch')
                            ORDER BY ts DESC) AS rn
  FROM prediction_outcomes
  WHERE horizon = ?
    AND resolved_at IS NOT NULL
    AND up IS NOT NULL
    AND prob IS NOT NULL
    AND date(ts,'unixepoch') BETWEEN ? AND ?
)
SELECT d, symbol_id, prob, up FROM dedup WHERE rn = 1
"""

EDGES = [(0.0, 0.30, "<30%"), (0.30, 0.45, "30-45%"), (0.45, 0.55, "45-55%"),
         (0.55, 0.70, "55-70%"), (0.70, 1.01, ">=70%")]


def holdout_sha() -> str:
    return hashlib.sha256(HOLDOUT_SQL.encode("utf-8")).hexdigest()


def wilson(k, n, z=Z):
    """Wilson score interval. Preferred over normal-approximation because the
    rates here sit near the tails and n_effective is small once clustering is
    accounted for -- exactly where the normal interval misbehaves."""
    if n <= 0:
        return 0.0, 1.0
    p = k / n
    denom = 1 + z * z / n
    centre = p + z * z / (2 * n)
    rad = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n))
    return max(0.0, (centre - rad) / denom), min(1.0, (centre + rad) / denom)


def design_effect(day_rates, mean_n, pooled):
    """Overdispersion of the daily hit rate versus a single binomial.

    Returns 1.0 when it cannot be measured. A caller MUST read 1.0 as
    'unmeasured', never as 'the days are independent' -- with two clusters there
    is no information about between-day variance, and assuming independence is
    how a 2-day window gets published with a +-4pp interval."""
    if len(day_rates) < 2 or mean_n <= 0 or not (0 < pooled < 1):
        return 1.0
    expected = pooled * (1 - pooled) / mean_n
    if expected <= 0:
        return 1.0
    observed = sum((r - pooled) ** 2 for r in day_rates) / (len(day_rates) - 1)
    return max(1.0, observed / expected)


def grade(rows):
    n = len(rows)
    if n == 0:
        return {"n": 0}
    up_rate = sum(r["up"] for r in rows) / n
    base = max(up_rate, 1 - up_rate)
    hits = sum(1 for r in rows if (r["prob"] >= 0.5) == (r["up"] == 1))
    acc = hits / n

    brier = sum((r["prob"] - r["up"]) ** 2 for r in rows) / n
    brier_base = up_rate * (1 - up_rate)
    ll = sum(-math.log(min(max(r["prob"] if r["up"] == 1 else 1 - r["prob"], 1e-12), 1 - 1e-12))
             for r in rows) / n

    days = sorted({r["day"] for r in rows})
    per = [(sum(1 for r in rows if r["day"] == d),
            sum(1 for r in rows if r["day"] == d and (r["prob"] >= 0.5) == (r["up"] == 1)))
           for d in days]
    deff = design_effect([h / nn for nn, h in per], sum(nn for nn, _ in per) / len(per), acc)
    eff = n / deff
    lo, hi = wilson(round(acc * eff), round(eff))

    buckets = []
    for lo_e, hi_e, label in EDGES:
        sel = [r for r in rows if lo_e <= r["prob"] < hi_e]
        if not sel:
            continue
        a = sum(r["up"] for r in sel) / len(sel)
        blo, bhi = wilson(sum(r["up"] for r in sel), len(sel))
        buckets.append({"label": label, "n": len(sel),
                        "said": sum(r["prob"] for r in sel) / len(sel),
                        "actual": a, "ci_lo": blo, "ci_hi": bhi,
                        "vs_base": a - up_rate})
    ece = sum(b["n"] / n * abs(b["said"] - b["actual"]) for b in buckets)

    return {"n": n, "days": len(days), "up_rate": up_rate, "baseline_acc": base,
            "accuracy": acc, "acc_lift": acc - base, "acc_ci": [lo, hi],
            "brier": brier, "brier_baseline": brier_base,
            "brier_skill": (1 - brier / brier_base) if brier_base else 0.0,
            "log_loss": ll, "ece": ece,
            "design_effect": deff, "effective_n": eff, "buckets": buckets}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default="data/signaldeck.db")
    ap.add_argument("--horizon", default="1d")
    ap.add_argument("--start", required=True)
    ap.add_argument("--end", required=True)
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args()

    con = sqlite3.connect(f"file:{a.db}?mode=ro", uri=True)
    rows = [{"day": d, "symbol_id": s, "prob": p, "up": u}
            for d, s, p, u in con.execute(HOLDOUT_SQL, (a.horizon, a.start, a.end))]
    r = grade(rows)

    if a.json:
        print(json.dumps({**r, "holdout_sha": holdout_sha(),
                          "window": [a.start, a.end], "horizon": a.horizon}, indent=2))
        return
    if not r.get("n"):
        print(f"no rows in {a.start}..{a.end}")
        return

    print(f"holdout sha : {holdout_sha()}")
    print(f"window      : {a.start} .. {a.end}  horizon {a.horizon}")
    print(f"n           : {r['n']} symbol-days over {r['days']} trading day(s)")
    print(f"design eff. : {r['design_effect']:.2f}  -> effective n {r['effective_n']:.0f}"
          + ("   (UNMEASURED: needs >=2 days)" if r["days"] < 2 else ""))
    print()
    print(f"  up rate         {r['up_rate']*100:6.2f}%")
    print(f"  baseline        {r['baseline_acc']*100:6.2f}%   (always {'UP' if r['up_rate']>=0.5 else 'DOWN'})")
    print(f"  accuracy        {r['accuracy']*100:6.2f}%   95% CI [{r['acc_ci'][0]*100:.1f}%, {r['acc_ci'][1]*100:.1f}%]")
    print(f"  LIFT            {r['acc_lift']*100:+6.2f}pp")
    print(f"  Brier           {r['brier']:.4f}   baseline {r['brier_baseline']:.4f}   skill {r['brier_skill']:+.3f}")
    print(f"  log loss        {r['log_loss']:.4f}")
    print(f"  ECE             {r['ece']:.4f}")
    print()
    print(f"  {'bucket':<9} {'n':>6} {'said':>7} {'actual':>8} {'95% CI':>16} {'vs base':>9}")
    for b in r["buckets"]:
        print(f"  {b['label']:<9} {b['n']:>6} {b['said']*100:>6.1f}% {b['actual']*100:>7.1f}%"
              f"  [{b['ci_lo']*100:>5.1f}%,{b['ci_hi']*100:>6.1f}%] {b['vs_base']*100:>+8.1f}pp")
    if r["days"] < 10:
        print(f"\n  WITHHELD: {r['days']} distinct day(s). The accuracy registry requires 10.")
        print("  Nothing above supports a verdict; it is a description of a short window.")


if __name__ == "__main__":
    main()

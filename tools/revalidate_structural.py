#!/usr/bin/env python3
"""Re-validate the structural claims against the SURVIVORSHIP-CLEAN universe.

Why this is worth running
------------------------
trend21's 82-97% accuracies were measured on symbols the platform was actively
tracking. Every name that was dropped or delisted left that population, and the
names that die are disproportionately the ones whose downtrends never recovered
— exactly the cases a trend-persistence forecast should find EASY. So the
published number could be either inflated (dead names were easy wins we ignored)
or deflated (they were hard). Without running it, nobody knows which.

The survivorship-clean universe built on 2026-07-24 makes the test possible for
the first time: bars for delisted names were never deleted, only hidden from
research by an active=1 filter.

This is still a WALK-FORWARD BACKTEST, not a live record. It cannot and does not
replace the live grades that begin 2026-08-07. What it can do is say whether the
claim survives contact with the graveyard.

Method, matching the engine's own rules
---------------------------------------
  * forecast: is close on the same side of SMA200 in 21 sessions?
  * conviction: percentile of |close/SMA200 - 1| within its trailing 200 days
  * NON-OVERLAPPING sampling (step 21 sessions) so forward windows never share
    days — overlapping daily samples inflate n ~21x and lie
  * quarter-block bootstrap CIs, because days inside a quarter are correlated
  * contaminated windows refused exactly as the engine refuses them

THE GEOMETRY CONTROL (added 2026-08-03 — read this before quoting any number)
----------------------------------------------------------------------------
This report used to end by calling the conviction spread "THE ACTUAL EDGE".
That was wrong, and the control below is why.

Conviction ranks |close/SMA200 - 1|. The forecast asks whether close is still
on the same side of SMA200 in 21 sessions. A price far from the line needs a
large move to cross it; a price sitting on the line crosses on noise. So high
conviction predicts "correct" for a reason that has nothing to do with markets.
It is distance to a barrier. It is arithmetic.

The control measures the barrier distance directly, in units the horizon can
actually move:

    z = |close/SMA200 - 1| / (sigma_60d * sqrt(21))

and compares realised accuracy to Phi(z), the probability that a DRIFTLESS
RANDOM WALK ends the horizon on the side it started. Phi(z) has no free
parameters and no knowledge of anything. It is the pure-geometry null.

Measured 2026-08-03 on the survivorship-clean universe (57,158 non-overlapping
samples, 1,305 days, 1,090 symbols):

  * realised accuracy tracks Phi(z) to within ~2pp in EVERY z band
    (55.10% vs 54.99%, 64.76% vs 64.51%, 81.07% vs 80.82%, ...)
  * the +24.58pp conviction spread collapses to +6.31pp inside z bands,
    so roughly three quarters of it is barrier distance
  * out-of-sample (Hansen SPA, stationary block bootstrap over dates,
    expanding-window refits): conviction does NOT add over z, p=0.204.
    z DOES add over conviction, p<0.001. Brier: z 0.1165, conviction 0.1225.

CONCLUSION: trend21 has no demonstrated forecasting skill. It is a
barrier-distance calculator, and conviction is a worse-parameterised proxy for
z. The 82% headline is the base rate (this report already refused to call that
skill) and the band spread is geometry. Neither is an edge.

This does not make the number useless. Knowing how reliable a call is remains
worth having for SIZING. It is not worth having as a PREDICTION, and the two
must not be quoted interchangeably.

Usage:  python3 tools/revalidate_structural.py [--db PATH]
"""
from __future__ import annotations

import argparse
import math
import os
import random
import sqlite3
import statistics
import sys
from collections import defaultdict

DEFAULT_DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                          "data", "signaldeck.db")

SMA_LEN = 200
HORIZON = 21
RANK_WINDOW = 200
VOL_WINDOW = 60             # trailing window for realised vol, for the z control
MIN_BARS = SMA_LEN + RANK_WINDOW + HORIZON + 5
MAX_SANE_RETURN = 0.65      # the engine's wild-move guard
BOOTSTRAP = 2000

BANDS = [(0.0, 0.5, "low (<0.5)"), (0.5, 0.8, "moderate (0.5-0.8)"),
         (0.8, 0.9, "high (0.8-0.9)"), (0.9, 1.01, "very-high (>=0.9)")]

# Barrier distance in 21-day sigma units. Fixed before looking at any outcome.
Z_BANDS = [(0.0, 0.25), (0.25, 0.5), (0.5, 0.75), (0.75, 1.0), (1.0, 1.5),
           (1.5, 2.0), (2.0, 3.0), (3.0, 4.0), (4.0, 6.0), (6.0, float("inf"))]


def sma(vals, n):
    out, run = [None] * len(vals), 0.0
    for i, v in enumerate(vals):
        run += v
        if i >= n:
            run -= vals[i - n]
        out[i] = run / n if i >= n - 1 else None
    return out


def phi(x):
    """Standard normal CDF. Stdlib only: this tool has no numpy dependency and
    should not grow one just to run its own control."""
    return 0.5 * (1.0 + math.erf(x / math.sqrt(2.0)))


def trailing_vol(closes, i, n=VOL_WINDOW):
    """Population sd of daily log returns over the n bars ending at i.
    Uses no data after i."""
    if i < n:
        return None
    rets = []
    for j in range(i - n + 1, i + 1):
        if closes[j - 1] <= 0 or closes[j] <= 0:
            return None
        rets.append(math.log(closes[j] / closes[j - 1]))
    return statistics.pstdev(rets) if len(rets) == n else None


def wilson(k, n, z=1.96):
    if n <= 0:
        return (0.0, 0.0)
    p = k / n
    d = 1 + z * z / n
    c = (p + z * z / (2 * n)) / d
    m = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / d
    return (max(0.0, c - m), min(1.0, c + m))


def quarter_bootstrap(by_quarter, iters=BOOTSTRAP):
    """Resample whole QUARTERS, not observations: days inside a quarter share a
    market regime, so treating them as independent manufactures confidence."""
    keys = list(by_quarter)
    if len(keys) < 4:
        return (None, None)
    accs = []
    rng = random.Random(12345)          # seeded: the number must be reproducible
    for _ in range(iters):
        hit = tot = 0
        for _ in range(len(keys)):
            k = keys[rng.randrange(len(keys))]
            h, t = by_quarter[k]
            hit += h
            tot += t
        if tot:
            accs.append(hit / tot)
    if not accs:
        return (None, None)
    accs.sort()
    return (accs[int(0.025 * len(accs))], accs[int(0.975 * len(accs))])


def evaluate(rows):
    """rows: list of (ts, close).

    Returns list of (conviction, correct, quarter, z), where z is the barrier
    distance in 21-day sigma units and is None when trailing vol is unusable.
    z is what the geometry control in report() tests conviction against.
    """
    closes = [r[1] for r in rows]
    ts = [r[0] for r in rows]
    n = len(closes)
    s = sma(closes, SMA_LEN)

    absd = [None] * n
    for i in range(n):
        if s[i] and s[i] > 0:
            absd[i] = abs(closes[i] / s[i] - 1)

    out = []
    start = SMA_LEN + RANK_WINDOW
    # NON-OVERLAPPING: step a full horizon so no two samples share forward days.
    for i in range(start, n - HORIZON, HORIZON):
        if not s[i] or s[i] <= 0 or absd[i] is None:
            continue
        # Refuse contaminated windows exactly as the engine does.
        wild = False
        for j in range(max(1, i - SMA_LEN), i + 1):
            if closes[j - 1] > 0 and abs(closes[j] / closes[j - 1] - 1) > MAX_SANE_RETURN:
                wild = True
                break
        if wild:
            continue
        window = [x for x in absd[i - RANK_WINDOW:i] if x is not None]
        if len(window) < RANK_WINDOW // 2:
            continue
        conv = sum(1 for x in window if x <= absd[i]) / len(window)

        up_now = closes[i] > s[i]
        j = i + HORIZON
        if not s[j] or s[j] <= 0:
            continue
        up_then = closes[j] > s[j]
        correct = (up_now == up_then)

        # Geometry control input: how far is the barrier, in units the horizon
        # can actually move? sigma uses only bars at or before i.
        sig = trailing_vol(closes, i)
        z = absd[i] / (sig * math.sqrt(HORIZON)) if sig and sig > 0 else None

        import datetime
        d = datetime.datetime.utcfromtimestamp(ts[i])
        out.append((conv, correct, f"{d.year}Q{(d.month - 1) // 3 + 1}", z))
    return out


def report(label, samples):
    print(f"\n{'='*78}\n{label}\n{'='*78}")
    if not samples:
        print("  no evaluable samples")
        return
    print("%-22s %8s %9s %-22s %s" % ("CONVICTION BAND", "n", "accuracy", "95% CI (quarter)", "Wilson"))
    print("-" * 78)
    for lo, hi, name in BANDS:
        sel = [s for s in samples if lo <= s[0] < hi]
        if not sel:
            print("%-22s %8s" % (name, "0"))
            continue
        hits = sum(1 for s in sel if s[1])
        byq = defaultdict(lambda: [0, 0])
        for s in sel:
            q, ok = s[2], s[1]
            byq[q][1] += 1
            if ok:
                byq[q][0] += 1
        qlo, qhi = quarter_bootstrap({k: tuple(v) for k, v in byq.items()})
        wlo, whi = wilson(hits, len(sel))
        ci = f"[{qlo:.3f}, {qhi:.3f}]" if qlo is not None else "(too few quarters)"
        print("%-22s %8d %8.1f%% %-22s [%.3f, %.3f]" %
              (name, len(sel), 100 * hits / len(sel), ci, wlo, whi))
    hits = sum(1 for s in samples if s[1])
    print("-" * 78)
    print("%-22s %8d %8.1f%%   distinct quarters: %d" %
          ("ALL", len(samples), 100 * hits / len(samples),
           len({s[2] for s in samples})))
    # THE HONEST NULL, and the point of this whole report.
    #
    # The naive strategy for "will price stay on the same side of its 200-day
    # average" is to ALWAYS answer yes. That scores exactly the base rate of
    # persistence — which is the same number as the model's overall accuracy,
    # because the model answers yes almost every time too.
    #
    # So the overall figure is NOT skill. Quoting "83% accurate" as a headline
    # would be claiming credit for the base rate. The model's real contribution
    # is DISCRIMINATION: sorting calls into bands whose accuracy actually
    # differs (~73% at low conviction vs ~98% at very-high). That spread is the
    # product; the average is not.
    persist = sum(1 for s in samples if s[1]) / len(samples)
    print("%-22s %8s %8.1f%%   <- ALWAYS-PERSISTS baseline == the overall figure."
          % ("NAIVE BASELINE", "", 100 * persist))
    lows = [s for s in samples if s[0] < 0.5]
    highs = [s for s in samples if s[0] >= 0.9]
    uncond = None
    if lows and highs:
        la = sum(1 for s in lows if s[1]) / len(lows)
        ha = sum(1 for s in highs if s[1]) / len(highs)
        uncond = ha - la
        print("%-22s %8s %8.1fpp  <- spread only. NOT an edge until the geometry "
              "control below\n%-22s %8s          says how much of it survives "
              "(low %.1f%% vs very-high %.1f%%)"
              % ("DISCRIMINATION", "", uncond * 100, "", "", la * 100, ha * 100))
    geometry_control(samples, uncond)


def geometry_control(samples, uncond):
    """Is the conviction spread anything more than distance to the barrier?

    Two checks, both cheap and both decisive enough to belong in the same report
    as the claim they qualify:

      1. Compare realised accuracy to Phi(z), the driftless-random-walk
         probability of ending the horizon on the starting side. Phi(z) has zero
         free parameters. If accuracy tracks it, the model is measuring geometry.
      2. Re-measure the conviction spread WITHIN z bands. Whatever survives is
         the part conviction contributes that barrier distance does not.

    A full out-of-sample test (Hansen SPA against a z-only benchmark) lives
    outside this stdlib tool; see the module docstring for its 2026-08-03 result.
    """
    with_z = [s for s in samples if len(s) > 3 and s[3] is not None]
    if len(with_z) < 500:
        print("\n  GEOMETRY CONTROL: too few samples with usable trailing vol")
        return

    print("\n  " + "-" * 74)
    print("  GEOMETRY CONTROL - conviction vs barrier distance z = dist/(sigma*sqrt(21))")
    print("  " + "-" * 74)
    print("  %-14s %7s %9s %9s %11s %10s" %
          ("z band", "n", "actual", "Phi(z)", "act-Phi", "conv spread"))

    num = den = 0.0
    for lo, hi in Z_BANDS:
        sel = [s for s in with_z if lo <= s[3] < hi]
        if len(sel) < 200:
            continue
        acc = sum(1 for s in sel if s[1]) / len(sel)
        pred = sum(phi(s[3]) for s in sel) / len(sel)
        bl = [s for s in sel if s[0] < 0.5]
        bh = [s for s in sel if s[0] >= 0.9]
        if len(bl) >= 50 and len(bh) >= 50:
            sp = (sum(1 for s in bh if s[1]) / len(bh)
                  - sum(1 for s in bl if s[1]) / len(bl))
            num += len(sel) * sp
            den += len(sel)
            sp_s = "%+.2fpp" % (sp * 100)
        else:
            sp_s = "-"
        label = f"[{lo:.2f},{hi:.2f})" if hi != float("inf") else f">={lo:.2f}"
        print("  %-14s %7d %8.2f%% %8.2f%% %10.2fpp %10s" %
              (label, len(sel), acc * 100, pred * 100, (acc - pred) * 100, sp_s))

    print("  " + "-" * 74)
    allacc = sum(1 for s in with_z if s[1]) / len(with_z)
    allphi = sum(phi(s[3]) for s in with_z) / len(with_z)
    print("  overall actual %.2f%%  vs  Phi(z) %.2f%%   (gap %+.2fpp)"
          % (allacc * 100, allphi * 100, (allacc - allphi) * 100))
    if den and uncond is not None:
        within = num / den
        kept = 100 * within / uncond if uncond else float("nan")
        print("  conviction spread: unconditional %+.2fpp  ->  within-z %+.2fpp"
              "   (%.0f%% survives)" % (uncond * 100, within * 100, kept))
        print("  => %.0f%% of the 'discrimination' is barrier distance, not forecasting."
              % (100 - kept))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=DEFAULT_DB)
    args = ap.parse_args()
    con = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    c = con.cursor()

    syms = c.execute(
        "SELECT id, symbol, active FROM symbols WHERE market='stocks'").fetchall()
    live_samples, dead_samples = [], []
    scanned = skipped = 0
    for sid, sym, active in syms:
        rows = c.execute(
            "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND close>0 ORDER BY ts",
            (sid,)).fetchall()
        if len(rows) < MIN_BARS:
            skipped += 1
            continue
        scanned += 1
        s = evaluate(rows)
        (live_samples if active else dead_samples).append((sym, s))

    flat = lambda pack: [x for _, xs in pack for x in xs]
    live, dead = flat(live_samples), flat(dead_samples)

    print(f"symbols with enough history: {scanned}  (skipped {skipped} too-short)")
    print(f"  active: {len(live_samples)}   delisted/inactive: {len(dead_samples)}")
    report("TREND21 — ACTIVE ONLY (what the published claim was measured on)", live)
    report("TREND21 — DELISTED / INACTIVE ONLY (the survivorship blind spot)", dead)
    report("TREND21 — SURVIVORSHIP-CLEAN (every symbol ever tracked)", live + dead)

    if live and dead:
        la = sum(1 for s in live if s[1]) / len(live)
        da = sum(1 for s in dead if s[1]) / len(dead)
        print(f"\nVERDICT: active {la*100:.1f}%  vs  delisted {da*100:.1f}%  "
              f"=> survivorship effect {(la-da)*100:+.1f}pp")
        print("  positive => the published claim was INFLATED by excluding dead names")
        print("  negative => dead names were EASIER; the claim was conservative")
    return 0


if __name__ == "__main__":
    sys.exit(main())

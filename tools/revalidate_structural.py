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
MIN_BARS = SMA_LEN + RANK_WINDOW + HORIZON + 5
MAX_SANE_RETURN = 0.65      # the engine's wild-move guard
BOOTSTRAP = 2000

BANDS = [(0.0, 0.5, "low (<0.5)"), (0.5, 0.8, "moderate (0.5-0.8)"),
         (0.8, 0.9, "high (0.8-0.9)"), (0.9, 1.01, "very-high (>=0.9)")]


def sma(vals, n):
    out, run = [None] * len(vals), 0.0
    for i, v in enumerate(vals):
        run += v
        if i >= n:
            run -= vals[i - n]
        out[i] = run / n if i >= n - 1 else None
    return out


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
    """rows: list of (ts, close). Returns list of (conviction, correct, quarter)."""
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

        import datetime
        d = datetime.datetime.utcfromtimestamp(ts[i])
        out.append((conv, correct, f"{d.year}Q{(d.month - 1) // 3 + 1}"))
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
        for c, ok, q in sel:
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
    if lows and highs:
        la = sum(1 for s in lows if s[1]) / len(lows)
        ha = sum(1 for s in highs if s[1]) / len(highs)
        print("%-22s %8s %8.1fpp  <- THE ACTUAL EDGE: low-conviction %.1f%% vs "
              "very-high %.1f%%" % ("DISCRIMINATION", "", (ha - la) * 100,
                                    la * 100, ha * 100))


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

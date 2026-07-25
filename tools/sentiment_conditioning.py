#!/usr/bin/env python3
"""Phase 2 — does news sentiment sharpen trend21, or add nothing?

THE HYPOTHESIS
--------------
Sentiment was wired into the DIRECTIONAL ensemble, which is now retired at 48%
against a 54% baseline. That leaves the sentiment pipeline orphaned: it runs,
tags headlines, and feeds nothing that works.

The question worth asking is different from the one that failed. Not "can
sentiment predict direction" — six independent tests say no. Instead: given
that trend21 already works, does its accuracy DIFFER when sentiment agrees with
the trend versus contradicts it? That is a conditioning question, not a
prediction question, and it is a far more plausible bet: sentiment would be
sharpening a signal that already has edge rather than manufacturing one.

WHAT WOULD COUNT AS A RESULT
----------------------------
A meaningful split needs the agree and disagree buckets to differ by more than
sampling noise, judged by a two-proportion test rather than by eyeballing the
gap. Anything else is a coin landing heads twice.

Absence of a split is ALSO a result, and a useful one: it retires the last
reason to keep the sentiment pipeline running, which is worth knowing.

THE GATE
--------
This refuses to report a number it cannot support. Both buckets need
MIN_PER_BUCKET independent observations, and trend21 outcomes only begin
resolving 2026-08-08 (earliest forecast 2026-07-18 + a 21-session horizon), so
before then the honest output is "no data yet" rather than a figure computed
from nothing.

Usage:  python3 tools/sentiment_conditioning.py [--db PATH] [--min-n 30]
"""
from __future__ import annotations

import argparse
import math
import os
import sqlite3
import sys

DEFAULT_DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                          "data", "signaldeck.db")

MIN_PER_BUCKET = 30
# Sentiment scores cluster near zero; this band is treated as "no opinion" so a
# near-neutral score is not forced into an agree/disagree bucket it does not
# belong in.
NEUTRAL_BAND = 0.05


def wilson(k, n, z=1.96):
    if n <= 0:
        return (0.0, 0.0)
    p = k / n
    d = 1 + z * z / n
    c = (p + z * z / (2 * n)) / d
    m = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / d
    return (max(0.0, c - m), min(1.0, c + m))


def two_proportion_z(k1, n1, k2, n2):
    """Two-proportion z-test. Returns (z, p_two_sided) or (None, None)."""
    if n1 == 0 or n2 == 0:
        return (None, None)
    p1, p2 = k1 / n1, k2 / n2
    pool = (k1 + k2) / (n1 + n2)
    se = math.sqrt(pool * (1 - pool) * (1 / n1 + 1 / n2))
    if se == 0:
        return (None, None)
    z = (p1 - p2) / se
    # Two-sided p via the normal CDF complement.
    p = 2 * (1 - 0.5 * (1 + math.erf(abs(z) / math.sqrt(2))))
    return (z, p)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--min-n", type=int, default=MIN_PER_BUCKET)
    args = ap.parse_args()

    con = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    c = con.cursor()

    # Join each RESOLVED trend21 call to that symbol's sentiment on the day the
    # call was made. Sentiment must be as-of the forecast, never later — using
    # sentiment from after the call would be lookahead and would manufacture an
    # edge that cannot be traded.
    rows = c.execute("""
        SELECT o.regime, o.correct, s.mean_score, s.n
        FROM regime_outcomes o
        JOIN sentiment_daily s
          ON s.symbol_id = o.symbol_id
         AND s.day = date(o.ts, 'unixepoch')
        WHERE o.kind = 'trend21'
          AND o.resolved_at IS NOT NULL
          AND o.correct IN (0,1)
          AND s.n > 0
    """).fetchall()

    agree = [0, 0]     # [correct, total]
    disagree = [0, 0]
    neutral = [0, 0]
    for regime, correct, score, _n in rows:
        if score is None:
            continue
        bullish_trend = (regime == "uptrend")
        if abs(score) < NEUTRAL_BAND:
            bucket = neutral
        elif (score > 0) == bullish_trend:
            bucket = agree
        else:
            bucket = disagree
        bucket[1] += 1
        bucket[0] += int(correct)

    print("=" * 74)
    print("PHASE 2 — does sentiment sharpen trend21?")
    print("=" * 74)
    print(f"resolved trend21 calls with same-day sentiment: {len(rows)}")
    print()

    def line(label, b):
        if b[1] == 0:
            print(f"  {label:<28} n=0")
            return
        lo, hi = wilson(b[0], b[1])
        print(f"  {label:<28} n={b[1]:<6} accuracy {100*b[0]/b[1]:5.1f}%  "
              f"95% CI [{lo:.3f}, {hi:.3f}]")

    line("sentiment AGREES with trend", agree)
    line("sentiment DISAGREES", disagree)
    line("sentiment neutral (|s|<0.05)", neutral)
    print()

    if agree[1] < args.min_n or disagree[1] < args.min_n:
        # The honest output before 2026-08-08. Reporting a split from a handful
        # of observations would be exactly the overfitting-to-noise this repo
        # has already been burned by twice.
        print(f"VERDICT: NO RESULT — need {args.min_n} independent observations per "
              f"bucket, have agree={agree[1]} disagree={disagree[1]}.")
        first = c.execute("""SELECT date(MIN(ts)+21*86400,'unixepoch')
                             FROM regime_outcomes WHERE kind='trend21'""").fetchone()
        if first and first[0]:
            print(f"         trend21 outcomes begin resolving {first[0]}; this test "
                  f"becomes answerable once enough have accrued.")
        print("         A number computed from fewer observations would not be an "
              "answer — it would be noise wearing a decimal point.")
        return 0

    z, p = two_proportion_z(agree[0], agree[1], disagree[0], disagree[1])
    gap = (agree[0] / agree[1] - disagree[0] / disagree[1]) * 100
    print(f"  gap (agree − disagree): {gap:+.1f}pp    z={z:.2f}  p={p:.4f}")
    print()
    if p is not None and p < 0.05:
        print("VERDICT: REAL SPLIT. Sentiment carries information ABOUT trend21's "
              "reliability.")
        print("         Next step is conditioning the shipped conviction on it — NOT "
              "trading sentiment directly, which is the thing already disproven.")
    else:
        print("VERDICT: NO SPLIT. Sentiment does not distinguish reliable trend21 "
              "calls from unreliable ones.")
        print("         That retires the last live reason to run the sentiment "
              "pipeline, which is a useful thing to know rather than a failure.")
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Re-derive EVERY cross-sectional factor leg's edge from the live database.

Why this file exists
--------------------
The constants in ``daemon/internal/xsfactor/edge.go`` shipped from a script that
was never committed. A shipped constant nobody can re-derive is not a
measurement, it is an assertion — and a reviewer replicating the liquidity leg
independently got the OPPOSITE SIGN from what the endpoint publishes. This
script is the missing derivation: run it and every number in ``edge.go`` comes
back out, or the constants are wrong and must change.

It is READ-ONLY (``mode=ro``). It writes nothing to the database, ever.

The question being measured
---------------------------
Exactly the one the endpoint claims, and no other:

    "will this symbol beat the same-day universe MEDIAN forward return?"

That label is a median split, so its base rate is exactly 50% and measured
accuracy IS skill — there is no class imbalance to hide behind. A leg's EDGE is
``(accuracy - 50%)`` in percentage points, where the leg's forecast is simply
``legPercentile > 0.5``.

Method, and every choice in it is a defensive one
-------------------------------------------------
  * ONE OBSERVATION PER (symbol, UTC-day). The bar table is asserted to contain
    no duplicate symbol-day before anything is measured. Raw row counts are
    never a sample size here.
  * NON-OVERLAPPING forward windows. Formation days are taken every H sessions
    along a GLOBAL trading-day calendar, so no two forward windows share an
    interior day. Overlapping daily sampling would inflate n by ~H and
    manufacture confidence that is not in the data.
  * EQUITY ONLY (``market='stocks'``). The constants under audit claimed "968
    stocks"; pooling crypto's volatility into the same percentile would make the
    rank incomparable to that claim, and to the endpoint's own default universe.
  * DAY-CLUSTERED bootstrap intervals. The resampling unit is the FORMATION DAY,
    never the observation: ~1,000 symbols on one day share one market move, so
    treating them as independent is the single most common way a factor study
    lies about its own precision.
  * METRICS COMPUTED EXACTLY AS THE ENGINE COMPUTES THEM. Window lengths, the
    split guard, the tie-averaged percentile mapping and the "absent leg is
    absent, never zero" rule are all ports of
    ``daemon/internal/xsfactor/xsfactor.go``. If this script and the engine
    disagree about a percentile, the replication is measuring something else.
  * THE SPLIT GUARD IS APPLIED TO THE FORWARD WINDOW TOO. A 4:1 split inside the
    forward window is not a -75% return, it is a data artifact, and one of them
    lands in the tail that decides a median split. ``--no-forward-guard`` runs it
    off so the sensitivity is visible rather than assumed.
  * SURVIVORSHIP. ``--universe all`` (the default) ranks every stock ever
    tracked, including the 749 delisted names whose bars were kept. ``--universe
    active`` reproduces the narrower live universe. Both are reported because
    the difference between them IS the survivorship bound.

What this script CANNOT do
--------------------------
It is a walk-forward BACKTEST over stored bars, not a live track record, and the
universe was seeded from 2026 survivors so the delisted arm is itself incomplete.
A symbol that stops trading inside a forward window has no forward return and is
dropped from that day's cross-section; the count is reported, not hidden.

Usage
-----
    python3 tools/xsfactor_edge.py                       # full report
    python3 tools/xsfactor_edge.py --universe active
    python3 tools/xsfactor_edge.py --out daemon/internal/xsfactor/derivation.json
"""
from __future__ import annotations

import argparse
import bisect
import json
import math
import os
import random
import sqlite3
import sys
from array import array
from collections import deque

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_DB = os.path.join(REPO, "data", "signaldeck.db")

# ---------------------------------------------------------------------------
# Constants ported VERBATIM from daemon/internal/xsfactor/xsfactor.go. Changing
# one here without changing it there makes this script measure a factor the
# endpoint does not ship.
# ---------------------------------------------------------------------------
TRAILING_BARS = 300           # xsfactor.TrailingBars
VOL_WINDOW = 21               # xsfactor.volWindow (returns)
DOLLAR_VOL_WINDOW = 21        # xsfactor.dollarVolWindow (days)
MOM_LOOKBACK = 252            # xsfactor.momLookback
MOM_SKIP = 21                 # xsfactor.momSkip
MIN_VOL_CLOSES = VOL_WINDOW + 1
MOM_MIN_CLOSES = MOM_LOOKBACK + 1
TRADING_DAYS_PER_YEAR = 252.0
IMPOSSIBLE_DAILY_MOVE = 0.65  # xsfactor.ImpossibleDailyMove
MIN_UNIVERSE = 20             # api.xsFactorMinUniverse — a percentile over fewer
                              # symbols is noise, not a rank

LEG_LIQUIDITY = "liquidity"
LEG_LOW_VOL = "lowVol"
LEG_MOM121 = "mom12_1"
LEGS = [LEG_LIQUIDITY, LEG_LOW_VOL, LEG_MOM121]

HORIZON_SESSIONS = {"5d": 5, "21d": 21, "63d": 63}
HORIZONS = ["5d", "21d", "63d"]

BOOTSTRAP = 20000
SEED = 12345                  # seeded: a number nobody can reproduce is not a number

# MULTIPLICITY. This script runs 3 legs x 3 horizons = 9 tests against the same
# database, so a 95% interval on the best-looking one is not a 95% statement.
# Every leg therefore also carries a Bonferroni-corrected interval at
# alpha/FAMILY_TESTS, and the report says which legs survive it. The correction
# is disclosed, never used to quietly widen a claim.
FAMILY_TESTS = 9

# The constants under audit, as they shipped on 2026-07-24, recorded here ONLY
# so the report can print measured-vs-shipped side by side. This script never
# reads them for anything else — they are the claim, not the input.
SHIPPED_AT_AUDIT = {
    "5d":  {LEG_LIQUIDITY: (1.46, 1.01, 1.86),
            LEG_LOW_VOL:   (1.56, 0.67, 2.54),
            LEG_MOM121:    (1.47, 0.67, 2.30)},
    "21d": {LEG_LIQUIDITY: (2.50, 1.61, 3.37),
            LEG_LOW_VOL:   (2.80, 0.81, 4.66)},
    "63d": {LEG_LIQUIDITY: (3.04, 1.85, 4.27),
            LEG_LOW_VOL:   (3.56, 0.14, 7.00)},
}


# ---------------------------------------------------------------------------
# Percentile mapping — a port of xsfactor.percentiles. Ties share the average of
# their ranks; the lowest value maps to 0, the highest to 1; a single-member
# cross-section gets 0.5, because a percentile against yourself is meaningless.
# ---------------------------------------------------------------------------
def percentiles(idx_vals):
    n = len(idx_vals)
    if n == 0:
        return {}
    if n == 1:
        return {idx_vals[0][0]: 0.5}
    srt = sorted(v for _, v in idx_vals)
    out = {}
    for i, v in idx_vals:
        lo = bisect.bisect_left(srt, v)
        hi = bisect.bisect_right(srt, v)
        out[i] = (lo + (hi - lo - 1) / 2.0) / (n - 1)
    return out


class Series:
    """One symbol's daily bars plus the rolling quantities the legs need.

    Everything is precomputed once per symbol so a formation day costs O(1)
    lookups instead of re-walking a 300-bar window ~400,000 times.
    """

    __slots__ = ("sym", "active", "days", "close", "dvol", "day_idx",
                 "s1", "s2", "maxabs", "ret")

    def __init__(self, sym, active, days, close, dvol):
        self.sym = sym
        self.active = active
        self.days = days
        self.close = close
        self.dvol = dvol
        self.day_idx = {d: i for i, d in enumerate(days)}
        n = len(close)

        # Simple daily returns; ret[0] is a placeholder that no window reads.
        ret = array("d", [0.0])
        for k in range(1, n):
            ret.append(close[k] / close[k - 1] - 1.0)
        self.ret = ret

        # Prefix sums of r and r^2 → the 21-return sample sd in O(1).
        s1 = array("d", [0.0])
        s2 = array("d", [0.0])
        acc1 = acc2 = 0.0
        for k in range(1, n):
            acc1 += ret[k]
            acc2 += ret[k] * ret[k]
            s1.append(acc1)
            s2.append(acc2)
        self.s1, self.s2 = s1, s2

        # maxabs[k] = max |return| inside the trailing TRAILING_BARS window
        # ending at k — the engine's split guard, as a sliding-window maximum.
        maxabs = array("d", [0.0])
        dq = deque()          # indices, |ret| decreasing
        for k in range(1, n):
            lo = max(1, k - (TRAILING_BARS - 2))   # window closes[max(0,k-299)..k]
            a = abs(ret[k])
            while dq and abs(ret[dq[-1]]) <= a:
                dq.pop()
            dq.append(k)
            while dq[0] < lo:
                dq.popleft()
            maxabs.append(abs(ret[dq[0]]))
        self.maxabs = maxabs

    def window_len(self, k):
        """Length of the trailing window the engine would have been handed."""
        return k - max(0, k - (TRAILING_BARS - 1)) + 1

    def rejected(self, k):
        """True when rejectSeries would refuse this trailing window outright."""
        return self.maxabs[k] > IMPOSSIBLE_DAILY_MOVE

    def realized_vol(self, k):
        if self.window_len(k) < MIN_VOL_CLOSES:
            return None
        s = self.s1[k] - self.s1[k - VOL_WINDOW]
        q = self.s2[k] - self.s2[k - VOL_WINDOW]
        mean = s / VOL_WINDOW
        var = (q - VOL_WINDOW * mean * mean) / (VOL_WINDOW - 1)
        if var < 0:                      # only reachable from float cancellation
            var = 0.0
        sd = math.sqrt(var) * math.sqrt(TRADING_DAYS_PER_YEAR)
        if math.isnan(sd) or math.isinf(sd):
            return None
        return sd

    def med_dollar_vol(self, k):
        if self.window_len(k) < DOLLAR_VOL_WINDOW:
            return None
        tail = self.dvol[k - DOLLAR_VOL_WINDOW + 1:k + 1]
        for v in tail:
            if v < 0 or math.isnan(v) or math.isinf(v):
                return None
        m = sorted(tail)[DOLLAR_VOL_WINDOW // 2]  # 21 is odd → the middle value
        if m <= 0:                        # no real turnover: ABSENT, not zero
            return None
        return m

    def mom121(self, k):
        if self.window_len(k) < MOM_MIN_CLOSES:
            return None
        start = self.close[k - MOM_LOOKBACK]
        if start <= 0:
            return None
        m = self.close[k - MOM_SKIP] / start - 1.0
        if math.isnan(m) or math.isinf(m):
            return None
        return m

    def forward_split_artifact(self, k, k2):
        """True when the forward window itself contains a split-sized move."""
        for j in range(k + 1, k2 + 1):
            if abs(self.ret[j]) > IMPOSSIBLE_DAILY_MOVE:
                return True
        return False


def load(db_path, universe):
    con = sqlite3.connect("file:%s?mode=ro" % db_path, uri=True)
    c = con.cursor()

    # HOUSE INVARIANT, asserted rather than assumed: one observation per
    # (symbol, UTC-day). If this ever fires, every count below is a lie.
    dups = c.execute(
        "SELECT COUNT(*) FROM (SELECT b.symbol_id, b.ts/86400 d FROM bars b "
        "JOIN symbols s ON s.id=b.symbol_id WHERE b.tf='1d' AND s.market='stocks' "
        "GROUP BY 1,2 HAVING COUNT(*)>1)").fetchone()[0]
    if dups:
        raise SystemExit("ABORT: %d (symbol, UTC-day) duplicates in bars — the "
                         "one-observation-per-symbol-day invariant is broken and "
                         "no sample size computed from this table is meaningful" % dups)

    where = "s.market='stocks'"
    if universe == "active":
        where += " AND s.active=1"
    rows = c.execute(
        "SELECT b.symbol_id, s.symbol, s.active, b.ts/86400, b.close, b.volume "
        "FROM bars b JOIN symbols s ON s.id=b.symbol_id "
        "WHERE b.tf='1d' AND " + where + " ORDER BY b.symbol_id, b.ts").fetchall()
    con.close()

    out, cur = [], None
    sid = sym = act = None
    days = close = dvol = None

    def flush():
        if cur is not None and len(days) >= MIN_VOL_CLOSES:
            out.append(Series(sym, act, days, close, dvol))

    for r_sid, r_sym, r_act, r_day, r_close, r_vol in rows:
        if r_sid != sid:
            flush()
            sid, sym, act = r_sid, r_sym, r_act
            days, close, dvol = [], array("d"), array("d")
            cur = True
        days.append(r_day)
        close.append(r_close)
        dvol.append(r_close * r_vol)
    flush()
    return out


def bootstrap_ci(per_day, iters=BOOTSTRAP):
    """Intervals by resampling FORMATION DAYS, never observations.

    Day is the cluster: every symbol measured on one day shares that day's
    market move, so an observation-level interval would be 3-5x too narrow. This
    is the same unit the rest of the platform is being moved onto.

    Returns (lo95, hi95, loBonf, hiBonf) in percentage points of edge — the
    Bonferroni pair covers the FAMILY_TESTS tests this script runs at once.
    """
    if len(per_day) < 8:
        return (None, None, None, None)
    hits = [v[0] for v in per_day.values()]
    tots = [v[1] for v in per_day.values()]
    n = len(hits)
    pop = range(n)
    rng = random.Random(SEED)
    accs = []
    for _ in range(iters):
        hit = tot = 0
        for i in rng.choices(pop, k=n):
            hit += hits[i]
            tot += tots[i]
        if tot:
            accs.append(hit / tot)
    if not accs:
        return (None, None, None, None)
    accs.sort()
    m = len(accs)

    def at(q):
        return (accs[min(m - 1, max(0, int(q * m)))] - 0.5) * 100

    a = 0.05 / FAMILY_TESTS
    return (at(0.025), at(0.975), at(a / 2), at(1 - a / 2))


def measure(series, horizon, forward_guard=True):
    """Walk the calendar at a stride of H sessions and grade every leg."""
    h = HORIZON_SESSIONS[horizon]

    # The composites are diagnostics, not new claims: "shipped" is exactly what
    # /api/xs-factor averages today, so the report can answer "is the disputed
    # leg helping or hurting the number users actually see?"
    composites = {
        "composite_as_audited": sorted(SHIPPED_AT_AUDIT.get(horizon, {})),
        "composite_lowvol_only": [LEG_LOW_VOL],
        "composite_all3": LEGS,
    }

    cal = sorted({d for s in series for d in s.days})
    per_day = {leg: {} for leg in LEGS + list(composites)}
    totals = {leg: [0, 0] for leg in per_day}
    stats = {"formationDaysTried": 0, "formationDaysUsed": 0,
             "splitRejectedTrailing": 0, "splitRejectedForward": 0,
             "noForwardBar": 0, "tiedAtMedian": 0, "thinCrossSection": 0}

    for i in range(0, len(cal) - h, h):
        stats["formationDaysTried"] += 1
        day, fday = cal[i], cal[i + h]

        rows = []          # (series, k, k2, fwdRet)
        for s in series:
            k = s.day_idx.get(day)
            if k is None:
                continue
            if s.rejected(k):
                stats["splitRejectedTrailing"] += 1
                continue
            k2 = s.day_idx.get(fday)
            if k2 is None:
                # Symbol stopped trading (or has a hole) inside the window: it
                # has no H-session forward return, so it cannot be graded. Not
                # zero, not carried forward — absent, and counted.
                stats["noForwardBar"] += 1
                continue
            if forward_guard and s.forward_split_artifact(k, k2):
                stats["splitRejectedForward"] += 1
                continue
            rows.append((s, k, k2, s.close[k2] / s.close[k] - 1.0))

        if len(rows) < MIN_UNIVERSE:
            stats["thinCrossSection"] += 1
            continue
        stats["formationDaysUsed"] += 1

        # THE LABEL: beat this day's cross-sectional MEDIAN forward return. A
        # median split has a base rate of exactly 50%, so accuracy is skill.
        fwd = sorted(r[3] for r in rows)
        n = len(fwd)
        med = fwd[n // 2] if n % 2 else (fwd[n // 2 - 1] + fwd[n // 2]) / 2.0

        # Percentiles over the symbols that HAVE each metric — an absent leg is
        # absent from its own cross-section, never imputed.
        vol_vals, dol_vals, mom_vals = [], [], []
        for j, (s, k, _k2, _f) in enumerate(rows):
            v = s.realized_vol(k)
            if v is not None:
                vol_vals.append((j, v))
            d = s.med_dollar_vol(k)
            if d is not None:
                dol_vals.append((j, d))
            m = s.mom121(k)
            if m is not None:
                mom_vals.append((j, m))
        vol_p = percentiles(vol_vals) if len(vol_vals) >= MIN_UNIVERSE else {}
        dol_p = percentiles(dol_vals) if len(dol_vals) >= MIN_UNIVERSE else {}
        mom_p = percentiles(mom_vals) if len(mom_vals) >= MIN_UNIVERSE else {}

        day_hits = {leg: [0, 0] for leg in per_day}
        for j, (_s, _k, _k2, f) in enumerate(rows):
            if f == med:
                stats["tiedAtMedian"] += 1
                continue
            label = f > med
            # The engine's flips: THIN scores high on liquidity, CALM scores
            # high on low-vol, a strong prior-year trend scores high on momentum.
            legpct = {}
            if j in dol_p:
                legpct[LEG_LIQUIDITY] = 1 - dol_p[j]
            if j in vol_p:
                legpct[LEG_LOW_VOL] = 1 - vol_p[j]
            if j in mom_p:
                legpct[LEG_MOM121] = mom_p[j]
            for leg, p in legpct.items():
                if p == 0.5:            # no call: the leg is exactly at the median
                    continue
                day_hits[leg][1] += 1
                if (p > 0.5) == label:
                    day_hits[leg][0] += 1
            for name, members in composites.items():
                present = [legpct[l] for l in members if l in legpct]
                if not present:
                    continue
                comp = sum(present) / len(present)
                if comp == 0.5:
                    continue
                day_hits[name][1] += 1
                if (comp > 0.5) == label:
                    day_hits[name][0] += 1

        for leg, (hh, tt) in day_hits.items():
            if tt:
                per_day[leg][day] = (hh, tt)
                totals[leg][0] += hh
                totals[leg][1] += tt

    out = {"horizon": horizon, "sessions": h, "stats": stats,
           "calendarDays": len(cal), "legs": {}}
    for leg in per_day:
        hh, tt = totals[leg]
        if tt == 0:
            out["legs"][leg] = None
            continue
        acc = hh / tt
        lo, hi, blo, bhi = bootstrap_ci(per_day[leg])
        rnd = lambda v: None if v is None else round(v, 4)
        out["legs"][leg] = {
            "n": tt, "distinctDays": len(per_day[leg]),
            "accuracyPct": round(acc * 100, 4),
            "edgePP": round((acc - 0.5) * 100, 4),
            "ciLow": rnd(lo), "ciHigh": rnd(hi),
            "ciLowBonferroni": rnd(blo), "ciHighBonferroni": rnd(bhi),
            "survives95": lo is not None and (lo > 0 or hi < 0),
            "survivesBonferroni": blo is not None and (blo > 0 or bhi < 0),
        }
    return out


def verdict(measured, shipped):
    """One line a reader can act on, always naming the shipped claim."""
    if measured is None:
        return "NOT MEASURABLE"
    lo, hi = measured["ciLow"], measured["ciHigh"]
    claim = "" if shipped is None else " (shipped %+.2fpp [%+.2f, %+.2f])" % shipped
    if lo is None:
        return "NO INTERVAL — too few formation days" + claim
    if lo <= 0 <= hi:
        return ("indistinguishable from zero" +
                (" — CONTRADICTS shipped%s" % claim if shipped else claim))
    if shipped is None:
        return "unpublished leg, %s" % ("positive" if lo > 0 else "NEGATIVE")
    if (shipped[0] > 0) != (measured["edgePP"] > 0):
        return "*** SIGN INVERTED ***" + claim
    if shipped[1] <= measured["edgePP"] <= shipped[2] or lo <= shipped[0] <= hi:
        return "replicates" + claim
    return "same sign, magnitude disagrees" + claim


def report(title, results):
    print("\n" + "=" * 96)
    print(title)
    print("=" * 96)
    for r in results:
        h = r["horizon"]
        st = r["stats"]
        print("\nHORIZON %s  (stride %d sessions, NON-OVERLAPPING)  calendar days: %d"
              % (h, r["sessions"], r["calendarDays"]))
        print("  formation days used %d of %d tried   thin cross-sections skipped %d"
              % (st["formationDaysUsed"], st["formationDaysTried"], st["thinCrossSection"]))
        print("  dropped: trailing split-guard %d | forward split-guard %d | "
              "no forward bar %d | tied at median %d"
              % (st["splitRejectedTrailing"], st["splitRejectedForward"],
                 st["noForwardBar"], st["tiedAtMedian"]))
        print("  %-22s %9s %6s %9s %-20s %-20s %s"
              % ("LEG", "n", "days", "edge pp", "95% CI (day-clust)",
                 "Bonferroni CI", "VERDICT vs shipped"))
        print("  " + "-" * 118)
        for leg in LEGS + ["composite_as_audited", "composite_lowvol_only", "composite_all3"]:
            m = r["legs"].get(leg)
            shipped = SHIPPED_AT_AUDIT.get(h, {}).get(leg)
            if m is None:
                print("  %-22s %9s" % (leg, "-"))
                continue
            ci = ("[%+.2f, %+.2f]" % (m["ciLow"], m["ciHigh"])
                  if m["ciLow"] is not None else "(too few days)")
            bci = ("[%+.2f, %+.2f]%s" % (m["ciLowBonferroni"], m["ciHighBonferroni"],
                                         " *" if m["survivesBonferroni"] else "")
                   if m["ciLowBonferroni"] is not None else "-")
            print("  %-22s %9d %6d %+8.2f %-20s %-20s %s"
                  % (leg, m["n"], m["distinctDays"], m["edgePP"], ci, bci,
                     verdict(m, shipped)))
    print("\n  * = also survives Bonferroni over the %d leg x horizon tests this "
          "script runs at once." % FAMILY_TESTS)


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--universe", choices=["all", "active"], default="all",
                    help="all = every stock ever tracked (survivorship-clean); "
                         "active = the live endpoint's narrower universe")
    ap.add_argument("--no-forward-guard", action="store_true",
                    help="do NOT reject forward windows containing a split-sized "
                         "move — run it to see how much the guard is worth")
    ap.add_argument("--horizons", default=",".join(HORIZONS))
    ap.add_argument("--out", default="", help="write the derivation JSON here")
    args = ap.parse_args()

    horizons = [h.strip() for h in args.horizons.split(",") if h.strip()]
    for h in horizons:
        if h not in HORIZON_SESSIONS:
            raise SystemExit("unknown horizon %r (want %s)" % (h, HORIZONS))

    print(__doc__.split("Usage\n-----")[0].rstrip())
    print("\n" + "=" * 96)
    print("RUN CONFIGURATION")
    print("=" * 96)
    print("  database          : %s (mode=ro)" % args.db)
    print("  universe          : %s" % args.universe)
    print("  forward guard     : %s" % ("OFF" if args.no_forward_guard else "ON"))
    print("  bootstrap         : %d resamples of FORMATION DAYS, seed %d"
          % (BOOTSTRAP, SEED))
    print("  cross-section floor: %d symbols" % MIN_UNIVERSE)

    series = load(args.db, args.universe)
    live = sum(1 for s in series if s.active)
    print("  symbols loaded    : %d  (active %d, delisted/inactive %d)"
          % (len(series), live, len(series) - live))

    results = [measure(series, h, forward_guard=not args.no_forward_guard)
               for h in horizons]
    report("MEASURED EDGE PER LEG — universe=%s, forward guard %s"
           % (args.universe, "OFF" if args.no_forward_guard else "ON"), results)

    payload = {
        "universe": args.universe,
        "forwardGuard": not args.no_forward_guard,
        "bootstrap": BOOTSTRAP,
        "seed": SEED,
        "minUniverse": MIN_UNIVERSE,
        "symbolsLoaded": len(series),
        "symbolsActive": live,
        "script": "tools/xsfactor_edge.py",
        "horizons": {r["horizon"]: r for r in results},
    }
    if args.out:
        with open(args.out, "w") as f:
            json.dump(payload, f, indent=2, sort_keys=True)
            f.write("\n")
        print("\nwrote %s" % args.out)
    return 0


if __name__ == "__main__":
    sys.exit(main())

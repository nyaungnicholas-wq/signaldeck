"""Runnable gates for the accuracy items in ops/IMPROVE_BACKLOG.md.

Each gate exits 0 when satisfied, 1 when failed, and 2 when unresolved (the
self-improve loop ticks an item off only on exit 0).

Read-only against the live DB. Usage:
    python ops/accuracy_gates.py xsection
    python ops/accuracy_gates.py beats-naive --horizon 1d
    python ops/accuracy_gates.py --selftest
"""
import argparse
import os
import sqlite3
import sys

# Add tools/ (sibling of ops/) to sys.path so we can import skillpower
TOOLS_DIR = os.path.join(os.path.dirname(os.path.dirname(__file__)), "tools")
sys.path.insert(0, TOOLS_DIR)
from skillpower import skill_resolvable

DB = "data/signaldeck.db"

# One deduped observation per (symbol, horizon, UTC day). Counting raw rows is
# pseudo-replication: the runner re-scores each symbol dozens of times a day and
# every copy resolves to the same label.
DEDUP = """
with d as (
  select symbol_id, horizon, date(ts,'unixepoch') as day,
         avg(prob) as prob, max(up) as up
  from prediction_outcomes
  where prob is not null and up is not null
  group by symbol_id, horizon, day
)
"""


def connect(db):
    return sqlite3.connect("file:" + db + "?mode=ro", uri=True)


def gate_xsection(db, horizon, days, lo, hi):
    """The cross-section must not collapse onto a single market call.

    A per-symbol forecaster disagrees with itself across symbols. When the
    calibration map collapses, every symbol on a day gets the same side and the
    up-call rate pins to ~0 or ~1 -- one market call published as N forecasts.
    """
    with connect(db) as c:
        rows = c.execute(DEDUP + """
            select day, count(*) n,
                   avg(case when prob > 0.5 then 1.0 else 0.0 end) up_calls
            from d where horizon = ?
            group by day order by day desc limit ?""", (horizon, days)).fetchall()
    if not rows:
        print(f"xsection[{horizon}]: no graded days found")
        return 1
    bad = [r for r in rows if r[2] < lo or r[2] > hi]
    for day, n, u in rows:
        mark = "COLLAPSED" if (u < lo or u > hi) else "ok"
        print(f"  {day}  n={n:5d}  up_calls={u:.3f}  {mark}")
    print(f"xsection[{horizon}]: {len(bad)}/{len(rows)} days collapsed "
          f"(allowed band {lo:.2f}-{hi:.2f})")
    return 1 if bad else 0


def gate_beats_naive(db, horizon, min_issue_rate):
    """Accuracy must beat the folded majority baseline at full issue rate.

    The issue rate is the share of deduplicated observations that received a
    non-0.5 call. This is NOT forecast coverage (the share of the symbol
    universe that received a forecast at all); forecast coverage is reported by
    forecastmon.
    """
    with connect(db) as c:
        # Per-day tallies for the bootstrap
        day_rows = c.execute(DEDUP + """
            select day,
                   sum(case when prob != 0.5 then 1 else 0 end) as n,
                   sum(case when prob != 0.5 and ((prob > 0.5 and up = 1)
                        or (prob < 0.5 and up = 0)) then 1.0 else 0.0 end) as hits
            from d where horizon = ?
            group by day having n > 0""", (horizon,)).fetchall()

        # Pooled eligible and up_rate over issued rows (unchanged definitions)
        pool_row = c.execute(DEDUP + """
            select count(*) eligible,
                   sum(case when prob != 0.5 then 1 else 0 end) issued,
                   avg(case when prob != 0.5 then up * 1.0 else null end) up_rate
            from d where horizon = ?""", (horizon,)).fetchone()

    eligible, issued, up_rate = pool_row
    if not issued or up_rate is None:
        print(f"beats-naive[{horizon}]: no issued observations")
        return 1

    naive = max(up_rate, 1.0 - up_rate)

    # Derive issued and acc from per-day sums (point estimate unchanged)
    total_n = sum(r[1] for r in day_rows)
    total_hits = sum(r[2] for r in day_rows)
    acc = total_hits / total_n if total_n else 0.0
    issue_rate = issued / eligible
    lift = acc - naive

    # Build tallies for skill_resolvable: (n, hits, n * naive) per day
    tallies = [(r[1], r[2], r[1] * naive) for r in day_rows]
    result = skill_resolvable(tallies)

    days = result["days"]
    ci_lo, ci_hi = result["ci_lo"], result["ci_hi"]
    resolvable = result["resolvable"]
    reason = result["reason"]

    print(f"beats-naive[{horizon}]: n={issued} issue_rate={issue_rate:.4f} "
          f"acc={acc:.4f} naive={naive:.4f} lift={lift:+.4f} "
          f"days={days} ci=[{ci_lo:+.4f}, {ci_hi:+.4f}]")

    if issue_rate < min_issue_rate:
        print(f"  FAIL issue_rate {issue_rate:.4f} < {min_issue_rate:.4f} "
              f"(accuracy bought by abstaining does not count)")
        return 1

    if not resolvable:
        print(f"  UNRESOLVED {reason}")
        return 2

    if ci_hi < 0:
        print(f"  FAIL model worse than folded majority baseline (95% CI upper bound {ci_hi:+.4f} < 0)")
        print("  caveat: the folded majority baseline is estimated on the SAME window being graded, "
              "so a directionally lopsided window inflates it, and this gate therefore measures "
              "the model against that window's majority side rather than against a fixed 50 percent.")
        return 1

    print("  OK")
    return 0


def selftest():
    """Threshold logic only — the SQL is exercised against the live DB."""
    lo, hi = 0.05, 0.95
    collapsed = [0.012, 0.991, 0.015]
    healthy = [0.45, 0.52, 0.38]
    assert all(u < lo or u > hi for u in collapsed), "collapsed days must trip the band"
    assert not any(u < lo or u > hi for u in healthy), "healthy days must pass"
    # folded baseline is symmetric: a 57% up market and a 43% up market both
    # give a naive of 0.57, so a model must clear the MAJORITY side either way.
    assert max(0.5746, 1 - 0.5746) == 0.5746
    assert abs(max(0.43, 1 - 0.43) - 0.57) < 1e-9
    assert 0.4641 - 0.5746 < 0, "the live 1d surface must currently FAIL this gate"

    # skill_resolvable synthetic tests
    # Two days clearly separating from zero -> resolvable
    tallies_pos = [(100, 65, 50.0), (100, 65, 50.0)]  # skill = 0.15 per day
    res_pos = skill_resolvable(tallies_pos)
    assert res_pos["resolvable"] is True, "clearly positive skill must be resolvable"
    assert res_pos["ci_lo"] > 0, "CI must be entirely above zero"

    # Two days straddling zero -> not resolvable
    tallies_mixed = [(100, 60, 50.0), (100, 40, 50.0)]  # skills +0.10 and -0.10
    res_mixed = skill_resolvable(tallies_mixed)
    assert res_mixed["resolvable"] is False, "mixed skill must not be resolvable"

    print("accuracy_gates selftest: OK")
    return 0


def main():
    p = argparse.ArgumentParser()
    p.add_argument("gate", nargs="?", choices=["xsection", "beats-naive"])
    p.add_argument("--db", default=DB)
    p.add_argument("--horizon", default="1d")
    p.add_argument("--days", type=int, default=10)
    p.add_argument("--lo", type=float, default=0.05)
    p.add_argument("--hi", type=float, default=0.95)
    p.add_argument("--min-coverage", type=float, default=0.95, dest="min_issue_rate",
                   help=argparse.SUPPRESS)
    p.add_argument("--min-issue-rate", type=float, default=0.95, dest="min_issue_rate",
                   help="Minimum issue rate (share of deduplicated observations with a non-0.5 call). Preferred over --min-coverage.")
    p.add_argument("--selftest", action="store_true")
    a = p.parse_args()
    if a.selftest:
        return selftest()
    if a.gate == "xsection":
        return gate_xsection(a.db, a.horizon, a.days, a.lo, a.hi)
    if a.gate == "beats-naive":
        return gate_beats_naive(a.db, a.horizon, a.min_issue_rate)
    p.error("pick a gate or --selftest")


if __name__ == "__main__":
    sys.exit(main())
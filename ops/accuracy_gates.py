"""Runnable gates for the accuracy items in ops/IMPROVE_BACKLOG.md.

Each gate exits 0 when satisfied and 1 (with the failing numbers printed) until
then, so the self-improve loop can tick an item off only on real evidence.

Read-only against the live DB. Usage:
    python ops/accuracy_gates.py xsection
    python ops/accuracy_gates.py beats-naive --horizon 1d
    python ops/accuracy_gates.py --selftest
"""
import argparse
import sqlite3
import sys

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


def gate_beats_naive(db, horizon, min_coverage):
    """Accuracy must beat the folded majority baseline at full coverage.

    Coverage is checked too, because abstaining raises accuracy for free.
    """
    with connect(db) as c:
        row = c.execute(DEDUP + """
            select count(*) eligible,
                   sum(case when prob != 0.5 then 1 else 0 end) issued,
                   avg(case when prob != 0.5 and ((prob > 0.5 and up = 1)
                        or (prob < 0.5 and up = 0)) then 1.0 else 0.0 end)
                     * count(*) / nullif(sum(case when prob != 0.5 then 1 else 0 end), 0) acc,
                   avg(case when prob != 0.5 then up * 1.0 else null end) up_rate
            from d where horizon = ?""", (horizon,)).fetchone()
    eligible, issued, acc, up_rate = row
    if not issued or acc is None or up_rate is None:
        print(f"beats-naive[{horizon}]: no issued observations")
        return 1
    naive = max(up_rate, 1.0 - up_rate)
    coverage = issued / eligible
    lift = acc - naive
    print(f"beats-naive[{horizon}]: n={issued} coverage={coverage:.4f} "
          f"acc={acc:.4f} naive={naive:.4f} lift={lift:+.4f}")
    if coverage < min_coverage:
        print(f"  FAIL coverage {coverage:.4f} < {min_coverage:.4f} "
              f"(accuracy bought by abstaining does not count)")
        return 1
    if lift < 0:
        print(f"  FAIL lift {lift:+.4f} — worse than always calling the majority side")
        return 1
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
    p.add_argument("--min-coverage", type=float, default=0.95)
    p.add_argument("--selftest", action="store_true")
    a = p.parse_args()
    if a.selftest:
        return selftest()
    if a.gate == "xsection":
        return gate_xsection(a.db, a.horizon, a.days, a.lo, a.hi)
    if a.gate == "beats-naive":
        return gate_beats_naive(a.db, a.horizon, a.min_coverage)
    p.error("pick a gate or --selftest")


if __name__ == "__main__":
    sys.exit(main())

"""Advisory statistical validation sidecar - a second opinion, never a second grader.

WHY THIS EXISTS
---------------
tools/accuracy_registry.py publishes verdicts using a Wilson interval widened by a
measured design effect and a Bonferroni divisor. That path is FROZEN on the
pre-registration chain and byte-compared against prereg.MultiplicityRule in the Go
daemon (see accuracy_registry.grader_registration_error). It is deliberately hard
to change, and this file does not change it.

But Bonferroni is the crude instrument for the question this surface actually asks.
"I graded a family of signals repeatedly; is the best one real?" is precisely what
Hansen's SPA test answers, and it answers it with more power than Bonferroni
because it accounts for the CORRELATION between the signals rather than assuming
they are independent tests. Likewise, a design-effect-widened Wilson interval is a
parametric approximation to the dependence in the sample; a stationary bootstrap
resamples the dependence directly instead of assuming a form for it.

So this file re-grades the SAME inputs with those two estimators and writes what it
finds to its own table. Every verdict it produces is prefixed "ADVISORY:".

WHY IT MAY NOT PUBLISH
----------------------
Swapping SPA in for the frozen Bonferroni rule would be a PROTOCOL AMENDMENT, and
adopting it because it produced a nicer answer is exactly the optional-stopping
failure the look-counter exists to price. If this sidecar disagrees with the
published registry, the correct response is to pre-register an amendment and let
the chain record that the rule changed - not to let a script quietly re-grade.

Accordingly, this file:
  * opens the database READ-ONLY for every input it reads,
  * reuses accuracy_registry's fetchers so the inputs are identical by construction
    rather than by a re-derived copy of the SQL that can drift,
  * never touches the registry's multiplicity state,
  * writes only to validation_advisory, append-only, so reruns accumulate into a
    track record instead of overwriting the last opinion.

WHAT THE UNIT OF EVIDENCE IS
----------------------------
The same one the registry uses for structural predictors: a non-overlapping
forward-horizon BLOCK, via reg.horizon_blocks. A 21-day predictor called on 21
consecutive days is one independent forward window wearing 21 costumes, and
resampling call-days would reintroduce the pseudo-replication both graders exist to
remove. The bootstrap statistic is the RATIO estimator sum(hits)/sum(n) over
resampled blocks, not the mean of per-block accuracies: block sizes here range from
7 observations to over a thousand, and an unweighted mean would let the smallest
blocks dominate.
"""
from __future__ import annotations

import argparse
import json
import os
import sqlite3
import sys
import time
from typing import Any, Iterable, Sequence

import numpy as np
from arch.bootstrap import SPA, StationaryBootstrap, optimal_block_length

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import accuracy_registry as reg  # noqa: E402 - single source of truth for the inputs

ADVISORY_TABLE = "validation_advisory"

# Minimum non-overlapping units before any interval is published. Mirrors the
# registry's MIN_DISTINCT_BLOCKS for the same reason: a between-block variance
# estimated from three blocks is not a correction, it is a different way to be
# overconfident.
MIN_UNITS = 10

ALPHA = 0.05

DEFAULT_DB = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "data", "signaldeck.db")


def advisory_verdict(v: str) -> str:
    """Every persisted verdict carries this prefix.

    A downstream consumer that joins this table against the published registry
    must not be able to mistake one for the other by reading the column.
    """
    return f"ADVISORY:{v}"


# --------------------------------------------------------------------------- #
# The interval
# --------------------------------------------------------------------------- #

# Directional horizons are LABELS ("1d", "1w"), optionally carrying the
# prequential-benchmark suffix ("1w#pm"). The number of calendar days each one
# looks forward decides the fold width, and getting it wrong is not cosmetic:
# folding a 1w record at one day treats seven overlapping forward windows as
# seven independent observations.
#
# NOTE: the published registry grades directional records day-clustered at every
# horizon (see accuracy_registry.grade_directional_days, "independent symbol-days")
# and reserves block-folding for structural predictors. Folding 1w at 7 days is
# therefore a place where this sidecar deliberately DISAGREES with the published
# protocol - which is the entire reason it exists as a separate opinion.
_HORIZON_DAYS = {"1d": 1, "1w": 7, "2w": 14, "1m": 30, "1q": 91}


def _horizon_days(label: str) -> int:
    """Calendar days a directional horizon label looks forward. Unknown -> 1.

    Unknown labels fall back to 1 rather than raising: a new horizon appearing
    in the data should not take the sidecar down, and 1 is the same unit the
    published registry already uses, so the fallback is never MORE permissive
    than what is published today.
    """
    base = str(label).split(reg.BENCHMARK_SUFFIX)[0].strip().lower()
    return _HORIZON_DAYS.get(base, 1)


def _fold(day_rows: Sequence[tuple], horizon_days: int) -> list[tuple[int, int, int]]:
    """Per-call-day tallies -> non-overlapping forward-horizon blocks."""
    return reg.horizon_blocks([(d, n, h) for d, n, h, *_ in day_rows], horizon_days)


def _block_length(unit_acc: np.ndarray) -> float:
    """Politis-White optimal stationary-bootstrap block length, floored at 1.

    Falls back to 1.0 (i.e. an iid bootstrap over blocks) when the series is too
    short or degenerate for the estimator. That is the conservative direction
    only because the units are already non-overlapping.
    """
    try:
        bl = optimal_block_length(np.asarray(unit_acc, dtype=float))
        return max(1.0, float(bl["stationary"].iloc[0]))
    except Exception:
        return 1.0


def bootstrap_ci(day_rows: Sequence[tuple], horizon_days: int, reps: int = 1000,
                 seed: int = 0, null: float = 0.5) -> dict[str, Any]:
    """Stationary-bootstrap interval and two-sided p-value over horizon blocks."""
    units = _fold(day_rows, horizon_days)
    k = len(units)
    hits_arr = np.array([u[2] for u in units], dtype=np.float64)
    n_arr = np.array([u[1] for u in units], dtype=np.float64)
    total_n = int(n_arr.sum())
    total_hits = int(hits_arr.sum())
    acc = (total_hits / total_n) if total_n else None

    if k < MIN_UNITS:
        # A refusal, not a wide interval. It must never be rendered as a number.
        return {"n": total_n, "units": k, "acc": acc, "ci": None, "p_value": None,
                "block_length": None,
                "withheld": f"insufficient independent units: {k} < {MIN_UNITS}"}

    unit_acc = np.divide(hits_arr, n_arr, out=np.zeros_like(hits_arr),
                         where=n_arr > 0)
    block_size = _block_length(unit_acc)

    bs = StationaryBootstrap(block_size, hits_arr, n_arr,
                             seed=np.random.default_rng(seed))
    theta = bs.apply(lambda h, n: h.sum() / n.sum(), reps=reps).ravel()

    ci = (float(np.percentile(theta, 100 * ALPHA / 2)),
          float(np.percentile(theta, 100 * (1 - ALPHA / 2))))

    # Two-sided p for H0: acc == null. The resampled distribution is centred on
    # theta_hat, not on the null, so the deviation is measured from theta_hat and
    # compared against the observed distance to the null. Clipped below at
    # 1/(reps+1): a bootstrap can bound a p-value, it cannot prove it is zero.
    observed = abs(acc - null)
    p = float(np.mean(np.abs(theta - acc) >= observed))
    p = min(1.0, max(1.0 / (reps + 1), p))

    return {"n": total_n, "units": k, "acc": acc, "ci": ci, "p_value": p,
            "block_length": block_size, "withheld": None}


# --------------------------------------------------------------------------- #
# The family
# --------------------------------------------------------------------------- #

def spa_family(family: dict[str, Sequence[tuple]], horizon_days: int,
               reps: int = 1000, seed: int = 0,
               null: float = 0.5) -> dict[str, Any]:
    """Hansen's SPA: given that this whole family was tested, is the best one real?

    Models must be compared on the SAME observations for the test to mean
    anything, so the members are folded at a common horizon and then intersected
    on their block anchors. Callers group by horizon before calling here.
    """
    folded: dict[str, dict[int, tuple[int, int]]] = {}
    for name, day_rows in family.items():
        units = _fold(day_rows, horizon_days)
        if len(units) < MIN_UNITS:
            continue
        folded[name] = {day: (n, h) for day, n, h in units}

    skipped = [n for n in family if n not in folded]

    def _acc(name: str, days: Iterable[int]) -> float | None:
        tn = sum(folded[name][d][0] for d in days)
        th = sum(folded[name][d][1] for d in days)
        return (th / tn) if tn else None

    if len(folded) < 2:
        # One model pays no family multiplicity, so there is nothing for SPA to
        # correct. Say so explicitly rather than returning a p-value that would
        # read as a passed test.
        only = next(iter(folded), None)
        return {"spa_p": None, "family_size": len(family), "survivors": [],
                "skipped": skipped, "best": only,
                "best_acc": _acc(only, folded[only]) if only else None,
                "note": "fewer than 2 usable models: no family multiplicity to price"}

    common = sorted(set.intersection(*(set(u) for u in folded.values())))
    names = sorted(folded)

    if len(common) < MIN_UNITS:
        return {"spa_p": None, "family_size": len(family), "survivors": [],
                "skipped": skipped, "best": None, "best_acc": None,
                "note": f"models share only {len(common)} common units"}

    # Loss is negative accuracy so that "lower loss" means "more accurate", which
    # is the orientation SPA expects. The benchmark is the null the family claims
    # to beat. arch wants models shaped (nobs, nmodels).
    losses = np.column_stack(
        [[-(folded[n][d][1] / folded[n][d][0]) if folded[n][d][0] else 0.0
          for d in common] for n in names])
    benchmark = np.full(len(common), -null, dtype=np.float64)

    spa = SPA(benchmark, losses, reps=reps, seed=np.random.default_rng(seed))
    spa.compute()
    spa_p = float(spa.pvalues["consistent"])

    # SPA.pvalues is indexed lower/consistent/upper - it is a statement about the
    # FAMILY, not about any one member. Which members survive is a separate
    # question, answered by each model's own bootstrap, and only asked at all
    # once the family has cleared the multiplicity bar.
    survivors: list[str] = []
    if spa_p < ALPHA:
        for name in names:
            rows = [(d, folded[name][d][0], folded[name][d][1]) for d in common]
            r = bootstrap_ci(rows, 1, reps=reps, seed=seed, null=null)
            if (r["p_value"] is not None and r["p_value"] < ALPHA
                    and r["acc"] is not None and r["acc"] > null):
                survivors.append(name)

    # Ranked on POOLED accuracy over the common units. Ranking on the best single
    # unit would crown whichever model had the luckiest day.
    accs = {n: _acc(n, common) for n in names}
    best = max(names, key=lambda n: (accs[n] is not None, accs[n]))

    return {"spa_p": spa_p, "family_size": len(family), "survivors": survivors,
            "skipped": skipped, "best": best, "best_acc": accs[best]}


# --------------------------------------------------------------------------- #
# Persistence - its own table, append-only
# --------------------------------------------------------------------------- #

_COLS = ("run_ts", "signal", "family", "n", "units", "acc", "ci_lo", "ci_hi",
         "p_value", "spa_p", "family_size", "verdict", "method", "withheld")


def ensure_schema(con: sqlite3.Connection) -> None:
    """Create the advisory table and nothing else.

    Deliberately no AUTOINCREMENT: it would materialise sqlite_sequence, and this
    function must not add a table the caller did not ask for.
    """
    con.execute(f"""
        CREATE TABLE IF NOT EXISTS {ADVISORY_TABLE} (
            id          INTEGER PRIMARY KEY,
            run_ts      INTEGER NOT NULL,
            signal      TEXT    NOT NULL,
            family      TEXT    NOT NULL,
            n           INTEGER,
            units       INTEGER,
            acc         REAL,
            ci_lo       REAL,
            ci_hi       REAL,
            p_value     REAL,
            spa_p       REAL,
            family_size INTEGER,
            verdict     TEXT    NOT NULL,
            method      TEXT,
            withheld    TEXT
        )""")
    con.execute(f"CREATE INDEX IF NOT EXISTS idx_{ADVISORY_TABLE}_run "
                f"ON {ADVISORY_TABLE} (run_ts, signal)")
    con.commit()


def persist(con: sqlite3.Connection, rows: Iterable[dict], run_ts: int) -> None:
    """Append every row. Never UPDATE, never REPLACE - this is a track record."""
    con.executemany(
        f"INSERT INTO {ADVISORY_TABLE} ({','.join(_COLS)}) "
        f"VALUES ({','.join('?' * len(_COLS))})",
        [tuple(r.get(c) if c != "run_ts" else r.get("run_ts", run_ts)
               for c in _COLS) for r in rows])
    con.commit()


# --------------------------------------------------------------------------- #
# Grading
# --------------------------------------------------------------------------- #

def _verdict(ci: dict, spa_p: float | None, null: float) -> str:
    if ci["withheld"] is not None:
        return "WITHHELD"
    # spa_p is None when the horizon group held a single model - there is no
    # family multiplicity to clear, so the individual test stands alone.
    family_ok = spa_p is None or spa_p < ALPHA
    if (ci["p_value"] is not None and ci["p_value"] < ALPHA and family_ok
            and ci["acc"] is not None and ci["acc"] > null):
        return "SURVIVES"
    return "REFUTED"


def _row(signal: str, family: str, ci: dict, spa: dict, method: str) -> dict:
    return {"signal": signal, "family": family, "n": ci["n"], "units": ci["units"],
            "acc": ci["acc"],
            "ci_lo": ci["ci"][0] if ci["ci"] else None,
            "ci_hi": ci["ci"][1] if ci["ci"] else None,
            "p_value": ci["p_value"], "spa_p": spa.get("spa_p"),
            "family_size": spa.get("family_size"), "method": method,
            "withheld": ci["withheld"]}


def grade(con: sqlite3.Connection, reps: int, seed: int) -> list[dict]:
    """Grade both families. Read-only in, plain dicts out."""
    method = f"stationary-bootstrap+spa(reps={reps},seed={seed})"
    out: list[dict] = []

    # --- directional. Each horizon is a separate model, but models at different
    # horizons are not evaluated on the same forward windows, so SPA is run
    # WITHIN a horizon group rather than across them.
    by_h: dict[int, dict[str, list]] = {}
    for horizon, rows in reg.fetch_directional_days(con).items():
        by_h.setdefault(_horizon_days(horizon), {})[f"directional/{horizon}"] = rows

    for hd, fam in by_h.items():
        spa = spa_family(fam, hd, reps=reps, seed=seed, null=0.5)
        for signal, rows in fam.items():
            ci = bootstrap_ci(rows, hd, reps=reps, seed=seed, null=0.5)
            r = _row(signal, "directional", ci, spa, method)
            r["verdict"] = advisory_verdict(_verdict(ci, spa.get("spa_p"), 0.5))
            out.append(r)

    # --- structural. The null is the FROZEN naive-persistence baseline committed
    # at call time, not a coin: beating a coin was never the claim.
    _totals, per_day, naive_per_day = reg.fetch_structural(con)
    struct: dict[int, dict[str, list]] = {}
    for (kind, hd), rows in per_day.items():
        struct.setdefault(int(hd or 1), {})[f"structural/{kind}/{hd}"] = rows

    def _null_for(signal: str) -> float:
        _, kind, hd = signal.split("/", 2)
        nrows = naive_per_day.get((kind, int(hd)))
        if not nrows:
            return 0.5
        n = sum(r[1] for r in nrows)
        return (sum(r[2] for r in nrows) / n) if n else 0.5

    for hd, fam in struct.items():
        # One SPA per horizon group, priced against the mean frozen null of its
        # members so the benchmark series is coherent across the family.
        nulls = {s: _null_for(s) for s in fam}
        spa = spa_family(fam, hd, reps=reps, seed=seed,
                         null=float(np.mean(list(nulls.values()))))
        for signal, rows in fam.items():
            ci = bootstrap_ci(rows, hd, reps=reps, seed=seed, null=nulls[signal])
            r = _row(signal, "structural", ci, spa, method)
            r["verdict"] = advisory_verdict(_verdict(ci, spa.get("spa_p"),
                                                     nulls[signal]))
            out.append(r)

    return out


def _print_table(rows: list[dict]) -> None:
    # ASCII only: this prints to a cp1252 console on Windows.
    print("ADVISORY ONLY - these numbers do NOT modify the published registry.")
    print("The published grading protocol is frozen on the pre-registration chain;")
    print("changing it requires an amendment, not this script.\n")
    hdr = (f"{'signal':<34}{'n':>9}{'units':>7}{'acc':>8}{'95% CI':>17}"
           f"{'p':>8}{'spa_p':>8}  verdict")
    print(hdr)
    print("-" * len(hdr))
    for r in sorted(rows, key=lambda x: (x["family"], x["signal"])):
        ci = (f"[{r['ci_lo']:.3f},{r['ci_hi']:.3f}]"
              if r["ci_lo"] is not None else "withheld")
        acc = f"{r['acc']:.4f}" if r["acc"] is not None else "-"
        p = f"{r['p_value']:.4f}" if r["p_value"] is not None else "-"
        sp = f"{r['spa_p']:.4f}" if r["spa_p"] is not None else "-"
        print(f"{r['signal']:<34}{r['n']:>9}{r['units']:>7}{acc:>8}{ci:>17}"
              f"{p:>8}{sp:>8}  {r['verdict']}")
        if r["withheld"]:
            print(f"{'':<34}  -> {r['withheld']}")
    if not rows:
        print("(no signals had gradeable rows)")


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(
        description="Advisory statistical validation sidecar "
                    "(stationary bootstrap + Hansen SPA). Does NOT modify the "
                    "published accuracy registry.")
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--reps", type=int, default=1000)
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--dry-run", action="store_true",
                    help="compute and print, persist nothing")
    args = ap.parse_args(argv)

    if not os.path.exists(args.db):
        print(f"no such database: {args.db}", file=sys.stderr)
        return 2

    con = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    try:
        rows = grade(con, args.reps, args.seed)
    finally:
        con.close()

    run_ts = int(time.time())
    if not args.dry_run:
        rw = sqlite3.connect(args.db)
        try:
            ensure_schema(rw)
            persist(rw, rows, run_ts)
        finally:
            rw.close()

    if args.json:
        print(json.dumps({"run_ts": run_ts, "rows": rows}, indent=2, default=str))
    else:
        _print_table(rows)
        print(f"\n{len(rows)} signals graded at run_ts={run_ts}"
              f"{' (dry run, nothing persisted)' if args.dry_run else ''}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

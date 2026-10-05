#!/usr/bin/env python3
"""Compare three multiple-testing screens on SignalDeck's own 48 research rules.

Reconstructs each rule's weekly return series from research_weeks (the same corpus
the Go loop grades against), then runs:
  - Bonferroni  (what internal/pipeline/researchloop.go does today, divisor 384)
  - Hansen SPA  (arch.bootstrap.SPA)
  - Romano-Wolf StepM (arch.bootstrap.StepM)

Writes tools/spa_ledger_result.json. Read-only against data/signaldeck.db.
"""

import json
import os
import re
import sqlite3
import sys
import time

import numpy as np
import pandas as pd
import statsmodels.api as sm
from arch.bootstrap import SPA, StepM

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
DB = os.path.join(ROOT, "data", "signaldeck.db")
OUT = os.path.join(HERE, "spa_ledger_result.json")

REPS = 5000
SEED = 20260803
DIVISOR = 384  # live value: grid 48 x 8 searches (research_loop_runs)

# "<feature> [(week-pct)] >=|<= <float>"
COND = re.compile(r"^(?P<feat>\w+)\s*(?P<pct>\(week-pct\))?\s*(?P<op>>=|<=)\s*(?P<thr>-?[\d.]+)$")


def parse_rule(descr):
    """-> (direction, [(feature, is_week_pct, op, threshold), ...])"""
    body = descr.split("weekly rule:", 1)[1].strip()
    body = body.replace("(week-trial graded)", "").strip()
    direction, _, rest = body.partition(" when ")
    direction = direction.strip()
    assert direction in ("follow_pressure", "inverse_pressure"), direction
    conds = []
    for part in rest.split(" and "):
        m = COND.match(part.strip())
        assert m, f"unparsed condition {part!r} in {descr!r}"
        conds.append((m["feat"], bool(m["pct"]), m["op"], float(m["thr"])))
    return direction, conds


def build_panel():
    """-> (R, meta) where R is a weeks x 48 DataFrame of weekly returns."""
    con = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    rules = con.execute(
        "select id, descr, weeks, wilson_lower from research_loop_hypotheses order by id"
    ).fetchall()
    rows = con.execute("select week, ts, vec, fwd_return from research_weeks").fetchall()
    con.close()

    weeks = np.fromiter((r[0] for r in rows), dtype=np.int64, count=len(rows))
    ts = np.fromiter((r[1] for r in rows), dtype=np.int64, count=len(rows))
    fwd = np.fromiter((r[3] for r in rows), dtype=np.float64, count=len(rows))
    df = pd.DataFrame.from_records([json.loads(r[2]) for r in rows])

    df["_week"] = weeks
    df["_fwd"] = fwd
    # follow = trade with the pressure sign; inverse = exact negation
    df["_sign"] = np.sign(df["pressure_score"].to_numpy())

    order = np.sort(np.unique(weeks))
    idx = pd.Index(order, name="week")

    # week-pct features are ranked cross-sectionally within their own week
    pct_cache = {}

    def week_pct(feat):
        if feat not in pct_cache:
            pct_cache[feat] = df.groupby("_week")[feat].rank(pct=True)
        return pct_cache[feat]

    panel, meta = {}, []
    for rid, descr, led_weeks, led_wl in rules:
        direction, conds = parse_rule(descr)
        mask = pd.Series(True, index=df.index)
        for feat, is_pct, op, thr in conds:
            col = week_pct(feat) if is_pct else df[feat]
            mask &= (col >= thr) if op == ">=" else (col <= thr)

        sign = df["_sign"] if direction == "follow_pressure" else -df["_sign"]
        weekly = (sign * df["_fwd"])[mask].groupby(df["_week"][mask]).mean()
        fired = int(weekly.notna().sum())  # weeks in which at least one row fired
        # weeks with no firing row = flat, out of market
        series = weekly.reindex(idx).fillna(0.0)

        panel[rid] = series.to_numpy()
        meta.append({
            "id": rid, "descr": descr, "direction": direction,
            "weeks_fired": fired,
            "ledger_weeks": int(led_weeks), "ledger_wilson_lower": float(led_wl),
        })

    ids = [m["id"] for m in meta]
    R = pd.DataFrame({i: panel[i] for i in ids}, index=idx)
    R.attrs["ts_from"] = int(ts.min())
    R.attrs["ts_to"] = int(ts.max())
    R.attrs["n_observations"] = len(rows)
    return R, meta


def record(report, day):
    """Append tonight's SPA/StepM verdict to research_loop_spa. NON-GATING.

    Nothing in the Go loop reads this table, and nothing may: SPA controls FWER
    over ONE candidate set in ONE application and has no notion of prior
    searches, so it cannot replace the loop's grid x (1 + searches) divisor
    without silently dropping the temporal half of the correction. This exists
    to accumulate a track record of what a joint procedure WOULD have promoted,
    so the question can be decided on more than one day's snapshot.
    """
    con = sqlite3.connect(DB, timeout=30)
    try:
        con.execute("PRAGMA busy_timeout=30000")
        con.execute("""
            CREATE TABLE IF NOT EXISTS research_loop_spa (
                day                TEXT PRIMARY KEY,
                ran_at             INTEGER NOT NULL,
                n_weeks            INTEGER NOT NULL,
                n_rules            INTEGER NOT NULL,
                spa_p_lower        REAL    NOT NULL,
                spa_p_consistent   REAL    NOT NULL,
                spa_p_upper        REAL    NOT NULL,
                stepm_survivors    TEXT    NOT NULL,
                bonferroni_divisor INTEGER NOT NULL,
                bonferroni_alpha   REAL    NOT NULL,
                bonferroni_survivors TEXT  NOT NULL,
                reps               INTEGER NOT NULL,
                bootstrap          TEXT    NOT NULL
            )""")
        p, s, b = report["panel"], report["spa"], report["bonferroni"]
        con.execute("""
            INSERT INTO research_loop_spa VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
            ON CONFLICT(day) DO UPDATE SET
              ran_at=excluded.ran_at, n_weeks=excluded.n_weeks,
              n_rules=excluded.n_rules, spa_p_lower=excluded.spa_p_lower,
              spa_p_consistent=excluded.spa_p_consistent,
              spa_p_upper=excluded.spa_p_upper,
              stepm_survivors=excluded.stepm_survivors,
              bonferroni_divisor=excluded.bonferroni_divisor,
              bonferroni_alpha=excluded.bonferroni_alpha,
              bonferroni_survivors=excluded.bonferroni_survivors,
              reps=excluded.reps, bootstrap=excluded.bootstrap""", (
            day, int(time.time()), p["n_weeks"], p["n_rules"],
            s["pvalue_lower"], s["pvalue_consistent"], s["pvalue_upper"],
            json.dumps(report["stepm"]["survivors"]),
            b["divisor"], b["corrected_alpha"], json.dumps(b["survivors"]),
            s["reps"], s["bootstrap"]))
        con.commit()
    finally:
        con.close()


def main():
    R, meta = build_panel()
    ids = list(R.columns)
    T = len(R)

    # --- per-rule HAC stats: regress on a constant only, so index 0 is the only term ---
    maxlags = int(round(T ** (1 / 3)))
    for m in meta:
        y = R[m["id"]].to_numpy()
        fit = sm.OLS(y, np.ones((T, 1))).fit(cov_type="HAC", cov_kwds={"maxlags": maxlags})
        m["mean_weekly_return"] = float(y.mean())
        m["t_stat"] = float(fit.tvalues[0])
        m["p_value"] = float(fit.pvalues[0])

    alpha = 0.05 / DIVISOR
    bonf = [m["id"] for m in meta if m["p_value"] < alpha and m["mean_weekly_return"] > 0]

    # --- SPA / StepM: arch is LOSS-based (lower is better), so negate returns ---
    losses = -R
    bench = pd.Series(np.zeros(T), index=R.index)  # flat = zero loss

    spa = SPA(bench, losses, reps=REPS, bootstrap="stationary", seed=SEED)
    spa.compute()
    pv = spa.pvalues
    try:
        better = list(spa.better_models(pvalue=0.05))
    except Exception:
        better = []

    stepm = StepM(bench, losses, size=0.05, reps=REPS, bootstrap="stationary", seed=SEED)
    stepm.compute()
    superior = list(stepm.superior_models)

    report = {
        "panel": {"n_weeks": T, "n_rules": len(ids),
                  "ts_from": R.attrs["ts_from"], "ts_to": R.attrs["ts_to"],
                  "n_observations": R.attrs["n_observations"]},
        "rules": meta,
        "bonferroni": {"divisor": DIVISOR, "corrected_alpha": alpha, "survivors": bonf},
        "spa": {"pvalue_lower": float(pv["lower"]),
                "pvalue_consistent": float(pv["consistent"]),
                "pvalue_upper": float(pv["upper"]),
                "bootstrap": "stationary", "reps": REPS,
                "better_models": [str(x) for x in better]},
        "stepm": {"survivors": [str(x) for x in superior], "size": 0.05, "reps": REPS},
    }
    with open(OUT, "w") as fh:
        json.dump(report, fh, indent=2)

    top = sorted(meta, key=lambda m: -m["t_stat"])[:8]
    print(f"panel: {T} weeks x {len(ids)} rules, {R.attrs['n_observations']} observations")
    print(f"{'rule':<14}{'mean/wk':>10}{'HAC t':>9}{'p':>10}{'fired':>7}  descr")
    for m in top:
        print(f"{m['id']:<14}{m['mean_weekly_return']:>10.5f}{m['t_stat']:>9.2f}"
              f"{m['p_value']:>10.4f}{m['weeks_fired']:>7}  {m['descr'][28:70]}")
    print(f"\nBonferroni  alpha={alpha:.2e} divisor={DIVISOR}: {len(bonf)} survivors {bonf}")
    print(f"SPA         p lower/consistent/upper = "
          f"{pv['lower']:.4f} / {pv['consistent']:.4f} / {pv['upper']:.4f}")
    print(f"StepM       size=0.05: {len(superior)} survivors {superior}")

    if "--write" in sys.argv:
        day = time.strftime("%Y-%m-%d", time.gmtime())
        record(report, day)
        print(f"recorded to research_loop_spa for {day} (NON-GATING diagnostic)")


if __name__ == "__main__":
    main()

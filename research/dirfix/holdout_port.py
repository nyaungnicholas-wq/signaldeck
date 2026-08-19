"""Grade the Sharpe-optimised book ONCE on the sealed holdout.

Config is chosen on the SEARCH period only. Reports Newey-West t (overlapping
holds autocorrelate the daily series), worst single day (this book is heavily
short and AMC 2021-01-27 was +301% in the tradeable universe), and deflates for
the number of risk-control combinations tried.
"""
import json, sys, hashlib, numpy as np, pandas as pd, lab, port, search

TRIALS = int(sys.argv[1]) if len(sys.argv) > 1 else 96
best = json.load(open("best_config.json"))
meta = json.load(open("HOLDOUT.json"))
p = pd.read_parquet("panel.parquet"); hold = p[p.day >= meta["split"]]
h = hashlib.sha256(pd.util.hash_pandas_object(
    hold[["symbol_id", "day", "close"]], index=False).values.tobytes()).hexdigest()
print("holdout seal %s\n" % ("INTACT" if h == meta["sha256"] else "!!! ALTERED !!!"))

rets = pd.read_parquet("rets_daily.parquet")
pred = pd.read_parquet("preds_%d_%s_%s.parquet" % (best["h"], best["target"], best["model"]))
split = pd.Timestamp(meta["split"])
kw = dict(horizon=best["h"], k=best["k"], weighting=best["weighting"],
          beta_neutral=best["beta_neutral"],
          vol_target=(best["vol_target"] or None),
          cost_bps=10.0, borrow_bps_yr=300.0, max_weight=0.10)
print("config: %s\n" % json.dumps(best))

for era, sub in (("SEARCH", pred[pred.day < split]), ("HOLDOUT", pred[pred.day >= split])):
    r = port.simulate(sub[["day", "symbol_id", "prob"]], rets, **kw)
    d = r["daily"]
    worst = d.nsmallest(5)
    print("%-8s  Sharpe %5.2f   ann %+6.2f%%   vol %5.2f%%   maxDD %6.1f%%   Calmar %5.2f"
          % (era, r["sharpe"], 100*r["ann_ret"], 100*r["ann_vol"], 100*r["max_dd"], r["calmar"]))
    print("          t_naive %5.2f  t_NW %5.2f   beta %+.3f   turnover %5.1fx   days %d"
          % (r["t_naive"], r["t_nw"], r["beta_to_market"], r["turnover_ann"], r["n_days"]))
    print("          worst 5 days: %s" % ", ".join("%.2f%%" % (100*v) for v in worst))
    if era == "HOLDOUT":
        se = r["ann_ret"] / r["sharpe"] / np.sqrt(252 / 1) if r["sharpe"] else 0
        bar = search.deflated_threshold(abs(r["sharpe"]) / max(abs(r["t_nw"]), 1e-9), TRIALS)
        print("          deflated Sharpe bar for %d trials: %.2f  ->  %s"
              % (TRIALS, bar, "SURVIVES" if r["sharpe"] > bar else "does NOT survive"))
    print()

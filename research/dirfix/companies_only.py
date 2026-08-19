"""Does the edge survive when ETFs and leveraged products are removed?

Over half the short book was leveraged inverse ETFs (TSLZ, TSDD, MSTZ, SOXS).
Shorting those harvests daily-rebalance decay, not a directional forecast, and
borrow on them runs 20-100%/yr rather than the 3% charged. If the alpha is real
stock selection it survives here; if it was decay, it collapses.
"""
import sqlite3, numpy as np, pandas as pd, lab, port

con = sqlite3.connect("file:../../data/signaldeck.db?mode=ro", uri=True)
fund = {x for x, in con.execute("select distinct symbol_id from fundamentals")}
con.close()

rets = pd.read_parquet("rets_daily.parquet")
split = pd.Timestamp("2025-03-01")
kw = dict(horizon=10, k=10, weighting="conviction", beta_neutral=True,
          vol_target=None, cost_bps=10.0, borrow_bps_yr=300.0, max_weight=0.10)

p = pd.read_parquet("preds_10_extremes10_ens_lh.parquet")
print("%-34s %7s %9s %8s %8s %7s" % ("book", "Sharpe", "ann", "vol", "maxDD", "days"))
for label, sub in (("ALL instruments (as reported)", p),
                   ("operating COMPANIES only", p[p.symbol_id.isin(fund)])):
    for era, q in (("search", sub[sub.day < split]), ("holdout", sub[sub.day >= split])):
        r = port.simulate(q[["day", "symbol_id", "prob"]], rets, **kw)
        print("%-34s %7.2f %8.1f%% %7.1f%% %7.1f%% %7d"
              % (f"{label} [{era}]", r["sharpe"], 100*r["ann_ret"],
                 100*r["ann_vol"], 100*r["max_dd"], r["n_days"]))

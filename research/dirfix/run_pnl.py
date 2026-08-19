"""Does the edge survive as MONEY? Long-short book, realistic costs.

Accuracy and profit are different objectives: a 60% predictor loses if its 40%
wrong trades are bigger than its right ones. This measures the thing that
matters. Costs: 5bps/way is a realistic liquid-US-equity assumption for a small
book, 10bps a conservative one; 30bps/yr borrow = easy-to-borrow names.
"""
import numpy as np, pandas as pd, lab, search, pnl

px = lab.load("panel.parquet")
close = px["close"]
split = pd.Timestamp("2025-03-01")
Xfull = pd.read_parquet("features_v2.parquet")
m = lab.universe_mask(px).stack(); m.index.names = ["day", "symbol_id"]
Xfull = Xfull.loc[Xfull.index.intersection(m[m].index)]

CANDS = [dict(h=42, t="extremes20", model="logit", f="v2_all"),
         dict(h=42, t="extremes20", model="logit", f="v2_no_market"),
         dict(h=21, t="extremes10", model="ens3",  f="v2_all"),
         dict(h=5,  t="extremes20", model="ens_lh", f="v2_no_market")]

for c in CANDS:
    h = c["h"]
    X = Xfull[search.FEATURE_SETS[c["f"][3:]](Xfull.columns.tolist())]
    y_tr = lab.make_labels(px, h, c["t"]).loc[X.index]
    y_ev = lab.make_labels(px, h, "relative").loc[X.index]
    pred = lab.walkforward(X, y_tr, horizon=h, model_fn=search.MODELS[c["model"]],
                           retrain_every=126, min_train_days=252, y_eval=y_ev)
    fwd = (close.shift(-h) / close - 1.0).stack()
    fwd.index.names = ["day", "symbol_id"]
    print("\n%s  h=%dd  %s  %s" % ("="*78, h, c["t"], c["model"]))
    for era, sub in (("SEARCH", pred[pred.day < split]), ("HOLDOUT", pred[pred.day >= split])):
        for k in (10, 25, 50):
            for cost in (5.0, 10.0):
                r = pnl.backtest(sub, fwd, horizon=h, k=k, cost_bps=cost, borrow_bps_yr=30.0)
                if r["n_days"] == 0: continue
                print("  %-7s k=%-3d cost=%2.0fbps | gross %+7.2f%%  NET %+7.2f%%/yr  "
                      "Sharpe %5.2f  maxDD %6.1f%%  hit %.3f  L %+.3f%% S %+.3f%%  tranches %d"
                      % (era, k, cost, 100*r["gross_ann"], 100*r["net_ann"], r["sharpe"],
                         100*r["max_dd"], r["hit_rate"], 100*r["long_ret"], 100*r["short_ret"],
                         r["n_tranches"]))

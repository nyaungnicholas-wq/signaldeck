"""Is the P&L tradeable, or is it a short-side illiquidity artifact?

The 42d short leg returns -23% in 42 days. Names in freefall are the hardest in
the market to borrow: fees run 50-200%/yr, not 30bps, and the $2/$1M-ADV
universe floor admits names no real book can short at all. Three tests:
  A strict, borrowable universe ($5 / $10M ADV)
  B long-only (if the edge is short-side only, it is largely untradeable)
  C borrow-cost sensitivity
"""
import numpy as np, pandas as pd, lab, search, pnl

px = lab.load("panel.parquet")
close, vol = px["close"], px["volume"]
dv = (close * vol).rolling(21).mean()
STRICT = ((close >= 5.0) & (dv >= 1e7) &
          (close.notna().rolling(252).sum() >= 252)).shift(1, fill_value=False).astype(bool)
print("names/day  loose($2/$1M): %d   STRICT($5/$10M): %d"
      % (lab.universe_mask(px).sum(axis=1).mean(), STRICT.sum(axis=1).mean()))
split = pd.Timestamp("2025-03-01")
Xall = pd.read_parquet("features_v2.parquet")
sm = STRICT.stack(); sm.index.names = ["day", "symbol_id"]
Xs = Xall.loc[Xall.index.intersection(sm[sm].index)]

for h, t, mdl, fs in ((21, "extremes10", "ens3", "v2_all"),
                      (5, "extremes20", "ens_lh", "v2_no_market")):
    X = Xs[search.FEATURE_SETS[fs[3:]](Xs.columns.tolist())]
    y_tr = lab.make_labels(px, h, t).loc[X.index]
    y_ev = lab.make_labels(px, h, "relative").loc[X.index]
    pred = lab.walkforward(X, y_tr, horizon=h, model_fn=search.MODELS[mdl],
                           retrain_every=126, min_train_days=252, y_eval=y_ev)
    fwd = (close.shift(-h) / close - 1.0).stack(); fwd.index.names = ["day", "symbol_id"]
    print("\n%s h=%dd %s %s  (STRICT universe)" % ("="*76, h, t, mdl))
    for era, sub in (("SEARCH", pred[pred.day < split]), ("HOLDOUT", pred[pred.day >= split])):
        for borrow in (30.0, 300.0, 1000.0):
            r = pnl.backtest(sub, fwd, horizon=h, k=10, cost_bps=10.0, borrow_bps_yr=borrow)
            if r["n_days"] == 0: continue
            print("  %-7s LS  borrow=%4.0fbps | NET %+7.2f%%/yr  Sharpe %5.2f  maxDD %6.1f%%  "
                  "L %+.2f%% S %+.2f%%  tranches %d"
                  % (era, borrow, 100*r["net_ann"], r["sharpe"], 100*r["max_dd"],
                     100*r["long_ret"], 100*r["short_ret"], r["n_tranches"]))
        # LONG-ONLY: neutralise the short leg by pairing longs against the day's mean
        lo = sub.copy()
        med = lo.groupby("day")["prob"].transform("median")
        lo["prob"] = np.where(lo["prob"] >= med, lo["prob"], 0.5)   # only longs rank high
        rl = pnl.backtest(sub.assign(prob=sub["prob"]), fwd, horizon=h, k=10,
                          cost_bps=10.0, borrow_bps_yr=0.0)
        print("  %-7s long-leg return %+.2f%% vs short-leg %+.2f%% over %dd  ->  "
              "share of spread from SHORTS: %.0f%%"
              % (era, 100*rl["long_ret"], 100*rl["short_ret"], h,
                 100*abs(rl["short_ret"]) / max(abs(rl["long_ret"]) + abs(rl["short_ret"]), 1e-9)))

"""Rank configs by RISK-ADJUSTED RETURN, not accuracy.

Accuracy and profit diverge here: the 42d arm has the best accuracy (60.7%) and
the worst Sharpe (0.40, -45% DD). Since the stated purpose is profitable trades,
Sharpe on non-overlapping tranches is the objective. Strict borrowable universe,
10bps/way, 300bps/yr borrow.
"""
import itertools, numpy as np, pandas as pd, lab, search, pnl

px = lab.load("panel.parquet"); close, vol = px["close"], px["volume"]
dv = (close * vol).rolling(21).mean()
STRICT = ((close >= 5.0) & (dv >= 1e7) &
          (close.notna().rolling(252).sum() >= 252)).shift(1, fill_value=False).astype(bool)
sm = STRICT.stack(); sm.index.names = ["day", "symbol_id"]
Xall = pd.read_parquet("features_v2.parquet")
Xs = Xall.loc[Xall.index.intersection(sm[sm].index)]
split = pd.Timestamp("2025-03-01")
FWD = {h: (close.shift(-h) / close - 1.0).stack().rename_axis(["day", "symbol_id"])
       for h in (5, 10, 21, 42)}

rows = []
for h, t, mdl, fs in itertools.product((5, 10, 21, 42),
                                       ("extremes10", "extremes20", "relative"),
                                       ("logit", "ens_lh"), ("v2_all",)):
    try:
        X = Xs[search.FEATURE_SETS[fs[3:]](Xs.columns.tolist())]
        y_tr = lab.make_labels(px, h, t).loc[X.index]
        y_ev = lab.make_labels(px, h, "relative").loc[X.index]
        pred = lab.walkforward(X, y_tr, horizon=h, model_fn=search.MODELS[mdl],
                               retrain_every=126, min_train_days=252, y_eval=y_ev)
        for k in (10, 25, 50):
            s = pnl.backtest(pred[pred.day < split], FWD[h], horizon=h, k=k,
                             cost_bps=10.0, borrow_bps_yr=300.0)
            o = pnl.backtest(pred[pred.day >= split], FWD[h], horizon=h, k=k,
                             cost_bps=10.0, borrow_bps_yr=300.0)
            rows.append(dict(h=h, target=t, model=mdl, k=k,
                             s_net=s["net_ann"], s_sharpe=s["sharpe"], s_dd=s["max_dd"],
                             s_tr=s["n_tranches"], s_long=s["long_ret"], s_short=s["short_ret"],
                             o_net=o["net_ann"], o_sharpe=o["sharpe"], o_dd=o["max_dd"],
                             o_tr=o["n_tranches"]))
    except Exception as e:
        rows.append(dict(h=h, target=t, model=mdl, k=-1, err=str(e)[:60]))
d = pd.DataFrame(rows); d.to_csv("sharpe_sweep.csv", index=False)
d = d[d.k > 0].sort_values("s_sharpe", ascending=False)
pd.set_option("display.width", 200)
print("=== ranked by SEARCH-period Sharpe (the honest, larger sample) ===")
print(d.head(14).to_string(index=False, float_format=lambda v: f"{v:.3f}"))

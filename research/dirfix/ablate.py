"""Ablate each risk control independently on the SEARCH period only.

Shipping a bundle of controls that works for unknown reasons is how a strategy
survives backtest and dies live. Each row here isolates one change.
Predictions are cached per arm so the ablation is cheap.
"""
import itertools, os, numpy as np, pandas as pd, lab, search, port

px = lab.load("panel.parquet"); close, vol = px["close"], px["volume"]
rets = pd.read_parquet("rets_daily.parquet")
dv = (close * vol).rolling(21).mean()
STRICT = ((close >= 5.0) & (dv >= 1e7) &
          (close.notna().rolling(252).sum() >= 252)).shift(1, fill_value=False).astype(bool)
sm = STRICT.stack(); sm.index.names = ["day", "symbol_id"]
Xall = pd.read_parquet("features_v2.parquet")
Xs = Xall.loc[Xall.index.intersection(sm[sm].index)]
SPLIT = pd.Timestamp("2025-03-01")

ARMS = [(42, "extremes10", "ens_lh"), (21, "extremes10", "ens_lh"), (10, "extremes10", "ens_lh")]

def predictions(h, t, mdl):
    f = f"preds_{h}_{t}_{mdl}.parquet"
    if os.path.exists(f):
        return pd.read_parquet(f)
    y_tr = lab.make_labels(px, h, t).loc[Xs.index]
    y_ev = lab.make_labels(px, h, "relative").loc[Xs.index]
    p = lab.walkforward(Xs, y_tr, horizon=h, model_fn=search.MODELS[mdl],
                        retrain_every=126, min_train_days=252, y_eval=y_ev)
    p.to_parquet(f); return p

rows = []
for h, t, mdl in ARMS:
    p = predictions(h, t, mdl)
    ps = p[p.day < SPLIT][["day", "symbol_id", "prob"]]
    # Isolate each control against a common baseline instead of a full cross
    # product: 96 cells was ~1s/entry-day x 1200 days x 96 and answers the same
    # question ("which control earns its keep") with 4x the compute.
    COMBOS = [("equal", False, None, 10),                 # baseline
              ("invvol", False, None, 10),                # +inverse-vol
              ("conviction", False, None, 10),            # +conviction
              ("conviction_invvol", False, None, 10),     # +both
              ("equal", True, None, 10),                  # +beta-neutral
              ("equal", False, 0.10, 10),                 # +vol target
              ("invvol", True, 0.10, 10),                 # stacked
              ("conviction_invvol", True, 0.10, 10),      # stacked
              ("invvol", True, 0.10, 25)]                 # wider book
    for w, bn, vt, k in COMBOS:
        print("  h=%d %-18s bn=%-5s vt=%-4s k=%d" % (h, w, bn, vt, k), flush=True)
        try:
            r = port.simulate(ps, rets, horizon=h, k=k, weighting=w,
                              beta_neutral=bn, vol_target=vt,
                              cost_bps=10.0, borrow_bps_yr=300.0, max_weight=0.10)
            d = r["daily"]
            rows.append(dict(h=h, k=k, weighting=w, beta_neutral=bn,
                             vol_target=vt or 0, sharpe=r["sharpe"], ann_ret=r["ann_ret"],
                             ann_vol=r["ann_vol"], max_dd=r["max_dd"], calmar=r["calmar"],
                             t_nw=r["t_nw"], beta=r["beta_to_market"],
                             worst_day=float(d.min()) if len(d) else 0.0,
                             turnover=r["turnover_ann"]))
        except Exception as e:
            rows.append(dict(h=h, k=k, weighting=w, beta_neutral=bn,
                             vol_target=vt or 0, sharpe=np.nan, err=str(e)[:70]))
d = pd.DataFrame(rows); d.to_csv("ablation.csv", index=False)
pd.set_option("display.width", 220)
ok = d[d.sharpe.notna()].sort_values("sharpe", ascending=False)
print("=== TOP 15 by SEARCH-period Sharpe ===")
print(ok.head(15).to_string(index=False, float_format=lambda v: f"{v:.3f}"))
print("\n=== marginal effect of each control (mean Sharpe) ===")
for col in ("weighting", "beta_neutral", "vol_target", "k", "h"):
    print(" ", col); print(ok.groupby(col)["sharpe"].agg(["mean", "max", "count"]).to_string())

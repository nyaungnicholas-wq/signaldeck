"""Cache walk-forward predictions per arm so the ablation is cheap to re-run."""
import os, pandas as pd, lab, search
px = lab.load("panel.parquet"); close, vol = px["close"], px["volume"]
dv = (close * vol).rolling(21).mean()
STRICT = ((close >= 5.0) & (dv >= 1e7) &
          (close.notna().rolling(252).sum() >= 252)).shift(1, fill_value=False).astype(bool)
sm = STRICT.stack(); sm.index.names = ["day", "symbol_id"]
X = pd.read_parquet("features_v2.parquet")
X = X.loc[X.index.intersection(sm[sm].index)]
print("strict universe X:", X.shape)
for h, t, mdl in ((42, "extremes10", "ens_lh"), (21, "extremes10", "ens_lh"),
                  (10, "extremes10", "ens_lh")):
    f = f"preds_{h}_{t}_{mdl}.parquet"
    if os.path.exists(f):
        print("cached", f); continue
    y_tr = lab.make_labels(px, h, t).loc[X.index]
    y_ev = lab.make_labels(px, h, "relative").loc[X.index]
    p = lab.walkforward(X, y_tr, horizon=h, model_fn=search.MODELS[mdl],
                        retrain_every=126, min_train_days=252, y_eval=y_ev)
    p.to_parquet(f)
    print("wrote", f, p.shape, "days", p.day.nunique())

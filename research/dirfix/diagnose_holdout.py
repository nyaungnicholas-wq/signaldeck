"""Two integrity tests on the holdout winners.

1. Class balance of the SELECTED subset -- if the null is 0.60 on a median-split
   target, the selection created the imbalance and the null is absorbing the
   very thing that looks like skill.
2. BLOCK bootstrap for h>1. Consecutive days' h-day forward returns overlap by
   (h-1)/h, so resampling days independently treats ~95%-correlated observations
   as independent and reports a CI far too narrow.
"""
import numpy as np, pandas as pd, lab, search

px = lab.load("panel.parquet")
X = pd.read_parquet("features_v2.parquet")
m = lab.universe_mask(px).stack(); m.index.names = ["day", "symbol_id"]
X = X.loc[X.index.intersection(m[m].index)]
split = pd.Timestamp("2025-03-01")

for h, cov in ((1, 0.01), (5, 0.01), (21, 0.01)):
    y = lab.make_labels(px, h, "relative").loc[X.index]
    pred = lab.walkforward(X, y, horizon=h, model_fn=search.MODELS["hgb_shallow"],
                           retrain_every=126, min_train_days=252)
    oos = pred[pred.day >= split].assign(conv=lambda d: (d.prob - 0.5).abs())
    r = oos.groupby("day")["conv"].rank(pct=True, method="first", ascending=False)
    s = oos[r <= cov].copy()
    s["hit"] = ((s.prob > 0.5) == (s.y == 1)).astype(float)

    up_call = (s.prob > 0.5).mean()
    print("h=%-3d n=%-6d  y==1 in selection: %.3f   model calls up: %.3f"
          % (h, len(s), s.y.mean(), up_call))
    print("      full-universe y==1 over holdout: %.3f"
          % y[y.index.get_level_values(0) >= split].mean())

    # block bootstrap over non-overlapping h-day blocks
    days = np.sort(s.day.unique())
    blocks = [days[i:i+h] for i in range(0, len(days), h)]
    bymap = {d: i for i, b in enumerate(blocks) for d in b}
    s["blk"] = s.day.map(bymap)
    gb = s.groupby("blk")["hit"].agg(["sum", "count"])
    rng = np.random.default_rng(0)
    idx = rng.integers(0, len(gb), size=(4000, len(gb)))
    bs = gb["sum"].to_numpy()[idx].sum(1) / gb["count"].to_numpy()[idx].sum(1)
    lo, hi = np.percentile(bs, [2.5, 97.5])
    day_ci = np.percentile(
        (lambda g: g["sum"].to_numpy()[rng.integers(0, len(g), (4000, len(g)))].sum(1)
         / g["count"].to_numpy()[rng.integers(0, len(g), (4000, len(g)))].sum(1))
        (s.groupby("day")["hit"].agg(["sum", "count"])), [2.5, 97.5])
    print("      acc %.4f | day-clustered CI [%.4f, %.4f] width %.4f"
          % (s.hit.mean(), day_ci[0], day_ci[1], day_ci[1]-day_ci[0]))
    print("      %d independent %d-day BLOCKS -> block CI [%.4f, %.4f] width %.4f  (%.1fx wider)\n"
          % (len(gb), h, lo, hi, hi-lo, (hi-lo)/max(day_ci[1]-day_ci[0], 1e-9)))

"""Is the 42d extremes20 candidate real, or the one-sided book again?

Two tests: (a) call balance -- if it calls one way ~92% of the time and the
null is only 0.50 because the SEARCH-period selection happened to be balanced,
that is luck not skill; (b) the sealed holdout, with a 42-day block bootstrap.
"""
import numpy as np, pandas as pd, lab, search

TRIALS = 1246
px = lab.load("panel.parquet")
split = pd.Timestamp("2025-03-01")

for feats in ("v2_all", "v2_no_market"):
    X = pd.read_parquet("features_v2.parquet")
    X = X[search.FEATURE_SETS[feats[3:]](X.columns.tolist())]
    m = lab.universe_mask(px).stack(); m.index.names = ["day", "symbol_id"]
    X = X.loc[X.index.intersection(m[m].index)]
    y_tr = lab.make_labels(px, 42, "extremes20").loc[X.index]
    y_ev = lab.make_labels(px, 42, "relative").loc[X.index]
    pred = lab.walkforward(X, y_tr, horizon=42, model_fn=search.MODELS["logit"],
                           retrain_every=126, min_train_days=252, y_eval=y_ev)
    for era, sub in (("SEARCH", pred[pred.day < split]), ("HOLDOUT", pred[pred.day >= split])):
        if sub.empty:
            print(f"{feats:13s} {era:8s} EMPTY"); continue
        s = sub.assign(conv=(sub.prob - 0.5).abs())
        r = s.groupby("day")["conv"].rank(pct=True, method="first", ascending=False)
        for cov in (0.02, 0.01):
            sel = s[r <= cov]
            e = lab.evaluate(sel, block=42)
            up = float((sel.prob > 0.5).mean())
            se = (e["ci_hi"] - e["ci_lo"]) / 3.92
            bar = search.deflated_threshold(se, TRIALS)
            print(f"{feats:13s} {era:8s} cov={cov:.0%}  acc={e['acc']:.4f}  null={e['null']:.4f}  "
                  f"skill={e['skill']:+.4f}  ci=[{e['ci_lo']:.4f},{e['ci_hi']:.4f}]  "
                  f"blocks={e['blocks']:3d}  calls_up={up:.3f}  "
                  f"bar={bar:+.4f} {'SURVIVES' if e['skill']>bar else 'FAILS'}  "
                  f"{'60%+' if e['acc']>=0.60 else '<60%'}")
    print()

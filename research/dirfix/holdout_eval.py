"""THE ONE AUTHORIZED HOLDOUT READ. 533 sealed days, sha256 de0efff7.

Winners were chosen on the search period only, by best honest accuracy among
rows whose day-clustered ci_lo cleared the null. Trials = 686, so the deflated
bar is reported alongside: with that many looks, a skill estimate must clear
se*sqrt(2*ln(686)) to mean anything.
"""
import json, hashlib, numpy as np, pandas as pd, lab, search

TRIALS = 910
WINNERS = [
    dict(h=1,  model="hgb_shallow", target="relative", cov=0.01, search_acc=0.5492),
    dict(h=5,  model="hgb_shallow", target="relative", cov=0.01, search_acc=0.5550),
    dict(h=21, model="hgb_shallow", target="relative", cov=0.01, search_acc=0.5869),
    dict(h=21, model="logit", target="extremes10", cov=0.05, search_acc=0.5792),
]
meta = json.load(open("HOLDOUT.json"))
p = pd.read_parquet("panel.parquet")
hold = p[p.day >= meta["split"]]
h = hashlib.sha256(pd.util.hash_pandas_object(
    hold[["symbol_id", "day", "close"]], index=False).values.tobytes()).hexdigest()
print("holdout sha256 %s  %s" % (h, "MATCHES SEAL" if h == meta["sha256"] else "!!! ALTERED !!!"))
print("holdout: %s .. %s, %d days\n" % (hold.day.min().date(), hold.day.max().date(),
                                        hold.day.nunique()))

px = lab.load("panel.parquet")
X_all = pd.read_parquet("features_v2.parquet")
mask = lab.universe_mask(px)
sel = mask.stack(); sel.index.names = ["day", "symbol_id"]
X_all = X_all.loc[X_all.index.intersection(sel[sel].index)]
split = pd.Timestamp(meta["split"])

for w in WINNERS:
    y_tr = lab.make_labels(px, w["h"], w["target"]).loc[X_all.index]
    y_ev = lab.make_labels(px, w["h"], "relative").loc[X_all.index]
    pred = lab.walkforward(X_all, y_tr, horizon=w["h"],
                           model_fn=search.MODELS[w["model"]],
                           retrain_every=126, min_train_days=252, y_eval=y_ev)
    oos = pred[pred.day >= split]
    rows = search.sweep_coverage(oos, {})
    r = [x for x in rows if abs(x["coverage"] - w["cov"]) < 1e-9][0]
    se = (r["ci_hi"] - r["ci_lo"]) / 3.92
    bar = search.deflated_threshold(se, TRIALS)
    print("h=%-3s %-12s train_on=%-11s cov=%.0f%%" % (w["h"], w["model"], w["target"], w["cov"]*100))
    print("   search-period acc %.4f  ->  HOLDOUT acc %.4f  (n=%d, %d days)"
          % (w["search_acc"], r["acc"], r["n"], r["days"]))
    print("   null %.4f   skill %+.4f   ci [%.4f, %.4f]   deff %.1f"
          % (r["null"], r["skill"], r["ci_lo"], r["ci_hi"], r["deff"]))
    print("   deflated bar for %d trials = %+.4f  ->  %s" % (TRIALS, bar,
          "SURVIVES" if r["skill"] > bar else "does NOT survive deflation"))
    print("   reaches 60%%: %s\n" % ("YES" if r["acc"] >= 0.60 else "NO (%.1fpp short)" % ((0.60-r["acc"])*100)))

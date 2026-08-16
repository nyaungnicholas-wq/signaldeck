"""Train on the tail target, GRADE on a label defined for every row.

The stage D/E numbers score only rows that turned out to be decile movers --
that conditions on a future outcome. Live you pick names without knowing which
will be tail movers. This re-grades the same models honestly.
"""
import pandas as pd, lab, search

BEST = [dict(h=5, model="logit",      feats="v2_all"),
        dict(h=1, model="logit_c001", feats="v2_no_market")]

for b in BEST:
    Xf, y_train, mask = search.load_search_data(b["h"], "extremes10", v2=True)
    cols = search.FEATURE_SETS[b["feats"][3:]](Xf.columns.tolist())
    X = Xf[cols]
    sel = mask.stack(); sel.index.names = ["day", "symbol_id"]
    X = X.loc[X.index.intersection(sel[sel].index)]
    y_train = y_train.loc[X.index]
    print(f"\n{'='*74}\nhorizon {b['h']}d  model={b['model']}  features={b['feats']}")
    for grade_on in ("extremes10", "relative", "absolute"):
        _, y_g, _ = search.load_search_data(b["h"], grade_on, v2=True)
        pred = lab.walkforward(X, y_train, horizon=b["h"],
                               model_fn=search.MODELS[b["model"]],
                               retrain_every=126, min_train_days=252,
                               y_eval=y_g.loc[X.index])
        search.assert_search_only(pred)
        rows = search.sweep_coverage(pred, {"grade_on": grade_on})
        d = pd.DataFrame(rows)
        tag = "  <-- conditions on the outcome" if grade_on == "extremes10" else ""
        print(f"\n  graded on {grade_on}{tag}")
        print(d[["coverage", "n", "days", "acc", "null", "skill", "ci_lo", "deff"]]
              .to_string(index=False, float_format=lambda v: f"{v:.4f}"))

"""Accuracy-vs-Sharpe frontier as a function of book BREADTH (k names per leg).

ablate.py only ever ran k=10, plus three k=25 cells all pinned to invvol+beta_neutral
+vol_target (the worst corner of every other axis). So "0 of 27 configs clear the bar"
was measured with breadth held constant. This sweeps it properly.

SEARCH PERIOD ONLY. The holdout is sealed; reading it is a protocol breach.
"""
import pandas as pd
import port

SPLIT = pd.Timestamp("2025-03-01")


def sweep(horizons=(21, 42), ks=(10, 20, 40, 75, 150, 300),
          weightings=("conviction", "equal"), betas=(False, True)):
    rets = pd.read_parquet("rets_daily.parquet")
    out, cache = [], {}

    for h in horizons:
        if h not in cache:
            df = pd.read_parquet(f"preds_{h}_extremes10_ens_lh.parquet")
            cache[h] = df[df["day"] < SPLIT].copy()
        preds = cache[h]
        day_counts = preds.groupby("day").size()
        pool_med = float(day_counts.median())

        for k in ks:
            # Mirror port.simulate's own cap (port.py:114 k_eff = min(k, n//2)) so the
            # book we grade is the book we trade. Without it head(k)/tail(k) overlap on
            # any day with fewer than 2k names and a symbol is both long and short.
            n_long_med = float(day_counts.map(lambda c: min(k, c // 2)).median())

            hits, ys = [], []
            for _, grp in preds.groupby("day"):
                # same tie-break as port.py:112 so both pick the identical names
                g = grp.sort_values(["prob", "symbol_id"], ascending=[False, True])
                k_eff = min(k, len(g) // 2)
                if k_eff < 1:
                    continue
                sel = pd.concat([g.head(k_eff).assign(call=1.0),
                                 g.tail(k_eff).assign(call=0.0)]).dropna(subset=["y"])
                if sel.empty:
                    continue
                hits.append((sel["call"] == sel["y"]).astype(float))
                ys.append(sel["y"])

            if hits:
                hs, yv = pd.concat(hits), pd.concat(ys)
                acc, my, n_bets = float(hs.mean()), float(yv.mean()), int(len(hs))
                null = max(my, 1.0 - my)
            else:
                acc = null = float("nan")
                n_bets = 0

            for w in weightings:
                for bn in betas:
                    try:
                        r = port.simulate(pred=preds[["day", "symbol_id", "prob"]],
                                          rets=rets, horizon=h, k=k, weighting=w,
                                          beta_neutral=bn, vol_target=None,
                                          cost_bps=10.0, borrow_bps_yr=300.0,
                                          max_weight=0.10)
                        got = {"sharpe": r["sharpe"], "t_nw": r["t_nw"],
                               "ann_ret": r["ann_ret"], "ann_vol": r["ann_vol"],
                               "max_dd": r["max_dd"], "calmar": r["calmar"],
                               "beta": r["beta_to_market"], "turnover": r["turnover_ann"]}
                    except Exception as e:
                        # never swallow silently: a NaN row that reads as "no result"
                        # is how a broken call survives an entire sweep
                        print(f"  !! h={h} k={k} {w} bn={bn} FAILED: "
                              f"{type(e).__name__}: {e}", flush=True)
                        got = dict.fromkeys(("sharpe", "t_nw", "ann_ret", "ann_vol",
                                             "max_dd", "calmar", "beta", "turnover"),
                                            float("nan"))
                    out.append({"h": h, "k": k, "weighting": w, "beta_neutral": bn,
                                **got, "n_long_med": n_long_med, "pool_med": pool_med,
                                "acc": acc, "null": null, "n_bets": n_bets})
                    print(f"h={h} k={k} {w} bn={bn} sharpe={got['sharpe']:.3f} "
                          f"acc={acc:.4f}", flush=True)
    return out


if __name__ == "__main__":
    df = pd.DataFrame(sweep())
    df.to_csv("breadth.csv", index=False)
    pd.set_option("display.width", 220)
    print("\n=== by Sharpe ===")
    print(df.sort_values("sharpe", ascending=False).to_string(
        index=False, float_format=lambda v: f"{v:.3f}"))

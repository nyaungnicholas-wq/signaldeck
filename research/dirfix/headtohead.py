"""Production's live book vs the new model, same simulator, same everything.

The only difference is whose probabilities rank the names. Same universe, same
horizon (1w = 5 trading days), same days, same costs, same k, same weighting.
Anything else would be comparing two decisions at once.

Window is short by necessity: production has only published since 2026-07-03,
so this is ~44 days and ~8 non-overlapping 5-day blocks. Reported, not hidden.
"""
import sqlite3, numpy as np, pandas as pd, lab, search, port

H = 5
px = lab.load("panel.parquet")
close, vol = px["close"], px["volume"]
rets = pd.read_parquet("rets_daily.parquet")
dv = (close * vol).rolling(21).mean()
STRICT = ((close >= 5.0) & (dv >= 1e7) &
          (close.notna().rolling(252).sum() >= 252)).shift(1, fill_value=False).astype(bool)
sm = STRICT.stack(); sm.index.names = ["day", "symbol_id"]
strict_idx = sm[sm].index

# --- production's own published probabilities, deduped to one per symbol-day
con = sqlite3.connect("file:../../data/signaldeck.db?mode=ro", uri=True)
prod = pd.read_sql("""
    WITH d AS (SELECT symbol_id, date(ts,'unixepoch') day, prob,
               ROW_NUMBER() OVER (PARTITION BY symbol_id, date(ts,'unixepoch')
                                  ORDER BY ts DESC) rn
               FROM prediction_outcomes WHERE horizon='1w' AND prob IS NOT NULL)
    SELECT symbol_id, day, prob FROM d WHERE rn=1""", con)
con.close()
prod["day"] = pd.to_datetime(prod["day"])
prod = prod.set_index(["day", "symbol_id"])
prod = prod.loc[prod.index.intersection(strict_idx)].reset_index()
WIN = sorted(prod["day"].unique())
print("live window: %s .. %s  (%d days, %d prod rows, %d names/day)"
      % (WIN[0].date(), WIN[-1].date(), len(WIN), len(prod), len(prod) // len(WIN)))

# --- the new model on the SAME days
X = pd.read_parquet("features_v2.parquet")
X = X.loc[X.index.intersection(strict_idx)]
y_tr = lab.make_labels(px, H, "extremes10").loc[X.index]
y_ev = lab.make_labels(px, H, "relative").loc[X.index]
# Fit-and-predict on EVERY production day, not just fold boundaries. The
# walk-forward emits predictions only on its test blocks, which covered 9 of
# these 28 days -- too few to compare anything. Each day is fitted only on rows
# whose label had resolved by then (embargo of H trading days).
from sklearn.impute import SimpleImputer
idx = close.index
days_all = X.index.get_level_values("day")
yv = y_tr.reindex(X.index)
parts = []
for d in WIN:
    if d not in idx:
        continue
    cut = idx[max(0, idx.get_loc(d) - H)]
    tr = (days_all <= cut) & yv.notna().to_numpy()
    te = days_all == d
    if tr.sum() < 5000 or te.sum() < 25:
        continue
    imp = SimpleImputer(strategy="median")
    m = search.MODELS["ens_lh"]()
    m.fit(imp.fit_transform(X[tr]), yv[tr].to_numpy())
    p = m.predict_proba(imp.transform(X[te]))[:, 1]
    parts.append(pd.DataFrame({"day": d,
                               "symbol_id": X[te].index.get_level_values("symbol_id"),
                               "prob": p}))
    print("  fitted %s (%d train, %d names)" % (d.date(), tr.sum(), te.sum()), flush=True)
mine = pd.concat(parts, ignore_index=True)
prod = prod[prod.day.isin(set(mine.day.unique()))]
print("compared on %d common days\n" % mine.day.nunique())

kw = dict(horizon=H, k=10, weighting="conviction", beta_neutral=True,
          vol_target=None, cost_bps=10.0, borrow_bps_yr=300.0, max_weight=0.10)
print("%-26s %8s %9s %8s %8s %9s %7s" %
      ("book", "Sharpe", "ann", "vol", "maxDD", "worst day", "days"))
for name, p in (("production (live 1w probs)", prod), ("new model (5d, ens_lh)", mine)):
    r = port.simulate(p, rets, **kw)
    d = r["daily"]
    print("%-26s %8.2f %8.1f%% %7.1f%% %7.1f%% %8.2f%% %7d"
          % (name, r["sharpe"], 100*r["ann_ret"], 100*r["ann_vol"],
             100*r["max_dd"], 100*(d.min() if len(d) else 0), r["n_days"]))

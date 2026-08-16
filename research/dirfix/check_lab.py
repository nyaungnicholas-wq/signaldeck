"""Acceptance test for lab.py. The worker's module must pass this UNCHANGED.
These assertions encode the failures this repo has already shipped:
lookahead, label-overlap leakage, pseudo-replication, and naive binomial CIs."""
import numpy as np, pandas as pd, lab

FAIL = []
def ck(name, cond):
    print(("  OK   " if cond else "  FAIL ") + name)
    if not cond: FAIL.append(name)

px = lab.load("panel.parquet")
ck("load returns close/high/low/volume", all(k in px for k in ("close","high","low","volume")))
C = px["close"]
ck("close indexed by day ascending", C.index.is_monotonic_increasing)
# 1,916 trading days after the stocks-only rebuild (was 2,156 when 7 crypto
# symbols were injecting weekend rows into the shared calendar).
ck("close is day x symbol wide (%d days x %d symbols)" % C.shape,
   1800 < C.shape[0] < 2000 and C.shape[1] > 2000)

# --- 1. NO LOOKAHEAD: features at day T must not change when future data is removed
T = C.index[-400]
full = lab.make_features(px)
trunc_px = {k: (v if k == "meta" else v.loc[:T]) for k, v in px.items()}
trunc = lab.make_features(trunc_px)
a = full.xs(T, level=0).sort_index()
b = trunc.xs(T, level=0).sort_index()
common = a.index.intersection(b.index); cols = a.columns.intersection(b.columns)
ck("features have >=15 columns", len(cols) >= 15)
diff = (a.loc[common, cols] - b.loc[common, cols]).abs()
worst = float(np.nanmax(diff.values)) if len(common) else 9e9
ck("NO LOOKAHEAD: features at T identical without future rows (max|d|<1e-9), got %.3g" % worst,
   worst < 1e-9)

# --- 2. LABELS: horizon alignment and target nulls
for h in (1, 5):
    ya = lab.make_labels(px, h, "absolute")
    yr = lab.make_labels(px, h, "relative")
    last = ya.dropna().index.get_level_values(0).max()
    ck("h=%d absolute label unresolved for final %d days" % (h, h),
       last <= C.index[-h-1])
    # Measured full-universe up-rate is 0.4823 (1d) / 0.4973 (5d) -- BELOW 0.5,
    # because equal-weighting 2,947 names loads illiquid microcaps that drift
    # down. Only a sanity bound belongs here; the load-bearing property is that
    # the relative and extremes targets are pinned at 0.5 by construction.
    ck("h=%d absolute null in a sane range (0.40-0.62), got %.4f" % (h, ya.mean()),
       0.40 < ya.mean() < 0.62)
    ck("h=%d RELATIVE null is ~0.5 by construction (0.47-0.53)" % h, 0.47 < yr.mean() < 0.53)
    ye = lab.make_labels(px, h, "extremes")
    ck("h=%d EXTREMES null is ~0.5 and withholds the middle" % h,
       0.47 < ye.mean() < 0.53 and ye.notna().sum() < 0.75 * ya.notna().sum())

# --- 3. EMBARGO: no training label may overlap the test day
days = C.index
sp = lab.wf_splits(days, horizon=5, retrain_every=63, min_train_days=252)
ck("wf_splits produced >=10 folds", len(sp) >= 10)
bad = [(tr[-1], te[0]) for tr, te in sp if (te[0] - tr[-1]).days < 5]
ck("EMBARGO: every fold's last train day is >=horizon before first test day", not bad)
ck("folds are chronological and non-overlapping",
   all(sp[i][1][-1] < sp[i+1][1][0] for i in range(len(sp)-1)))

# --- 4. EVALUATE: correctness + day-clustered CI must be WIDER than naive binomial
rng = np.random.default_rng(0)
d = np.repeat(pd.date_range("2020-01-01", periods=60, freq="D"), 200)
# every symbol on a day shares the outcome -> design effect must be huge
dayflip = np.repeat(rng.random(60) < 0.5, 200)
pred = pd.DataFrame({"day": d, "symbol_id": np.tile(np.arange(200), 60),
                     "prob": np.where(dayflip, 0.9, 0.1), "y": dayflip.astype(float)})
r = lab.evaluate(pred)
ck("evaluate: perfect predictions score 1.0", abs(r["acc"] - 1.0) < 1e-9)
pred2 = pred.copy(); pred2["prob"] = 1 - pred2["prob"]
r2 = lab.evaluate(pred2)
ck("evaluate: inverted predictions score 0.0", abs(r2["acc"]) < 1e-9)
mix = pred.copy()
mix["prob"] = np.where(np.repeat(np.arange(60) < 24, 200), 1 - mix["prob"], mix["prob"])
r3 = lab.evaluate(mix)
ck("evaluate: 36/60 days correct -> acc 0.60", abs(r3["acc"] - 0.6) < 1e-9)
ck("evaluate: reports distinct_days", r3.get("days") == 60)
naive = 1.96 * np.sqrt(0.6 * 0.4 / len(mix))
width = r3["ci_hi"] - r3["ci_lo"]
ck("DAY-CLUSTERED CI is wider than naive binomial (%.4f vs %.4f)" % (width, 2*naive),
   width > 2 * naive * 2)
ck("evaluate: design_effect >> 1 when the book moves together", r3.get("deff", 0) > 5)
ck("evaluate: mean daily agreement ~1.0 on a unanimous book",
   r3.get("agreement", 0) > 0.99)
ck("evaluate: reports skill vs prequential null", "skill" in r3 and "null" in r3)

print("\n%d/%d checks passed" % (len(FAIL) == 0 and 1 or 0, 1))
if FAIL:
    print("FAILED:"); [print("  - " + f) for f in FAIL]
    raise SystemExit(1)
print("ALL CHECKS PASSED")

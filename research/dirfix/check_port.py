"""Acceptance test for port.py -- the real portfolio simulator.

Each assertion pins a risk control that has to actually work, not merely be
present. The k-bug lesson applies: a control that is silently ignored still
passes any test that only checks the output exists.
"""
import numpy as np, pandas as pd, port

FAIL = []
def ck(n, c):
    print(("  OK   " if c else "  FAIL ") + n)
    if not c: FAIL.append(n)

rng = np.random.default_rng(7)
days = pd.bdate_range("2020-01-01", periods=500)
S = 120
# deliberately heterogeneous vols: names 0..39 are 5x more volatile
vols = np.concatenate([np.full(40, 0.05), np.full(80, 0.01)])
mkt = rng.normal(0, 0.01, len(days))
betas = np.linspace(0.2, 2.0, S)
idio = rng.normal(0, 1, (len(days), S)) * vols
rets = pd.DataFrame(mkt[:, None] * betas[None, :] + idio, index=days,
                    columns=np.arange(S))
H = 10
fwd = (1 + rets).rolling(H).apply(np.prod, raw=True).shift(-H) - 1   # H-day fwd

# a predictor that genuinely knows the forward return
pf = fwd.stack().rename("f").reset_index()
pf.columns = ["day", "symbol_id", "f"]
pf["prob"] = 1 / (1 + np.exp(-pf["f"].fillna(0) * 30))
pred = pf[["day", "symbol_id", "prob"]]

base = port.simulate(pred, rets, horizon=H, k=10, weighting="equal",
                     cost_bps=0.0, borrow_bps_yr=0.0)
ck("returns a daily portfolio series", len(base["daily"]) > 400)
# Stats must cover the ACTIVE window, not the whole rets index. Grading a
# holdout-only prediction frame over a padded series reported Sharpe 1.11 for a
# book whose real number was 2.58.
_late = pred[pred["day"] >= days[300]]
_r = port.simulate(_late, rets, horizon=H, k=10, cost_bps=0.0, borrow_bps_yr=0.0)
ck("a late-starting book is not padded with leading zeros (n=%d, not %d)"
   % (_r["n_days"], len(days)), _r["n_days"] < 0.75 * len(days))
ck("late-start book has no all-zero leading run",
   float((_r["daily"].iloc[:20] == 0).mean()) < 0.9)
ck("skilled predictor makes money (ann %.3f)" % base["ann_ret"], base["ann_ret"] > 0)
ck("reports sharpe, ann_vol, max_dd, turnover", all(
    x in base for x in ("sharpe", "ann_vol", "max_dd", "turnover_ann")))

inv = pred.assign(prob=1 - pred["prob"])
ck("inverted predictor loses money", port.simulate(
    inv, rets, horizon=H, k=10, cost_bps=0.0, borrow_bps_yr=0.0)["ann_ret"] < 0)

# OVERLAPPING TRANCHES: the book must hold H staggered tranches at once
ck("holds ~H overlapping tranches once warm (%.1f)" % base["avg_tranches"],
   abs(base["avg_tranches"] - H) < 1.5)

# DOLLAR NEUTRAL
ck("dollar-neutral each day (max |sum w| = %.2e)" % base["max_abs_net_weight"],
   base["max_abs_net_weight"] < 1e-9)

# INVERSE-VOL WEIGHTING must actually cut realised vol vs equal weight
iv = port.simulate(pred, rets, horizon=H, k=10, weighting="invvol",
                   cost_bps=0.0, borrow_bps_yr=0.0)
ck("inverse-vol weighting lowers realised vol (%.4f < %.4f)"
   % (iv["ann_vol"], base["ann_vol"]), iv["ann_vol"] < base["ann_vol"])

# VOL TARGETING must hit its target
vt = port.simulate(pred, rets, horizon=H, k=10, weighting="equal",
                   vol_target=0.10, cost_bps=0.0, borrow_bps_yr=0.0)
ck("vol targeting lands near 10%% (got %.3f)" % vt["ann_vol"],
   abs(vt["ann_vol"] - 0.10) < 0.04)

# BETA NEUTRALITY must actually null the market exposure
bn = port.simulate(pred, rets, horizon=H, k=10, beta_neutral=True,
                   cost_bps=0.0, borrow_bps_yr=0.0)
ck("beta-neutral book has |beta| < unconstrained (%.3f vs %.3f)"
   % (abs(bn["beta_to_market"]), abs(base["beta_to_market"])),
   abs(bn["beta_to_market"]) < max(abs(base["beta_to_market"]) * 0.5, 0.05))

# COSTS
c0 = port.simulate(pred, rets, horizon=H, k=10, cost_bps=0.0, borrow_bps_yr=0.0)["ann_ret"]
c1 = port.simulate(pred, rets, horizon=H, k=10, cost_bps=10.0, borrow_bps_yr=0.0)["ann_ret"]
c2 = port.simulate(pred, rets, horizon=H, k=10, cost_bps=50.0, borrow_bps_yr=0.0)["ann_ret"]
ck("costs reduce return monotonically", c0 > c1 > c2)

# NEWEY-WEST: overlapping holds autocorrelate the daily series, so the
# significance test must be wider than the naive iid one
ck("reports newey-west t-stat", "t_nw" in base and "t_naive" in base)
ck("newey-west t < naive t under overlap (%.2f vs %.2f)" % (base["t_nw"], base["t_naive"]),
   abs(base["t_nw"]) < abs(base["t_naive"]))

# zero signal must not print money after costs, in EXPECTATION over seeds
nets = []
for sd in range(8):
    g = np.random.default_rng(500 + sd)
    n = pred.assign(prob=g.random(len(pred)))
    nets.append(port.simulate(n, rets, horizon=H, k=10, cost_bps=10.0,
                              borrow_bps_yr=300.0)["ann_ret"])
ck("zero-signal loses after costs in expectation (%.4f)" % np.mean(nets), np.mean(nets) < 0)

print("\nALL CHECKS PASSED" if not FAIL else "\nFAILED:\n  " + "\n  ".join(FAIL))
raise SystemExit(1 if FAIL else 0)

"""Acceptance test for pnl.py. Accuracy is not profit -- this file pins the
difference. Costs and overlapping holds are where paper edges die."""
import numpy as np, pandas as pd, pnl

FAIL = []
def ck(n, c):
    print(("  OK   " if c else "  FAIL ") + n)
    if not c: FAIL.append(n)

rng = np.random.default_rng(0)
days = pd.bdate_range("2020-01-01", periods=420)
syms = np.arange(200)
idx = pd.MultiIndex.from_product([days, syms], names=["day", "symbol_id"])
truth = pd.Series(rng.normal(0, 0.05, len(idx)), index=idx)      # 5d fwd returns

# a perfect predictor: prob monotone in the realised forward return
perfect = pd.DataFrame({"day": idx.get_level_values(0), "symbol_id": idx.get_level_values(1),
                        "prob": 1 / (1 + np.exp(-truth.to_numpy() * 100))})
r = pnl.backtest(perfect, truth, horizon=5, k=10, cost_bps=0.0, borrow_bps_yr=0.0)
ck("perfect predictor is profitable gross", r["gross_ann"] > 0.5)
ck("reports tranche count and days", r["n_tranches"] > 50 and r["n_days"] > 300)
ck("dollar-neutral: equal longs and shorts", r["n_long"] == r["n_short"])
# k must BIND. n_long == n_short holds whether k is honoured or ignored, so
# assert the actual count: 200 names/day, k=10 -> exactly 10 longs per day.
ck("k actually binds (n_long == k * n_days), got %d vs %d"
   % (r["n_long"], 10 * r["n_days"]), r["n_long"] == 10 * r["n_days"])
_r50 = pnl.backtest(perfect, truth, horizon=5, k=50, cost_bps=0.0, borrow_bps_yr=0.0)
ck("a larger k takes more positions", _r50["n_long"] > r["n_long"])
ck("a wider book dilutes the tails: k=50 gross < k=10 gross",
   _r50["gross_ann"] < r["gross_ann"])

inv = perfect.assign(prob=1 - perfect["prob"])
ri = pnl.backtest(inv, truth, horizon=5, k=10, cost_bps=0.0, borrow_bps_yr=0.0)
ck("inverted predictor loses symmetrically (%.3f vs %.3f)" % (ri["gross_ann"], r["gross_ann"]),
   abs(ri["gross_ann"] + r["gross_ann"]) < 1e-6)

# costs must strictly reduce net return, and monotonically
nets = [pnl.backtest(perfect, truth, horizon=5, k=10, cost_bps=c,
                     borrow_bps_yr=0.0)["net_ann"] for c in (0.0, 5.0, 20.0, 100.0)]
ck("costs strictly reduce net return, monotonically", all(a > b for a, b in zip(nets, nets[1:])))
ck("net < gross once costs are on", nets[1] < r["gross_ann"])

# a zero-information predictor must not print money after costs
# Averaged over seeds: on a 10-name book a single noise draw has enough
# variance to clear the ~5%/yr cost drag by luck. The claim is about the
# EXPECTATION -- no free money from noise -- so test that, not one draw.
nets, grosses = [], []
for seed in range(12):
    g = np.random.default_rng(100 + seed)
    noise = perfect.assign(prob=g.random(len(perfect)))
    rn = pnl.backtest(noise, truth, horizon=5, k=10, cost_bps=5.0, borrow_bps_yr=30.0)
    nets.append(rn["net_ann"]); grosses.append(rn["gross_ann"])
ck("zero-signal is ~flat gross in expectation (|%.4f| < 0.03)" % np.mean(grosses),
   abs(np.mean(grosses)) < 0.03)
ck("zero-signal LOSES after costs in expectation (%.4f < 0)" % np.mean(nets),
   np.mean(nets) < 0)
ck("most zero-signal seeds lose after costs (%d/12)" % sum(n < 0 for n in nets),
   sum(n < 0 for n in nets) >= 8)

# Sharpe must come from NON-OVERLAPPING tranches: h-day holds entered daily
# overlap by (h-1)/h, and treating them as independent inflates Sharpe by ~sqrt(h)
ck("reports sharpe and max_dd", "sharpe" in r and "max_dd" in r)
ck("max_dd is a non-positive fraction", r["max_dd"] <= 0)
ck("tranche returns are non-overlapping (count ~ days/horizon)",
   abs(r["n_tranches"] - len(days) / 5) <= 2)
ck("turnover reported and positive", r.get("turnover_ann", 0) > 0)

print("\nALL CHECKS PASSED" if not FAIL else "\nFAILED:\n  " + "\n  ".join(FAIL))
raise SystemExit(1 if FAIL else 0)

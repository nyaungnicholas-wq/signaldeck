import sys
import json
import hashlib
import pathlib
import datetime
import pandas as pd
import numpy as np
import evalcore
import strategies

D = pathlib.Path(__file__).parent
manifest_path = D / "out" / "finalist_manifest.json"
with manifest_path.open() as f:
    M = json.load(f)
F = M.get("finalist")
if F is None:
    print("NO FINALIST")
    sys.exit(0)

if not evalcore.seal_is_open():
    print("REFUSAL: SPYX_SEAL")
    sys.exit(2)

R = D / "out" / "SEAL_RESULT.json"
if R.exists():
    with R.open() as f:
        Rdata = json.load(f)
    print(Rdata.get("graded_utc", ""))
    print(Rdata.get("finalist_name", ""))
    sys.exit(3)

def sha256_path(p):
    h = hashlib.sha256()
    with p.open("rb") as f:
        for chunk in iter(lambda: f.read(8192), b""):
            h.update(chunk)
    return h.hexdigest()

dig_strat = sha256_path(D / "strategies.py")
dig_eval = sha256_path(D / "evalcore.py")
frozen = M.get("frozen_hashes")
if frozen is not None:
    if dig_strat != frozen.get("strategies.py", "") or dig_eval != frozen.get("evalcore.py", ""):
        print(dig_strat)
        print(dig_eval)
        sys.exit(4)

px = evalcore.closes(strategies.SLEEVES, evalcore.DEV_START, evalcore.SEAL_END)
rets = px.pct_change().fillna(0.0)
rf = evalcore.riskfree_daily(px.index)

seal_start = pd.Timestamp(evalcore.SEAL_START)
seal_end = pd.Timestamp(evalcore.SEAL_END)

if F["weighting"] == "equal":
    w_func = strategies.weights_divtrend
else:
    w_func = strategies.weights_divtrend_invvol

w_full = w_func(px, sma=F["sma"], leverage=F["leverage"], sleeves=None)
bw_full = strategies.weights_spy_bh(px)

mask = (px.index >= seal_start) & (px.index <= seal_end)
px_s = px.loc[mask]
rets_s = rets.loc[mask]
rf_s = rf.loc[mask]
w_s = w_full.loc[mask]
bw_s = bw_full.loc[mask]

print(f"Pre-seal history used for warm-up only: {len(px)} days total, {len(px_s)} days in seal period.")

net_s = evalcore.cost_returns(w_s, rets_s, rf_s)
bench_s = evalcore.cost_returns(bw_s, rets_s, rf_s)

strat_m = evalcore.metrics(net_s, rf_s, weights=w_s, bench_ret=bench_s, name=F["name"])
bench_m = evalcore.metrics(bench_s, rf_s, weights=bw_s, bench_ret=bench_s, name="spy_bh")

excess = evalcore.excess_pp(strat_m["cum_return"], bench_m["cum_return"])

def numpy_metrics(ret_series, rf_series):
    cum = (1 + ret_series).prod() - 1
    ann_vol = ret_series.std() * np.sqrt(252)
    excess_ret = ret_series - rf_series
    if excess_ret.std() == 0:
        sharpe = None
    else:
        sharpe = excess_ret.mean() / excess_ret.std() * np.sqrt(252)
    wealth = (1 + ret_series).cumprod()
    dd = (wealth - wealth.cummax()) / wealth.cummax()
    max_dd = dd.min()  # NEGATIVE, matching the evalcore convention
    return {"cum_return": float(cum), "ann_vol": float(ann_vol), "sharpe": sharpe, "max_dd": float(max_dd)}

strat_np = numpy_metrics(net_s, rf_s)
bench_np = numpy_metrics(bench_s, rf_s)

print("Strategy cum_return:", strat_m["cum_return"], "vs", strat_np["cum_return"])
print("Strategy ann_vol:", strat_m["ann_vol"], "vs", strat_np["ann_vol"])
print("Strategy sharpe:", strat_m["sharpe"], "vs", strat_np["sharpe"])
print("Strategy max_dd:", strat_m["max_dd"], "vs", strat_np["max_dd"])
print("Benchmark cum_return:", bench_m["cum_return"], "vs", bench_np["cum_return"])
print("Benchmark ann_vol:", bench_m["ann_vol"], "vs", bench_np["ann_vol"])
print("Benchmark sharpe:", bench_m["sharpe"], "vs", bench_np["sharpe"])
print("Benchmark max_dd:", bench_m["max_dd"], "vs", bench_np["max_dd"])

assert abs(strat_m["cum_return"] - strat_np["cum_return"]) < 1e-6
assert abs(strat_m["ann_vol"] - strat_np["ann_vol"]) < 1e-6
if strat_m["sharpe"] is None:
    assert strat_np["sharpe"] is None
else:
    assert abs(strat_m["sharpe"] - strat_np["sharpe"]) < 1e-6
assert abs(strat_m["max_dd"] - strat_np["max_dd"]) < 1e-6
assert abs(bench_m["cum_return"] - bench_np["cum_return"]) < 1e-6
assert abs(bench_m["ann_vol"] - bench_np["ann_vol"]) < 1e-6
if bench_m["sharpe"] is None:
    assert bench_np["sharpe"] is None
else:
    assert abs(bench_m["sharpe"] - bench_np["sharpe"]) < 1e-6
assert abs(bench_m["max_dd"] - bench_np["max_dd"]) < 1e-6

gate_return = excess >= 10.0
gate_sharpe = strat_m["sharpe"] is not None and strat_m["sharpe"] >= 1.00
gate_both = gate_return and gate_sharpe

print("Finalist:", json.dumps(F, indent=2))
print("Digests:")
print("  strategies.py:", dig_strat)
print("  evalcore.py:", dig_eval)
manifest_digest = sha256_path(manifest_path)
print("  finalist_manifest.json:", manifest_digest)
print(f"Sealed dates: {evalcore.SEAL_START} to {evalcore.SEAL_END}")
print(f"n_days: {len(px_s)}")
print("Strategy metrics:")
for k, v in strat_m.items():
    print(f"  {k}: {v}")
print("Benchmark metrics:")
for k, v in bench_m.items():
    print(f"  {k}: {v}")
print("Gates:")
print(f"  gate_return: excess={excess:.6f} >= 10.0 ? {'PASS' if gate_return else 'FAIL'}")
print(f"  gate_sharpe: sharpe={strat_m['sharpe'] if strat_m['sharpe'] is not None else 'None'} >= 1.00 ? {'PASS' if gate_sharpe else 'FAIL'}")
print(f"  gate_both: {gate_both}")

# Cost sensitivity
sens_rows = []
scenarios = [
    (5, 90),
    (20, 90),
    (40, 90),
    (10, 150)
]
for cost_bps, fin_bps in scenarios:
    net_sens = evalcore.cost_returns(w_s, rets_s, rf_s, cost_bps=cost_bps, financing_spread_bps=fin_bps)
    bench_sens = evalcore.cost_returns(bw_s, rets_s, rf_s, cost_bps=cost_bps, financing_spread_bps=fin_bps)
    m_sens = evalcore.metrics(net_sens, rf_s, weights=w_s, bench_ret=bench_sens, name=F["name"])
    b_sens = evalcore.metrics(bench_sens, rf_s, weights=bw_s, bench_ret=bench_sens, name="spy_bh")
    excess_sens = evalcore.excess_pp(m_sens["cum_return"], b_sens["cum_return"])
    sharpe_sens = m_sens["sharpe"]
    sens_rows.append({
        "cost_bps": cost_bps,
        "financing_bps": fin_bps,
        "excess": excess_sens,
        "sharpe": sharpe_sens
    })
    print(f"Cost sensitivity: cost_bps={cost_bps}, financing_bps={fin_bps} -> excess={excess_sens:.6f}, sharpe={sharpe_sens}")

print("VERDICT: " + ("COMPLETE" if gate_both else "NOT COMPLETE"))
if not gate_both:
    failed = []
    if not gate_return:
        failed.append(f"gate_return (excess={excess:.6f})")
    if not gate_sharpe:
        failed.append(f"gate_sharpe (sharpe={strat_m['sharpe']})")
    print("Failed gate: " + ", ".join(failed))

def _json_native(o):
    if isinstance(o, (np.bool_,)):
        return bool(o)
    if isinstance(o, (np.integer,)):
        return int(o)
    if isinstance(o, (np.floating,)):
        return float(o)
    raise TypeError(f"unserialisable {type(o)}")

out_data = {
    "graded_utc": datetime.datetime.now(datetime.UTC).isoformat(),
    "finalist_name": F["name"],
    "n_days": int(len(px_s)),
    "seal_start": str(px_s.index[0].date()),
    "seal_end": str(px_s.index[-1].date()),
    "cost_bps": 10.0,
    "financing_spread_bps": 90.0,
    "git_commit": M.get("git_commit"),
    "strategy_metrics": strat_m,
    "benchmark_metrics": bench_m,
    "independent_recomputation": {"strategy": strat_np, "benchmark": bench_np},
    "digests": {
        "strategies.py": dig_strat,
        "evalcore.py": dig_eval,
        "finalist_manifest.json": manifest_digest
    },
    "gates": {
        "return": gate_return,
        "sharpe": gate_sharpe,
        "both": gate_both,
        "excess": excess,
        "sharpe_val": strat_m["sharpe"]
    },
    "cost_sensitivity": sens_rows,
    "caveats": [
        "taxes excluded",
        "market impact beyond flat spread not modelled",
        "universe survivorship-selected",
        "this block was analysed in aggregate by a prior session so it is a contaminated sub-period test, not virgin out-of-sample"
    ]
}
out_path = D / "out" / "SEAL_RESULT.json"
with out_path.open("w") as f:
    json.dump(out_data, f, indent=2, default=_json_native)
print(f"WROTE {out_path}")
print("SEAL GRADED")
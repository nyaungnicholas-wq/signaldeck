import argparse
import datetime
import itertools
import json
import math
import os
import sys
from pathlib import Path

import evalcore
import numpy as np
import pandas as pd
import strategies

def phi(x: float) -> float:
    return 0.5 * (1.0 + math.erf(x / math.sqrt(2.0)))

def phi_inv(p: float) -> float:
    if p <= 0.0 or p >= 1.0:
        raise ValueError("p must be in (0,1)")
    a1 = -39.6968302866538
    a2 = 220.946098424521
    a3 = -275.928510446969
    a4 = 138.357751867269
    a5 = -30.6647980661472
    a6 = 2.50662827745924
    b1 = -54.4760987982241
    b2 = 161.585836858041
    b3 = -155.698979859887
    b4 = 66.8013118877197
    b5 = -13.2806815528857
    c1 = -7.78489400243029e-03
    c2 = -0.322396458041136
    c3 = -2.40075827716184
    c4 = -2.54973253934373
    c5 = 4.37466414146497
    c6 = 2.93816398269878
    d1 = 7.78469570904146e-03
    d2 = 0.322467129070040
    d3 = 2.44513413714300
    d4 = 3.75440866190742
    p_low = 0.02425
    p_high = 1.0 - p_low
    if p < p_low:
        q = math.sqrt(-2.0 * math.log(p))
        return (((((c1 * q + c2) * q + c3) * q + c4) * q + c5) * q + c6) / (
            ((((d1 * q + d2) * q + d3) * q + d4) * q + 1.0)
        )
    if p > p_high:
        q = math.sqrt(-2.0 * math.log(1.0 - p))
        return -(((((c1 * q + c2) * q + c3) * q + c4) * q + c5) * q + c6) / (
            ((((d1 * q + d2) * q + d3) * q + d4) * q + 1.0)
        )
    q = p - 0.5
    r = q * q
    return (((((a1 * r + a2) * r + a3) * r + a4) * r + a5) * r + a6) * q / (
        (((((b1 * r + b2) * r + b3) * r + b4) * r + b5) * r + 1.0)
    )

def compute_dsr(
    excess_returns: pd.Series, all_config_excess: dict[str, pd.Series], grid_size: int = 8
) -> dict:
    r = excess_returns.dropna()
    T = len(r)
    if T < 2:
        return {"error": "insufficient data"}
    mu = r.mean()
    sigma = r.std(ddof=1)
    if sigma == 0:
        return {"error": "zero variance"}
    SR = mu / sigma
    g3 = r.skew()
    m4 = ((r - mu) ** 4).mean()
    g4 = m4 / (sigma**4)
    sr_values = []
    for name, ser in all_config_excess.items():
        s = ser.dropna()
        if len(s) >= 2 and s.std(ddof=1) > 0:
            sr_values.append(s.mean() / s.std(ddof=1))
    sr_std = np.std(sr_values, ddof=1) if len(sr_values) >= 2 else 0.0
    gamma = 0.5772156649
    N = float(grid_size)
    term1 = (1.0 - gamma) * phi_inv(1.0 - 1.0 / N)
    term2 = gamma * phi_inv(1.0 - 1.0 / (N * math.e))
    SR0 = sr_std * (term1 + term2)
    denom = math.sqrt(1.0 - g3 * SR + ((g4 - 1.0) / 4.0) * SR * SR)
    if denom <= 0:
        DSR = 0.0
    else:
        z = (SR - SR0) * math.sqrt(T - 1) / denom
        DSR = phi(z)
    return {
        "SR_annualised": SR * math.sqrt(evalcore.TRADING_DAYS),
        "SR_per_obs": SR,
        "SR0": SR0,
        "DSR": DSR,
        "T": T,
        "g3": g3,
        "g4": g4,
        "sr_std": sr_std,
    }

def compute_pbo(excess_matrix: pd.DataFrame, n_blocks: int = 8) -> dict:
    T, N = excess_matrix.shape
    block_size = T // n_blocks
    if block_size == 0:
        return {"error": "insufficient rows for blocks", "PBO": None, "median_lambda": None, "skipped": 0}
    used_rows = block_size * n_blocks
    mat = excess_matrix.iloc[:used_rows].values
    blocks = [mat[i * block_size : (i + 1) * block_size] for i in range(n_blocks)]
    block_indices = list(range(n_blocks))
    lambdas = []
    skipped = 0
    for in_idx in itertools.combinations(block_indices, n_blocks // 2):
        in_idx = set(in_idx)
        out_idx = [i for i in block_indices if i not in in_idx]
        in_data = np.vstack([blocks[i] for i in in_idx])
        out_data = np.vstack([blocks[i] for i in out_idx])
        in_sharpes = []
        out_sharpes = []
        valid = True
        for j in range(N):
            in_col = in_data[:, j]
            out_col = out_data[:, j]
            in_std = in_col.std(ddof=1)
            out_std = out_col.std(ddof=1)
            if in_std == 0 or out_std == 0 or np.isnan(in_std) or np.isnan(out_std):
                valid = False
                break
            in_sharpes.append(in_col.mean() / in_std)
            out_sharpes.append(out_col.mean() / out_std)
        if not valid:
            skipped += 1
            continue
        n_star = int(np.argmax(in_sharpes))
        # CSCV rank must INCREASE with OOS performance: best gets rank N, so that
        # omega->1 and lambda>0 means the IS-best generalised. Counting strategies
        # ABOVE n_star inverts this and reports good generalisation as overfitting.
        rank = 1 + sum(1 for s in out_sharpes if s < out_sharpes[n_star])
        omega = rank / (N + 1)
        if omega <= 0 or omega >= 1:
            skipped += 1
            continue
        lam = math.log(omega / (1 - omega))
        lambdas.append(lam)
    if not lambdas:
        return {"PBO": None, "median_lambda": None, "skipped": skipped, "total_splits": 70}
    pbo = sum(1 for l in lambdas if l <= 0) / len(lambdas)
    median_lam = float(np.median(lambdas))
    return {"PBO": pbo, "median_lambda": median_lam, "skipped": skipped, "total_splits": 70}

def slice_metrics(net_ret: pd.Series, rf: pd.Series, weights: pd.DataFrame, bench_ret: pd.Series, mask: pd.Series, name: str) -> dict:
    sl_net = net_ret[mask]
    sl_rf = rf[mask]
    sl_w = weights[mask]
    sl_bench = bench_ret[mask]
    m = evalcore.metrics(sl_net, sl_rf, weights=sl_w, bench_ret=sl_bench, name=name)
    excess = evalcore.excess_pp((1 + sl_net).prod() - 1, (1 + sl_bench).prod() - 1)
    m["excess_pp"] = excess
    return m

def main() -> int:
    if evalcore.seal_is_open():
        print("REFUSAL: this script must never run with the seal open", file=sys.stderr)
        return 2

    script_dir = Path(__file__).parent
    out_dir = script_dir / "out"
    out_dir.mkdir(parents=True, exist_ok=True)

    px = evalcore.closes(strategies.SLEEVES, evalcore.DEV_START, evalcore.VAL_END)
    print(f"Joint sample: {px.index[0].date()} to {px.index[-1].date()}, rows={len(px)}")

    rets = px.pct_change().fillna(0.0)
    rf = evalcore.riskfree_daily(px.index)

    bench_w = strategies.weights_spy_bh(px)
    bench_net = evalcore.cost_returns(bench_w, rets, rf)

    dev_mask = px.index <= evalcore.DEV_END
    val_mask = px.index >= evalcore.VAL_START

    configs = []
    for sma in [100, 150, 200, 250]:
        for wgt in ["equal", "invvol"]:
            name = f"dt_sma{sma}_{wgt}"
            configs.append({"name": name, "sma": sma, "weighting": wgt})

    all_trials = []
    all_excess_full = {}
    leverage_map = {}

    for cfg in configs:
        name = cfg["name"]
        sma = cfg["sma"]
        wgt = cfg["weighting"]
        if wgt == "equal":
            w1 = strategies.weights_divtrend(px, sma=sma, leverage=1.0, sleeves=strategies.SLEEVES)
        else:
            w1 = strategies.weights_divtrend_invvol(px, sma=sma, leverage=1.0, sleeves=strategies.SLEEVES)
        net1 = evalcore.cost_returns(w1, rets, rf)
        spy_vol = bench_net.std(ddof=1) * math.sqrt(evalcore.TRADING_DAYS)
        cfg_vol = net1.std(ddof=1) * math.sqrt(evalcore.TRADING_DAYS)
        if cfg_vol == 0:
            L = 1.0
        else:
            L = spy_vol / cfg_vol
        L = max(1.0, min(5.0, round(L, 2)))
        leverage_map[name] = L

        if wgt == "equal":
            wL = strategies.weights_divtrend(px, sma=sma, leverage=L, sleeves=strategies.SLEEVES)
        else:
            wL = strategies.weights_divtrend_invvol(px, sma=sma, leverage=L, sleeves=strategies.SLEEVES)
        netL = evalcore.cost_returns(wL, rets, rf)

        dev_m = slice_metrics(netL, rf, wL, bench_net, dev_mask, name)
        val_m = slice_metrics(netL, rf, wL, bench_net, val_mask, name)
        full_m = slice_metrics(netL, rf, wL, bench_net, pd.Series(True, index=px.index), name)

        excess_full = netL - rf
        all_excess_full[name] = excess_full

        trial = {
            "name": name,
            "sma": sma,
            "weighting": wgt,
            "leverage": L,
            "dev": dev_m,
            "val": val_m,
            "full": full_m,
            "generated_utc": datetime.datetime.now(datetime.UTC).isoformat(),
            "grid_size": 8,
        }
        all_trials.append(trial)

    ledger_path = out_dir / "trials.jsonl"
    with ledger_path.open("a") as f:
        for t in all_trials:
            f.write(json.dumps(t) + "\n")
    print(f"WROTE {ledger_path.absolute()}")

    floors = {
        "dev_sharpe_min": 0.40,
        "val_sharpe_min": 0.40,
        "dev_excess_min": 0.0,
        "val_excess_min": 0.0,
        "max_gross_max": 5.0,
        "min_trades": 30,
    }

    print("\nConfigurations:")
    print(f"{'name':<20} {'L':>4} {'devSharpe':>9} {'devExcess':>9} {'valSharpe':>9} {'valExcess':>9} {'fullSharpe':>9} {'fullMaxDD':>9} {'gross':>6} {'trades':>6} {'eligible'}")
    eligible = []
    for t in all_trials:
        name = t["name"]
        L = t["leverage"]
        dev_s = t["dev"].get("sharpe")
        dev_ex = t["dev"].get("excess_pp", 0)
        val_s = t["val"].get("sharpe")
        val_ex = t["val"].get("excess_pp", 0)
        full_s = t["full"].get("sharpe")
        full_dd = t["full"].get("max_dd", 0)
        full_gross = t["full"].get("max_gross", 0)
        full_trades = t["full"].get("n_trades", 0)

        fails = []
        if dev_s is None or dev_s < floors["dev_sharpe_min"]:
            fails.append("dev_sharpe")
        if val_s is None or val_s < floors["val_sharpe_min"]:
            fails.append("val_sharpe")
        if dev_ex <= floors["dev_excess_min"]:
            fails.append("dev_excess")
        if val_ex <= floors["val_excess_min"]:
            fails.append("val_excess")
        if full_gross > floors["max_gross_max"] + 1e-9:
            fails.append("max_gross")
        if full_trades < floors["min_trades"]:
            fails.append("min_trades")

        is_eligible = len(fails) == 0
        eligible_flag = "YES" if is_eligible else "NO"
        print(f"{name:<20} {L:>4.2f} {dev_s if dev_s is not None else 'None':>9} {dev_ex:>9.2f} {val_s if val_s is not None else 'None':>9} {val_ex:>9.2f} {full_s if full_s is not None else 'None':>9} {full_dd:>9.4f} {full_gross:>6.2f} {full_trades:>6} {eligible_flag}")
        if not is_eligible:
            print(f"  -> FAILS: {', '.join(fails)}")
        else:
            eligible.append(t)

    print("\nEligibility verdict:")
    for t in all_trials:
        name = t["name"]
        dev_s = t["dev"].get("sharpe")
        val_s = t["val"].get("sharpe")
        dev_ex = t["dev"].get("excess_pp", 0)
        val_ex = t["val"].get("excess_pp", 0)
        full_gross = t["full"].get("max_gross", 0)
        full_trades = t["full"].get("n_trades", 0)
        fails = []
        if dev_s is None or dev_s < floors["dev_sharpe_min"]:
            fails.append(f"dev_sharpe ({dev_s})")
        if val_s is None or val_s < floors["val_sharpe_min"]:
            fails.append(f"val_sharpe ({val_s})")
        if dev_ex <= floors["dev_excess_min"]:
            fails.append(f"dev_excess ({dev_ex:.2f})")
        if val_ex <= floors["val_excess_min"]:
            fails.append(f"val_excess ({val_ex:.2f})")
        if full_gross > floors["max_gross_max"] + 1e-9:
            fails.append(f"max_gross ({full_gross:.2f})")
        if full_trades < floors["min_trades"]:
            fails.append(f"min_trades ({full_trades})")
        if fails:
            print(f"  {name}: INELIGIBLE - {', '.join(fails)}")
        else:
            print(f"  {name}: ELIGIBLE")

    finalist = None
    if eligible:
        def rank_key(t):
            dev_s = t["dev"].get("sharpe") or -1e9
            val_s = t["val"].get("sharpe") or -1e9
            min_s = min(dev_s, val_s)
            turnover = t["full"].get("turnover_ann", 1e9)
            return (-min_s, turnover, t["name"])
        eligible.sort(key=rank_key)
        finalist = eligible[0]
        print(f"\nFINALIST: {finalist['name']}")
    else:
        print("\nNO ELIGIBLE CANDIDATE")
        finalist = None

    dsr_block = {}
    pbo_block = {}
    if finalist:
        finalist_excess = all_excess_full[finalist["name"]]
        dsr_block = compute_dsr(finalist_excess, all_excess_full, grid_size=8)
        print("\nDSR:")
        for k, v in dsr_block.items():
            print(f"  {k}: {v}")

        excess_df = pd.DataFrame(all_excess_full)
        pbo_block = compute_pbo(excess_df, n_blocks=8)
        print("\nPBO:")
        for k, v in pbo_block.items():
            print(f"  {k}: {v}")
    else:
        dsr_block = {"finalist": None}
        pbo_block = {"finalist": None}
        print("\nDSR: skipped (no finalist)")
        print("PBO: skipped (no finalist)")

    manifest = {
        "generated_utc": datetime.datetime.now(datetime.UTC).isoformat(),
        "grid_size": 8,
        "grid": configs,
        "leverage_rule": "vol-match to SPY over DEV+VAL: L = ann_vol(SPY) / ann_vol(config@1x), capped [1.0, 5.0], rounded to 2 decimals",
        "selection_rule": "floors: dev_sharpe>=0.4, val_sharpe>=0.4, dev_excess>0, val_excess>0, max_gross<=5.0, n_trades>=30; then rank by min(dev_sharpe, val_sharpe) desc; tiebreak: lower turnover_ann, then name",
        "floors": floors,
        "trials": all_trials,
        "finalist": finalist,
        "dsr": dsr_block,
        "pbo": pbo_block,
        "cost_bps": 10.0,
        "financing_spread_bps": 90.0,
        "dev_window": f"{evalcore.DEV_START} to {evalcore.DEV_END}",
        "val_window": f"{evalcore.VAL_START} to {evalcore.VAL_END}",
        "sealed_window_not_touched": True,
    }

    manifest_path = out_dir / "finalist_manifest.json"
    with manifest_path.open("w") as f:
        json.dump(manifest, f, indent=2, default=str)
    print(f"WROTE {manifest_path.absolute()}")

    print("CANDIDATES OK")
    return 0

if __name__ == "__main__":
    sys.exit(main())
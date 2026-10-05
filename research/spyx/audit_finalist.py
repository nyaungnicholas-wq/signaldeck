import json
import hashlib
import datetime
import os
import sys
import numpy as np
import pandas as pd

# Import the provided modules
import evalcore
import strategies

def main():
    script_dir = os.path.dirname(os.path.abspath(__file__))
    manifest_path = os.path.join(script_dir, "out", "finalist_manifest.json")
    
    try:
        with open(manifest_path, 'r') as f:
            manifest = json.load(f)
    except FileNotFoundError:
        print("Manifest file not found.")
        sys.exit(1)
    
    finalist = manifest.get("finalist")
    if finalist is None:
        print("NO FINALIST TO AUDIT")
        sys.exit(0)
    
    name = finalist["name"]
    sma = finalist["sma"]
    weighting = finalist["weighting"]
    leverage = finalist["leverage"]
    
    if weighting == "equal":
        weight_func = lambda px: strategies.weights_divtrend(px, sma=sma, leverage=leverage, sleeves=strategies.SLEEVES)
    elif weighting == "invvol":
        weight_func = lambda px: strategies.weights_divtrend_invvol(px, sma=sma, vol_win=60, leverage=leverage, sleeves=strategies.SLEEVES)
    else:
        raise ValueError(f"Unknown weighting: {weighting}")
    
    # Setup data
    px = evalcore.closes(strategies.SLEEVES, evalcore.DEV_START, evalcore.VAL_END)
    rets = px.pct_change().fillna(0.0)
    rf = evalcore.riskfree_daily(px.index)
    w = weight_func(px)
    net = evalcore.cost_returns(w, rets, rf)
    bw = strategies.weights_spy_bh(px)
    bench = evalcore.cost_returns(bw, rets, rf)
    
    # Define checks
    def check_seal_still_closed():
        try:
            if evalcore.seal_is_open():
                return {"id": "A1", "name": "seal_still_closed", "passed": False, "detail": "seal_is_open() returned True"}
            # Try to load beyond seal
            evalcore.load("SPY", evalcore.DEV_START, "2026-08-28")
            return {"id": "A1", "name": "seal_still_closed", "passed": False, "detail": "load did not raise PermissionError"}
        except PermissionError:
            return {"id": "A1", "name": "seal_still_closed", "passed": True, "detail": "seal is closed and load beyond seal raises PermissionError"}
        except Exception as e:
            return {"id": "A1", "name": "seal_still_closed", "passed": False, "detail": f"Unexpected error: {e}"}
    
    def check_causality_by_truncation():
        n = len(px)
        start_idx = 400
        if n - start_idx < 25:
            step = 1
        else:
            step = (n - start_idx) // 25
        positions = [start_idx + i * step for i in range(25)]
        positions = [p for p in positions if p < n]
        if len(positions) < 25:
            positions = list(range(start_idx, min(start_idx+25, n)))
        mismatches = 0
        details = []
        for p in positions:
            px_trunc = px.iloc[:p+1]
            w_trunc = weight_func(px_trunc)
            if not w_trunc.index.equals(px_trunc.index):
                mismatches += 1
                details.append(f"pos {p}: index mismatch")
                continue
            w_full_row = w.iloc[p]
            w_trunc_row = w_trunc.iloc[-1]
            if not np.allclose(w_trunc_row, w_full_row, atol=1e-9):
                mismatches += 1
                details.append(f"pos {p}: weight mismatch max diff {np.max(np.abs(w_trunc_row - w_full_row))}")
        passed = mismatches == 0
        detail = f"{len(positions)-mismatches}/{len(positions)} dates matched"
        if not passed:
            detail += "; " + "; ".join(details[:3])
        return {"id": "A2", "name": "causality_by_truncation", "passed": passed, "detail": detail}
    
    def check_perturbation_isolation():
        px2 = px.copy()
        if len(px2) < 5:
            return {"id": "A3", "name": "perturbation_isolation", "passed": False, "detail": "px has less than 5 rows"}
        px2.iloc[-5:] *= 1.5
        w2 = weight_func(px2)
        if not w2.index.equals(w.index):
            return {"id": "A3", "name": "perturbation_isolation", "passed": False, "detail": "index mismatch"}
        diff = np.max(np.abs(w2.iloc[:-5] - w.iloc[:-5]))
        passed = diff <= 1e-12
        return {"id": "A3", "name": "perturbation_isolation", "passed": passed, "detail": f"max diff {diff}"}
    
    def check_accounting_identity():
        asset = (w * rets).sum(axis=1)
        cash_weight = 1 - w.sum(axis=1)
        cash = np.where(cash_weight >= 0, cash_weight * rf, cash_weight * (rf + 90.0/1e4/252))
        turnover = (w - w.shift(1).fillna(0)).abs().sum(axis=1)
        cost = turnover * 10.0 / 1e4
        manual = asset + cash - cost
        diff = np.max(np.abs(manual - net))
        passed = diff <= 1e-12
        return {"id": "A4", "name": "accounting_identity", "passed": passed, "detail": f"max diff {diff}"}
    
    def check_benchmark_alignment():
        idx_equal = net.index.equals(bench.index) and bench.index.equals(px.index)
        dev_len = len(px[px.index <= evalcore.DEV_END])
        val_len = len(px[(px.index >= evalcore.VAL_START) & (px.index <= evalcore.VAL_END)])
        total_len = len(px)
        passed = idx_equal and (dev_len + val_len == total_len)
        detail = f"net.index==bench.index==px.index: {idx_equal}; dev days={dev_len}, val days={val_len}, total={total_len}"
        return {"id": "A5", "name": "benchmark_alignment", "passed": passed, "detail": detail}
    
    def check_exposure_within_frozen_limit():
        gross = w.abs().sum(axis=1)
        max_gross = gross.max()
        min_weight = w.min().min()
        passed = (max_gross <= leverage + 1e-9) and (min_weight >= -1e-12)
        return {"id": "A6", "name": "exposure_within_frozen_limit", "passed": passed, "detail": f"max gross={max_gross}, min weight={min_weight}"}
    
    def check_reproduce_manifest_metrics():
        dev_mask = px.index <= evalcore.DEV_END
        val_mask = (px.index >= evalcore.VAL_START) & (px.index <= evalcore.VAL_END)
        net_dev = net[dev_mask]
        bench_dev = bench[dev_mask]
        rf_dev = rf[dev_mask]
        w_dev = w[dev_mask]
        net_val = net[val_mask]
        bench_val = bench[val_mask]
        rf_val = rf[val_mask]
        w_val = w[val_mask]
        metrics_dev = evalcore.metrics(net_dev, rf_dev, weights=w_dev, bench_ret=bench_dev, name="dev")
        metrics_val = evalcore.metrics(net_val, rf_val, weights=w_val, bench_ret=bench_val, name="val")
        stored_dev = finalist["dev"]
        stored_val = finalist["val"]
        passed = True
        details = []
        for key in ["sharpe", "cum_return", "max_dd"]:
            if key in stored_dev and key in metrics_dev:
                if metrics_dev[key] is None or stored_dev[key] is None:
                    if metrics_dev[key] != stored_dev[key]:
                        passed = False
                        details.append(f"dev {key}: {metrics_dev[key]} vs {stored_dev[key]}")
                elif abs(metrics_dev[key] - stored_dev[key]) > 1e-6:
                    passed = False
                    details.append(f"dev {key}: {metrics_dev[key]} vs {stored_dev[key]}")
            if key in stored_val and key in metrics_val:
                if metrics_val[key] is None or stored_val[key] is None:
                    if metrics_val[key] != stored_val[key]:
                        passed = False
                        details.append(f"val {key}: {metrics_val[key]} vs {stored_val[key]}")
                elif abs(metrics_val[key] - stored_val[key]) > 1e-6:
                    passed = False
                    details.append(f"val {key}: {metrics_val[key]} vs {stored_val[key]}")
        detail = "; ".join(details) if details else "All metrics match within 1e-6"
        return {"id": "A7", "name": "reproduce_manifest_metrics", "passed": passed, "detail": detail}
    
    def check_cost_monotonicity():
        cost_bps_list = [0, 10, 40, 100]
        cum_rets = []
        for cb in cost_bps_list:
            net_cost = evalcore.cost_returns(w, rets, rf, cost_bps=cb)
            cum_rets.append((1 + net_cost).prod() - 1)
        passed = all(cum_rets[i] > cum_rets[i+1] for i in range(len(cum_rets)-1))
        detail = ", ".join([f"{cb}bps: {cr:.6f}" for cb, cr in zip(cost_bps_list, cum_rets)])
        return {"id": "A8", "name": "cost_monotonicity", "passed": passed, "detail": detail}
    
    def check_financing_monotonicity():
        finance_bps_list = [0, 90, 300]
        cum_rets = []
        for fb in finance_bps_list:
            net_finance = evalcore.cost_returns(w, rets, rf, cost_bps=10.0, financing_spread_bps=fb)
            cum_rets.append((1 + net_finance).prod() - 1)
        passed = all(cum_rets[i] > cum_rets[i+1] for i in range(len(cum_rets)-1))
        detail = ", ".join([f"{fb}bps: {cr:.6f}" for fb, cr in zip(finance_bps_list, cum_rets)])
        return {"id": "A9", "name": "financing_monotonicity", "passed": passed, "detail": detail}
    
    def check_not_a_single_asset():
        best_single_sharpe = -np.inf
        for s in strategies.SLEEVES:
            w_single = weight_func(px)
            w_single[:] = 0
            w_single[s] = 1.0 * leverage  # leverage applied to single asset
            net_single = evalcore.cost_returns(w_single, rets, rf)
            metrics_single = evalcore.metrics(net_single, rf, name=s)
            sharpe = metrics_single.get("sharpe")
            if sharpe is not None and sharpe > best_single_sharpe:
                best_single_sharpe = sharpe
        metrics_full = evalcore.metrics(net, rf, weights=w, bench_ret=bench, name="full")
        full_sharpe = metrics_full.get("sharpe")
        passed = full_sharpe is not None and best_single_sharpe is not None and full_sharpe > best_single_sharpe
        detail = f"best single: {best_single_sharpe:.6f}, full: {full_sharpe:.6f}"
        return {"id": "A10", "name": "not_a_single_asset", "passed": passed, "detail": detail}
    
    def check_trade_count_sufficiency():
        w_diff = (w - w.shift(1).fillna(0)).abs().sum(axis=1)
        trade_days = (w_diff > 1e-9).sum()
        rebalance_months = w_diff.resample('ME').sum().gt(1e-9).sum()
        passed = trade_days >= 30
        return {"id": "A11", "name": "trade_count_sufficiency", "passed": passed, "detail": f"trade days={trade_days}, rebalance months={rebalance_months}"}
    
    checks = [
        check_seal_still_closed,
        check_causality_by_truncation,
        check_perturbation_isolation,
        check_accounting_identity,
        check_benchmark_alignment,
        check_exposure_within_frozen_limit,
        check_reproduce_manifest_metrics,
        check_cost_monotonicity,
        check_financing_monotonicity,
        check_not_a_single_asset,
        check_trade_count_sufficiency
    ]
    
    results = []
    for check in checks:
        try:
            res = check()
            results.append(res)
        except Exception as e:
            results.append({
                "id": check.__name__.replace("check_", "").upper(),
                "name": check.__name__.replace("check_", "").replace("_", " ").title(),
                "passed": False,
                "detail": f"Exception: {e}"
            })
    
    passed_count = sum(1 for r in results if r["passed"])
    failed_count = len(results) - passed_count
    
    print(f"Auditing finalist: {name} (sma={sma}, weighting={weighting}, leverage={leverage})")
    for r in results:
        status = "[PASS]" if r["passed"] else "[FAIL]"
        print(f"{status} {r['id']} {r['name']}: {r['detail']}")
    print(f"AUDIT passed={passed_count} failed={failed_count}")
    
    # Prepare audit.json
    strategies_path = os.path.join(script_dir, "strategies.py")
    evalcore_path = os.path.join(script_dir, "evalcore.py")
    with open(strategies_path, 'rb') as f:
        strategies_digest = hashlib.sha256(f.read()).hexdigest()
    with open(evalcore_path, 'rb') as f:
        evalcore_digest = hashlib.sha256(f.read()).hexdigest()
    
    audit_data = {
        "finalist": finalist,
        "checks": results,
        "strategies_sha256": strategies_digest,
        "evalcore_sha256": evalcore_digest,
        "generated_utc": datetime.datetime.now(datetime.UTC).isoformat()
    }
    
    out_dir = os.path.join(script_dir, "out")
    os.makedirs(out_dir, exist_ok=True)
    audit_path = os.path.join(out_dir, "audit.json")
    with open(audit_path, 'w') as f:
        json.dump(audit_data, f, indent=2, default=str)
    
    print(f"WROTE {os.path.abspath(audit_path)}")
    if failed_count == 0:
        print("AUDIT CLEAN")
    else:
        print("AUDIT FAILED")
    print("AUDIT DONE")

if __name__ == "__main__":
    main()
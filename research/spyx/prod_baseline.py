import argparse
import json
import pathlib
import subprocess
import sys
import pandas as pd
import evalcore

TRADER_DIR = pathlib.Path(r"C:\Users\Nicholas_N\Desktop\claude code\stock-trader")
TRADER_PY = TRADER_DIR / ".venv" / "Scripts" / "python.exe"

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--window", choices=["dev", "val", "devval", "seal"], default="dev")
    parser.add_argument("--out", type=pathlib.Path, default=None)
    args = parser.parse_args()

    if args.window == "dev":
        start, end = evalcore.DEV_START, evalcore.DEV_END
    elif args.window == "val":
        start, end = evalcore.VAL_START, evalcore.VAL_END
    elif args.window == "devval":
        start, end = evalcore.DEV_START, evalcore.VAL_END
    else:  # seal
        if not evalcore.seal_is_open():
            print("SEAL window is closed; set SPYX_SEAL=OPEN to grade it", file=sys.stderr)
            sys.exit(2)
        start, end = evalcore.SEAL_START, evalcore.SEAL_END

    program = f"""
import json
from trader.config import Config
from trader.rotation import apply_daily_champion, run_rotation_backtest
cfg = Config()
apply_daily_champion(cfg)
cfg.backtest_start = "{start}"
cfg.backtest_end   = "{end}"
result = run_rotation_backtest(cfg, verbose=False)
output = {{
    "equity_curve": [[str(d), float(e)] for d, e in result["broker"].equity_curve],
    "keys": sorted(result.keys())
}}
print("@@CURVE@@" + json.dumps(output))
"""

    completed = subprocess.run(
        [str(TRADER_PY), "-c", program],
        cwd=str(TRADER_DIR),
        capture_output=True,
        text=True,
        timeout=3600
    )
    if completed.returncode != 0:
        raise RuntimeError(f"Trader subprocess failed (stderr):\\n{completed.stderr[-2000:]}")
    marker = "@@CURVE@@"
    lines = completed.stdout.splitlines()
    curve_line = None
    for line in lines:
        if line.startswith(marker):
            curve_line = line[len(marker):]
            break
    if curve_line is None:
        raise RuntimeError(f"Marker not found in stdout:\\n{completed.stdout[-2000:]}")
    data = json.loads(curve_line)
    curve = data["equity_curve"]
    dates = [pd.Timestamp(d) for d, _ in curve]
    equity = [e for _, e in curve]
    series = pd.Series(equity, index=pd.DatetimeIndex(dates, name="date"))
    assert series.index.is_monotonic_increasing, "Equity curve dates not increasing"
    assert len(series) > 100, f"Insufficient points: {len(series)}"
    assert (series > 0).all(), "Non-positive equity encountered"
    rets = series.pct_change().dropna()
    mask = (rets.index >= start) & (rets.index <= end)
    rets = rets.loc[mask]
    first_date, last_date = rets.index[0], rets.index[-1]
    rf = evalcore.riskfree_daily(rets.index)
    spy_px = evalcore.closes(["SPY"], first_date.strftime("%Y-%m-%d"), last_date.strftime("%Y-%m-%d"))
    spy_px = spy_px.reindex(rets.index)
    missing = spy_px.isna().any(axis=1)
    n_missing = missing.sum()
    if n_missing > 0:
        print(f"Dropped {n_missing} dates missing from SPY data", file=sys.stderr)
        keep = ~missing
        rets = rets[keep]
        spy_px = spy_px[keep]
        rf = rf[keep]
    spy_w = pd.DataFrame(1.0, index=rets.index, columns=["SPY"])
    spy_rets = spy_px.pct_change().fillna(0.0)
    bench_ret = evalcore.cost_returns(
        spy_w,
        spy_rets,
        rf,
        cost_bps=10.0,
        financing_spread_bps=90.0
    )
    strat_m = evalcore.metrics(rets, rf, weights=None, bench_ret=bench_ret, name="prod_rotation")
    bench_m = evalcore.metrics(bench_ret, rf, weights=spy_w, bench_ret=bench_ret, name="spy_bh")
    excess = evalcore.excess_pp(strat_m["cum_return"], bench_m["cum_return"])
    gate_excess = excess >= 10.0
    gate_sharpe = strat_m["sharpe"] is not None and strat_m["sharpe"] >= 1.00
    caveats = [
        "Strategy returns include engine's internal cost model; benchmark returns use protocol cost model (10 bps transaction + 90 bps financing spread)."
    ]
    result_json = {
        "window": args.window,
        "dates_used": [start, end],
        "n_days": int(len(rets)),
        "strategy_metrics": strat_m,
        "benchmark_metrics": bench_m,
        "excess_pp": float(excess),
        "gate_excess": bool(gate_excess),
        "gate_sharpe": bool(gate_sharpe),
        "caveats": caveats
    }
    out_path = args.out
    if out_path is None:
        out_path = pathlib.Path(__file__).parent / "out" / f"prod_{args.window}.json"
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with open(out_path, "w") as f:
        json.dump(result_json, f, indent=2, default=str)
    print(f"WROTE {out_path.resolve()}")
    print("PROD OK")

if __name__ == "__main__":
    main()
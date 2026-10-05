#!/usr/bin/env python3
"""Parity fixture for the Go live grader (daemon/internal/rvgrade).

Generates deterministic forecast rows and the statistics that the REFERENCE
implementation, tools/rv_forecast_backtest.py, computes on them. Go's
TestParityWithPythonReference must match those statistics. The reference module
is imported, never edited.

    .venv/Scripts/python.exe tools/rv_grader_parity.py --write   # regenerate testdata
    .venv/Scripts/python.exe tools/rv_grader_parity.py --check   # testdata is this output
"""
import argparse
import importlib.util
import json
import math
import os
import random
import statistics
import sys
from datetime import datetime, timezone, timedelta

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("rvbt", os.path.join(HERE, "rv_forecast_backtest.py"))
bt = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bt)

def r6(x):
    return float(f"{x:.6e}")

def generate():
    rng = random.Random(20261002)
    start = datetime(2026, 10, 5)
    timestamps = []
    d = start
    while len(timestamps) < 64:
        if d.weekday() < 5:
            timestamps.append(int(datetime(d.year, d.month, d.day, 20, 0, tzinfo=timezone.utc).timestamp()))
        d += timedelta(days=1)
    symbols = [f"S{i:02d}" for i in range(40)]  # >= 30 with RV^CC every day, so the floor is a no-op
    rows = []
    for ts in timestamps:
        m = math.exp(rng.gauss(0, 0.5))
        for sym in symbols:
            v = math.exp(rng.gauss(-8.5, 0.6)) * m
            har = r6(v * math.exp(rng.gauss(0, 0.3)))
            ewma = r6(v * math.exp(rng.gauss(0.05, 0.45)))
            rw = r6(v * math.exp(rng.gauss(0, 0.6)))
            gk = r6(v * math.exp(rng.gauss(-0.1, 0.5)))
            u = rng.random()
            if u < 0.03:
                cc = 0.0
            elif u < 0.05:
                cc = None
            else:
                cc = r6(v * rng.expovariate(1.0))
            rows.append({"sym": sym, "ts": ts, "har": har, "rw": rw, "ewma": ewma, "gk": gk, "cc": cc})
    return rows

def generate_closes():
    rngb = random.Random(7)
    def make_series():
        close = 100.0
        series = [r6(close)]
        for _ in range(13):
            close = r6(close * math.exp(rngb.gauss(0, 0.02)))
            series.append(close)
        return series
    A = make_series()
    B = make_series()
    B[5] = 0.0
    C = make_series()
    C[8] = -1.0
    return {"A": A, "B": B, "C": C}

def expected(rows, closes):
    out = {"horizons": {}, "cc": {}}
    for h in (1, 5):
        by_sym = {}
        for row in rows:
            sym = row["sym"]
            by_sym.setdefault(sym, []).append({
                "ts": row["ts"],
                "actual": row["gk"],
                "har": row["har"],
                "rw": row["rw"],
                "ewma": row["ewma"],
                "flat": row["rw"]
            })
        agg = bt.aggregate(list(by_sym.values()), h)
        def pick(prefix):
            for e in agg["dm"]:
                if e["label"].startswith(prefix):
                    return {"mean": e["mean"], "t": e["t"], "n": e["n"], "lag": e["lag"]}
            raise ValueError(f"No dm entry for prefix {prefix}")
        headline = pick("QLIKE: HAR vs EWMA(0.94)")
        qlike_rw = pick("QLIKE: HAR vs random walk")
        mse_ewma = pick("MSE:   HAR vs EWMA(0.94)")
        per_day = {}
        zero = 0
        for row in rows:
            a = row["cc"]
            if a is None:
                continue
            if a <= 0:
                zero += 1
                continue
            d = bt.day_key(row["ts"])
            slot = per_day.setdefault(d, {"har": [], "ewma": []})
            slot["har"].append((a - row["har"]) ** 2)
            slot["ewma"].append((a - row["ewma"]) ** 2)
        days = sorted(d for d, v in per_day.items() if v["har"] and v["ewma"])
        har = [statistics.fmean(per_day[d]["har"]) for d in days]
        ew = [statistics.fmean(per_day[d]["ewma"]) for d in days]
        diff = [x - y for x, y in zip(har, ew)]
        lag = max(bt.newey_west_lag(len(days)), h)
        r = bt.diebold_mariano(diff, lag)
        control = {"mean": r["mean"], "t": r["t"], "n": r["n"], "lag": r["lag"], "zero": zero}
        out["horizons"][str(h)] = {"headline": headline, "qlike_rw": qlike_rw, "mse_ewma": mse_ewma, "control": control}
    for name, cl in closes.items():
        bars = [(i, 0, 0, 0, c) for i, c in enumerate(cl)]
        cc = bt.cc_series(bars)
        targets = {}
        for h in (1, 5):
            lst = []
            for t in range(len(cl)):
                if t + h >= len(cc):
                    lst.append(None)
                    continue
                w = cc[t+1:t+1+h]
                lst.append(None if any(v is None for v in w) else sum(w) / h)
            targets[str(h)] = lst
        out["cc"][name] = {"closes": cl, "targets": targets}
    return out

def main():
    parser = argparse.ArgumentParser(
        description="Regenerate the Go grader's parity fixture from the reference implementation. "
                    "Run with --write to create fixture files, or --check (default) to verify them."
    )
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--write", action="store_true", help="Write fixture files")
    group.add_argument("--check", action="store_true", help="Check fixture files")
    args = parser.parse_args()
    if not args.write and not args.check:
        args.check = True
    rows = generate()
    closes = generate_closes()
    rows_json = json.dumps({"rows": rows}, indent=None, separators=(",", ":"), sort_keys=True) + "\n"
    expected_json = json.dumps(expected(rows, closes), indent=None, separators=(",", ":"), sort_keys=True) + "\n"
    fix_path = os.path.join(HERE, "..", "daemon", "internal", "rvgrade", "testdata", "parity_rows.json")
    exp_path = os.path.join(HERE, "..", "daemon", "internal", "rvgrade", "testdata", "parity_expected.json")
    if args.write:
        os.makedirs(os.path.dirname(fix_path), exist_ok=True)
        with open(fix_path, "w", encoding="utf-8", newline="\n") as f:
            f.write(rows_json)
        with open(exp_path, "w", encoding="utf-8", newline="\n") as f:
            f.write(expected_json)
        print(f"wrote {fix_path} and {exp_path}")
        return 0
    stale = None
    try:
        with open(fix_path, "rb") as f:
            if f.read() != rows_json.encode("utf-8"):
                stale = fix_path
    except FileNotFoundError:
        stale = fix_path
    if stale is None:
        try:
            with open(exp_path, "rb") as f:
                if f.read() != expected_json.encode("utf-8"):
                    stale = exp_path
        except FileNotFoundError:
            stale = exp_path
    if stale is not None:
        print(f"PARITY FIXTURE STALE: {stale}", file=sys.stderr)
        return 1
    print("PARITY FIXTURE OK")
    return 0

if __name__ == "__main__":
    sys.exit(main())
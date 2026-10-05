"""Phase-1 baseline reproduction probe: prior constructions under the corrected accounting.

Reports every construction over DEV and VAL separately so the question that actually matters
-- is Sharpe >= 1.00 reachable over a multi-year window at all -- gets a measured answer
instead of an assumption. Uses only the two green modules; writes nothing to the repo.
"""
import sys, pathlib, os
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pandas as pd
import evalcore as ec
import strategies as st

WINDOWS = {
    "DEV  1993-2015": (ec.DEV_START, ec.DEV_END),
    "VAL  2016-2020": (ec.VAL_START, ec.VAL_END),
}

hdr = f"{'window':16} {'name':20} {'n':>5} {'cum%':>9} {'cagr%':>7} {'vol%':>6} {'sharpe':>7} {'maxdd%':>8} {'gross':>6} {'excess_pp':>10} {'gate':>5}"

for wname, (start, end) in WINDOWS.items():
    px = ec.closes(st.SLEEVES, start, end)
    rets = px.pct_change().fillna(0.0)
    rf = ec.riskfree_daily(px.index)
    bw = st.REGISTRY["spy_bh"](px)
    bench = ec.cost_returns(bw, rets, rf)
    bm = ec.metrics(bench, rf, weights=bw, bench_ret=bench, name="spy_bh")
    print()
    print(f"{wname}   instruments={list(px.columns)}")
    print(f"{'':16} joint sample {px.index[0].date()} .. {px.index[-1].date()}  rows={len(px)}")
    print(hdr)
    rows = []
    for name, fn in st.REGISTRY.items():
        w = fn(px)
        nr = ec.cost_returns(w, rets, rf)
        m = ec.metrics(nr, rf, weights=w, bench_ret=bench, name=name)
        xs = ec.excess_pp(m["cum_return"], bm["cum_return"])
        gate = ("R" if xs >= 10.0 else "-") + ("S" if (m["sharpe"] or 0.0) >= 1.00 else "-")
        rows.append((name, m, xs, gate))
    for name, m, xs, gate in rows:
        print(f"{'':16} {name:20} {m['n_days']:5d} {m['cum_return']*100:9.2f} {m['cagr']*100:7.2f} "
              f"{m['ann_vol']*100:6.2f} {(m['sharpe'] if m['sharpe'] is not None else 0.0):7.2f} {m['max_dd']*100:8.2f} "
              f"{(m['avg_gross'] or 0):6.2f} {xs:10.2f} {gate:>5}")

print()
print("PROBE OK")

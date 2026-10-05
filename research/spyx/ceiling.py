"""Upper bound on achievable Sharpe: the hindsight-optimal long-only static allocation.

No causal strategy can beat a portfolio whose weights were optimised on the very data it is
measured over. So this is a CEILING, not a strategy. If the ceiling sits below 1.00, the
Sharpe gate is unreachable in this instrument set and no amount of further rule search can
change that.

Development data only. Refuses to run with the seal open.
"""
import os
import sys
import json
import datetime
import pathlib
import numpy as np
import pandas as pd

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import evalcore as ec
import strategies as st

D = pathlib.Path(__file__).parent

SECTORS = ["XLK", "XLF", "XLE", "XLV", "XLI", "XLP", "XLU", "XLY", "XLB"]
EXTRA = ["EEM", "SHY", "LQD", "HYG", "MTUM", "QUAL", "USMV", "VTV", "VUG"]


def _dedupe(seq: list) -> list:
    out = []
    for s in seq:
        if s not in out:
            out.append(s)
    return out


UNIVERSES = {
    "sleeves9": st.SLEEVES,
    "sectors": _dedupe(["SPY"] + SECTORS),
    "broad25": _dedupe(st.SLEEVES + SECTORS + EXTRA),
}


def max_sharpe_longonly(R: np.ndarray, rf: np.ndarray, iters: int = 3000, lr: float = 0.05):
    """Projected gradient ascent on the Sharpe of a long-only, sum-to-one static allocation."""
    n = R.shape[1]
    w = np.ones(n) / n
    best_w, best_s = w.copy(), -np.inf
    for _ in range(iters):
        e = R @ w - rf
        mu, sd = e.mean(), e.std(ddof=1)
        if sd <= 0 or not np.isfinite(sd):
            break
        s = mu / sd
        if s > best_s:
            best_s, best_w = s, w.copy()
        # dS/dw_i = mean(R_i)/sd - mu * cov(e, R_i) / sd**3
        cov = ((R - R.mean(axis=0)) * (e - mu)[:, None]).sum(axis=0) / (len(e) - 1)
        g = R.mean(axis=0) / sd - mu * cov / sd**3
        gn = np.linalg.norm(g)
        if gn == 0 or not np.isfinite(gn):
            break
        w = w + lr * g / gn
        w = np.clip(w, 0.0, None)
        tot = w.sum()
        if tot <= 0:
            break
        w = w / tot
    return best_w, best_s * np.sqrt(ec.TRADING_DAYS)


import argparse


def run() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--window", choices=["dev", "seal"], default="dev")
    args = ap.parse_args()
    if args.window == "seal":
        # POST-MORTEM ONLY. The seal is already spent (graded 2026-08-31). This selects no
        # strategy and grades nothing -- it characterises what the period rewarded, to test
        # whether the "bond bull reversed" explanation for the failure is actually correct.
        if not ec.seal_is_open():
            print("REFUSAL: --window seal needs SPYX_SEAL=OPEN")
            sys.exit(2)
        W_START, W_END = ec.SEAL_START, ec.SEAL_END
    else:
        W_START, W_END = ec.DEV_START, ec.VAL_END
    if args.window == "dev" and ec.seal_is_open():
        print("REFUSAL: ceiling.py is development-only and the seal is OPEN (SPYX_SEAL). Exiting.")
        sys.exit(2)

    rows = []
    for uname, tickers in UNIVERSES.items():
        try:
            px = ec.closes(tickers, W_START, W_END)
        except Exception as exc:  # one retry, dropping the offending ticker
            print(f"  {uname}: load failed ({exc}); retrying without the failing ticker")
            keep = []
            for t in tickers:
                try:
                    ec.load(t, W_START, W_END)
                    keep.append(t)
                except Exception:
                    print(f"    dropped {t}")
            if len(keep) < 3:
                print(f"  {uname}: SKIPPED, too few instruments survived")
                continue
            px = ec.closes(keep, W_START, W_END)

        rets = px.pct_change().fillna(0.0)
        rf = ec.riskfree_daily(px.index)
        bw = st.weights_spy_bh(px)
        bench = ec.cost_returns(bw, rets, rf)
        bm = ec.metrics(bench, rf, weights=bw, bench_ret=bench, name="spy_bh")

        # equal weight, costed
        ew = pd.DataFrame(1.0 / px.shape[1], index=px.index, columns=px.columns)
        ew_net = ec.cost_returns(ew, rets, rf)
        ew_m = ec.metrics(ew_net, rf, weights=ew, bench_ret=bench, name="equal_weight")

        # the ceiling
        w_opt, s_gross = max_sharpe_longonly(rets.values, rf.values)
        wf = pd.DataFrame(np.tile(w_opt, (len(px), 1)), index=px.index, columns=px.columns)
        opt_net = ec.cost_returns(wf, rets, rf)
        opt_m = ec.metrics(opt_net, rf, weights=wf, bench_ret=bench, name="max_sharpe_hindsight")

        # same weights, levered to SPY's realised vol
        L = float(bm["ann_vol"] / opt_m["ann_vol"]) if opt_m["ann_vol"] > 0 else 1.0
        wl = wf * L
        lev_net = ec.cost_returns(wl, rets, rf)
        lev_m = ec.metrics(lev_net, rf, weights=wl, bench_ret=bench, name="max_sharpe_levered")
        lev_excess = ec.excess_pp(lev_m["cum_return"], bm["cum_return"])

        top = sorted(zip(px.columns, w_opt), key=lambda kv: -kv[1])[:5]
        rows.append({
            "universe": uname,
            "n_instruments": int(px.shape[1]),
            "first_date": str(px.index[0].date()),
            "rows": int(len(px)),
            "spy_sharpe": bm["sharpe"],
            "equal_weight_sharpe": ew_m["sharpe"],
            "ceiling_sharpe_gross": round(float(s_gross), 6),
            "ceiling_sharpe_costed": opt_m["sharpe"],
            "ceiling_levered_sharpe": lev_m["sharpe"],
            "ceiling_levered_excess_pp": lev_excess,
            "leverage_for_vol_match": round(L, 3),
            "top_weights": [[t, round(float(v), 4)] for t, v in top],
        })
        print(f"\n{uname}: {px.shape[1]} instruments, {px.index[0].date()} .. {px.index[-1].date()}, {len(px)} rows")
        print(f"  SPY Sharpe            {bm['sharpe']}")
        print(f"  equal-weight Sharpe   {ew_m['sharpe']}")
        print(f"  CEILING gross Sharpe  {s_gross:.6f}")
        print(f"  CEILING costed Sharpe {opt_m['sharpe']}")
        print(f"  levered to SPY vol    Sharpe {lev_m['sharpe']}, excess {lev_excess:.2f} pp, L={L:.3f}")
        print(f"  top weights           {top}")

    if not rows:
        print("NO UNIVERSE EVALUATED")
        sys.exit(1)

    best = max(rows, key=lambda r: (r["ceiling_sharpe_costed"] or -9e9))
    print("\n" + "=" * 72)
    print(f"CEILING {best['ceiling_sharpe_costed']} ON {best['universe']}")
    print("This is the best Sharpe obtainable by a long-only static allocation chosen WITH")
    print("HINDSIGHT on this same data. Any causal strategy must score below it.")
    gate = 1.00
    if (best["ceiling_sharpe_costed"] or 0) < gate:
        print(f"\nThe ceiling is BELOW the {gate:.2f} gate. No causal long-only allocation over")
        print("these instruments can reach the Sharpe target, with or without further search.")
    else:
        print(f"\nThe ceiling EXCEEDS the {gate:.2f} gate, so the target is not arithmetically")
        print("impossible here -- but it required perfect foresight of the weights.")

    out = {
        "generated_utc": datetime.datetime.now(datetime.UTC).isoformat(),
        "window": [W_START, W_END],
        "note": "hindsight-optimal long-only static allocation; an upper bound, not a strategy",
        "gate_sharpe": gate,
        "results": rows,
        "best": best,
    }
    (D / "out").mkdir(parents=True, exist_ok=True)
    p = D / "out" / f"ceiling_{args.window}.json"
    with p.open("w") as f:
        json.dump(out, f, indent=2)
    print(f"WROTE {p}")
    print("CEILING OK")


if __name__ == "__main__":
    run()

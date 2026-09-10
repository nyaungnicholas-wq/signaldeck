"""Structural research dataset (manifest forecastplan-exp-v1).

One row per (origin day, symbol) with: the frozen resolver labels and persistence nulls
(structlabels.py, parity-checked against the Go port), the incumbent's calls and convictions
(PredictTrend / PredictLiquidity / PredictVol21 ported from structregime.go), bar-only
features, the manifest's eligibility screen, and cross-sectional / market columns.
Reads research/dirfix/panel.parquet only; writes under --out-dir.
"""
import argparse
import hashlib
import json
import math
import os
import sys
import tempfile
import time
from datetime import datetime, timezone

import numpy as np
import pandas as pd

THIS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, THIS)
import structlabels as SL  # noqa: E402

L = SL.labels
WINDOW, MINHIST, LAMBDA, MAXSANE = 200, 260, 0.94, 0.65
KINDS = ("trend21", "liquidity21", "vol21")


def frac(win, v):
    """structregime.frac: share of finite window values below v, ties count half; 0.5 if empty."""
    fin = win[np.isfinite(win)]
    if fin.size == 0 or not np.isfinite(v):
        return 0.5
    return (np.count_nonzero(fin < v) + 0.5 * np.count_nonzero(fin == v)) / fin.size


def build_symbol(sym, g):
    closes = g["close"].to_numpy(float)
    vols = g["volume"].to_numpy(float)
    n = len(closes)
    if n < 282:
        return None
    cl, vl = closes.tolist(), vols.tolist()
    rets = SL.bar_returns2(cl)                      # rets[k] belongs to bar k+1
    lab = {
        "trend21": SL.trend_actual(cl, 21), "naive_trend21": SL.naive_trend(cl),
        "liquidity21": SL.liquidity_actual(cl, vl), "naive_liquidity21": SL.naive_liquidity(cl, vl),
    }
    va, nv = SL.vol21_actual(rets.tolist()), SL.naive_vol21(rets.tolist())
    sma = np.array(L.roll_mean(cl, WINDOW))
    with np.errstate(invalid="ignore", divide="ignore"):
        d = np.where(sma > 0, closes / sma - 1, np.nan)
        prod = closes * vols
        dv = np.where(prod > 0, np.log(np.where(prod > 0, prod, 1.0)), np.nan)
    absd = np.abs(d)
    m = np.array(L.roll_mean_nan(dv.tolist(), 21))
    ev = np.empty(n - 1)
    v = 0.0
    for k, r in enumerate(rets):                     # structregime.ewmaVol
        v = LAMBDA * v + (1 - LAMBDA) * r * r
        ev[k] = math.sqrt(v)
    wild = np.abs(rets) > MAXSANE
    lr = pd.Series(np.diff(np.log(closes)))
    vol21 = lr.rolling(21).std(ddof=1).to_numpy()   # index k -> bar k+1
    vol63 = lr.rolling(63).std(ddof=1).to_numpy()
    dv21 = pd.Series(prod).rolling(21).mean().to_numpy()
    dd = pd.Series(d[21:] - d[:-21])                 # change from j to j+21, index j
    sigma = dd.rolling(WINDOW, min_periods=30).std(ddof=1).to_numpy()
    rows = []
    for i in range(MINHIST - 1, n):
        wild_i = bool(wild[max(0, i - MINHIST):i].any())
        cur = d[i]
        call_t = conv_t = None
        if np.isfinite(cur) and cur != 0 and not wild_i:
            call_t = "uptrend" if cur > 0 else "downtrend"
            conv_t = frac(absd[max(0, i - WINDOW):i], abs(cur))
        call_l = conv_l = rank_l = None
        if np.isfinite(m[i]):
            rank_l = frac(m[max(0, i - WINDOW):i], m[i])
            conv_l = abs(rank_l - 0.5) * 2
            call_l = "active" if rank_l > 0.5 else "quiet"
        call_v = conv_v = rank_v = None
        if i >= 230 and not wild_i:
            rank_v = frac(ev[max(0, i - 1 - WINDOW):i - 1], ev[i - 1])
            conv_v = abs(rank_v - 0.5) * 2
            call_v = "elevated" if rank_v > 0.5 else "calm"
        sig = sigma[i - 21] if i - 21 < len(sigma) and i - 21 >= 0 else np.nan
        rows.append({
            "day": g["day"].iloc[i], "symbol_id": sym,
            "trend21": lab["trend21"][i], "naive_trend21": lab["naive_trend21"][i],
            "liquidity21": lab["liquidity21"][i], "naive_liquidity21": lab["naive_liquidity21"][i],
            "vol21": va[i - 1], "naive_vol21": nv[i - 1],
            "call_trend21": call_t, "conv_trend21": conv_t, "dist_trend": cur,
            "call_liquidity21": call_l, "conv_liquidity21": conv_l, "rank_liquidity": rank_l,
            "call_vol21": call_v, "conv_vol21": conv_v, "rank_vol": rank_v,
            "z_trend": cur / sig if np.isfinite(sig) and sig > 0 else np.nan,
            "sma_slope5": sma[i] / sma[i - 5] - 1 if sma[i - 5] > 0 else np.nan,
            "vol_21": vol21[i - 1], "vol_63": vol63[i - 1],
            "vol_ratio_21_63": vol21[i - 1] / vol63[i - 1] if vol63[i - 1] > 0 else np.nan,
            "mom_21": closes[i] / closes[i - 21] - 1, "mom_63": closes[i] / closes[i - 63] - 1,
            "dollar_vol_21": dv21[i], "ret_1": rets[i - 1], "close": closes[i],
        })
    return pd.DataFrame(rows)


def build(panel_path, out_dir, limit_symbols=0):
    t0 = time.time()
    os.makedirs(out_dir, exist_ok=True)
    panel = pd.read_parquet(panel_path, columns=["symbol_id", "day", "close", "volume"])
    panel = panel[panel["close"] > 0].sort_values(["symbol_id", "day"])
    syms = sorted(panel["symbol_id"].unique())
    if limit_symbols:
        syms = syms[:limit_symbols]
    parts = []
    for sym in syms:
        part = build_symbol(sym, panel[panel["symbol_id"] == sym].reset_index(drop=True))
        if part is not None:
            parts.append(part)
    df = pd.concat(parts, ignore_index=True)
    df["cs_rank_vol_21"] = df.groupby("day")["vol_21"].rank(pct=True)
    mkt = df.groupby("day")["ret_1"].agg(mkt_ret="mean", mkt_dispersion="std").sort_index()
    mkt["mkt_mean_ret_21"] = mkt["mkt_ret"].rolling(21).mean()
    df = df.merge(mkt[["mkt_mean_ret_21", "mkt_dispersion"]], left_on="day", right_index=True, how="left")
    df["eligible"] = (df["close"] >= 5.0) & (df["dollar_vol_21"] >= 1e7)
    df.to_parquet(os.path.join(out_dir, "structural_dataset_v1.parquet"), index=False)
    summary = {"rows": int(len(df)), "eligible_rows": int(df["eligible"].sum()),
               "days": int(df["day"].nunique()), "symbols": int(df["symbol_id"].nunique()),
               "first_origin": str(df["day"].min().date()), "last_origin": str(df["day"].max().date()),
               "kinds": {}, "manifest_id": "forecastplan-exp-v1",
               "generated_at_utc": datetime.now(timezone.utc).isoformat(), "duration_s": round(time.time() - t0, 1)}
    with open(panel_path, "rb") as fh:
        summary["panel_sha256"] = hashlib.sha256(fh.read()).hexdigest()
    for scope, sub in (("all", df), ("eligible", df[df["eligible"]])):
        for k in KINDS:
            act, nai, call = sub[k], sub[f"naive_{k}"], sub[f"call_{k}"]
            both = act.notna() & nai.notna()
            bc = act.notna() & call.notna()
            summary["kinds"][f"{k}@{scope}"] = {
                "classes": {str(c): int(v) for c, v in act.value_counts().items()},
                "none_share": round(float(act.isna().mean()), 4),
                "persistence_accuracy": round(float((act[both] == nai[both]).mean()), 4) if both.any() else None,
                "incumbent_accuracy": round(float((act[bc] == call[bc]).mean()), 4) if bc.any() else None,
                "incumbent_persistence_agreement": round(float((call[nai.notna() & call.notna()] == nai[nai.notna() & call.notna()]).mean()), 4),
                "n_resolved": int(act.notna().sum()),
            }
    with open(os.path.join(out_dir, "structural_dataset_v1.json"), "w", encoding="utf-8") as fh:
        json.dump(summary, fh, indent=1, sort_keys=True)
    return summary


def selfcheck():
    rng = np.random.default_rng(3)
    days = pd.bdate_range("2020-01-01", periods=700)
    frames = []
    for s in range(4):
        close = 100 * np.cumprod(1 + rng.normal(0, 0.01, len(days)))
        frames.append(pd.DataFrame({"symbol_id": s + 1, "day": days, "close": close,
                                    "volume": 1e6 * np.exp(rng.normal(0, 0.3, len(days)))}))
    tmp = tempfile.mkdtemp()
    panel_path = os.path.join(tmp, "panel.parquet")
    pd.concat(frames).to_parquet(panel_path, index=False)
    summary = build(panel_path, tmp)
    df = pd.read_parquet(os.path.join(tmp, "structural_dataset_v1.parquet"))
    assert summary["rows"] > 1000, summary["rows"]
    has = df["call_trend21"].notna()
    assert ((df.loc[has, "call_trend21"] == "uptrend") == (df.loc[has, "dist_trend"] > 0)).all()
    for c in ("conv_trend21", "conv_liquidity21", "conv_vol21"):
        v = df[c].dropna()
        assert ((v >= 0) & (v <= 1)).all(), c
    both = df["naive_trend21"].notna() & df["call_trend21"].notna()
    assert (df.loc[both, "naive_trend21"] == df.loc[both, "call_trend21"]).all()
    g = frames[0]
    rets = SL.bar_returns2(g["close"].tolist())
    va = SL.vol21_actual(rets.tolist())
    sub = df[df["symbol_id"] == 1].reset_index(drop=True)
    for i in (259, 300, 400, 500, 600):
        row = sub[sub["day"] == days[i]].iloc[0]
        exp = va[i - 1]
        assert (row["vol21"] == exp) or (exp is None and pd.isna(row["vol21"])), (i, row["vol21"], exp)
    print("SELFCHECK OK")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--panel", default="research/dirfix/panel.parquet")
    ap.add_argument("--out-dir", default="research/forecastplan/exp/out")
    ap.add_argument("--limit-symbols", type=int, default=0)
    ap.add_argument("--selfcheck", action="store_true")
    a = ap.parse_args()
    if a.selfcheck:
        selfcheck()
        return
    s = build(a.panel, a.out_dir, a.limit_symbols)
    print(f"STRUCTURAL DATA OK rows={s['rows']} days={s['days']} symbols={s['symbols']}")


if __name__ == "__main__":
    main()

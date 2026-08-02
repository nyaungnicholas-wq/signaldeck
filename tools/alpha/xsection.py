"""Cross-sectional features, and an honest measurement of whether they rank.

THE QUESTION THIS ANSWERS
The ensemble's raw score has a measured cross-sectional information coefficient
of -0.0269 (t = -1.23, 13/29 days positive) against next-day return: its
per-symbol ordering is indistinguishable from noise. Beta calibration can now
preserve an ordering, but preserving noise is worthless, so the ordering has to
earn it first.

The workflow's claim is that the fix is CROSS-SECTIONAL normalisation: an
absolute RSI of 65 says little, while "RSI in the 0.8 percentile of its sector
today" is a relative statement with market beta removed. This script builds
those features and measures whether that claim survives contact with the data.

DISCIPLINE
  * Point-in-time universe. A symbol enters on its first bar and leaves at
    delisted_at, so the cross-section on day t contains what was actually
    tradable on day t -- including the 716 companies that later died. Ranking
    against today's survivors would be lookahead in the denominator of every
    feature.
  * Features use bars up to and including t; the label is t -> t+1. Nothing
    reads across that boundary.
  * IC is Spearman per DAY, then averaged over days. Pooling all (symbol, day)
    rows would treat ~1,000 symbols sharing one market move as 1,000
    independent observations.
  * The t-stat uses the standard error ACROSS DAYS for the same reason.
  * Reported per feature, unadjusted and Bonferroni-adjusted. Testing 10
    features and quoting the best one is how noise gets published.

Read-only. Writes nothing to the database.
"""
import argparse
import json
import math
import sqlite3

import numpy as np
import pandas as pd

DB = r"C:\Users\Nicholas_N\Desktop\claude code\signaldeck\data\signaldeck.db"


def load_panel(db, min_bars=250):
    """Wide close/volume panels plus the point-in-time tradable mask."""
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    bars = pd.read_sql_query(
        """SELECT b.symbol_id, b.ts, b.close, b.high, b.low, b.volume
             FROM bars b WHERE b.tf='1d'""", con)
    syms = pd.read_sql_query(
        "SELECT id AS symbol_id, symbol, added_at, delisted_at FROM symbols "
        "WHERE market='stocks'", con)
    con.close()

    bars = bars[bars.symbol_id.isin(syms.symbol_id)]
    keep = bars.groupby("symbol_id").ts.count()
    bars = bars[bars.symbol_id.isin(keep[keep >= min_bars].index)]

    bars["day"] = pd.to_datetime(bars.ts, unit="s").dt.normalize()
    close = bars.pivot_table(index="day", columns="symbol_id", values="close")
    vol = bars.pivot_table(index="day", columns="symbol_id", values="volume")

    # POINT-IN-TIME MASK. True only where the symbol was listed and not yet
    # delisted on that day. Without this the cross-section is today's survivors
    # projected backwards.
    s = syms.set_index("symbol_id")
    added = pd.to_datetime(s.added_at, unit="s").reindex(close.columns)
    dl = s.delisted_at.reindex(close.columns)
    delisted = pd.to_datetime(dl.where(dl > 0), unit="s")
    days = close.index.values[:, None]
    alive = (days >= added.values[None, :]) & (
        (pd.isna(delisted.values)[None, :]) | (days < delisted.values[None, :]))
    mask = pd.DataFrame(alive, index=close.index, columns=close.columns)
    return close.where(mask & close.notna()), vol.where(mask), bars


def build_features(close, vol):
    """Raw (not yet cross-sectional) per-symbol features, all causal."""
    r1 = close.pct_change()
    f = {}
    # Momentum at several horizons; 21-1 skips the last month, the standard
    # construction that avoids contaminating momentum with short-term reversal.
    f["mom_5"] = close.pct_change(5)
    f["mom_21"] = close.pct_change(21)
    f["mom_63"] = close.pct_change(63)
    f["mom_252_21"] = close.shift(21).pct_change(231)
    # Short-horizon reversal.
    f["rev_1"] = -r1
    f["rev_5"] = -close.pct_change(5)
    # Risk / liquidity.
    f["vol_21"] = r1.rolling(21).std()
    f["dollar_vol_21"] = (close * vol).rolling(21).mean()
    # Distance from a moving average, scaled by its own volatility.
    ma = close.rolling(50).mean()
    f["ma_gap_50"] = (close - ma) / (close.rolling(50).std() + 1e-12)
    # Volume surprise.
    f["vol_surprise"] = vol / (vol.rolling(21).mean() + 1e-12)
    return f


def cross_sectional_rank(df, min_names=20):
    """Rank each row to [-0.5, 0.5]; rows with too few names are dropped.

    This is the whole point: the number that reaches a model is a symbol's
    position among its peers THAT DAY, not its absolute level. Market beta —
    the component every name shares — is removed by construction, because a
    day on which everything rises produces the same ranks as a day on which
    everything falls.
    """
    ranked = df.rank(axis=1, pct=True) - 0.5
    return ranked.where(df.notna().sum(axis=1).ge(min_names), other=np.nan)


def daily_ic(feat, fwd, min_names=20):
    """Spearman IC per day between a feature and the forward return."""
    ics, days = [], []
    common = feat.index.intersection(fwd.index)
    for d in common:
        a, b = feat.loc[d], fwd.loc[d]
        ok = a.notna() & b.notna()
        if ok.sum() < min_names:
            continue
        ra = a[ok].rank()
        rb = b[ok].rank()
        if ra.std() == 0 or rb.std() == 0:
            continue
        ics.append(float(np.corrcoef(ra, rb)[0, 1]))
        days.append(d)
    return pd.Series(ics, index=pd.DatetimeIndex(days))


def summarize(name, ic, n_tests):
    n = len(ic)
    if n < 20:
        return {"feature": name, "days": n, "verdict": "INSUFFICIENT DAYS"}
    mean = float(ic.mean())
    se = float(ic.std(ddof=1) / math.sqrt(n))
    t = mean / se if se > 0 else 0.0
    # Bonferroni over the features tested in this run. Quoting the best of ten
    # at its unadjusted t is how a noise feature gets published as an edge.
    crit = 1.96
    crit_bonf = abs(_norm_ppf(1 - 0.025 / n_tests))
    return {
        "feature": name,
        "days": n,
        "mean_ic": round(mean, 5),
        "t_stat": round(t, 2),
        "hit_days_pct": round(100 * float((ic > 0).mean()), 1),
        "significant_raw": abs(t) > crit,
        "significant_bonferroni": abs(t) > crit_bonf,
        "bonferroni_crit_t": round(crit_bonf, 2),
    }


def _norm_ppf(p):
    """Acklam's inverse normal CDF — good to ~1e-9, no scipy dependency."""
    a = [-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
         1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00]
    b = [-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
         6.680131188771972e+01, -1.328068155288572e+01]
    c = [-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
         -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00]
    d = [7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
         3.754408661907416e+00]
    pl, ph = 0.02425, 1 - 0.02425
    if p < pl:
        q = math.sqrt(-2 * math.log(p))
        return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q+c[5]) / \
               ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q+1)
    if p > ph:
        q = math.sqrt(-2 * math.log(1 - p))
        return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q+c[5]) / \
               ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q+1)
    q = p - 0.5
    r = q * q
    return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r+a[5])*q / \
           (((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r+1)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=DB)
    ap.add_argument("--horizon", type=int, default=1, help="forward days")
    ap.add_argument("--out", default="xsection_ic.json")
    args = ap.parse_args()

    close, vol, _ = load_panel(args.db)
    print(f"panel: {close.shape[0]} days x {close.shape[1]} symbols "
          f"({close.index.min().date()} .. {close.index.max().date()})", flush=True)
    print(f"median names per day (PIT): {int(close.notna().sum(axis=1).median())}",
          flush=True)

    # Label: forward return over the horizon, aligned so day t's feature is
    # scored against the move that happens AFTER t.
    fwd = close.shift(-args.horizon) / close - 1

    raw = build_features(close, vol)
    results = []
    n_tests = 2 * len(raw)  # each feature measured raw AND cross-sectional

    for name, f in raw.items():
        results.append(summarize(f"{name} [absolute]", daily_ic(f, fwd), n_tests))
        results.append(summarize(f"{name} [x-sectional]",
                                 daily_ic(cross_sectional_rank(f), fwd), n_tests))

    results.sort(key=lambda r: -abs(r.get("t_stat") or 0))
    print(f"\n{'feature':<28} {'days':>5} {'mean IC':>9} {'t':>7} {'>0%':>6}  sig")
    print("-" * 70)
    for r in results:
        if "mean_ic" not in r:
            print(f"{r['feature']:<28} {r['days']:>5}  {r['verdict']}")
            continue
        sig = "BONF" if r["significant_bonferroni"] else ("raw" if r["significant_raw"] else "-")
        print(f"{r['feature']:<28} {r['days']:>5} {r['mean_ic']:>+9.5f} "
              f"{r['t_stat']:>+7.2f} {r['hit_days_pct']:>5.1f}%  {sig}")

    with open(args.out, "w") as fh:
        json.dump({"horizon_days": args.horizon, "n_tests": n_tests,
                   "results": results}, fh, indent=2)
    print(f"\nwrote {args.out}")

    surv = [r for r in results if r.get("significant_bonferroni")]
    print(f"\nSURVIVED BONFERRONI: {len(surv)}/{len(results)}")
    for r in surv:
        print(f"  {r['feature']}  IC={r['mean_ic']:+.5f}  t={r['t_stat']:+.2f}")
    if not surv:
        print("  none — no feature's cross-sectional ordering is distinguishable")
        print("  from noise after correcting for the number of features tested.")


if __name__ == "__main__":
    main()

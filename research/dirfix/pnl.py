"""Walk-forward long-short backtest from classifier predictions."""

import numpy as np
import pandas as pd


def backtest(pred, fwd, horizon, k=10, cost_bps=5.0, borrow_bps_yr=30.0):
    if pred.empty:
        empty_daily = pd.DataFrame(columns=["gross", "net", "n_long", "n_short"])
        empty_daily.index.name = "day"
        return {
            "n_days": 0,
            "n_tranches": 0,
            "n_long": 0,
            "n_short": 0,
            "gross_ann": 0.0,
            "net_ann": 0.0,
            "sharpe": 0.0,
            "max_dd": 0.0,
            "hit_rate": 0.0,
            "avg_win": 0.0,
            "avg_loss": 0.0,
            "turnover_ann": (252.0 / horizon) * 2.0,
            "long_ret": 0.0,
            "short_ret": 0.0,
            "daily": empty_daily,
        }

    fwd_df = fwd.reset_index()
    fwd_df.columns = ["day", "symbol_id", "fwd_ret"]
    merged = pred.merge(fwd_df, on=["day", "symbol_id"], how="inner")

    if merged.empty:
        empty_daily = pd.DataFrame(columns=["gross", "net", "n_long", "n_short"])
        empty_daily.index.name = "day"
        return {
            "n_days": 0,
            "n_tranches": 0,
            "n_long": 0,
            "n_short": 0,
            "gross_ann": 0.0,
            "net_ann": 0.0,
            "sharpe": 0.0,
            "max_dd": 0.0,
            "hit_rate": 0.0,
            "avg_win": 0.0,
            "avg_loss": 0.0,
            "turnover_ann": (252.0 / horizon) * 2.0,
            "long_ret": 0.0,
            "short_ret": 0.0,
            "daily": empty_daily,
        }

    # prob DESCENDING: rank 0 must be the HIGHEST-probability name, because
    # rank < k_eff is the long leg. Sorting prob ascending here longs the names
    # the model likes least and silently returns the negated P&L.
    merged = merged.sort_values(["day", "prob", "symbol_id"],
                                ascending=[True, False, True])
    merged["rank"] = merged.groupby("day").cumcount()
    day_counts = merged.groupby("day").size().rename("n_day")
    merged = merged.join(day_counts, on="day")

    # min(k, n//2): without the k the book silently becomes top-HALF vs
    # bottom-HALF of ~890 names, which is a different and much weaker strategy
    # than the top-k the caller asked for, and identical for every k.
    merged["k_eff"] = np.minimum(int(k), merged["n_day"] // 2).clip(lower=0)
    valid = merged["k_eff"] >= 1
    merged = merged[valid].copy()

    if merged.empty:
        empty_daily = pd.DataFrame(columns=["gross", "net", "n_long", "n_short"])
        empty_daily.index.name = "day"
        return {
            "n_days": 0,
            "n_tranches": 0,
            "n_long": 0,
            "n_short": 0,
            "gross_ann": 0.0,
            "net_ann": 0.0,
            "sharpe": 0.0,
            "max_dd": 0.0,
            "hit_rate": 0.0,
            "avg_win": 0.0,
            "avg_loss": 0.0,
            "turnover_ann": (252.0 / horizon) * 2.0,
            "long_ret": 0.0,
            "short_ret": 0.0,
            "daily": empty_daily,
        }

    merged["is_long"] = merged["rank"] < merged["k_eff"]
    merged["is_short"] = merged["rank"] >= merged["n_day"] - merged["k_eff"]

    longs = merged[merged["is_long"]]
    shorts = merged[merged["is_short"]]

    long_stats = longs.groupby("day")["fwd_ret"].mean().rename("long_mean")
    short_stats = shorts.groupby("day")["fwd_ret"].mean().rename("short_mean")
    n_long = longs.groupby("day").size().rename("n_long")
    n_short = shorts.groupby("day").size().rename("n_short")

    daily = pd.concat([long_stats, short_stats, n_long, n_short], axis=1)
    daily["gross"] = (daily["long_mean"] - daily["short_mean"]) / 2.0

    round_trip = 2 * cost_bps / 10000.0
    borrow = (borrow_bps_yr / 10000.0) * (horizon / 252.0) * 0.5
    daily["net"] = daily["gross"] - round_trip - borrow

    n_days = len(daily)
    n_long_total = int(daily["n_long"].sum())
    n_short_total = int(daily["n_short"].sum())

    gross_ann = daily["gross"].mean() * (252.0 / horizon)
    net_ann = daily["net"].mean() * (252.0 / horizon)

    hit_rate = (daily["gross"] > 0).mean()
    avg_win = daily.loc[daily["gross"] > 0, "gross"].mean()
    avg_loss = daily.loc[daily["gross"] <= 0, "gross"].mean()
    if pd.isna(avg_win):
        avg_win = 0.0
    if pd.isna(avg_loss):
        avg_loss = 0.0

    long_ret = longs["fwd_ret"].mean()
    short_ret = shorts["fwd_ret"].mean()
    if pd.isna(long_ret):
        long_ret = 0.0
    if pd.isna(short_ret):
        short_ret = 0.0

    unique_days = sorted(daily.index.unique())
    tranche_indices = list(range(0, len(unique_days), horizon))
    tranche_days = [unique_days[i] for i in tranche_indices]
    tranche_returns = daily.loc[tranche_days, "net"].values
    n_tranches = len(tranche_returns)

    if n_tranches >= 2 and np.std(tranche_returns, ddof=1) > 0:
        sharpe = np.mean(tranche_returns) / np.std(tranche_returns, ddof=1) * np.sqrt(252.0 / horizon)
    else:
        sharpe = 0.0

    cum = np.cumprod(1 + tranche_returns)
    peak = np.maximum.accumulate(cum)
    dd = (cum - peak) / peak
    max_dd = dd.min() if len(dd) > 0 else 0.0

    turnover_ann = (252.0 / horizon) * 2.0

    daily_out = daily[["gross", "net", "n_long", "n_short"]].copy()
    daily_out.index.name = "day"

    return {
        "n_days": int(n_days),
        "n_tranches": int(n_tranches),
        "n_long": n_long_total,
        "n_short": n_short_total,
        "gross_ann": float(gross_ann),
        "net_ann": float(net_ann),
        "sharpe": float(sharpe),
        "max_dd": float(max_dd),
        "hit_rate": float(hit_rate),
        "avg_win": float(avg_win),
        "avg_loss": float(avg_loss),
        "turnover_ann": float(turnover_ann),
        "long_ret": float(long_ret),
        "short_ret": float(short_ret),
        "daily": daily_out,
    }
"""Daily mark-to-market long-short portfolio simulator for equities."""

import numpy as np
import pandas as pd
from typing import Dict, Any


_BETA_CACHE = {}


def simulate(
    pred: pd.DataFrame,
    rets: pd.DataFrame,
    horizon: int,
    k: int = 10,
    weighting: str = "equal",
    vol_lookback: int = 63,
    vol_target: float = None,
    beta_neutral: bool = False,
    beta_lookback: int = 252,
    cost_bps: float = 10.0,
    borrow_bps_yr: float = 300.0,
    max_weight: float = 0.10,
) -> Dict[str, Any]:
    if pred.empty:
        empty_series = pd.Series(dtype=float, index=pd.DatetimeIndex([]))
        empty_series.index.name = "day"
        return {
            "daily": empty_series,
            "n_days": 0,
            "ann_ret": 0.0,
            "ann_vol": 0.0,
            "sharpe": 0.0,
            "max_dd": 0.0,
            "calmar": 0.0,
            "turnover_ann": 0.0,
            "avg_gross_exposure": 0.0,
            "avg_tranches": 0.0,
            "max_abs_net_weight": 0.0,
            "beta_to_market": 0.0,
            "t_naive": 0.0,
            "t_nw": 0.0,
            "n_long": 0,
            "n_short": 0,
        }

    rets = rets.copy()
    rets.index = pd.to_datetime(rets.index)
    pred = pred.copy()
    pred["day"] = pd.to_datetime(pred["day"])

    common_days = sorted(set(pred["day"].unique()) & set(rets.index))
    if not common_days:
        empty_series = pd.Series(dtype=float, index=pd.DatetimeIndex([]))
        empty_series.index.name = "day"
        return {
            "daily": empty_series,
            "n_days": 0,
            "ann_ret": 0.0,
            "ann_vol": 0.0,
            "sharpe": 0.0,
            "max_dd": 0.0,
            "calmar": 0.0,
            "turnover_ann": 0.0,
            "avg_gross_exposure": 0.0,
            "avg_tranches": 0.0,
            "max_abs_net_weight": 0.0,
            "beta_to_market": 0.0,
            "t_naive": 0.0,
            "t_nw": 0.0,
            "n_long": 0,
            "n_short": 0,
        }

    market_ret = rets.mean(axis=1)
    symbols = rets.columns

    vol_est = rets.rolling(vol_lookback, min_periods=vol_lookback).std()
    vol_est = vol_est.shift(1)

    if beta_neutral:
        # Vectorised + memoised. The per-symbol loop this replaces ran 2,940
        # rolling covariances per simulate() call, which is hours across an
        # ablation sweep. cov = E[r_i*r_m] - E[r_i]E[r_m], all population
        # moments so cov and var use the same estimator.
        _key = (id(rets), int(beta_lookback))
        if _key in _BETA_CACHE:
            betas = _BETA_CACHE[_key]
        else:
            L = int(beta_lookback)
            m_i = rets.rolling(L, min_periods=L).mean()
            m_m = market_ret.rolling(L, min_periods=L).mean()
            m_im = rets.mul(market_ret, axis=0).rolling(L, min_periods=L).mean()
            cov = m_im.sub(m_i.mul(m_m, axis=0))
            var_m = (market_ret.pow(2).rolling(L, min_periods=L).mean() - m_m.pow(2))
            betas = cov.div(var_m, axis=0).shift(1).fillna(1.0)
            _BETA_CACHE[_key] = betas

    tranches = []
    n_long_total = 0
    n_short_total = 0
    daily_gross_weights = {}
    daily_costs = {}
    daily_turnover = {}

    for entry_day in common_days:
        day_pred = pred[pred["day"] == entry_day].copy()
        valid_symbols = [s for s in day_pred["symbol_id"].values if s in rets.columns]
        if not valid_symbols:
            continue
        day_pred = day_pred[day_pred["symbol_id"].isin(valid_symbols)]
        day_pred = day_pred.sort_values(["prob", "symbol_id"], ascending=[False, True])
        n = len(day_pred)
        k_eff = min(k, n // 2)
        if k_eff < 1:
            continue

        long_syms = day_pred.head(k_eff)["symbol_id"].values
        short_syms = day_pred.tail(k_eff)["symbol_id"].values
        n_long_total += k_eff
        n_short_total += k_eff

        if weighting == "equal":
            w_long_raw = np.ones(k_eff)
            w_short_raw = np.ones(k_eff)
        elif weighting == "invvol":
            sigma_long = vol_est.loc[entry_day, long_syms].values
            sigma_short = vol_est.loc[entry_day, short_syms].values
            sigma_long = np.where(np.isnan(sigma_long) | (sigma_long <= 0), np.nan, sigma_long)
            sigma_short = np.where(np.isnan(sigma_short) | (sigma_short <= 0), np.nan, sigma_short)
            p5_long = np.nanpercentile(sigma_long, 5) if np.any(~np.isnan(sigma_long)) else 1.0
            p5_short = np.nanpercentile(sigma_short, 5) if np.any(~np.isnan(sigma_short)) else 1.0
            sigma_long = np.maximum(sigma_long, p5_long)
            sigma_short = np.maximum(sigma_short, p5_short)
            w_long_raw = 1.0 / sigma_long
            w_short_raw = 1.0 / sigma_short
        elif weighting == "conviction":
            prob_long = day_pred.head(k_eff)["prob"].values
            prob_short = day_pred.tail(k_eff)["prob"].values
            w_long_raw = np.abs(prob_long - 0.5)
            w_short_raw = np.abs(prob_short - 0.5)
        elif weighting == "conviction_invvol":
            prob_long = day_pred.head(k_eff)["prob"].values
            prob_short = day_pred.tail(k_eff)["prob"].values
            sigma_long = vol_est.loc[entry_day, long_syms].values
            sigma_short = vol_est.loc[entry_day, short_syms].values
            sigma_long = np.where(np.isnan(sigma_long) | (sigma_long <= 0), np.nan, sigma_long)
            sigma_short = np.where(np.isnan(sigma_short) | (sigma_short <= 0), np.nan, sigma_short)
            p5_long = np.nanpercentile(sigma_long, 5) if np.any(~np.isnan(sigma_long)) else 1.0
            p5_short = np.nanpercentile(sigma_short, 5) if np.any(~np.isnan(sigma_short)) else 1.0
            sigma_long = np.maximum(sigma_long, p5_long)
            sigma_short = np.maximum(sigma_short, p5_short)
            w_long_raw = np.abs(prob_long - 0.5) / sigma_long
            w_short_raw = np.abs(prob_short - 0.5) / sigma_short
        else:
            raise ValueError(f"Unknown weighting: {weighting}")

        w_long_raw = w_long_raw / w_long_raw.sum() * 0.5
        w_short_raw = w_short_raw / w_short_raw.sum() * 0.5

        w_long = w_long_raw / horizon
        w_short = -w_short_raw / horizon

        max_w = max_weight / horizon
        w_long = np.clip(w_long, 0, max_w)
        w_short = np.clip(w_short, -max_w, 0)

        if w_long.sum() > 0:
            w_long = w_long / w_long.sum() * 0.5 / horizon
        if w_short.sum() < 0:
            w_short = w_short / (-w_short.sum()) * 0.5 / horizon

        if beta_neutral:
            beta_long = betas.loc[entry_day, long_syms].values
            beta_short = betas.loc[entry_day, short_syms].values
            beta_long = np.where(np.isnan(beta_long), 1.0, beta_long)
            beta_short = np.where(np.isnan(beta_short), 1.0, beta_short)

            for _ in range(2):
                all_w = np.concatenate([w_long, w_short])
                all_beta = np.concatenate([beta_long, beta_short])
                net_beta = np.sum(all_w * all_beta)
                sum_beta2 = np.sum(all_beta ** 2)
                if sum_beta2 > 0:
                    all_w = all_w - net_beta * all_beta / sum_beta2
                all_w = all_w - all_w.mean()
                w_long = all_w[:k_eff]
                w_short = all_w[k_eff:]

        tranche = {
            "entry_day": entry_day,
            "exit_day_idx": None,
            "long_syms": long_syms,
            "short_syms": short_syms,
            "w_long": w_long,
            "w_short": w_short,
        }
        tranches.append(tranche)

    if not tranches:
        empty_series = pd.Series(dtype=float, index=pd.DatetimeIndex([]))
        empty_series.index.name = "day"
        return {
            "daily": empty_series,
            "n_days": 0,
            "ann_ret": 0.0,
            "ann_vol": 0.0,
            "sharpe": 0.0,
            "max_dd": 0.0,
            "calmar": 0.0,
            "turnover_ann": 0.0,
            "avg_gross_exposure": 0.0,
            "avg_tranches": 0.0,
            "max_abs_net_weight": 0.0,
            "beta_to_market": 0.0,
            "t_naive": 0.0,
            "t_nw": 0.0,
            "n_long": 0,
            "n_short": 0,
        }

    rets_index = rets.index
    day_to_idx = {d: i for i, d in enumerate(rets_index)}

    for tr in tranches:
        entry_idx = day_to_idx[tr["entry_day"]]
        exit_idx = entry_idx + horizon
        if exit_idx < len(rets_index):
            tr["exit_day_idx"] = exit_idx
            tr["exit_day"] = rets_index[exit_idx]
        else:
            tr["exit_day_idx"] = len(rets_index)
            tr["exit_day"] = None

    daily_ret = pd.Series(0.0, index=rets_index, dtype=float)
    daily_gross = pd.Series(0.0, index=rets_index, dtype=float)
    daily_cost = pd.Series(0.0, index=rets_index, dtype=float)
    daily_turn = pd.Series(0.0, index=rets_index, dtype=float)
    active_tranches_count = pd.Series(0, index=rets_index, dtype=int)
    daily_net_weight = pd.Series(0.0, index=rets_index, dtype=float)

    cost_rate = cost_bps / 10000.0
    borrow_rate_daily = borrow_bps_yr / 10000.0 / 252.0

    for tr in tranches:
        entry_idx = day_to_idx[tr["entry_day"]]
        exit_idx = tr["exit_day_idx"]
        long_syms = tr["long_syms"]
        short_syms = tr["short_syms"]
        w_long = tr["w_long"]
        w_short = tr["w_short"]

        gross_tranche = np.sum(np.abs(w_long)) + np.sum(np.abs(w_short))
        daily_cost.iloc[entry_idx] += cost_rate * gross_tranche
        daily_turn.iloc[entry_idx] += gross_tranche

        if exit_idx < len(rets_index):
            daily_cost.iloc[exit_idx] += cost_rate * gross_tranche
            daily_turn.iloc[exit_idx] += gross_tranche

        for hold_idx in range(entry_idx, min(exit_idx, len(rets_index))):
            day = rets_index[hold_idx]
            r_long = rets.loc[day, long_syms].values
            r_short = rets.loc[day, short_syms].values
            r_long = np.nan_to_num(r_long, nan=0.0)
            r_short = np.nan_to_num(r_short, nan=0.0)
            pnl = np.sum(w_long * r_long) + np.sum(w_short * r_short)
            daily_ret.iloc[hold_idx] += pnl

            gross_held = np.sum(np.abs(w_long)) + np.sum(np.abs(w_short))
            daily_gross.iloc[hold_idx] += gross_held

            short_held = np.sum(np.abs(w_short))
            daily_cost.iloc[hold_idx] += borrow_rate_daily * short_held

            active_tranches_count.iloc[hold_idx] += 1
            net_w = np.sum(w_long) + np.sum(w_short)
            daily_net_weight.iloc[hold_idx] += net_w

    daily_ret = daily_ret - daily_cost

    if vol_target is not None:
        port_vol = daily_ret.rolling(63, min_periods=63).std() * np.sqrt(252)
        port_vol = port_vol.shift(1)
        scale = vol_target / port_vol
        scale = scale.clip(0.2, 3.0)
        scale = scale.fillna(1.0)
        daily_ret = daily_ret * scale

    # TRIM TO THE ACTIVE WINDOW. daily_ret is built over the whole `rets`
    # index, so grading a holdout-only prediction frame left 1,550 leading
    # ZEROS in the series -- which diluted the mean, shrank the vol, and
    # reported Sharpe 1.11 / +21% for a book whose real numbers were 2.58 /
    # +114%. Stats must cover the days the book was actually holding.
    _active = active_tranches_count[active_tranches_count > 0]
    if len(_active):
        _lo, _hi = _active.index[0], _active.index[-1]
        daily_ret = daily_ret.loc[_lo:_hi]
        daily_gross = daily_gross.loc[_lo:_hi]
        daily_turn = daily_turn.loc[_lo:_hi]
        daily_net_weight = daily_net_weight.loc[_lo:_hi]

    n_days = len(daily_ret)
    ann_ret = daily_ret.mean() * 252
    ann_vol = daily_ret.std(ddof=1) * np.sqrt(252)
    sharpe = ann_ret / ann_vol if ann_vol > 0 else 0.0

    cum = (1 + daily_ret).cumprod()
    running_max = cum.expanding().max()
    dd = (cum - running_max) / running_max
    max_dd = dd.min()
    calmar = ann_ret / abs(max_dd) if max_dd < 0 else 0.0

    turnover_ann = daily_turn.mean() * 252
    avg_gross_exposure = daily_gross.mean()
    avg_tranches = active_tranches_count.iloc[horizon:].mean() if len(active_tranches_count) > horizon else 0.0
    max_abs_net_weight = daily_net_weight.abs().max()

    market_aligned = market_ret.reindex(daily_ret.index).fillna(0.0)
    if market_aligned.std() > 0 and daily_ret.std() > 0:
        beta_to_market = np.cov(daily_ret, market_aligned)[0, 1] / np.var(market_aligned)
    else:
        beta_to_market = 0.0

    t_naive = daily_ret.mean() / (daily_ret.std(ddof=1) / np.sqrt(n_days)) if n_days > 1 and daily_ret.std(ddof=1) > 0 else 0.0

    lag = horizon
    n = len(daily_ret)
    if n > lag + 1:
        mean_ret = daily_ret.mean()
        demeaned = daily_ret - mean_ret
        gamma_0 = np.sum(demeaned ** 2) / n
        S = gamma_0
        for L in range(1, lag + 1):
            if L < n:
                gamma_L = np.sum(demeaned.iloc[L:] * demeaned.iloc[:-L].values) / n
                weight = 1.0 - L / (lag + 1)
                S += 2 * weight * gamma_L
        se_nw = np.sqrt(S / n) if S > 0 else 0.0
        t_nw = mean_ret / se_nw if se_nw > 0 else 0.0
    else:
        t_nw = 0.0

    daily_ret.index.name = "day"
    return {
        "daily": daily_ret,
        "n_days": int(n_days),
        "ann_ret": float(ann_ret),
        "ann_vol": float(ann_vol),
        "sharpe": float(sharpe),
        "max_dd": float(max_dd),
        "calmar": float(calmar),
        "turnover_ann": float(turnover_ann),
        "avg_gross_exposure": float(avg_gross_exposure),
        "avg_tranches": float(avg_tranches),
        "max_abs_net_weight": float(max_abs_net_weight),
        "beta_to_market": float(beta_to_market),
        "t_naive": float(t_naive),
        "t_nw": float(t_nw),
        "n_long": int(n_long_total),
        "n_short": int(n_short_total),
    }
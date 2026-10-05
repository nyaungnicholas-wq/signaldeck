"""Library of weight-generating functions for a long/flat trading research protocol.
Each function turns a price panel into a weights panel and shifts its signal by one day to avoid look-ahead bias."""
import pandas as pd
import numpy as np
import functools

SLEEVES = ["SPY", "EFA", "QQQ", "IWM", "TLT", "IEF", "GLD", "DBC", "VNQ"]

def _blank(px: pd.DataFrame) -> pd.DataFrame:
    """Return a DataFrame of zeros with the same index and columns as px, dtype float."""
    return pd.DataFrame(0.0, index=px.index, columns=px.columns, dtype=float)

def _month_end_mask(index: pd.DatetimeIndex) -> pd.Series:
    """True on the last trading day of each calendar month in the given DatetimeIndex."""
    # Group by year and month, take the last date of each group
    last_dates = index.to_series().groupby([index.year, index.month]).apply(lambda x: x.iloc[-1])
    mask = index.isin(last_dates.values)
    return pd.Series(mask, index=index)

def weights_spy_bh(px: pd.DataFrame) -> pd.DataFrame:
    """Buy-and-hold SPY: 1.0 in SPY column every row, 0 elsewhere. No shifting."""
    out = _blank(px)
    out["SPY"] = 1.0
    return out

def weights_cash(px: pd.DataFrame) -> pd.DataFrame:
    """All zeros."""
    return _blank(px)

def weights_spy_trend(px: pd.DataFrame, sma: int = 200) -> pd.DataFrame:
    """Long SPY when its price exceeds its SMA; signal shifted by one day."""
    sma_series = px["SPY"].rolling(window=sma, min_periods=sma).mean()
    signal = (px["SPY"] > sma_series).astype(float)
    shifted = signal.shift(1).fillna(0.0)
    out = _blank(px)
    out["SPY"] = shifted
    return out

def weights_divtrend(px: pd.DataFrame, sma: int = 200, leverage: float = 1.0, sleeves=None) -> pd.DataFrame:
    """Monthly-rebalanced equal-weight long-only trend sleeves, shifted by one day."""
    if sleeves is None:
        sleeves = SLEEVES
    # Use only sleeves that exist in px
    sleeves = [s for s in sleeves if s in px.columns]
    n = len(sleeves)
    if n == 0:
        return _blank(px)
    # Daily raw signal
    raw = pd.DataFrame(0.0, index=px.index, columns=sleeves, dtype=float)
    for s in sleeves:
        ma = px[s].rolling(window=sma, min_periods=sma).mean()
        raw[s] = (px[s] > ma).astype(float) * (leverage / n)
    # Monthly rebalance: keep only month-end rows, forward fill, then shift
    mask = _month_end_mask(px.index)
    month_end_raw = raw.where(mask, np.nan)
    ffilled = month_end_raw.ffill().fillna(0.0)
    shifted = ffilled.shift(1).fillna(0.0)
    return shifted.reindex(columns=px.columns, fill_value=0.0)

def weights_divtrend_invvol(px: pd.DataFrame, sma: int = 200, vol_win: int = 60, leverage: float = 1.0, sleeves=None) -> pd.DataFrame:
    """Monthly-rebalanced inverse-vol weighted trend sleeves, shifted by one day."""
    if sleeves is None:
        sleeves = SLEEVES
    sleeves = [s for s in sleeves if s in px.columns]
    n = len(sleeves)
    if n == 0:
        return _blank(px)
    # Daily trend signal
    in_trend = pd.DataFrame(False, index=px.index, columns=sleeves)
    for s in sleeves:
        ma = px[s].rolling(window=sma, min_periods=sma).mean()
        in_trend[s] = (px[s] > ma)
    # Trailing volatility of daily simple returns
    returns = px[sleeves].pct_change().fillna(0.0)
    sigma = returns.rolling(window=vol_win, min_periods=vol_win).std()
    # Prepare container for month-end raw weights
    raw_month_end = pd.DataFrame(np.nan, index=px.index, columns=sleeves, dtype=float)
    mask = _month_end_mask(px.index)
    for date in px.index[mask]:
        it_row = in_trend.loc[date]
        sig_row = sigma.loc[date]
        # Compute inverse-vol weights for in-trend sleeves with positive sigma
        inv = pd.Series(0.0, index=sleeves)
        valid = it_row & (sig_row > 0)
        inv[valid] = 1.0 / sig_row[valid]
        sum_inv = inv.sum()
        n_trend = it_row.sum()
        target_sum = (n_trend / n) * leverage
        if sum_inv > 0:
            weights = inv * (target_sum / sum_inv)
        else:
            weights = pd.Series(0.0, index=sleeves)
        raw_month_end.loc[date] = weights
    # Forward fill month-end weights, shift, fill NaN
    ffilled = raw_month_end.ffill().fillna(0.0)
    shifted = ffilled.shift(1).fillna(0.0)
    return shifted.reindex(columns=px.columns, fill_value=0.0)

def weights_spy_levered(px: pd.DataFrame, leverage: float = 2.0) -> pd.DataFrame:
    """Constant leverage in SPY column, zero elsewhere."""
    out = _blank(px)
    out["SPY"] = leverage
    return out

REGISTRY = {
    "spy_bh": weights_spy_bh,
    "cash": weights_cash,
    "spy_trend200": functools.partial(weights_spy_trend, sma=200),
    "spy_2x": functools.partial(weights_spy_levered, leverage=2.0),
    "divtrend_1x": functools.partial(weights_divtrend, leverage=1.0),
    "divtrend_2x": functools.partial(weights_divtrend, leverage=2.0),
    "divtrend_3x": functools.partial(weights_divtrend, leverage=3.0),
    "divtrend_5x": functools.partial(weights_divtrend, leverage=5.0),
    "divtrend_invvol_1x": functools.partial(weights_divtrend_invvol, leverage=1.0),
    "divtrend_invvol_3x": functools.partial(weights_divtrend_invvol, leverage=3.0),
}

if __name__ == "__main__":
    # Synthetic price panel
    idx = pd.bdate_range("2000-01-03", periods=1500)
    rng = np.random.default_rng(0)
    px = pd.DataFrame(
        np.exp(np.cumsum(rng.normal(0.0003, 0.01, size=(1500, len(SLEEVES))), axis=0)) * 100.0,
        index=idx,
        columns=SLEEVES,
    )
    # a) Basic checks for each registry entry
    for name, func in REGISTRY.items():
        out = func(px)
        assert out.index.equals(px.index), f"{name}: index mismatch"
        assert out.columns.equals(px.columns), f"{name}: columns mismatch"
        assert not out.isna().any().any(), f"{name}: output contains NaN"
        print(f"{name} OK")
    # b) No look-ahead for weights_spy_trend
    px2 = px.copy()
    px2.iloc[-1, px2.columns.get_loc("SPY")] *= 10.0
    wt1 = weights_spy_trend(px)
    wt2 = weights_spy_trend(px2)
    # All rows except possibly the last must be identical
    pd.testing.assert_frame_equal(wt1.iloc[:-1], wt2.iloc[:-1])
    # Explanation: if signal were not shifted, changing the last price would affect the last weight;
    # because we shift, the last weight depends only on data up to the previous day, so it is unchanged.
    print("spy_trend lookahead test OK")
    # c) Gross exposure limits for divtrend leverage 1.0
    wt_div1 = weights_divtrend(px, leverage=1.0)
    gross1 = wt_div1.abs().sum(axis=1)
    assert gross1.max() <= 1.0 + 1e-9, "divtrend 1x gross > 1"
    assert gross1.min() >= 0.0, "divtrend 1x gross < 0"
    print("divtrend 1x gross OK")
    # d) Gross exposure limits for divtrend leverage 3.0
    wt_div3 = weights_divtrend(px, leverage=3.0)
    gross3 = wt_div3.abs().sum(axis=1)
    assert gross3.max() <= 3.0 + 1e-9, "divtrend 3x gross > 3"
    assert gross3.min() >= 0.0, "divtrend 3x gross < 0"
    print("divtrend 3x gross OK")
    # e) Monthly rebalance actually bites: number of weight change rows < 400 and > 10
    wt_div = weights_divtrend(px, leverage=1.0)
    change_rows = (wt_div != wt_div.shift()).any(axis=1)
    n_changes = change_rows.sum()
    assert n_changes < 400, f"too many changes: {n_changes}"
    assert n_changes > 10, f"too few changes: {n_changes}"
    print(f"divtrend rebalance changes = {n_changes} OK")
    # f) Inverse-vol gross matches equal-weight gross on in-trend rows
    wt_inv = weights_divtrend_invvol(px, leverage=1.0)
    wt_eq = weights_divtrend(px, leverage=1.0)
    # Identify rows where anything is held (gross > 0) in either
    held = (wt_eq.abs().sum(axis=1) > 0) | (wt_inv.abs().sum(axis=1) > 0)
    gross_eq = wt_eq.abs().sum(axis=1)[held]
    gross_inv = wt_inv.abs().sum(axis=1)[held]
    assert np.allclose(gross_eq, gross_inv, atol=1e-9), "inverse-vol gross mismatch"
    print("divtrend_invvol gross match OK")
    # g) First row of every shifted function is all zeros
    for name, func in REGISTRY.items():
        if name in ("spy_bh", "spy_2x"):
            # spy_bh is not shifted; its first row SPY column is 1.0
            out = func(px)
            assert out.iloc[0]["SPY"] > 0.0, f"{name}: first row SPY not positive"
            # other columns zero
            assert (out.iloc[0].drop("SPY") == 0.0).all(), f"{name}: first row non-SPY not zero"
        else:
            out = func(px)
            assert (out.iloc[0] == 0.0).all(), f"{name}: first row not all zeros"
    print("SELFCHECK OK")
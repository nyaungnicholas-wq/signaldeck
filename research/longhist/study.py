"""Reproducible study of long-horizon market timing rules.

Publication lags are critical: without them a macro backtest reads data before it
existed and manufactures a false positive. The lags below reflect when each
series is actually published and available to a real-time investor.
"""

import argparse
import json
import sqlite3
import sys
import pathlib
from pathlib import Path

import numpy as np
import pandas as pd
import yfinance as yf

COST_PER_SWITCH = 0.0005
TRADING_DAYS = 252
DATA_DIR = Path("research/longhist/data")
PUBLICATION_LAG_DAYS = {"T10Y2Y": 1, "NFCI": 8, "UNRATE": 10, "VIXCLS": 1}


def stats(returns: pd.Series) -> dict:
    """Compute annualised return, vol, Sharpe, max drawdown, and observation count."""
    r = returns.dropna().astype(np.float64)
    n = len(r)
    if n < 60:
        return {k: np.nan for k in ("ann_pct", "vol_pct", "sharpe", "max_dd_pct", "n_days")}
    cum = (1 + r).cumprod()
    ann = cum.iloc[-1] ** (TRADING_DAYS / n) - 1
    vol = r.std(ddof=0) * np.sqrt(TRADING_DAYS)
    sharpe = ann / vol if vol != 0 else np.nan
    peak = cum.cummax()
    dd = (cum / peak - 1).min()
    return {
        "ann_pct": float(ann * 100),
        "vol_pct": float(vol * 100),
        "sharpe": float(sharpe),
        "max_dd_pct": float(dd * 100),
        "n_days": int(n),
    }


def fetch_total_return(tickers: list[str], force: bool = False) -> pd.DataFrame:
    """Fetch total-return (dividends reinvested) daily closes for ETFs."""
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    cache_path = DATA_DIR / "etf_total_return.parquet"
    meta_path = DATA_DIR / "etf_total_return.json"

    if not force and cache_path.exists() and meta_path.exists():
        with open(meta_path) as f:
            meta = json.load(f)
        if meta.get("tickers") == tickers:
            return pd.read_parquet(cache_path)

    frames = []
    for t in tickers:
        hist = yf.Ticker(t).history(period="max", interval="1d", auto_adjust=True)
        if hist.empty:
            continue
        s = hist["Close"].astype(np.float64)
        s.index = pd.to_datetime(s.index).tz_localize(None).normalize()
        s.name = t
        frames.append(s)

    df = pd.concat(frames, axis=1).sort_index()
    df.to_parquet(cache_path)
    with open(meta_path, "w") as f:
        json.dump({"tickers": tickers, "fetched": pd.Timestamp.now().isoformat()}, f)
    return df


def fetch_indices(force: bool = False) -> pd.DataFrame:
    """Fetch PRICE-ONLY (no dividends) daily closes for major indices."""
    tickers = ["^GSPC", "^NDX", "^IXIC", "^VIX"]
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    cache_path = DATA_DIR / "indices.parquet"
    meta_path = DATA_DIR / "indices.json"

    if not force and cache_path.exists() and meta_path.exists():
        with open(meta_path) as f:
            meta = json.load(f)
        if meta.get("tickers") == tickers:
            return pd.read_parquet(cache_path)

    frames = []
    for t in tickers:
        hist = yf.Ticker(t).history(period="max", interval="1d", auto_adjust=False)
        if hist.empty:
            continue
        s = hist["Close"].astype(np.float64)
        s.index = pd.to_datetime(s.index).tz_localize(None).normalize()
        s.name = t
        frames.append(s)

    df = pd.concat(frames, axis=1).sort_index()
    df.to_parquet(cache_path)
    with open(meta_path, "w") as f:
        json.dump({"tickers": tickers, "fetched": pd.Timestamp.now().isoformat()}, f)
    return df


def validation_gate(indices: pd.DataFrame, etfs: pd.DataFrame) -> bool:
    """Validate that index and ETF daily returns correlate > 0.99 from 2016-01-04."""
    start = pd.Timestamp("2016-01-04")
    pairs = [("^GSPC", "SPY"), ("^NDX", "QQQ")]
    all_pass = True

    for idx_tkr, etf_tkr in pairs:
        if idx_tkr not in indices.columns or etf_tkr not in etfs.columns:
            print(f"Missing {idx_tkr} or {etf_tkr}")
            all_pass = False
            continue

        idx_r = indices[idx_tkr].loc[start:].pct_change().dropna()
        etf_r = etfs[etf_tkr].loc[start:].pct_change().dropna()
        common = idx_r.index.intersection(etf_r.index)
        if len(common) < 60:
            print(f"Insufficient overlap for {idx_tkr}/{etf_tkr}")
            all_pass = False
            continue

        idx_r = idx_r.loc[common]
        etf_r = etf_r.loc[common]
        corr = idx_r.corr(etf_r)
        idx_ann = (1 + idx_r).prod() ** (TRADING_DAYS / len(idx_r)) - 1
        etf_ann = (1 + etf_r).prod() ** (TRADING_DAYS / len(etf_r)) - 1

        print(
            f"{idx_tkr} vs {etf_tkr}: n={len(common)}, corr={corr:.6f}, "
            f"index_ann={idx_ann*100:.2f}%, etf_ann={etf_ann*100:.2f}% "
            f"(ETF higher: includes dividends; gap ~1.5-2.0 pp expected)"
        )
        if corr <= 0.99:
            all_pass = False

    return all_pass


def ma_signal(close: pd.Series, window: int) -> pd.Series:
    """Boolean signal: close > rolling mean, shifted one day to prevent look-ahead."""
    sig = close > close.rolling(window, min_periods=window).mean()
    # shift(1) is what prevents look-ahead: today's signal uses yesterday's close vs MA
    return sig.shift(1).fillna(False)


def apply_signal(returns: pd.Series, signal: pd.Series, cost: float = COST_PER_SWITCH) -> pd.Series:
    """Apply boolean signal to returns, subtracting switching costs."""
    flips = signal.astype(int).diff().abs().fillna(0)
    result = returns.where(signal, 0.0) - flips * cost
    return result.astype(np.float64)


def ma_study(etfs: pd.DataFrame, windows: tuple[int, ...] = (100, 200, 250)) -> pd.DataFrame:
    """Run MA timing study across assets and windows."""
    rows = []
    for asset in etfs.columns:
        close = etfs[asset].dropna()
        ret = close.pct_change().dropna()
        bh = stats(ret)
        for w in windows:
            sig = ma_signal(close, w)
            timed_ret = apply_signal(ret, sig)
            timed = stats(timed_ret)
            rows.append({
                "asset": asset,
                "window": w,
                "bh_ann": bh["ann_pct"],
                "bh_sharpe": bh["sharpe"],
                "bh_dd": bh["max_dd_pct"],
                "timed_ann": timed["ann_pct"],
                "timed_sharpe": timed["sharpe"],
                "timed_dd": timed["max_dd_pct"],
                "excess_pp": timed["ann_pct"] - bh["ann_pct"],
                "sharpe_gain": timed["sharpe"] - bh["sharpe"],
            })
    return pd.DataFrame(rows)


def macro_signals(db_path: str, calendar: pd.DatetimeIndex) -> dict[str, pd.Series]:
    """Read macro series from SQLite, apply publication lags, build boolean in-market signals."""
    uri = f"file:{db_path}?mode=ro"
    signals = {}

    with sqlite3.connect(uri, uri=True) as con:
        for name, lag in PUBLICATION_LAG_DAYS.items():
            df = pd.read_sql_query(
                "SELECT ts, value FROM macro_series WHERE series = ? ORDER BY ts",
                con,
                params=(name,),
            )
            if df.empty:
                print(f"Warning: {name} not found in macro_series, skipping")
                continue

            s = pd.Series(df["value"].values, index=pd.to_datetime(df["ts"], unit="s"))
            s = s.shift(freq=pd.Timedelta(days=lag))
            s = s.reindex(calendar, method="ffill")

            if name == "T10Y2Y":
                signals["curve_inverted"] = (s >= 0).shift(1).fillna(False)
            elif name == "NFCI":
                signals["tight_credit"] = (s <= 0).shift(1).fillna(False)
            elif name == "UNRATE":
                min_1y = s.rolling(252, min_periods=252).min()
                signals["unemp_rising"] = (s <= min_1y + 0.5).shift(1).fillna(False)
            elif name == "VIXCLS":
                mean_200 = s.rolling(200, min_periods=200).mean()
                signals["vix_elevated"] = (s <= mean_200).shift(1).fillna(False)

    return signals


def robustness(returns: pd.Series, signal: pd.Series) -> dict:
    """Kill tests: full sample, ex-2008/09, ex-2020, ex-all, per-decade, n_decisions."""
    # n_decisions is the EFFECTIVE sample size; daily observation counts do not substitute for it
    n_decisions = int((signal.astype(int).diff() == -1).sum())

    def _excess_sharpe(r, s):
        bh = stats(r)
        timed = stats(apply_signal(r, s))
        return {
            "excess_pp": timed["ann_pct"] - bh["ann_pct"],
            "sharpe_gain": timed["sharpe"] - bh["sharpe"],
        }

    out = {"n_decisions": n_decisions}

    # Full sample
    out["full"] = _excess_sharpe(returns, signal)

    # Exclude 2008-2009
    mask_0809 = ~returns.index.year.isin([2008, 2009])
    out["ex_2008_2009"] = _excess_sharpe(returns[mask_0809], signal[mask_0809])

    # Exclude 2020
    mask_2020 = returns.index.year != 2020
    out["ex_2020"] = _excess_sharpe(returns[mask_2020], signal[mask_2020])

    # Exclude all three
    mask_all = mask_0809 & mask_2020
    out["ex_all"] = _excess_sharpe(returns[mask_all], signal[mask_all])

    # Per-decade
    decades = {
        "1990s": (1990, 1999),
        "2000s": (2000, 2009),
        "2010s": (2010, 2019),
        "2020s": (2020, 2029),
    }
    for label, (start, end) in decades.items():
        mask = (returns.index.year >= start) & (returns.index.year <= end)
        if mask.sum() >= 60:
            out[label] = _excess_sharpe(returns[mask], signal[mask])
        else:
            out[label] = {"excess_pp": np.nan, "sharpe_gain": np.nan}

    return out


def _print_table(df: pd.DataFrame) -> None:
    """Print DataFrame with aligned columns."""
    if df.empty:
        print("(empty)")
        return
    with pd.option_context("display.max_columns", None, "display.width", 200, "display.float_format", "{:.2f}".format):
        print(df.to_string(index=False))


def main() -> int:
    parser = argparse.ArgumentParser(description="Long-horizon timing study")
    sub = parser.add_subparsers(dest="cmd", required=True)

    sub.add_parser("fetch", help="Fetch data and run validation gate")
    sub.add_parser("ma", help="Print MA timing study table")
    sub.add_parser("macro", help="Print macro rule stats vs buy-and-hold on SPY")
    sub.add_parser("robustness", help="Run robustness on unemp_rising vs SPY")
    sub.add_parser("all", help="Run all steps in order")

    args = parser.parse_args()

    if args.cmd in ("fetch", "all"):
        etfs = fetch_total_return(["SPY", "QQQ", "IWM", "EFA", "EEM", "TLT", "GLD"])
        indices = fetch_indices()
        if not validation_gate(indices, etfs):
            print("Validation gate FAILED", file=sys.stderr)
            return 1
        print("Validation gate PASSED")

    if args.cmd in ("ma", "all"):
        etfs = fetch_total_return(["SPY", "QQQ", "IWM", "EFA", "EEM", "TLT", "GLD"])
        df = ma_study(etfs)
        print("\n=== MA Timing Study ===")
        _print_table(df)

    if args.cmd in ("macro", "all"):
        etfs = fetch_total_return(["SPY"])
        spy_ret = etfs["SPY"].pct_change().dropna()
        calendar = spy_ret.index
        # macro_series lives in the PRODUCTION database, not a separate file.
        db_path = pathlib.Path("data/signaldeck.db")
        if not db_path.exists():
            print(f"Macro DB not found at {db_path}", file=sys.stderr)
            return 1
        signals = macro_signals(str(db_path), calendar)
        print("\n=== Macro Rules vs SPY Buy-and-Hold ===")
        bh = stats(spy_ret)
        print(f"Buy-and-hold: ann={bh['ann_pct']:.2f}%, sharpe={bh['sharpe']:.2f}, dd={bh['max_dd_pct']:.2f}%")
        for name, sig in signals.items():
            timed = stats(apply_signal(spy_ret, sig))
            print(f"{name}: ann={timed['ann_pct']:.2f}%, sharpe={timed['sharpe']:.2f}, dd={timed['max_dd_pct']:.2f}%, "
                  f"excess={timed['ann_pct']-bh['ann_pct']:.2f}pp, sharpe_gain={timed['sharpe']-bh['sharpe']:.2f}")

    if args.cmd in ("robustness", "all"):
        etfs = fetch_total_return(["SPY"])
        spy_ret = etfs["SPY"].pct_change().dropna()
        calendar = spy_ret.index
        # macro_series lives in the PRODUCTION database, not a separate file.
        db_path = pathlib.Path("data/signaldeck.db")
        if not db_path.exists():
            print(f"Macro DB not found at {db_path}", file=sys.stderr)
            return 1
        signals = macro_signals(str(db_path), calendar)
        if "unemp_rising" not in signals:
            print("unemp_rising signal not available", file=sys.stderr)
            return 1
        rob = robustness(spy_ret, signals["unemp_rising"])
        print("\n=== Robustness: unemp_rising on SPY ===")
        for key, val in rob.items():
            if isinstance(val, dict):
                print(f"  {key}: excess={val['excess_pp']:.2f}pp, sharpe_gain={val['sharpe_gain']:.2f}")
            else:
                print(f"  {key}: {val}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
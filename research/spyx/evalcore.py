"""Single source of truth for loading prices and computing net performance; all costs flow through cost_returns."""
import os
import pathlib
import json
import math
import numpy as np
import pandas as pd
import yfinance as yf

CACHE_DIR = pathlib.Path(r"C:\Users\Nicholas_N\Desktop\claude code\stock-trader\data\_cache")
DEV_START = "1993-01-29"
DEV_END = "2015-12-31"
VAL_START = "2016-01-01"
VAL_END = "2020-11-30"
EMBARGO_END = "2020-12-31"
SEAL_START = "2021-01-01"
SEAL_END = "2026-08-28"
TRADING_DAYS = 252

def seal_is_open() -> bool:
    return os.environ.get("SPYX_SEAL") == "OPEN"

PIN_PATH = pathlib.Path(__file__).parent / "out" / "data_manifest.json"


def _read_pins() -> dict:
    if PIN_PATH.exists():
        return json.loads(PIN_PATH.read_text())
    return {}


def _write_pin(ticker: str, filename: str) -> None:
    pins = _read_pins()
    if pins.get(ticker) == filename:
        return
    pins[ticker] = filename
    PIN_PATH.parent.mkdir(parents=True, exist_ok=True)
    PIN_PATH.write_text(json.dumps(pins, indent=2, sort_keys=True))


def _as_ts(value, argname: str) -> pd.Timestamp:
    """Coerce a date argument to a Timestamp, or fail loudly.

    The seal guard MUST NOT compare raw strings. Lexicographic comparison of ISO text
    silently passes any format whose leading character sorts below '2' -- measured:
    end="12/31/2026" compared False against "2021-01-01" and served 8453 rows of sealed
    data. Parsing first makes the guard independent of the caller's date format.
    """
    try:
        ts = pd.Timestamp(value)
    except Exception as exc:
        raise ValueError(f"{argname}={value!r} is not a parseable date: {exc}") from exc
    if pd.isna(ts):
        raise ValueError(f"{argname}={value!r} parsed to NaT")
    return ts


def load(ticker: str, start, end) -> pd.DataFrame:
    start_ts = _as_ts(start, "start")
    end_ts = _as_ts(end, "end")
    if not seal_is_open() and end_ts >= pd.Timestamp(SEAL_START):
        raise PermissionError(
            f"Data for {ticker} until {end_ts.date()} is sealed; set SPYX_SEAL=OPEN to access >= {SEAL_START}"
        )
    start = start_ts.strftime("%Y-%m-%d")
    end = end_ts.strftime("%Y-%m-%d")

    # DATA LINEAGE PIN. Selecting the "widest covering file" from a shared, mutable cache
    # means a backtest's inputs depend on whatever else wrote to that directory first --
    # measured on 2026-08-31, the finalist was selected on one TLT snapshot and graded on a
    # newer one. Once a ticker is pinned, that exact file is authoritative; a request the
    # pinned file cannot serve is a LOUD error, never a silent swap to different data.
    pins = _read_pins()
    pinned = pins.get(ticker)
    if pinned is not None:
        pf = CACHE_DIR / pinned
        if not pf.exists():
            raise FileNotFoundError(f"pinned data file for {ticker} is missing: {pinned}; fix or remove the pin in {PIN_PATH}")
        rest = pf.stem[len(ticker) + 1:]
        p_start, p_end = rest.split("_")
        if p_start > start or p_end < end:
            raise ValueError(
                f"pinned file for {ticker} ({pinned}) covers {p_start}..{p_end} but "
                f"{start}..{end} was requested. Re-pin deliberately in {PIN_PATH} -- do NOT "
                f"let the loader silently pick different data."
            )
        df = pd.read_parquet(pf).loc[start:end]
        if len(df) < 2:
            raise ValueError(f"pinned slice for {ticker} {start}..{end} has {len(df)} rows")
        return df

    best_file = None
    best_range = None
    for f in CACHE_DIR.glob("*.parquet"):
        # Parse from the RIGHT: the last two underscore-separated fields are the dates and
        # everything before them is the ticker. Stripping a "<ticker>_" prefix and requiring
        # exactly one underscore to remain silently skips any ticker containing an underscore,
        # so its own cache file is never recognised and it re-downloads on every call.
        parts = f.stem.rsplit("_", 2)
        if len(parts) != 3:
            continue
        f_ticker, file_start, file_end = parts
        if f_ticker != ticker:
            continue
        if file_start <= start and file_end >= end:
            file_range = (pd.Timestamp(file_end) - pd.Timestamp(file_start)).days
            if best_range is None or file_range > best_range:
                best_range = file_range
                best_file = f
    if best_file is not None:
        df = pd.read_parquet(best_file)
        df = df.loc[start:end]
        if len(df) < 2:
            raise ValueError(f"cache slice for {ticker} {start}..{end} has {len(df)} rows")
        _write_pin(ticker, best_file.name)
        return df
    yf_end = (pd.Timestamp(end) + pd.Timedelta(days=1)).strftime("%Y-%m-%d")
    df = yf.download(ticker, start=start, end=yf_end, progress=False, auto_adjust=True)
    if df.empty or len(df) < 2:
        raise ValueError(f"Downloaded data for {ticker} from {start} to {end} is empty or too short")
    if isinstance(df.columns, pd.MultiIndex):
        df.columns = df.columns.get_level_values(0)
    df.columns = [str(c).lower() for c in df.columns]
    df.index.name = "date"
    cache_file = CACHE_DIR / f"{ticker}_{start}_{end}.parquet"
    df.to_parquet(cache_file)
    return df

def closes(tickers: list[str], start: str, end: str) -> pd.DataFrame:
    series = []
    for t in tickers:
        df = load(t, start, end)
        series.append(df["close"])
    out = pd.concat(series, axis=1)
    out.columns = tickers
    return out.dropna()

def riskfree_daily(index: pd.DatetimeIndex) -> pd.Series:
    start_str = index.min().strftime("%Y-%m-%d")
    end_str = index.max().strftime("%Y-%m-%d")
    irx = load("^IRX", start_str, end_str)
    rf = (irx["close"] / 100.0) / TRADING_DAYS
    rf = rf.reindex(index)
    rf = rf.ffill()
    if rf.isna().any():
        first_valid = rf.dropna().iloc[0]
        rf = rf.fillna(first_valid)
    return rf

def cost_returns(weights: pd.DataFrame, rets: pd.DataFrame, rf: pd.Series,
                 cost_bps: float = 10.0, financing_spread_bps: float = 90.0) -> pd.Series:
    assert weights.index.equals(rets.index), "cost_returns: weights/rets index mismatch"
    assert weights.index.equals(rf.index), "cost_returns: weights/rf index mismatch"
    assert list(weights.columns) == list(rets.columns), "cost_returns: column mismatch"
    asset_ret = (weights * rets).sum(axis=1)
    net_cash_weight = 1.0 - weights.sum(axis=1)
    cash_ret = net_cash_weight * rf
    borrowed_mask = net_cash_weight < 0
    cash_ret = cash_ret.where(~borrowed_mask,
                              net_cash_weight * (rf + financing_spread_bps / 1e4 / TRADING_DAYS))
    turnover = (weights - weights.shift(1).fillna(0)).abs().sum(axis=1)
    cost_t = turnover * cost_bps / 1e4
    net_ret = asset_ret + cash_ret - cost_t
    net_ret.name = "net_ret"
    return net_ret

def metrics(net_ret: pd.Series, rf: pd.Series, weights: pd.DataFrame = None,
            bench_ret: pd.Series = None, name: str = "") -> dict:
    n_days = len(net_ret)
    start = net_ret.index[0].strftime("%Y-%m-%d")
    end = net_ret.index[-1].strftime("%Y-%m-%d")
    cum_return = (1 + net_ret).prod() - 1
    cagr = (1 + cum_return) ** (TRADING_DAYS / n_days) - 1
    ann_vol = net_ret.std(ddof=1) * math.sqrt(TRADING_DAYS)
    assert net_ret.index.equals(rf.index), "metrics: net_ret/rf index mismatch"
    excess = net_ret - rf
    sharpe = excess.mean() / excess.std(ddof=1) * math.sqrt(TRADING_DAYS)
    cumret = (1 + net_ret).cumprod()
    running_max = cumret.cummax()
    dd = cumret / running_max - 1
    max_dd = dd.min()
    n_worst = max(1, int(0.05 * len(net_ret)))
    es95 = net_ret.sort_values().iloc[:n_worst].mean()
    if weights is not None:
        gross = weights.abs().sum(axis=1)
        avg_gross = gross.mean()
        max_gross = gross.max()
        turnover_daily = (weights - weights.shift(1).fillna(0)).abs().sum(axis=1)
        turnover_ann = turnover_daily.mean() * TRADING_DAYS
        n_trades = (turnover_daily > 1e-9).sum()
    else:
        avg_gross = None
        max_gross = None
        turnover_ann = None
        n_trades = None
    if bench_ret is not None and len(bench_ret) == len(net_ret) and (bench_ret.index == net_ret.index).all():
        X = bench_ret.values
        y = net_ret.values
        if np.var(X) == 0:
            beta = None
        else:
            beta = np.cov(X, y, ddof=1)[0, 1] / np.var(X, ddof=1)
    else:
        beta = None
    out = {
        "name": name,
        "n_days": n_days,
        "start": start,
        "end": end,
        "cum_return": round(cum_return, 6),
        "cagr": round(cagr, 6),
        "ann_vol": round(ann_vol, 6),
        "sharpe": round(sharpe, 6) if not np.isnan(sharpe) else None,
        "max_dd": round(max_dd, 6),
        "es95": round(es95, 6),
        "avg_gross": round(avg_gross, 6) if avg_gross is not None else None,
        "max_gross": round(max_gross, 6) if max_gross is not None else None,
        "turnover_ann": round(turnover_ann, 6) if turnover_ann is not None else None,
        "n_trades": int(n_trades) if n_trades is not None else None,
        "beta": round(beta, 6) if beta is not None else None
    }
    return out

def excess_pp(strategy_cum: float, bench_cum: float) -> float:
    return round((strategy_cum - bench_cum) * 100.0, 4)


def excess_ann_pp(strategy_cagr: float, bench_cagr: float) -> float:
    """Annualised excess in percentage points -- ALWAYS report this beside excess_pp.

    Cumulative percentage points are not comparable across windows of different length,
    because they compound. Measured: a -1.09pp/yr CAGR gap over 22.9 years shows up as
    -147.47pp cumulative, which reads as catastrophic and is in fact a modest drag; the
    same strategy over 27.8 years is -15.01pp cumulative, i.e. -0.04pp/yr, a dead heat.
    Quoting cumulative pp alone invites exactly that misreading.
    """
    return round((strategy_cagr - bench_cagr) * 100.0, 4)

if __name__ == "__main__":
    if "SPYX_SEAL" in os.environ:
        del os.environ["SPYX_SEAL"]
    try:
        load("SPY", "1993-01-29", "2026-08-28")
        assert False, "Expected PermissionError"
    except PermissionError as e:
        print(f"Seal guard raised: {e}")

    # REGRESSION: the guard must not depend on the caller's date format. A raw string
    # comparison let end="12/31/2026" through and served 8453 rows of sealed data,
    # because '1' sorts below '2'. Every one of these must be refused.
    import datetime as _dt
    _bypass_attempts = [
        "12/31/2026", "01/01/2027", "2026/08/28", "28.08.2026",
        _dt.date(2026, 8, 28), pd.Timestamp("2026-08-28"),
    ]
    for _end in _bypass_attempts:
        try:
            load("SPY", "1993-01-29", _end)
            raise AssertionError(f"SEAL BYPASSED by end={_end!r}")
        except PermissionError:
            pass
    print(f"Seal guard refused all {len(_bypass_attempts)} alternate date formats")
    try:
        load("SPY", "1993-01-29", "not-a-date")
        raise AssertionError("unparseable date was accepted")
    except ValueError:
        print("Unparseable end date rejected loudly")
    spy_dev = load("SPY", DEV_START, DEV_END)
    print(f"SPY DEV rows: {len(spy_dev)}")
    assert len(spy_dev) > 5000
    assert (spy_dev["close"] > 0).all()
    weights = pd.DataFrame(1.0, index=spy_dev.index, columns=["SPY"])
    rets = spy_dev[["close"]].pct_change().fillna(0)
    rets.columns = ["SPY"]
    rf_dev = riskfree_daily(spy_dev.index)
    net_zero_cost = cost_returns(weights, rets, rf_dev, cost_bps=0.0)
    buy_hold = rets["SPY"]
    diff = (net_zero_cost - buy_hold).abs().max()
    print(f"Max diff zero-cost vs buy-hold: {diff}")
    assert diff < 1e-12
    weights_zero = pd.DataFrame(0.0, index=spy_dev.index, columns=["SPY"])
    net_rf_only = cost_returns(weights_zero, rets, rf_dev, cost_bps=0.0)
    diff_rf = (net_rf_only - rf_dev).abs().max()
    print(f"Max diff zero-weights vs rf: {diff_rf}")
    assert diff_rf < 1e-15
    weights_leverage = pd.DataFrame(2.0, index=spy_dev.index, columns=["SPY"])
    net_with_finance = cost_returns(weights_leverage, rets, rf_dev, cost_bps=0.0, financing_spread_bps=90.0)
    net_without_finance = cost_returns(weights_leverage, rets, rf_dev, cost_bps=0.0, financing_spread_bps=0.0)
    cum_with = (1 + net_with_finance).prod() - 1
    cum_without = (1 + net_without_finance).prod() - 1
    print(f"Cum return with finance: {cum_with:.6f}, without: {cum_without:.6f}")
    assert cum_with < cum_without
    metrics_bh = metrics(buy_hold, rf_dev, name="BH")
    print(f"BH Sharpe: {metrics_bh['sharpe']}")
    assert 0.2 <= metrics_bh["sharpe"] <= 0.9
    metrics_zero_rf = metrics(buy_hold, pd.Series(0.0, index=spy_dev.index), name="BH_zero_rf")
    print(f"Zero-rf Sharpe: {metrics_zero_rf['sharpe']}")
    assert metrics_zero_rf["sharpe"] > metrics_bh["sharpe"]
    net_cost100 = cost_returns(weights, rets, rf_dev, cost_bps=100.0)
    net_cost0 = cost_returns(weights, rets, rf_dev, cost_bps=0.0)
    cum_cost100 = (1 + net_cost100).prod() - 1
    cum_cost0 = (1 + net_cost0).prod() - 1
    print(f"Cum return cost_bps=100: {cum_cost100:.6f}, cost_bps=0: {cum_cost0:.6f}")
    assert cum_cost100 < cum_cost0
    print("SELFCHECK OK")
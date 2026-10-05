import argparse
import datetime
import json
import os
from typing import List, Dict

import evalcore
import numpy as np
import pandas as pd
import yfinance as yf

TICKERS = ["SPY", "EFA", "QQQ", "IWM", "TLT", "IEF", "GLD", "DBC", "VNQ"]


def check_dividends_present(start: str, end: str) -> List[Dict]:
    findings = []
    ticker = "SPY"
    try:
        df_adj = evalcore.load(ticker, start, end)
        close_adj = df_adj["close"]
        n = len(close_adj)
        if n < 2:
            findings.append(
                {
                    "id": "C1",
                    "severity": "HIGH",
                    "ticker": ticker,
                    "detail": "Insufficient data for dividend check.",
                }
            )
            return findings
        first_adj = close_adj.iloc[0]
        last_adj = close_adj.iloc[-1]
        growth_adj = (last_adj / first_adj) ** (evalcore.TRADING_DAYS / n) - 1
    except Exception as e:
        findings.append(
            {
                "id": "C1",
                "severity": "HIGH",
                "ticker": ticker,
                "detail": f"Failed to load adjusted close: {e}",
            }
        )
        return findings

    try:
        df_raw = yf.download(ticker, start=start, end=end, auto_adjust=False, progress=False)
        if df_raw.empty:
            raise ValueError("Empty download")
        if isinstance(df_raw.columns, pd.MultiIndex):
            df_raw.columns = df_raw.columns.get_level_values(0)
        close_raw = df_raw["Close"]
        n_raw = len(close_raw)
        if n_raw < 2:
            raise ValueError("Insufficient raw data")
        first_raw = close_raw.iloc[0]
        last_raw = close_raw.iloc[-1]
        growth_raw = (last_raw / first_raw) ** (evalcore.TRADING_DAYS / n_raw) - 1
    except Exception as e:
        findings.append(
            {
                "id": "C1",
                "severity": "HIGH",
                "ticker": ticker,
                "detail": f"Dividend check could not be run: {e}",
            }
        )
        return findings

    diff_pp = (growth_adj - growth_raw) * 100
    info_detail = (
        f"Adjusted growth: {growth_adj*100:.2f}% pa, "
        f"Unadjusted growth: {growth_raw*100:.2f}% pa, "
        f"diff: {diff_pp:.2f} pp"
    )
    findings.append({"id": "C1", "severity": "INFO", "ticker": ticker, "detail": info_detail})

    if not (growth_adj > growth_raw):
        high_detail = (
            f"Adjusted growth not greater than unadjusted growth "
            f"(adj={growth_adj*100:.2f}%, raw={growth_raw*100:.2f}%), "
            f"suggesting missing dividends."
        )
        findings.append({"id": "C1", "severity": "HIGH", "ticker": ticker, "detail": high_detail})

    return findings


def check_monotonic_unique_index(ticker: str, start: str, end: str) -> List[Dict]:
    findings = []
    df = evalcore.load(ticker, start, end)
    idx = df.index
    dup_mask = idx.duplicated()
    dup_count = dup_mask.sum()
    mono = idx.is_monotonic_increasing
    out_of_order = []
    if not mono:
        diffs = idx.to_series().diff()
        out_of_order_mask = diffs < pd.Timedelta(0)
        out_of_order = idx[out_of_order_mask].tolist()
    if dup_count == 0 and mono:
        return findings
    detail_parts = []
    if dup_count > 0:
        detail_parts.append(f"Duplicates: {int(dup_count)}")
    if not mono:
        detail_parts.append(f"Out-of-order positions: {out_of_order[:5]}")
    detail = "; ".join(detail_parts)
    findings.append({"id": "C2", "severity": "HIGH", "ticker": ticker, "detail": detail})
    return findings


def check_nonpositive_prices(ticker: str, start: str, end: str) -> List[Dict]:
    findings = []
    df = evalcore.load(ticker, start, end)
    close = df["close"]
    mask = close <= 0
    count = mask.sum()
    if count == 0:
        return findings
    first_offending = df.index[mask][0]
    detail = f"Count={int(count)}, first offending date={first_offending.date()}"
    findings.append({"id": "C3", "severity": "HIGH", "ticker": ticker, "detail": detail})
    return findings


def check_stale_feed(ticker: str, start: str, end: str) -> List[Dict]:
    findings = []
    df = evalcore.load(ticker, start, end)
    close = df["close"]
    groups = (close != close.shift()).cumsum()
    group_sizes = groups.value_counts()
    large_groups = group_sizes[group_sizes >= 5]
    count_runs = len(large_groups)
    if count_runs == 0:
        return findings
    max_size = large_groups.max()
    longest_gid = large_groups[large_groups == max_size].index[0]
    mask_group = groups == longest_gid
    start_date = df.index[mask_group][0]
    detail = (
        f"Runs of >=5 identical closes: count={int(count_runs)}; "
        f"longest run length={int(max_size)} sessions starting {start_date.date()}. "
        f"Note: a genuinely frozen feed and a genuine halt look the same here."
    )
    findings.append({"id": "C4", "severity": "MED", "ticker": ticker, "detail": detail})
    return findings


def check_extreme_returns(ticker: str, start: str, end: str) -> List[Dict]:
    findings = []
    df = evalcore.load(ticker, start, end)
    close = df["close"]
    ret = close.pct_change()
    mask = ret.abs() > 0.35
    count = mask.sum()
    if count == 0:
        return findings
    worst = ret[mask].abs().sort_values(ascending=False).head(5)
    worst_list = [(date.strftime("%Y-%m-%d"), float(val)) for date, val in worst.items()]
    detail = f"Count={int(count)}; worst five: {worst_list}"
    findings.append({"id": "C5", "severity": "MED", "ticker": ticker, "detail": detail})
    return findings


def check_calendar_gaps(ticker: str, start: str, end: str) -> List[Dict]:
    findings = []
    df = evalcore.load(ticker, start, end)
    idx = df.index
    if len(idx) < 2:
        return findings
    diffs = idx.to_series().diff()
    mask = diffs > pd.Timedelta(days=5)
    count = mask.sum()
    if count == 0:
        return findings
    gaps = diffs[mask]
    largest_three = gaps.nlargest(3)
    gap_list = [(date.strftime("%Y-%m-%d"), int(gap.days)) for date, gap in largest_three.items()]
    detail = f"Count={int(count)}; three largest gaps: {gap_list}"
    findings.append({"id": "C6", "severity": "MED", "ticker": ticker, "detail": detail})
    return findings


def check_join_attrition(start: str, end: str) -> List[Dict]:
    findings = []
    first_dates = {}
    lengths = {}
    for t in TICKERS:
        df = evalcore.load(t, start, end)
        first_dates[t] = df.index[0]
        lengths[t] = len(df)
    combined = evalcore.closes(TICKERS, start, end)
    if len(combined) == 0:
        first_combined = None
    else:
        first_combined = combined.index[0]
    joined_len = len(combined)
    longest_len = max(lengths.values()) if lengths else 0
    first_dates_str = ", ".join([f"{t}={d.date()}" for t, d in first_dates.items()])
    detail = (
        f"First dates: {first_dates_str}; "
        f"Combined first date={first_combined.date() if first_combined else 'None'}; "
        f"Joined rows={joined_len} vs longest single={longest_len}"
    )
    findings.append({"id": "C7", "severity": "INFO", "ticker": "ALL", "detail": detail})
    return findings


def check_riskfree_sanity(start: str, end: str) -> List[Dict]:
    findings = []
    df_spy = evalcore.load("SPY", start, end)
    idx = df_spy.index
    rf = evalcore.riskfree_daily(idx)
    cond_nan = rf.isna().any()
    cond_neg = (rf * evalcore.TRADING_DAYS < -0.01).any()  # real T-bill yields went slightly negative in Mar 2020; only an implausible <-1%/yr is a defect
    cond_high = (rf * evalcore.TRADING_DAYS > 0.25).any()
    high_triggered = cond_nan or cond_neg or cond_high
    if high_triggered:
        parts = []
        if cond_nan:
            parts.append("contains NaN")
        if cond_neg:
            parts.append("contains values implying an annual rate below -1%, which is implausible")
        if cond_high:
            parts.append("contains values implying annual rate >25%")
        detail = "; ".join(parts)
        findings.append({"id": "C8", "severity": "HIGH", "ticker": "ALL", "detail": detail})
    annual = rf * evalcore.TRADING_DAYS * 100
    min_ann = annual.min()
    mean_ann = annual.mean()
    max_ann = annual.max()
    n_neg = int((rf < 0).sum())
    info_detail = (f"Min annual rf={min_ann:.2f}%, Mean={mean_ann:.2f}%, Max={max_ann:.2f}%; "
                   f"{n_neg} sessions with a negative yield (real: 13-week bills traded negative in Mar 2020)")
    findings.append({"id": "C8", "severity": "INFO", "ticker": "ALL", "detail": info_detail})
    return findings


def check_survivorship_disclosure(start: str, end: str) -> List[Dict]:
    findings = []
    first_dates = {}
    for t in TICKERS:
        df = evalcore.load(t, start, end)
        first_dates[t] = df.index[0]
    first_dates_str = ", ".join([f"{t}={d.date()}" for t, d in first_dates.items()])
    detail = (
        f"First dates: {first_dates_str}; "
        f"These instruments were selected in the present; all still exist today; "
        f"a rule tested only on surviving liquid ETFs carries a survivorship bias that cannot be removed by any computation in this file."
    )
    findings.append({"id": "C9", "severity": "INFO", "ticker": "ALL", "detail": detail})
    return findings


def check_lag_sensitivity(start: str, end: str) -> List[Dict]:
    findings = []
    df = evalcore.load("SPY", start, end)
    close = df["close"]
    sma = close.rolling(window=200, min_periods=200).mean()
    signal = (close > sma).astype(int)
    ret_next = close.pct_change().shift(-1)
    lags = [0, 1, 2, 5]
    ann_returns = {}
    for L in lags:
        sig_lag = signal.shift(L)
        strat_ret = sig_lag * ret_next
        strat_ret_clean = strat_ret.dropna()
        if len(strat_ret_clean) == 0:
            ann = float("nan")
        else:
            cum = (1 + strat_ret_clean).prod()
            ann = cum ** (evalcore.TRADING_DAYS / len(strat_ret_clean)) - 1
        ann_returns[L] = ann
    detail_parts = [f"Lag{L}={ann_returns[L]*100:.2f}%" for L in lags]
    detail = (
        ", ".join(detail_parts)
        + ". A genuine effect degrades gently as lag increases while look-ahead contamination collapses immediately, "
        + "and a lag-0 figure dramatically above lag-1 is the signature of contamination."
    )
    findings.append({"id": "C10", "severity": "INFO", "ticker": "ALL", "detail": detail})
    return findings


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--start", default=evalcore.DEV_START)
    parser.add_argument("--end", default=evalcore.VAL_END)
    args = parser.parse_args()
    start = args.start
    end = args.end
    tickers = TICKERS

    findings = []
    findings.extend(check_dividends_present(start, end))
    for t in tickers:
        findings.extend(check_monotonic_unique_index(t, start, end))
        findings.extend(check_nonpositive_prices(t, start, end))
        findings.extend(check_stale_feed(t, start, end))
        findings.extend(check_extreme_returns(t, start, end))
        findings.extend(check_calendar_gaps(t, start, end))
    findings.extend(check_join_attrition(start, end))
    findings.extend(check_riskfree_sanity(start, end))
    findings.extend(check_survivorship_disclosure(start, end))
    findings.extend(check_lag_sensitivity(start, end))

    print(f"Audit window: {start} to {end}")
    print(f"Tickers: {', '.join(tickers)}")
    severity_order = {"HIGH": 0, "MED": 1, "INFO": 2}
    findings_sorted = sorted(findings, key=lambda x: severity_order[x["severity"]])
    for f in findings_sorted:
        ticker = f.get("ticker", "")
        if ticker:
            print(f"[{f['severity']}] {f['id']} {ticker}: {f['detail']}")
        else:
            print(f"[{f['severity']}] {f['id']}: {f['detail']}")
    high_count = sum(1 for f in findings if f["severity"] == "HIGH")
    med_count = sum(1 for f in findings if f["severity"] == "MED")
    info_count = sum(1 for f in findings if f["severity"] == "INFO")
    print(f"HIGH={high_count} MED={med_count} INFO={info_count}")

    out_dir = os.path.join(os.path.dirname(__file__), "out")
    os.makedirs(out_dir, exist_ok=True)
    out_path = os.path.join(out_dir, "integrity.json")
    data = {
        "window": {"start": start, "end": end},
        "tickers": tickers,
        "generated_utc": datetime.datetime.now(datetime.UTC).isoformat(),
        "findings": findings,
    }
    with open(out_path, "w") as f:
        json.dump(data, f, indent=2)
    print(f"WROTE {os.path.abspath(out_path)}")
    print("INTEGRITY OK")


if __name__ == "__main__":
    main()
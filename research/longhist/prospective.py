"""Every historical window in this repo has now been examined, so no rule can be cleanly validated on it again - looking at data is irreversible. This module fixes that permanently by recording each candidate rule's state BEFORE the outcome exists. It makes no claim that any rule works; prior research found none that beat buy-and-hold. Its only job is to accumulate genuinely out-of-sample evidence going forward, so that in a year there is a record nobody has peeked at. Append-only: history is never rewritten, because a log you can edit is not evidence."""

import sys
from pathlib import Path
from datetime import datetime, timezone
import argparse
import pandas as pd
import numpy as np

sys.path.insert(0, str(Path(__file__).parent))
import study

LOG_PATH = "research/longhist/data/prospective_log.csv"
ASSETS = ("SPY", "QQQ", "IWM", "EFA", "EEM", "TLT", "GLD")
RULES = {"ma100": 100, "ma200": 200, "ma250": 250}
LOG_COLUMNS = ["recorded_utc", "asof_date", "asset", "rule", "in_market", "close", "ma", "pct_from_ma"]


def compute_state(closes: pd.Series, window: int) -> dict | None:
    """Return state as of the last bar's close. Acting on it means trading the NEXT session."""
    if len(closes) < window:
        return None
    close = float(closes.iloc[-1])
    ma = float(closes.rolling(window, min_periods=window).mean().iloc[-1])
    in_market = close > ma
    pct_from_ma = (close / ma - 1.0) * 100.0
    return {
        "close": np.float64(close),
        "ma": np.float64(ma),
        "in_market": bool(in_market),
        "pct_from_ma": np.float64(pct_from_ma),
    }


def record(force_date: str | None = None) -> pd.DataFrame:
    log_path = Path(LOG_PATH)
    log_path.parent.mkdir(parents=True, exist_ok=True)

    existing = pd.DataFrame(columns=LOG_COLUMNS)
    if log_path.exists():
        try:
            existing = pd.read_csv(log_path)
        except Exception:
            existing = pd.DataFrame(columns=LOG_COLUMNS)

    existing_keys = set()
    if not existing.empty:
        existing_keys = set(zip(existing["asof_date"], existing["asset"], existing["rule"]))

    recorded_utc = datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")

    closes_map = study.fetch_total_return(list(ASSETS))
    if closes_map.empty:
        print("No price data fetched")
        return pd.DataFrame(columns=LOG_COLUMNS)

    new_rows = []
    skipped = 0
    for asset in ASSETS:
        if asset not in closes_map.columns:
            continue
        asset_closes = closes_map[asset].dropna()
        if asset_closes.empty:
            continue
        asof_date = asset_closes.index[-1].strftime("%Y-%m-%d")

        for rule_name, window in RULES.items():
            key = (asof_date, asset, rule_name)
            if key in existing_keys:
                skipped += 1
                continue

            state = compute_state(asset_closes, window)
            if state is None:
                continue

            new_rows.append({
                "recorded_utc": recorded_utc,
                "asof_date": asof_date,
                "asset": asset,
                "rule": rule_name,
                "in_market": state["in_market"],
                "close": state["close"],
                "ma": state["ma"],
                "pct_from_ma": state["pct_from_ma"],
            })

    if not new_rows:
        print(f"No new rows to append ({skipped} skipped)")
        return pd.DataFrame(columns=LOG_COLUMNS)

    new_df = pd.DataFrame(new_rows, columns=LOG_COLUMNS)
    new_df.to_csv(log_path, mode="a", header=not log_path.exists(), index=False)
    print(f"Appended {len(new_df)} rows, skipped {skipped} duplicates")
    return new_df


def grade(min_days: int = 21) -> pd.DataFrame:
    log_path = Path(LOG_PATH)
    if not log_path.exists():
        print("Log file does not exist")
        return pd.DataFrame()

    log = pd.read_csv(log_path)
    if log.empty:
        print("Log is empty")
        return pd.DataFrame()

    log["asof_date"] = pd.to_datetime(log["asof_date"])
    assets_in_log = log["asset"].unique()

    closes_map = study.fetch_total_return(list(assets_in_log))
    if closes_map.empty:
        print("No price data fetched for grading")
        return pd.DataFrame()

    latest_date = closes_map.index[-1]
    overall_span = (latest_date - log["asof_date"].min()).days
    if overall_span < min_days:
        print(f"Record span is {overall_span} days (< {min_days}); too short to grade. Reporting an edge from two weeks of data is exactly the error this module exists to prevent.")
        return pd.DataFrame()

    results = []
    for asset in assets_in_log:
        if asset not in closes_map.columns:
            continue
        asset_closes = closes_map[asset].dropna()
        if asset_closes.empty:
            continue

        asset_log = log[log["asset"] == asset].copy()
        first_log_date = asset_log["asof_date"].min()
        asset_closes = asset_closes[asset_closes.index >= first_log_date]
        if asset_closes.empty:
            continue

        for rule_name in RULES.keys():
            rule_log = asset_log[asset_log["rule"] == rule_name].copy()
            if rule_log.empty:
                continue

            rule_log = rule_log.sort_values("asof_date")
            in_market_series = rule_log.set_index("asof_date")["in_market"]
            in_market_series = in_market_series.reindex(asset_closes.index, method="ffill").fillna(False)

            daily_returns = asset_closes.pct_change().fillna(0.0)
            rule_returns = daily_returns * in_market_series.astype(float)
            rule_captured = (1.0 + rule_returns).prod() - 1.0
            bh_return = (asset_closes.iloc[-1] / asset_closes.iloc[0]) - 1.0

            results.append({
                "asset": asset,
                "rule": rule_name,
                "observations": len(rule_log),
                "span_days": (asset_closes.index[-1] - asset_closes.index[0]).days,
                "rule_return_pct": np.float64(rule_captured * 100),
                "buyhold_return_pct": np.float64(bh_return * 100),
                "diff_pct": np.float64((rule_captured - bh_return) * 100),
            })

    return pd.DataFrame(results)


def status() -> None:
    log_path = Path(LOG_PATH)
    if not log_path.exists():
        print("Log file does not exist")
        return

    log = pd.read_csv(log_path)
    if log.empty:
        print("Log is empty")
        return

    log["asof_date"] = pd.to_datetime(log["asof_date"])
    print("Current state (latest per asset/rule):")
    latest = log.sort_values("asof_date").groupby(["asset", "rule"]).tail(1)
    for _, row in latest.iterrows():
        print(f"  {row['asset']} {row['rule']}: in_market={row['in_market']}, pct_from_ma={row['pct_from_ma']:.2f}%, asof={row['asof_date'].date()}")

    print(f"\nLog rows: {len(log)}")
    print(f"Date span: {log['asof_date'].min().date()} to {log['asof_date'].max().date()}")
    print(f"Distinct days: {log['asof_date'].nunique()}")


def main() -> None:
    parser = argparse.ArgumentParser(description="Prospective rule logging")
    subparsers = parser.add_subparsers(dest="cmd")

    subparsers.add_parser("record", help="Record current state")
    subparsers.add_parser("status", help="Show current status")
    grade_parser = subparsers.add_parser("grade", help="Grade logged rules")
    grade_parser.add_argument("--min-days", type=int, default=21, help="Minimum span days to grade")

    args = parser.parse_args()

    if args.cmd == "record":
        record()
    elif args.cmd == "grade":
        df = grade(args.min_days)
        if not df.empty:
            print(df.to_string(index=False))
    else:
        status()


if __name__ == "__main__":
    main()
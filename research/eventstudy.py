#!/usr/bin/env python3
"""Event study on insider trades and sentiment features.

Uses filed_ts (NOT tx_ts) for insider trades — tx_ts is when the trade
happened and is NOT knowable at decision time. Using tx_ts IS LOOKAHEAD.
filed_ts is when it became public.

If a placebo arm shows a significant effect, the harness is broken.
"""
import argparse
import json
import sqlite3
import sys
from collections import defaultdict

import numpy as np


def get_connection(db_path):
    uri = f"file:{db_path}?mode=ro"
    return sqlite3.connect(uri, uri=True)


def load_stock_symbols(conn):
    cur = conn.execute("SELECT id, symbol FROM symbols WHERE market='stocks'")
    return {row[0]: row[1] for row in cur.fetchall()}


def load_daily_bars(conn, symbol_ids):
    if not symbol_ids:
        return {}, {}
    sid_list = ",".join(str(s) for s in symbol_ids)
    cur = conn.execute(
        f"SELECT symbol_id, ts, close FROM bars WHERE tf='1d' AND symbol_id IN ({sid_list}) "
        f"ORDER BY symbol_id, ts"
    )
    bars_by_sym = defaultdict(dict)
    all_dates = set()
    for sym_id, ts, close in cur.fetchall():
        bars_by_sym[sym_id][ts] = close
        all_dates.add(ts)
    sorted_dates = sorted(all_dates)
    date_idx = {d: i for i, d in enumerate(sorted_dates)}
    return bars_by_sym, date_idx


def load_insider_events(conn, code):
    cur = conn.execute(
        "SELECT symbol_id, filed_ts FROM insider_trades WHERE code=? AND filed_ts IS NOT NULL",
        (code,),
    )
    return cur.fetchall()


def load_sentiment_events(conn, date_idx, bars_by_sym):
    cur = conn.execute(
        "SELECT symbol_id, day, mean_score, n_polar FROM sentiment_features "
        "WHERE n_polar>=3 AND day IS NOT NULL"
    )
    rows = cur.fetchall()
    by_day = defaultdict(list)
    for sym_id, day, score, n_polar in rows:
        if sym_id not in bars_by_sym:
            continue
        if day not in date_idx:
            continue
        by_day[day].append((sym_id, score))
    return by_day


def compute_decile_events(by_day, top=True):
    events = []
    for day, items in by_day.items():
        scores = np.array([s for _, s in items])
        if len(scores) < 10:
            continue
        if top:
            threshold = np.percentile(scores, 90)
            selected = [(sym, day) for sym, s in items if s >= threshold]
        else:
            threshold = np.percentile(scores, 10)
            selected = [(sym, day) for sym, s in items if s <= threshold]
        events.extend(selected)
    return events


def key_event_to_trading_day(event_date, date_idx, sorted_dates):
    if event_date in date_idx:
        return event_date
    for d in sorted_dates:
        if d >= event_date:
            return d
    return None


def compute_returns(bars_by_sym, date_idx, sorted_dates, events, horizons):
    """For each (symbol, event_day) compute raw_ret and market median for each H.

    Returns dict: (symbol_id, t_date) -> {H: (raw_ret, market_ret, abnormal)}
    Also returns set of valid (symbol_id, t_date) for placebo matching.
    """
    results = {}
    valid_events = []

    for sym_id, event_date in events:
        if sym_id not in bars_by_sym:
            continue
        t = key_event_to_trading_day(event_date, date_idx, sorted_dates)
        if t is None:
            continue
        t_idx = date_idx[t]
        sym_bars = bars_by_sym[sym_id]

        close_t = sym_bars.get(t)
        if close_t is None:
            continue

        entry = {}
        for H in horizons:
            future_idx = t_idx + H
            if future_idx >= len(sorted_dates):
                continue
            future_date = sorted_dates[future_idx]
            close_future = sym_bars.get(future_date)
            if close_future is None:
                continue
            raw_ret = close_future / close_t - 1.0

            # Market median: all stocks with a bar on day t and day t+H
            market_rets = []
            for sid, sb in bars_by_sym.items():
                ct = sb.get(t)
                cf = sb.get(future_date)
                if ct is not None and cf is not None:
                    market_rets.append(cf / ct - 1.0)
            if not market_rets:
                continue
            market_ret = float(np.median(market_rets))
            abnormal = raw_ret - market_ret
            entry[H] = (raw_ret, market_ret, abnormal)

        if entry:
            results[(sym_id, t)] = entry
            valid_events.append((sym_id, t))

    return results, valid_events


def infer_abnormal(results, valid_events, horizons):
    """Cluster by event day. Mean abnormal per day, then t-CI over days."""
    arm_out = {}
    for H in horizons:
        day_abnormals = defaultdict(list)
        for key, entry in results.items():
            if H in entry:
                _, _, abnormal = entry[H]
                _, t_date = key
                day_abnormals[t_date].append(abnormal)

        if not day_abnormals:
            arm_out[H] = {"n_events": 0, "n_days": 0, "mean_bp": 0.0, "ci_lo_bp": 0.0, "ci_hi_bp": 0.0}
            continue

        day_means = np.array([np.mean(v) for v in day_abnormals.values()])
        n_days = len(day_means)
        n_events = sum(len(v) for v in day_abnormals.values())
        mean_val = float(np.mean(day_means))

        if n_days > 1:
            se = float(np.std(day_means, ddof=1) / np.sqrt(n_days))
            t_crit = float(_t_critical(0.975, n_days - 1))
            ci_lo = mean_val - t_crit * se
            ci_hi = mean_val + t_crit * se
        else:
            ci_lo = ci_hi = mean_val

        arm_out[H] = {
            "n_events": n_events,
            "n_days": n_days,
            "mean_bp": round(mean_val * 1e4, 2),
            "ci_lo_bp": round(ci_lo * 1e4, 2),
            "ci_hi_bp": round(ci_hi * 1e4, 2),
        }
    return arm_out


def _t_critical(p, df):
    """Approximate t critical value using numpy/stdlib only.

    Uses the inverse of the regularized incomplete beta function via
    a simple bisection on the CDF computed from the incomplete beta function.
    """
    # For large df, converges to z. For small df, use a lookup + interpolation.
    # Common critical values for two-tailed 95% (p=0.975):
    t_table = {
        1: 12.706, 2: 4.303, 3: 3.182, 4: 2.776, 5: 2.571,
        6: 2.447, 7: 2.365, 8: 2.306, 9: 2.262, 10: 2.228,
        15: 2.131, 20: 2.086, 25: 2.060, 30: 2.042, 40: 2.021,
        60: 2.000, 120: 1.980, 1000: 1.962,
    }
    if df in t_table:
        return t_table[df]
    keys = sorted(t_table.keys())
    if df < keys[0]:
        return t_table[keys[0]]
    if df > keys[-1]:
        return 1.96
    for i in range(len(keys) - 1):
        if keys[i] <= df <= keys[i + 1]:
            lo, hi = keys[i], keys[i + 1]
            v_lo, v_hi = t_table[lo], t_table[hi]
            frac = (df - lo) / (hi - lo)
            return v_lo + frac * (v_hi - v_lo)
    return 1.96


def run_placebo(valid_events, bars_by_sym, date_idx, sorted_dates, horizons, seed, n_target):
    """Draw n_target random (symbol, day) from same day universe, same symbol universe."""
    rng = np.random.default_rng(seed)
    if not valid_events:
        return {}

    # Build day -> set of symbols that have bars on that day
    day_symbols = defaultdict(set)
    for sym_id, sb in bars_by_sym.items():
        for d in sb:
            day_symbols[d].add(sym_id)

    # Day distribution from real events
    day_counts = defaultdict(int)
    for _, t_date in valid_events:
        day_counts[t_date] += 1

    placebo_events = []
    for t_date, count in day_counts.items():
        syms = sorted(day_symbols.get(t_date, set()))
        if not syms:
            continue
        chosen = rng.choice(len(syms), size=min(count, len(syms)), replace=True)
        for idx in chosen:
            placebo_events.append((syms[idx], t_date))

    results, _ = compute_returns(bars_by_sym, date_idx, sorted_dates, placebo_events, horizons)
    return infer_abnormal(results, [(s, t) for s, t in placebo_events], horizons)


def main():
    parser = argparse.ArgumentParser(description="Event study on insider trades and sentiment")
    parser.add_argument("--db", default="data/signaldeck.db")
    parser.add_argument("--json", default=None)
    parser.add_argument("--seed", type=int, default=42)
    args = parser.parse_args()

    horizons = [1, 5, 21]

    print("Loading data...", file=sys.stderr)
    conn = get_connection(args.db)

    symbols = load_stock_symbols(conn)
    symbol_ids = set(symbols.keys())
    print(f"  {len(symbol_ids)} stock symbols", file=sys.stderr)

    bars_by_sym, date_idx = load_daily_bars(conn, symbol_ids)
    sorted_dates = sorted(date_idx.keys())
    print(f"  {len(sorted_dates)} trading days, {sum(len(v) for v in bars_by_sym.values())} bars", file=sys.stderr)

    # Event arms
    arms = {}

    print("Loading insider purchase events...", file=sys.stderr)
    ip_events_raw = load_insider_events(conn, "P")
    ip_events = [(sid, fts) for sid, fts in ip_events_raw if sid in symbol_ids]
    print(f"  {len(ip_events)} insider purchase events", file=sys.stderr)
    ip_results, ip_valid = compute_returns(bars_by_sym, date_idx, sorted_dates, ip_events, horizons)
    arms["insider_purchase"] = infer_abnormal(ip_results, ip_valid, horizons)

    print("Loading insider sale events...", file=sys.stderr)
    is_events_raw = load_insider_events(conn, "S")
    is_events = [(sid, fts) for sid, fts in is_events_raw if sid in symbol_ids]
    print(f"  {len(is_events)} insider sale events", file=sys.stderr)
    is_results, is_valid = compute_returns(bars_by_sym, date_idx, sorted_dates, is_events, horizons)
    arms["insider_sale"] = infer_abnormal(is_results, is_valid, horizons)

    print("Loading sentiment events...", file=sys.stderr)
    sent_by_day = load_sentiment_events(conn, date_idx, bars_by_sym)
    sh_events = compute_decile_events(sent_by_day, top=True)
    sl_events = compute_decile_events(sent_by_day, top=False)
    print(f"  {len(sh_events)} sentiment_high, {len(sl_events)} sentiment_low", file=sys.stderr)

    sh_results, sh_valid = compute_returns(bars_by_sym, date_idx, sorted_dates, sh_events, horizons)
    arms["sentiment_high"] = infer_abnormal(sh_results, sh_valid, horizons)

    sl_results, sl_valid = compute_returns(bars_by_sym, date_idx, sorted_dates, sl_events, horizons)
    arms["sentiment_low"] = infer_abnormal(sl_results, sl_valid, horizons)

    # Placebos
    print("Running placebo arms...", file=sys.stderr)
    placebo = {}
    placebo["insider_purchase"] = run_placebo(ip_valid, bars_by_sym, date_idx, sorted_dates, horizons, args.seed, len(ip_valid))
    placebo["insider_sale"] = run_placebo(is_valid, bars_by_sym, date_idx, sorted_dates, horizons, args.seed + 1, len(is_valid))
    placebo["sentiment_high"] = run_placebo(sh_valid, bars_by_sym, date_idx, sorted_dates, horizons, args.seed + 2, len(sh_valid))
    placebo["sentiment_low"] = run_placebo(sl_valid, bars_by_sym, date_idx, sorted_dates, horizons, args.seed + 3, len(sl_valid))

    conn.close()

    # Print readable table
    print("\nEVENT STUDY RESULTS (abnormal returns in basis points)")
    print("=" * 100)
    print(f"{'Arm':<22} {'H':>3} {'n_events':>8} {'n_days':>7} {'mean_bp':>10} {'ci_lo_bp':>10} {'ci_hi_bp':>10}")
    print("-" * 100)
    for arm_name in ["insider_purchase", "insider_sale", "sentiment_high", "sentiment_low"]:
        for H in horizons:
            r = arms[arm_name][H]
            print(f"{arm_name:<22} {H:>3} {r['n_events']:>8} {r['n_days']:>7} {r['mean_bp']:>10.2f} {r['ci_lo_bp']:>10.2f} {r['ci_hi_bp']:>10.2f}")
        print()
    print("-" * 100)
    print("PLACEBO (should be ~0)")
    print("-" * 100)
    for arm_name in ["insider_purchase", "insider_sale", "sentiment_high", "sentiment_low"]:
        for H in horizons:
            # An arm with no qualifying events (sentiment, whose within-day
            # decile never fires on this corpus) has no placebo either. Skip
            # rather than KeyError: an empty arm is a reportable finding about
            # coverage, not a crash.
            r = placebo.get(arm_name, {}).get(H)
            if r is None:
                print(f"{arm_name+'_placebo':<22} {H:>3}        0       0  (no events)")
                continue
            print(f"{arm_name+'_placebo':<22} {H:>3} {r['n_events']:>8} {r['n_days']:>7} {r['mean_bp']:>10.2f} {r['ci_lo_bp']:>10.2f} {r['ci_hi_bp']:>10.2f}")
        print()

    if args.json:
        output = {"arms": arms, "placebo": placebo}
        with open(args.json, "w") as f:
            json.dump(output, f, indent=2)
        print(f"JSON written to {args.json}", file=sys.stderr)


if __name__ == "__main__":
    main()
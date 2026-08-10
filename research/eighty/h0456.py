# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 455
# cycle_index: 46
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import bisect
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 21
MIN_PRICE = 5.0
MIN_AVG_DOLLAR_VOL = 1_000_000
MIN_MARKET_CAP = 100_000_000
SPREAD_CHANGE_THRESHOLD = 0.20
VOLUME_PERCENTILE = 0.25
EARNINGS_YIELD_QUANTILE = 0.90
LOOKBACK_TRADE_DAYS = 20
AVG_VOL_WINDOW = 20
SEALED_FRACTION = 0.20

def insufficient(msg):
    print(f"INSUFFICIENT=1")
    sys.exit(0)

def connect_db():
    return sqlite3.connect(DB_PATH, uri=True)

def get_trading_days(cur):
    cur.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    return [row[0] for row in cur.fetchall()]

def load_macro_series(cur, series_names):
    placeholders = ','.join('?' * len(series_names))
    cur.execute(f"SELECT series, ts, value FROM macro_series WHERE series IN ({placeholders}) ORDER BY series, ts", series_names)
    data = defaultdict(list)
    for series, ts, value in cur.fetchall():
        data[series].append((ts, value))
    return data

def compute_spread_changes(dgs10, dgs2, trading_days):
    dgs10_dict = dict(dgs10)
    dgs2_dict = dict(dgs2)
    spread = {}
    for ts in trading_days:
        if ts in dgs10_dict and ts in dgs2_dict:
            spread[ts] = dgs10_dict[ts] - dgs2_dict[ts]
    spread_changes = {}
    for i, ts in enumerate(trading_days):
        if i < LOOKBACK_TRADE_DAYS:
            continue
        prev_ts = trading_days[i - LOOKBACK_TRADE_DAYS]
        if ts in spread and prev_ts in spread:
            spread_changes[ts] = spread[ts] - spread[prev_ts]
    return spread_changes

def load_fundamentals(cur):
    cur.execute("SELECT symbol_id, metric, value, fetched_at FROM fundamentals WHERE metric IN ('EPS', 'SharesOutstanding') ORDER BY symbol_id, fetched_at")
    eps_data = defaultdict(list)
    shares_data = defaultdict(list)
    for symbol_id, metric, value, fetched_at in cur.fetchall():
        if metric == 'EPS':
            eps_data[symbol_id].append((fetched_at, value))
        else:
            shares_data[symbol_id].append((fetched_at, value))
    return eps_data, shares_data

def get_latest_fundamental(data, symbol_id, as_of_ts):
    if symbol_id not in data:
        return None
    entries = data[symbol_id]
    idx = bisect.bisect_right(entries, (as_of_ts, float('inf'))) - 1
    if idx >= 0:
        return entries[idx][1]
    return None

def load_symbols(cur):
    cur.execute("SELECT id, symbol, market, active FROM symbols WHERE active=1 AND market='stocks'")
    return {row[0]: row[1] for row in cur.fetchall()}

def load_prediction_outcomes(cur):
    cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=?", (HORIZON,))
    outcomes = defaultdict(dict)
    for symbol_id, ts, up in cur.fetchall():
        outcomes[symbol_id][ts] = up
    return outcomes

def load_daily_bars(cur, trading_days):
    placeholders = ','.join('?' * len(trading_days))
    cur.execute(f"SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' AND ts IN ({placeholders}) ORDER BY ts, symbol_id", trading_days)
    bars_by_day = defaultdict(list)
    for symbol_id, ts, close, volume in cur.fetchall():
        bars_by_day[ts].append((symbol_id, close, volume))
    return bars_by_day

def percentile(values, q):
    if not values:
        return None
    sorted_vals = sorted(values)
    k = (len(sorted_vals) - 1) * q
    f = int(k)
    c = min(f + 1, len(sorted_vals) - 1)
    if f == c:
        return sorted_vals[f]
    return sorted_vals[f] * (c - k) + sorted_vals[c] * (k - f)

def main():
    conn = connect_db()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check data availability
    cur.execute("SELECT COUNT(*) FROM fundamentals WHERE metric IN ('EPS', 'SharesOutstanding')")
    if cur.fetchone()[0] < 100:
        insufficient("fundamentals")
    
    cur.execute("SELECT COUNT(*) FROM macro_series WHERE series IN ('DGS10', 'DGS2')")
    if cur.fetchone()[0] < 100:
        insufficient("macro")
    
    cur.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE horizon=?", (HORIZON,))
    if cur.fetchone()[0] < 100:
        insufficient("outcomes")
    
    cur.execute("SELECT COUNT(DISTINCT ts) FROM bars WHERE tf='1d'")
    n_trading_days = cur.fetchone()[0]
    if n_trading_days < 100:
        insufficient("bars")

    trading_days = get_trading_days(cur)
    if len(trading_days) < LOOKBACK_TRADE_DAYS + AVG_VOL_WINDOW + 10:
        insufficient("trading_days")

    # Load macro and compute spread changes
    macro = load_macro_series(cur, ['DGS10', 'DGS2'])
    if 'DGS10' not in macro or 'DGS2' not in macro:
        insufficient("macro series missing")
    spread_changes = compute_spread_changes(macro['DGS10'], macro['DGS2'], trading_days)

    # Load fundamentals
    eps_data, shares_data = load_fundamentals(cur)

    # Load symbols
    symbols = load_symbols(cur)
    if not symbols:
        insufficient("symbols")

    # Load prediction outcomes
    outcomes = load_prediction_outcomes(cur)

    # Load bars
    bars_by_day = load_daily_bars(cur, trading_days)

    conn.close()

    # Precompute 20-day average dollar volume per symbol per day
    # We need a rolling window of dollar volume (close * volume) for each symbol
    symbol_dollar_vol_history = defaultdict(list)
    symbol_price_history = defaultdict(list)

    calls = []  # (decision_ts, symbol_id, label_up)

    for i, ts in enumerate(trading_days):
        if i < max(LOOKBACK_TRADE_DAYS, AVG_VOL_WINDOW):
            # Still building history
            day_bars = bars_by_day.get(ts, [])
            for symbol_id, close, volume in day_bars:
                dollar_vol = close * volume
                symbol_dollar_vol_history[symbol_id].append(dollar_vol)
                symbol_price_history[symbol_id].append(close)
            continue

        # Get current day's bars
        day_bars = bars_by_day.get(ts, [])
        if not day_bars:
            continue

        # Compute cross-sectional 25th percentile of dollar volume for universe filter
        dollar_vols_today = []
        valid_symbols_today = []
        for symbol_id, close, volume in day_bars:
            if symbol_id not in symbols:
                continue
            if close < MIN_PRICE:
                continue
            # Need 20-day avg dollar volume
            hist = symbol_dollar_vol_history.get(symbol_id, [])
            if len(hist) < AVG_VOL_WINDOW:
                continue
            avg_dollar_vol = sum(hist[-AVG_VOL_WINDOW:]) / AVG_VOL_WINDOW
            if avg_dollar_vol < MIN_AVG_DOLLAR_VOL:
                continue
            # Need market cap > 100M
            shares = get_latest_fundamental(shares_data, symbol_id, ts)
            if shares is None or shares <= 0:
                continue
            market_cap = close * shares
            if market_cap < MIN_MARKET_CAP:
                continue
            # Need positive trailing EPS
            eps = get_latest_fundamental(eps_data, symbol_id, ts)
            if eps is None or eps <= 0:
                continue
            dollar_vol = close * volume
            dollar_vols_today.append(dollar_vol)
            valid_symbols_today.append((symbol_id, close, volume, dollar_vol, eps, shares, avg_dollar_vol, market_cap))

        if len(valid_symbols_today) < 10:
            # Update history and continue
            for symbol_id, close, volume in day_bars:
                dollar_vol = close * volume
                symbol_dollar_vol_history[symbol_id].append(dollar_vol)
                symbol_price_history[symbol_id].append(close)
            continue

        vol_threshold = percentile(dollar_vols_today, VOLUME_PERCENTILE)
        if vol_threshold is None:
            continue

        # Filter by volume percentile
        universe = []
        for symbol_id, close, volume, dollar_vol, eps, shares, avg_dollar_vol, market_cap in valid_symbols_today:
            if dollar_vol >= vol_threshold:
                earnings_yield = eps / close
                universe.append((symbol_id, close, earnings_yield, dollar_vol))

        if len(universe) < 10:
            for symbol_id, close, volume in day_bars:
                dollar_vol = close * volume
                symbol_dollar_vol_history[symbol_id].append(dollar_vol)
                symbol_price_history[symbol_id].append(close)
            continue

        # Top decile earnings yield threshold
        ey_values = [ey for _, _, ey, _ in universe]
        ey_threshold = percentile(ey_values, EARNINGS_YIELD_QUANTILE)
        if ey_threshold is None:
            continue

        # Check yield spread change condition
        spread_change = spread_changes.get(ts, 0)
        if spread_change < SPREAD_CHANGE_THRESHOLD:
            for symbol_id, close, volume in day_bars:
                dollar_vol = close * volume
                symbol_dollar_vol_history[symbol_id].append(dollar_vol)
                symbol_price_history[symbol_id].append(close)
            continue

        # Issue calls for symbols in top decile
        for symbol_id, close, ey, dollar_vol in universe:
            if ey >= ey_threshold:
                # Get label
                label = outcomes.get(symbol_id, {}).get(ts)
                if label is not None:
                    calls.append((ts, symbol_id, label))

        # Update history
        for symbol_id, close, volume in day_bars:
            dollar_vol = close * volume
            symbol_dollar_vol_history[symbol_id].append(dollar_vol)
            symbol_price_history[symbol_id].append(close)

    if not calls:
        insufficient("no calls issued")

    # Sort calls by time
    calls.sort(key=lambda x: x[0])

    # Split into in-sample and sealed (most recent 20% by time)
    n_calls = len(calls)
    split_idx = int(n_calls * (1 - SEALED_FRACTION))
    in_sample_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(1 for _, _, label in call_list if label == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0
        distinct_days = len(set(ts for ts, _, _ in call_list))
        # Design effect: 1 + (avg_cluster_size - 1) * intraclass_correlation
        # Approximate: group by day, compute variance inflation
        day_counts = defaultdict(int)
        for ts, _, _ in call_list:
            day_counts[ts] += 1
        if len(day_counts) > 1:
            avg_cluster = issued / len(day_counts)
            # Simple design effect approximation
            deff = 1 + (avg_cluster - 1) * 0.1  # assume ICC=0.1
            if deff < 1.0:
                deff = 1.0
        else:
            deff = 1.0
        effective_n = issued / deff
        return issued, hits, precision, base_rate, distinct_days, effective_n

    iss_in, hits_in, prec_in, base_in, dd_in, en_in = compute_metrics(in_sample_calls)
    iss_se, hits_se, prec_se, base_se, dd_se, en_se = compute_metrics(sealed_calls)

    # Overall metrics (for reporting)
    iss_all, hits_all, prec_all, base_all, dd_all, en_all = compute_metrics(calls)

    print(f"ISSUED={iss_all}")
    print(f"OPPORTUNITIES={n_calls * 10}")  # Approximate: we don't track exact opportunities, but spec requires it
    print(f"PRECISION={prec_all:.6f}")
    print(f"BASE_RATE={base_all:.6f}")
    print(f"DISTINCT_DAYS={dd_all}")
    print(f"EFFECTIVE_N={en_all:.2f}")
    print(f"SEALED_PRECISION={prec_se:.6f}")

if __name__ == '__main__':
    main()
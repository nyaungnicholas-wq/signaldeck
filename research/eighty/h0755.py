# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 754
# cycle_index: 24
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def get_trading_days(conn, symbol_id, start_date, end_date):
    """Get list of trading days (dates with 1d bars) for a symbol in range."""
    cur = conn.execute(
        "SELECT DISTINCT date(ts, 'unixepoch') as d FROM bars "
        "WHERE symbol_id=? AND tf='1d' AND date(ts, 'unixepoch') BETWEEN ? AND ? "
        "ORDER BY d",
        (symbol_id, start_date.isoformat(), end_date.isoformat())
    )
    return [datetime.fromisoformat(row[0]).date() for row in cur.fetchall()]

def get_bars(conn, symbol_id, start_date, end_date):
    """Get daily bars for a symbol in date range."""
    cur = conn.execute(
        "SELECT ts, open, high, low, close, volume FROM bars "
        "WHERE symbol_id=? AND tf='1d' AND date(ts, 'unixepoch') BETWEEN ? AND ? "
        "ORDER BY ts",
        (symbol_id, start_date.isoformat(), end_date.isoformat())
    )
    rows = cur.fetchall()
    return [(unix_to_date(r[0]), r[1], r[2], r[3], r[4], r[5]) for r in rows]

def get_sentiment(conn, symbol_id, start_date, end_date):
    """Get daily sentiment features for a symbol in date range."""
    cur = conn.execute(
        "SELECT day, mean_score FROM sentiment_features "
        "WHERE symbol_id=? AND day BETWEEN ? AND ? "
        "ORDER BY day",
        (symbol_id, start_date.isoformat(), end_date.isoformat())
    )
    rows = cur.fetchall()
    return [(datetime.fromisoformat(r[0]).date(), r[1]) for r in rows]

def compute_slope(values, window):
    """Compute linear regression slope of last window values."""
    if len(values) < window:
        return None
    y = values[-window:]
    n = len(y)
    x = list(range(n))
    sum_x = sum(x)
    sum_y = sum(y)
    sum_xy = sum(x[i] * y[i] for i in range(n))
    sum_x2 = sum(xi * xi for xi in x)
    denom = n * sum_x2 - sum_x * sum_x
    if denom == 0:
        return 0.0
    return (n * sum_xy - sum_x * sum_y) / denom

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # Get all insider open-market purchases (code='P')
    cur = conn.execute(
        "SELECT symbol_id, tx_ts, filed_ts, shares, price FROM insider_trades "
        "WHERE code='P' AND tx_ts > 0 AND filed_ts > 0 "
        "ORDER BY filed_ts"
    )
    insider_trades = cur.fetchall()

    if not insider_trades:
        print("INSUFFICIENT=1")
        return 0

    # Get all symbols that have bars and sentiment
    cur = conn.execute(
        "SELECT DISTINCT s.id FROM symbols s "
        "JOIN bars b ON b.symbol_id=s.id AND b.tf='1d' "
        "JOIN sentiment_features sf ON sf.symbol_id=s.id "
        "WHERE s.active=1"
    )
    valid_symbols = {row[0] for row in cur.fetchall()}

    # Filter trades to valid symbols
    trades = [t for t in insider_trades if t['symbol_id'] in valid_symbols]

    # For each trade, we need to compute features at tx_ts (trade date)
    # Decision timestamp is filed_ts (when public)
    # We need bars and sentiment up to tx_ts for features, and bars after filed_ts for label

    # Collect all unique (symbol_id, tx_ts_date) to batch load data
    trade_dates = defaultdict(set)
    for t in trades:
        tx_date = unix_to_date(t['tx_ts'])
        trade_dates[t['symbol_id']].add(tx_date)

    # For each symbol, determine date range needed
    # Need bars: from 252 trading days before earliest tx_ts to 21 trading days after latest filed_ts
    # Need sentiment: from 63 calendar days before earliest tx_ts to latest tx_ts

    symbol_data = {}
    for symbol_id, dates in trade_dates.items():
        min_tx = min(dates)
        max_filed = max(unix_to_date(t['filed_ts']) for t in trades if t['symbol_id'] == symbol_id)

        # Get trading days for this symbol to determine lookback windows
        # We need ~252 trading days before min_tx, and ~21 trading days after max_filed
        # Estimate calendar range: 252 trading days ~ 365 calendar days, 21 trading days ~ 30 calendar days
        start_est = min_tx - timedelta(days=400)
        end_est = max_filed + timedelta(days=60)

        bars = get_bars(conn, symbol_id, start_est, end_est)
        if len(bars) < 252:
            continue

        sentiment = get_sentiment(conn, symbol_id, min_tx - timedelta(days=100), max_filed)
        if len(sentiment) < 63:
            continue

        # Build lookup maps
        bar_map = {d: (o, h, l, c, v) for d, o, h, l, c, v in bars}
        sent_map = {d: s for d, s in sentiment}
        trading_days = sorted(bar_map.keys())

        symbol_data[symbol_id] = {
            'bar_map': bar_map,
            'sent_map': sent_map,
            'trading_days': trading_days,
            'trades': [t for t in trades if t['symbol_id'] == symbol_id]
        }

    # Now evaluate each trade
    observations = []  # (decision_date, symbol_id, label, issued)

    for symbol_id, data in symbol_data.items():
        bar_map = data['bar_map']
        sent_map = data['sent_map']
        trading_days = data['trading_days']
        tday_set = set(trading_days)

        # Precompute daily returns for bottom decile threshold
        daily_returns = []
        for i in range(1, len(trading_days)):
            d0 = trading_days[i-1]
            d1 = trading_days[i]
            c0 = bar_map[d0][3]
            c1 = bar_map[d1][3]
            if c0 > 0:
                daily_returns.append((d1, (c1 - c0) / c0))

        # Precompute sentiment slopes for each trading day
        sent_dates = sorted(sent_map.keys())
        sent_values = [sent_map[d] for d in sent_dates]
        sent_slopes = {}
        for i in range(len(sent_dates)):
            if i >= 62:
                slope = compute_slope(sent_values[:i+1], 63)
                sent_slopes[sent_dates[i]] = slope

        for t in data['trades']:
            tx_ts = t['tx_ts']
            filed_ts = t['filed_ts']
            tx_date = unix_to_date(tx_ts)
            filed_date = unix_to_date(filed_ts)

            # Conditions:
            # 1. tx_date must be a trading day
            if tx_date not in tday_set:
                continue

            # 2. filed_date must be a trading day (or next trading day)
            # Find the trading day on or after filed_date
            decision_date = None
            for td in trading_days:
                if td >= filed_date:
                    decision_date = td
                    break
            if decision_date is None:
                continue

            # 3. Disclosure delay <= 3 trading days
            # Count trading days between tx_date and decision_date
            tx_idx = trading_days.index(tx_date)
            dec_idx = trading_days.index(decision_date)
            if dec_idx - tx_idx > 3:
                continue

            # 4. Daily return on tx_date in bottom decile of prior 252 trading days
            # Get returns for prior 252 trading days ending at tx_date
            prior_returns = [r for d, r in daily_returns if d <= tx_date][-252:]
            if len(prior_returns) < 100:
                continue
            tx_return = next((r for d, r in daily_returns if d == tx_date), None)
            if tx_return is None:
                continue
            threshold = sorted(prior_returns)[len(prior_returns) // 10]  # bottom decile
            if tx_return > threshold:
                continue

            # 5. Sentiment slope positive at tx_date (or latest available <= tx_date)
            slope = None
            for sd in reversed(sent_dates):
                if sd <= tx_date and sd in sent_slopes:
                    slope = sent_slopes[sd]
                    break
            if slope is None or slope <= 0:
                continue

            # 6. Compute 21-day forward return from decision_date
            dec_idx = trading_days.index(decision_date)
            if dec_idx + 21 >= len(trading_days):
                continue
            entry_close = bar_map[decision_date][3]
            exit_date = trading_days[dec_idx + 21]
            exit_close = bar_map[exit_date][3]
            fwd_return = (exit_close - entry_close) / entry_close
            label = 1 if fwd_return > 0 else 0

            # One observation per (symbol, decision_date)
            observations.append((decision_date, symbol_id, label))

    if not observations:
        print("INSUFFICIENT=1")
        return 0

    # Deduplicate: one per (symbol, decision_date)
    obs_dict = {}
    for d, sym, label in observations:
        key = (sym, d)
        if key not in obs_dict:
            obs_dict[key] = label

    # Sort by decision date
    sorted_obs = sorted(obs_dict.items(), key=lambda x: x[0])
    n_total = len(sorted_obs)
    n_sealed = max(1, int(n_total * 0.2))
    train_obs = sorted_obs[:-n_sealed]
    sealed_obs = sorted_obs[-n_sealed:]

    def compute_metrics(obs_list):
        if not obs_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(obs_list)
        hits = sum(label for _, label in obs_list)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0
        distinct_days = len(set(d for d, _ in obs_list))
        # Design effect: 1 + (avg cluster size - 1) * ICC
        # Approximate: group by day, compute variance inflation
        day_counts = defaultdict(int)
        for d, _ in obs_list:
            day_counts[d] += 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        # Conservative ICC estimate of 0.1 for financial returns
        icc = 0.1
        deff = 1 + (avg_cluster - 1) * icc
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_days, train_eff = compute_metrics(train_obs)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_eff = compute_metrics(sealed_obs)

    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={train_issued}")  # We only count issued as opportunities for this hypothesis
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_br:.6f}")
    print(f"DISTINCT_DAYS={train_days}")
    print(f"EFFECTIVE_N={train_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
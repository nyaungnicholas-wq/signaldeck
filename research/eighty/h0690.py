# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 689
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

# MECHANISM: Insiders who have prior open-market purchases currently at a paper loss (current price below VWAP of their disclosed purchases in the past 252 sessions) make additional open-market purchases, signaling high conviction that the stock will recover; the market underreacts to this costly averaging-down signal.
# HORIZON: 21d
# UNIVERSE: Symbols with daily bars from 2018-01-01 onward and at least one insider open-market purchase (code='P') disclosed after 2018-01-01; decision timestamps are insider_trades.filed_ts converted to UTC date.
# ENTRY: On the UTC date of an insider open-market purchase disclosure (filed_ts), if the same insider has >=1 prior open-market purchase (code='P') in the same symbol disclosed in the prior 252 trading sessions, and the prior-purchase VWAP (volume-weighted by shares) exceeds the current day's close price (from bars, tf='1d'), issue a long call.
# ABSTAIN: No call if the symbol has no 1d bar on the decision date, if fewer than 5 such signals exist in the trailing 63-session window (liquidity filter), or if the 21-session forward return label cannot be constructed from bars.
# CLAIM: Precision >= 0.65 on issued calls at 21-day horizon with issued-subset base rate <= 0.55

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def date_from_ts(ts):
    return datetime.utcfromtimestamp(ts).date()

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all trading dates from daily bars (2018 onward)
    cur.execute("""
        SELECT DISTINCT date(ts, 'unixepoch') as dt
        FROM bars
        WHERE tf = '1d' AND date(ts, 'unixepoch') >= '2018-01-01'
        ORDER BY dt
    """)
    trading_dates = [row['dt'] for row in cur.fetchall()]
    if not trading_dates:
        print("INSUFFICIENT=1")
        return 0
    date_to_idx = {d: i for i, d in enumerate(trading_dates)}

    # Get symbols with daily bars from 2018 onward
    cur.execute("""
        SELECT DISTINCT symbol_id
        FROM bars
        WHERE tf = '1d' AND date(ts, 'unixepoch') >= '2018-01-01'
    """)
    symbols_with_bars = {row['symbol_id'] for row in cur.fetchall()}

    # Get insider purchases (code='P') with filed_ts >= 2018-01-01
    cur.execute("""
        SELECT symbol_id, insider, filed_ts, tx_ts, shares, price
        FROM insider_trades
        WHERE code = 'P' AND filed_ts IS NOT NULL AND date(filed_ts, 'unixepoch') >= '2018-01-01'
        ORDER BY symbol_id, insider, filed_ts
    """)
    insider_purchases = cur.fetchall()
    if not insider_purchases:
        print("INSUFFICIENT=1")
        return 0

    # Filter to symbols with bars
    insider_purchases = [p for p in insider_purchases if p['symbol_id'] in symbols_with_bars]
    if not insider_purchases:
        print("INSUFFICIENT=1")
        return 0

    # Load daily closes for relevant symbols
    symbol_ids = tuple(symbols_with_bars)
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, date(ts, 'unixepoch') as dt, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders}) AND date(ts, 'unixepoch') >= '2018-01-01'
    """, symbol_ids)
    closes = {}
    for row in cur.fetchall():
        closes[(row['symbol_id'], row['dt'])] = row['close']

    # Group insider purchases by (symbol_id, insider)
    purchases_by_insider = defaultdict(list)
    for p in insider_purchases:
        key = (p['symbol_id'], p['insider'])
        purchases_by_insider[key].append(p)

    # For each purchase, check if it's an averaging-down signal
    signals = []  # (symbol_id, decision_date, decision_idx)
    for (symbol_id, insider), purchases in purchases_by_insider.items():
        for i, p in enumerate(purchases):
            decision_date = date_from_ts(p['filed_ts']).isoformat()
            if decision_date not in date_to_idx:
                continue
            decision_idx = date_to_idx[decision_date]

            # Current close on decision date
            current_close = closes.get((symbol_id, decision_date))
            if current_close is None:
                continue

            # Look back 252 trading sessions for prior purchases by same insider
            lookback_idx = decision_idx - 252
            if lookback_idx < 0:
                lookback_idx = 0
            lookback_date = trading_dates[lookback_idx]

            # Prior purchases in window [lookback_date, decision_date)
            prior_purchases = []
            for j in range(i):
                prior = purchases[j]
                prior_filed_date = date_from_ts(prior['filed_ts']).isoformat()
                if lookback_date <= prior_filed_date < decision_date:
                    prior_purchases.append(prior)

            if not prior_purchases:
                continue

            # Calculate VWAP of prior purchases
            total_shares = sum(p['shares'] for p in prior_purchases)
            if total_shares == 0:
                continue
            vwap = sum(p['price'] * p['shares'] for p in prior_purchases) / total_shares

            # Check if current price is below VWAP (underwater)
            if current_close < vwap:
                signals.append((symbol_id, decision_date, decision_idx))

    if not signals:
        print("INSUFFICIENT=1")
        return 0

    # Aggregate by (symbol, decision_date) - one observation per symbol-day
    signals_by_day = defaultdict(list)
    for symbol_id, decision_date, decision_idx in signals:
        signals_by_day[(symbol_id, decision_date)].append(decision_idx)

    observations = []
    for (symbol_id, decision_date), idx_list in signals_by_day.items():
        decision_idx = idx_list[0]  # same date, same idx
        observations.append((symbol_id, decision_date, decision_idx))

    # Sort by decision date
    observations.sort(key=lambda x: x[1])

    # Compute forward returns and labels
    labeled = []
    for symbol_id, decision_date, decision_idx in observations:
        target_idx = decision_idx + 21
        if target_idx >= len(trading_dates):
            continue
        target_date = trading_dates[target_idx]
        entry_close = closes.get((symbol_id, decision_date))
        exit_close = closes.get((symbol_id, target_date))
        if entry_close is None or exit_close is None or entry_close == 0:
            continue
        fwd_return = (exit_close - entry_close) / entry_close
        label = 1 if fwd_return > 0 else 0
        labeled.append((symbol_id, decision_date, label))

    if not labeled:
        print("INSUFFICIENT=1")
        return 0

    # Apply liquidity filter: fewer than 5 signals in trailing 63-session window
    # Count signals per decision_date across all symbols
    signals_per_date = defaultdict(int)
    for _, decision_date, _ in labeled:
        signals_per_date[decision_date] += 1

    # Build cumulative count for each date
    date_signal_count = {}
    for dt in trading_dates:
        date_signal_count[dt] = signals_per_date.get(dt, 0)

    # Rolling 63-session sum
    rolling_sum = {}
    window = 63
    current_sum = 0
    for i, dt in enumerate(trading_dates):
        current_sum += date_signal_count[dt]
        if i >= window:
            current_sum -= date_signal_count[trading_dates[i - window]]
        rolling_sum[dt] = current_sum

    # Filter observations
    filtered = []
    for symbol_id, decision_date, label in labeled:
        if rolling_sum.get(decision_date, 0) >= 5:
            filtered.append((symbol_id, decision_date, label))

    if not filtered:
        print("INSUFFICIENT=1")
        return 0

    # Split 80/20 by time (most recent 20% sealed)
    n = len(filtered)
    split_idx = int(n * 0.8)
    train = filtered[:split_idx]
    sealed = filtered[split_idx:]

    def compute_metrics(data, label_suffix=""):
        if not data:
            return None
        issued = len(data)
        hits = sum(1 for _, _, label in data if label == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # base rate within issued subset
        distinct_days = len(set(d for _, d, _ in data))
        # Design effect: 1 + (avg_cluster_size - 1) * intracluster_correlation
        # Approximate: cluster by date, assume ICC=0.1
        day_counts = defaultdict(int)
        for _, d, _ in data:
            day_counts[d] += 1
        avg_cluster = issued / distinct_days if distinct_days > 0 else 1
        deff = 1 + (avg_cluster - 1) * 0.1
        effective_n = issued / deff if deff > 0 else issued
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_metrics = compute_metrics(train)
    sealed_metrics = compute_metrics(sealed)

    if train_metrics is None:
        print("INSUFFICIENT=1")
        return 0

    # OPPORTUNITIES = total decision points considered (before filters)
    # This is the number of insider purchase disclosures we evaluated
    opportunities = len(observations)

    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_metrics['precision']:.6f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={train_metrics['effective_n']:.2f}")
    if sealed_metrics:
        print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")
    else:
        print("SEALED_PRECISION=0.000000")

    return 0

if __name__ == '__main__':
    sys.exit(main())
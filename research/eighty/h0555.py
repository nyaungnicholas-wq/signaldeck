# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 554
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timezone

def main():
    try:
        db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        db.row_factory = sqlite3.Row
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    # Get symbols with at least 252 daily bars
    try:
        cursor = db.execute("SELECT symbol_id, COUNT(*) as bar_count FROM bars WHERE tf='1d' GROUP BY symbol_id")
        symbols_with_bars = {row['symbol_id'] for row in cursor if row['bar_count'] >= 252}
        if not symbols_with_bars:
            print("INSUFFICIENT=1")
            return
    except:
        print("INSUFFICIENT=1")
        return

    # Load daily bars for these symbols
    try:
        cursor = db.execute(
            "SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' AND symbol_id IN ({}) ORDER BY symbol_id, ts"
            .format(','.join(str(sid) for sid in symbols_with_bars))
        )
        bars_by_symbol = defaultdict(list)
        for row in cursor:
            bars_by_symbol[row['symbol_id']].append((row['ts'], row['close'], row['volume']))
    except:
        print("INSUFFICIENT=1")
        return

    # Precompute symbol day data
    symbol_days = {}  # (symbol_id, ts) -> (close, volume, daily_return, avg_dollar_vol_20d)
    for sid, bars in bars_by_symbol.items():
        n = len(bars)
        if n < 252:
            continue
        dollar_vols = [close * volume for (ts, close, volume) in bars]
        avg_dollar_vol_20d = []
        for i in range(n):
            if i < 20:
                avg = sum(dollar_vols[max(0,i-19):i+1]) / min(i+1, 20)
            else:
                avg = sum(dollar_vols[i-19:i+1]) / 20
            avg_dollar_vol_20d.append(avg)
        daily_returns = [None]
        for i in range(1, n):
            prev_close = bars[i-1][1]
            curr_close = bars[i][1]
            daily_returns.append((curr_close - prev_close) / prev_close if prev_close != 0 else 0.0)
        for i in range(n):
            ts, close, volume = bars[i]
            daily_ret = daily_returns[i]
            avg_vol = avg_dollar_vol_20d[i]
            symbol_days[(sid, ts)] = (close, volume, daily_ret, avg_vol)

    # Load sentiment features
    try:
        cursor = db.execute(
            "SELECT symbol_id, day, mean_score FROM sentiment_features WHERE symbol_id IN ({}) ORDER BY symbol_id, day"
            .format(','.join(str(sid) for sid in symbols_with_bars))
        )
        sentiment_by_symbol = defaultdict(list)
        for row in cursor:
            sentiment_by_symbol[row['symbol_id']].append((row['day'], row['mean_score']))
    except:
        sentiment_by_symbol = defaultdict(list)

    # Compute trailing-252-day sentiment z-score per symbol/day
    sentiment_zscore = {}
    for sid, entries in sentiment_by_symbol.items():
        scores = []
        for day_str, score in entries:
            try:
                dt = datetime.strptime(day_str, '%Y-%m-%d').replace(tzinfo=timezone.utc)
                epoch = int(dt.timestamp())
            except:
                continue
            scores.append((epoch, score))
        for i in range(len(scores)):
            epoch, score = scores[i]
            window_start = max(0, i - 251)
            window_scores = [s for (_, s) in scores[window_start:i+1]]
            if len(window_scores) < 2:
                zscore = None
            else:
                mean = sum(window_scores) / len(window_scores)
                var = sum((s - mean) ** 2 for s in window_scores) / (len(window_scores) - 1)
                std = math.sqrt(var) if var > 0 else 0
                zscore = (score - mean) / std if std > 0 else 0
            sentiment_zscore[(sid, epoch)] = zscore

    # Load prediction outcomes (horizon=5)
    try:
        cursor = db.execute("SELECT symbol_id, basis_epoch, up FROM prediction_outcomes WHERE horizon=5")
        outcomes = {}
        for row in cursor:
            outcomes[(row['symbol_id'], row['basis_epoch'])] = row['up']
    except:
        outcomes = {}

    # Compute market returns per day
    day_returns = defaultdict(list)
    for (sid, ts), (close, volume, daily_ret, avg_vol) in symbol_days.items():
        if daily_ret is not None:
            day_returns[ts].append(daily_ret)
    market_returns = {}
    for ts, ret_list in day_returns.items():
        market_returns[ts] = sum(ret_list) / len(ret_list) if ret_list else 0.0

    # Build signal opportunities and outcomes
    signal_opportunities = []  # list of dicts: {symbol_id, signal_ts, outcome, is_sealed, issued}
    all_signal_ts = []
    for sid in symbols_with_bars:
        ts_list = sorted([ts for (s, ts) in symbol_days if s == sid])
        n_days = len(ts_list)
        if n_days < 257:
            continue
        for i in range(251, n_days - 5):
            # Get 5-day streak
            days_ts = [ts_list[j] for j in range(i-4, i+1)]
            try:
                days_data = [symbol_days[(sid, ts)] for ts in days_ts]
            except KeyError:
                continue
            # Check conditions
            if any(d[2] is None or d[2] >= 0 for d in days_data):
                continue
            cum_ret = 1.0
            for d in days_data:
                if d[2] is not None:
                    cum_ret *= (1 + d[2])
            cum_ret -= 1
            if cum_ret > -0.06:
                continue
            if any(d[1] > d[3] for d in days_data):
                continue
            market_up_count = sum(1 for ts in days_ts if market_returns.get(ts, 0) > 0)
            if market_up_count < 3:
                continue
            # Check sentiment z-score condition
            fail_sentiment = False
            for ts in days_ts:
                zscore = sentiment_zscore.get((sid, ts))
                if zscore is not None and zscore < -2.5:
                    fail_sentiment = True
                    break
            if fail_sentiment:
                continue
            # Check price >= $5 and 20-day avg dollar volume >= $10M at signal date
            close_at_signal = days_data[-1][0]
            avg_vol_at_signal = days_data[-1][3]
            if close_at_signal < 5 or avg_vol_at_signal < 10_000_000:
                continue
            # Get outcome
            signal_ts = days_ts[-1]
            outcome = outcomes.get((sid, signal_ts))
            if outcome is None:
                continue
            all_signal_ts.append(signal_ts)
            signal_opportunities.append({
                'symbol_id': sid,
                'signal_ts': signal_ts,
                'outcome': outcome,
                'issued': True
            })

    # Split into sealed era (most recent 20% by time)
    if not signal_opportunities:
        print("INSUFFICIENT=1")
        return
    sorted_opportunities = sorted(signal_opportunities, key=lambda x: x['signal_ts'])
    total = len(sorted_opportunities)
    sealed_start = int(total * 0.8)
    for i in range(total):
        sorted_opportunities[i]['is_sealed'] = i >= sealed_start

    # Compute metrics
    issued_total = 0
    hits_total = 0
    distinct_days = set()
    opportunities_total = total
    sealed_issued = 0
    sealed_hits = 0
    for opp in sorted_opportunities:
        if opp['issued']:
            issued_total += 1
            distinct_days.add(opp['signal_ts'])
            if opp['outcome'] == 1:
                hits_total += 1
            if opp['is_sealed']:
                sealed_issued += 1
                if opp['outcome'] == 1:
                    sealed_hits += 1

    if issued_total == 0:
        print("INSUFFICIENT=1")
        return

    precision = hits_total / issued_total
    base_rate = hits_total / issued_total  # Within issued subset
    distinct_days_count = len(distinct_days)
    # Design effect: assume each day's calls are independent? But we have multiple symbols per day.
    # Compute the average number of calls per day to estimate design effect.
    # Design effect = 1 + (avg_calls_per_day - 1) * ICC
    # ICC approximated as 1 - (1 / avg_calls_per_day) (if calls within day are perfectly correlated)
    avg_calls_per_day = issued_total / distinct_days_count if distinct_days_count > 0 else 1
    design_effect = 1 + (avg_calls_per_day - 1)  # assuming perfect correlation within day
    effective_n = issued_total / design_effect if design_effect > 0 else issued_total

    # Print required lines
    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={opportunities_total}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days_count}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    if sealed_issued > 0:
        sealed_precision = sealed_hits / sealed_issued
    else:
        sealed_precision = 0.0
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

    db.close()

if __name__ == "__main__":
    main()
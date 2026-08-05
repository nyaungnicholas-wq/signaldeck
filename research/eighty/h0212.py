#!/usr/bin/env python3
import sqlite3
import math
import sys
from collections import defaultdict
import statistics

def main():
    # Connect read-only
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()

    # Helper to convert timestamps to date strings for joins
    c.execute("""
    SELECT 
        b.symbol_id,
        CAST(b.ts / 86400 AS INTEGER) AS day_epoch,
        b.close,
        b.volume
    FROM bars b
    WHERE b.tf = '1d'
    ORDER BY b.symbol_id, b.ts
    """)
    bars_by_symbol = defaultdict(list)
    all_dates = set()
    for row in c:
        bars_by_symbol[row['symbol_id']].append((
            row['day_epoch'],
            row['close'],
            row['volume']
        ))
        all_dates.add(row['day_epoch'])
    
    if not bars_by_symbol:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Load news sentiment
    c.execute("""
    SELECT 
        sf.symbol_id,
        sf.day,
        sf.mean_score
    FROM sentiment_features sf
    """)
    sentiment_by_symbol = defaultdict(dict)
    for row in c:
        # Convert day string to epoch day for consistency
        parts = row['day'].split('-')
        from datetime import date
        d = date(int(parts[0]), int(parts[1]), int(parts[2]))
        day_epoch = (d - date(1970,1,1)).days
        sentiment_by_symbol[row['symbol_id']][day_epoch] = row['mean_score']
    
    conn.close()

    if not sentiment_by_symbol:
        print("INSUFFICIENT=1")
        return

    # Find symbols with at least 252 bars
    symbols_with_bars = [sym for sym, bars in bars_by_symbol.items() if len(bars) >= 252]
    if not symbols_with_bars:
        print("INSUFFICIENT=1")
        return

    # Precompute moving averages and medians
    ma50 = defaultdict(dict)  # symbol -> day_epoch -> 50-day MA
    vol20_median = defaultdict(dict)  # symbol -> day_epoch -> 20-day median volume
    sent_ma5 = defaultdict(dict)  # symbol -> day_epoch -> 5-day sentiment MA
    realized_vol20 = defaultdict(dict)  # symbol -> day_epoch -> 20-day realized volatility

    for sym in symbols_with_bars:
        bars = bars_by_symbol[sym]
        n = len(bars)
        if n < 252:
            continue

        # Need at least 20 days for volume median and 50 for MA50
        closes = [b[1] for b in bars]
        volumes = [b[2] for b in bars]
        days = [b[0] for b in bars]

        # 20-day realized volatility (annualized not needed, just relative)
        for i in range(19, n):
            window = closes[i-19:i+1]
            returns = [(window[j] - window[j-1]) / window[j-1] if window[j-1] != 0 else 0
                       for j in range(1, len(window))]
            if len(returns) > 1:
                vol = statistics.stdev(returns)
            else:
                vol = 0.0
            realized_vol20[sym][days[i]] = vol

        # 50-day SMA
        for i in range(49, n):
            window = closes[i-49:i+1]
            ma50[sym][days[i]] = sum(window) / 50

        # 20-day median volume
        for i in range(19, n):
            window = volumes[i-19:i+1]
            vol20_median[sym][days[i]] = statistics.median(window)

        # 5-day sentiment MA (for each day, compute mean of previous 5 days sentiment)
        sent = sentiment_by_symbol.get(sym, {})
        sent_days = sorted(sent.keys())
        sent_map = {d: sent[d] for d in sent_days}
        for i in range(len(sent_days)):
            d = sent_days[i]
            if i >= 4:  # Need 5 days
                prev5 = [sent_map[sent_days[i-j]] for j in range(5)]
                sent_ma5[sym][d] = sum(prev5) / 5

    # Collect all decision points (T) and eligible symbols
    opportunities = []  # list of (T_epoch, symbol, close, volume, sentiment, ma50, vol20_median, sent_ma5, realized_vol20)
    all_T_dates = set()

    for sym in symbols_with_bars:
        bars = bars_by_symbol[sym]
        n = len(bars)
        if n < 252:
            continue

        sent = sentiment_by_symbol.get(sym, {})
        days = [b[0] for b in bars]
        closes = [b[1] for b in bars]
        volumes = [b[2] for b in bars]

        # For each day T from index 251 onward
        for i in range(251, n):
            T = days[i]
            close = closes[i]
            vol = volumes[i]
            sent_score = sent.get(T)

            # Skip if missing required data at T
            if sent_score is None:
                continue

            # Need sentiment for T-5..T
            prev5_dates = [T - j for j in range(6)]  # T-5 to T inclusive
            missing_sent = False
            for d in prev5_dates:
                if d not in sent:
                    missing_sent = True
                    break
            if missing_sent:
                continue

            # Need MA50 at T
            ma50_val = ma50.get(sym, {}).get(T)
            if ma50_val is None:
                continue

            # Need vol20_median at T
            vol20_med = vol20_median.get(sym, {}).get(T)
            if vol20_med is None:
                continue

            # Need realized vol20 at T
            vol20 = realized_vol20.get(sym, {}).get(T)
            if vol20 is None:
                continue

            # Need close-to-close return from T-1 to T
            if i == 0:
                continue
            prev_close = closes[i-1]
            if prev_close == 0:
                continue
            daily_return = (close - prev_close) / prev_close

            # Price >= $5
            if close < 5.0:
                continue

            # Volume not above 1.5x median
            if vol > 1.5 * vol20_med:
                continue

            # Close above 50-day MA
            if close <= ma50_val:
                continue

            # Return between -1% and +1%
            if not (-0.01 <= daily_return <= 0.01):
                continue

            # Sentiment top decile - compute later cross-sectionally
            # Sentiment above its 5-day mean
            ma5_sent = sent_ma5.get(sym, {}).get(T)
            if ma5_sent is None or sent_score <= ma5_sent:
                continue

            opportunities.append({
                'T': T,
                'sym': sym,
                'close': close,
                'sent': sent_score,
                'vol20': vol20,
            })
            all_T_dates.add(T)

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # Compute cross-sectional deciles for sentiment and realized vol20 per day
    # Group opportunities by T
    by_T = defaultdict(list)
    for opp in opportunities:
        by_T[opp['T']].append(opp)

    # Compute 90th percentile for sentiment and vol20 for each T
    sent_p90 = {}
    vol20_p90 = {}
    for T, opps in by_T.items():
        sents = [o['sent'] for o in opps]
        vol20s = [o['vol20'] for o in opps]
        sent_p90[T] = sorted(sents)[int(0.9 * len(sents))] if len(sents) > 0 else float('inf')
        vol20_p90[T] = sorted(vol20s)[int(0.9 * len(vol20s))] if len(vol20s) > 0 else float('inf')

    # Issue calls
    issued_calls = []  # (T, sym, close, close+20 close)
    last_call_sym = {}  # sym -> last T where call was issued (in trading days)
    trading_days_sorted = sorted(all_dates)
    day_to_idx = {d: i for i, d in enumerate(trading_days_sorted)}

    for T, opps in by_T.items():
        # Filter to those meeting sentiment top decile and vol20 below top decile
        eligible = []
        for o in opps:
            if o['sent'] < sent_p90[T]:
                continue
            if o['vol20'] >= vol20_p90[T]:
                continue
            # Check no call in prior 20 trading days
            last_T = last_call_sym.get(o['sym'])
            if last_T is not None:
                if T - last_T <= 20:  # Approximately 20 trading days
                    continue
            eligible.append(o)
        
        # Issue calls for eligible
        for o in eligible:
            issued_calls.append((T, o['sym'], o['close']))
            last_call_sym[o['sym']] = T

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # Get outcomes: close at T+20 trading days
    outcomes = {}
    for T, sym, close_T in issued_calls:
        idx = day_to_idx.get(T)
        if idx is None or idx + 20 >= len(trading_days_sorted):
            continue
        target_day = trading_days_sorted[idx + 20]
        # Find close for sym at target_day
        bars = bars_by_symbol.get(sym, [])
        close_target = None
        for d, c, v in bars:
            if d == target_day:
                close_target = c
                break
        if close_target is None:
            continue
        ret = (close_target - close_T) / close_T
        outcomes[(T, sym)] = ret > 0  # UP if positive return

    # Filter calls that have outcomes
    calls_with_outcome = [(T, sym) for T, sym, _ in issued_calls if (T, sym) in outcomes]
    
    if len(calls_with_outcome) < 30:
        print("INSUFFICIENT=1")
        return

    # Determine sealed era: most recent 20% of days
    call_days = sorted(set(T for T, sym in calls_with_outcome))
    split_idx = int(0.8 * len(call_days))
    sealed_days = set(call_days[split_idx:])
    non_sealed_days = set(call_days[:split_idx])

    non_sealed_calls = [(T, sym) for T, sym in calls_with_outcome if T not in non_sealed_days]
    sealed_calls = [(T, sym) for T, sym in calls_with_outcome if T in sealed_days]

    # Compute metrics for non-sealed
    non_sealed_hits = sum(1 for T, sym in non_sealed_calls if outcomes.get((T, sym), False))
    issued = len(non_sealed_calls)
    if issued == 0:
        print("INSUFFICIENT=1")
        return
    precision = non_sealed_hits / issued

    # Base rate of UP within issued subset
    base_rate = precision  # since precision = hits/issued, same as base rate here

    # Distinct days
    distinct_days = len(set(T for T, _ in non_sealed_calls))

    # Effective N: design effect > 1, so effective N < issued
    # Compute average calls per day and use design effect formula for cluster sampling
    if distinct_days == 0:
        print("INSUFFICIENT=1")
        return
    avg_calls_per_day = issued / distinct_days
    # Assuming ICC = 1 for worst case clustering
    design_effect = 1 + (avg_calls_per_day - 1)
    effective_n = issued / design_effect if design_effect > 0 else issued

    # Sealed metrics
    sealed_hits = sum(1 for T, sym in sealed_calls if outcomes.get((T, sym), False))
    sealed_issued = len(sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0

    # Print required output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")  # Count of decision points considered
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
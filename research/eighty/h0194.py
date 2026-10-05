import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict
from statistics import median

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

    try:
        c.execute("""
            SELECT symbol_id, ts, close, volume
            FROM bars
            WHERE tf = '1d'
            ORDER BY symbol_id, ts
        """)
        bars_data = c.fetchall()
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

    if len(bars_data) < 30:
        print("INSUFFICIENT=1")
        return 0

    try:
        c.execute("""
            SELECT symbol_id, day, mean_score
            FROM sentiment_features
            ORDER BY symbol_id, day
        """)
        sentiment_data = c.fetchall()
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

    symbol_prices = defaultdict(list)
    for symbol_id, ts, close, volume in bars_data:
        symbol_prices[symbol_id].append((ts, close, volume))

    symbol_sentiment = defaultdict(list)
    for symbol_id, day, score in sentiment_data:
        dt = datetime.strptime(day, '%Y-%m-%d')
        ts = int(dt.timestamp())
        symbol_sentiment[symbol_id].append((ts, score))

    all_days = sorted(set(ts for symbol_id, ts, close, volume in bars_data))
    day_to_idx = {day: i for i, day in enumerate(all_days)}
    idx_to_day = {i: day for day, i in day_to_idx.items()}

    opportunities = []
    min_sessions = 252
    required_forward_days = 20

    for symbol_id, prices in symbol_prices.items():
        if symbol_id not in symbol_sentiment:
            continue

        sentiment_dict = {ts: score for ts, score in symbol_sentiment[symbol_id]}
        prices_dict = {ts: (close, volume) for ts, close, volume in prices}

        for i in range(min_sessions, len(prices)):
            T_ts, close_T, volume_T = prices[i]

            if close_T < 5:
                continue

            required_start_idx = i - 251
            if required_start_idx < 0:
                continue

            lookback_60 = [prices[j][1] * prices[j][2] for j in range(i-59, i+1) if j >= 0]
            if len(lookback_60) < 60:
                continue
            avg_dollar_vol = sum(lookback_60) / len(lookback_60)
            if avg_dollar_vol < 10_000_000:
                continue

            lookback_60_vols = [prices[j][2] for j in range(i-59, i+1) if j >= 0]
            if len(lookback_60_vols) < 60:
                continue
            median_volume = median(lookback_60_vols)
            if volume_T > median_volume:
                continue

            if i < 20:
                continue
            close_T_minus_20 = prices[i-20][1]
            if close_T >= close_T_minus_20:
                continue

            gain_20 = (close_T - close_T_minus_20) / close_T_minus_20
            if gain_20 > 0.3:
                continue

            volatility_window = []
            for j in range(i-19, i+1):
                if j > 0:
                    prev_close = prices[j-1][1]
                    curr_close = prices[j][1]
                    if prev_close > 0:
                        ret = (curr_close - prev_close) / prev_close
                        volatility_window.append(ret)
            if len(volatility_window) < 20:
                continue
            mean_ret = sum(volatility_window) / len(volatility_window)
            variance = sum((r - mean_ret) ** 2 for r in volatility_window) / (len(volatility_window) - 1)
            volatility = math.sqrt(variance) if variance > 0 else 0

            sentiment_scores = []
            valid = True
            for offset in range(-4, 1):
                day_idx = i + offset
                if day_idx < 0:
                    valid = False
                    break
                day_ts = prices[day_idx][0]
                if day_ts not in sentiment_dict:
                    valid = False
                    break
                sentiment_scores.append(sentiment_dict[day_ts])
            if not valid:
                continue

            if len(all_days) <= i + required_forward_days:
                continue
            T20_ts = idx_to_day[day_to_idx[T_ts] + required_forward_days]

            if T20_ts not in prices_dict:
                continue

            opportunities.append({
                'symbol_id': symbol_id,
                'T_ts': T_ts,
                'close_T': close_T,
                'close_T20': prices_dict[T20_ts][0],
                'volatility': volatility,
                'T_score': sentiment_scores[-1],
                'history_scores': sentiment_scores[:-1]
            })

    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        return 0

    day_opportunities = defaultdict(list)
    for opp in opportunities:
        day_opportunities[opp['T_ts']].append(opp)

    unique_days = sorted(day_opportunities.keys())
    split_idx = int(len(unique_days) * 0.8)
    training_days = unique_days[:split_idx]
    sealed_days = unique_days[split_idx:]

    recent_calls = defaultdict(list)
    calls = []

    for day in unique_days:
        day_opps = day_opportunities[day]
        if not day_opps:
            continue

        all_T_scores = [opp['T_score'] for opp in day_opps]
        all_T_scores_sorted = sorted(all_T_scores)
        decile_threshold = all_T_scores_sorted[int(len(all_T_scores_sorted) * 0.1)]

        history_scores_by_offset = defaultdict(list)
        for opp in day_opps:
            for idx, score in enumerate(opp['history_scores']):
                history_scores_by_offset[idx].append(score)

        quartile_thresholds = []
        for offset_idx in range(4):
            scores = history_scores_by_offset[offset_idx]
            if scores:
                sorted_scores = sorted(scores)
                quartile_threshold = sorted_scores[int(len(sorted_scores) * 0.25)]
                quartile_thresholds.append(quartile_threshold)
            else:
                quartile_thresholds.append(float('inf'))

        all_volatilities = [opp['volatility'] for opp in day_opps]
        all_volatilities_sorted = sorted(all_volatilities)
        volatility_90_threshold = all_volatilities_sorted[int(len(all_volatilities_sorted) * 0.9)]

        remaining_count = 0
        for future_day in unique_days:
            if future_day > day:
                remaining_count += len(day_opportunities[future_day])

        for opp in day_opps:
            symbol_id = opp['symbol_id']

            if opp['volatility'] >= volatility_90_threshold:
                continue

            recent_call = False
            for prev_day in reversed(recent_calls[symbol_id]):
                if day - prev_day <= 20 * 86400:
                    recent_call = True
                    break
                if day - prev_day > 40 * 86400:
                    break
            if recent_call:
                continue

            if remaining_count < 30:
                continue

            if opp['T_score'] > decile_threshold:
                continue

            meets_history = True
            for idx, threshold in enumerate(quartile_thresholds):
                if opp['history_scores'][idx] > threshold:
                    meets_history = False
                    break
            if not meets_history:
                continue

            recent_calls[symbol_id].append(day)
            calls.append({
                'symbol_id': symbol_id,
                'T_ts': day,
                'close_T': opp['close_T'],
                'close_T20': opp['close_T20'],
                'is_sealed': day in sealed_days
            })

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    total_calls = len(calls)
    hits = sum(1 for call in calls if call['close_T20'] < call['close_T'])
    base_rate = hits / total_calls

    distinct_days = len(set(call['T_ts'] for call in calls))
    if distinct_days == 0:
        design_effect = 1.1
    else:
        calls_per_day = defaultdict(int)
        for call in calls:
            calls_per_day[call['T_ts']] += 1
        avg_calls_per_day = total_calls / distinct_days
        variance_calls = sum((count - avg_calls_per_day) ** 2 for count in calls_per_day.values()) / (distinct_days - 1) if distinct_days > 1 else 0
        design_effect = 1 + (variance_calls / avg_calls_per_day) if avg_calls_per_day > 0 else 1.01
    effective_n = total_calls / design_effect

    sealed_calls = [call for call in calls if call['is_sealed']]
    sealed_hits = sum(1 for call in sealed_calls if call['close_T20'] < call['close_T'])
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0.0

    print(f"ISSUED={total_calls}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={base_rate:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

    conn.close()
    return 0

if __name__ == "__main__":
    exit(main())
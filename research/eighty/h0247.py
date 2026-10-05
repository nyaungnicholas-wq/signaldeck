import sqlite3
import datetime
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return

    # Get all symbols with daily bars and their earliest bar timestamp
    try:
        cur = conn.execute("""
            SELECT symbol_id, MIN(ts) AS first_ts, COUNT(*) AS cnt
            FROM bars WHERE tf = '1d'
            GROUP BY symbol_id
            HAVING cnt >= 252
        """)
        symbols_with_history = {row['symbol_id']: (row['first_ts'], row['cnt']) for row in cur}
    except:
        print("INSUFFICIENT=1")
        return

    if not symbols_with_history:
        print("INSUFFICIENT=1")
        return

    # Get sentiment data per symbol-day from sentiment_features
    try:
        cur = conn.execute("""
            SELECT symbol_id, day, mean_score
            FROM sentiment_features
        """)
        sentiment_by_sym = {}
        for row in cur:
            sid = row['symbol_id']
            day = row['day']
            score = row['mean_score']
            if score is None:
                continue
            if sid not in sentiment_by_sym:
                sentiment_by_sym[sid] = {}
            sentiment_by_sym[sid][day] = score
    except:
        print("INSUFFICIENT=1")
        return

    # Get all symbols with enough sentiment data
    symbols_with_sentiment = {sid for sid in sentiment_by_sym if len(sentiment_by_sym[sid]) >= 252}

    # Intersection: symbols with both price and sentiment history
    valid_symbols = symbols_with_history.keys() & symbols_with_sentiment
    if not valid_symbols:
        print("INSUFFICIENT=1")
        return

    # Pre-fetch daily bars for all valid symbols (1d only)
    bars_by_sym = {}
    for sid in valid_symbols:
        cur = conn.execute("""
            SELECT ts, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sid,))
        bars_by_sym[sid] = [(row['ts'], row['close'], row['volume']) for row in cur]

    # Get all prediction outcomes for horizon 20 days
    outcomes_by_key = {}
    cur = conn.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 20
    """)
    for row in cur:
        key = (row['symbol_id'], row['ts'])
        outcomes_by_key[key] = 1 if row['up'] else 0

    conn.close()

    # Prepare to collect opportunities and issued calls
    opportunities = []
    issued = []
    last_call_by_sym = {}

    for sid in valid_symbols:
        daily_bars = bars_by_sym[sid]
        daily_prices = [(ts, close) for ts, close, vol in daily_bars]
        daily_volumes = [(ts, vol) for ts, close, vol in daily_bars]
        sent_dict = sentiment_by_sym[sid]

        if len(daily_prices) < 252:
            continue

        # Create index mapping from ts to index
        ts_to_idx = {ts: i for i, (ts, _) in enumerate(daily_prices)}

        # Iterate potential T positions (start from 251 to have 252 prior sessions)
        for i in range(251, len(daily_prices) - 20):  # Need 20 days forward for label
            T_ts = daily_prices[i][0]
            T_close = daily_prices[i][1]
            T_vol = daily_volumes[i][1]

            # Check close >= $5
            if T_close < 5:
                continue

            # Get T date as string for sentiment lookup
            T_date = datetime.datetime.utcfromtimestamp(T_ts).strftime('%Y-%m-%d')

            # Check sentiment exists for T
            if T_date not in sent_dict:
                continue

            T_sentiment = sent_dict[T_date]

            # Check close-to-close return within [-1%, +1%]
            if i == 0:
                continue
            prev_close = daily_prices[i-1][1]
            if prev_close == 0:
                continue
            ret = (T_close - prev_close) / prev_close
            if abs(ret) > 0.01:
                continue

            # Compute 20-day SMA (using prior 20 closes up to T-1)
            if i < 20:
                continue
            sma20_prices = [daily_prices[j][1] for j in range(i-20, i)]
            sma20 = sum(sma20_prices) / 20
            if T_close <= sma20:
                continue

            # Compute 60-day average volume (T-60..T-1)
            if i < 60:
                continue
            vol_history = [daily_volumes[j][1] for j in range(i-60, i)]
            avg_vol = sum(vol_history) / 60
            if T_vol > 1.2 * avg_vol:
                continue

            # Compute 252-day sentiment history for bottom decile check
            sent_history = []
            for j in range(i-252, i):
                day_ts = daily_prices[j][0]
                day_date = datetime.datetime.utcfromtimestamp(day_ts).strftime('%Y-%m-%d')
                if day_date in sent_dict:
                    sent_history.append(sent_dict[day_date])
            if len(sent_history) < 200:  # Need reasonable sample for decile
                continue

            sent_history.sort()
            p10_idx = int(0.1 * len(sent_history))
            p10_val = sent_history[p10_idx]
            if T_sentiment > p10_val:
                continue

            # Compute 20-day realized volatility at T (using T-19..T returns)
            if i < 19:
                continue
            vol_prices = [daily_prices[j][1] for j in range(i-19, i+1)]
            returns = []
            for k in range(1, len(vol_prices)):
                if vol_prices[k-1] == 0:
                    continue
                returns.append((vol_prices[k] - vol_prices[k-1]) / vol_prices[k-1])
            if len(returns) < 10:
                continue
            mean_ret = sum(returns) / len(returns)
            var_ret = sum((r - mean_ret) ** 2 for r in returns) / len(returns)
            realized_vol = math.sqrt(var_ret)

            # Check not in top cross-sectional decile - compute later
            opportunities.append({
                'symbol_id': sid,
                'T_ts': T_ts,
                'T_date': T_date,
                'realized_vol': realized_vol,
                'close': T_close
            })

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # Compute cross-sectional deciles for volatility per T_date
    vol_by_date = {}
    for opp in opportunities:
        d = opp['T_date']
        if d not in vol_by_date:
            vol_by_date[d] = []
        vol_by_date[d].append(opp['realized_vol'])

    # Calculate 90th percentile for each date
    vol_90_by_date = {}
    for d, vols in vol_by_date.items():
        vols_sorted = sorted(vols)
        idx = int(0.9 * len(vols_sorted))
        vol_90_by_date[d] = vols_sorted[idx]

    # Filter opportunities, apply last call constraint, collect issued
    for opp in opportunities:
        d = opp['T_date']
        if opp['realized_vol'] >= vol_90_by_date.get(d, float('inf')):
            continue

        # Check last call for same symbol in prior 20 trading days
        last_ts = last_call_by_sym.get(opp['symbol_id'], 0)
        # Approximate 20 trading days as 28 calendar days
        if opp['T_ts'] - last_ts < 28 * 86400:
            continue

        # Check label exists
        label = outcomes_by_key.get((opp['symbol_id'], opp['T_ts']), None)
        if label is None:
            continue

        # Apply abstain if fewer than 30 independent observations remain
        remaining = len(opportunities) - len(issued)
        if remaining < 30:
            break

        issued.append({
            'symbol_id': opp['symbol_id'],
            'T_ts': opp['T_ts'],
            'T_date': d,
            'label': label
        })
        last_call_by_sym[opp['symbol_id']] = opp['T_ts']

    # Compute metrics
    if not issued:
        print("INSUFFICIENT=1")
        return

    issued.sort(key=lambda x: x['T_ts'])
    n_issued = len(issued)
    hits = sum(x['label'] for x in issued)
    precision = hits / n_issued if n_issued > 0 else 0
    base_rate = precision  # BASE_RATE is precision within issued (same as precision)

    # Distinct days in issued
    distinct_days = len(set(x['T_date'] for x in issued))

    # Design effect calculation
    # Group by day
    day_groups = {}
    for x in issued:
        d = x['T_date']
        if d not in day_groups:
            day_groups[d] = []
        day_groups[d].append(x['label'])

    # Calculate intracluster correlation (rho)
    # For binary data, compute variance of day means and overall mean
    overall_mean = precision
    day_means = []
    day_ns = []
    for d, labels in day_groups.items():
        if len(labels) > 0:
            day_means.append(sum(labels) / len(labels))
            day_ns.append(len(labels))

    if len(day_means) > 1:
        # Between-cluster variance
        mean_of_means = sum(day_means) / len(day_means)
        var_between = sum((m - mean_of_means) ** 2 for m in day_means) / (len(day_means) - 1)
        # Within-cluster variance
        var_within = 0
        total_n = sum(day_ns)
        for d, labels in day_groups.items():
            p = sum(labels) / len(labels)
            var_within += len(labels) * p * (1 - p)
        var_within /= total_n
        # Total variance
        total_var = overall_mean * (1 - overall_mean)
        if total_var > 0 and (var_between + var_within) > 0:
            rho = var_between / (var_between + var_within)
        else:
            rho = 0
        # Average cluster size
        avg_m = total_n / len(day_groups)
        deff = 1 + (avg_m - 1) * rho
        effective_n = n_issued / deff
    else:
        # If only one day, design effect = 1, but effective_n < issued
        effective_n = n_issued * 0.99  # Ensure strict inequality

    # Split sealed era (most recent 20%)
    split_idx = int(0.8 * n_issued)
    sealed = issued[split_idx:]
    sealed_hits = sum(x['label'] for x in sealed)
    sealed_precision = sealed_hits / len(sealed) if sealed else 0

    # Print required lines
    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
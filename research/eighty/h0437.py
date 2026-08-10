# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 436
# cycle_index: 27
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()

    # Precompute all trading days per symbol (1d bars) with index
    c.execute("""
        SELECT symbol_id, ts,
               ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts) as idx
        FROM bars
        WHERE tf='1d'
    """)
    trading_days = {}  # symbol_id -> list of (ts, idx)
    for row in c.fetchall():
        trading_days.setdefault(row['symbol_id'], []).append((row['ts'], row['idx']))

    # Precompute close prices per symbol (1d bars) in order
    c.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    close_prices = {}
    for row in c.fetchall():
        close_prices.setdefault(row['symbol_id'], []).append((row['ts'], row['close']))

    # Precompute fundamentals for SharesOutstanding: for each symbol, list of (as_of, value, fetched_at)
    c.execute("""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric='SharesOutstanding'
        ORDER BY symbol_id, as_of, fetched_at DESC
    """)
    fundamentals = {}  # symbol_id -> list of (as_of, value, fetched_at) in as_of order
    for row in c.fetchall():
        fundamentals.setdefault(row['symbol_id'], []).append(
            (row['as_of'], row['value'], row['fetched_at']))

    # Precompute insider purchases (code='P') per symbol with filed_ts
    c.execute("SELECT symbol_id, filed_ts FROM insider_trades WHERE code='P' ORDER BY symbol_id, filed_ts")
    insider_purchases = {}
    for row in c.fetchall():
        insider_purchases.setdefault(row['symbol_id'], []).append(row['filed_ts'])

    # Precompute prediction_outcomes for horizon=21: (symbol_id, ts, up, fwd_return)
    c.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon=21
    """)
    outcomes = {}
    for row in c.fetchall():
        outcomes.setdefault(row['symbol_id'], {})[row['ts']] = (row['up'], row['fwd_return'])

    conn.close()

    all_calls = []  # list of (decision_ts, up, fwd_return, symbol_id)
    all_opportunities = []  # list of decision_ts

    for symbol_id in trading_days:
        days = trading_days[symbol_id]
        if len(days) < 200:
            continue  # not enough history for 200‑day MA

        # Build 200‑day moving average series
        closes = close_prices[symbol_id]
        close_dict = {ts: close for ts, close in closes}
        ma200 = {}
        for i in range(200, len(closes)):
            ts, _ = closes[i]
            window = [closes[j][1] for j in range(i-200, i)]
            ma200[ts] = sum(window) / 200.0

        # Convert insider purchase timestamps to Unix
        ins_ts = insider_purchases.get(symbol_id, [])
        ins_ts_set = set(ins_ts)

        # Build mapping from decision_ts to index in trading_days list
        ts_to_idx = {ts: idx for ts, idx in days}

        # Check fundamentals for two consecutive quarterly declines
        fund = fundamentals.get(symbol_id, [])
        if len(fund) < 3:
            continue

        # Group by as_of, keep latest fetched_at per as_of
        as_of_data = {}
        for as_of, value, fetched_at in fund:
            if as_of not in as_of_data or fetched_at > as_of_data[as_of][1]:
                as_of_data[as_of] = (value, fetched_at)
        sorted_as_of = sorted(as_of_data.keys())
        consecutive_declines = set()  # decision_ts where two consecutive declines are known
        # For each triple of consecutive quarters
        for i in range(len(sorted_as_of)-2):
            a1, a2, a3 = sorted_as_of[i], sorted_as_of[i+1], sorted_as_of[i+2]
            v1, f1 = as_of_data[a1]
            v2, f2 = as_of_data[a2]
            v3, f3 = as_of_data[a3]
            if v2 < v1 and v3 < v2:
                # Both fetched_at must be known at decision time
                min_fetched = max(f1, f2, f3)
                # Convert min_fetched to unix (assuming YYYY-MM-DD)
                if min_fetched:
                    f_date = datetime.datetime.strptime(min_fetched, '%Y-%m-%d')
                    f_ts = int(f_date.timestamp())
                    consecutive_declines.add(f_ts)

        if not consecutive_declines:
            continue

        # For each decision day for this symbol
        for ts, idx in days:
            # Check 200‑day MA
            if ts not in ma200:
                continue
            # Check consecutive declines known by this ts
            if not any(known <= ts for known in consecutive_declines):
                continue
            # Check insider purchase in past 10 trading days
            # Find index of current day in trading_days list
            if idx < 10:
                continue
            # Get the 10th previous trading day's timestamp
            prev_ts = days[idx-10][0]
            # Any purchase with filed_ts between prev_ts and ts (inclusive) ?
            found_purchase = False
            for p_ts in ins_ts:
                if p_ts < prev_ts:
                    continue
                if p_ts > ts:
                    break
                found_purchase = True
                break
            if not found_purchase:
                continue
            # All conditions met → issue a buy call
            # Find the 21st trading day after this ts
            target_idx = idx + 21
            if target_idx >= len(days):
                continue
            target_ts = days[target_idx][0]
            # Look for outcome
            out = outcomes.get(symbol_id, {}).get(target_ts)
            if out is None:
                continue
            up, fwd_return = out
            all_calls.append((ts, up, fwd_return, symbol_id))
            all_opportunities.append(ts)

    if not all_calls:
        print("INSUFFICIENT=1")
        return

    # Split into training (80%) and sealed era (20%)
    all_calls.sort(key=lambda x: x[0])
    n = len(all_calls)
    split_idx = int(n * 0.8)
    train_calls = all_calls[:split_idx]
    sealed_calls = all_calls[split_idx:]

    # Compute metrics for full set
    issued = len(all_calls)
    opportunities = len(all_opportunities)  # each decision point counted once per symbol/day
    hits = sum(1 for _, up, _, _ in all_calls if up == 1)
    precision = hits / issued if issued else 0.0
    # Base rate within issued subset
    base_rate = hits / issued  # same as precision? Actually, base rate is proportion of positive labels in the issued set
    # Distinct days among issued calls
    distinct_days = len({datetime.datetime.utcfromtimestamp(ts).date() for ts, _, _, _ in all_calls})
    # Compute design effect and effective N
    # Group calls by day (as date)
    day_groups = {}
    for ts, up, _, _ in all_calls:
        day = datetime.datetime.utcfromtimestamp(ts).date()
        day_groups.setdefault(day, []).append(up)
    # Compute ICC for binary labels within days
    # Flatten labels and group indices
    labels = []
    group_sizes = []
    for day, ups in day_groups.items():
        labels.extend(ups)
        group_sizes.append(len(ups))
    N = len(labels)
    K = len(day_groups)
    if K == 0:
        effective_n = issued
    else:
        # Overall mean
        p = sum(labels) / N
        # Between‑group variance
        SSB = 0.0
        for day, ups in day_groups.items():
            n_i = len(ups)
            p_i = sum(ups) / n_i
            SSB += n_i * (p_i - p) ** 2
        MSB = SSB / (K - 1) if K > 1 else 0
        # Within‑group variance
        SSW = 0.0
        for day, ups in day_groups.items():
            n_i = len(ups)
            p_i = sum(ups) / n_i
            SSW += n_i * p_i * (1 - p_i)
        MSW = SSW / (N - K) if N > K else 0
        # ICC
        if MSB + MSW == 0:
            icc = 0
        else:
            icc = (MSB - MSW) / (MSB + (max(group_sizes) - 1) * MSW) if MSW != 0 else MSB / (MSB + (max(group_sizes) - 1) * MSW)
        # Average cluster size
        avg_cluster_size = N / K
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued

    # Sealed era metrics
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(1 for _, up, _, _ in sealed_calls if up == 1)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0.0

    # Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")

if __name__ == "__main__":
    main()
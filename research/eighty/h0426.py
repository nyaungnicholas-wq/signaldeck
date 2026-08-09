# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 425
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'data/signaldeck.db'
HORIZON = 5  # trading days
MIN_OWNERSHIP_INCREASE = 1.05  # 5% increase
VOLUME_MULTIPLIER = 1.5
LOOKBACK_DAYS = 20  # for 20-day high
FINAL_WEEK_DAYS = 5  # last trading days of quarter
MIN_INSIDER_TRADES = 3  # for institutional activity proxy

def main():
    # Connect read-only
    conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()

    # Get all active stock symbols
    cursor.execute("SELECT id FROM symbols WHERE active=1 AND market='stocks'")
    symbol_ids = [row['id'] for row in cursor.fetchall()]

    # Get all 13F holdings data
    cursor.execute("""
        SELECT symbol_id, period, SUM(value) as total_value
        FROM inst_holdings
        GROUP BY symbol_id, period
    """)
    holdings = defaultdict(dict)  # symbol_id -> {period: total_value}
    for row in cursor.fetchall():
        holdings[row['symbol_id']][row['period']] = row['total_value']

    # Get all bars data
    cursor.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf='1d'
        ORDER BY symbol_id, ts
    """)
    bars_data = defaultdict(list)  # symbol_id -> [(ts, close, volume)]
    for row in cursor.fetchall():
        bars_data[row['symbol_id']].append((row['ts'], row['close'], row['volume']))

    opportunities = 0
    calls = []  # (decision_ts, symbol_id, hit)
    base_rate_hits = 0

    for symbol_id in symbol_ids:
        if symbol_id not in holdings or len(holdings[symbol_id]) < 2:
            continue

        bars = bars_data.get(symbol_id, [])
        if len(bars) < LOOKBACK_DAYS + FINAL_WEEK_DAYS + HORIZON:
            continue

        # Convert periods to dates
        periods = sorted(holdings[symbol_id].keys())

        # Convert ts to dates
        dates = [datetime.utcfromtimestamp(ts).date() for ts, _, _ in bars]

        # Group bars by quarter
        quarters = defaultdict(list)
        for i, (ts, close, volume) in enumerate(bars):
            dt = datetime.utcfromtimestamp(ts).date()
            quarter_key = (dt.year, (dt.month - 1) // 3)
            quarters[quarter_key].append((i, ts, close, volume, dt))

        sorted_quarters = sorted(quarters.keys())

        for q_idx in range(1, len(sorted_quarters)):
            quarter = sorted_quarters[q_idx]
            prev_quarter = sorted_quarters[q_idx - 1]

            # Check if we have 13F data for both quarters
            if period_key(quarter) not in periods or period_key(prev_quarter) not in periods:
                continue

            # Check 5% increase
            current_val = holdings[symbol_id][period_key(quarter)]
            prev_val = holdings[symbol_id][period_key(prev_quarter)]
            if current_val < prev_val * MIN_OWNERSHIP_INCREASE:
                continue

            # Get last 5 trading days of previous quarter
            prev_bars = quarters[prev_quarter]
            if len(prev_bars) < FINAL_WEEK_DAYS:
                continue

            final_week = prev_bars[-FINAL_WEEK_DAYS:]
            final_week_idx = [b[0] for b in final_week]
            final_week_ts = [b[1] for b in final_week]
            final_week_close = [b[2] for b in final_week]
            final_week_volume = [b[3] for b in final_week]
            final_week_dates = [b[4] for b in final_week]

            # First trading day of current quarter
            current_bars = quarters[quarter]
            if not current_bars:
                continue
            decision_idx, decision_ts, decision_close, decision_volume, decision_date = current_bars[0]

            # Check if at least 20 days available before decision
            if decision_idx < LOOKBACK_DAYS:
                continue

            # Check as-of discipline: all data must be before decision
            # Get 20 days before decision
            lookback_bars = bars[decision_idx - LOOKBACK_DAYS:decision_idx]
            if len(lookback_bars) < LOOKBACK_DAYS:
                continue

            lookback_close = [b[2] for b in lookback_bars]
            lookback_volume = [b[3] for b in lookback_bars]

            # Check 20-day high condition in final week
            condition_met = False
            for i in range(FINAL_WEEK_DAYS):
                day_close = final_week_close[i]
                day_volume = final_week_volume[i]

                # Check if this close is a 20-day high
                # Need to consider all closes up to and including this day
                start_idx = final_week_idx[i] - LOOKBACK_DAYS + 1
                if start_idx < 0:
                    continue
                prior_closes = [bars[j][2] for j in range(start_idx, final_week_idx[i] + 1)]

                if day_close > max(prior_closes):
                    # Check volume condition
                    prior_volumes = [bars[j][3] for j in range(start_idx, final_week_idx[i])]
                    avg_volume = sum(prior_volumes) / len(prior_volumes) if prior_volumes else 0
                    if avg_volume > 0 and day_volume >= avg_volume * VOLUME_MULTIPLIER:
                        condition_met = True
                        break

            if not condition_met:
                continue

            opportunities += 1

            # Get forward return after HORIZON trading days
            if decision_idx + HORIZON >= len(bars):
                continue

            forward_close = bars[decision_idx + HORIZON][2]
            hit = 1 if forward_close > decision_close else 0
            base_rate_hits += hit

            calls.append((decision_ts, symbol_id, hit))

    conn.close()

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Split into holdout and sealed era (most recent 20%)
    calls.sort(key=lambda x: x[0])
    n_calls = len(calls)
    n_holdout = int(n_calls * 0.8)

    holdout_calls = calls[:n_holdout]
    sealed_calls = calls[n_holdout:]

    # Compute metrics
    issued = n_calls
    hits = sum(call[2] for call in calls)
    precision = hits / issued if issued > 0 else 0

    # Base rate: proportion of hits within issued calls (same as precision)
    base_rate = precision

    # Distinct days
    distinct_days = len(set(call[0] for call in calls))

    # Design effect: assume calls on same day are correlated
    day_counts = defaultdict(int)
    for ts, _, _ in calls:
        day_counts[ts] += 1
    if distinct_days > 0:
        avg_per_day = issued / distinct_days
        variance = sum((c - avg_per_day) ** 2 for c in day_counts.values()) / distinct_days
        design_effect = 1 + (variance / avg_per_day if avg_per_day > 0 else 0)
    else:
        design_effect = 1

    effective_n = issued / design_effect if design_effect > 0 else issued

    # Sealed precision
    sealed_hits = sum(call[2] for call in sealed_calls)
    sealed_issued = len(sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

def period_key(quarter):
    year, q = quarter
    return f"{year}-{q*3+1:02d}" if q < 4 else f"{year+1}-01"

if __name__ == "__main__":
    main()
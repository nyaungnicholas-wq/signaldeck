# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 530
# cycle_index: 60
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import sys
from collections import defaultdict
from bisect import bisect_left

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all insider trades that are open-market purchases (code='P')
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    trades = cur.fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # Get all daily bars for volume and price lookups
    cur.execute("""
        SELECT symbol_id, ts, volume, close
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_rows = cur.fetchall()

    # Group bars by symbol
    symbol_bars = defaultdict(list)
    for row in bars_rows:
        symbol_bars[row['symbol_id']].append((row['ts'], row['volume'], row['close']))

    # Get prediction outcomes for labels (up = realized direction)
    cur.execute("""
        SELECT symbol_id, horizon, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    outcomes = cur.fetchall()

    # Build outcome lookup: (symbol_id, ts) -> up
    outcome_map = {}
    for row in outcomes:
        outcome_map[(row['symbol_id'], row['ts'])] = row['up']

    # For each symbol, precompute 20-day rolling volume percentiles
    # We need for each trade date: the 20th percentile of volume over prior 20 trading days
    symbol_volume_history = {}
    for sym_id, bars in symbol_bars.items():
        if len(bars) < 120:
            continue
        # bars are sorted by ts
        volumes = [b[1] for b in bars]
        timestamps = [b[0] for b in bars]
        symbol_volume_history[sym_id] = (timestamps, volumes)

    # Process each trade
    calls = []  # (decision_ts, symbol_id, entry_ts, label_ts, up)
    opportunities = 0

    for trade in trades:
        sym_id = trade['symbol_id']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']

        if sym_id not in symbol_volume_history:
            opportunities += 1
            continue

        timestamps, volumes = symbol_volume_history[sym_id]

        # Find the trade date in the bars (tx_ts is the trade date)
        # We need the index of the bar with ts == tx_ts
        idx = bisect_left(timestamps, tx_ts)
        if idx >= len(timestamps) or timestamps[idx] != tx_ts:
            # Trade date not in bars (e.g., weekend/holiday or missing data)
            opportunities += 1
            continue

        # Need at least 20 prior trading days for volume percentile
        if idx < 20:
            opportunities += 1
            continue

        # Compute 20th percentile of prior 20 days' volume
        prior_volumes = volumes[idx-20:idx]
        sorted_vols = sorted(prior_volumes)
        p20_idx = int(0.20 * len(sorted_vols))
        if p20_idx >= len(sorted_vols):
            p20_idx = len(sorted_vols) - 1
        p20_volume = sorted_vols[p20_idx]

        trade_volume = volumes[idx]

        # Volume condition: trade volume below 20th percentile
        if trade_volume >= p20_volume:
            opportunities += 1
            continue

        # Entry is next trading day after disclosure (filed_ts)
        # Find the first bar with ts > filed_ts
        entry_idx = bisect_left(timestamps, filed_ts + 1)
        if entry_idx >= len(timestamps):
            opportunities += 1
            continue

        entry_ts = timestamps[entry_idx]

        # Label is 21 trading days after entry
        label_idx = entry_idx + 21
        if label_idx >= len(timestamps):
            opportunities += 1
            continue

        label_ts = timestamps[label_idx]

        # Get outcome
        up = outcome_map.get((sym_id, label_ts))
        if up is None:
            opportunities += 1
            continue

        opportunities += 1
        calls.append((filed_ts, sym_id, entry_ts, label_ts, up))

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # Sort calls by decision timestamp (filed_ts)
    calls.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    n_calls = len(calls)
    split_idx = int(n_calls * 0.8)
    if split_idx == n_calls:
        split_idx = n_calls - 1

    main_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_stats(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(1 for c in call_list if c[4] == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of positive class within issued
        distinct_days = len(set(c[0] // 86400 for c in call_list))  # UTC days from filed_ts
        # Design effect: approximate using clustering by day
        # Count calls per day
        day_counts = defaultdict(int)
        for c in call_list:
            day = c[0] // 86400
            day_counts[day] += 1
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Use simple approximation: deff = 1 + (mean_cluster_size - 1) * 0.5
        # But simpler: effective_n = issued / (1 + (mean_cluster_size - 1))
        # where mean_cluster_size = issued / distinct_days
        if distinct_days > 0:
            mean_cluster = issued / distinct_days
            deff = 1 + (mean_cluster - 1) * 0.5  # conservative ICC=0.5
            effective_n = issued / deff
        else:
            effective_n = 0.0
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_stats(main_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_stats(sealed_calls)

    # Overall stats for reporting
    total_issued = main_issued + sealed_issued
    total_hits = main_hits + sealed_hits
    total_precision = total_hits / total_issued if total_issued > 0 else 0.0
    total_base_rate = total_hits / total_issued if total_issued > 0 else 0.0
    total_distinct_days = len(set(c[0] // 86400 for c in calls))
    # Overall effective_n
    day_counts = defaultdict(int)
    for c in calls:
        day = c[0] // 86400
        day_counts[day] += 1
    if total_distinct_days > 0:
        mean_cluster = total_issued / total_distinct_days
        deff = 1 + (mean_cluster - 1) * 0.5
        total_effective_n = total_issued / deff
    else:
        total_effective_n = 0.0

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base_rate:.6f}")
    print(f"DISTINCT_DAYS={total_distinct_days}")
    print(f"EFFECTIVE_N={total_effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
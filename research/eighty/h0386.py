# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 385
# cycle_index: 53
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import statistics
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()

    # Get all insider purchases (code='P') with trade and filing dates
    cur.execute('''
        SELECT symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY tx_ts
    ''')
    purchases = cur.fetchall()
    if not purchases:
        print("INSUFFICIENT=1")
        return

    # Group purchases by symbol
    purchases_by_symbol = defaultdict(list)
    for symbol_id, tx_ts, filed_ts in purchases:
        purchases_by_symbol[symbol_id].append((tx_ts, filed_ts))

    # Get all insider trades (any code) for median calculation
    cur.execute('''
        SELECT symbol_id, tx_ts, filed_ts
        FROM insider_trades
        ORDER BY tx_ts
    ''')
    all_trades = cur.fetchall()
    trades_by_symbol = defaultdict(list)
    for symbol_id, tx_ts, filed_ts in all_trades:
        trades_by_symbol[symbol_id].append((tx_ts, filed_ts))

    # Prepare to collect events
    events = []  # (symbol_id, decision_ts, label_up)

    two_years = 2 * 365.25 * 24 * 3600  # in seconds

    for symbol_id, symbol_purchases in purchases_by_symbol.items():
        # Get all trades for this symbol for median calculation
        symbol_trades = trades_by_symbol.get(symbol_id, [])
        if len(symbol_trades) < 10:
            continue

        # For each purchase, check if disclosure delay is less than median
        for tx_ts, filed_ts in symbol_purchases:
            # Collect trades in prior 2 years with known disclosure (filed_ts <= current trade time)
            window_trades = []
            for t_tx, t_filed in symbol_trades:
                if t_tx <= tx_ts and t_tx >= (tx_ts - two_years) and t_filed <= tx_ts:
                    window_trades.append(t_filed - t_tx)

            if len(window_trades) < 10:
                continue

            median_delay = statistics.median(window_trades)
            current_delay = filed_ts - tx_ts

            if current_delay < median_delay:
                # This is an entry point: decision date is disclosure date (filed_ts)
                decision_ts = filed_ts

                # Get label: 21 trading days forward return from prices table
                # Find bars for this symbol after decision_ts
                cur.execute('''
                    SELECT open, close
                    FROM bars
                    WHERE symbol_id = ? AND tf = '1d' AND ts > ?
                    ORDER BY ts
                    LIMIT 22
                ''', (symbol_id, decision_ts))
                bars = cur.fetchall()

                if len(bars) < 22:
                    continue  # Not enough data for 21-day horizon

                # Use first bar open as entry, 21st bar close as exit
                entry_price = bars[0][0]
                exit_price = bars[21][1]
                if entry_price <= 0:
                    continue

                fwd_return = (exit_price - entry_price) / entry_price
                up = 1 if fwd_return > 0 else 0

                events.append((symbol_id, decision_ts, up))

    if not events:
        print("INSUFFICIENT=1")
        return

    # Sort events by time for time-based split
    events.sort(key=lambda x: x[1])

    # Split into train (80%) and sealed (20%) by time
    split_idx = int(len(events) * 0.8)
    train_events = events[:split_idx]
    sealed_events = events[split_idx:]

    # Compute metrics for full sample
    issued = len(events)
    hits = sum(1 for _, _, up in events if up == 1)
    precision = hits / issued if issued > 0 else 0
    base_rate = precision  # base rate within issued subset

    # Count distinct days in issued calls
    distinct_days = len(set(ts for _, ts, _ in events))

    # Compute design effect (clustering by day)
    # Group events by day
    events_by_day = defaultdict(list)
    for _, ts, up in events:
        # Convert unix timestamp to date
        day = ts // 86400
        events_by_day[day].append(up)

    cluster_sizes = [len(vals) for vals in events_by_day.values()]
    avg_cluster_size = statistics.mean(cluster_sizes) if cluster_sizes else 1

    # Compute ICC (intra-class correlation)
    p_overall = precision
    if p_overall > 0 and p_overall < 1:
        # Compute variance between cluster proportions
        var_between = 0
        for day, labels in events_by_day.items():
            p_day = sum(labels) / len(labels)
            var_between += (p_day - p_overall) ** 2
        var_between /= len(events_by_day)

        total_var = p_overall * (1 - p_overall)
        icc = var_between / total_var if total_var > 0 else 0
        design_effect = 1 + (avg_cluster_size - 1) * icc
    else:
        design_effect = 1.001  # avoid division by zero

    effective_n = issued / design_effect

    # Sealed era metrics
    sealed_hits = sum(1 for _, _, up in sealed_events if up == 1)
    sealed_precision = sealed_hits / len(sealed_events) if sealed_events else 0

    # Print required output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    conn.close()

if __name__ == "__main__":
    main()
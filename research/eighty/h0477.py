# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 476
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all insider open-market purchases (code P) for active stocks
    # Join with anomalies on same symbol and same trade date where z < -2
    # Decision timestamp is filed_ts (when trade becomes public)
    cur.execute("""
        SELECT 
            it.symbol_id,
            it.tx_ts,
            it.filed_ts,
            date(it.tx_ts, 'unixepoch') as trade_date,
            a.z as anomaly_z,
            s.symbol
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        JOIN anomalies a ON it.symbol_id = a.symbol_id 
            AND date(it.tx_ts, 'unixepoch') = date(a.ts, 'unixepoch')
        WHERE it.code = 'P'
          AND a.z < -2
          AND s.market = 'stocks'
          AND s.active = 1
        ORDER BY it.filed_ts
    """)
    signals = cur.fetchall()

    if not signals:
        print("INSUFFICIENT=1")
        return 0

    # Get opportunities: all code P trades for active stocks (distinct symbol, trade_date)
    cur.execute("""
        SELECT COUNT(DISTINCT it.symbol_id, date(it.tx_ts, 'unixepoch'))
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'P'
          AND s.market = 'stocks'
          AND s.active = 1
    """)
    opportunities = cur.fetchone()[0] or 0

    # Load labels from prediction_outcomes for horizon='21d'
    # Key: (symbol_id, date(ts)) -> up (1 or 0)
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = '21d'
    """)
    labels = {}
    for row in cur.fetchall():
        key = (row['symbol_id'], date(row['ts'], 'unixepoch'))
        labels[key] = row['up']

    # Match signals to labels
    issued = []
    for sig in signals:
        decision_date = date(sig['filed_ts'], 'unixepoch')
        key = (sig['symbol_id'], decision_date)
        if key in labels:
            issued.append({
                'symbol_id': sig['symbol_id'],
                'decision_ts': sig['filed_ts'],
                'decision_date': decision_date,
                'up': labels[key],
                'trade_date': sig['trade_date'],
                'anomaly_z': sig['anomaly_z']
            })

    if not issued:
        print("INSUFFICIENT=1")
        return 0

    # Sort by decision timestamp
    issued.sort(key=lambda x: x['decision_ts'])

    # Hold out most recent 20% as sealed era
    n_sealed = max(1, len(issued) // 5)
    sealed = issued[-n_sealed:]
    unsealed = issued[:-n_sealed]

    # Compute metrics on full issued set
    total_issued = len(issued)
    hits = sum(1 for x in issued if x['up'] == 1)
    precision = hits / total_issued if total_issued else 0.0
    base_rate = precision  # base rate within issued subset

    # Distinct UTC days among issued calls
    distinct_days = len(set(x['decision_date'] for x in issued))

    # Design effect: conservative estimate based on day clustering
    if distinct_days > 0 and distinct_days < total_issued:
        design_effect = total_issued / distinct_days
    else:
        design_effect = 1.01  # minimum > 1
    effective_n = total_issued / design_effect

    # Sealed era precision
    sealed_hits = sum(1 for x in sealed if x['up'] == 1)
    sealed_precision = sealed_hits / len(sealed) if sealed else 0.0

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
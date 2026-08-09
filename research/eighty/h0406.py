# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 405
# cycle_index: 73
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def unix_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load all code P trades with their 21-day outcomes
    cur.execute("""
        SELECT 
            it.accession,
            it.symbol_id,
            it.insider,
            it.code,
            it.shares,
            it.price,
            it.value,
            it.tx_ts,
            it.filed_ts,
            po.up,
            po.fwd_return
        FROM insider_trades it
        LEFT JOIN prediction_outcomes po
            ON it.symbol_id = po.symbol_id
            AND po.horizon = 21
            AND it.filed_ts = po.ts
        WHERE it.code = 'P'
        ORDER BY it.insider, it.filed_ts
    """)
    rows = cur.fetchall()

    if not rows:
        print("INSUFFICIENT=1")
        return 0

    # Group by insider for track record computation
    by_insider = {}
    for r in rows:
        insider = r['insider']
        by_insider.setdefault(insider, []).append(r)

    qualifying_trades = []
    all_p_trades_by_day = {}

    for insider, trades in by_insider.items():
        prior_with_outcome = []
        for t in trades:
            filed_ts = t['filed_ts']
            day = unix_to_date(filed_ts)
            key = (t['symbol_id'], day)
            all_p_trades_by_day.setdefault(key, []).append(t)

            # Check if this trade qualifies as a signal
            if t['value'] >= 100000 and t['up'] is not None:
                if len(prior_with_outcome) >= 3:
                    correct = sum(1 for p in prior_with_outcome if p['up'] == 1)
                    precision = correct / len(prior_with_outcome)
                    if precision >= 0.70:
                        qualifying_trades.append(t)

            # Add to prior track record if it has a known outcome
            if t['up'] is not None:
                prior_with_outcome.append(t)

    if not qualifying_trades:
        print("INSUFFICIENT=1")
        return 0

    # Aggregate to symbol-day: one call per symbol-day if any qualifying trade
    issued_by_day = {}
    for t in qualifying_trades:
        day = unix_to_date(t['filed_ts'])
        key = (t['symbol_id'], day)
        if key not in issued_by_day:
            issued_by_day[key] = t  # keep first qualifying trade for outcome

    # Opportunities: all symbol-days with at least one code P trade
    opportunities = set(all_p_trades_by_day.keys())

    # Outcomes for issued calls
    issued_list = []
    for (sym, day), trade in issued_by_day.items():
        issued_list.append({
            'symbol_id': sym,
            'day': day,
            'filed_ts': trade['filed_ts'],
            'up': trade['up']
        })

    # Sort by date for sealed split
    issued_list.sort(key=lambda x: x['filed_ts'])

    # Split: most recent 20% as sealed
    n_total = len(issued_list)
    n_sealed = max(1, int(n_total * 0.2))
    train = issued_list[:-n_sealed]
    sealed = issued_list[-n_sealed:]

    def compute_precision(items):
        if not items:
            return 0.0
        hits = sum(1 for x in items if x['up'] == 1)
        return hits / len(items)

    # Metrics
    ISSUED = len(issued_list)
    OPPORTUNITIES = len(opportunities)
    PRECISION = compute_precision(train) if train else 0.0
    SEALED_PRECISION = compute_precision(sealed) if sealed else 0.0

    # Base rate within issued subset (actual up rate among issued)
    BASE_RATE = sum(1 for x in issued_list if x['up'] == 1) / ISSUED if ISSUED else 0.0

    # Distinct days among issued calls
    DISTINCT_DAYS = len(set(x['day'] for x in issued_list))

    # Design effect: ISSUED / DISTINCT_DAYS (clustering by day)
    if DISTINCT_DAYS > 0:
        design_effect = ISSUED / DISTINCT_DAYS
        if design_effect <= 1.0:
            design_effect = 1.001
    else:
        design_effect = 1.001
    EFFECTIVE_N = ISSUED / design_effect

    # Print required lines
    print(f"ISSUED={ISSUED}")
    print(f"OPPORTUNITIES={OPPORTUNITIES}")
    print(f"PRECISION={PRECISION:.6f}")
    print(f"BASE_RATE={BASE_RATE:.6f}")
    print(f"DISTINCT_DAYS={DISTINCT_DAYS}")
    print(f"EFFECTIVE_N={EFFECTIVE_N:.6f}")
    print(f"SEALED_PRECISION={SEALED_PRECISION:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
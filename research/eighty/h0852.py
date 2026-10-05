# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 851
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    conn = sqlite3.connect(db_path, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get qualifying insider purchase events: code='P', filed on Friday, disclosure delay <= 2 business days
    # Friday check: strftime('%w', datetime(filed_ts, 'unixepoch')) = '5' (0=Sun, 5=Fri)
    # Delay: filed_ts - tx_ts <= 3*86400 seconds (covers 2 business days including weekend)
    cur.execute("""
        SELECT 
            it.symbol_id,
            it.filed_ts,
            it.tx_ts,
            s.symbol
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'P'
          AND (it.filed_ts - it.tx_ts) <= 259200
          AND strftime('%w', datetime(it.filed_ts, 'unixepoch')) = '5'
        ORDER BY it.filed_ts
    """)
    events = cur.fetchall()

    if not events:
        print("INSUFFICIENT=1")
        return 0

    # Deduplicate by (symbol_id, decision_date) where decision_date = date of filed_ts
    seen = set()
    unique_events = []
    for ev in events:
        decision_date = datetime.utcfromtimestamp(ev['filed_ts']).date()
        key = (ev['symbol_id'], decision_date)
        if key not in seen:
            seen.add(key)
            unique_events.append((ev['symbol_id'], ev['filed_ts'], decision_date, ev['symbol']))

    if not unique_events:
        print("INSUFFICIENT=1")
        return 0

    # For each unique event, get entry (next trading day open) and exit (21 trading days later close)
    results = []
    for symbol_id, filed_ts, decision_date, symbol in unique_events:
        # Get daily bars for this symbol after filed_ts
        cur.execute("""
            SELECT ts, open, close
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts > ?
            ORDER BY ts
            LIMIT 22
        """, (symbol_id, filed_ts))
        bars = cur.fetchall()
        if len(bars) < 22:
            continue
        entry_price = bars[0]['open']
        exit_price = bars[21]['close']
        ret = (exit_price - entry_price) / entry_price
        hit = 1 if ret > 0 else 0
        results.append({
            'symbol_id': symbol_id,
            'symbol': symbol,
            'decision_date': decision_date,
            'filed_ts': filed_ts,
            'entry_price': entry_price,
            'exit_price': exit_price,
            'return': ret,
            'hit': hit
        })

    if not results:
        print("INSUFFICIENT=1")
        return 0

    # Determine sealed era: most recent 20% of time span
    decision_dates = [r['decision_date'] for r in results]
    min_date = min(decision_dates)
    max_date = max(decision_dates)
    total_days = (max_date - min_date).days
    if total_days <= 0:
        print("INSUFFICIENT=1")
        return 0
    split_date = max_date - timedelta(days=int(total_days * 0.2))

    # Split results
    in_sample = [r for r in results if r['decision_date'] < split_date]
    sealed = [r for r in results if r['decision_date'] >= split_date]

    # Compute metrics
    issued = len(results)
    opportunities = len(unique_events)  # decision points considered
    hits = sum(r['hit'] for r in results)
    precision = hits / issued if issued else 0.0
    base_rate = precision  # predicted class is "up" for all issued calls

    # Distinct UTC days among issued calls
    distinct_days = len(set(r['decision_date'] for r in results))

    # Design effect and effective N
    if distinct_days > 0:
        design_effect = max(1.01, issued / distinct_days)
    else:
        design_effect = 1.01
    effective_n = issued / design_effect

    # Sealed precision
    sealed_issued = len(sealed)
    sealed_hits = sum(r['hit'] for r in sealed)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0.0

    # Output required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 777
# cycle_index: 47
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Universe: symbols with >=252 daily bars, avg daily volume > 100k
    cur.execute("""
        SELECT symbol_id, COUNT(*) as bar_count, AVG(volume) as avg_vol
        FROM bars 
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING bar_count >= 252 AND avg_vol > 100000
    """)
    universe = {row['symbol_id'] for row in cur.fetchall()}
    if not universe:
        print("INSUFFICIENT=1")
        return 0

    # Get all open-market insider purchases (code='P') for universe symbols
    placeholders = ','.join('?' * len(universe))
    cur.execute(f"""
        SELECT symbol_id, tx_ts, filed_ts, shares, price, value, insider, title
        FROM insider_trades
        WHERE code = 'P' AND symbol_id IN ({placeholders})
        ORDER BY filed_ts
    """, list(universe))
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # For each trade, evaluate conditions at tx_ts using data available at tx_ts
    qualifying = []  # (symbol_id, decision_date, filed_ts, entry_ts)
    for tr in trades:
        sym = tr['symbol_id']
        tx_ts = tr['tx_ts']
        filed_ts = tr['filed_ts']

        # Get bar at tx_ts date (trade day)
        cur.execute("""
            SELECT close, volume FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            AND date(ts, 'unixepoch') = date(?, 'unixepoch')
        """, (sym, tx_ts))
        bar = cur.fetchone()
        if not bar:
            continue
        close_tx, vol_tx = bar['close'], bar['volume']

        # 252-day low up to tx_ts (inclusive)
        cur.execute("""
            SELECT MIN(close) FROM (
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
                ORDER BY ts DESC LIMIT 252
            )
        """, (sym, tx_ts))
        low_252 = cur.fetchone()[0]
        if low_252 is None:
            continue

        # 20th percentile of volume over 252 days up to tx_ts
        cur.execute("""
            SELECT volume FROM (
                SELECT volume FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
                ORDER BY ts DESC LIMIT 252
            ) ORDER BY volume ASC LIMIT 1 OFFSET 50
        """, (sym, tx_ts))
        vol_p20 = cur.fetchone()
        if not vol_p20:
            continue
        vol_p20 = vol_p20[0]

        # Conditions: close within 2% of 252-day low, volume in bottom 20%
        if close_tx <= low_252 * 1.02 and vol_tx <= vol_p20:
            decision_date = datetime.utcfromtimestamp(filed_ts).date()
            qualifying.append((sym, decision_date, filed_ts))

    if not qualifying:
        print("INSUFFICIENT=1")
        return 0

    # Group by (symbol_id, decision_date) - one observation per symbol per day
    calls = {}
    for sym, ddate, filed_ts in qualifying:
        key = (sym, ddate)
        if key not in calls:
            calls[key] = filed_ts  # earliest filed_ts for that day

    # For each call, compute 21-day forward return from filed_ts
    results = []  # (symbol_id, decision_date, filed_ts, hit)
    for (sym, ddate), filed_ts in calls.items():
        # Entry: first bar on or after filed_ts
        cur.execute("""
            SELECT close, ts FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            ORDER BY ts ASC LIMIT 1
        """, (sym, filed_ts))
        entry_bar = cur.fetchone()
        if not entry_bar:
            continue
        entry_close, entry_ts = entry_bar['close'], entry_bar['ts']

        # Exit: bar 21 trading days after entry
        cur.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts > ?
            ORDER BY ts ASC LIMIT 21
        """, (sym, entry_ts))
        exit_bars = cur.fetchall()
        if len(exit_bars) < 21:
            continue
        exit_close = exit_bars[-1]['close']

        fwd_return = (exit_close - entry_close) / entry_close
        hit = 1 if fwd_return > 0 else 0
        results.append((sym, ddate, filed_ts, hit))

    if not results:
        print("INSUFFICIENT=1")
        return 0

    # Sort by decision date, hold out most recent 20% as sealed
    results.sort(key=lambda x: x[2])  # sort by filed_ts
    n = len(results)
    split_idx = int(n * 0.8)
    train = results[:split_idx]
    sealed = results[split_idx:]

    def compute_metrics(data, label):
        if not data:
            return
        issued = len(data)
        hits = sum(r[3] for r in data)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate within issued subset
        distinct_days = len(set(r[1] for r in data))
        # Design effect: average calls per day (clustering)
        calls_per_day = {}
        for r in data:
            calls_per_day[r[1]] = calls_per_day.get(r[1], 0) + 1
        avg_cluster = sum(calls_per_day.values()) / len(calls_per_day) if calls_per_day else 1
        design_effect = 1 + (avg_cluster - 1) * 0.5  # assume ICC=0.5
        effective_n = issued / design_effect if design_effect > 0 else issued
        print(f"{label}_ISSUED={issued}")
        print(f"{label}_OPPORTUNITIES={issued}")  # each call is a decision point considered
        print(f"{label}_PRECISION={precision:.6f}")
        print(f"{label}_BASE_RATE={base_rate:.6f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.6f}")

    compute_metrics(train, "TRAIN")
    compute_metrics(sealed, "SEALED")

    # Final required output format (using sealed for SEALED_PRECISION, train for others)
    issued = len(train)
    hits = sum(r[3] for r in train)
    precision = hits / issued if issued else 0.0
    base_rate = hits / issued if issued else 0.0
    distinct_days = len(set(r[1] for r in train))
    calls_per_day = {}
    for r in train:
        calls_per_day[r[1]] = calls_per_day.get(r[1], 0) + 1
    avg_cluster = sum(calls_per_day.values()) / len(calls_per_day) if calls_per_day else 1
    design_effect = 1 + (avg_cluster - 1) * 0.5
    effective_n = issued / design_effect if design_effect > 0 else issued
    sealed_precision = sum(r[3] for r in sealed) / len(sealed) if sealed else 0.0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
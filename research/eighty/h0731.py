# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 730
# cycle_index: 57
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, date
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # HYPOTHESIS
    # MECHANISM: Insiders executing open-market purchases at or near the day's low
    # demonstrate conviction and favorable execution, signaling intraday oversold
    # conditions that revert over the next week.
    # HORIZON: 1w (5 trading days forward from filing date)
    # UNIVERSE: Symbols with daily bars and insider trades (code='P')
    # ENTRY: On filing date (filed_ts), if trade price <= low + 1% of daily range
    # on trade date (tx_ts), enter at next available close.
    # ABSTAIN: No bar on trade date, zero daily range, no forward bars, or
    # multiple qualifying trades same symbol-day (deduplicate).
    # CLAIM: 5-day forward return > 0 with precision above base rate.

    # Get all open-market purchases with trade-date bars
    cur.execute("""
        SELECT
            it.symbol_id,
            it.tx_ts,
            it.filed_ts,
            it.price AS trade_price,
            b.low,
            b.high,
            b.ts AS bar_ts
        FROM insider_trades it
        JOIN bars b
            ON it.symbol_id = b.symbol_id
            AND date(it.tx_ts, 'unixepoch') = date(b.ts, 'unixepoch')
            AND b.tf = '1d'
        WHERE it.code = 'P'
          AND (b.high - b.low) > 0
        ORDER BY it.filed_ts
    """)
    all_trades = cur.fetchall()

    if not all_trades:
        print("INSUFFICIENT=1")
        return 0

    opportunities = len(all_trades)

    # Filter to qualifying trades (price near low)
    qualifying = []
    for t in all_trades:
        low, high, price = t['low'], t['high'], t['trade_price']
        if price <= low + 0.01 * (high - low):
            qualifying.append(t)

    if not qualifying:
        print("INSUFFICIENT=1")
        return 0

    # For each qualifying trade, compute 5-day forward return from filing date
    results = []
    for t in qualifying:
        symbol_id = t['symbol_id']
        filed_ts = t['filed_ts']
        filed_date = datetime.utcfromtimestamp(filed_ts).date()

        # Entry bar: first daily bar on or after filed_date
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') >= ?
            ORDER BY ts LIMIT 1
        """, (symbol_id, filed_date.isoformat()))
        entry = cur.fetchone()
        if not entry:
            continue
        entry_ts, entry_close = entry['ts'], entry['close']

        # Exit bar: 5 trading days after entry
        cur.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts > ?
            ORDER BY ts LIMIT 5
        """, (symbol_id, entry_ts))
        exits = cur.fetchall()
        if len(exits) < 5:
            continue
        exit_close = exits[-1]['close']

        fwd_return = (exit_close - entry_close) / entry_close
        hit = 1 if fwd_return > 0 else 0
        results.append({
            'symbol_id': symbol_id,
            'filed_ts': filed_ts,
            'filed_date': filed_date,
            'hit': hit,
            'fwd_return': fwd_return
        })

    if not results:
        print("INSUFFICIENT=1")
        return 0

    # Deduplicate by (symbol_id, filed_date) - one observation per symbol-day
    seen = set()
    deduped = []
    for r in results:
        key = (r['symbol_id'], r['filed_date'])
        if key not in seen:
            seen.add(key)
            deduped.append(r)

    results = deduped
    issued = len(results)

    if issued < 30:
        print("INSUFFICIENT=1")
        return 0

    # Sort by time for temporal split
    results.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(results) * 0.8)
    train = results[:split_idx]
    sealed = results[split_idx:]

    def compute_design_effect(data):
        if not data:
            return 1.0
        # Cluster by symbol
        clusters = defaultdict(list)
        for d in data:
            clusters[d['symbol_id']].append(d['hit'])
        cluster_sizes = [len(v) for v in clusters.values()]
        n_avg = sum(cluster_sizes) / len(cluster_sizes)
        # Overall mean
        p = sum(d['hit'] for d in data) / len(data)
        # Between-cluster variance of means
        cluster_means = [sum(v)/len(v) for v in clusters.values()]
        between_var = sum((m - p)**2 for m in cluster_means) / len(cluster_means)
        # Within-cluster variance (binomial)
        within_var = p * (1 - p)
        if within_var == 0:
            return 1.0
        icc = between_var / (between_var + within_var)
        icc = max(0.0, min(1.0, icc))
        return 1 + (n_avg - 1) * icc

    def compute_metrics(data, deff):
        n = len(data)
        hits = sum(d['hit'] for d in data)
        precision = hits / n if n else 0.0
        base_rate = precision  # base rate of predicted class (up) within issued
        distinct_days = len(set(d['filed_date'] for d in data))
        effective_n = n / deff if deff > 1 else n
        return n, hits, precision, base_rate, distinct_days, effective_n

    deff = compute_design_effect(results)
    issued_n, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(results, deff)

    sealed_precision = 0.0
    if sealed:
        sealed_hits = sum(d['hit'] for d in sealed)
        sealed_precision = sealed_hits / len(sealed)

    # Invariants
    if distinct_days > issued_n:
        print("INSUFFICIENT=1", file=sys.stderr)
        return 0
    if effective_n >= issued_n:
        print("INSUFFICIENT=1", file=sys.stderr)
        return 0

    distinct_days_final = distinct_days
    if distinct_days_final < 10:
        print("INSUFFICIENT=1")
        return 0

    print(f"ISSUED={issued_n}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_final}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    return 0

if __name__ == '__main__':
    sys.exit(main())
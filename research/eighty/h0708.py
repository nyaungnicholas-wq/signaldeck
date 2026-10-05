# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 707
# cycle_index: 34
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta, timezone

DB_PATH = 'data/signaldeck.db'

def unix_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day, tzinfo=timezone.utc).timestamp())

def main():
    conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get max bar date for 1d bars
    cur.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
    max_bar_ts = cur.fetchone()[0]
    if max_bar_ts is None:
        print("INSUFFICIENT=1")
        return 0
    max_bar_date = unix_to_date(max_bar_ts)
    # Need ~21 trading days = ~30 calendar days buffer
    cutoff_date = max_bar_date - timedelta(days=35)
    cutoff_ts = date_to_unix(cutoff_date)

    # Step 1: Get officer open-market purchases with daily low on trade date
    query = """
    SELECT 
        it.symbol_id,
        it.tx_ts,
        it.filed_ts,
        it.price as trade_price,
        b.low as daily_low,
        date(it.tx_ts, 'unixepoch') as trade_date,
        date(it.filed_ts, 'unixepoch') as decision_date
    FROM insider_trades it
    JOIN bars b ON b.symbol_id = it.symbol_id 
        AND b.tf = '1d'
        AND date(it.tx_ts, 'unixepoch') = date(b.ts, 'unixepoch')
    WHERE it.code = 'P'
      AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' OR it.title LIKE '%COO%' OR it.title LIKE '%PRESIDENT%')
      AND it.price <= 1.005 * b.low
      AND it.filed_ts <= ?
      AND it.tx_ts <= it.filed_ts
    """
    cur.execute(query, (cutoff_ts,))
    trades = cur.fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # Collect symbol_ids and decision date range
    symbol_ids = list(set(t['symbol_id'] for t in trades))
    decision_dates = [unix_to_date(t['filed_ts']) for t in trades]
    min_decision_date = min(decision_dates)
    max_decision_date = max(decision_dates)

    # Step 2: Fetch all 1d bars for these symbols from min_decision_date to max_bar_date
    placeholders = ','.join('?' * len(symbol_ids))
    bars_query = f"""
    SELECT symbol_id, ts, close
    FROM bars
    WHERE tf='1d'
      AND symbol_id IN ({placeholders})
      AND ts >= ?
      AND ts <= ?
    ORDER BY symbol_id, ts
    """
    min_decision_ts = date_to_unix(min_decision_date)
    cur.execute(bars_query, symbol_ids + [min_decision_ts, max_bar_ts])
    bars_rows = cur.fetchall()

    # Organize bars by symbol_id
    bars_by_symbol = {}
    for row in bars_rows:
        sid = row['symbol_id']
        if sid not in bars_by_symbol:
            bars_by_symbol[sid] = []
        bars_by_symbol[sid].append((row['ts'], row['close']))

    # Step 3: For each trade, compute 21-day forward return from decision date
    results = []
    for t in trades:
        sid = t['symbol_id']
        decision_ts = t['filed_ts']
        decision_date = unix_to_date(decision_ts)

        if sid not in bars_by_symbol:
            continue
        bars = bars_by_symbol[sid]
        if not bars:
            continue

        # Find entry bar: first bar on or after decision_ts
        entry_idx = None
        for i, (ts, close) in enumerate(bars):
            if ts >= decision_ts:
                entry_idx = i
                entry_close = close
                entry_ts = ts
                break
        if entry_idx is None:
            continue

        # Find exit bar: 21 trading days after entry
        exit_idx = entry_idx + 21
        if exit_idx >= len(bars):
            continue
        exit_ts, exit_close = bars[exit_idx]

        fwd_return = (exit_close - entry_close) / entry_close
        hit = 1 if fwd_return > 0 else 0

        results.append({
            'symbol_id': sid,
            'decision_ts': decision_ts,
            'decision_date': decision_date,
            'entry_ts': entry_ts,
            'exit_ts': exit_ts,
            'fwd_return': fwd_return,
            'hit': hit
        })

    if not results:
        print("INSUFFICIENT=1")
        return 0

    # Step 4: Deduplicate by (symbol_id, decision_date) - keep first
    seen = set()
    deduped = []
    for r in results:
        key = (r['symbol_id'], r['decision_date'])
        if key not in seen:
            seen.add(key)
            deduped.append(r)

    # Step 5: Sort by decision_ts
    deduped.sort(key=lambda x: x['decision_ts'])

    # Step 6: Split 80/20 by time (most recent 20% = sealed)
    n = len(deduped)
    split_idx = int(n * 0.8)
    training = deduped[:split_idx]
    sealed = deduped[split_idx:]

    # Step 7: Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0, 0
        issued = len(calls)
        hits = sum(c['hit'] for c in calls)
        precision = hits / issued if issued > 0 else 0
        base_rate = precision  # base rate within issued subset = precision
        distinct_days = len(set(c['decision_date'] for c in calls))
        # Design effect: assume intra-day correlation rho=0.5
        avg_cluster = issued / distinct_days if distinct_days > 0 else 1
        design_effect = 1 + (avg_cluster - 1) * 0.5
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_precision, train_base, train_days, train_eff = compute_metrics(training)
    sealed_issued, sealed_hits, sealed_precision, sealed_base, sealed_days, sealed_eff = compute_metrics(sealed)

    total_issued = train_issued + sealed_issued
    total_opportunities = len(trades)  # decision points considered
    total_precision = (train_hits + sealed_hits) / total_issued if total_issued > 0 else 0
    total_base = total_precision
    total_days = len(set(c['decision_date'] for c in deduped))
    total_eff = train_eff + sealed_eff

    # Print required output
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base:.6f}")
    print(f"DISTINCT_DAYS={total_days}")
    print(f"EFFECTIVE_N={total_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
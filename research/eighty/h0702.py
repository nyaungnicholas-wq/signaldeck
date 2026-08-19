# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 701
# cycle_index: 28
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load SharesOutstanding from fundamentals
    so_rows = cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding'
        ORDER BY symbol_id, as_of, fetched_at
    """).fetchall()

    if not so_rows:
        print("INSUFFICIENT=1")
        return 0

    # Group by symbol_id, for each quarter (as_of) keep earliest fetched_at (when first known)
    so_by_symbol = defaultdict(list)
    for r in so_rows:
        so_by_symbol[r['symbol_id']].append({
            'value': r['value'],
            'as_of': r['as_of'],
            'fetched_at': r['fetched_at']
        })

    # For each symbol, compute QoQ decline for each quarter where we have prior quarter
    # and both were known by some fetched_at
    qoq_by_symbol = defaultdict(dict)  # symbol_id -> {as_of: (decline_pct, known_at_fetched_at)}
    for sym_id, records in so_by_symbol.items():
        # Sort by as_of (quarter)
        records.sort(key=lambda x: x['as_of'])
        for i in range(1, len(records)):
            curr = records[i]
            prev = records[i-1]
            if prev['value'] and prev['value'] > 0:
                decline = (curr['value'] - prev['value']) / prev['value']
                # This QoQ comparison becomes known at max(curr['fetched_at'], prev['fetched_at'])
                known_at = max(curr['fetched_at'], prev['fetched_at'])
                qoq_by_symbol[sym_id][curr['as_of']] = (decline, known_at)

    # 2. Load insider sales (code='S')
    insider_rows = cur.execute("""
        SELECT accession, symbol_id, price, filed_ts
        FROM insider_trades
        WHERE code = 'S'
        ORDER BY filed_ts
    """).fetchall()

    if not insider_rows:
        print("INSUFFICIENT=1")
        return 0

    # 3. Get symbols that have both SharesOutstanding and insider trades
    symbols_with_both = set(so_by_symbol.keys()) & set(r['symbol_id'] for r in insider_rows)
    if not symbols_with_both:
        print("INSUFFICIENT=1")
        return 0

    # 4. Load daily bars for relevant symbols (tf='1d')
    # We'll fetch bars per symbol as needed for SMA and forward returns
    # But first, let's get the date range we need
    min_filed = min(r['filed_ts'] for r in insider_rows if r['symbol_id'] in symbols_with_both)
    max_filed = max(r['filed_ts'] for r in insider_rows if r['symbol_id'] in symbols_with_both)

    # 5. Process each insider sale in chronological order
    qualifying_sales = []  # (symbol_id, filed_ts, price, quarter_key)
    
    # For counting sales per symbol per quarter
    sales_count = defaultdict(int)  # (symbol_id, quarter) -> count

    for row in insider_rows:
        sym_id = row['symbol_id']
        if sym_id not in symbols_with_both:
            continue
        filed_ts = row['filed_ts']
        price = row['price']

        # Find most recent SharesOutstanding quarter known at filed_ts
        best_as_of = None
        best_decline = None
        for as_of, (decline, known_at) in qoq_by_symbol[sym_id].items():
            if known_at <= filed_ts:
                if best_as_of is None or as_of > best_as_of:
                    best_as_of = as_of
                    best_decline = decline

        if best_decline is None or best_decline >= -0.02:  # need >2% decline (i.e., decline < -0.02)
            continue

        # Check 200-day SMA at filed_ts
        # Get daily bars up to filed_ts
        bars = cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
            ORDER BY ts DESC LIMIT 200
        """, (sym_id, filed_ts)).fetchall()

        if len(bars) < 200:
            continue

        sma200 = sum(b['close'] for b in bars) / 200
        if price <= sma200:
            continue

        # Determine quarter of filed_ts (quarter end as_of)
        dt = datetime.fromtimestamp(filed_ts, tz=timezone.utc)
        quarter = (dt.month - 1) // 3
        quarter_key = (dt.year, quarter)

        # Count qualifying sales in this quarter so far
        sales_count[(sym_id, quarter_key)] += 1
        if sales_count[(sym_id, quarter_key)] < 2:
            continue

        # This is a qualifying sale
        qualifying_sales.append({
            'symbol_id': sym_id,
            'filed_ts': filed_ts,
            'price': price,
            'quarter_key': quarter_key
        })

    if not qualifying_sales:
        print("INSUFFICIENT=1")
        return 0

    # 6. Compute 21-day forward returns for qualifying sales
    results = []  # (filed_ts, symbol_id, fwd_return_negative)
    for sale in qualifying_sales:
        sym_id = sale['symbol_id']
        filed_ts = sale['filed_ts']

        # Find decision bar (latest daily bar <= filed_ts)
        decision_bar = cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
            ORDER BY ts DESC LIMIT 1
        """, (sym_id, filed_ts)).fetchone()

        if not decision_bar:
            continue

        decision_ts = decision_bar['ts']
        decision_close = decision_bar['close']

        # Get next 21 daily bars after decision_ts
        future_bars = cur.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts > ?
            ORDER BY ts ASC LIMIT 21
        """, (sym_id, decision_ts)).fetchall()

        if len(future_bars) < 21:
            continue

        future_close = future_bars[-1]['close']
        fwd_return = (future_close - decision_close) / decision_close
        is_negative = fwd_return < 0

        results.append({
            'filed_ts': filed_ts,
            'symbol_id': sym_id,
            'negative': is_negative
        })

    if not results:
        print("INSUFFICIENT=1")
        return 0

    # 7. Sort by filed_ts, hold out most recent 20% as sealed era
    results.sort(key=lambda x: x['filed_ts'])
    n = len(results)
    split_idx = int(n * 0.8)
    main_results = results[:split_idx]
    sealed_results = results[split_idx:]

    # 8. Compute metrics
    def compute_metrics(res_list):
        if not res_list:
            return 0, 0, 0, 0, 0
        issued = len(res_list)
        hits = sum(1 for r in res_list if r['negative'])
        precision = hits / issued if issued > 0 else 0
        base_rate = precision  # base rate within issued subset = actual negative rate
        distinct_days = len(set(datetime.fromtimestamp(r['filed_ts'], tz=timezone.utc).date() for r in res_list))
        # Design effect: assume calls on same day are perfectly correlated
        design_effect = issued / distinct_days if distinct_days > 0 else 1
        effective_n = issued / design_effect if design_effect > 0 else 0
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_main, hits_main, prec_main, base_main, days_main, eff_main = compute_metrics(main_results)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, days_sealed, eff_sealed = compute_metrics(sealed_results)

    # OPPORTUNITIES = total decision points considered (insider sales with code='S' for symbols with both)
    opportunities = sum(1 for r in insider_rows if r['symbol_id'] in symbols_with_both)

    # 9. Output
    print(f"ISSUED={issued_main}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={prec_main:.6f}")
    print(f"BASE_RATE={base_main:.6f}")
    print(f"DISTINCT_DAYS={days_main}")
    print(f"EFFECTIVE_N={eff_main:.6f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
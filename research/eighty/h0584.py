# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 583
# cycle_index: 1
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Step 1: Get all insider trades (purchases and sales) with their dates.
    cur.execute("""
        SELECT 
            symbol_id,
            code,
            value,
            CAST(filed_ts AS INTEGER) as filed_ts,
            DATE(filed_ts, 'unixepoch') as decision_date
        FROM insider_trades
        WHERE code IN ('P', 'S')
    """)
    trades = cur.fetchall()

    # Group trades by (symbol_id, decision_date) to check conditions.
    trade_groups = {}
    for row in trades:
        sym = row['symbol_id']
        dec_date = row['decision_date']
        key = (sym, dec_date)
        if key not in trade_groups:
            trade_groups[key] = {'purchases': [], 'sales': []}
        if row['code'] == 'P':
            trade_groups[key]['purchases'].append(row['value'])
        else:
            trade_groups[key]['sales'].append(1)

    # Step 2: Get daily bars for volume and price. Only for symbols present in insider_trades.
    symbols = set(sym for sym, _ in trade_groups.keys())
    placeholders = ','.join(['?'] * len(symbols))
    cur.execute(f"""
        SELECT 
            symbol_id,
            ts,
            close,
            volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, list(symbols))
    bars = cur.fetchall()

    # Organize bars per symbol.
    bars_by_symbol = {}
    for row in bars:
        sym = row['symbol_id']
        if sym not in bars_by_symbol:
            bars_by_symbol[sym] = []
        bars_by_symbol[sym].append(row)

    # For each symbol, compute rolling 20-day average dollar volume.
    avg_dollar_vol = {}
    for sym, sym_bars in bars_by_symbol.items():
        n = len(sym_bars)
        if n < 21:
            continue
        for i in range(20, n):
            window = sym_bars[i-20:i]
            total_dollar = sum(r['close'] * r['volume'] for r in window)
            avg = total_dollar / 20.0
            dec_date = datetime.utcfromtimestamp(sym_bars[i]['ts']).strftime('%Y-%m-%d')
            avg_dollar_vol[(sym, dec_date)] = avg

    # Step 3: Determine issued calls.
    issued_calls = []
    opportunities = 0
    for (sym, dec_date), group in trade_groups.items():
        opportunities += 1
        if sym not in bars_by_symbol:
            continue
        sym_bars = bars_by_symbol[sym]
        # Find bar on decision date (or latest before).
        dec_ts = None
        for bar in sym_bars:
            bar_date = datetime.utcfromtimestamp(bar['ts']).strftime('%Y-%m-%d')
            if bar_date == dec_date:
                dec_ts = bar['ts']
                break
        if dec_ts is None:
            continue
        # Need at least 20 prior daily bars.
        prior_bars = [b for b in sym_bars if b['ts'] < dec_ts]
        if len(prior_bars) < 20:
            continue
        # Average dollar volume over 20 days before decision date.
        if (sym, dec_date) not in avg_dollar_vol:
            continue
        avg_vol = avg_dollar_vol[(sym, dec_date)]
        if avg_vol < 1_000_000:
            continue
        # Closing price on decision date.
        dec_bar = None
        for bar in sym_bars:
            if bar['ts'] == dec_ts:
                dec_bar = bar
                break
        if dec_bar is None:
            continue
        close_price = dec_bar['close']
        if close_price < 2.0:
            continue
        # No sale on same date.
        if group['sales']:
            continue
        # At least one qualifying purchase.
        qualifying = False
        for val in group['purchases']:
            if val >= 0.1 * avg_vol:
                qualifying = True
                break
        if not qualifying:
            continue
        # Find price 21 trading days later.
        future_price = None
        future_count = 0
        for bar in sym_bars:
            if bar['ts'] > dec_ts:
                future_count += 1
                if future_count == 21:
                    future_price = bar['close']
                    break
        if future_price is None:
            continue
        hit = 1 if future_price > close_price else 0
        issued_calls.append({
            'symbol_id': sym,
            'decision_date': dec_date,
            'hit': hit
        })

    conn.close()

    issued = len(issued_calls)
    if issued == 0:
        print("INSUFFICIENT=1")
        return

    hits = sum(c['hit'] for c in issued_calls)
    precision = hits / issued
    base_rate = precision  # Within issued subset.

    # Distinct days among issued calls.
    distinct_days = len(set(c['decision_date'] for c in issued_calls))

    # Design effect: calls per day.
    day_counts = {}
    for c in issued_calls:
        d = c['decision_date']
        day_counts[d] = day_counts.get(d, 0) + 1
    avg_per_day = issued / distinct_days
    # Design effect = 1 + (avg_per_day - 1) * intraclass correlation.
    # Approximate ICC using average squared deviation from mean.
    if distinct_days > 1:
        sum_sq_dev = sum((cnt - avg_per_day) ** 2 for cnt in day_counts.values())
        variance = sum_sq_dev / (distinct_days - 1)
        icc = max(0, variance / (avg_per_day ** 2)) if avg_per_day > 0 else 0
    else:
        icc = 0
    design_effect = 1 + (avg_per_day - 1) * icc
    effective_n = issued / design_effect if design_effect > 0 else issued

    # Sealed era: most recent 20% of issued calls by date.
    if issued >= 5:
        sorted_calls = sorted(issued_calls, key=lambda x: x['decision_date'])
        cutoff = int(issued * 0.8)
        sealed_calls = sorted_calls[cutoff:]
        sealed_hits = sum(c['hit'] for c in sealed_calls)
        sealed_issued = len(sealed_calls)
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    else:
        sealed_precision = 0.0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
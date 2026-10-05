# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 774
# cycle_index: 44
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load insider purchases (code='P') by officers
    cur.execute("""
        SELECT it.accession, it.symbol_id, it.insider, it.title, it.code,
               it.shares, it.price, it.value, it.tx_ts, it.filed_ts
        FROM insider_trades it
        WHERE it.code = 'P'
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' OR it.title LIKE '%COO%' 
               OR it.title LIKE '%PRESIDENT%' OR it.title LIKE '%CHIEF%')
          AND it.filed_ts IS NOT NULL
          AND it.tx_ts IS NOT NULL
          AND it.value > 0
        ORDER BY it.filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # 2. Load daily bars for all symbols in trades
    symbol_ids = list(set(t['symbol_id'] for t in trades))
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars = cur.fetchall()

    # Organize bars by symbol
    bars_by_symbol = {}
    for b in bars:
        bars_by_symbol.setdefault(b['symbol_id'], []).append((b['ts'], b['close']))

    # 3. Process each trade: compute features at filed_ts (decision time)
    opportunities = []
    for t in trades:
        sym = t['symbol_id']
        filed_ts = t['filed_ts']
        tx_ts = t['tx_ts']
        value = t['value']
        insider = t['insider']

        # Disclosure delay in days
        delay_days = (filed_ts - tx_ts) / 86400.0
        if delay_days > 2:
            continue

        # Trailing 12-month average purchase value for this insider
        cutoff = filed_ts - 365 * 86400
        cur.execute("""
            SELECT AVG(value) FROM insider_trades
            WHERE insider = ? AND code = 'P' AND filed_ts >= ? AND filed_ts < ?
        """, (insider, cutoff, filed_ts))
        row = cur.fetchone()
        avg_val = row[0] if row and row[0] else 0
        if avg_val == 0 or value < 2 * avg_val:
            continue

        # Get price at filed_ts (use close of that day or prior)
        sym_bars = bars_by_symbol.get(sym, [])
        if not sym_bars:
            continue
        # Find bar at or before filed_ts
        filed_day = filed_ts // 86400 * 86400
        price_at = None
        bars_before = [(ts, c) for ts, c in sym_bars if ts <= filed_day]
        if not bars_before:
            continue
        price_at = bars_before[-1][1]

        # 52-week high prior to filed_ts
        year_ago = filed_day - 365 * 86400
        bars_52w = [c for ts, c in bars_before if ts >= year_ago]
        if len(bars_52w) < 50:  # need sufficient history
            continue
        high_52w = max(bars_52w)
        if price_at >= 0.85 * high_52w:  # not a >15% pullback
            continue

        # Forward 21-day return from bars
        # Find index of filed_day bar
        idx = next(i for i, (ts, _) in enumerate(bars_before) if ts == filed_day)
        if idx + 21 >= len(sym_bars):
            continue
        fwd_price = sym_bars[idx + 21][1]
        fwd_return = (fwd_price - price_at) / price_at

        opportunities.append({
            'filed_ts': filed_ts,
            'symbol_id': sym,
            'fwd_return': fwd_return,
            'up': 1 if fwd_return > 0 else 0
        })

    if not opportunities:
        print("INSUFFICIENT=1")
        return 0

    # 4. Sort by decision time, hold out most recent 20% as sealed
    opportunities.sort(key=lambda x: x['filed_ts'])
    n = len(opportunities)
    split = int(n * 0.8)
    train = opportunities[:split]
    sealed = opportunities[split:]

    def compute_metrics(ops):
        if not ops:
            return None
        issued = len(ops)
        hits = sum(1 for o in ops if o['up'] == 1)
        precision = hits / issued if issued else 0
        base_rate = precision  # within issued subset, base rate = precision for binary up
        distinct_days = len(set(o['filed_ts'] // 86400 for o in ops))
        # Design effect: cluster by day, compute variance inflation
        day_counts = {}
        for o in ops:
            d = o['filed_ts'] // 86400
            day_counts[d] = day_counts.get(d, 0) + 1
        if len(day_counts) > 1:
            mean_c = issued / len(day_counts)
            var_c = sum((c - mean_c) ** 2 for c in day_counts.values()) / len(day_counts)
            deff = 1 + (mean_c - 1) * (var_c / (mean_c ** 2)) if mean_c > 0 else 1
        else:
            deff = 1
        effective_n = issued / deff if deff > 0 else issued
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_m = compute_metrics(train)
    sealed_m = compute_metrics(sealed)

    if not train_m or not sealed_m:
        print("INSUFFICIENT=1")
        return 0

    # 5. Output required lines
    print(f"ISSUED={train_m['issued']}")
    print(f"OPPORTUNITIES={n}")
    print(f"PRECISION={train_m['precision']:.6f}")
    print(f"BASE_RATE={train_m['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_m['distinct_days']}")
    print(f"EFFECTIVE_N={train_m['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_m['precision']:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
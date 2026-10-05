# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 761
# cycle_index: 31
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def utc_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def is_friday(d):
    return d.weekday() == 4

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Universe symbols: have insider_trades, news, and >=252 daily bars since 2018-07-26
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE EXISTS (SELECT 1 FROM insider_trades it WHERE it.symbol_id = s.id)
          AND EXISTS (SELECT 1 FROM news n WHERE n.symbol_id = s.id)
          AND (
            SELECT COUNT(*) FROM bars b
            WHERE b.symbol_id = s.id AND b.tf = '1d' AND b.ts >= 1532563200
          ) >= 252
    """)
    universe = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not universe:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = list(universe.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # Load all daily bars for universe symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))

    # Load all news dates per symbol
    cur.execute(f"""
        SELECT symbol_id, date(ts, 'unixepoch') as news_date
        FROM news
        WHERE symbol_id IN ({placeholders})
    """, symbol_ids)
    news_dates_by_symbol = defaultdict(set)
    for row in cur.fetchall():
        news_dates_by_symbol[row['symbol_id']].add(row['news_date'])

    # Load insider purchases (code='P')
    cur.execute(f"""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code = 'P' AND symbol_id IN ({placeholders})
        ORDER BY filed_ts
    """, symbol_ids)
    purchases = cur.fetchall()

    opportunities = 0
    issued = []
    entry_dates = []

    for p in purchases:
        sym_id = p['symbol_id']
        filed_ts = p['filed_ts']
        filing_date = utc_date(filed_ts)

        if not is_friday(filing_date):
            continue

        bars = bars_by_symbol.get(sym_id, [])
        if not bars:
            continue

        # Find next trading day after filing_date
        entry_idx = None
        for i, (ts, _) in enumerate(bars):
            bar_date = utc_date(ts)
            if bar_date > filing_date:
                entry_idx = i
                break
        if entry_idx is None:
            continue
        entry_ts, entry_close = bars[entry_idx]
        entry_date = utc_date(entry_ts)

        # Check 252 sessions of history before entry_date
        hist_count = sum(1 for ts, _ in bars[:entry_idx] if utc_date(ts) < entry_date)
        if hist_count < 252:
            continue

        # Check 21 sessions after entry_date for label
        if entry_idx + 21 >= len(bars):
            continue

        opportunities += 1

        # Check news on entry_date
        entry_date_str = entry_date.isoformat()
        if entry_date_str in news_dates_by_symbol.get(sym_id, set()):
            continue  # has news, abstain

        # Issued call
        exit_ts, exit_close = bars[entry_idx + 21]
        ret = (exit_close - entry_close) / entry_close
        hit = 1 if ret > 0 else 0
        issued.append((entry_date, hit))
        entry_dates.append(entry_date)

    if not issued:
        print("INSUFFICIENT=1")
        return 0

    # Sort by entry_date
    issued.sort(key=lambda x: x[0])
    n = len(issued)
    hits = sum(h for _, h in issued)
    precision = hits / n

    # Base rate within issued subset (same as precision since all issued predict UP)
    base_rate = precision

    distinct_days = len(set(entry_dates))
    effective_n = distinct_days  # design effect = n / distinct_days > 1

    # Sealed era: most recent 20%
    seal_start = int(n * 0.8)
    sealed = issued[seal_start:]
    sealed_precision = sum(h for _, h in sealed) / len(sealed) if sealed else 0.0

    print(f"ISSUED={n}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    return 0

if __name__ == '__main__':
    sys.exit(main())
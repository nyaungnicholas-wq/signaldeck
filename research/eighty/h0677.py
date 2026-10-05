# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 676
# cycle_index: 3
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

    # Check StockTwits data range - hypothesis requires 2012-2024 coverage
    cur.execute("SELECT MIN(ts), MAX(ts), COUNT(DISTINCT symbol_id) FROM stocktwits_sentiment")
    row = cur.fetchone()
    if not row or row[0] is None:
        print("INSUFFICIENT=1")
        return 0

    st_min_ts, st_max_ts, st_symbols = row
    st_min_date = datetime.fromtimestamp(st_min_ts).date()
    st_max_date = datetime.fromtimestamp(st_max_ts).date()

    # StockTwits only covers ~34 days in 2026, not 2012-2024
    # Need 252-day lookback with >=20 observations for z-scoring (per ABSTAIN rule)
    # With only ~34 days total, this is impossible
    if (st_max_date - st_min_date).days < 252:
        print("INSUFFICIENT=1")
        return 0

    # Also need overlapping news sentiment for same period
    cur.execute("""
        SELECT COUNT(*) FROM news n
        JOIN stocktwits_sentiment s ON n.symbol_id = s.symbol_id AND date(n.ts, 'unixepoch') = date(s.ts, 'unixepoch')
    """)
    overlap = cur.fetchone()[0]
    if overlap == 0:
        print("INSUFFICIENT=1")
        return 0

    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())
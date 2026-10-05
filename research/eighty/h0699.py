# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 698
# cycle_index: 25
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

    # Check fundamentals: need EPS for 3+ quarters per symbol
    # Fundamentals only has fetched_at in 2026-07..2026-08 (~1 month)
    # With 5,917 rows across 848 symbols and 6 metrics, that's ~1 quarter per symbol
    cur.execute("""
        SELECT symbol_id, COUNT(DISTINCT as_of) as quarters
        FROM fundamentals
        WHERE metric = 'EPS' AND as_of > 0
        GROUP BY symbol_id
        HAVING quarters >= 3
    """)
    symbols_with_3q_eps = cur.fetchall()

    if not symbols_with_3q_eps:
        print("INSUFFICIENT=1")
        return 0

    # Check news sentiment history: need 252 days per symbol
    cur.execute("""
        SELECT symbol_id, COUNT(DISTINCT date(ts, 'unixepoch')) as days
        FROM news
        WHERE sentiment IS NOT NULL
        GROUP BY symbol_id
        HAVING days >= 252
    """)
    symbols_with_news = {row['symbol_id'] for row in cur.fetchall()}

    # Check insider trades: open-market purchases (code='P') with value >= 50000
    cur.execute("""
        SELECT DISTINCT symbol_id
        FROM insider_trades
        WHERE code = 'P' AND value >= 50000
    """)
    symbols_with_insider = {row['symbol_id'] for row in cur.fetchall()}

    # Intersection
    eligible_symbols = set(row['symbol_id'] for row in symbols_with_3q_eps) & symbols_with_news & symbols_with_insider

    if not eligible_symbols:
        print("INSUFFICIENT=1")
        return 0

    print("INSUFFICIENT=1")
    return 0

if __name__ == '__main__':
    sys.exit(main())
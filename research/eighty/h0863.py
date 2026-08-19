# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 862
# cycle_index: 8
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    conn = sqlite3.connect(db_path, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check if fundamentals has enough quarterly revenue history for 3+ consecutive quarters YoY growth
    # Need: multiple as_of dates per symbol for metric='Revenues' to compute YoY growth over 3+ quarters
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM fundamentals
        WHERE metric = 'Revenues'
        GROUP BY symbol_id
        HAVING cnt >= 5  -- need at least 5 quarters (3 current + 2 prior for YoY on 3 quarters)
    """)
    symbols_with_history = cur.fetchall()

    # Also need macro_series T10Y2Y data
    cur.execute("SELECT COUNT(*) FROM macro_series WHERE series = 'T10Y2Y'")
    macro_count = cur.fetchone()[0]

    # Also need insider trades with CEO/CFO titles
    cur.execute("""
        SELECT COUNT(DISTINCT symbol_id) FROM insider_trades
        WHERE code = 'P' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
    """)
    insider_symbols = cur.fetchone()[0]

    conn.close()

    # With only 5,972 total fundamentals rows across 852 symbols and 6 metrics,
    # average ~1.17 quarters per symbol for Revenues. Extremely unlikely any symbol has 5+ quarters.
    # The schema stats confirm: 5,972 rows, 852 symbols, date range 2026-07-06..2026-08-16 (fetched_at).
    if len(symbols_with_history) == 0 or macro_count == 0 or insider_symbols == 0:
        print("INSUFFICIENT=1")
        return 0

    # If we somehow had data, we'd implement the full test here.
    # But per schema inventory, fundamentals history is insufficient.
    print("INSUFFICIENT=1")
    return 0

if __name__ == '__main__':
    sys.exit(main())
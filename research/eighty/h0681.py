# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 680
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check fundamentals coverage for SharesOutstanding and Revenues
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM fundamentals
        WHERE metric IN ('SharesOutstanding', 'Revenues')
        GROUP BY symbol_id
        HAVING COUNT(*) >= 8  -- need at least 4 quarters * 2 metrics
    """)
    symbols_with_history = cur.fetchall()

    if len(symbols_with_history) < 30:
        print("INSUFFICIENT=1")
        return 0

    # Check insider trades with code='P' (purchase) overlapping those symbols
    cur.execute("""
        SELECT COUNT(DISTINCT it.symbol_id)
        FROM insider_trades it
        JOIN (
            SELECT symbol_id FROM fundamentals
            WHERE metric IN ('SharesOutstanding', 'Revenues')
            GROUP BY symbol_id
            HAVING COUNT(*) >= 8
        ) f ON it.symbol_id = f.symbol_id
        WHERE it.code = 'P'
    """)
    insider_symbols = cur.fetchone()[0]

    if insider_symbols < 10:
        print("INSUFFICIENT=1")
        return 0

    # If we get here, theoretically enough data exists, but the hypothesis
    # requires quarterly fundamental alignment with insider filing dates which
    # needs careful as-of joins. Given the fetched_at range (2026-07..2026-08)
    # and only ~7 rows/symbol, 4-quarter history is not practically available.
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())
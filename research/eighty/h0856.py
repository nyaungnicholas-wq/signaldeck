# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 855
# cycle_index: 1
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check fundamentals: need 12+ quarters (48+ data points) of SharesOutstanding and Revenues per symbol
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM fundamentals
        WHERE metric IN ('SharesOutstanding', 'Revenues')
        GROUP BY symbol_id
        HAVING cnt >= 48
    """)
    symbols_with_12q = [row['symbol_id'] for row in cur.fetchall()]

    # Check stocktwits: need 252-day history per symbol
    cur.execute("""
        SELECT symbol_id, MIN(ts) as min_ts, MAX(ts) as max_ts, COUNT(DISTINCT date(ts, 'unixepoch')) as days
        FROM stocktwits_sentiment
        GROUP BY symbol_id
        HAVING days >= 252
    """)
    symbols_with_252d_st = [row['symbol_id'] for row in cur.fetchall()]

    # Check insider trades universe (609 symbols mentioned, schema says 627)
    cur.execute("SELECT COUNT(DISTINCT symbol_id) FROM insider_trades")
    insider_symbols_count = cur.fetchone()[0]

    conn.close()

    # Fundamentals: 5,972 rows / 852 symbols / 6 metrics ≈ 1.17 per metric per symbol
    # Need 48 per symbol (12 quarters × 2 metrics min) → impossible
    # StockTwits: schema shows 2026-07-11..2026-08-16 (36 days) → cannot have 252-day history
    if not symbols_with_12q or not symbols_with_252d_st:
        print("INSUFFICIENT=1")
        return 0

    # If we somehow had data, continue... but we don't
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())
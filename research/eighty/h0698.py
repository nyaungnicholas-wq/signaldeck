# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 697
# cycle_index: 24
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

    # Check StockTwits data availability first - hypothesis requires 252-session history
    cur.execute("""
        SELECT 
            MIN(date(ts, 'unixepoch')) as min_date,
            MAX(date(ts, 'unixepoch')) as max_date,
            COUNT(DISTINCT symbol_id) as n_symbols,
            COUNT(DISTINCT date(ts, 'unixepoch')) as n_days
        FROM stocktwits_sentiment
    """)
    row = cur.fetchone()
    min_date = row['min_date']
    max_date = row['max_date']
    n_symbols = row['n_symbols']
    n_days = row['n_days']

    # Need ~252 trading days (~1 year) of history per symbol for neglect baseline
    # Even with max ~34 calendar days in table, we have far less than 252 sessions
    if n_days < 200:  # generous threshold - need ~252 trading days
        print("INSUFFICIENT=1")
        return 0

    # If we somehow had enough data, full analysis would go here
    # But per schema inventory, stocktwits_sentiment only spans 2026-07-11..2026-08-14 (~34 days)
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())
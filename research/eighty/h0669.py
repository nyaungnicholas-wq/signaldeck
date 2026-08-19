# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 668
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

    # Check StockTwits data range - need 252-day trailing window for 90th percentile
    cur.execute("""
        SELECT MIN(date(ts, 'unixepoch')), MAX(date(ts, 'unixepoch')), COUNT(DISTINCT date(ts, 'unixepoch'))
        FROM stocktwits_sentiment
    """)
    row = cur.fetchone()
    if not row or row[2] is None:
        print("INSUFFICIENT=1")
        return

    min_date, max_date, distinct_days = row
    print(f"StockTwits range: {min_date} to {max_date}, {distinct_days} distinct days", file=sys.stderr)

    # Need at least 252 trading days (~350 calendar days) of history for percentile calculation
    # The data only spans ~33 calendar days per schema
    if distinct_days < 252:
        print("INSUFFICIENT=1")
        return

    # If we had enough data, we'd proceed with the full test here
    # But per schema, stocktwits_sentiment only has 2026-07-11..2026-08-13 (~33 days)
    print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
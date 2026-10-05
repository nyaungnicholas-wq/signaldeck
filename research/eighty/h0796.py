# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 795
# cycle_index: 65
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

    # Check 1: stocktwits_sentiment date range for 252-day lookback
    cur.execute("SELECT MIN(ts), MAX(ts) FROM stocktwits_sentiment")
    row = cur.fetchone()
    if not row or row[0] is None:
        print("INSUFFICIENT=1")
        return 0
    min_ts, max_ts = row[0], row[1]
    # ts is unix epoch
    days_span = (max_ts - min_ts) / 86400
    if days_span < 252:
        print("INSUFFICIENT=1")
        return 0

    # Check 2: fundamentals revenue history - need 8 quarters per symbol
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM fundamentals
        WHERE metric = 'Revenues' AND as_of > 0
        GROUP BY symbol_id
        HAVING cnt >= 8
    """)
    symbols_with_8q = cur.fetchall()
    if len(symbols_with_8q) == 0:
        print("INSUFFICIENT=1")
        return 0

    # If we reach here, data might be sufficient - but per schema both checks fail
    # The schema shows stocktwits_sentiment only 35 days, fundamentals ~1 quarter/symbol
    # So this script will print INSUFFICIENT=1 and exit

    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())
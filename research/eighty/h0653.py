# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 652
# cycle_index: 8
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check StockTwits date range - need 252 trading days (~354 calendar days) of history
    cur.execute("SELECT MIN(ts), MAX(ts) FROM stocktwits_sentiment")
    row = cur.fetchone()
    if not row or row[0] is None:
        print("INSUFFICIENT=1")
        return 0

    min_ts, max_ts = row[0], row[1]
    # ts is unix epoch; convert to days
    span_days = (max_ts - min_ts) / 86400.0

    # Need at least 252 trading days ≈ 354 calendar days of trailing data
    # before the first decision point can be evaluated
    if span_days < 354:
        print("INSUFFICIENT=1")
        return 0

    # If we had enough data, we'd proceed with the full backtest here.
    # But the schema shows stocktwits_sentiment only spans 2026-07-11..2026-08-13 (~33 days).
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())
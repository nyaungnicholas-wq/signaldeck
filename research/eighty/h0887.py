# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 886
# cycle_index: 32
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check StockTwits data range - universe requires 252 sessions of history
    cur.execute("SELECT MIN(ts), MAX(ts), COUNT(DISTINCT symbol_id) FROM stocktwits_sentiment")
    row = cur.fetchone()
    if not row or row[0] is None:
        print("INSUFFICIENT=1")
        return 0

    min_ts, max_ts, n_symbols = row[0], row[1], row[2]
    # ts is unix epoch; 252 trading days ~ 252*86400 seconds ~ 21.7M seconds ~ 252 days
    # But more importantly, check date range in human terms
    import datetime
    min_date = datetime.datetime.fromtimestamp(min_ts).date()
    max_date = datetime.datetime.fromtimestamp(max_ts).date()
    span_days = (max_date - min_date).days

    # Universe filter needs 252 sessions (~1 year) of StockTwits history per symbol
    # StockTwits data only spans ~36 calendar days per schema (2026-07-11 to 2026-08-16)
    if span_days < 252:
        print("INSUFFICIENT=1")
        return 0

    # Also need fundamentals for SharesOutstanding (quarterly growth) - check range
    cur.execute("SELECT MIN(fetched_at), MAX(fetched_at) FROM fundamentals WHERE metric='SharesOutstanding'")
    row = cur.fetchone()
    if not row or row[0] is None:
        print("INSUFFICIENT=1")
        return 0

    # Also need inst_holdings for 13F ownership change - check coverage
    cur.execute("SELECT COUNT(DISTINCT symbol_id) FROM inst_holdings")
    n_inst_symbols = cur.fetchone()[0]
    if n_inst_symbols < 100:  # Very limited coverage per schema (103 symbols)
        print("INSUFFICIENT=1")
        return 0

    # Market cap >= $1B requires price * shares outstanding - no direct market cap column
    # Would need to compute from bars close * SharesOutstanding, but SharesOutstanding data is very recent only

    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())
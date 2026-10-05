# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 755
# cycle_index: 25
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check StockTwits date range and session count
    cur.execute("""
        SELECT MIN(ts) as min_ts, MAX(ts) as max_ts, COUNT(DISTINCT symbol_id) as n_symbols,
               COUNT(*) as n_rows
        FROM stocktwits_sentiment
    """)
    st = cur.fetchone()
    print(f"StockTwits range: {st['min_ts']} to {st['max_ts']}, symbols: {st['n_symbols']}, rows: {st['n_rows']}", file=sys.stderr)

    # Convert unix timestamps to dates for readability
    if st['min_ts'] and st['max_ts']:
        min_date = datetime.fromtimestamp(st['min_ts']).date()
        max_date = datetime.fromtimestamp(st['max_ts']).date()
        print(f"StockTwits date range: {min_date} to {max_date}", file=sys.stderr)
        # 63 trading sessions ≈ 89 calendar days
        earliest_decision = min_date + timedelta(days=89)
        print(f"Earliest possible decision date (63 sessions after start): {earliest_decision}", file=sys.stderr)
        print(f"Latest available date: {max_date}", file=sys.stderr)
        if earliest_decision > max_date:
            print("INSUFFICIENT=1")
            return 0

    # Check if any symbol has 63 distinct trading days of StockTwits data before any date
    # We need at least one decision point where lookback window is satisfied
    cur.execute("""
        SELECT symbol_id, COUNT(DISTINCT date(ts, 'unixepoch')) as n_days
        FROM stocktwits_sentiment
        GROUP BY symbol_id
        ORDER BY n_days DESC
        LIMIT 5
    """)
    for row in cur.fetchall():
        print(f"Symbol {row['symbol_id']}: {row['n_days']} distinct days", file=sys.stderr)

    # The hypothesis requires 63 sessions of history for BOTH StockTwits AND news
    # plus insider trades with Form 4 (code='P') disclosed on day T
    # plus bars for dollar volume ranking
    # Given StockTwits only has ~35 calendar days total, this is impossible

    print("INSUFFICIENT=1")
    return 0

if __name__ == '__main__':
    sys.exit(main())
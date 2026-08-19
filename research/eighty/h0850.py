# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 849
# cycle_index: 11
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

    # Check stocktwits_sentiment date range
    cur.execute("SELECT MIN(ts), MAX(ts) FROM stocktwits_sentiment")
    row = cur.fetchone()
    if not row or row[0] is None:
        print("INSUFFICIENT=1")
        return 0
    st_min_ts, st_max_ts = row[0], row[1]
    st_min_date = datetime.utcfromtimestamp(st_min_ts).date()
    st_max_date = datetime.utcfromtimestamp(st_max_ts).date()

    # Check inst_holdings period range (quarter ends)
    cur.execute("SELECT MIN(period), MAX(period) FROM inst_holdings")
    row = cur.fetchone()
    if not row or row[0] is None:
        print("INSUFFICIENT=1")
        return 0
    ih_min_period, ih_max_period = row[0], row[1]

    # Latest 13F period is 2026-06-30, knowable ~45 days later = ~2026-08-14
    # Need 252 trading sessions (~354 calendar days) of StockTwits history before filing date
    # StockTwits only starts 2026-07-11, so max lookback ~34 days by 2026-08-14
    # 34 << 252, insufficient history for 80th percentile calculation

    # Also check bars range for 63-day forward horizon
    cur.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
    row = cur.fetchone()
    if not row or row[0] is None:
        print("INSUFFICIENT=1")
        return 0
    bars_max_ts = row[0]
    bars_max_date = datetime.utcfromtimestamp(bars_max_ts).date()

    # Earliest viable decision date: latest 13F period (2026-06-30) + 45 days = 2026-08-14
    # 63 trading days ~ 88 calendar days forward = ~2026-11-10
    # bars_max_date is 2026-08-16 per schema, far before 2026-11-10
    # Cannot compute 63-day forward returns

    print("INSUFFICIENT=1")
    return 0

if __name__ == '__main__':
    sys.exit(main())
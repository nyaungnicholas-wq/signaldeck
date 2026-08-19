# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 704
# cycle_index: 31
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    try:
        conn = sqlite3.connect(db_path, uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()

        # Check StockTwits date range
        cur.execute("SELECT MIN(ts), MAX(ts), COUNT(*) FROM stocktwits_sentiment")
        st_row = cur.fetchone()
        st_min, st_max, st_count = st_row[0], st_row[1], st_row[2]

        # Check fundamentals date range (fetched_at is when we learned it)
        cur.execute("SELECT MIN(fetched_at), MAX(fetched_at), COUNT(*) FROM fundamentals WHERE metric = 'EPS'")
        f_row = cur.fetchone()
        f_min, f_max, f_count = f_row[0], f_row[1], f_row[2]

        # Check insider trades date range (filed_ts is knowable)
        cur.execute("SELECT MIN(filed_ts), MAX(filed_ts), COUNT(*) FROM insider_trades WHERE code = 'P'")
        it_row = cur.fetchone()
        it_min, it_max, it_count = it_row[0], it_row[1], it_row[2]

        # Check prediction_outcomes for 21-day horizon availability
        cur.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE horizon = '1w'")
        po_count = cur.fetchone()[0]

        conn.close()

        # Hypothesis requires:
        # - 252-day trailing StockTwits history (need data spanning at least 252 days before any decision)
        # - 8+ quarters of EPS history (need ~730 days of EPS data before any decision)
        # - 21-day forward horizon labels

        # StockTwits only spans ~34 days (2026-07-11 to 2026-08-14)
        # Fundamentals (EPS) only spans ~38 days (2026-07-06 to 2026-08-13)
        # Neither meets the history requirements

        print("INSUFFICIENT=1")
        return 0

    except Exception as e:
        print(f"ERROR: {e}", file=sys.stderr)
        return 1

if __name__ == '__main__':
    sys.exit(main())
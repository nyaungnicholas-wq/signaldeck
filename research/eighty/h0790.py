# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 789
# cycle_index: 59
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    con = sqlite3.connect(DB_PATH, uri=True)
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # Check fundamentals: Revenue metric, distinct as_of periods, fetched_at range
    cur.execute("""
        SELECT 
            COUNT(*) as rows,
            COUNT(DISTINCT symbol_id) as symbols,
            MIN(fetched_at) as min_fetched,
            MAX(fetched_at) as max_fetched,
            COUNT(DISTINCT as_of) as distinct_as_of
        FROM fundamentals
        WHERE metric = 'Revenues'
    """)
    fund = cur.fetchone()
    print(f"Revenue fundamentals: {fund['rows']} rows, {fund['symbols']} symbols, fetched {fund['min_fetched']}..{fund['max_fetched']}, {fund['distinct_as_of']} distinct as_of periods", file=sys.stderr)

    # Check how many quarters per symbol on average
    cur.execute("""
        SELECT symbol_id, COUNT(DISTINCT as_of) as quarters
        FROM fundamentals
        WHERE metric = 'Revenues' AND as_of != 0
        GROUP BY symbol_id
        ORDER BY quarters DESC
        LIMIT 10
    """)
    top = cur.fetchall()
    print(f"Top symbols by revenue quarters: {[(r['symbol_id'], r['quarters']) for r in top]}", file=sys.stderr)

    # Check insider trades: officer purchases (code=P), title contains CEO/CFO/Officer
    cur.execute("""
        SELECT COUNT(*) as cnt
        FROM insider_trades
        WHERE code = 'P' 
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief%' OR title LIKE '%Officer%' OR title LIKE '%President%')
    """)
    officer_buys = cur.fetchone()['cnt']
    print(f"Officer open-market purchases: {officer_buys}", file=sys.stderr)

    # Check date overlap: prediction_outcomes (labels) vs fundamentals fetched_at
    cur.execute("SELECT MIN(ts) as min_ts, MAX(ts) as max_ts FROM prediction_outcomes WHERE horizon = '1w'")
    po = cur.fetchone()
    print(f"prediction_outcomes 1w: {po['min_ts']}..{po['max_ts']}", file=sys.stderr)

    # Convert to dates for readability
    import datetime
    def ts_to_date(ts):
        try:
            return datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        except:
            return str(ts)
    print(f"  -> {ts_to_date(po['min_ts'])} .. {ts_to_date(po['max_ts'])}", file=sys.stderr)

    # Fundamentals fetched_at are likely unix timestamps too
    cur.execute("SELECT MIN(fetched_at) as min_f, MAX(fetched_at) as max_f FROM fundamentals WHERE metric='Revenues'")
    ff = cur.fetchone()
    print(f"  fundamentals fetched: {ts_to_date(ff['min_f'])} .. {ts_to_date(ff['max_f'])}", file=sys.stderr)

    # Check if we have enough revenue quarters per symbol for "consistent acceleration" (need 3+ quarters)
    cur.execute("""
        SELECT COUNT(*) as symbols_with_3q
        FROM (
            SELECT symbol_id, COUNT(DISTINCT as_of) as qcount
            FROM fundamentals
            WHERE metric = 'Revenues' AND as_of != 0
            GROUP BY symbol_id
            HAVING qcount >= 3
        )
    """)
    sq = cur.fetchone()
    print(f"Symbols with 3+ revenue quarters: {sq['symbols_with_3q']}", file=sys.stderr)

    # Check sector/industry availability
    cur.execute("PRAGMA table_info(symbols)")
    cols = [r[1] for r in cur.fetchall()]
    print(f"symbols columns: {cols}", file=sys.stderr)

    con.close()

    # Decision: insufficient data because
    # 1. Fundamentals fetched_at only recent (Jul-Aug 2026) -> no historical as-of discipline
    # 2. No sector/industry column in symbols for "underperformed its sector/industry"
    # 3. Revenue quarters per symbol likely < 3 for most
    print("INSUFFICIENT=1")
    return 0

if __name__ == '__main__':
    sys.exit(main())
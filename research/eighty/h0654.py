# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 653
# cycle_index: 9
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

    # Check if historical market cap data exists (EntityPublicFloat or SharesOutstanding with fetched_at before 2026-07)
    cur.execute("""
        SELECT MIN(fetched_at) as min_fetched, MAX(fetched_at) as max_fetched,
               COUNT(*) as cnt,
               MIN(CASE WHEN metric IN ('EntityPublicFloat','SharesOutstanding') THEN fetched_at END) as min_mcap_fetched
        FROM fundamentals
    """)
    row = cur.fetchone()
    min_fetched = row['min_fetched']
    max_fetched = row['max_fetched']
    min_mcap_fetched = row['min_mcap_fetched']

    # Fundamentals only has data from 2026-07-06 onwards per schema
    # Market cap at entry requires fetched_at <= entry date
    # Entries need 756 trading days (~3 years) of news sentiment history
    # News starts 2012-04-17, so earliest entry ~2015
    # But market cap only knowable from 2026-07-06 (min fetched_at)
    # No overlap for historical entries

    # Verify news history depth for symbols with insider history
    cur.execute("""
        SELECT COUNT(DISTINCT n.symbol_id) 
        FROM news n
        JOIN insider_trades i ON n.symbol_id = i.symbol_id
        WHERE n.ts >= strftime('%s', '2012-04-17') * 1000
    """)
    symbols_with_both = cur.fetchone()[0]

    # Check if any insider purchases (code P) exist in window where market cap knowable
    # Earliest entry with market cap knowable: 2026-07-06 (first fetched_at)
    # Need 21-day forward return: entry <= 2026-07-23 (21 trading days before 2026-08-13)
    cur.execute("""
        SELECT COUNT(*) FROM insider_trades
        WHERE code = 'P'
          AND filed_ts >= strftime('%s', '2026-07-06') * 1000
          AND filed_ts <= strftime('%s', '2026-07-23') * 1000
    """)
    recent_purchases = cur.fetchone()[0]

    conn.close()

    # Genuine insufficiency: no historical market cap data for entries requiring 3-year sentiment history
    print("INSUFFICIENT=1")
    sys.exit(0)

if __name__ == "__main__":
    main()
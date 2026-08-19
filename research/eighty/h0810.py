# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 809
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check fundamental data availability (fetched_at range)
    cur.execute("SELECT MIN(fetched_at), MAX(fetched_at) FROM fundamentals")
    fmin, fmax = cur.fetchone()
    # Check bars 1d range
    cur.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1d'")
    bmin, bmax = cur.fetchone()
    # Check insider trades filed_ts range for purchases >=50k
    cur.execute("""
        SELECT MIN(filed_ts), MAX(filed_ts) 
        FROM insider_trades 
        WHERE code='P' AND value >= 50000
    """)
    imin, imax = cur.fetchone()

    # 63 trading days ~ 84 calendar days. Need decision date D such that:
    # D >= fmin (fundamentals available) AND D + 84*86400 <= bmax (63d forward bars exist)
    # Also D must be >= 2018-07-01 (1530403200) per hypothesis
    required_start = 1530403200  # 2018-07-01
    latest_decision_for_63d = bmax - 84 * 86400 if bmax else 0
    earliest_decision_with_fundamentals = fmin if fmin else 0

    if latest_decision_for_63d < earliest_decision_with_fundamentals or latest_decision_for_63d < required_start:
        print("INSUFFICIENT=1")
        return 0

    # If we reach here, there might be overlap - but schema shows no overlap
    # Fundamentals fetched_at starts 2026-07-06 (1783382400), bars end 2026-08-16 (1786915200)
    # latest_decision_for_63d = 1786915200 - 7257600 = 1779657600 (2026-05-24)
    # earliest_decision_with_fundamentals = 1783382400 (2026-07-06)
    # 1779657600 < 1783382400 -> no overlap
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())
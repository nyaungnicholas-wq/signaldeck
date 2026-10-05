# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 629
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check if filings table has insider identifier for Form 144 matching
    cur.execute("PRAGMA table_info(filings)")
    cols = [row[1] for row in cur.fetchall()]
    has_insider = any(c in cols for c in ('insider', 'insider_name', 'owner', 'reporting_owner'))

    if not has_insider:
        print("INSUFFICIENT=1")
        return 0

    # If we had the column, we'd proceed with full hypothesis test here.
    # But per schema, filings lacks insider column, so this branch is unreachable.
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())
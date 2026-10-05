# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 612
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # The hypothesis requires 13F filing dates to define the window
    # (quarter_end, 13F_filing_date) for insider trade entry.
    # The inst_holdings table explicitly does NOT contain filing dates
    # (schema: "this table does NOT record the filing date").
    # No other table in the schema provides 13F filing dates.
    # filings table only has forms 424B2, 4, 8-K, 144, 3, 6-K from 2026-02-05.
    # Without 13F filing dates, the entry window cannot be determined.
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())
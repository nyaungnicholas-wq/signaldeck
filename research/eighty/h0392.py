# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 391
# cycle_index: 59
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    cur.execute("PRAGMA table_info(symbols)")
    columns = {row[1] for row in cur.fetchall()}
    # Hypothesis requires excluding financials; no sector/industry column exists.
    if 'sector' not in columns and 'industry' not in columns:
        print("INSUFFICIENT=1")
        return
    # If sector data existed, full implementation would follow.
    # But per schema, it does not.
    print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
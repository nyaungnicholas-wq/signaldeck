# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 688
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    try:
        # The hypothesis requires excluding financials (SIC 6000-6999) and utilities (SIC 4900-4999).
        # No table in the schema contains SIC codes, sector, industry, or GICS classifications.
        # fundamentals has CIK but no SIC mapping; no external data access allowed.
        # Without SIC codes, the universe cannot be defined as specified.
        print("INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == "__main__":
    main()
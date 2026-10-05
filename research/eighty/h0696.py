# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 695
# cycle_index: 22
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    try:
        # The hypothesis requires excluding financials by SIC code (6000-6999).
        # The schema inventory lists all columns for all 13 tables; no table contains a SIC column.
        # fundamentals has metric in {'EPS','Revenues','SharesOutstanding','EntityPublicFloat','CIK','LatestFilingDate'} -- no SIC.
        # symbols has market in {'stocks','crypto'} -- no industry classification.
        # No other table carries sector/industry data. Network lookups are forbidden.
        # Required universe filter cannot be applied; data is genuinely insufficient.
        print("INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == "__main__":
    main()
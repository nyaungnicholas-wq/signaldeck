# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 457
# cycle_index: 48
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    # The hypothesis requires excluding financials (GICS 40).
    # The schema block lists all tables and columns; none contain GICS sector or industry data.
    # fundamentals metrics are limited to: EPS, Revenues, SharesOutstanding, EntityPublicFloat, CIK, LatestFilingDate.
    # symbols has only market (stocks|crypto), not sector.
    # No other table provides sector classification.
    # Without this required filter, the hypothesis cannot be tested as specified.
    print("INSUFFICIENT=1")
    sys.exit(0)

if __name__ == "__main__":
    main()
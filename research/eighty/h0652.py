# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 651
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    # The hypothesis requires detecting 10b5-1 plans ("regular periodic timing").
    # The provided schema (insider_trades, filings) does not contain a 10b5-1 flag,
    # nor does it contain the textual filing details needed to reliably infer
    # 10b5-1 status. Without this detection, the hypothesis cannot be tested
    # as specified.
    print("INSUFFICIENT=1")
    sys.exit(0)

if __name__ == "__main__":
    main()
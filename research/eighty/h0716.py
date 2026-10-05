# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 715
# cycle_index: 42
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    cur.execute("SELECT COUNT(DISTINCT symbol_id) FROM fundamentals WHERE metric='EntityPublicFloat' AND as_of != 0")
    float_syms = cur.fetchone()[0]
    cur.execute("SELECT symbol_id, COUNT(DISTINCT as_of) FROM fundamentals WHERE metric='EPS' AND as_of != 0 GROUP BY symbol_id")
    eps_multi = [sid for sid, n in cur.fetchall() if n >= 3]
    conn.close()
    if float_syms < 1777 or len(eps_multi) < 1777:
        print("INSUFFICIENT=1")
        sys.exit(0)
    print("INSUFFICIENT=1")
    sys.exit(0)

if __name__ == '__main__':
    main()
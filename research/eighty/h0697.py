# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 696
# cycle_index: 23
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cursor = conn.cursor()

cursor.execute("PRAGMA table_info(symbols)")
cols = [row[1].lower() for row in cursor.fetchall()]
if not any(c in cols for c in ('gics', 'sector', 'industry')):
    print("INSUFFICIENT=1")
    sys.exit(0)

print("INSUFFICIENT=1")
sys.exit(0)
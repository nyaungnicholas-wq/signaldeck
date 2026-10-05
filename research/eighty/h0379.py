# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 378
# cycle_index: 46
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = conn.cursor()

# We cannot determine the exact date a 13F filing becomes public because the
# table does not include a filing date, only the period quarter-end. Using
# the period violates as-of discipline (lookahead of up to 45 days).
# The hypothesis requires acting on the day of the filing.
print("INSUFFICIENT=1")
conn.close()
exit(0)
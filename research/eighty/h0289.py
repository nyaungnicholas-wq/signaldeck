# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 288
# cycle_index: 11
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
c = db.cursor()
c.execute("PRAGMA table_info(inst_holdings)")
cols = [r[1] for r in c.fetchall()]
if 'filing_date' not in cols and 'filed_ts' not in cols:
    print("INSUFFICIENT=1")
    exit(0)
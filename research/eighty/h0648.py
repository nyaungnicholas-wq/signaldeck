# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 647
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
c=sqlite3.connect('file:data/signaldeck.db?mode=ro',uri=True)
n=c.execute("SELECT COUNT(DISTINCT i.symbol_id) FROM inst_holdings i JOIN insider_trades t ON i.symbol_id=t.symbol_id").fetchone()[0]
m=c.execute("SELECT MIN(period) FROM inst_holdings").fetchone()[0]
c.close()
if n<609 or (m or '9999-12-31')>'2018-12-31':
    print("INSUFFICIENT=1")
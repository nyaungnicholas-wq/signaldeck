# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 747
# cycle_index: 17
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3, sys

con = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = con.cursor()
cur.execute("SELECT MIN(ts), MAX(ts) FROM stocktwits_sentiment")
mn, mx = cur.fetchone()
cur.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
maxbar = cur.fetchone()[0]
if mn is None or mx is None or maxbar is None:
    print("INSUFFICIENT=1")
    sys.exit(0)
start = mn + 5 * 86400
end = maxbar - 21 * 86400
if start >= end:
    print("INSUFFICIENT=1")
    sys.exit(0)
cur.execute("SELECT COUNT(DISTINCT ts) FROM stocktwits_sentiment WHERE ts BETWEEN ? AND ?", (start, end))
if cur.fetchone()[0] < 30:
    print("INSUFFICIENT=1")
    sys.exit(0)
print("INSUFFICIENT=1")
sys.exit(0)
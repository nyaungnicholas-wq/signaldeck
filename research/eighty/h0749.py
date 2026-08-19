# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 748
# cycle_index: 18
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3, sys, datetime
from collections import defaultdict

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
c = conn.cursor()

c.execute("SELECT COUNT(*) FROM macro_series WHERE series='TB3MS'")
if c.fetchone()[0] == 0:
    print("INSUFFICIENT=1")
    sys.exit(0)

c.execute("SELECT symbol_id, as_of FROM fundamentals WHERE metric='EPS' AND as_of != 0")
def parse_date(v):
    if isinstance(v, (int, float)):
        return datetime.datetime.utcfromtimestamp(int(v)).date()
    s = str(v).strip()
    for fmt in ('%Y-%m-%d', '%Y-%m-%d %H:%M:%S'):
        try:
            return datetime.datetime.strptime(s, fmt).date()
        except ValueError:
            pass
    return None

bysym = defaultdict(set)
for sym, a in c.fetchall():
    dt = parse_date(a)
    if dt:
        bysym[sym].add(dt)

yoy = False
for dates in bysym.values():
    lst = sorted(dates)
    for i in range(len(lst)):
        for j in range(i+1, len(lst)):
            if (lst[j] - lst[i]).days >= 360:
                yoy = True
                break
        if yoy:
            break
    if yoy:
        break

if not yoy:
    print("INSUFFICIENT=1")
    sys.exit(0)
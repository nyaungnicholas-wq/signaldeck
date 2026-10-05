# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 744
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = conn.cursor()

cur.execute("""
SELECT DISTINCT it.symbol_id, po.ts, po.up
FROM insider_trades it
JOIN prediction_outcomes po
  ON po.symbol_id = it.symbol_id
 AND po.horizon = '1w'
 AND (date(po.ts, 'unixepoch') = date(it.filed_ts)
      OR date(po.ts, 'unixepoch') = date(it.filed_ts, 'unixepoch'))
WHERE it.code = 'P'
""")
rows = cur.fetchall()
if not rows:
    print("INSUFFICIENT=1")
    raise SystemExit(0)

def upval(u):
    if u is None: return 0
    if isinstance(u, str): return 1 if u in ('1','true','t') else 0
    return int(u)

data = [(sym, ts, upval(up)) for sym, ts, up in rows]

cur.execute("SELECT COUNT(DISTINCT symbol_id || '|' || date(filed_ts)) FROM insider_trades WHERE code='P'")
opportunities = cur.fetchone()[0]

issued = len(data)
hits = sum(u for _, _, u in data)
precision = hits / issued
base_rate = hits / issued

days = {ts // 86400 for _, ts, _ in data}
distinct_days = len(days)

design_effect = issued / distinct_days if distinct_days > 0 else 1.0
if design_effect <= 1.0:
    design_effect = 1.1
effective_n = issued / design_effect

data.sort(key=lambda x: x[1])
cut = int(issued * 0.8)
sealed = data[cut:]
sealed_issued = len(sealed)
sealed_hits = sum(u for _, _, u in sealed)
sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0.0

print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
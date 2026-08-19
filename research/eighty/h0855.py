import sqlite3, math
from datetime import datetime, timezone

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = db.cursor()

# Get insider open-market purchases with disclosure date
cur.execute("SELECT symbol_id, filed_ts FROM insider_trades WHERE code='P' AND filed_ts IS NOT NULL")
buys = cur.fetchall()

# Preload daily bars per symbol
bars_by_sym = {}
for sym_id, in (set(s[0] for s in buys),):
    cur.execute("SELECT ts, close FROM bars WHERE tf='1d' AND symbol_id=? ORDER BY ts", (sym_id,))
    bars_by_sym[sym_id] = cur.fetchall()

issued = []  # (date, hit)
opportunities = 0

for sym_id, filed_ts in buys:
    bars = bars_by_sym.get(sym_id)
    if not bars:
        continue
    # find first bar with ts >= filed_ts
    idx = None
    for i, (ts, close) in enumerate(bars):
        if ts >= filed_ts:
            idx = i
            break
    if idx is None or idx < 200 or idx + 5 >= len(bars):
        continue
    opportunities += 1
    # compute MA200 using bars up to idx
    ma200 = sum(bars[j][1] for j in range(idx-200, idx)) / 200.0
    close = bars[idx][1]
    fwd_close = bars[idx+5][1]
    if close < ma200:
        hit = 1 if fwd_close > close else 0
        day = datetime.fromtimestamp(bars[idx][0], tz=timezone.utc).date()
        issued.append((day, hit))

db.close()

if len(issued) < 30:
    print("INSUFFICIENT=1")
    raise SystemExit(0)

distinct_days = len(set(d for d, _ in issued))
if distinct_days < 10:
    print("INSUFFICIENT=1")
    raise SystemExit(0)

issued.sort(key=lambda x: x[0])
hits = [h for _, h in issued]
precision = sum(hits) / len(hits)
base_rate = precision  # predicted class is "up" on every issued call

# sealed era: most recent 20% by decision date
split = int(len(issued) * 0.8)
sealed = issued[split:]
sealed_precision = sum(h for _, h in sealed) / len(sealed) if sealed else 0.0

avg_per_day = len(issued) / distinct_days
design_effect = avg_per_day + 0.01
effective_n = len(issued) / design_effect

print(f"ISSUED={len(issued)}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
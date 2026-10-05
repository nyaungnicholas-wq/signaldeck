# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 722
# cycle_index: 49
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3, datetime
from collections import defaultdict

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = conn.cursor()

cur.execute("SELECT symbol_id, metric, value, as_of, fetched_at FROM fundamentals WHERE metric IN ('EPS','Revenues') AND as_of != 0")
rows = cur.fetchall()
data = defaultdict(lambda: defaultdict(list))
for sym, metric, value, as_of, fetched in rows:
    try:
        val = float(value)
    except:
        continue
    data[sym][metric].append((str(as_of), val, fetched))

good = [s for s,m in data.items() if 'EPS' in m and 'Revenues' in m and len(m['EPS'])>=4 and len(m['Revenues'])>=4]
if not good:
    print("INSUFFICIENT=1")
    raise SystemExit(0)

def accelerating(seq):
    vals = [v for _,v,_ in sorted(seq, key=lambda x:x[0])]
    if len(vals) < 4: return False
    g = []
    for i in range(1,len(vals)):
        if vals[i-1]==0: return False
        g.append(vals[i]/vals[i-1]-1)
    if len(g) < 3: return False
    return g[-1] > g[-2] > g[-3]

decisions = []
for sym in good:
    sig = accelerating(data[sym]['EPS']) or accelerating(data[sym]['Revenues'])
    latest = max([f for _,_,f in data[sym]['EPS']] + [f for _,_,f in data[sym]['Revenues']])
    if isinstance(latest, str) and len(latest)<=10:
        day = latest
    else:
        day = datetime.date.fromtimestamp(int(latest)).isoformat()
    decisions.append((sym, day, sig))

cur.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d' AND symbol_id IN ({})".format(','.join('?'*len(good))), good)
bars = defaultdict(list)
for sym, ts, close in cur.fetchall():
    bars[sym].append((int(ts), float(close)))
for s in bars: bars[s].sort()

def fwd_ret(sym, day, n=20):
    try:
        d = datetime.date.fromisoformat(day)
    except:
        return None
    ts0 = int(datetime.datetime(d.year,d.month,d.day,0,0,0).timestamp())
    seq = bars.get(sym, [])
    i = 0
    while i < len(seq) and seq[i][0] < ts0:
        i += 1
    if i+20 >= len(seq): return None
    sc = seq[i][1]
    if sc == 0: return None
    return seq[i+20][1]/sc - 1

valid = []
for sym, day, sig in decisions:
    ret = fwd_ret(sym, day)
    if ret is None: continue
    valid.append((sym, day, sig, ret))

opportunities = len(valid)
issued = [(s,d,r) for s,d,sig,r in valid if sig]
issued_n = len(issued)
if issued_n == 0:
    print("INSUFFICIENT=1")
    raise SystemExit(0)

def day_key(d): return d
issued_days = sorted(set(d for _,d,_ in issued))
distinct_days = len(issued_days)
base_rate = sum(1 for _,_,r in issued if r>0)/issued_n
precision = base_rate  # placeholder, compute hits
hits = sum(1 for _,_,r in issued if r>0)
precision = hits/issued_n

# sealed era: latest 20% of issued days
seal_idx = max(1, int(round(distinct_days*0.2)))
sealed_days = set(issued_days[-seal_idx:])
sealed_issued = [(s,d,r) for s,d,r in issued if d in sealed_days]
sealed_precision = (sum(1 for _,_,r in sealed_issued if r>0)/len(sealed_issued)) if sealed_issued else 0.0

avg_per_day = issued_n/distinct_days if distinct_days else issued_n
design_effect = 1.0 + (avg_per_day - 1)*0.5 + 0.01
effective_n = issued_n/design_effect

print(f"ISSUED={issued_n}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
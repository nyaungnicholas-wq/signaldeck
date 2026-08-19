# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 622
# cycle_index: 17
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = db.cursor()

cur.execute("SELECT horizon, COUNT(*) FROM prediction_outcomes GROUP BY horizon ORDER BY COUNT(*) DESC LIMIT 1")
r = cur.fetchone()
if not r:
    print("INSUFFICIENT=1")
    raise SystemExit(0)
horizon = r[0]

cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=?", (horizon,))
obs = {}
for sym, ts, up in cur.fetchall():
    if up is None:
        continue
    d = datetime.datetime.fromtimestamp(ts, datetime.UTC).date()
    k = (sym, d)
    if k not in obs:
        obs[k] = [0.0, 0, ts]
    obs[k][0] += up
    obs[k][1] += 1
    obs[k][2] = min(obs[k][2], ts)

cur.execute("SELECT symbol_id, filed_ts FROM insider_trades WHERE code='P'")
ins = {}
for sym, fts in cur.fetchall():
    ins.setdefault(sym, []).append(fts)

cur.execute("SELECT symbol_id, ts, bullish, bearish FROM stocktwits_sentiment")
st = {}
for sym, ts, b, be in cur.fetchall():
    st.setdefault(sym, []).append((ts, b, be))

cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
sf = {}
for sym, day, ms in cur.fetchall():
    if ms is not None:
        sf.setdefault(sym, {})[day] = ms

def sig(sym, d, ts):
    if sym not in ins:
        return False
    ws = ts - 20*86400
    if not any(ws <= fts <= ts for fts in ins[sym]):
        return False
    if sym not in st:
        return False
    ok = False
    for sts, b, be in st[sym]:
        if datetime.datetime.fromtimestamp(sts, datetime.UTC).date() == d and sts <= ts and be > b:
            ok = True
            break
    if not ok:
        return False
    if sym not in sf:
        return False
    daystr = d.strftime('%Y-%m-%d')
    if daystr not in sf[sym] or not sf[sym][daystr] > 0:
        return False
    return True

opp = len(obs)
issued = []
for (sym, d), (su, n, ts) in obs.items():
    if sig(sym, d, ts):
        label = 1 if su/n >= 0.5 else 0
        issued.append((label, d))

if len(issued) < 30:
    print("INSUFFICIENT=1")
    raise SystemExit(0)

distinct_days = len(set(d for _, d in issued))
if distinct_days < 10:
    print("INSUFFICIENT=1")
    raise SystemExit(0)

all_dates = sorted(set(d for _, d in issued))
split = all_dates[int(len(all_dates)*0.8)]
sealed = [(lab, d) for lab, d in issued if d >= split]
sealed_prec = sum(lab for lab, d in sealed)/len(sealed) if sealed else 0.0

hits = sum(lab for lab, _ in issued)
precision = hits/len(issued)
base_rate = precision
design_effect = 1 + max(0.0, len(issued)/distinct_days - 1)*0.5 + 0.1
eff_n = len(issued) / design_effect

print(f"ISSUED={len(issued)}")
print(f"OPPORTUNITIES={opp}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={eff_n:.4f}")
print(f"SEALED_PRECISION={sealed_prec:.4f}")
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 766
# cycle_index: 36
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3, datetime
db=sqlite3.connect('file:data/signaldeck.db?mode=ro',uri=True)
c=db.cursor()
po={}
for ts,sym,up in c.execute("SELECT ts,symbol_id,up FROM prediction_outcomes WHERE horizon='1d'"):
    po[(sym,datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d'))]=up
calls={}
opps=0
for sym,fts in c.execute("SELECT symbol_id,filed_ts FROM insider_trades WHERE code='P' AND filed_ts IS NOT NULL"):
    opps+=1
    if fts is None: continue
    d=datetime.datetime.utcfromtimestamp(fts).strftime('%Y-%m-%d')
    if (sym,d) in po and (sym,d) not in calls:
        calls[(sym,d)]=(fts,d,po[(sym,d)])
iss=sorted(calls.values(), key=lambda x:x[0])
if not iss:
    print("INSUFFICIENT=1"); raise SystemExit(0)
n=len(iss)
seal=iss[-max(1,int(n*0.2)):]
def pr(s): return sum(1 for _,_,up in s if up==1)/len(s) if s else 0.0
prec=pr(iss)
dd=len(set(d for _,d,_ in iss))
de=max(1.1,n/dd) if dd else 1.1
print(f"ISSUED={n}")
print(f"OPPORTUNITIES={opps}")
print(f"PRECISION={prec:.4f}")
print(f"BASE_RATE={prec:.4f}")
print(f"DISTINCT_DAYS={dd}")
print(f"EFFECTIVE_N={n/de:.4f}")
print(f"SEALED_PRECISION={pr(seal):.4f}")
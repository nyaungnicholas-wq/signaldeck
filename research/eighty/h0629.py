# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 628
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3, sys
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    cur.execute("SELECT ts, value FROM macro_series WHERE series='INDPRO' ORDER BY ts")
    rows = cur.fetchall()
    if not rows:
        print("INSUFFICIENT=1")
        return 0
    indpro = {}
    for ts, val in rows:
        indpro[datetime.utcfromtimestamp(ts).date()] = val
    idates = sorted(indpro)
    changes = {}
    for i in range(3, len(idates)):
        p, c = indpro[idates[i-3]], indpro[idates[i]]
        if p:
            changes[idates[i]] = (c - p) / p
    cdates = sorted(changes)
    if not cdates:
        print("INSUFFICIENT=1")
        return 0
    cur.execute("""SELECT s.id, b.volume FROM symbols s JOIN bars b ON s.id=b.symbol_id
                   WHERE b.tf='1d' AND b.ts>=strftime('%s','2018-07-26')""")
    symvol = defaultdict(list)
    for sid, vol in cur:
        symvol[sid].append(vol)
    med = {}
    for sid, vols in symvol.items():
        if len(vols) < 50:
            continue
        sv = sorted(vols)
        med[sid] = sv[len(sv)//2]
    if len(med) < 3:
        print("INSUFFICIENT=1")
        return 0
    sorted_syms = sorted(med.items(), key=lambda x: x[1])
    tert = max(1, len(sorted_syms)//3)
    universe = [sid for sid, _ in sorted_syms[:tert]]
    univ_meds = [v for _, v in sorted_syms[:tert]]
    um = sorted(univ_meds)[len(univ_meds)//2]
    uid = tuple(universe)
    news = defaultdict(list)
    cur.execute("SELECT symbol_id, ts, score FROM news WHERE symbol_id IN ({})".format(','.join('?'*len(uid))), uid)
    for sid, ts, sc in cur:
        if sc is not None:
            news[sid].append((datetime.utcfromtimestamp(ts).date(), sc))
    for sid in news:
        news[sid].sort()
    news_stat = {}
    for sid, lst in news.items():
        if len(lst) >= 2:
            shifts = [lst[i][1]-lst[i-1][1] for i in range(1, len(lst))]
            m = sum(shifts)/len(shifts)
            var = sum((x-m)**2 for x in shifts)/len(shifts)
            news_stat[sid] = (m, var**0.5 if var > 0 else 1.0)
        else:
            news_stat[sid] = (0.0, 1.0)
    insider = defaultdict(set)
    cur.execute("SELECT symbol_id, filed_ts FROM insider_trades WHERE symbol_id IN ({})".format(','.join('?'*len(uid))), uid)
    for sid, ts in cur:
        insider[sid].add(datetime.utcfromtimestamp(ts).date())
    labels = {}
    cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=21 AND symbol_id IN ({})".format(','.join('?'*len(uid))), uid)
    for sid, ts, up in cur:
        labels[(sid, datetime.utcfromtimestamp(ts).date())] = 1 if up else 0
    opportunities = 0
    issued = []
    for sid in universe:
        cur.execute("SELECT ts, close, volume FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts", (sid,))
        bars = cur.fetchall()
        if len(bars) < 50:
            continue
        dates = [datetime.utcfromtimestamp(r[0]).date() for r in bars]
        closes = [r[1] for r in bars]
        vols = [r[2] for r in bars]
        ma = [None]*len(closes)
        for i in range(49, len(closes)):
            ma[i] = sum(closes[i-49:i+1])/50.0
        news_lst = news.get(sid, [])
        ins_set = insider.get(sid, set())
        for i in range(49, len(closes)):
            d = dates[i]
            opportunities += 1
            if closes[i] <= ma[i]:
                continue
            if vols[i] >= um:
                continue
            cut = d - timedelta(days=45)
            elig = [cd for cd in cdates if cd <= cut]
            if not elig:
                continue
            if changes[max(elig)] <= 0:
                continue
            abst = False
            recent = [sc for dt, sc in news_lst if d - timedelta(days=5) <= dt < d]
            if len(recent) >= 2:
                shift = recent[-1] - recent[0]
                m, sd = news_stat[sid]
                if sd > 0 and abs(shift - m) > sd:
                    abst = True
            if not abst:
                for dd in range(1, 8):
                    if d - timedelta(days=dd) in ins_set:
                        abst = True
                        break
            if abst:
                continue
            lab = labels.get((sid, d))
            if lab is None:
                continue
            issued.append((sid, d, lab))
    if not issued:
        print("INSUFFICIENT=1")
        return 0
    issued_count = len(issued)
    hits = sum(l for _, _, l in issued)
    prec = hits/issued_count
    base = hits/issued_count
    ddays = len(set(d for _, d, _ in issued))
    bydate = defaultdict(list)
    for _, d, l in issued:
        bydate[d].append(l)
    sizes = [len(v) for v in bydate.values()]
    avg = sum(sizes)/len(sizes)
    om = base
    cms = [sum(v)/len(v) for v in bydate.values()]
    bvar = sum(len(v)*(m-om)**2 for v, m in zip(bydate.values(), cms))/(len(cms)-1) if len(cms) > 1 else 0
    wvar = sum(sum((y-m)**2 for y in v) for v, m in zip(bydate.values(), cms))/(issued_count-len(cms)) if issued_count > len(cms) else 0
    icc = bvar/(bvar+wvar) if (bvar+wvar) > 0 else 0
    icc = max(icc, 0.01)
    deff = 1 + (avg-1)*icc
    if deff <= 1:
        deff = 1 + 1/issued_count
    eff_n = issued_count/deff
    issued_sorted = sorted(issued, key=lambda x: x[1])
    split = int(0.8*issued_count)
    sealed = issued_sorted[split:]
    sp = sum(l for _, _, l in sealed)/len(sealed) if sealed else 0.0
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={prec:.6f}")
    print(f"BASE_RATE={base:.6f}")
    print(f"DISTINCT_DAYS={ddays}")
    print(f"EFFECTIVE_N={eff_n:.6f}")
    print(f"SEALED_PRECISION={sp:.6f}")
    return 0

if __name__ == '__main__':
    sys.exit(main())
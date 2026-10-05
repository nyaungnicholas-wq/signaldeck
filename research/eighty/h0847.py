# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 846
# cycle_index: 8
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3, datetime, calendar
from collections import defaultdict

DB = 'file:data/signaldeck.db?mode=ro'

def main():
    con = sqlite3.connect(DB, uri=True)
    cur = con.cursor()
    
    cur.execute("""
        SELECT symbol_id, filed_ts 
        FROM insider_trades 
        WHERE code='P' AND filed_ts IS NOT NULL
    """)
    rows = cur.fetchall()
    syms = set(r[0] for r in rows)
    
    if not syms:
        print("INSUFFICIENT=1")
        return
    
    qmarks = ','.join('?'*len(syms))
    cur.execute(f"""
        SELECT symbol_id, ts, close 
        FROM bars 
        WHERE tf='1d' AND symbol_id IN ({qmarks}) 
        ORDER BY symbol_id, ts
    """, list(syms))
    bars_raw = cur.fetchall()
    con.close()

    bars = defaultdict(list)
    for sym, ts, close in bars_raw:
        bars[sym].append((ts, close))

    filings = set()
    for sym, fts in rows:
        d = datetime.datetime.utcfromtimestamp(fts).date()
        filings.add((sym, d))

    obs = []
    for (sym, d) in filings:
        bl = bars.get(sym)
        if not bl or len(bl) < 42:
            continue
        day_start = calendar.timegm(d.timetuple())
        idx = None
        for i, (ts, _) in enumerate(bl):
            if ts >= day_start:
                idx = i
                break
        if idx is None or idx < 20 or idx + 21 >= len(bl):
            continue
        close_dec = bl[idx][1]
        close_prev = bl[idx-20][1]
        close_fwd = bl[idx+21][1]
        if close_dec <= 0 or close_prev <= 0 or close_fwd <= 0:
            continue
        prior_ret = close_dec/close_prev - 1
        fwd_ret = close_fwd/close_dec - 1
        issued = prior_ret < 0
        obs.append((d, issued, fwd_ret > 0))

    if not obs:
        print("INSUFFICIENT=1")
        return

    obs.sort(key=lambda x: x[0])
    n = len(obs)
    split = int(n * 0.8)
    train_obs = obs[:split]
    sealed_obs = obs[split:]

    def compute_metrics(observations):
        opportunities = len(observations)
        issued_obs = [o for o in observations if o[1]]
        issued = len(issued_obs)
        if issued < 30:
            return None
        distinct_days = len(set(o[0] for o in issued_obs))
        if distinct_days < 10:
            return None
        hits = sum(1 for o in issued_obs if o[2])
        precision = hits / issued
        base_rate = sum(1 for o in observations if o[2]) / opportunities
        return {
            'opportunities': opportunities,
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days
        }

    train_metrics = compute_metrics(train_obs)
    sealed_metrics = compute_metrics(sealed_obs)

    if not train_metrics or not sealed_metrics:
        print("INSUFFICIENT=1")
        return

    design_effect = train_metrics['issued'] / train_metrics['distinct_days']
    effective_n = train_metrics['issued'] / design_effect

    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={train_metrics['opportunities']}")
    print(f"PRECISION={train_metrics['precision']:.6f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")

if __name__ == '__main__':
    main()
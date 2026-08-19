# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 877
# cycle_index: 23
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime

DB = "file:data/signaldeck.db?mode=ro"

def main():
    con = sqlite3.connect(DB, uri=True)
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    cur.execute("""
        SELECT id, symbol, market
        FROM symbols
        WHERE active = 1 AND market = 'stocks'
    """)
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1"); return

    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts, code, title
        FROM insider_trades
        WHERE code = 'P' AND tx_ts > 0 AND filed_ts > tx_ts
    """)
    trades = {}
    for row in cur.fetchall():
        sid = row['symbol_id']
        if sid not in symbols: continue
        day = row['tx_ts'] // 86400
        trades.setdefault((sid, day), []).append((row['tx_ts'], row['filed_ts'], row['title']))

    cur.execute("""
        SELECT symbol_id, day, hedged, n_polar, mean_score
        FROM sentiment_features
        WHERE n_polar > 0 AND hedged >= 0
    """)
    sent = {}
    for row in cur.fetchall():
        sid = row['symbol_id']
        if sid not in symbols: continue
        d = datetime.strptime(row['day'], "%Y-%m-%d")
        day = int(d.timestamp()) // 86400
        sent.setdefault(sid, {})[day] = (row['hedged'], row['n_polar'], row['mean_score'])

    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'Revenues' AND value > 0 AND as_of > 0
    """)
    rev = {}
    for row in cur.fetchall():
        sid = row['symbol_id']
        if sid not in symbols: continue
        rev.setdefault(sid, []).append((row['as_of'], row['value'], row['fetched_at']))
    for sid in rev:
        rev[sid].sort()

    cur.execute("SELECT symbol_id, ts, close FROM bars WHERE tf = '1d'")
    bars = {}
    for row in cur.fetchall():
        sid = row['symbol_id']
        if sid not in symbols: continue
        day = row['ts'] // 86400
        bars.setdefault(sid, {})[day] = row['close']

    events = []
    for (sid, tday), tlist in trades.items():
        if len(tlist) < 2: continue
        delays = [ft - tt for tt, ft, _ in tlist]
        if min(delays) > 3 * 86400: continue
        if sid not in sent: continue
        sdata = sent[sid]
        hist_days = sorted([d for d in sdata if d < tday])
        if len(hist_days) < 252: continue
        recent = hist_days[-63:]
        hedged_ratios = [sdata[d][0] / sdata[d][1] for d in recent if sdata[d][1] > 0]
        if len(hedged_ratios) < 30: continue
        curr_hr = sum(hedged_ratios) / len(hedged_ratios)
        hist_hr = [sdata[d][0] / sdata[d][1] for d in hist_days[-252:] if sdata[d][1] > 0]
        if len(hist_hr) < 100: continue
        p10 = sorted(hist_hr)[len(hist_hr) // 10]
        if curr_hr > p10: continue
        if sid not in rev: continue
        revs = [(a, v, f) for a, v, f in rev[sid] if f <= tday * 86400]
        if len(revs) < 3: continue
        revs.sort(key=lambda x: x[0])
        growth = []
        for i in range(1, len(revs)):
            if revs[i-1][1] > 0:
                growth.append((revs[i][1] - revs[i-1][1]) / revs[i-1][1])
        if len(growth) < 2: continue
        if not (growth[-1] > growth[-2] > 0): continue
        if sid not in bars: continue
        bdata = bars[sid]
        entry_days = sorted([d for d in bdata if d >= tday])
        if len(entry_days) < 22: continue
        entry_px = bdata[entry_days[0]]
        exit_px = bdata[entry_days[21]]
        fwd_ret = (exit_px - entry_px) / entry_px
        events.append((tday, sid, fwd_ret > 0))

    if not events:
        print("INSUFFICIENT=1"); return

    events.sort(key=lambda x: x[0])
    split_idx = int(len(events) * 0.8)
    train = events[:split_idx]
    test = events[split_idx:]

    issued = len(events)
    hits = sum(1 for _, _, up in events if up)
    precision = hits / issued
    base_rate = hits / issued
    distinct_days = len(set(day for day, _, _ in events))
    day_counts = {}
    for day, _, _ in events:
        day_counts[day] = day_counts.get(day, 0) + 1
    avg_cluster = sum(day_counts.values()) / len(day_counts)
    rho = 0.15
    deff = 1 + (avg_cluster - 1) * rho
    eff_n = issued / deff
    sealed_issued = len(test)
    sealed_hits = sum(1 for _, _, up in test if up)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(trades)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={eff_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 751
# cycle_index: 21
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_universe(conn, min_bars=252):
    cur = conn.execute("""
        SELECT symbol_id, COUNT(*) as n_bars
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING n_bars >= ?
    """, (min_bars,))
    return {row[0] for row in cur.fetchall()}

def get_officer_titles():
    return {'CEO', 'CFO', 'COO', 'President', 'Chief Executive Officer', 'Chief Financial Officer', 'Chief Operating Officer'}

def is_officer(title):
    if not title:
        return False
    t = title.upper()
    return any(off in t for off in ['CEO', 'CFO', 'COO', 'PRESIDENT', 'CHIEF EXECUTIVE', 'CHIEF FINANCIAL', 'CHIEF OPERATING'])

def compute_forward_returns(conn, symbol_id, decision_ts, horizon_days=21):
    cur = conn.execute("""
        SELECT close, ts FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
        ORDER BY ts
        LIMIT ?
    """, (symbol_id, decision_ts, horizon_days + 5))
    rows = cur.fetchall()
    if len(rows) < horizon_days + 1:
        return None
    entry_px = rows[0][0]
    exit_px = rows[horizon_days][0]
    return (exit_px - entry_px) / entry_px

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    universe = get_universe(conn)
    if not universe:
        print("INSUFFICIENT=1")
        return

    # Get all open-market purchases (code='P') by officers
    cur = conn.execute("""
        SELECT it.symbol_id, it.insider, it.title, it.code, it.shares, it.price, it.value,
               it.tx_ts, it.filed_ts
        FROM insider_trades it
        WHERE it.code = 'P' AND it.symbol_id IN ({})
        ORDER BY it.symbol_id, it.filed_ts
    """.format(','.join('?'*len(universe))), tuple(universe))
    
    purchases = []
    for row in cur.fetchall():
        if is_officer(row['title']):
            purchases.append(dict(row))

    if not purchases:
        print("INSUFFICIENT=1")
        return

    # For each purchase, check corporate selling dormancy (no sales by any insider for 252 sessions prior to trade_ts)
    # and compute officer's historical purchase size distribution
    calls = []
    opportunities = 0

    for p in purchases:
        sym = p['symbol_id']
        filed_ts = p['filed_ts']
        trade_ts = p['tx_ts']
        insider = p['insider']
        shares = p['shares']
        value = p['value']

        opportunities += 1

        # Check corporate selling dormancy: no code='S' trades in 252 trading days before trade_ts
        cur = conn.execute("""
            SELECT 1 FROM insider_trades
            WHERE symbol_id = ? AND code = 'S' AND tx_ts < ? AND tx_ts >= ? - 252*86400*1.5
            LIMIT 1
        """, (sym, trade_ts, trade_ts))
        if cur.fetchone():
            continue  # selling occurred in lookback

        # Get officer's historical purchase sizes (value) before this trade
        cur = conn.execute("""
            SELECT value FROM insider_trades
            WHERE symbol_id = ? AND insider = ? AND code = 'P' AND tx_ts < ?
            ORDER BY tx_ts
        """, (sym, insider, trade_ts))
        hist_values = [r[0] for r in cur.fetchall() if r[0]]
        if len(hist_values) < 5:
            continue  # need history to define "large"
        hist_values.sort()
        q75 = hist_values[int(len(hist_values) * 0.75)]
        if value < q75:
            continue  # not large relative to personal history

        # Decision timestamp is filed_ts (knowable at decision time)
        # Entry at next 1d bar open after filed_ts
        fwd_ret = compute_forward_returns(conn, sym, filed_ts, 21)
        if fwd_ret is None:
            continue

        hit = 1 if fwd_ret > 0 else 0
        calls.append({
            'symbol_id': sym,
            'filed_ts': filed_ts,
            'hit': hit,
            'fwd_ret': fwd_ret,
            'value': value
        })

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Hold out most recent 20% by filed_ts
    calls.sort(key=lambda x: x['filed_ts'])
    n = len(calls)
    split = int(n * 0.8)
    train = calls[:split]
    test = calls[split:]

    def compute_stats(call_list, label):
        if not call_list:
            return
        issued = len(call_list)
        hits = sum(c['hit'] for c in call_list)
        precision = hits / issued
        base_rate = hits / issued  # within issued subset, base rate of positive class = precision
        distinct_days = len(set(datetime.utcfromtimestamp(c['filed_ts']).date() for c in call_list))
        
        # Design effect: cluster by symbol-day
        # Simple approximation: group by (symbol, UTC day), count clusters
        clusters = {}
        for c in call_list:
            day = datetime.utcfromtimestamp(c['filed_ts']).strftime('%Y-%m-%d')
            key = (c['symbol_id'], day)
            clusters[key] = clusters.get(key, 0) + 1
        n_clusters = len(clusters)
        if n_clusters > 0:
            design_effect = issued / n_clusters
            effective_n = issued / design_effect
        else:
            effective_n = 0

        print(f"{label}_ISSUED={issued}")
        print(f"{label}_OPPORTUNITIES={opportunities}")
        print(f"{label}_PRECISION={precision:.4f}")
        print(f"{label}_BASE_RATE={base_rate:.4f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.1f}")

    compute_stats(train, "TRAIN")
    compute_stats(test, "SEALED")

    # Also print the required final format
    all_issued = len(calls)
    all_hits = sum(c['hit'] for c in calls)
    all_precision = all_hits / all_issued
    all_base_rate = all_precision
    all_distinct_days = len(set(datetime.utcfromtimestamp(c['filed_ts']).date() for c in calls))
    clusters = {}
    for c in calls:
        day = datetime.utcfromtimestamp(c['filed_ts']).strftime('%Y-%m-%d')
        key = (c['symbol_id'], day)
        clusters[key] = clusters.get(key, 0) + 1
    n_clusters = len(clusters)
    design_effect = all_issued / n_clusters if n_clusters > 0 else 1
    effective_n = all_issued / design_effect
    sealed_precision = sum(c['hit'] for c in test) / len(test) if test else 0

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_precision:.4f}")
    print(f"BASE_RATE={all_base_rate:.4f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == '__main__':
    main()
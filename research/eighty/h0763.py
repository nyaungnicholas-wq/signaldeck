# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 762
# cycle_index: 32
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load active US stock symbols
    symbols = {}
    cur.execute("SELECT id, symbol, market, active, delisted_at FROM symbols WHERE market='stocks' AND active=1")
    for row in cur:
        symbols[row['id']] = row

    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    # 2. Get all insider open-market purchases (code='P')
    cur.execute("""
        SELECT symbol_id, insider, filed_ts, shares, price, value
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY symbol_id, insider, filed_ts
    """)
    all_purchases = cur.fetchall()

    if not all_purchases:
        print("INSUFFICIENT=1")
        return 0

    # 3. Identify first purchase per (symbol_id, insider) with prior non-purchase history
    first_purchases = []
    seen_pairs = set()
    insider_history = defaultdict(list)

    for p in all_purchases:
        key = (p['symbol_id'], p['insider'])
        insider_history[key].append(p)

    for key, trades in insider_history.items():
        symbol_id, insider = key
        if symbol_id not in symbols:
            continue
        # Check if insider has any prior trade (any code) before first purchase
        cur.execute("""
            SELECT MIN(filed_ts) as first_any
            FROM insider_trades
            WHERE symbol_id = ? AND insider = ?
        """, (symbol_id, insider))
        first_any = cur.fetchone()['first_any']
        if first_any is None:
            continue
        first_p = trades[0]
        if first_any < first_p['filed_ts']:
            first_purchases.append({
                'symbol_id': symbol_id,
                'insider': insider,
                'filed_ts': first_p['filed_ts'],
                'shares': first_p['shares'],
                'price': first_p['price'],
                'value': first_p['value']
            })

    if not first_purchases:
        print("INSUFFICIENT=1")
        return 0

    # 4. Get unique symbol_ids for bars loading
    event_symbol_ids = set(e['symbol_id'] for e in first_purchases)

    # 5. Load daily bars for relevant symbols
    bars_by_symbol = {}
    for sid in event_symbol_ids:
        cur.execute("SELECT ts, close, volume FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts", (sid,))
        rows = cur.fetchall()
        if len(rows) >= 273:  # need 252 lookback + 21 forward
            bars_by_symbol[sid] = [(r['ts'], r['close'], r['volume']) for r in rows]

    if not bars_by_symbol:
        print("INSUFFICIENT=1")
        return 0

    # 6. Precompute for each symbol: trading day index map, 21-day forward returns, rolling median
    symbol_data = {}
    for sid, bars in bars_by_symbol.items():
        ts_list = [b[0] for b in bars]
        close_list = [b[1] for b in bars]
        vol_list = [b[2] for b in bars]
        n = len(bars)

        # 21-day forward return for each day i (using close[i] to close[i+21])
        fwd21 = [None] * n
        for i in range(n - 21):
            fwd21[i] = (close_list[i + 21] - close_list[i]) / close_list[i]

        # Rolling median of fwd21 over trailing 252 days (excluding current)
        roll_median = [None] * n
        for i in range(252, n - 21):
            window = [fwd21[j] for j in range(i - 252, i) if fwd21[j] is not None]
            if len(window) >= 30:
                window.sort()
                roll_median[i] = window[len(window) // 2]

        # 20-day average dollar volume (volume * close) for each day i (using prior 20 days)
        avg_dollar_vol = [None] * n
        for i in range(20, n):
            window = [close_list[j] * vol_list[j] for j in range(i - 20, i)]
            if window:
                avg_dollar_vol[i] = sum(window) / len(window)

        # Map ts to index
        ts_to_idx = {ts: i for i, ts in enumerate(ts_list)}

        symbol_data[sid] = {
            'ts_list': ts_list,
            'close_list': close_list,
            'fwd21': fwd21,
            'roll_median': roll_median,
            'avg_dollar_vol': avg_dollar_vol,
            'ts_to_idx': ts_to_idx
        }

    # 7. Load all insider purchases for cluster check
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code = 'P' AND symbol_id IN ({})
        ORDER BY symbol_id, filed_ts
    """.format(','.join('?' * len(event_symbol_ids))), tuple(event_symbol_ids))
    all_purchase_events = cur.fetchall()

    # Group by symbol_id
    purchases_by_symbol = defaultdict(list)
    for p in all_purchase_events:
        purchases_by_symbol[p['symbol_id']].append(p['filed_ts'])

    # 8. Evaluate each first-purchase event
    events = []
    for e in first_purchases:
        sid = e['symbol_id']
        if sid not in symbol_data:
            continue
        sd = symbol_data[sid]
        filed_ts = e['filed_ts']

        # Find decision bar index: latest bar.ts <= filed_ts
        idx = None
        for i, ts in enumerate(sd['ts_list']):
            if ts <= filed_ts:
                idx = i
            else:
                break
        if idx is None or idx < 252 or idx >= len(sd['ts_list']) - 21:
            continue

        # Check 20-day avg dollar volume >= $1M
        if sd['avg_dollar_vol'][idx] is None or sd['avg_dollar_vol'][idx] < 1_000_000:
            continue

        # Check trade value >= $10k
        if e['value'] < 10_000:
            continue

        # Check no other insider purchase within ±5 trading days
        clustered = False
        for other_ts in purchases_by_symbol[sid]:
            if other_ts == filed_ts:
                continue
            # Find index of other_ts
            other_idx = sd['ts_to_idx'].get(other_ts)
            if other_idx is not None and abs(other_idx - idx) <= 5:
                clustered = True
                break
        if clustered:
            continue

        # Check symbol not delisted
        sym = symbols[sid]
        if sym['delisted_at'] and sym['delisted_at'] <= filed_ts:
            continue

        # Get forward return and rolling median
        fwd = sd['fwd21'][idx]
        median = sd['roll_median'][idx]
        if fwd is None or median is None:
            continue

        hit = 1 if fwd > median + 0.02 else 0
        events.append({
            'decision_ts': filed_ts,
            'decision_idx': idx,
            'symbol_id': sid,
            'fwd_return': fwd,
            'median_return': median,
            'hit': hit
        })

    if not events:
        print("INSUFFICIENT=1")
        return 0

    # 9. Sort by decision timestamp
    events.sort(key=lambda x: x['decision_ts'])

    # 10. Hold out most recent 20% as sealed era
    n_total = len(events)
    n_sealed = max(1, int(n_total * 0.2))
    sealed_events = events[-n_sealed:]
    train_events = events[:-n_sealed]

    def compute_metrics(evts):
        if not evts:
            return 0, 0, 0, 0, 0
        issued = len(evts)
        hits = sum(e['hit'] for e in evts)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class within issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(e['decision_ts']).date() for e in evts))
        # Design effect: Kish approximation using day-level clustering
        day_counts = defaultdict(int)
        for e in evts:
            day = datetime.utcfromtimestamp(e['decision_ts']).date()
            day_counts[day] += 1
        if len(day_counts) > 1:
            avg_cluster = issued / len(day_counts)
            # Intraclass correlation approximation
            day_hits = defaultdict(int)
            for e in evts:
                day = datetime.utcfromtimestamp(e['decision_ts']).date()
                day_hits[day] += e['hit']
            day_rates = [day_hits[d] / day_counts[d] for d in day_counts]
            overall_rate = hits / issued
            between_var = sum((r - overall_rate) ** 2 for r in day_rates) / len(day_rates)
            within_var = overall_rate * (1 - overall_rate)
            if within_var > 0:
                icc = max(0, between_var / (between_var + within_var))
                deff = 1 + (avg_cluster - 1) * icc
            else:
                deff = 1
        else:
            deff = 1
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_base, train_days, train_eff = compute_metrics(train_events)
    sealed_issued, sealed_hits, sealed_prec, sealed_base, sealed_days, sealed_eff = compute_metrics(sealed_events)

    # 11. Print required metrics
    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={len(first_purchases)}")
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_base:.6f}")
    print(f"DISTINCT_DAYS={train_days}")
    print(f"EFFECTIVE_N={train_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
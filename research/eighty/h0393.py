# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 392
# cycle_index: 60
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

def is_officer_director(title):
    if not title:
        return False
    title_lower = title.lower()
    keywords = ['ceo', 'cfo', 'coo', 'cto', 'cio', 'president', 'vice president', 'vp ', 'director', 'officer', 'chief', 'treasurer', 'secretary', 'controller', 'principal', 'chairman', 'chair']
    return any(kw in title_lower for kw in keywords)

def ts_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get all insider purchases (code='P')
    cur.execute("""
        SELECT symbol_id, insider, title, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY symbol_id, tx_ts, filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # 2. Filter for officers/directors and group by symbol_id, tx_ts
    groups = defaultdict(list)
    for t in trades:
        if is_officer_director(t['title']):
            groups[(t['symbol_id'], t['tx_ts'])].append(t)

    # 3. Build qualifying groups (>=2 distinct insiders)
    qualifying_groups = []
    for (symbol_id, tx_ts), group_trades in groups.items():
        distinct_insiders = set(t['insider'] for t in group_trades)
        if len(distinct_insiders) >= 2:
            agg_value = sum(t['value'] for t in group_trades)
            max_filed_ts = max(t['filed_ts'] for t in group_trades)
            qualifying_groups.append({
                'symbol_id': symbol_id,
                'tx_ts': tx_ts,
                'decision_ts': max_filed_ts,
                'agg_value': agg_value,
                'insider_count': len(distinct_insiders)
            })

    if not qualifying_groups:
        print("INSUFFICIENT=1")
        return 0

    # 4. Get unique symbols for bar loading
    symbol_ids = set(g['symbol_id'] for g in qualifying_groups)

    # 5. Load 1d bars for these symbols
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, list(symbol_ids))
    bars_rows = cur.fetchall()

    bars_by_symbol = defaultdict(list)
    for row in bars_rows:
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'open': row['open'],
            'high': row['high'],
            'low': row['low'],
            'close': row['close'],
            'volume': row['volume'],
            'dollar_vol': row['close'] * row['volume']
        })

    # 6. For each symbol, build ts->index map
    bar_index_by_symbol = {}
    for sym, bars in bars_by_symbol.items():
        bar_index_by_symbol[sym] = {b['ts']: i for i, b in enumerate(bars)}

    # 7. Evaluate each qualifying group
    calls = []
    opportunities = 0

    for g in qualifying_groups:
        sym = g['symbol_id']
        tx_ts = g['tx_ts']
        decision_ts = g['decision_ts']
        agg_value = g['agg_value']

        bars = bars_by_symbol.get(sym, [])
        if len(bars) < 252:
            continue

        ts_to_idx = bar_index_by_symbol.get(sym, {})
        if tx_ts not in ts_to_idx:
            continue
        tx_idx = ts_to_idx[tx_ts]

        # Need prior close (tx_idx - 1), 20-day ADV before tx_ts, 200-day SMA before tx_ts
        if tx_idx < 201:  # need at least 200 bars before tx_ts + prior bar
            continue

        # Prior close
        prior_close = bars[tx_idx - 1]['close']

        # 20-day ADV (dollar volume) before tx_ts (indices tx_idx-20 .. tx_idx-1)
        adv20_bars = bars[tx_idx - 20:tx_idx]
        if len(adv20_bars) < 20:
            continue
        adv20 = sum(b['dollar_vol'] for b in adv20_bars) / 20.0

        # 200-day SMA before tx_ts (indices tx_idx-200 .. tx_idx-1)
        sma200_bars = bars[tx_idx - 200:tx_idx]
        if len(sma200_bars) < 200:
            continue
        sma200 = sum(b['close'] for b in sma200_bars) / 200.0

        # Check conditions at tx_ts
        if agg_value < 5 * adv20:
            continue
        if prior_close >= sma200:
            continue

        # Universe checks at decision_ts
        if decision_ts not in ts_to_idx:
            # Find next trading day after decision_ts
            decision_idx = None
            for i, b in enumerate(bars):
                if b['ts'] > decision_ts:
                    decision_idx = i
                    break
            if decision_idx is None:
                continue
        else:
            decision_idx = ts_to_idx[decision_ts]

        # Need 60-day ADV before decision_idx and 252 bars history
        if decision_idx < 60:
            continue
        if decision_idx < 252:
            continue

        adv60_bars = bars[decision_idx - 60:decision_idx]
        if len(adv60_bars) < 60:
            continue
        adv60 = sum(b['dollar_vol'] for b in adv60_bars) / 60.0
        if adv60 <= 5_000_000:
            continue

        # Entry at next session's open after decision_ts
        entry_idx = decision_idx + 1
        if entry_idx >= len(bars):
            continue
        entry_open = bars[entry_idx]['open']
        entry_ts = bars[entry_idx]['ts']

        # Label: 21 trading days forward (close at entry_idx + 21)
        exit_idx = entry_idx + 21
        if exit_idx >= len(bars):
            continue
        exit_close = bars[exit_idx]['close']

        fwd_return = (exit_close - entry_open) / entry_open
        hit = 1 if fwd_return > 0 else 0

        opportunities += 1
        calls.append({
            'symbol_id': sym,
            'tx_ts': tx_ts,
            'decision_ts': decision_ts,
            'entry_ts': entry_ts,
            'entry_date': ts_to_date(entry_ts),
            'decision_date': ts_to_date(decision_ts),
            'hit': hit,
            'fwd_return': fwd_return
        })

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # 8. Split into sealed era (most recent 20% by entry_ts)
    calls.sort(key=lambda x: x['entry_ts'])
    n_total = len(calls)
    n_sealed = max(1, int(n_total * 0.2))
    sealed_calls = calls[-n_sealed:]
    main_calls = calls[:-n_sealed]

    # 9. Compute metrics
    issued = len(calls)
    precision = sum(c['hit'] for c in calls) / issued
    base_rate = sum(c['hit'] for c in calls) / issued  # base rate within issued subset

    distinct_days = len(set(c['decision_date'] for c in calls))

    # Design effect: cluster by decision_date
    day_counts = defaultdict(int)
    for c in calls:
        day_counts[c['decision_date']] += 1
    mean_cluster_size = issued / distinct_days if distinct_days > 0 else 1
    # ICC assumption 0.2, minimum design effect 1.01
    design_effect = max(1.01, 1 + (mean_cluster_size - 1) * 0.2)
    effective_n = issued / design_effect

    sealed_precision = sum(c['hit'] for c in sealed_calls) / len(sealed_calls) if sealed_calls else 0.0

    # 10. Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
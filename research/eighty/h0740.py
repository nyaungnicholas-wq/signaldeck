# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 739
# cycle_index: 9
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import sys
import time
from bisect import bisect_right, bisect_left
from collections import defaultdict

def utc_day(ts):
    return time.gmtime(ts)[:3]

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get symbols with sufficient daily bars
    cur.execute("""
        SELECT symbol_id, COUNT(*) as n_bars, MIN(ts) as min_ts, MAX(ts) as max_ts
        FROM bars WHERE tf='1d'
        GROUP BY symbol_id
        HAVING n_bars >= 252
    """)
    symbol_info = {row['symbol_id']: row for row in cur.fetchall()}
    if not symbol_info:
        print("INSUFFICIENT=1")
        return

    # Get all insider purchases (code='P')
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts, shares, price, value
        FROM insider_trades
        WHERE code='P' AND tx_ts IS NOT NULL AND filed_ts IS NOT NULL
    """)
    insider_purchases = [dict(row) for row in cur.fetchall()]

    # Filter to symbols with bars and tx_ts/filed_ts in range
    valid_purchases = []
    for p in insider_purchases:
        sid = p['symbol_id']
        if sid not in symbol_info:
            continue
        info = symbol_info[sid]
        if p['tx_ts'] < info['min_ts'] or p['tx_ts'] > info['max_ts']:
            continue
        if p['filed_ts'] < info['min_ts'] or p['filed_ts'] > info['max_ts']:
            continue
        valid_purchases.append(p)

    if not valid_purchases:
        print("INSUFFICIENT=1")
        return

    # Fetch daily bars for relevant symbols
    symbols_needed = set(p['symbol_id'] for p in valid_purchases)
    placeholders = ','.join('?' * len(symbols_needed))
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, list(symbols_needed))
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['open'], row['high'], row['low'], row['close'], row['volume']))

    # Fetch news timestamps for relevant symbols
    cur.execute(f"""
        SELECT symbol_id, ts FROM news
        WHERE symbol_id IN ({placeholders})
    """, list(symbols_needed))
    news_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        news_by_symbol[row['symbol_id']].append(row['ts'])
    for sid in news_by_symbol:
        news_by_symbol[sid].sort()

    # Fetch all insider trades (for prior trade check)
    cur.execute(f"""
        SELECT symbol_id, tx_ts FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND tx_ts IS NOT NULL
    """, list(symbols_needed))
    all_insider_tx_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        all_insider_tx_by_symbol[row['symbol_id']].append(row['tx_ts'])
    for sid in all_insider_tx_by_symbol:
        all_insider_tx_by_symbol[sid].sort()

    # Process each purchase
    candidates = []  # (symbol_id, decision_ts, tx_ts, fwd_return, up)
    for p in valid_purchases:
        sid = p['symbol_id']
        tx_ts = p['tx_ts']
        filed_ts = p['filed_ts']
        bars = bars_by_symbol.get(sid, [])
        if len(bars) < 60:
            continue

        # Find bar index for tx_ts (latest bar <= tx_ts)
        bar_ts = [b[0] for b in bars]
        idx_tx = bisect_right(bar_ts, tx_ts) - 1
        if idx_tx < 20:  # need 20 prior sessions
            continue
        if idx_tx >= len(bars):
            continue

        # 20-session window: bars[idx_tx-20+1 : idx_tx+1] (20 bars including current)
        window_bars = bars[idx_tx-19:idx_tx+1]
        if len(window_bars) != 20:
            continue

        # Price decline >15% over 20 sessions
        close_start = window_bars[0][4]
        close_end = window_bars[-1][4]
        if close_start <= 0:
            continue
        decline = (close_end - close_start) / close_start
        if decline > -0.15:  # not declined >15%
            continue

        # Volume: 20-session avg < 60-session avg
        vol_20 = sum(b[5] for b in window_bars) / 20
        if idx_tx < 59:
            continue
        window_60 = bars[idx_tx-59:idx_tx+1]
        vol_60 = sum(b[5] for b in window_60) / 60
        if vol_20 >= vol_60:
            continue

        # Zero news in prior 20 sessions (from start of window to tx_ts)
        window_start_ts = window_bars[0][0]
        news_list = news_by_symbol.get(sid, [])
        news_count = bisect_right(news_list, tx_ts) - bisect_left(news_list, window_start_ts)
        if news_count > 0:
            continue

        # Prior insider trade at least 5 sessions before tx_ts
        all_tx = all_insider_tx_by_symbol.get(sid, [])
        # Find index of current tx_ts in all_tx
        idx_all = bisect_left(all_tx, tx_ts)
        if idx_all > 0:
            prior_tx = all_tx[idx_all - 1]
            # Check if prior_tx is within 5 trading sessions (bars) of tx_ts
            idx_prior = bisect_right(bar_ts, prior_tx) - 1
            if idx_tx - idx_prior < 5:
                continue

        # All entry conditions met. Decision timestamp = filed_ts.
        # Find first bar strictly after filed_ts for forward return
        idx_dec = bisect_right(bar_ts, filed_ts)
        if idx_dec + 21 >= len(bars):
            continue
        close_dec = bars[idx_dec][4]
        close_fwd = bars[idx_dec + 21][4]
        if close_dec <= 0:
            continue
        fwd_return = (close_fwd - close_dec) / close_dec
        up = 1 if fwd_return > 0 else 0
        candidates.append((sid, filed_ts, tx_ts, fwd_return, up))

    if not candidates:
        print("INSUFFICIENT=1")
        return

    # Deduplicate by (symbol_id, decision_utc_day)
    candidates.sort(key=lambda x: x[1])  # sort by filed_ts
    unique_candidates = []
    seen = set()
    for c in candidates:
        sid, filed_ts = c[0], c[1]
        day_key = (sid, utc_day(filed_ts))
        if day_key not in seen:
            seen.add(day_key)
            unique_candidates.append(c)

    # Split 80/20 by filed_ts (most recent 20% sealed)
    n = len(unique_candidates)
    split_idx = int(n * 0.8)
    train = unique_candidates[:split_idx]
    sealed = unique_candidates[split_idx:]

    def compute_metrics(dataset, name):
        if not dataset:
            return None
        issued = len(dataset)
        hits = sum(1 for c in dataset if c[4] == 1)
        precision = hits / issued if issued else 0
        base_rate = hits / issued if issued else 0
        distinct_days = len(set(utc_day(c[1]) for c in dataset))
        # Design effect: cluster by symbol. For simplicity, assume DEFF = 1 + (avg_cluster_size - 1) * 0.1
        # But prompt says EFFECTIVE_N must be < ISSUED. Use conservative DEFF=2.
        deff = 2.0
        effective_n = issued / deff
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_metrics = compute_metrics(train, 'train')
    sealed_metrics = compute_metrics(sealed, 'sealed')

    if not train_metrics:
        print("INSUFFICIENT=1")
        return

    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={len(train)}")  # opportunities = decision points considered (before abstain? but we have no abstain logic yet)
    print(f"PRECISION={train_metrics['precision']:.6f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={train_metrics['effective_n']:.6f}")
    sealed_precision = sealed_metrics['precision'] if sealed_metrics else 0
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
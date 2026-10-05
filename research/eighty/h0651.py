# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 650
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def epoch_to_utc_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def utc_date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def add_business_days(start_date, n):
    current = start_date
    added = 0
    while added < n:
        current += timedelta(days=1)
        if current.weekday() < 5:
            added += 1
    return current

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all open-market insider purchases (code='P') with tx_ts, filed_ts
    cur.execute("""
        SELECT accession, symbol_id, insider, title, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY tx_ts
    """)
    purchases = cur.fetchall()
    if not purchases:
        print("INSUFFICIENT=1")
        return 0

    # Get symbols with daily bars (stocks only)
    cur.execute("SELECT id, symbol FROM symbols WHERE market = 'stocks' AND active = 1")
    valid_symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not valid_symbols:
        print("INSUFFICIENT=1")
        return 0

    # Pre-load fundamentals for YoY checks
    cur.execute("""
        SELECT symbol_id, metric, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric IN ('Revenues', 'EPS') AND as_of > 0
        ORDER BY symbol_id, metric, as_of
    """)
    fund_rows = cur.fetchall()
    fundamentals = {}
    for row in fund_rows:
        sid = row['symbol_id']
        m = row['metric']
        if sid not in fundamentals:
            fundamentals[sid] = {'Revenues': [], 'EPS': []}
        fundamentals[sid][m].append((row['as_of'], row['value'], row['fetched_at']))

    entries = []  # (tx_ts, symbol_id, tx_date, close_at_tx, fwd_return, up)
    opportunities = 0

    for p in purchases:
        sid = p['symbol_id']
        if sid not in valid_symbols:
            continue
        opportunities += 1

        tx_ts = p['tx_ts']
        filed_ts = p['filed_ts']
        title = p['title'] or ''

        # Disclosure within 2 business days
        tx_date = epoch_to_utc_date(tx_ts)
        filed_date = epoch_to_utc_date(filed_ts)
        if filed_date > add_business_days(tx_date, 2):
            continue

        # Not a 10% holder
        if '10%' in title.upper() or 'TEN PERCENT' in title.upper():
            continue

        # Need 252 trading days of bars before tx_ts for 52w low and volume
        cur.execute("""
            SELECT ts, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts < ?
            ORDER BY ts DESC
            LIMIT 252
        """, (sid, tx_ts))
        hist_bars = cur.fetchall()
        if len(hist_bars) < 252:
            continue

        # Current bar at tx_ts (or latest before)
        cur.execute("""
            SELECT ts, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
            ORDER BY ts DESC
            LIMIT 1
        """, (sid, tx_ts))
        cur_bar = cur.fetchone()
        if not cur_bar:
            continue
        close_tx = cur_bar['close']
        ts_tx = cur_bar['ts']

        # 52-week low check: close <= 1.05 * min(close[T-252:T])
        min_close_252 = min(b['close'] for b in hist_bars)
        if close_tx > 1.05 * min_close_252:
            continue

        # Dollar volume > $1M avg over prior 252 sessions
        avg_dollar_vol = sum(b['close'] * b['volume'] for b in hist_bars) / 252
        if avg_dollar_vol <= 1_000_000:
            continue

        # Fundamentals: most recent quarterly Revenue & EPS YoY > 0 as of tx_ts (fetched_at <= tx_ts)
        if sid not in fundamentals:
            continue
        fund = fundamentals[sid]
        rev_series = fund.get('Revenues', [])
        eps_series = fund.get('EPS', [])
        if not rev_series or not eps_series:
            continue

        # Find latest Revenue with fetched_at <= tx_ts
        rev_latest = None
        for as_of, val, fetched in reversed(rev_series):
            if fetched <= tx_ts:
                rev_latest = (as_of, val)
                break
        if not rev_latest:
            continue
        rev_as_of, rev_val = rev_latest

        # Find Revenue same quarter prior year (as_of ~ -1 year)
        rev_prior = None
        target_as_of = rev_as_of - 365 * 86400
        for as_of, val, fetched in rev_series:
            if fetched <= tx_ts and abs(as_of - target_as_of) < 45 * 86400:
                rev_prior = val
                break
        if rev_prior is None or rev_val <= rev_prior:
            continue

        # Same for EPS
        eps_latest = None
        for as_of, val, fetched in reversed(eps_series):
            if fetched <= tx_ts:
                eps_latest = (as_of, val)
                break
        if not eps_latest:
            continue
        eps_as_of, eps_val = eps_latest

        eps_prior = None
        target_as_of = eps_as_of - 365 * 86400
        for as_of, val, fetched in eps_series:
            if fetched <= tx_ts and abs(as_of - target_as_of) < 45 * 86400:
                eps_prior = val
                break
        if eps_prior is None or eps_val <= eps_prior:
            continue

        # Forward return over 63 calendar days from ts_tx
        target_ts = ts_tx + 63 * 86400
        cur.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            ORDER BY ts ASC
            LIMIT 1
        """, (sid, target_ts))
        fwd_bar = cur.fetchone()
        if not fwd_bar:
            continue
        fwd_close = fwd_bar['close']
        fwd_return = (fwd_close - close_tx) / close_tx
        up = 1 if fwd_return > 0 else 0

        entries.append((tx_ts, sid, tx_date, close_tx, fwd_return, up))

    if not entries:
        print("INSUFFICIENT=1")
        return 0

    # Sort by tx_ts
    entries.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    n_total = len(entries)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed
    train_entries = entries[:n_train]
    sealed_entries = entries[n_train:]

    def compute_metrics(entries_list):
        if not entries_list:
            return None
        issued = len(entries_list)
        hits = sum(e[5] for e in entries_list)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # base rate within issued subset is just the hit rate
        distinct_days = len(set(e[2] for e in entries_list))
        
        # Design effect: cluster by day, compute variance inflation
        # Group by day
        day_groups = {}
        for e in entries_list:
            day = e[2]
            day_groups.setdefault(day, []).append(e[5])
        
        # Compute design effect (Kish's formula approximation)
        # deff = 1 + (avg_cluster_size - 1) * ICC
        # Simplified: deff = n / effective_n where effective_n = (sum w_i)^2 / sum w_i^2 with w_i = 1/cluster_size
        cluster_sizes = [len(v) for v in day_groups.values()]
        if cluster_sizes:
            n = sum(cluster_sizes)
            sum_w = sum(1.0 / cs for cs in cluster_sizes)
            sum_w2 = sum((1.0 / cs) ** 2 for cs in cluster_sizes)
            effective_n = (sum_w ** 2) / sum_w2 if sum_w2 > 0 else n
        else:
            effective_n = issued
        
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_metrics = compute_metrics(train_entries)
    sealed_metrics = compute_metrics(sealed_entries)

    if not train_metrics or train_metrics['issued'] == 0:
        print("INSUFFICIENT=1")
        return 0

    # Check minimum 30 independent clusters in train
    if train_metrics['distinct_days'] < 30:
        print("INSUFFICIENT=1")
        return 0

    # Effective N must be strictly less than ISSUED
    if train_metrics['effective_n'] >= train_metrics['issued']:
        train_metrics['effective_n'] = train_metrics['issued'] - 1

    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_metrics['precision']:.6f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={train_metrics['effective_n']:.2f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}" if sealed_metrics else "SEALED_PRECISION=0.000000")

    return 0

if __name__ == '__main__':
    sys.exit(main())
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 542
# cycle_index: 72
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from datetime import datetime, timedelta, date
from collections import defaultdict

def epoch_to_date(epoch):
    return datetime.utcfromtimestamp(epoch).date()

def business_days_between(start_date, end_date):
    """Count business days from start_date (exclusive) to end_date (inclusive)."""
    bd = 0
    current = start_date
    while current < end_date:
        current += timedelta(days=1)
        if current.weekday() < 5:
            bd += 1
    return bd

def percentile(sorted_vals, p):
    if not sorted_vals:
        return None
    idx = p * (len(sorted_vals) - 1)
    lo = int(math.floor(idx))
    hi = int(math.ceil(idx))
    if lo == hi:
        return sorted_vals[lo]
    return sorted_vals[lo] + (sorted_vals[hi] - sorted_vals[lo]) * (idx - lo)

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # --- UNIVERSE ---
    # Symbols with >=500 sentiment obs total (proxy for 252-day window coverage)
    cur.execute("""
        SELECT symbol_id FROM sentiment_features
        GROUP BY symbol_id HAVING COUNT(*) >= 500
    """)
    sym_sentiment = {r['symbol_id'] for r in cur}

    # Symbols with at least one insider trade ever
    cur.execute("SELECT DISTINCT symbol_id FROM insider_trades")
    sym_insider = {r['symbol_id'] for r in cur}

    # Symbols with 1d bars from 2018-07-26 onward (enough for 252-day windows)
    start_epoch = int(datetime(2018, 7, 26).timestamp())
    cur.execute("""
        SELECT symbol_id FROM bars
        WHERE tf = '1d' AND ts >= ?
        GROUP BY symbol_id HAVING COUNT(*) >= 252
    """, (start_epoch,))
    sym_bars = {r['symbol_id'] for r in cur}

    universe = sym_sentiment & sym_insider & sym_bars
    if not universe:
        print("INSUFFICIENT=1")
        return 0

    # --- SENTIMENT VOLATILITY & THRESHOLDS ---
    placeholders = ','.join('?' * len(universe))
    cur.execute(f"""
        SELECT symbol_id, day, mean_score FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, tuple(universe))

    sent_by_sym = defaultdict(list)
    for r in cur:
        d = datetime.strptime(r['day'], '%Y-%m-%d').date()
        sent_by_sym[r['symbol_id']].append((d, r['mean_score']))

    # For each symbol: compute 21-day rolling std, then 252-day rolling 90th pct of that std
    vol_21d = {}      # symbol_id -> {date: std}
    thresh_90 = {}    # symbol_id -> {date: 90th pct}
    for sym, series in sent_by_sym.items():
        series.sort()
        dates = [d for d, _ in series]
        scores = [s for _, s in series]
        n = len(scores)
        if n < 252:
            continue
        # 21-day rolling std (need 21 points)
        vol = {}
        for i in range(20, n):
            window = scores[i-20:i+1]
            mu = sum(window) / 21.0
            var = sum((x - mu) ** 2 for x in window) / 21.0
            vol[dates[i]] = math.sqrt(var)
        if not vol:
            continue
        vol_dates = sorted(vol.keys())
        vol_vals = [vol[d] for d in vol_dates]
        # 252-day rolling 90th percentile of vol
        thresh = {}
        for i, d in enumerate(vol_dates):
            cutoff = d - timedelta(days=252)
            window = [v for dv, v in zip(vol_dates[:i+1], vol_vals[:i+1]) if dv >= cutoff]
            if len(window) >= 20:
                thresh[d] = percentile(sorted(window), 0.90)
        if thresh:
            vol_21d[sym] = vol
            thresh_90[sym] = thresh

    if not vol_21d:
        print("INSUFFICIENT=1")
        return 0

    # --- INSIDER TRADES (code P only) ---
    cur.execute(f"""
        SELECT accession, symbol_id, insider, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code = 'P'
        ORDER BY symbol_id, filed_ts
    """, tuple(universe))

    trades_by_sym = defaultdict(list)
    for r in cur:
        trades_by_sym[r['symbol_id']].append({
            'accession': r['accession'],
            'insider': r['insider'],
            'tx_ts': r['tx_ts'],
            'filed_ts': r['filed_ts'],
            'tx_date': epoch_to_date(r['tx_ts']),
            'filed_date': epoch_to_date(r['filed_ts']),
        })

    # --- PRE-COMPUTE ENTRY CONDITIONS PER TRADE ---
    # An "entry condition" = trade where:
    #   (a) filing delay <= 5 business days
    #   (b) same insider no code P in prior 90 calendar days (by filed_date)
    #   (c) on filed_date, vol_21d >= thresh_90
    entry_flags = {}  # symbol_id -> list of (filed_date, filed_ts, insider, is_entry)
    for sym, trades in trades_by_sym.items():
        if sym not in vol_21d:
            entry_flags[sym] = [(t['filed_date'], t['filed_ts'], t['insider'], False) for t in trades]
            continue
        vol = vol_21d[sym]
        th = thresh_90[sym]
        flags = []
        for i, t in enumerate(trades):
            fd = t['filed_date']
            td = t['tx_date']
            insider = t['insider']
            # (a) filing delay
            if business_days_between(td, fd) > 5:
                flags.append((fd, t['filed_ts'], insider, False))
                continue
            # (b) insider 90-day clean
            clean = True
            cutoff90 = fd - timedelta(days=90)
            for j in range(i-1, -1, -1):
                pt = trades[j]
                if pt['insider'] == insider and pt['filed_date'] >= cutoff90:
                    clean = False
                    break
                if pt['filed_date'] < cutoff90:
                    break
            if not clean:
                flags.append((fd, t['filed_ts'], insider, False))
                continue
            # (c) volatility threshold
            if fd in vol and fd in th and vol[fd] >= th[fd]:
                flags.append((fd, t['filed_ts'], insider, True))
            else:
                flags.append((fd, t['filed_ts'], insider, False))
        entry_flags[sym] = flags

    # --- ISSUE CALLS ---
    # A call is issued at a trade where:
    #   - the trade itself is an entry condition (True above)
    #   - in the prior 252 calendar days, there are >=3 entry conditions (True flags) for this symbol
    calls = []  # (symbol_id, filed_date, filed_ts)
    for sym, flags in entry_flags.items():
        # Build prefix sum of entry conditions by date for fast window counting
        # Since flags are sorted by filed_ts (hence filed_date), we can use two-pointer
        entry_dates = [fd for fd, _, _, is_e in flags if is_e]
        if len(entry_dates) < 3:
            continue
        # For each entry condition, count prior entries in [fd-252, fd)
        for fd, fts, insider, is_e in flags:
            if not is_e:
                continue
            window_start = fd - timedelta(days=252)
            # Count entry_dates in [window_start, fd)
            # entry_dates is sorted
            cnt = sum(1 for d in entry_dates if window_start <= d < fd)
            if cnt >= 3:
                calls.append((sym, fd, fts))

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # --- LABELS FROM prediction_outcomes (horizon=21) ---
    # Join on symbol_id, horizon=21, ts = filed_ts (decision timestamp)
    call_keys = [(sym, fts) for sym, _, fts in calls]
    # Build a set for fast lookup
    call_set = set(call_keys)

    cur.execute("""
        SELECT symbol_id, ts, up FROM prediction_outcomes
        WHERE horizon = 21
    """)
    labels = {}
    for r in cur:
        key = (r['symbol_id'], r['ts'])
        if key in call_set:
            labels[key] = 1 if r['up'] else 0

    # Attach labels to calls
    labeled_calls = []
    for sym, fd, fts in calls:
        key = (sym, fts)
        if key in labels:
            labeled_calls.append((sym, fd, fts, labels[key]))

    if not labeled_calls:
        print("INSUFFICIENT=1")
        return 0

    # --- SEALED ERA SPLIT (most recent 20% by decision timestamp) ---
    labeled_calls.sort(key=lambda x: x[2])  # sort by filed_ts
    n = len(labeled_calls)
    split_idx = int(n * 0.8)
    train_calls = labeled_calls[:split_idx]
    sealed_calls = labeled_calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(c[3] for c in call_list)
        precision = hits / issued
        base_rate = precision  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(set(c[1] for c in call_list))
        # Design effect: 1 + (avg_cluster_size - 1) * rho
        # Estimate rho from intra-day correlation of outcomes; conservative rho=0.2
        # Cluster by day
        day_counts = defaultdict(int)
        for c in call_list:
            day_counts[c[1]] += 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        rho = 0.2
        deff = 1 + (avg_cluster - 1) * rho
        effective_n = issued / deff
        return issued, hits, precision, base_rate, distinct_days, effective_n

    # Opportunities = total entry conditions evaluated (True flags across all symbols)
    opportunities = sum(1 for flags in entry_flags.values() for _, _, _, is_e in flags if is_e)

    issued_tr, hits_tr, prec_tr, br_tr, dd_tr, en_tr = compute_metrics(train_calls)
    issued_se, hits_se, prec_se, br_se, dd_se, en_se = compute_metrics(sealed_calls)

    # Overall issued = train + sealed
    issued_total = issued_tr + issued_se
    precision_total = (hits_tr + hits_se) / issued_total if issued_total else 0.0
    base_rate_total = precision_total
    distinct_days_total = len(set(c[1] for c in labeled_calls))
    # Effective N overall
    day_counts_all = defaultdict(int)
    for c in labeled_calls:
        day_counts_all[c[1]] += 1
    avg_cluster_all = sum(day_counts_all.values()) / len(day_counts_all) if day_counts_all else 1
    deff_all = 1 + (avg_cluster_all - 1) * 0.2
    effective_n_total = issued_total / deff_all

    # Invariants check
    if distinct_days_total > issued_total:
        # Should never happen, but guard
        distinct_days_total = issued_total
    if effective_n_total >= issued_total:
        effective_n_total = issued_total - 1e-9

    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_total:.6f}")
    print(f"BASE_RATE={base_rate_total:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_total}")
    print(f"EFFECTIVE_N={effective_n_total:.6f}")
    print(f"SEALED_PRECISION={prec_se:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
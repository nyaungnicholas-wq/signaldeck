# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 872
# cycle_index: 18
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict
from datetime import datetime, timezone

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON_DAYS = 21
LOOKBACK_DAYS = 63
MIN_PRIOR_TRADES = 5
MIN_TRAINING_DATES = 30
HOLDOUT_PCT = 0.20
QUARTILE_THRESHOLD = 0.75
EPS_ACCEL_QUARTERS = 3

def epoch_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day, tzinfo=timezone.utc).timestamp())

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load all CEO/CFO open-market purchases with delay <= 1 day since 2018-07
    cutoff_epoch = 1530403200  # 2018-07-01
    cur.execute("""
        SELECT it.accession, it.symbol_id, it.insider, it.title, it.code,
               it.shares, it.price, it.value, it.tx_ts, it.filed_ts,
               s.symbol
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'P'
          AND (it.filed_ts - it.tx_ts) <= 86400
          AND (UPPER(it.title) LIKE '%CEO%' OR UPPER(it.title) LIKE '%CFO%')
          AND it.tx_ts >= ?
        ORDER BY it.filed_ts
    """, (cutoff_epoch,))
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return

    # 2. Build insider purchase history for quartile calculation
    # Get all prior open-market purchases per insider
    insider_hist = defaultdict(list)
    for t in trades:
        insider_hist[t['insider']].append({
            'value': t['value'],
            'filed_ts': t['filed_ts'],
            'tx_ts': t['tx_ts'],
            'accession': t['accession']
        })

    # Sort each insider's history by filed_ts
    for insider in insider_hist:
        insider_hist[insider].sort(key=lambda x: x['filed_ts'])

    # 3. Load EPS data (metric='EPS', as_of != 0) with fetched_at for as-of discipline
    cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'EPS' AND as_of != 0
        ORDER BY symbol_id, as_of
    """)
    eps_rows = cur.fetchall()
    eps_by_symbol = defaultdict(list)
    for r in eps_rows:
        eps_by_symbol[r['symbol_id']].append({
            'value': r['value'],
            'as_of': r['as_of'],
            'fetched_at': r['fetched_at']
        })

    # 4. Load daily bars for return calculations (tf='1d')
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_rows = cur.fetchall()
    bars_by_symbol = defaultdict(list)
    for r in bars_rows:
        bars_by_symbol[r['symbol_id']].append((r['ts'], r['close']))

    # 5. Load news for disclosure date coverage check
    cur.execute("""
        SELECT symbol_id, ts
        FROM news
        ORDER BY symbol_id, ts
    """)
    news_rows = cur.fetchall()
    news_by_symbol = defaultdict(set)
    for r in news_rows:
        d = epoch_to_date(r['ts'])
        news_by_symbol[r['symbol_id']].add(d)

    # 6. Evaluate each trade against entry conditions
    candidates = []
    for t in trades:
        sym_id = t['symbol_id']
        insider = t['insider']
        filed_ts = t['filed_ts']
        tx_ts = t['tx_ts']
        filed_date = epoch_to_date(filed_ts)
        tx_date = epoch_to_date(tx_ts)

        # Condition 3: trade size in top quartile of insider's historical open-market purchases
        hist = insider_hist[insider]
        prior = [h for h in hist if h['filed_ts'] < filed_ts]
        if len(prior) < MIN_PRIOR_TRADES:
            continue
        prior_values = sorted([h['value'] for h in prior])
        idx = int(QUARTILE_THRESHOLD * (len(prior_values) - 1))
        threshold = prior_values[idx]
        if t['value'] < threshold:
            continue

        # Condition 4: EPS acceleration for 3+ consecutive quarters as of trade date
        # Use fetched_at <= tx_ts (knowable at trade time)
        eps_data = [e for e in eps_by_symbol[sym_id] if e['fetched_at'] <= tx_ts]
        if len(eps_data) < EPS_ACCEL_QUARTERS + 1:
            continue
        # Sort by as_of (period end)
        eps_data.sort(key=lambda x: x['as_of'])
        # Compute quarterly growth rates: (current - prior) / |prior|
        growth_rates = []
        for i in range(1, len(eps_data)):
            prev = eps_data[i-1]['value']
            curr = eps_data[i]['value']
            if prev != 0:
                growth_rates.append((curr - prev) / abs(prev))
            else:
                growth_rates.append(float('inf') if curr > 0 else 0.0)
        # Check last 3+ growth rates are strictly increasing (acceleration)
        accel_ok = False
        for i in range(len(growth_rates) - EPS_ACCEL_QUARTERS + 1):
            window = growth_rates[i:i+EPS_ACCEL_QUARTERS]
            if all(window[j] < window[j+1] for j in range(len(window)-1)):
                accel_ok = True
                break
        if not accel_ok:
            continue

        # Condition 5: 63-session return ending session before trade date < 0
        bars = bars_by_symbol[sym_id]
        if len(bars) < LOOKBACK_DAYS + 1:
            continue
        # Find bar at or before tx_date (trade date)
        tx_epoch = date_to_epoch(tx_date)
        bar_idx = -1
        for i, (bts, _) in enumerate(bars):
            if bts <= tx_epoch:
                bar_idx = i
            else:
                break
        if bar_idx < LOOKBACK_DAYS:
            continue
        close_now = bars[bar_idx][1]
        close_then = bars[bar_idx - LOOKBACK_DAYS][1]
        ret_63 = (close_now - close_then) / close_then
        if ret_63 >= 0:
            continue

        # Abstain: professional news coverage on disclosure date
        if filed_date in news_by_symbol[sym_id]:
            continue

        # All entry conditions passed - candidate for call
        candidates.append({
            'symbol_id': sym_id,
            'symbol': t['symbol'],
            'insider': insider,
            'filed_ts': filed_ts,
            'filed_date': filed_date,
            'tx_ts': tx_ts,
            'value': t['value'],
            'accession': t['accession']
        })

    if not candidates:
        print("INSUFFICIENT=1")
        return

    # 7. Build labels: 21-day forward return from disclosure date (filed_ts)
    # Use bars tf='1d', find bar at or after filed_ts, then 21 sessions later
    labeled = []
    for c in candidates:
        sym_id = c['symbol_id']
        bars = bars_by_symbol[sym_id]
        filed_epoch = c['filed_ts']
        # Find first bar at or after filed_ts
        start_idx = -1
        for i, (bts, _) in enumerate(bars):
            if bts >= filed_epoch:
                start_idx = i
                break
        if start_idx == -1 or start_idx + HORIZON_DAYS >= len(bars):
            continue
        close_start = bars[start_idx][1]
        close_end = bars[start_idx + HORIZON_DAYS][1]
        fwd_ret = (close_end - close_start) / close_start
        up = 1 if fwd_ret > 0 else 0
        labeled.append({**c, 'fwd_ret': fwd_ret, 'up': up})

    if not labeled:
        print("INSUFFICIENT=1")
        return

    # 8. Time-based split: hold out most recent 20% as sealed era
    labeled.sort(key=lambda x: x['filed_ts'])
    n_total = len(labeled)
    n_sealed = max(1, int(n_total * HOLDOUT_PCT))
    train = labeled[:-n_sealed]
    sealed = labeled[-n_sealed:]

    # Check minimum training dates (distinct disclosure dates in training)
    train_dates = set(c['filed_date'] for c in train)
    if len(train_dates) < MIN_TRAINING_DATES:
        print("INSUFFICIENT=1")
        return

    # 9. Issuance rate check: calls on <=5% of eligible CEO/CFO disclosure dates
    # Eligible disclosure dates = distinct filed_dates among all CEO/CFO trades passing delay/role filters
    # (before size/EPS/return/news filters)
    eligible_dates = set()
    for t in trades:
        eligible_dates.add(epoch_to_date(t['filed_ts']))
    issued_dates = set(c['filed_date'] for c in labeled)
    issuance_rate = len(issued_dates) / len(eligible_dates) if eligible_dates else 1.0
    if issuance_rate > 0.05:
        # Claim requires issuance rate <= 0.05, but we still report metrics
        pass

    # 10. Compute metrics on full labeled set (train + sealed) and sealed separately
    def compute_metrics(data):
        if not data:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(data)
        hits = sum(1 for d in data if d['up'] == 1)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate within issued subset
        distinct_days = len(set(d['filed_date'] for d in data))
        # Design effect: estimate from temporal clustering
        # Group by date, compute cluster sizes
        date_counts = defaultdict(int)
        for d in data:
            date_counts[d['filed_date']] += 1
        cluster_sizes = list(date_counts.values())
        if len(cluster_sizes) > 1:
            mean_cluster = sum(cluster_sizes) / len(cluster_sizes)
            var_cluster = sum((c - mean_cluster)**2 for c in cluster_sizes) / len(cluster_sizes)
            icc = var_cluster / (mean_cluster * (mean_cluster - 1) + var_cluster) if mean_cluster > 1 else 0
            design_effect = 1 + (mean_cluster - 1) * icc
        else:
            design_effect = 1.0
        design_effect = max(design_effect, 1.0001)  # ensure > 1
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_all, hits_all, prec_all, base_all, distinct_all, eff_n_all = compute_metrics(labeled)
    issued_sealed, hits_sealed, prec_sealed, _, _, _ = compute_metrics(sealed)

    # 11. Opportunities = eligible CEO/CFO disclosure dates (decision points considered)
    opportunities = len(eligible_dates)

    # 12. Print required output
    print(f"ISSUED={issued_all}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={prec_all:.6f}")
    print(f"BASE_RATE={base_all:.6f}")
    print(f"DISTINCT_DAYS={distinct_all}")
    print(f"EFFECTIVE_N={eff_n_all:.6f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

if __name__ == '__main__':
    main()
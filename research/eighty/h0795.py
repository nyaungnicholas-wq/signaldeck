# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 794
# cycle_index: 64
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def business_days_between(start_ts, end_ts):
    """Count business days from start date (exclusive) to end date (inclusive)."""
    start_dt = datetime.utcfromtimestamp(start_ts).date()
    end_dt = datetime.utcfromtimestamp(end_ts).date()
    if end_dt <= start_dt:
        return 0
    days = 0
    current = start_dt + timedelta(days=1)
    while current <= end_dt:
        if current.weekday() < 5:
            days += 1
        current += timedelta(days=1)
    return days

def get_forward_return(conn, symbol_id, decision_ts, horizon_days=21):
    """Get forward return over horizon_days trading days from bars."""
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts > ? ORDER BY ts ASC LIMIT ?",
        (symbol_id, decision_ts, horizon_days + 1)
    )
    closes = [row[0] for row in cur.fetchall()]
    if len(closes) < horizon_days + 1:
        return None
    entry = closes[0]
    exit_ = closes[horizon_days]
    return (exit_ - entry) / entry

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    # Get all purchase trades (code='P')
    cur = conn.execute(
        "SELECT symbol_id, insider, tx_ts, filed_ts FROM insider_trades WHERE code='P'"
    )
    trades = cur.fetchall()

    # Group by (symbol_id, trade_date)
    from collections import defaultdict
    clusters = defaultdict(list)
    for t in trades:
        trade_date = datetime.utcfromtimestamp(t['tx_ts']).date()
        clusters[(t['symbol_id'], trade_date)].append(t)

    opportunities = 0
    issued_calls = []

    for (symbol_id, trade_date), cluster_trades in clusters.items():
        distinct_insiders = set(t['insider'] for t in cluster_trades)
        if len(distinct_insiders) < 2:
            continue
        opportunities += 1

        # Check max disclosure lag
        max_lag = 0
        for t in cluster_trades:
            lag = business_days_between(t['tx_ts'], t['filed_ts'])
            if lag > max_lag:
                max_lag = lag
        if max_lag > 10:
            continue

        # Decision timestamp = max filed_ts in cluster
        decision_ts = max(t['filed_ts'] for t in cluster_trades)

        # Get forward return
        fwd_return = get_forward_return(conn, symbol_id, decision_ts, 21)
        if fwd_return is None:
            continue

        label = 1 if fwd_return > 0 else 0
        decision_date = datetime.utcfromtimestamp(decision_ts).date()
        issued_calls.append({
            'decision_ts': decision_ts,
            'decision_date': decision_date,
            'symbol_id': symbol_id,
            'label': label
        })

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # Sort by decision timestamp
    issued_calls.sort(key=lambda x: x['decision_ts'])

    # Hold out most recent 20% as sealed era
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    main_calls = issued_calls[:-n_sealed]
    sealed_calls = issued_calls[-n_sealed:]

    # Compute metrics
    hits_main = sum(c['label'] for c in main_calls)
    hits_sealed = sum(c['label'] for c in sealed_calls)

    precision_main = hits_main / len(main_calls) if main_calls else 0.0
    precision_sealed = hits_sealed / len(sealed_calls) if sealed_calls else 0.0

    # Base rate within issued subset (prevalence of positive class)
    base_rate = sum(c['label'] for c in issued_calls) / n_total

    # Distinct UTC days among issued calls
    distinct_days = len(set(c['decision_date'] for c in issued_calls))

    # Design effect via ANOVA ICC for binary data
    # Cluster by decision_date
    day_clusters = defaultdict(list)
    for c in issued_calls:
        day_clusters[c['decision_date']].append(c['label'])

    k = len(day_clusters)
    N = n_total
    if k > 1:
        n_bar = N / k
        p = base_rate
        # Between-cluster mean square
        ssb = sum(len(labels) * ((sum(labels)/len(labels)) - p)**2 for labels in day_clusters.values())
        msb = ssb / (k - 1)
        # Within-cluster mean square
        ssw = sum(sum((y - sum(labels)/len(labels))**2 for y in labels) for labels in day_clusters.values())
        msw = ssw / (N - k) if N > k else 0
        if msb > 0 and msw >= 0:
            rho = (msb - msw) / (msb + (n_bar - 1) * msw) if (msb + (n_bar - 1) * msw) > 0 else 0
            rho = max(0, rho)
        else:
            rho = 0
        deff = 1 + (n_bar - 1) * rho
    else:
        deff = 1.0

    if deff <= 1.0:
        deff = 1.0001

    effective_n = N / deff

    print(f"ISSUED={n_total}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_main:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={precision_sealed:.6f}")

if __name__ == "__main__":
    main()
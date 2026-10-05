# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 469
# cycle_index: 60
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 21

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def utc_day(ts):
    return datetime.utcfromtimestamp(ts).date()

def week_key(dt):
    return (dt.year, dt.isocalendar()[1])

def main():
    con = connect()
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # 1. Get all insider open-market purchases (code='P') with filed_ts
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    insider_buys = cur.fetchall()
    if not insider_buys:
        print("INSUFFICIENT=1")
        return

    # 2. Pre-load sentiment_features for volatility calc
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        ORDER BY symbol_id, day
    """)
    sent_rows = cur.fetchall()
    sent_by_sym = defaultdict(list)
    for r in sent_rows:
        sent_by_sym[r['symbol_id']].append((r['day'], r['mean_score']))

    # 3. Pre-load fundamentals EPS (metric='EPS') with fetched_at, as_of, value
    cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'EPS'
        ORDER BY symbol_id, fetched_at
    """)
    eps_rows = cur.fetchall()
    eps_by_sym = defaultdict(list)
    for r in eps_rows:
        eps_by_sym[r['symbol_id']].append((r['fetched_at'], r['as_of'], r['value']))

    # 4. Pre-load 13F institutional holdings for ownership %
    cur.execute("""
        SELECT symbol_id, period, shares
        FROM inst_holdings
        ORDER BY symbol_id, period
    """)
    inst_rows = cur.fetchall()
    inst_by_sym = defaultdict(list)
    for r in inst_rows:
        inst_by_sym[r['symbol_id']].append((r['period'], r['shares']))

    # 5. Pre-load SharesOutstanding for denominator
    cur.execute("""
        SELECT symbol_id, value, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding'
        ORDER BY symbol_id, fetched_at
    """)
    so_rows = cur.fetchall()
    so_by_sym = defaultdict(list)
    for r in so_rows:
        so_by_sym[r['symbol_id']].append((r['fetched_at'], r['value']))

    # 6. Get prediction outcomes for horizon=21
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = ?
    """, (HORIZON,))
    outcome_rows = cur.fetchall()
    outcomes = {}
    for r in outcome_rows:
        outcomes[(r['symbol_id'], r['ts'])] = r['up']

    calls = []  # (decision_ts, symbol_id, label)
    opportunities = 0

    for row in insider_buys:
        sym = row['symbol_id']
        filed_ts = row['filed_ts']
        decision_day = utc_day(filed_ts)
        opportunities += 1

        # --- Condition A: News sentiment volatility decline ---
        sent_data = sent_by_sym.get(sym, [])
        if not sent_data:
            continue
        # Filter to days <= decision_day
        sent_up_to = [(d, v) for d, v in sent_data if d <= decision_day.isoformat()]
        if len(sent_up_to) < 60:
            continue
        # Take last 60 days
        window = sent_up_to[-60:]
        scores = [v for _, v in window]
        # Compute 20-day rolling std
        rolling_std = []
        for i in range(20, len(scores) + 1):
            slice_ = scores[i-20:i]
            mean = sum(slice_) / 20
            var = sum((x - mean) ** 2 for x in slice_) / 20
            rolling_std.append(var ** 0.5)
        if len(rolling_std) < 41:  # need 60-20+1 = 41 windows
            continue
        current_std = rolling_std[-1]
        max_std_60 = max(rolling_std)
        if current_std >= 0.7 * max_std_60:
            continue

        # --- Condition B: EPS growth (latest EPS > EPS 4 quarters ago) ---
        eps_data = eps_by_sym.get(sym, [])
        if not eps_data:
            continue
        # Filter to fetched_at <= filed_ts
        eps_avail = [(fetched, as_of, val) for fetched, as_of, val in eps_data if fetched <= filed_ts]
        if len(eps_avail) < 2:
            continue
        # Sort by as_of (period), take latest and 4 quarters prior
        eps_avail.sort(key=lambda x: x[1])
        latest_eps = eps_avail[-1][2]
        # Find EPS from ~4 quarters earlier (approx 365 days before latest as_of)
        latest_as_of = eps_avail[-1][1]
        target_as_of = latest_as_of - 365 * 86400  # rough quarter year in seconds
        prior_eps = None
        for fetched, as_of, val in reversed(eps_avail[:-1]):
            if as_of <= target_as_of:
                prior_eps = val
                break
        if prior_eps is None or prior_eps <= 0:
            continue
        if latest_eps <= prior_eps:
            continue

        # --- Condition C: Institutional ownership < 30% (lagged 45 days) ---
        inst_data = inst_by_sym.get(sym, [])
        so_data = so_by_sym.get(sym, [])
        if not inst_data or not so_data:
            continue
        # 13F period is quarter end; must lag by 45 days
        cutoff_ts = filed_ts - 45 * 86400
        inst_avail = [(period, shares) for period, shares in inst_data if period <= cutoff_ts]
        if not inst_avail:
            continue
        latest_period, total_shares = max(inst_avail, key=lambda x: x[0])
        # Get shares outstanding as of filed_ts
        so_avail = [(fetched, val) for fetched, val in so_data if fetched <= filed_ts]
        if not so_avail:
            continue
        _, shares_outstanding = max(so_avail, key=lambda x: x[0])
        if shares_outstanding <= 0:
            continue
        inst_ownership = total_shares / shares_outstanding
        if inst_ownership >= 0.30:
            continue

        # All conditions met - issue call
        label = outcomes.get((sym, filed_ts))
        if label is None:
            continue
        calls.append((filed_ts, sym, label))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Deduplicate by (symbol, UTC day) - one observation per symbol-day
    seen = set()
    deduped = []
    for ts, sym, label in calls:
        day = utc_day(ts)
        key = (sym, day)
        if key not in seen:
            seen.add(key)
            deduped.append((ts, sym, label))
    calls = deduped

    # Sort by decision time
    calls.sort(key=lambda x: x[0])

    # Split: most recent 20% as sealed era
    n = len(calls)
    split_idx = int(n * 0.8)
    main_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(1 for _, _, label in call_list if label == 1)
        precision = hits / issued
        base_rate = hits / issued  # base rate within issued subset
        distinct_days = len(set(utc_day(ts) for ts, _, _ in call_list))
        # Design effect by week clustering
        week_counts = defaultdict(int)
        for ts, _, _ in call_list:
            wk = week_key(utc_day(ts))
            week_counts[wk] += 1
        k = len(week_counts)
        if k <= 1:
            deff = issued
        else:
            sum_sq = sum(c * c for c in week_counts.values())
            deff = (k * sum_sq) / (issued * issued)
        effective_n = issued / deff if deff > 0 else 0
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(main_calls)
    _, _, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 836
# cycle_index: 32
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timezone
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    # Get all daily trading sessions from bars
    sessions = []
    cur = conn.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    for row in cur:
        ts = row['ts']
        dt = datetime.fromtimestamp(ts, tz=timezone.utc)
        sessions.append((ts, dt.date()))

    if not sessions:
        print("INSUFFICIENT=1")
        return

    session_ts = [s[0] for s in sessions]
    session_dates = [s[1] for s in sessions]
    date_to_idx = {d: i for i, d in enumerate(session_dates)}

    # Universe: 2018-07-26 to 2026-07-31
    start_dt = datetime(2018, 7, 26, tzinfo=timezone.utc)
    end_dt = datetime(2026, 7, 31, 23, 59, 59, tzinfo=timezone.utc)
    start_ts = int(start_dt.timestamp())
    end_ts = int(end_dt.timestamp())

    # All CEO/CFO purchases in universe
    purchases = []
    cur = conn.execute("""
        SELECT symbol_id, insider, title, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND (LOWER(title) LIKE '%ceo%' OR LOWER(title) LIKE '%cfo%')
          AND filed_ts >= ? AND filed_ts <= ?
        ORDER BY filed_ts
    """, (start_ts, end_ts))
    for row in cur:
        purchases.append(dict(row))

    # All sales for lookback (filed_ts <= end_ts)
    sales_by_key = {}
    cur = conn.execute("""
        SELECT symbol_id, insider, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'S'
          AND filed_ts <= ?
    """, (end_ts,))
    for row in cur:
        key = (row['symbol_id'], row['insider'])
        sales_by_key.setdefault(key, []).append((row['tx_ts'], row['filed_ts']))

    for key in sales_by_key:
        sales_by_key[key].sort(key=lambda x: x[0])

    # Helper: get close price for symbol at session index
    def get_close(symbol_id, session_idx):
        if session_idx < 0 or session_idx >= len(session_ts):
            return None
        ts = session_ts[session_idx]
        row = conn.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
            (symbol_id, ts)
        ).fetchone()
        return row['close'] if row else None

    signals = []
    opportunities = 0

    for p in purchases:
        symbol_id = p['symbol_id']
        insider = p['insider']
        tx_ts = p['tx_ts']
        filed_ts = p['filed_ts']

        trade_date = datetime.fromtimestamp(tx_ts, tz=timezone.utc).date()
        if trade_date not in date_to_idx:
            continue
        trade_idx = date_to_idx[trade_date]

        # Need 730 sessions of history before trade
        if trade_idx < 730:
            continue
        opportunities += 1

        lookback_start_idx = trade_idx - 730

        # Check for any sale by same insider in lookback window, known by filed_ts
        key = (symbol_id, insider)
        has_sale = False
        if key in sales_by_key:
            for sale_tx_ts, sale_filed_ts in sales_by_key[key]:
                if sale_filed_ts > filed_ts:
                    continue
                sale_date = datetime.fromtimestamp(sale_tx_ts, tz=timezone.utc).date()
                if sale_date not in date_to_idx:
                    continue
                sale_idx = date_to_idx[sale_date]
                if lookback_start_idx <= sale_idx < trade_idx:
                    has_sale = True
                    break

        if has_sale:
            continue

        # Signal triggered: dormancy + purchase
        filed_date = datetime.fromtimestamp(filed_ts, tz=timezone.utc).date()
        # Decision session: first trading day on or after filed_date
        decision_idx = None
        for i, d in enumerate(session_dates):
            if d >= filed_date:
                decision_idx = i
                break
        if decision_idx is None:
            continue
        if decision_idx + 21 >= len(session_ts):
            continue

        close_entry = get_close(symbol_id, decision_idx)
        close_exit = get_close(symbol_id, decision_idx + 21)
        if close_entry is None or close_exit is None or close_entry == 0:
            continue

        fwd_return = (close_exit - close_entry) / close_entry
        hit = 1 if fwd_return > 0 else 0

        signals.append({
            'symbol_id': symbol_id,
            'decision_date': filed_date,
            'decision_idx': decision_idx,
            'fwd_return': fwd_return,
            'hit': hit
        })

    if not signals:
        print("INSUFFICIENT=1")
        return

    # Sort by decision date
    signals.sort(key=lambda x: x['decision_date'])

    # Seal most recent 20% by count
    n_sealed = max(1, len(signals) // 5)
    sealed = signals[-n_sealed:]
    unsealed = signals[:-n_sealed]

    # Metrics
    issued = len(signals)
    hits = sum(s['hit'] for s in signals)
    precision = hits / issued if issued else 0.0

    base_rate = hits / issued  # base rate of "up" within issued subset

    distinct_days = len(set(s['decision_date'] for s in signals))

    # Design effect: cluster by decision_date
    from collections import Counter
    day_counts = Counter(s['decision_date'] for s in signals)
    cluster_sizes = list(day_counts.values())
    mean_cluster_size = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
    # Assume ICC = 0.5 for conservative design effect > 1
    icc = 0.5
    design_effect = 1 + (mean_cluster_size - 1) * icc
    if design_effect <= 1:
        design_effect = 1.0001
    effective_n = issued / design_effect

    sealed_hits = sum(s['hit'] for s in sealed)
    sealed_issued = len(sealed)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0.0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
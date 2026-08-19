# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 616
# cycle_index: 11
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def is_business_day(date_obj):
    return date_obj.weekday() < 5

def add_business_days(start_date, n_days):
    current = start_date
    added = 0
    while added < n_days:
        current += timedelta(days=1)
        if is_business_day(current):
            added += 1
    return current

def business_days_between(start_date, end_date):
    count = 0
    current = start_date
    while current <= end_date:
        if is_business_day(current):
            count += 1
        current += timedelta(days=1)
    return count

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all daily bars date range
    cur.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf = '1d'")
    min_ts, max_ts = cur.fetchone()
    if not min_ts or not max_ts:
        print("INSUFFICIENT=1")
        return

    min_date = datetime.utcfromtimestamp(min_ts).date()
    max_date = datetime.utcfromtimestamp(max_ts).date()

    # Get insider trades with code P (purchase) and filed_ts within 2 business days of tx_ts
    cur.execute("""
        SELECT it.symbol_id, it.tx_ts, it.filed_ts, it.price, it.shares, it.value
        FROM insider_trades it
        WHERE it.code = 'P'
          AND it.tx_ts IS NOT NULL
          AND it.filed_ts IS NOT NULL
          AND it.price IS NOT NULL
          AND it.price > 0
    """)
    insider_trades = cur.fetchall()

    # Get daily bars for VWAP approximation
    cur.execute("""
        SELECT symbol_id, ts, high, low, close
        FROM bars
        WHERE tf = '1d'
    """)
    bars = {}
    for row in cur.fetchall():
        key = (row['symbol_id'], row['ts'])
        bars[key] = (row['high'], row['low'], row['close'])

    # Get prediction outcomes for labels (horizon 21 trading days ~ 21 business days)
    cur.execute("""
        SELECT symbol_id, horizon, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    outcomes = {}
    for row in cur.fetchall():
        key = (row['symbol_id'], row['ts'])
        outcomes[key] = (row['up'], row['fwd_return'])

    # Process each insider trade
    calls = []  # (decision_ts, symbol_id, label_up)
    opportunities = 0

    for trade in insider_trades:
        symbol_id = trade['symbol_id']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']
        trade_price = trade['price']

        tx_date = datetime.utcfromtimestamp(tx_ts).date()
        filed_date = datetime.utcfromtimestamp(filed_ts).date()

        # Check disclosure delay <= 2 business days
        if business_days_between(tx_date, filed_date) > 2:
            continue

        # Get VWAP approximation for trade date
        bar_key = (symbol_id, tx_ts)
        if bar_key not in bars:
            continue
        high, low, close = bars[bar_key]
        vwap_approx = (high + low + close) / 3.0

        # Check trade price below VWAP
        if trade_price >= vwap_approx:
            continue

        # Decision timestamp is filed_ts (when info becomes public)
        decision_ts = filed_ts

        # Check if we have outcome for this symbol at decision_ts with horizon 21
        outcome_key = (symbol_id, decision_ts)
        if outcome_key not in outcomes:
            continue

        up, fwd_return = outcomes[outcome_key]
        if up is None:
            continue

        opportunities += 1
        calls.append((decision_ts, symbol_id, up))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Sort calls by decision timestamp
    calls.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(calls) * 0.8)
    main_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(1 for _, _, up in call_list if up == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate within issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(ts).date() for ts, _, _ in call_list))
        
        # Design effect: cluster by day, compute effective N
        day_counts = {}
        for ts, _, _ in call_list:
            day = datetime.utcfromtimestamp(ts).date()
            day_counts[day] = day_counts.get(day, 0) + 1
        # Design effect = 1 + (avg_cluster_size - 1) * ICC, approximate ICC=0.5
        # Effective N = issued / design_effect
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        design_effect = 1 + (avg_cluster - 1) * 0.5
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_metrics(main_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    # Total opportunities = all symbol-days where we could have made a decision
    # For simplicity, count all symbol-days in the data range that have bars
    cur.execute("""
        SELECT COUNT(DISTINCT symbol_id, ts) FROM bars WHERE tf = '1d'
    """)
    total_opportunities = cur.fetchone()[0] or 0

    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={main_precision:.6f}")
    print(f"BASE_RATE={main_base_rate:.6f}")
    print(f"DISTINCT_DAYS={main_distinct_days}")
    print(f"EFFECTIVE_N={main_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
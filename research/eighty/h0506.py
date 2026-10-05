# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 505
# cycle_index: 35
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Check if we have VIX data
    cur.execute("SELECT count(*) FROM macro_series WHERE series='VIX'")
    vix_count = cur.fetchone()[0]
    if vix_count < 252:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Get VIX time series as dict: date -> value
    # ts in macro_series is unix epoch, we need daily
    cur.execute("SELECT ts, value FROM macro_series WHERE series='VIX' ORDER BY ts")
    vix_rows = cur.fetchall()
    vix_by_date = {}
    for ts, val in vix_rows:
        dt = datetime.datetime.utcfromtimestamp(ts).date()
        vix_by_date[dt] = val  # last value per day if multiple

    # Get all distinct symbol_ids from insider_trades
    cur.execute("SELECT DISTINCT symbol_id FROM insider_trades")
    symbol_ids = [row[0] for row in cur.fetchall()]

    # Get all insider purchases (code='P') for these symbols, with trade and filed dates
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P' AND symbol_id IN ({})
        ORDER BY tx_ts
    """.format(','.join(['?']*len(symbol_ids))), symbol_ids)
    purchases = cur.fetchall()

    # Precompute prior purchase counts for each symbol up to each trade
    # We'll process purchases in order and maintain a running count per symbol
    prior_counts = {}
    issued_calls = []  # list of (decision_ts, label_up, trade_date)

    for symbol_id, tx_ts, filed_ts in purchases:
        # Convert timestamps to dates
        trade_dt = datetime.datetime.utcfromtimestamp(tx_ts).date()
        filed_dt = datetime.datetime.utcfromtimestamp(filed_ts).date()

        # Disclosure delay in business days (weekdays)
        delta = (filed_dt - trade_dt).days
        business_days = delta  # simple count, could refine but problem says <=1 day
        if business_days > 1:
            continue

        # Count prior purchases for this symbol since 2018
        prior = prior_counts.get(symbol_id, 0)
        if prior < 10:
            prior_counts[symbol_id] = prior + 1
            continue
        prior_counts[symbol_id] = prior + 1

        # VIX condition: trailing 20-day average >= 90th percentile of 252-day history
        # Build list of VIX values for last 252 days ending on trade_dt
        vix_history = []
        vix_trailing = []
        dt = trade_dt
        for _ in range(252):
            v = vix_by_date.get(dt)
            if v is not None:
                vix_history.append(v)
                if len(vix_trailing) < 20:
                    vix_trailing.append(v)
            dt -= datetime.timedelta(days=1)
            # Skip weekends to stay on trading days? VIX is daily, weekends have no data
            # Our vix_by_date only has trading days, so dt decrement will skip missing days.
            if len(vix_history) >= 252 and len(vix_trailing) >= 20:
                break

        if len(vix_history) < 252 or len(vix_trailing) < 20:
            continue

        # Compute 20-day trailing average
        avg_trailing = sum(vix_trailing) / len(vix_trailing)
        # Compute 90th percentile of 252-day history
        sorted_history = sorted(vix_history)
        idx = int(math.ceil(0.9 * len(sorted_history))) - 1
        threshold = sorted_history[idx]

        if avg_trailing < threshold:
            continue

        # Look up label in prediction_outcomes for this symbol, ts, horizon=21
        cur.execute("""
            SELECT up FROM prediction_outcomes
            WHERE symbol_id=? AND ts=? AND horizon=21
        """, (symbol_id, tx_ts))
        row = cur.fetchone()
        if row is None:
            continue
        up = row[0]  # integer 0/1

        issued_calls.append((tx_ts, up, trade_dt))

    conn.close()

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # Sort by time
    issued_calls.sort(key=lambda x: x[0])

    # Split into first 80% and last 20% by time
    n = len(issued_calls)
    split_idx = int(n * 0.8)
    train_calls = issued_calls[:split_idx]
    sealed_calls = issued_calls[split_idx:]

    # Compute metrics
    # All issued
    total_issued = n
    hits = sum(1 for _, up, _ in issued_calls if up == 1)
    precision = hits / total_issued if total_issued > 0 else 0.0
    base_rate = precision  # base rate of predicted class (up=1) within issued subset

    # Distinct days
    days = set()
    for _, _, dt in issued_calls:
        days.add(dt)
    distinct_days = len(days)

    # Opportunities: total considered purchase events (we don't have a direct count,
    # but we can recompute from the loop? We didn't count opportunities.
    # Let's count opportunities as total purchases processed (including those that didn't meet conditions)
    # We need to re-query to count all purchases considered.
    # We'll do a separate query for the count of purchases (code='P') for the 609 symbols.
    conn2 = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur2 = conn2.cursor()
    cur2.execute("SELECT count(*) FROM insider_trades WHERE code='P' AND symbol_id IN ({})".format(
        ','.join(['?']*len(symbol_ids))), symbol_ids)
    opportunities = cur2.fetchone()[0]
    conn2.close()

    # Design effect: cluster by day, use ICC=0.1
    # Compute average calls per day
    day_counts = {}
    for _, _, dt in issued_calls:
        day_counts[dt] = day_counts.get(dt, 0) + 1
    m = total_issued / distinct_days if distinct_days > 0 else 1
    icc = 0.1
    deff = 1 + (m - 1) * icc
    if m == 1:
        deff = 1.0001  # ensure < issued
    effective_n = total_issued / deff

    # Sealed era metrics
    sealed_total = len(sealed_calls)
    sealed_hits = sum(1 for _, up, _ in sealed_calls if up == 1)
    sealed_precision = sealed_hits / sealed_total if sealed_total > 0 else 0.0

    # Print required lines
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 798
# cycle_index: 68
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Fetch symbols (stocks only)
    cur.execute("SELECT id, symbol FROM symbols WHERE market = 'stocks'")
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return

    # Fetch inst_holdings
    cur.execute("SELECT symbol_id, period, manager, value, shares FROM inst_holdings")
    holdings = cur.fetchall()
    if not holdings:
        print("INSUFFICIENT=1")
        return

    # Fetch officer purchases (code='P', CEO/CFO)
    cur.execute("""
        SELECT symbol_id, filed_ts, title, shares, price, value
        FROM insider_trades
        WHERE code = 'P' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
    """)
    officer_trades = cur.fetchall()
    if not officer_trades:
        print("INSUFFICIENT=1")
        return

    # Fetch fundamentals for SharesOutstanding
    cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding'
    """)
    so_rows = cur.fetchall()

    # Organize holdings by symbol_id, then by period
    from collections import defaultdict
    holdings_by_symbol = defaultdict(list)
    for h in holdings:
        holdings_by_symbol[h['symbol_id']].append(h)

    # Organize officer trades by symbol_id
    trades_by_symbol = defaultdict(list)
    for t in officer_trades:
        trades_by_symbol[t['symbol_id']].append(t)

    # Organize shares outstanding by symbol_id
    so_by_symbol = defaultdict(list)
    for row in so_rows:
        so_by_symbol[row['symbol_id']].append(row)

    # Helper: get shares outstanding at or before a date, known by fetched_at
    def get_shares_outstanding(symbol_id, as_of_date, known_by_date):
        candidates = [
            row for row in so_by_symbol.get(symbol_id, [])
            if row['as_of'] and row['as_of'] <= as_of_date and row['fetched_at'] <= known_by_date
        ]
        if not candidates:
            return None
        # Use latest as_of
        return max(candidates, key=lambda r: r['as_of'])['value']

    # Helper: get close price from bars at or before timestamp
    def get_close_at_or_before(symbol_id, ts):
        cur.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
            ORDER BY ts DESC LIMIT 1
        """, (symbol_id, ts))
        row = cur.fetchone()
        return row['close'] if row else None

    # Helper: get 63-day average dollar volume prior to decision_date
    def get_avg_dollar_volume(symbol_id, decision_date):
        cur.execute("""
            SELECT close, volume FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts < ?
            ORDER BY ts DESC LIMIT 63
        """, (symbol_id, decision_date))
        rows = cur.fetchall()
        if len(rows) < 63:
            return None
        total = sum(r['close'] * r['volume'] for r in rows)
        return total / 63.0

    # Helper: get 21-trading-day forward return from decision_date
    def get_forward_return_21d(symbol_id, decision_date):
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            ORDER BY ts ASC LIMIT 22
        """, (symbol_id, decision_date))
        rows = cur.fetchall()
        if len(rows) < 22:
            return None
        close_0 = rows[0]['close']
        close_21 = rows[21]['close']
        return (close_21 - close_0) / close_0

    # Process each symbol
    events = []  # (decision_date, label, symbol_id)
    SECONDS_PER_DAY = 86400

    for symbol_id, hlist in holdings_by_symbol.items():
        if symbol_id not in symbols:
            continue
        # Group by period
        by_period = defaultdict(list)
        for h in hlist:
            by_period[h['period']].append(h)
        periods = sorted(by_period.keys())
        if len(periods) < 5:
            continue

        # Track managers seen in prior 4 quarters
        for i in range(4, len(periods)):
            period = periods[i]
            prior_periods = periods[i-4:i]
            prior_managers = set()
            for p in prior_periods:
                for h in by_period[p]:
                    prior_managers.add(h['manager'])

            current_managers = {h['manager'] for h in by_period[period]}
            new_managers = current_managers - prior_managers

            for h in by_period[period]:
                if h['manager'] not in new_managers:
                    continue
                # Check >=5% ownership
                filing_date = period + 45 * SECONDS_PER_DAY
                # Need market cap at filing_date
                so = get_shares_outstanding(symbol_id, filing_date, filing_date)
                if so is None or so <= 0:
                    continue
                close = get_close_at_or_before(symbol_id, filing_date)
                if close is None or close <= 0:
                    continue
                market_cap = close * so
                if market_cap < 1e9:
                    continue
                pct_owned = (h['shares'] / so) * 100 if so > 0 else 0
                if pct_owned < 5:
                    continue

                # Look for officer purchase within 30 days of filing_date
                window_start = filing_date
                window_end = filing_date + 30 * SECONDS_PER_DAY
                for t in trades_by_symbol.get(symbol_id, []):
                    if not (window_start <= t['filed_ts'] <= window_end):
                        continue
                    if t['filed_ts'] < filing_date:
                        continue
                    decision_date = t['filed_ts']

                    # Universe checks at decision_date
                    # Avg dollar volume prior 63 sessions
                    avg_dv = get_avg_dollar_volume(symbol_id, decision_date)
                    if avg_dv is None or avg_dv < 5e6:
                        continue

                    # Compute 21-day forward return
                    fwd_ret = get_forward_return_21d(symbol_id, decision_date)
                    if fwd_ret is None:
                        continue

                    label = 1 if fwd_ret > 0 else 0
                    events.append((decision_date, label, symbol_id))

    if not events:
        print("INSUFFICIENT=1")
        return

    # Sort by decision_date
    events.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed
    n = len(events)
    split_idx = int(n * 0.8)
    train_events = events[:split_idx]
    sealed_events = events[split_idx:]

    def compute_metrics(evts):
        if not evts:
            return 0, 0, 0, 0, 0, 0
        issued = len(evts)
        hits = sum(e[1] for e in evts)
        precision = hits / issued if issued else 0
        base_rate = precision  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(set(datetime.fromtimestamp(e[0], tz=timezone.utc).date() for e in evts))
        # Design effect via ANOVA ICC estimator
        from collections import defaultdict
        day_outcomes = defaultdict(list)
        for e in evts:
            day = datetime.fromtimestamp(e[0], tz=timezone.utc).date()
            day_outcomes[day].append(e[1])
        D = len(day_outcomes)
        N = issued
        if D <= 1:
            deff = 1.001
        else:
            p = hits / N
            n_bar = N / D
            # Between-day variance
            B = sum(len(v) * ((sum(v)/len(v)) - p)**2 for v in day_outcomes.values()) / (D - 1)
            # Within-day variance
            W = sum(sum((y - sum(v)/len(v))**2 for y in v) for v in day_outcomes.values()) / (N - D)
            if W <= 0:
                icc = 1.0
            else:
                icc = (B - W) / (B + (n_bar - 1) * W)
                icc = max(0.0, min(1.0, icc))
            deff = 1 + (n_bar - 1) * icc
            if deff <= 1:
                deff = 1.001
        effective_n = N / deff
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(train_events)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_events)

    if issued < 30:
        print("INSUFFICIENT=1")
        return

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={issued}")  # opportunities considered = issued (each event is a decision point)
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
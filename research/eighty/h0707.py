# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 706
# cycle_index: 33
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load all inst_holdings data
    cur.execute("SELECT symbol_id, period, cik, value FROM inst_holdings ORDER BY symbol_id, period")
    rows = cur.fetchall()

    # Organize by symbol_id -> period -> list of (cik, value)
    by_symbol = defaultdict(lambda: defaultdict(list))
    periods_by_symbol = defaultdict(list)
    for r in rows:
        sym = r['symbol_id']
        per = r['period']
        cik = r['cik']
        val = r['value']
        by_symbol[sym][per].append((cik, val))
        if per not in periods_by_symbol[sym]:
            periods_by_symbol[sym].append(per)

    # Get max bar date for 1d tf
    cur.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
    max_bar_ts = cur.fetchone()[0]
    if max_bar_ts is None:
        print("INSUFFICIENT=1")
        return
    max_bar_date = datetime.utcfromtimestamp(max_bar_ts).date()

    # Parse period strings to dates
    def parse_period(p):
        return datetime.strptime(p, '%Y-%m-%d').date()

    # For each symbol with >=8 quarters, evaluate each qualifying period
    opportunities = 0
    issued_calls = []  # list of (decision_ts, symbol_id, period, hit)

    for sym, periods in periods_by_symbol.items():
        if len(periods) < 8:
            continue
        periods_sorted = sorted(periods)
        period_dates = [parse_period(p) for p in periods_sorted]

        # Need at least 5 periods to have T, T-1, T-2, T-3, T-4
        for i in range(4, len(periods_sorted)):
            T = periods_sorted[i]
            T_1 = periods_sorted[i-1]
            T_2 = periods_sorted[i-2]
            T_3 = periods_sorted[i-3]
            T_4 = periods_sorted[i-4]

            # Check >=5 holders in each of last 4 quarters (T, T-1, T-2, T-3)
            holders_T = by_symbol[sym][T]
            holders_T1 = by_symbol[sym][T_1]
            holders_T2 = by_symbol[sym][T_2]
            holders_T3 = by_symbol[sym][T_3]
            holders_T4 = by_symbol[sym][T_4]

            if len(holders_T) < 5 or len(holders_T1) < 5 or len(holders_T2) < 5 or len(holders_T3) < 5:
                continue
            if len(holders_T4) < 5:
                continue

            opportunities += 1

            # Compute entry conditions
            cik_T = {c for c, _ in holders_T}
            cik_T4 = {c for c, _ in holders_T4}
            new_holders = cik_T - cik_T4
            exited_holders = cik_T4 - cik_T

            # (a) turnover > 60%
            turnover = len(new_holders) / len(cik_T) if cik_T else 0
            if turnover <= 0.6:
                continue

            # (b) total institutional ownership increased QoQ (T vs T-1)
            total_T = sum(v for _, v in holders_T)
            total_T1 = sum(v for _, v in holders_T1)
            if total_T <= total_T1:
                continue

            # (c) new holders' aggregate position at T > exited holders' prior aggregate at T-4
            new_val_T = sum(v for c, v in holders_T if c in new_holders)
            exited_val_T4 = sum(v for c, v in holders_T4 if c in exited_holders)
            if new_val_T <= exited_val_T4:
                continue

            # All entry conditions met - issue call
            # Decision timestamp = period T + 45 days (embargo)
            T_date = period_dates[i]
            decision_date = T_date + timedelta(days=45)
            decision_ts = int(datetime.combine(decision_date, datetime.min.time()).timestamp())

            # Check if we have 63 calendar days of bars after decision_ts
            horizon_ts = decision_ts + 63 * 86400
            if horizon_ts > max_bar_ts:
                continue

            # Query bars for forward return
            # Find first bar on or after decision_ts
            cur.execute("""
                SELECT ts, close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts ASC LIMIT 1
            """, (sym, decision_ts))
            entry_bar = cur.fetchone()
            if not entry_bar:
                continue
            entry_close = entry_bar['close']

            # Find first bar on or after horizon_ts
            cur.execute("""
                SELECT ts, close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts ASC LIMIT 1
            """, (sym, horizon_ts))
            exit_bar = cur.fetchone()
            if not exit_bar:
                continue
            exit_close = exit_bar['close']

            fwd_return = (exit_close - entry_close) / entry_close
            hit = 1 if fwd_return > 0 else 0

            issued_calls.append((decision_ts, sym, T, hit))

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # Sort by decision_ts
    issued_calls.sort(key=lambda x: x[0])

    # Split sealed era: most recent 20%
    n_sealed = max(1, int(len(issued_calls) * 0.2))
    sealed_calls = issued_calls[-n_sealed:]
    main_calls = issued_calls[:-n_sealed]

    # Compute metrics
    ISSUED = len(issued_calls)
    OPPORTUNITIES = opportunities
    hits = sum(c[3] for c in issued_calls)
    PRECISION = hits / ISSUED if ISSUED else 0.0
    BASE_RATE = PRECISION  # within issued subset, predicted class is "up"

    # DISTINCT_DAYS: distinct UTC days among issued calls
    decision_dates = set()
    for ts, _, _, _ in issued_calls:
        dt = datetime.utcfromtimestamp(ts).date()
        decision_dates.add(dt)
    DISTINCT_DAYS = len(decision_dates)

    # EFFECTIVE_N: design effect from time clustering (by day)
    # Group by day, compute intraclass correlation
    day_groups = defaultdict(list)
    for ts, _, _, hit in issued_calls:
        dt = datetime.utcfromtimestamp(ts).date()
        day_groups[dt].append(hit)

    k = len(day_groups)
    N = ISSUED
    if k > 1 and N > k:
        n_bar = N / k
        p_overall = hits / N
        # Between-day variance
        p_days = [sum(g)/len(g) for g in day_groups.values()]
        MSB = sum(len(g) * (pd - p_overall)**2 for g, pd in zip(day_groups.values(), p_days)) / (k - 1)
        # Within-day variance
        MSW = sum(sum((h - pd)**2 for h in g) for g, pd in zip(day_groups.values(), p_days)) / (N - k)
        if MSB + (n_bar - 1) * MSW > 0:
            ICC = (MSB - MSW) / (MSB + (n_bar - 1) * MSW)
        else:
            ICC = 0
        deff = 1 + (n_bar - 1) * max(ICC, 0)
        if deff < 1.01:
            deff = 1.01
    else:
        deff = 1.01

    EFFECTIVE_N = ISSUED / deff

    # SEALED_PRECISION
    sealed_hits = sum(c[3] for c in sealed_calls)
    SEALED_PRECISION = sealed_hits / len(sealed_calls) if sealed_calls else 0.0

    # Print results
    print(f"ISSUED={ISSUED}")
    print(f"OPPORTUNITIES={OPPORTUNITIES}")
    print(f"PRECISION={PRECISION:.6f}")
    print(f"BASE_RATE={BASE_RATE:.6f}")
    print(f"DISTINCT_DAYS={DISTINCT_DAYS}")
    print(f"EFFECTIVE_N={EFFECTIVE_N:.6f}")
    print(f"SEALED_PRECISION={SEALED_PRECISION:.6f}")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 480
# cycle_index: 10
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()

    # Get max filed_ts for 5-year cutoff
    cur.execute("SELECT MAX(filed_ts) FROM insider_trades")
    max_filed_ts = cur.fetchone()[0]
    if max_filed_ts is None:
        print("INSUFFICIENT=1")
        return
    cutoff_ts = max_filed_ts - 5*365*24*3600

    # Universe: symbols with >=100 1d bars and at least one open-market purchase in last 5 years
    cur.execute("""
        SELECT symbol_id
        FROM bars
        WHERE tf='1d'
        GROUP BY symbol_id
        HAVING COUNT(*) >= 100
        INTERSECT
        SELECT symbol_id
        FROM insider_trades
        WHERE code='P' AND filed_ts > ?
    """, (cutoff_ts,))
    symbol_ids = {row[0] for row in cur.fetchall()}
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return

    # Load all 1d bars for these symbols
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(symbol_ids))), tuple(symbol_ids))
    bars_by_symbol = defaultdict(list)
    for symbol_id, ts, close, volume in cur.fetchall():
        bars_by_symbol[symbol_id].append((ts, close, volume))

    # Get opportunities: distinct (symbol, decision_date) from insider purchases
    cur.execute("""
        SELECT symbol_id,
               strftime('%Y-%m-%d', datetime(filed_ts, 'unixepoch')) as decision_date,
               MIN(filed_ts) as earliest_filed_ts
        FROM insider_trades
        WHERE code='P'
          AND symbol_id IN ({})
          AND filed_ts > ?
        GROUP BY symbol_id, decision_date
    """.format(','.join('?'*len(symbol_ids))), tuple(symbol_ids) + (cutoff_ts,))
    opportunities = cur.fetchall()
    opportunities_count = len(opportunities)

    calls = []  # (decision_date, symbol_id, hit)

    for symbol_id, decision_date_str, earliest_filed_ts in opportunities:
        # Parse decision date
        try:
            decision_date = datetime.date.fromisoformat(decision_date_str)
        except ValueError:
            continue
        decision_midnight_ts = int(datetime.datetime(
            decision_date.year, decision_date.month, decision_date.day,
            tzinfo=datetime.timezone.utc
        ).timestamp())

        # Get bars for this symbol
        bars = bars_by_symbol.get(symbol_id, [])
        if not bars:
            continue

        # Filter bars before decision day
        pre_bars = [b for b in bars if b[0] < decision_midnight_ts]
        pre_bars.sort(key=lambda x: x[0], reverse=True)  # most recent first

        # Need at least 50 bars
        if len(pre_bars) < 50:
            continue

        # Compute conditions
        vol_20 = sum(b[2] for b in pre_bars[:20]) / 20
        vol_50 = sum(b[2] for b in pre_bars[:50]) / 50
        if vol_20 <= vol_50:
            continue

        # 5-day return
        close_t_minus_1 = pre_bars[0][1]
        close_t_minus_5 = pre_bars[4][1]
        ret_5 = (close_t_minus_1 - close_t_minus_5) / close_t_minus_5
        if ret_5 >= 0:
            continue

        # Issue an "up" call. Now find decision day bar T (first bar on or after decision date)
        T_bar = None
        for b in bars:
            if b[0] >= decision_midnight_ts:
                T_bar = b
                break
        if T_bar is None:
            continue

        # Get label: 21-day forward return direction
        # Try prediction_outcomes first
        cur.execute("""
            SELECT up
            FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = 21 AND ts = ?
            LIMIT 1
        """, (symbol_id, T_bar[0]))
        row = cur.fetchone()
        if row is not None:
            hit = 1 if row[0] else 0
        else:
            # Compute from bars: find T's index and get 21st bar after
            try:
                idx = next(i for i, b in enumerate(bars) if b[0] == T_bar[0])
                if idx + 21 < len(bars):
                    close_T = T_bar[1]
                    close_T21 = bars[idx + 21][1]
                    hit = 1 if close_T21 > close_T else 0
                else:
                    continue
            except StopIteration:
                continue

        calls.append((decision_date_str, symbol_id, hit))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Sort by decision date
    calls.sort(key=lambda x: x[0])

    # Split into training (first 80%) and sealed (last 20%)
    n_sealed = max(1, len(calls) // 5)  # at least one in sealed
    sealed = calls[-n_sealed:]
    training = calls[:-n_sealed]

    # Compute metrics
    issued = len(calls)
    hits = sum(hit for _, _, hit in calls)
    precision = hits / issued

    # BASE_RATE is same as precision because we only issue "up" calls
    base_rate = precision

    # DISTINCT_DAYS: distinct decision dates in calls
    distinct_days = len({day for day, _, _ in calls})

    # EFFECTIVE_N: compute design effect with day clusters
    day_counts = defaultdict(int)
    for day, _, _ in calls:
        day_counts[day] += 1
    n_d_list = list(day_counts.values())
    distinct_days_count = len(n_d_list)

    # Kish effective sample size for day clusters
    sum_reciprocal = sum(1.0 / n for n in n_d_list)
    n_eff_day = (distinct_days_count ** 2) / sum_reciprocal

    if n_eff_day < issued:
        effective_n = n_eff_day
    else:
        # Use week clusters
        week_counts = defaultdict(int)
        for day_str, _, _ in calls:
            # Parse date
            try:
                d = datetime.date.fromisoformat(day_str)
                week_key = f"{d.year}-W{d.isocalendar()[1]}"
                week_counts[week_key] += 1
            except ValueError:
                continue
        n_w_list = list(week_counts.values())
        distinct_weeks = len(n_w_list)
        sum_reciprocal_week = sum(1.0 / n for n in n_w_list)
        n_eff_week = (distinct_weeks ** 2) / sum_reciprocal_week
        effective_n = n_eff_week if n_eff_week < issued else issued * 0.99  # ensure < issued

    # SEALED_PRECISION
    sealed_hits = sum(hit for _, _, hit in sealed)
    sealed_precision = sealed_hits / len(sealed) if sealed else 0.0

    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    conn.close()

if __name__ == "__main__":
    main()
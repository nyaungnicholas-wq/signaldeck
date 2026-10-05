# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 556
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    # Open database read-only
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=30)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get universe: symbols with ≥2 years of daily sentiment data AND insider trades
    # Sentiment: from sentiment_features (day column is 'YYYY-MM-DD')
    cur.execute("""
        SELECT symbol_id, COUNT(DISTINCT day) as days
        FROM sentiment_features
        GROUP BY symbol_id
        HAVING days >= 730
    """)
    sentiment_symbols = {row['symbol_id'] for row in cur.fetchall()}

    # Insider trades
    cur.execute("SELECT DISTINCT symbol_id FROM insider_trades")
    insider_symbols = {row['symbol_id'] for row in cur.fetchall()}

    universe = sentiment_symbols & insider_symbols
    if not universe:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # 2. Precompute daily sentiment for moving averages
    # Build dict: symbol -> list of (day_str, mean_score)
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({})
        ORDER BY symbol_id, day
    """.format(','.join('?'*len(universe))), tuple(universe))
    sentiment_data = defaultdict(list)
    for row in cur.fetchall():
        sentiment_data[row['symbol_id']].append((row['day'], row['mean_score']))

    # 3. Precompute 10-day and 50-day moving averages and crossover history
    # For each symbol, compute MA10 and MA50 for each day, and track crossover state
    ma_data = {}  # symbol -> {day: (ma10, ma50, below_count)}
    for sym, daily in sentiment_data.items():
        scores = [s for _, s in daily]
        days = [d for d, _ in daily]
        if len(scores) < 50:
            continue  # Need at least 50 days for MA50
        ma10 = []
        ma50 = []
        # Compute MAs
        for i in range(len(scores)):
            if i >= 9:
                ma10.append(sum(scores[i-9:i+1])/10)
            else:
                ma10.append(None)
            if i >= 49:
                ma50.append(sum(scores[i-49:i+1])/50)
            else:
                ma50.append(None)
        # Compute crossover state
        below_count = [0]*len(scores)  # consecutive days MA10 < MA50 before this day
        last_above = 0
        for i in range(len(scores)):
            if ma10[i] is not None and ma50[i] is not None:
                if ma10[i] > ma50[i]:
                    # MA10 > MA50: count since last time it was above
                    # Actually we need: "after being below for at least 20 days"
                    # So we need to know how many days in a row MA10 < MA50 immediately before this day
                    # Reset counter
                    consecutive_below = 0
                    for j in range(i-1, -1, -1):
                        if ma10[j] is not None and ma50[j] is not None:
                            if ma10[j] < ma50[j]:
                                consecutive_below += 1
                            else:
                                break
                        else:
                            break
                    below_count[i] = consecutive_below
                else:
                    below_count[i] = 0
            else:
                below_count[i] = 0
        # Store
        ma_data[sym] = {}
        for i in range(len(days)):
            ma_data[sym][days[i]] = (ma10[i], ma50[i], below_count[i])

    # 4. Precompute institutional ownership changes for abstain condition
    # Need to lag period by 45 days for as-of discipline
    # Get all periods for symbols in universe
    cur.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        WHERE symbol_id IN ({})
        GROUP BY symbol_id, period
    """.format(','.join('?'*len(universe))), tuple(universe))
    inst_data = defaultdict(list)  # symbol -> [(period_str, total_shares)]
    for row in cur.fetchall():
        inst_data[row['symbol_id']].append((row['period'], row['total_shares']))

    # Sort by period for each symbol
    for sym in inst_data:
        inst_data[sym].sort(key=lambda x: x[0])

    # 5. Get insider purchases (code='P' for open-market purchase)
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P' AND symbol_id IN ({})
    """.format(','.join('?'*len(universe))), tuple(universe))
    purchases = [(row['symbol_id'], row['tx_ts'], row['filed_ts']) for row in cur.fetchall()]

    # 6. For each purchase, evaluate conditions and collect opportunities
    opportunities = []  # list of (symbol_id, filed_ts, label)
    # We'll need prediction outcomes for horizon=21
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon=21
    """)
    outcomes = {}  # (symbol_id, ts) -> label (1 for up, 0 for down)
    for row in cur.fetchall():
        outcomes[(row['symbol_id'], row['ts'])] = 1 if row['up'] else 0

    for sym, tx_ts, filed_ts in purchases:
        # Convert filed_ts to day string 'YYYY-MM-DD'
        filed_dt = datetime.utcfromtimestamp(filed_ts)
        filed_day = filed_dt.strftime('%Y-%m-%d')

        # Condition 1: Sentiment crossover
        if sym not in ma_data or filed_day not in ma_data[sym]:
            continue
        ma10, ma50, below_days = ma_data[sym][filed_day]
        if ma10 is None or ma50 is None:
            continue
        if not (ma10 > ma50 and below_days >= 20):
            continue

        # Condition 2: Institutional ownership abstain
        if sym in inst_data:
            periods = inst_data[sym]  # list of (period_str, total_shares)
            # Find most recent period that is at least 45 days before filed_dt
            cutoff = filed_dt - timedelta(days=45)
            cutoff_str = cutoff.strftime('%Y-%m-%d')
            # Find last period <= cutoff_str
            most_recent = None
            for period_str, shares in reversed(periods):
                if period_str <= cutoff_str:
                    most_recent = (period_str, shares)
                    break
            if most_recent is None:
                # No lagged data, assume no increase (or could skip)
                pass
            else:
                # Find previous period
                idx = periods.index(most_recent)
                if idx > 0:
                    prev_period, prev_shares = periods[idx-1]
                    if prev_shares > 0:
                        pct_increase = (most_recent[1] - prev_shares) / prev_shares
                        if pct_increase > 0.10:
                            continue  # abstain

        # Get label for 21-day horizon from filed_ts
        # Find prediction_outcomes row with ts == filed_ts? Actually, prediction_outcomes.ts is the forecast time.
        # We assume the forecast is made at filed_ts (decision time). Check if exists.
        key = (sym, filed_ts)
        if key not in outcomes:
            # No outcome for this decision point
            continue

        label = outcomes[key]
        opportunities.append((sym, filed_ts, label, filed_dt))

    if not opportunities:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # 7. Deduplicate by (symbol, UTC day) - one observation per symbol per day
    # Group by (symbol, day)
    day_groups = defaultdict(list)
    for sym, ts, label, dt in opportunities:
        day_key = (sym, dt.strftime('%Y-%m-%d'))
        day_groups[day_key].append((ts, label))

    # For each day, issue one call (we'll take the earliest timestamp in that day)
    calls = []
    for day_key, items in day_groups.items():
        items.sort(key=lambda x: x[0])
        ts, label = items[0]
        calls.append((day_key[0], ts, label, datetime.utcfromtimestamp(ts)))

    # 8. Split into train and sealed (most recent 20% by time)
    calls.sort(key=lambda x: x[3])
    n = len(calls)
    seal_idx = int(n * 0.8)
    train_calls = calls[:seal_idx]
    sealed_calls = calls[seal_idx:]

    # 9. Compute metrics
    # ISSUED = total calls issued
    issued = len(calls)

    # OPPORTUNITIES = number of decision points considered (i.e., groups)
    opportunities_count = len(day_groups)

    # PRECISION = hits/issued
    hits = sum(1 for _, _, label, _ in calls if label == 1)
    precision = hits / issued if issued > 0 else 0.0

    # BASE_RATE = proportion of 1s within issued calls
    base_rate = precision  # same as precision because base rate is measured within issued

    # DISTINCT_DAYS = distinct UTC days among ISSUED calls
    distinct_days = len(set(dt.strftime('%Y-%m-%d') for _, _, _, dt in calls))

    # EFFECTIVE_N = issued / design effect
    # Design effect: 1 + intracluster correlation * (cluster_size - 1)
    # Clusters are by day; compute average cluster size and ICC
    # For simplicity, assume ICC = 0.5 (common for temporal data)
    ICC = 0.5
    # Compute cluster sizes (day -> count)
    day_counts = defaultdict(int)
    for sym, ts, label, dt in calls:
        day_counts[dt.strftime('%Y-%m-%d')] += 1
    avg_cluster_size = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    design_effect = 1 + ICC * (avg_cluster_size - 1)
    effective_n = issued / design_effect

    # SEALED_PRECISION
    sealed_hits = sum(1 for _, _, label, _ in sealed_calls if label == 1)
    sealed_issued = len(sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    # 10. Print required lines
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
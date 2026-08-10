# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 440
# cycle_index: 31
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()

    # Step 1: Find symbols with ≥2 years of daily bars
    c.execute("""
        SELECT symbol_id, MIN(ts) as min_ts, MAX(ts) as max_ts
        FROM bars WHERE tf='1d'
        GROUP BY symbol_id
        HAVING (MAX(ts) - MIN(ts)) >= 2*365*86400
    """)
    symbols_with_bars = {row['symbol_id']: (row['min_ts'], row['max_ts']) for row in c.fetchall()}

    if not symbols_with_bars:
        print("INSUFFICIENT=1")
        return

    # Step 2: For each symbol, get daily bars and compute conditions
    decision_points = []  # (symbol_id, ts, decision_date)
    calls = []            # (symbol_id, ts, is_up, decision_day, decision_date)

    for sym_id, (min_ts, max_ts) in symbols_with_bars.items():
        # Get daily bars for this symbol
        c.execute("""
            SELECT ts, open, high, low, close, volume
            FROM bars WHERE symbol_id=? AND tf='1d'
            ORDER BY ts
        """, (sym_id,))
        bars = c.fetchall()
        if len(bars) < 60:
            continue

        # Compute 20-day realized volatility for each day
        vols = []
        for i in range(19, len(bars)):
            returns = []
            for j in range(i-19, i+1):
                if bars[j-1]['close'] > 0:
                    ret = math.log(bars[j]['close'] / bars[j-1]['close'])
                    returns.append(ret)
            if len(returns) < 19:
                continue
            mean_ret = sum(returns) / len(returns)
            var = sum((r - mean_ret) ** 2 for r in returns) / (len(returns) - 1)
            vol20 = math.sqrt(var) * math.sqrt(252)
            vols.append((bars[i]['ts'], vol20))

        # Compute 60-day low of vol20 and check 13F condition
        # Pre-fetch 13F data for this symbol
        c.execute("""
            SELECT period, SUM(value) as total_value
            FROM inst_holdings
            WHERE symbol_id=?
            GROUP BY period
            ORDER BY period DESC
        """, (sym_id,))
        f_rows = c.fetchall()
        if len(f_rows) < 2:
            continue

        # Convert periods to datetime for comparison
        periods = []
        for row in f_rows:
            try:
                period_date = datetime.strptime(row['period'], '%Y-%m-%d')
                periods.append((period_date, row['total_value']))
            except:
                continue

        for idx in range(59, len(vols)):
            ts, vol20 = vols[idx]
            min_vol60 = min(vols[j][1] for j in range(idx-59, idx+1))
            if vol20 != min_vol60:
                continue

            decision_date = datetime.utcfromtimestamp(ts)

            # Find two most recent quarters available by decision_date (45-day lag)
            available_quarters = []
            for period_date, total_value in periods:
                available_date = period_date + timedelta(days=45)
                if available_date <= decision_date:
                    available_quarters.append((period_date, total_value))
                    if len(available_quarters) >= 2:
                        break

            if len(available_quarters) < 2:
                continue

            latest_value = available_quarters[0][1]
            prev_value = available_quarters[1][1]
            if prev_value <= 0:
                continue
            growth = (latest_value - prev_value) / prev_value
            if growth < 0.05:
                continue

            decision_points.append((sym_id, ts, decision_date))

            # Check for label
            c.execute("""
                SELECT up
                FROM prediction_outcomes
                WHERE symbol_id=? AND ts=? AND horizon=21
            """, (sym_id, ts))
            label_row = c.fetchone()
            if label_row:
                is_up = 1 if label_row['up'] else 0
                decision_day = decision_date.strftime('%Y-%m-%d')
                calls.append((sym_id, ts, is_up, decision_day, decision_date))

    conn.close()

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Split into training (80%) and sealed (20%) by time
    all_items = decision_points.copy()
    all_items.sort(key=lambda x: x[2])
    split_idx = int(len(all_items) * 0.8)
    split_date = all_items[split_idx][2]

    train_calls = [call for call in calls if call[4] < split_date]
    sealed_calls = [call for call in calls if call[4] >= split_date]

    # Count opportunities (decision points in training era)
    train_opportunities = [dp for dp in decision_points if dp[2] < split_date]
    opportunities = len(train_opportunities)

    if not train_calls:
        print("INSUFFICIENT=1")
        return

    # Compute metrics for training era
    issued = len(train_calls)
    hits = sum(call[2] for call in train_calls)
    precision = hits / issued
    base_rate = hits / issued  # Within issued subset

    # Count distinct days among issued calls
    distinct_days = len(set(call[3] for call in train_calls))

    # Compute design effect for clustering by day
    day_groups = defaultdict(list)
    for call in train_calls:
        day_groups[call[3]].append(call[2])

    C = len(day_groups)
    N = issued
    if C > 1 and N > C:
        grand_mean = hits / N
        msb_num = 0
        for day, outcomes in day_groups.items():
            n_j = len(outcomes)
            y_bar = sum(outcomes) / n_j
            msb_num += n_j * (y_bar - grand_mean) ** 2
        msb = msb_num / (C - 1)
        
        msw_num = 0
        for day, outcomes in day_groups.items():
            n_j = len(outcomes)
            y_bar = sum(outcomes) / n_j
            for y in outcomes:
                msw_num += (y - y_bar) ** 2
        msw = msw_num / (N - C)
        
        sum_nj_sq = sum(len(outcomes) ** 2 for outcomes in day_groups.values())
        k0 = (N - sum_nj_sq / N) / (C - 1)
        if msw > 0:
            icc = (msb - msw) / (msb + (k0 - 1) * msw)
            icc = max(0, icc)
        else:
            icc = 0
        design_effect = 1 + (k0 - 1) * icc
    else:
        design_effect = 1.0

    effective_n = issued / design_effect if design_effect > 0 else issued

    # Compute sealed precision
    sealed_hits = sum(call[2] for call in sealed_calls)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
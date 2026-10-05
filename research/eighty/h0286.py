# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 285
# cycle_index: 8
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta

DB_PATH = 'data/signaldeck.db'
conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
conn.row_factory = sqlite3.Row

# Get all symbols with 13F data and StockTwits history, daily bars since 2018
symbol_sql = """
SELECT DISTINCT inst.symbol_id
FROM inst_holdings inst
JOIN (SELECT symbol_id, MIN(ts) as min_ts FROM bars WHERE tf='1d' GROUP BY symbol_id
      HAVING MIN(ts) <= 1532611200) bars ON bars.symbol_id = inst.symbol_id
JOIN stocktwits_sentiment st ON st.symbol_id = inst.symbol_id
"""
c = conn.cursor()
c.execute(symbol_sql)
symbols = [row['symbol_id'] for row in c.fetchall()]

if not symbols:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Prepare structures to hold opportunities and calls
opportunities = []  # (symbol_id, decision_ts, is_call, label)
labels = {}

# For each symbol, process
for sym in symbols:
    # Get 13F periods aggregated by symbol and period, with previous period comparison
    c.execute("""
        WITH periods AS (
            SELECT symbol_id, period, SUM(value) as total_value,
                   LAG(SUM(value)) OVER (PARTITION BY symbol_id ORDER BY period) as prev_value
            FROM inst_holdings
            WHERE symbol_id = ?
            GROUP BY symbol_id, period
        )
        SELECT period, total_value, prev_value,
               CASE WHEN prev_value > 0 THEN (total_value - prev_value) / prev_value ELSE NULL END as pct_change
        FROM periods
        WHERE prev_value IS NOT NULL
        ORDER BY period
    """, (sym,))
    period_data = c.fetchall()
    
    if not period_data:
        continue

    # Get all StockTwits data for this symbol
    c.execute("""
        SELECT ts, bullish, bearish
        FROM stocktwits_sentiment
        WHERE symbol_id = ?
        ORDER BY ts
    """, (sym,))
    st_data = c.fetchall()
    if not st_data:
        continue

    # Get all daily bars for this symbol
    c.execute("""
        SELECT ts
        FROM bars
        WHERE symbol_id = ? AND tf='1d'
        ORDER BY ts
    """, (sym,))
    bar_ts_list = [row['ts'] for row in c.fetchall()]
    if not bar_ts_list:
        continue

    # Convert period strings to timestamps
    period_ts_list = []
    for row in period_data:
        period_str = row['period']
        try:
            # Assume period is YYYY-MM-DD
            dt = datetime.strptime(period_str, "%Y-%m-%d")
            period_ts = int(dt.timestamp())
            period_ts_list.append((period_ts, row['pct_change']))
        except:
            continue

    # Process each period for decision
    for period_ts, pct_change in period_ts_list:
        # Decision date = period + 45 days
        decision_dt = datetime.utcfromtimestamp(period_ts) + timedelta(days=45)
        decision_ts = int(decision_dt.timestamp())
        
        # Check if we have a bar on or after decision_ts (next trading day)
        next_bar_idx = None
        for i, ts in enumerate(bar_ts_list):
            if ts >= decision_ts:
                next_bar_idx = i
                break
        if next_bar_idx is None:
            continue
        actual_decision_ts = bar_ts_list[next_bar_idx]
        
        # Check if 13F data is stale (>135 days since period end)
        if (actual_decision_ts - period_ts) > 135 * 86400:
            continue

        # Find the index of actual_decision_ts in bar_ts_list
        decision_bar_idx = bar_ts_list.index(actual_decision_ts)
        
        # Need at least 252 trading days before decision
        if decision_bar_idx < 252:
            continue
        
        # Get bullish/bearish ratios for last 252 days
        # We have st_data sorted by ts, and bar_ts_list is sorted
        # We need to match StockTwits to trading days
        # Build a list of (day_ts, ratio) for each trading day using most recent StockTwits
        last_252_bar_ts = bar_ts_list[decision_bar_idx-251:decision_bar_idx+1]  # 252 days inclusive
        
        # Map each bar_ts to the latest StockTwits data up to that bar_ts
        ratios = []
        st_idx = 0
        for bar_ts in last_252_bar_ts:
            # Advance st_idx to the last entry with ts <= bar_ts
            while st_idx < len(st_data) and st_data[st_idx]['ts'] <= bar_ts:
                st_idx += 1
            if st_idx == 0:
                # No data before this bar
                continue
            # Use the last entry at or before bar_ts
            st = st_data[st_idx - 1]
            bullish = st['bullish']
            bearish = st['bearish']
            if bearish == 0:
                ratio = 1000.0  # large number for division by zero
            else:
                ratio = bullish / bearish
            ratios.append(ratio)
        
        if len(ratios) < 252:
            continue
        
        # Compute 10th percentile
        sorted_ratios = sorted(ratios)
        idx10 = int(0.1 * (len(sorted_ratios) - 1))
        idx10_frac = idx10 % 1
        percentile10 = sorted_ratios[int(idx10)] * (1 - idx10_frac) + sorted_ratios[int(idx10) + 1] * idx10_frac

        # Get the ratio on decision day (the last ratio computed)
        current_ratio = ratios[-1]
        
        # Check conditions
        if pct_change >= 0.05 and current_ratio <= percentile10:
            # Now look for label: next 21 trading days
            if decision_bar_idx + 21 >= len(bar_ts_list):
                continue
            future_ts = bar_ts_list[decision_bar_idx + 21]
            
            # Get prediction_outcomes for this symbol and horizon=21
            c.execute("""
                SELECT up
                FROM prediction_outcomes
                WHERE symbol_id = ? AND horizon = 21 AND ts >= ?
                ORDER BY ts
                LIMIT 1
            """, (sym, future_ts))
            outcome = c.fetchone()
            if outcome is None:
                continue
            label = 1 if outcome['up'] else 0
            
            opportunities.append((sym, actual_decision_ts, True, label))
        else:
            opportunities.append((sym, actual_decision_ts, False, None))

conn.close()

if not opportunities:
    print("INSUFFICIENT=1")
    exit(0)

# Split into train and sealed (most recent 20% by time)
all_decision_ts = [op[1] for op in opportunities]
max_ts = max(all_decision_ts)
min_ts = min(all_decision_ts)
threshold_ts = min_ts + 0.8 * (max_ts - min_ts)

issued = [op for op in opportunities if op[2]]
train_issued = [op for op in issued if op[1] <= threshold_ts]
sealed_issued = [op for op in issued if op[1] > threshold_ts]
train_opportunities = [op for op in opportunities if op[1] <= threshold_ts]
sealed_opportunities = [op for op in opportunities if op[1] > threshold_ts]

# Compute metrics for train set
if not issued:
    print("INSUFFICIENT=1")
    exit(0)

total_issued = len(issued)
total_opportunities = len(opportunities)

# Base rate: proportion of up=1 in issued subset
hits = sum(op[3] for op in issued)
precision = hits / total_issued
base_rate = hits / total_issued  # same as precision because hits/issued is the base rate within issued

# Distinct days among issued calls
distinct_days = len(set(op[1] for op in issued))

# Design effect and effective N
# Cluster by day: each day has multiple calls
day_counts = {}
day_hits = {}
for op in issued:
    day_ts = op[1]
    day_counts[day_ts] = day_counts.get(day_ts, 0) + 1
    day_hits[day_ts] = day_hits.get(day_ts, 0) + (1 if op[3] == 1 else 0)

n_days = len(day_counts)
if n_days == 0:
    design_effect = 1
else:
    # Compute within-day variance and between-day variance
    # Overall proportion
    p_overall = hits / total_issued
    # Variance of overall
    var_overall = p_overall * (1 - p_overall)
    
    # Between-day variance
    between_var = 0
    for day, cnt in day_counts.items():
        p_day = day_hits[day] / cnt if cnt > 0 else 0
        between_var += cnt * (p_day - p_overall) ** 2
    between_var /= (n_days - 1) if n_days > 1 else 1
    
    # ICC = between_var / var_overall if var_overall > 0 else 0
    icc = between_var / var_overall if var_overall > 0 else 0
    avg_cluster_size = total_issued / n_days
    design_effect = 1 + (avg_cluster_size - 1) * icc

effective_n = total_issued / design_effect

# Compute sealed precision
sealed_hits = sum(op[3] for op in sealed_issued) if sealed_issued else 0
sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0

print(f"ISSUED={total_issued}")
print(f"OPPORTUNITIES={total_opportunities}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.4f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")
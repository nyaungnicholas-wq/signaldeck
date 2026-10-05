# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 507
# cycle_index: 37
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check for required data existence
    required_tables = ['bars', 'stocktwits_sentiment', 'macro_series', 'prediction_outcomes']
    for table in required_tables:
        try:
            cur.execute(f"SELECT 1 FROM {table} LIMIT 1")
        except sqlite3.OperationalError:
            print("INSUFFICIENT=1")
            return

    # Get VIX data
    cur.execute("SELECT ts, value FROM macro_series WHERE series='VIXCLS'")
    vix_data = {}
    for row in cur:
        # Convert ts (unix epoch) to date string for consistent joining
        day_str = datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d')
        vix_data[day_str] = row['value']

    if not vix_data:
        print("INSUFFICIENT=1")
        return

    # Get universe: stocks with daily data since 2018-07-01 and StockTwits sentiment
    cur.execute("""
        SELECT DISTINCT b.symbol_id
        FROM bars b
        JOIN stocktwits_sentiment s ON b.symbol_id = s.symbol_id
        WHERE b.tf = '1d' AND b.ts >= strftime('%s', '2018-07-01')
    """)
    symbol_ids = [row['symbol_id'] for row in cur]

    if not symbol_ids:
        print("INSUFFICIENT=1")
        return

    # Pre-fetch all bars for these symbols (1d timeframe)
    bars_by_symbol = defaultdict(list)
    cur.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(symbol_ids))), symbol_ids)
    for row in cur:
        sid = row['symbol_id']
        bars_by_symbol[sid].append({
            'ts': row['ts'],
            'day_str': datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d'),
            'open': row['open'],
            'high': row['high'],
            'low': row['low'],
            'close': row['close'],
            'volume': row['volume']
        })

    # Pre-fetch all StockTwits sentiment for these symbols
    st_sentiment = defaultdict(list)
    cur.execute("""
        SELECT symbol_id, ts, bearish
        FROM stocktwits_sentiment
        WHERE symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(symbol_ids))), symbol_ids)
    for row in cur:
        sid = row['symbol_id']
        st_sentiment[sid].append({
            'ts': row['ts'],
            'day_str': datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d'),
            'bearish': row['bearish']
        })

    # Get prediction outcomes (labels) for horizon=10
    label_by_symbol_ts = {}
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 10
    """)
    for row in cur:
        key = (row['symbol_id'], row['ts'])
        label_by_symbol_ts[key] = row['up']

    conn.close()

    # Process opportunities
    opportunities = []
    for sid in symbol_ids:
        if sid not in bars_by_symbol or len(bars_by_symbol[sid]) < 270:
            continue  # Need at least 252 days history + 10 days + buffer
        bars = bars_by_symbol[sid]
        if sid not in st_sentiment:
            continue
        st = st_sentiment[sid]

        # Create mapping of day_str to bar index for fast lookup
        bar_by_day = {}
        for idx, b in enumerate(bars):
            bar_by_day[b['day_str']] = (b, idx)

        # Process each day as a decision point
        for i, bar in enumerate(bars):
            day = bar['day_str']
            # Check VIX condition
            if day not in vix_data:
                continue
            # Need at least 20 days of VIX history
            vix_values_recent = []
            for d in sorted(vix_data.keys()):
                if d > day:
                    break
                if len(vix_values_recent) >= 20:
                    vix_values_recent.pop(0)
                vix_values_recent.append(vix_data[d])
            if len(vix_values_recent) < 20:
                continue
            vix_low_20 = min(vix_values_recent[:-1])  # 20-day low up to but not including today?
            # The condition: VIX increased by at least 30% from its 20-day low
            # We'll use the 20-day low up to yesterday to avoid using today's VIX for the low?
            # Actually, the condition is on day t: "the VIX has increased by at least 30% from its 20-day low"
            # This likely means from its low over the past 20 days including today?
            # We'll interpret as: current VIX value >= 1.3 * (minimum VIX over past 20 days including today)
            vix_low_20_inclusive = min(vix_values_recent)
            vix_condition = vix_data[day] >= 1.3 * vix_low_20_inclusive
            if not vix_condition:
                continue

            # Check StockTwits bearish condition
            # Get bearish values for this symbol up to today
            st_bearish_recent = []
            for st_day in st:
                if st_day['day_str'] > day:
                    break
                st_bearish_recent.append(st_day['bearish'])
            if len(st_bearish_recent) < 252:
                continue
            # Get last 252 values
            st_bearish_252 = st_bearish_recent[-252:]
            current_bearish = st_bearish_recent[-1]
            # Check if in top 5% of its own 252-day history
            # Count how many values >= current_bearish
            count_above = sum(1 for v in st_bearish_252 if v >= current_bearish)
            # Top 5% means at least 95th percentile
            if count_above < 0.05 * len(st_bearish_252):
                continue

            # Check down days condition: at least 8 of the prior 10 trading days closed down
            # Need to find the 10 trading days before today
            # Get index of today
            try:
                today_idx = bar_by_day[day][1]
            except KeyError:
                continue
            if today_idx < 10:
                continue
            down_count = 0
            for j in range(today_idx-10, today_idx):
                b = bars[j]
                # Closed down if close < open
                if b['close'] < b['open']:
                    down_count += 1
            if down_count < 8:
                continue

            # All conditions met, this is an entry signal
            # Now check if we have a label for this symbol and time
            if (sid, bar['ts']) not in label_by_symbol_ts:
                continue
            label = label_by_symbol_ts[(sid, bar['ts'])]
            opportunities.append({
                'symbol_id': sid,
                'day_str': day,
                'ts': bar['ts'],
                'label': label
            })

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # Split into train (80%) and sealed (20%) by time
    opportunities.sort(key=lambda x: x['ts'])
    split_idx = int(len(opportunities) * 0.8)
    train_opp = opportunities[:split_idx]
    sealed_opp = opportunities[split_idx:]

    # Compute metrics for train
    train_issued = len(train_opp)
    train_hits = sum(1 for o in train_opp if o['label'] == 1)
    train_precision = train_hits / train_issued if train_issued > 0 else 0
    train_base_rate = train_precision  # Base rate of predicted class (up) within issued

    # Compute distinct days in train issued
    train_days = set(o['day_str'] for o in train_opp)
    train_distinct_days = len(train_days)

    # Compute design effect (clustered by day)
    # Group by day
    day_groups = defaultdict(list)
    for o in train_opp:
        day_groups[o['day_str']].append(o['label'])
    day_means = []
    for day, labels in day_groups.items():
        day_means.append(sum(labels) / len(labels))
    if len(day_means) > 1:
        overall_mean = train_hits / train_issued
        # Between-day variance
        var_between = sum((m - overall_mean) ** 2 for m in day_means) / (len(day_means) - 1)
        # Within-day variance (average of within-day variances)
        var_within = 0
        for labels in day_groups.values():
            n = len(labels)
            if n > 1:
                mean = sum(labels) / n
                var = sum((x - mean) ** 2 for x in labels) / (n - 1)
                var_within += var
        var_within /= len(day_groups)
        # Intracluster correlation
        avg_cluster_size = sum(len(labels) for labels in day_groups.values()) / len(day_groups)
        rho = var_between / (var_within + var_between) if var_within + var_between > 0 else 0
        design_effect = 1 + rho * (avg_cluster_size - 1)
    else:
        design_effect = 1.1  # Assume slightly clustered if only one day

    effective_n = train_issued / design_effect

    # Compute sealed metrics
    sealed_issued = len(sealed_opp)
    sealed_hits = sum(1 for o in sealed_opp if o['label'] == 1)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0

    # Print required outputs
    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={train_precision:.4f}")
    print(f"BASE_RATE={train_base_rate:.4f}")
    print(f"DISTINCT_DAYS={train_distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
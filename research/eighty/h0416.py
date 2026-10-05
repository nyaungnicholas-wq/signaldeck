# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 415
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
from datetime import datetime, timezone
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 63
MIN_QUARTERS = 5

conn = sqlite3.connect(DB_PATH, uri=True, timeout=10)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

# Step 1: Get all EPS fundamentals
cur.execute("""
    SELECT symbol_id, value, as_of, fetched_at
    FROM fundamentals
    WHERE metric = 'EPS'
    ORDER BY symbol_id, as_of
""")
eps_rows = cur.fetchall()

# Group by symbol
symbol_eps = defaultdict(list)
for row in eps_rows:
    symbol_eps[row['symbol_id']].append({
        'value': row['value'],
        'as_of': row['as_of'],
        'fetched_at': row['fetched_at']
    })

# Step 2: Find EPS flip events
events = []
for symbol_id, quarters in symbol_eps.items():
    if len(quarters) < MIN_QUARTERS:
        continue
    # Check for last quarter positive with four prior negative
    if quarters[-1]['value'] <= 0:
        continue
    if any(q['value'] > 0 for q in quarters[-5:-1]):
        continue
    events.append({
        'symbol_id': symbol_id,
        'decision_date': quarters[-1]['fetched_at'],
        'eps_value': quarters[-1]['value']
    })

if not events:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Step 3: Get 20-day average dollar volume for each event
events_with_vol = []
for event in events:
    sym = event['symbol_id']
    dec_date = event['decision_date']
    
    # Get bars before decision date
    cur.execute("""
        SELECT ts, close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts < ?
        ORDER BY ts DESC
        LIMIT 20
    """, (sym, dec_date))
    bars = cur.fetchall()
    
    if len(bars) < 20:
        continue  # Need full 20 days
    
    # Calculate average dollar volume
    dollar_vols = [b['close'] * b['volume'] for b in bars]
    avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
    event['avg_dollar_vol'] = avg_dollar_vol
    events_with_vol.append(event)

if not events_with_vol:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Step 4: Calculate universe median at each decision date
date_groups = defaultdict(list)
for event in events_with_vol:
    date_groups[event['decision_date']].append(event)

final_events = []
for date, evts in date_groups.items():
    # Get all symbols with bars on this date
    cur.execute("""
        SELECT DISTINCT symbol_id
        FROM bars
        WHERE tf = '1d' AND ts < ?
    """, (date,))
    universe_syms = [r['symbol_id'] for r in cur.fetchall()]
    
    if len(universe_syms) < 10:
        continue
    
    # Calculate 20-day avg dollar volume for all universe symbols
    vol_percentiles = []
    for sym in universe_syms:
        cur.execute("""
            SELECT close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts < ?
            ORDER BY ts DESC
            LIMIT 20
        """, (sym, date))
        bars = cur.fetchall()
        if len(bars) < 20:
            continue
        dollar_vols = [b['close'] * b['volume'] for b in bars]
        vol_percentiles.append(sum(dollar_vols) / len(dollar_vols))
    
    if len(vol_percentiles) < 10:
        continue
    
    # Find median
    vol_percentiles.sort()
    median_idx = len(vol_percentiles) // 2
    median_vol = vol_percentiles[median_idx]
    
    # Filter events below median
    for event in evts:
        if event['avg_dollar_vol'] < median_vol:
            final_events.append(event)

if not final_events:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Step 5: Get labels from prediction_outcomes
labeled_events = []
for event in final_events:
    sym = event['symbol_id']
    dec_date = event['decision_date']
    
    cur.execute("""
        SELECT up, fwd_return
        FROM prediction_outcomes
        WHERE symbol_id = ? AND horizon = ? AND ts = ?
        LIMIT 1
    """, (sym, HORIZON, dec_date))
    label = cur.fetchone()
    
    if label:
        event['up'] = label['up']
        event['fwd_return'] = label['fwd_return']
        labeled_events.append(event)

if len(labeled_events) < 10:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Step 6: Split into main and sealed (most recent 20%)
labeled_events.sort(key=lambda x: x['decision_date'])
split_idx = int(len(labeled_events) * 0.8)
main_events = labeled_events[:split_idx]
sealed_events = labeled_events[split_idx:]

if not main_events or not sealed_events:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Step 7: Calculate metrics
issued = len(labeled_events)
opportunities = issued  # All considered events become calls

# Calculate precision
hits = sum(1 for e in labeled_events if e['up'])
precision = hits / issued if issued > 0 else 0

# Calculate base rate (same as precision for bullish calls)
base_rate = precision

# Calculate distinct days
distinct_days = len(set(e['decision_date'][:10] for e in labeled_events))

# Calculate design effect using day clustering
day_counts = defaultdict(int)
for e in labeled_events:
    day = e['decision_date'][:10]
    day_counts[day] += 1

cluster_sizes = list(day_counts.values())
n = issued
d = distinct_days
avg_cluster = n / d
var_cluster = sum((c - avg_cluster) ** 2 for c in cluster_sizes) / d

if avg_cluster > 0:
    deff = 1 + (var_cluster / (avg_cluster ** 2))
else:
    deff = 1.0001  # Prevent division by zero

effective_n = n / deff if deff > 0 else n

# Calculate sealed precision
sealed_hits = sum(1 for e in sealed_events if e['up'])
sealed_precision = sealed_hits / len(sealed_events) if sealed_events else 0

# Step 8: Output results
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")

conn.close()
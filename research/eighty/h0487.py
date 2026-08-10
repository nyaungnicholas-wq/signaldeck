# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 486
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict

# Connect to the read-only database
conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
conn.row_factory = sqlite3.Row

# Get all insider trades (filed_ts is the knowable as-of date)
insider_trades = conn.execute("""
    SELECT symbol_id, insider, code, filed_ts, price, value
    FROM insider_trades
    WHERE code IN ('S', 'P')
    ORDER BY symbol_id, insider, filed_ts
""").fetchall()

# Group trades by insider+symbol for sequence detection
trades_by_insider = defaultdict(list)
for row in insider_trades:
    key = (row['symbol_id'], row['insider'])
    trades_by_insider[key].append(row)

# Find signals: open-market purchase after open-market sale within 180 days
signals = []
for (symbol_id, insider), trades in trades_by_insider.items():
    sale_dates = [row['filed_ts'] for row in trades if row['code'] == 'S']
    for row in trades:
        if row['code'] != 'P':
            continue
        purchase_date = row['filed_ts']
        # Check for sale in prior 180 calendar days
        prior_sales = [d for d in sale_dates if purchase_date - 180*24*3600 <= d < purchase_date]
        if not prior_sales:
            continue
        # Check purchase value >= $20k
        if row['value'] < 20000:
            continue
        signals.append({
            'symbol_id': symbol_id,
            'disclosure_ts': purchase_date,
            'insider': insider
        })

# Process signals with universe and abstention filters
opportunities = []
for sig in signals:
    symbol_id = sig['symbol_id']
    disc_ts = sig['disclosure_ts']
    disc_date = datetime.utcfromtimestamp(disc_ts).date()
    
    # Get daily bars for this symbol (only 1d available)
    bars = conn.execute("""
        SELECT ts, open, close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """, (symbol_id,)).fetchall()
    
    if len(bars) < 60:
        continue
    
    # Find disclosure date bar (or closest before)
    disc_bar_idx = None
    for i, bar in enumerate(bars):
        bar_date = datetime.utcfromtimestamp(bar['ts']).date()
        if bar_date <= disc_date:
            disc_bar_idx = i
    
    if disc_bar_idx is None or disc_bar_idx < 60:
        continue
    
    # Price filter: closing price >= $2 on disclosure date
    disc_close = bars[disc_bar_idx]['close']
    if disc_close < 2.0:
        continue
    
    # Liquidity filter: median daily dollar volume >= $1M over prior 60 trading days
    lookback_bars = bars[disc_bar_idx-59:disc_bar_idx+1]
    volumes = [bar['close'] * bar['volume'] for bar in lookback_bars]
    volumes.sort()
    median_vol = volumes[len(volumes)//2]
    if median_vol < 1_000_000:
        continue
    
    # Abstention: stock closed >10% above its close 20 trading days before disclosure
    if disc_bar_idx < 20:
        continue
    close_20_before = bars[disc_bar_idx - 20]['close']
    if disc_close > close_20_before * 1.10:
        continue
    
    # Entry day: first trading day after disclosure
    entry_bar_idx = disc_bar_idx + 1
    if entry_bar_idx >= len(bars):
        continue
    entry_date = datetime.utcfromtimestamp(bars[entry_bar_idx]['ts']).date()
    
    # Horizon: 21 trading days later
    exit_bar_idx = entry_bar_idx + 21
    if exit_bar_idx >= len(bars):
        continue
    
    opportunities.append({
        'symbol_id': symbol_id,
        'entry_date': entry_date,
        'entry_open': bars[entry_bar_idx]['open'],
        'exit_close': bars[exit_bar_idx]['close'],
        'disclosure_date': disc_date
    })

conn.close()

if len(opportunities) == 0:
    print("INSUFFICIENT=1")
    exit(0)

# Calculate labels and sort by entry_date
for opp in opportunities:
    opp['label'] = 1 if opp['exit_close'] > opp['entry_open'] else 0

opportunities.sort(key=lambda x: x['entry_date'])

# Split into development and sealed era (most recent 20% by time)
cutoff_idx = int(len(opportunities) * 0.8)
dev_opp = opportunities[:cutoff_idx]
sealed_opp = opportunities[cutoff_idx:]

# Statistics for development set
issued = len(dev_opp)
if issued == 0:
    print("INSUFFICIENT=1")
    exit(0)

opportunities_total = len(opportunities)
hits = sum(opp['label'] for opp in dev_opp)
precision = hits / issued

# Base rate within issued subset (same as precision in this case)
base_rate = precision

# Distinct days among issued calls
distinct_days = len(set(opp['entry_date'] for opp in dev_opp))

# Calculate effective N (design effect)
day_clusters = defaultdict(list)
for opp in dev_opp:
    day_clusters[opp['entry_date']].append(opp['label'])

cluster_sizes = [len(cluster) for cluster in day_clusters.values()]
m = len(day_clusters)
if m > 1:
    # Calculate intra-class correlation (ICC)
    overall_mean = precision
    between_var = sum((sum(cluster)/len(cluster) - overall_mean)**2 * len(cluster) 
                      for cluster in day_clusters.values()) / (m - 1)
    within_var = sum(sum((x - sum(cluster)/len(cluster))**2 
                         for x in cluster) 
                     for cluster in day_clusters.values()) / (issued - m)
    
    if between_var + within_var > 0:
        icc = (between_var - within_var) / (between_var + (len(dev_opp)/m - 1) * within_var)
    else:
        icc = 0
    design_effect = 1 + (len(dev_opp)/m - 1) * icc
else:
    design_effect = len(dev_opp)  # If only one cluster, all observations are dependent

effective_n = issued / design_effect if design_effect > 0 else issued

# Calculate sealed era precision
sealed_hits = sum(opp['label'] for opp in sealed_opp)
sealed_precision = sealed_hits / len(sealed_opp) if len(sealed_opp) > 0 else 0

# Print required outputs
print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opportunities_total}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.4f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")
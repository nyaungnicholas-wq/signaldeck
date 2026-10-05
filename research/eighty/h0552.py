# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 551
# cycle_index: 9
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
conn.row_factory = sqlite3.Row
c = conn.cursor()

# Get insider purchases with disclosure dates
c.execute("""
SELECT symbol_id, tx_ts, filed_ts
FROM insider_trades
WHERE code = 'P'
""")
trades = c.fetchall()
if not trades:
    print("INSUFFICIENT=1")
    exit(0)

# Build symbol sets for StockTwits and news sentiment availability
c.execute("SELECT DISTINCT symbol_id FROM stocktwits_sentiment")
st_symbols = {r[0] for r in c.fetchall()}
c.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
ns_symbols = {r[0] for r in c.fetchall()}

# Process each trade
signals = []
for trade in trades:
    sym = trade['symbol_id']
    if sym not in st_symbols or sym not in ns_symbols:
        continue
    
    filed_dt = datetime.utcfromtimestamp(trade['filed_ts'])
    day_str = filed_dt.strftime('%Y-%m-%d')
    
    # StockTwits bearish ratio check
    c.execute("""
    SELECT bearish, total
    FROM stocktwits_sentiment
    WHERE symbol_id = ? AND ts <= ?
    ORDER BY ts DESC LIMIT 1
    """, (sym, trade['filed_ts']))
    row = c.fetchone()
    if not row or row['total'] == 0:
        continue
    current_ratio = row['bearish'] / row['total']
    
    # Trailing 252-day distribution
    c.execute("""
    SELECT bearish, total
    FROM stocktwits_sentiment
    WHERE symbol_id = ? AND ts <= ? AND ts >= ?
    """, (sym, trade['filed_ts'], trade['filed_ts'] - 365*24*3600*2))
    rows = c.fetchall()
    if len(rows) < 20:  # Need enough history
        continue
    
    ratios = [r['bearish']/r['total'] for r in rows if r['total'] > 0]
    if len(ratios) < 20:
        continue
    
    ratios.sort()
    idx = int(len(ratios) * 0.9)
    threshold = ratios[idx]
    if current_ratio < threshold:
        continue
    
    # News sentiment check
    c.execute("""
    SELECT mean_score
    FROM sentiment_features
    WHERE symbol_id = ? AND day = ?
    """, (sym, day_str))
    row = c.fetchone()
    if not row:
        continue
    current_sentiment = row['mean_score']
    
    c.execute("""
    SELECT AVG(mean_score)
    FROM (
        SELECT mean_score
        FROM sentiment_features
        WHERE symbol_id = ? AND day <= ?
        ORDER BY day DESC LIMIT 5
    )
    """, (sym, day_str))
    ma5 = c.fetchone()[0]
    
    c.execute("""
    SELECT AVG(mean_score)
    FROM (
        SELECT mean_score
        FROM sentiment_features
        WHERE symbol_id = ? AND day <= ?
        ORDER BY day DESC LIMIT 252
    )
    """, (sym, day_str))
    ma252 = c.fetchone()[0]
    
    if ma5 is None or ma252 is None or ma5 >= ma252:
        continue
    
    # Get 21-day forward return
    c.execute("""
    SELECT up
    FROM prediction_outcomes
    WHERE symbol_id = ? AND horizon = 21 AND ts >= ? AND ts <= ?
    ORDER BY ts ASC LIMIT 1
    """, (sym, trade['filed_ts'] + 86400, trade['filed_ts'] + 86400*22))
    row = c.fetchone()
    if not row:
        continue
    
    signals.append({
        'filed_ts': trade['filed_ts'],
        'day': day_str,
        'hit': row['up']
    })

if not signals:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by time and split into train/test (80%/20%)
signals.sort(key=lambda x: x['filed_ts'])
split_idx = int(len(signals) * 0.8)
train = signals[:split_idx]
test = signals[split_idx:]

# Calculate metrics for full set
issued = len(signals)
days = len(set(s['day'] for s in signals))
hits = sum(s['hit'] for s in signals)
precision = hits / issued

# Calculate design effect (clustering by day)
day_counts = {}
for s in signals:
    day_counts[s['day']] = day_counts.get(s['day'], 0) + 1
if days > 1:
    cluster_sizes = list(day_counts.values())
    avg_cluster = len(signals) / days
    var_cluster = sum((x - avg_cluster) ** 2 for x in cluster_sizes) / (days - 1)
    # Estimate ICC as variance of cluster means over overall variance
    overall_mean = precision
    # For binary outcomes, approximate variance as p(1-p)
    overall_var = precision * (1 - precision)
    if overall_var > 0:
        icc = (var_cluster / avg_cluster) / overall_var
        icc = min(max(icc, 0), 1)
    else:
        icc = 0
    design_effect = 1 + (avg_cluster - 1) * icc
    effective_n = issued / design_effect
else:
    design_effect = 1
    effective_n = issued

# Base rate within issued set
base_rate = precision

# Test set metrics
if test:
    test_hits = sum(s['hit'] for s in test)
    test_precision = test_hits / len(test)
else:
    test_precision = 0

print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={len(trades)}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={days}")
print(f"EFFECTIVE_N={effective_n:.2f}")
print(f"SEALED_PRECISION={test_precision:.6f}")

conn.close()
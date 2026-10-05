# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 531
# cycle_index: 61
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import datetime

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
conn.row_factory = sqlite3.Row
c = conn.cursor()

c.execute("""
SELECT s.id
FROM symbols s
JOIN (
    SELECT symbol_id, COUNT(*) as n_days
    FROM bars
    WHERE tf='1d'
    GROUP BY symbol_id
    HAVING n_days >= 250
) b ON s.id = b.symbol_id
JOIN (
    SELECT symbol_id, COUNT(DISTINCT ts) as n_st
    FROM stocktwits_sentiment
    GROUP BY symbol_id
    HAVING n_st >= 60
) st ON s.id = st.symbol_id
WHERE s.active = 1
""")
universe = [row[0] for row in c.fetchall()]

if not universe:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

all_calls = []
all_opportunities = 0

for sym_id in universe:
    c.execute("""
        SELECT ts, open, close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """, (sym_id,))
    bars = c.fetchall()
    if len(bars) < 253:
        continue

    c.execute("""
        SELECT ts, bearish
        FROM stocktwits_sentiment
        WHERE symbol_id = ?
        ORDER BY ts
    """, (sym_id,))
    st = c.fetchall()
    if len(st) < 60:
        continue

    c.execute("""
        SELECT ts, up
        FROM prediction_outcomes
        WHERE symbol_id = ? AND horizon = 21
    """, (sym_id,))
    labels = {row[0]: row[1] for row in c.fetchall()}

    for i in range(251, len(bars) - 21):
        decision_ts = bars[i][0]
        decision_date = datetime.datetime.utcfromtimestamp(decision_ts).strftime('%Y-%m-%d')
        
        st_until = [b for b in st if b[0] <= decision_ts]
        if len(st_until) < 60:
            continue
        
        bars_until = [bar for bar in bars if bar[0] <= decision_ts]
        if len(bars_until) < 252:
            continue
        
        avg_vol = sum(bar[3] for bar in bars_until[-60:]) / 60
        if avg_vol < 5_000_000:
            continue
        
        all_opportunities += 1

        bearish_counts = [b[1] for b in st_until]
        recent_5 = bearish_counts[-5:] if len(bearish_counts) >= 5 else []
        trailing_60 = bearish_counts[-60:]

        if len(recent_5) < 5 or len(trailing_60) < 60:
            continue
        
        mean_5 = sum(recent_5) / 5
        if mean_5 < 10:
            continue
        
        mean_60 = sum(trailing_60) / 60
        var_60 = sum((x - mean_60) ** 2 for x in trailing_60) / 60
        std_60 = math.sqrt(var_60)
        if std_60 == 0 or mean_5 < mean_60 + 2 * std_60:
            continue
        
        if i < 21:
            continue
        ret_21 = bars[i][2] / bars[i-21][2] - 1
        if ret_21 > -0.05:
            continue
        
        low_252 = min(bar[2] for bar in bars_until[-252:])
        if bars[i][2] < low_252 * 1.10:
            continue

        label_ts = bars[i+1][0]
        if label_ts not in labels:
            continue

        up = labels[label_ts]
        all_calls.append((sym_id, decision_ts, up, decision_date))

conn.close()

if len(all_calls) < 30:
    print("INSUFFICIENT=1")
    exit(0)

all_calls.sort(key=lambda x: x[1])
issued = len(all_calls)
hits = sum(1 for call in all_calls if call[2] == 1)
precision = hits / issued
base_rate = precision

days = set(call[3] for call in all_calls)
distinct_days = len(days)

if distinct_days == 0:
    print("INSUFFICIENT=1")
    exit(0)

cluster_sizes = {}
for call in all_calls:
    day = call[3]
    cluster_sizes[day] = cluster_sizes.get(day, 0) + 1

m = issued / distinct_days
p = precision
q = 1 - p
if p == 0 or p == 1:
    icc = 0
else:
    daily_hits = {}
    for call in all_calls:
        day = call[3]
        if day not in daily_hits:
            daily_hits[day] = 0
        if call[2] == 1:
            daily_hits[day] += 1
    
    between_var = 0
    for day in days:
        n_d = cluster_sizes[day]
        p_d = daily_hits[day] / n_d
        between_var += n_d * (p_d - p) ** 2
    between_var /= (distinct_days - 1) if distinct_days > 1 else 1
    
    within_var = 0
    for day in days:
        n_d = cluster_sizes[day]
        p_d = daily_hits[day] / n_d
        within_var += n_d * p_d * (1 - p_d)
    within_var /= issued - distinct_days if issued > distinct_days else 1
    
    icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0

de = 1 + (m - 1) * icc
effective_n = issued / de

sealed_cutoff_idx = int(issued * 0.8)
sealed_calls = all_calls[sealed_cutoff_idx:]
if not sealed_calls:
    sealed_precision = 0
else:
    sealed_hits = sum(1 for call in sealed_calls if call[2] == 1)
    sealed_precision = sealed_hits / len(sealed_calls)

print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={all_opportunities}")
print(f"PRECISION={precision:.4f}")
print(f"BASE_RATE={base_rate:.4f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.2f}")
print(f"SEALED_PRECISION={sealed_precision:.4f}")
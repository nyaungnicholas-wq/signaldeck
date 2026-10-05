import sqlite3

DB = "data/signaldeck.db"

con = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
cur = con.cursor()

# Get all daily bars for all symbols, ordered
cur.execute("""
    SELECT symbol_id, ts, open, high, low, close, volume
    FROM bars
    WHERE tf='1d'
    ORDER BY symbol_id, ts
""")
bars = cur.fetchall()

# Organize by symbol
from collections import defaultdict
sym_bars = defaultdict(list)
for row in bars:
    sym_bars[row[0]].append(row[1:])  # ts, open, high, low, close, volume

# Precompute for each symbol
calls = []
opportunities = 0
min_bars = 252
cooldown = defaultdict(list)  # symbol_id -> list of recent call ts

for sym_id, bar_list in sym_bars.items():
    n = len(bar_list)
    if n < min_bars + 20:
        continue  # Need at least 252 prior + T+20

    # Precompute rolling averages
    closes = [b[4] for b in bar_list]
    volumes = [b[5] for b in bar_list]
    dollar_vol = [closes[i] * volumes[i] for i in range(n)]

    # Process each possible T (index of T bar)
    for t_idx in range(min_bars, n - 20):
        ts_T = bar_list[t_idx][0]
        close_T = closes[t_idx]
        vol_T = volumes[t_idx]

        # Entry conditions:
        if close_T < 5:
            continue

        # Check cooldown: no call in prior 20 trading days for same symbol
        recent_calls = cooldown[sym_id]
        if recent_calls and (ts_T - recent_calls[-1]) <= 20 * 86400:  # approximate
            continue

        # Check prior sessions >= 252 (already ensured by range)
        # Check bar existence for T-20..T: we have them because we iterate after min_bars

        # Compute avg volume over T-20..T-1 (indices t_idx-20 to t_idx-1)
        avg_vol_20 = sum(volumes[t_idx-20:t_idx]) / 20

        # Compute return from T-1 to T
        close_prev = closes[t_idx-1]
        if close_prev == 0:
            continue
        ret_T = (close_T - close_prev) / close_prev
        if ret_T > -0.08:
            continue  # Not a collapse

        # Check volume spike: vol_T >= 3 * avg_vol_20
        if vol_T < 3 * avg_vol_20:
            continue

        # Compute avg dollar volume over T-60..T-1
        avg_dollar_vol_60 = sum(dollar_vol[t_idx-60:t_idx]) / 60
        if avg_dollar_vol_60 < 5_000_000:
            continue

        # All entry conditions met, issue call
        opportunities += 1

        # Outcome at T+20
        ts_T20 = bar_list[t_idx+20][0]
        close_T20 = closes[t_idx+20]
        hit = 1 if close_T20 > close_T else 0

        calls.append((sym_id, ts_T, hit))
        cooldown[sym_id].append(ts_T)

con.close()

# Analysis
if not calls:
    print("INSUFFICIENT=1")
    exit(0)

# Sort by ts
calls.sort(key=lambda x: x[1])

issued = len(calls)
hits = sum(c[2] for c in calls)
precision = hits / issued if issued else 0

# Base rate: proportion of hits in issued calls
base_rate = precision  # Within issued subset, base rate = precision

# Distinct days among issued calls
days = set(c[1] for c in calls)
distinct_days = len(days)

# Effective N: design effect from clustering by day
# Group calls by day
from collections import Counter
day_counts = Counter(c[1] for c in calls)
day_hits = Counter()
for c in calls:
    if c[2]:
        day_hits[c[1]] += 1

# Compute variance of daily hit rates
total_n = issued
p = hits / total_n
weighted_var = 0
for day, n_d in day_counts.items():
    p_d = day_hits[day] / n_d
    weighted_var += n_d * (p_d - p) ** 2
weighted_var /= (total_n - 1) if total_n > 1 else 1

design_effect = weighted_var / (p * (1 - p)) if p * (1 - p) > 0 else 1
effective_n = issued / design_effect if design_effect > 0 else issued

# Sealed era: most recent 20% of calls
sealed_cutoff = int(issued * 0.8)
sealed_calls = calls[sealed_cutoff:]
sealed_issued = len(sealed_calls)
sealed_hits = sum(c[2] for c in sealed_calls)
sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0

print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
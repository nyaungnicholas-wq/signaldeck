# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 550
# cycle_index: 8
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta

DB_PATH = "file:data/signaldeck.db?mode=ro"
conn = sqlite3.connect(DB_PATH, uri=True)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

def safe_query(sql, params=()):
    try:
        cur.execute(sql, params)
        return cur.fetchall()
    except sqlite3.Error:
        return []

# Universe: symbols with daily data since 2018-07 and a 13F filing in the most recent quarter
# Step 1: symbols with daily data from 2018-07-26 onward (at least one bar)
symbols_with_daily = set(r[0] for r in safe_query("""
    SELECT symbol_id 
    FROM bars 
    WHERE tf = '1d' AND ts >= 1532611200
    GROUP BY symbol_id
    HAVING COUNT(*) >= 1
"""))

# Step 2: most recent quarter in inst_holdings
latest_quarter = safe_query("SELECT MAX(period) FROM inst_holdings")
if not latest_quarter or not latest_quarter[0][0]:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)
latest_q = latest_quarter[0][0]

# Step 3: symbols with a 13F filing in that quarter
symbols_with_13f = set(r[0] for r in safe_query("""
    SELECT DISTINCT symbol_id 
    FROM inst_holdings 
    WHERE period = ?
""", (latest_q,)))

universe = symbols_with_daily.intersection(symbols_with_13f)
if not universe:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Prepare inst_holdings: aggregate value per symbol per quarter
inst_agg = safe_query("""
    SELECT symbol_id, period, SUM(value) as total_value
    FROM inst_holdings
    WHERE symbol_id IN ({})
    GROUP BY symbol_id, period
    ORDER BY symbol_id, period
""".format(','.join('?' for _ in universe)), tuple(universe))

# Prepare stocktwits: get bearish per symbol per day
stocktwits_agg = safe_query("""
    SELECT symbol_id, DATE(ts, 'unixepoch') as day, bearish
    FROM stocktwits_sentiment
    WHERE symbol_id IN ({})
""".format(','.join('?' for _ in universe)), tuple(universe))

# Index by symbol
inst_by_symbol = {}
for row in inst_agg:
    sid = row['symbol_id']
    inst_by_symbol.setdefault(sid, []).append((row['period'], row['total_value']))

st_by_symbol = {}
for row in stocktwits_agg:
    sid = row['symbol_id']
    st_by_symbol.setdefault(sid, []).append((row['day'], row['bearish']))

# For each symbol, compute 13F QoQ changes
signals = []
for sid in universe:
    quarters = sorted(inst_by_symbol.get(sid, []))
    for i in range(1, len(quarters)):
        prev_period, prev_val = quarters[i-1]
        cur_period, cur_val = quarters[i]
        if prev_val is None or cur_val is None or prev_val == 0:
            continue
        qoq_change = (cur_val - prev_val) / prev_val
        if qoq_change >= 0.15:
            # Decision date = period + 45 days (to respect filing lag)
            try:
                period_date = datetime.strptime(cur_period, "%Y-%m-%d")
                decision_date = period_date + timedelta(days=45)
            except ValueError:
                continue
            decision_str = decision_date.strftime("%Y-%m-%d")
            signals.append((sid, decision_str, cur_period))

if not signals:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# For each signal, find StockTwits day >= decision date and compute bearish percentile over 252 days
issued_rows = []
opportunities = 0
for sid, dec_str, _ in signals:
    # Get stocktwits for this symbol after decision date
    st_days = sorted([d for d in st_by_symbol.get(sid, []) if d[0] >= dec_str])
    if not st_days:
        continue
    # Take the first day
    day_str, bearish_today = st_days[0]
    opportunities += 1
    # Get 252-day history including today
    hist = []
    for d_str, b in st_by_symbol.get(sid, []):
        if d_str <= day_str:
            hist.append(b)
    if len(hist) < 10:
        continue
    # Compute percentile (>=90th)
    sorted_hist = sorted(hist)
    rank = sum(1 for x in sorted_hist if x < bearish_today)
    percentile = rank / len(sorted_hist)
    if percentile < 0.9:
        continue
    # Now get 21-day forward return label from prediction_outcomes
    # Use ts as unix epoch of day_str
    try:
        day_epoch = int(datetime.strptime(day_str, "%Y-%m-%d").timestamp())
    except ValueError:
        continue
    label_row = safe_query("""
        SELECT up
        FROM prediction_outcomes
        WHERE symbol_id = ? AND horizon = 21 AND ts = ?
    """, (sid, day_epoch))
    if not label_row:
        continue
    up = label_row[0][0]
    if up is not None:
        issued_rows.append((sid, day_str, up))

# Split into development and sealed (last 20%)
if not issued_rows:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

issued_rows.sort(key=lambda x: x[1])  # sort by day
n_total = len(issued_rows)
n_sealed = max(1, int(n_total * 0.2))
sealed = issued_rows[-n_sealed:]
dev = issued_rows[:-n_sealed]

# Metrics
issued = len(issued_rows)
if issued == 0:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

hits = sum(1 for r in issued_rows if r[2] == 1)
precision = hits / issued
base_rate = precision  # by definition within issued

distinct_days = len(set(r[1] for r in issued_rows))

# Design effect: cluster by day
from collections import defaultdict
day_counts = defaultdict(int)
day_hits = defaultdict(int)
for _, day, up in issued_rows:
    day_counts[day] += 1
    if up == 1:
        day_hits[day] += 1
m_bar = issued / distinct_days
# Compute ICC for binary outcome
p_overall = hits / issued
var_between = 0
for day in day_counts:
    n_i = day_counts[day]
    p_i = day_hits.get(day, 0) / n_i if n_i > 0 else 0
    var_between += n_i * (p_i - p_overall) ** 2
var_between /= (distinct_days - 1) if distinct_days > 1 else 1
var_total = p_overall * (1 - p_overall)
ICC = var_between / var_total if var_total > 0 else 0
ICC = max(0, min(1, ICC))
DEFF = 1 + (m_bar - 1) * ICC
effective_n = issued / DEFF

# Sealed precision
sealed_hits = sum(1 for r in sealed if r[2] == 1)
sealed_precision = sealed_hits / sealed if sealed else 0

print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")
conn.close()
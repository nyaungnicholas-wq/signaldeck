# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 585
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = conn.cursor()

# Get all symbols with 13F data
cur.execute("SELECT DISTINCT symbol_id FROM inst_holdings")
symbols_13f = {row[0] for row in cur.fetchall()}

# Get all symbols with insider purchase data (code='P')
cur.execute("SELECT DISTINCT symbol_id FROM insider_trades WHERE code = 'P'")
symbols_insider = {row[0] for row in cur.fetchall()}

common_symbols = symbols_13f & symbols_insider
if not common_symbols:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Get all 13F periods with quarter-over-quarter increase >=5%
# For each symbol, get ordered periods and compute change from previous
cur.execute("""
    WITH symbol_periods AS (
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
    ),
    lagged AS (
        SELECT 
            symbol_id,
            period,
            total_shares,
            LAG(total_shares, 1) OVER (PARTITION BY symbol_id ORDER BY period) as prev_shares
        FROM symbol_periods
    )
    SELECT symbol_id, period, total_shares, prev_shares
    FROM lagged
    WHERE prev_shares IS NOT NULL 
      AND (total_shares - prev_shares) * 1.0 / prev_shares >= 0.05
""")
qualifying_13f = cur.fetchall()

if not qualifying_13f:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# For each qualifying 13F, check for insider purchase within 5 trading days
# of period + 45 days (conservative 13F publication lag)
calls = []

for symbol_id, period, total_shares, prev_shares in qualifying_13f:
    # period is 'YYYY-MM-DD' format
    period_date = datetime.datetime.strptime(period, '%Y-%m-%d').date()
    decision_date = period_date + datetime.timedelta(days=45)
    decision_epoch = int(datetime.datetime.combine(decision_date, datetime.time()).timestamp())
    
    # Get next 5 trading days after decision_date from bars (1d)
    cur.execute("""
        SELECT ts FROM bars 
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
        ORDER BY ts LIMIT 5
    """, (symbol_id, decision_epoch))
    trading_days = [row[0] for row in cur.fetchall()]
    
    if len(trading_days) < 5:
        # Not enough trading days after decision_date
        continue
    
    window_end = trading_days[-1]
    
    # Check for insider purchases (code='P') in the window
    cur.execute("""
        SELECT 1 FROM insider_trades 
        WHERE symbol_id = ? 
          AND code = 'P'
          AND filed_ts >= ? AND filed_ts <= ?
        LIMIT 1
    """, (symbol_id, decision_epoch, window_end))
    
    if cur.fetchone() is None:
        continue
    
    # Get the bar at decision_date for label lookup
    cur.execute("""
        SELECT ts FROM bars 
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
        ORDER BY ts LIMIT 1
    """, (symbol_id, decision_epoch))
    
    bar_row = cur.fetchone()
    if not bar_row:
        continue
    label_ts = bar_row[0]
    
    # Get label from prediction_outcomes
    cur.execute("""
        SELECT up, fwd_return FROM prediction_outcomes
        WHERE symbol_id = ? AND horizon = 21 AND ts = ?
    """, (symbol_id, label_ts))
    
    label_row = cur.fetchone()
    if not label_row:
        continue
    
    up, fwd_return = label_row
    calls.append((symbol_id, decision_date, label_ts, up, fwd_return))

conn.close()

if not calls:
    print("INSUFFICIENT=1")
    exit(0)

# Sort calls by date
calls.sort(key=lambda x: x[1])

# Split into holdout (last 20%) and train (first 80%)
n_calls = len(calls)
holdout_start = int(n_calls * 0.8)
train_calls = calls[:holdout_start]
holdout_calls = calls[holdout_start:]

# Count metrics
def calc_metrics(call_list):
    if not call_list:
        return 0, 0, 0, 0, 0, 0
    
    issued = len(call_list)
    hits = sum(1 for call in call_list if call[3] == 1)
    precision = hits / issued if issued else 0
    
    # Base rate of predicted class (up=1) within issued
    base_rate = hits / issued if issued else 0
    
    # Distinct days among issued calls
    distinct_days = len({call[1].isoformat() for call in call_list})
    
    # Design effect: calls clustered in time not independent
    # Average calls per day
    avg_per_day = issued / distinct_days if distinct_days else 1
    design_effect = avg_per_day  # Simplistic: DEFF ≈ avg cluster size
    effective_n = issued / design_effect if design_effect else 0
    
    return issued, precision, base_rate, distinct_days, effective_n, 0

# Calculate metrics for all calls
issued_all, precision_all, base_rate_all, distinct_days_all, effective_n_all, _ = calc_metrics(calls)

# Calculate metrics for holdout
issued_hold, precision_hold, _, _, _, _ = calc_metrics(holdout_calls)

# Print required outputs
print(f"ISSUED={issued_all}")
print(f"OPPORTUNITIES={len(qualifying_13f)}")
print(f"PRECISION={precision_all:.4f}")
print(f"BASE_RATE={base_rate_all:.4f}")
print(f"DISTINCT_DAYS={distinct_days_all}")
print(f"EFFECTIVE_N={effective_n_all:.4f}")
print(f"SEALED_PRECISION={precision_hold:.4f}")
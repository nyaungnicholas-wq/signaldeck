# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 547
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
c = conn.cursor()

try:
    # Get all symbols that have at least 2 consecutive quarters of 13F ownership increase and public float decline
    c.execute('''
    WITH inst_quarterly AS (
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
    ),
    inst_ranked AS (
        SELECT symbol_id, period, total_shares,
               LAG(total_shares) OVER (PARTITION BY symbol_id ORDER BY period) as prev_shares,
               LAG(period) OVER (PARTITION BY symbol_id ORDER BY period) as prev_period
        FROM inst_quarterly
    ),
    inst_increase AS (
        SELECT symbol_id, period, total_shares, prev_shares, prev_period
        FROM inst_ranked
        WHERE prev_shares IS NOT NULL AND total_shares > prev_shares
    ),
    float_data AS (
        SELECT symbol_id, as_of, value as float_value
        FROM fundamentals
        WHERE metric = 'EntityPublicFloat'
    ),
    float_ranked AS (
        SELECT symbol_id, as_of, float_value,
               LAG(float_value) OVER (PARTITION BY symbol_id ORDER BY as_of) as prev_float,
               LAG(as_of) OVER (PARTITION BY symbol_id ORDER BY as_of) as prev_as_of
        FROM float_data
    ),
    float_decline AS (
        SELECT symbol_id, as_of, float_value, prev_float, prev_as_of
        FROM float_ranked
        WHERE prev_float IS NOT NULL AND float_value < prev_float
    )
    SELECT i.symbol_id, i.period, f.as_of as float_period
    FROM inst_increase i
    JOIN float_decline f ON i.symbol_id = f.symbol_id 
        AND f.as_of = i.period 
        AND f.prev_as_of = i.prev_period
    ''')
    universe = c.fetchall()
    
    if not universe:
        print("INSUFFICIENT=1")
        conn.close()
        exit(0)
    
    # Build universe lookup: symbol -> (13F period, float period)
    symbol_data = {}
    for row in universe:
        symbol_id, inst_period, float_period = row
        if inst_period == float_period:  # periods match
            symbol_data[symbol_id] = inst_period
    
    if not symbol_data:
        print("INSUFFICIENT=1")
        conn.close()
        exit(0)
    
    # For each symbol, find decision date (first trading day after 13F disclosure)
    # Assuming 13F filing occurs 45 days after quarter end
    c.execute("SELECT ts FROM bars WHERE tf='1d' ORDER BY ts DESC LIMIT 1")
    max_ts = c.fetchone()[0]
    
    opportunities = []
    calls = []
    
    for symbol_id, quarter_end in symbol_data.items():
        # Estimate 13F disclosure date: quarter end + 45 days
        c.execute("SELECT ts FROM bars WHERE tf='1d' AND ts >= ? ORDER BY ts LIMIT 1", 
                  (quarter_end + 45*86400,))
        row = c.fetchone()
        if not row:
            continue
        decision_ts = row[0]
        
        # Get the quarter end date as epoch
        # Convert quarter_end (string like '2025-03-31') to epoch
        c.execute("SELECT strftime('%s', ?) || '000'", (quarter_end,))
        quarter_epoch = int(c.fetchone()[0])
        
        # Check EPS growth acceleration (trailing 4 quarters)
        c.execute('''
        WITH eps_data AS (
            SELECT as_of, value as eps
            FROM fundamentals
            WHERE symbol_id = ? AND metric = 'EPS' AND fetched_at <= ?
            ORDER BY as_of DESC
            LIMIT 4
        )
        SELECT as_of, eps FROM eps_data ORDER BY as_of ASC
        ''', (symbol_id, decision_ts))
        eps_rows = c.fetchall()
        
        if len(eps_rows) < 4:
            continue  # Need 4 quarters for acceleration check
        
        # Calculate year-over-year growth for each quarter
        growth_rates = []
        for i in range(1, len(eps_rows)):
            prev_eps = eps_rows[i-1][1]
            curr_eps = eps_rows[i][1]
            if prev_eps is None or curr_eps is None or prev_eps == 0:
                continue
            growth = (curr_eps - prev_eps) / abs(prev_eps)
            growth_rates.append((eps_rows[i][0], growth))
        
        if len(growth_rates) < 2:
            continue
        
        # Check acceleration: last growth > previous growth
        if growth_rates[-1][1] <= growth_rates[-2][1]:
            continue  # Not accelerating
        
        # Check sentiment positive (above 20-day MA)
        c.execute('''
        SELECT day, mean_score
        FROM sentiment_features
        WHERE symbol_id = ? AND day <= date(?, 'unixepoch')
        ORDER BY day DESC
        LIMIT 21
        ''', (symbol_id, decision_ts))
        sent_rows = c.fetchall()
        
        if len(sent_rows) < 21:
            continue  # Need 20 days + current
        
        current_sent = sent_rows[0][1]
        ma_20 = sum(row[1] for row in sent_rows[1:21]) / 20
        
        if current_sent is None or ma_20 is None or current_sent <= ma_20:
            continue  # Not above MA
        
        # Get label: up/down in next 63 trading days
        c.execute('''
        SELECT close FROM bars 
        WHERE symbol_id = ? AND tf='1d' AND ts >= ?
        ORDER BY ts ASC LIMIT 1
        ''', (symbol_id, decision_ts))
        entry_row = c.fetchone()
        if not entry_row:
            continue
        entry_close = entry_row[0]
        
        # Find close 63 trading days later
        c.execute('''
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf='1d' AND ts > ?
        ORDER BY ts ASC LIMIT 1 OFFSET 62
        ''', (symbol_id, decision_ts))
        exit_row = c.fetchone()
        if not exit_row:
            continue
        exit_close = exit_row[0]
        
        up = 1 if exit_close > entry_close else 0
        
        opportunities.append((symbol_id, decision_ts))
        calls.append((symbol_id, decision_ts, up))
    
    if not calls:
        print("INSUFFICIENT=1")
        conn.close()
        exit(0)
    
    # Split into train/test (80/20 by time)
    calls.sort(key=lambda x: x[1])
    n_calls = len(calls)
    split_idx = int(n_calls * 0.8)
    train_calls = calls[:split_idx]
    test_calls = calls[split_idx:]
    
    # Calculate metrics
    def calc_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        
        issued = len(call_list)
        hits = sum(1 for c in call_list if c[2] == 1)
        precision = hits / issued if issued > 0 else 0.0
        
        # Base rate within issued
        base_rate = precision  # Since we're predicting "up" and hits are actual ups
        
        # Distinct days
        days = set()
        for c in call_list:
            day_ts = c[1] // 86400 * 86400  # Floor to day
            days.add(day_ts)
        distinct_days = len(days)
        
        # Effective N (adjust for clustering)
        # Calculate average cluster size per day
        day_counts = defaultdict(int)
        for c in call_list:
            day_ts = c[1] // 86400 * 86400
            day_counts[day_ts] += 1
        
        if distinct_days > 0:
            avg_per_day = issued / distinct_days
            design_effect = 1 + (avg_per_day - 1)  # Simplified DEFF
            effective_n = issued / design_effect
        else:
            effective_n = 0
        
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    train_issued, train_hits, train_prec, train_base, train_days, train_eff = calc_metrics(train_calls)
    test_issued, test_hits, test_prec, test_base, test_days, test_eff = calc_metrics(test_calls)
    
    total_issued = len(calls)
    total_opportunities = len(opportunities)
    total_hits = train_hits + test_hits
    total_precision = total_hits / total_issued if total_issued > 0 else 0.0
    total_base = total_precision
    total_days_set = set()
    for c in calls:
        day_ts = c[1] // 86400 * 86400
        total_days_set.add(day_ts)
    total_distinct = len(total_days_set)
    
    # Effective N for full set
    day_counts_full = defaultdict(int)
    for c in calls:
        day_ts = c[1] // 86400 * 86400
        day_counts_full[day_ts] += 1
    
    if total_distinct > 0:
        avg_full = total_issued / total_distinct
        deff_full = 1 + (avg_full - 1)
        eff_full = total_issued / deff_full
    else:
        eff_full = 0
    
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base:.6f}")
    print(f"DISTINCT_DAYS={total_distinct}")
    print(f"EFFECTIVE_N={eff_full:.6f}")
    print(f"SEALED_PRECISION={test_prec:.6f}")

finally:
    conn.close()
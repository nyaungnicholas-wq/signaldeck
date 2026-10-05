# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 568
# cycle_index: 26
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime

def get_connection():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def compute_rsi(closes, period=14):
    if len(closes) < period + 1:
        return None
    deltas = [closes[i+1] - closes[i] for i in range(len(closes)-1)]
    gains = [max(d, 0) for d in deltas]
    losses = [max(-d, 0) for d in deltas]
    avg_gain = sum(gains[:period]) / period
    avg_loss = sum(losses[:period]) / period
    for i in range(period, len(deltas)):
        avg_gain = (avg_gain * (period-1) + gains[i]) / period
        avg_loss = (avg_loss * (period-1) + losses[i]) / period
    if avg_loss == 0:
        return 100.0
    rs = avg_gain / avg_loss
    return 100.0 - (100.0 / (1.0 + rs))

def main():
    conn = get_connection()
    cursor = conn.cursor()
    
    # Check for insider purchases with open-market code P
    cursor.execute("SELECT COUNT(*) FROM insider_trades WHERE code='P'")
    if cursor.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        return
    
    # Check for FRED initial claims series
    cursor.execute("SELECT COUNT(*) FROM macro_series WHERE series='ICSA'")
    if cursor.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        return
    
    # Get all insider purchases (open-market)
    cursor.execute("""
        SELECT symbol_id, tx_ts, filed_ts, price, shares
        FROM insider_trades 
        WHERE code='P'
        ORDER BY filed_ts
    """)
    trades = cursor.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    # Get all FRED ICSA data
    cursor.execute("SELECT ts, value FROM macro_series WHERE series='ICSA' ORDER BY ts")
    icsa_data = cursor.fetchall()
    if len(icsa_data) < 4:
        print("INSUFFICIENT=1")
        return
    
    # Build weekly moving average of ICSA (4-week)
    # Convert to weekly timestamps
    weekly_icsa = {}
    for ts, value in icsa_data:
        dt = datetime.datetime.utcfromtimestamp(ts)
        # Get start of week (Monday)
        start_week = dt - datetime.timedelta(days=dt.weekday())
        week_key = start_week.date().isoformat()
        if week_key not in weekly_icsa:
            weekly_icsa[week_key] = []
        weekly_icsa[week_key].append(value)
    
    # Compute weekly average
    weeks = sorted(weekly_icsa.keys())
    weekly_avg = {}
    for week in weeks:
        weekly_avg[week] = sum(weekly_icsa[week]) / len(weekly_icsa[week])
    
    # Compute 4-week moving average
    moving_avg = {}
    for i in range(3, len(weeks)):
        current_week = weeks[i]
        past_4_weeks = [weeks[i-3], weeks[i-2], weeks[i-1], weeks[i]]
        moving_avg[current_week] = sum(weekly_avg[w] for w in past_4_weeks) / 4
    
    # Check if moving average is falling for 4 consecutive weeks
    def check_falling_4_weeks(week_date):
        # Find the index of this week in the moving_avg keys
        ma_weeks = sorted(moving_avg.keys())
        if week_date not in ma_weeks:
            return False
        idx = ma_weeks.index(week_date)
        if idx < 3:
            return False
        # Check falling: moving_avg[week-3] > moving_avg[week-2] > moving_avg[week-1] > moving_avg[week]
        return (moving_avg[ma_weeks[idx-3]] > moving_avg[ma_weeks[idx-2]] > 
                moving_avg[ma_weeks[idx-1]] > moving_avg[ma_weeks[idx]])
    
    # Get yield curve data
    cursor.execute("SELECT ts, value FROM macro_series WHERE series='DGS10' ORDER BY ts")
    dgs10_data = cursor.fetchall()
    cursor.execute("SELECT ts, value FROM macro_series WHERE series='DGS2' ORDER BY ts")
    dgs2_data = cursor.fetchall()
    
    # Get symbols with at least 2 years of daily data and non-zero volume
    cursor.execute("""
        SELECT symbol_id, MIN(ts), MAX(ts), COUNT(*)
        FROM bars
        WHERE tf='1d' AND volume > 0
        GROUP BY symbol_id
        HAVING COUNT(*) >= 504  # approx 2 years of trading days
    """)
    eligible_symbols = {row[0]: (row[1], row[2]) for row in cursor.fetchall()}
    
    # Prepare outcomes from prediction_outcomes
    cursor.execute("""
        SELECT symbol_id, horizon, ts, up
        FROM prediction_outcomes
        WHERE horizon=21
    """)
    outcomes = {}
    for row in cursor.fetchall():
        sym, hor, ts, up = row
        outcomes[(sym, ts)] = up
    
    # Process each trade
    opportunities = []
    issued_calls = []
    
    for trade in trades:
        sym_id, tx_ts, filed_ts, price, shares = trade
        
        # Skip if symbol not in eligible set
        if sym_id not in eligible_symbols:
            continue
            
        # Skip if transaction not at least 21 days before now (need horizon)
        if filed_ts + 21*24*3600 > datetime.datetime.utcnow().timestamp():
            continue
            
        # Check if this is an open-market purchase (we already filtered code='P')
        # Check yield curve
        # Get most recent DGS10 and DGS2 before filed_ts
        yield_curve_valid = True
        recent_dgs10 = None
        recent_dgs2 = None
        for ts, val in reversed(dgs10_data):
            if ts < filed_ts:
                recent_dgs10 = val
                break
        for ts, val in reversed(dgs2_data):
            if ts < filed_ts:
                recent_dgs2 = val
                break
        
        if recent_dgs10 is None or recent_dgs2 is None:
            yield_curve_valid = False
        else:
            spread = recent_dgs10 - recent_dgs2
            if spread < 0:  # inverted
                yield_curve_valid = False
        
        if not yield_curve_valid:
            continue
        
        # Check RSI
        # Get last 15 days of daily closes before filed_ts
        dt_filed = datetime.datetime.utcfromtimestamp(filed_ts)
        start_rsi = dt_filed - datetime.timedelta(days=30)
        cursor.execute("""
            SELECT ts, close
            FROM bars
            WHERE symbol_id=? AND tf='1d' AND ts >= ? AND ts < ?
            ORDER BY ts
        """, (sym_id, int(start_rsi.timestamp()), filed_ts))
        recent_bars = cursor.fetchall()
        
        if len(recent_bars) < 15:
            continue
            
        closes = [bar[1] for bar in recent_bars]
        rsi = compute_rsi(closes, 14)
        if rsi is None or rsi > 70:
            continue
        
        # Check ICSA condition
        # Get the week of filed_ts
        filed_dt = datetime.datetime.utcfromtimestamp(filed_ts)
        filed_week = (filed_dt - datetime.timedelta(days=filed_dt.weekday())).date().isoformat()
        
        if not check_falling_4_weeks(filed_week):
            continue
        
        # All conditions met - this is an opportunity
        opportunities.append((sym_id, filed_ts))
        
        # Check outcome
        if (sym_id, filed_ts) in outcomes:
            up = outcomes[(sym_id, filed_ts)]
            if up:  # label is up (true positive if we predict up)
                issued_calls.append((sym_id, filed_ts, True))
            else:
                issued_calls.append((sym_id, filed_ts, False))
    
    # Split into training and sealed (most recent 20% by time)
    if issued_calls:
        all_timestamps = [call[1] for call in issued_calls]
        cutoff_idx = int(len(all_timestamps) * 0.8)
        cutoff_time = sorted(all_timestamps)[cutoff_idx]
        
        training_calls = [call for call in issued_calls if call[1] <= cutoff_time]
        sealed_calls = [call for call in issued_calls if call[1] > cutoff_time]
    else:
        training_calls = []
        sealed_calls = []
    
    # Compute metrics
    issued_count = len(issued_calls)
    opportunities_count = len(opportunities)
    
    if issued_count == 0:
        print("INSUFFICIENT=1")
        return
    
    hits = sum(1 for call in issued_calls if call[2])
    precision = hits / issued_count
    
    base_rate = hits / issued_count  # within issued subset
    
    distinct_days = len(set(datetime.datetime.utcfromtimestamp(call[1]).date() for call in issued_calls))
    
    # Compute design effect for effective N
    # Group by day
    day_groups = {}
    for call in issued_calls:
        day = datetime.datetime.utcfromtimestamp(call[1]).date()
        if day not in day_groups:
            day_groups[day] = []
        day_groups[day].append(call[2])
    
    # Compute intra-class correlation
    # For binary data
    total_correct = hits
    total_issued = issued_count
    p = total_correct / total_issued
    
    # Variance between days
    day_vars = []
    day_sizes = []
    for day, outcomes in day_groups.items():
        n_d = len(outcomes)
        p_d = sum(outcomes) / n_d
        day_vars.append(n_d * (p_d - p) ** 2)
        day_sizes.append(n_d)
    
    n_days = len(day_groups)
    if n_days > 1:
        var_between = sum(day_vars) / (n_days - 1)
        m0 = total_issued / n_days
        rho = (var_between - p*(1-p)/m0) / (p*(1-p)) if p*(1-p) > 0 else 0
        if rho < 0:
            rho = 0
        deff = 1 + (m0 - 1) * rho
        effective_n = total_issued / deff
    else:
        effective_n = total_issued
    
    # Training metrics
    training_issued = len(training_calls)
    if training_issued > 0:
        training_hits = sum(1 for call in training_calls if call[2])
        training_precision = training_hits / training_issued
    else:
        training_precision = 0.0
    
    # Sealed metrics
    sealed_issued = len(sealed_calls)
    if sealed_issued > 0:
        sealed_hits = sum(1 for call in sealed_calls if call[2])
        sealed_precision = sealed_hits / sealed_issued
    else:
        sealed_precision = 0.0
    
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
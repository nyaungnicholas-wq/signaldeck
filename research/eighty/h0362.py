# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 361
# cycle_index: 29
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cursor = conn.cursor()
    
    # Get all insider purchases with required info
    cursor.execute("""
        SELECT symbol_id, insider, value, tx_ts, filed_ts, code
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    trades = cursor.fetchall()
    
    opportunities = []
    issued_calls = []
    
    for symbol_id, insider, value, tx_ts, filed_ts, code in trades:
        # Check trade-to-disclosure lag <= 5 calendar days
        tx_dt = datetime.utcfromtimestamp(tx_ts)
        filed_dt = datetime.utcfromtimestamp(filed_ts)
        if (filed_dt - tx_dt).days > 5:
            continue
            
        # Check symbol has at least 250 trading days before disclosure date
        disclosure_date = filed_dt.strftime('%Y-%m-%d')
        cursor.execute("""
            SELECT COUNT(*)
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts < ?
        """, (symbol_id, filed_ts))
        bar_count = cursor.fetchone()[0]
        if bar_count < 250:
            continue
        
        # Check insider has no prior open-market purchase
        cursor.execute("""
            SELECT COUNT(*)
            FROM insider_trades
            WHERE symbol_id = ? AND insider = ? AND code = 'P' AND filed_ts < ?
        """, (symbol_id, insider, filed_ts))
        prior_purchases = cursor.fetchone()[0]
        if prior_purchases > 0:
            continue
            
        # Check value >= $50,000
        if value < 50000:
            continue
            
        # Get trailing 20-day average dollar volume through disclosure date
        cursor.execute("""
            SELECT AVG(volume * close)
            FROM (
                SELECT volume, close
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
                ORDER BY ts DESC
                LIMIT 20
            )
        """, (symbol_id, filed_ts))
        avg_dollar_vol = cursor.fetchone()[0]
        if avg_dollar_vol is None or avg_dollar_vol == 0:
            continue
            
        # Check value >= 25% of trailing 20-day average dollar volume
        if value < 0.25 * avg_dollar_vol:
            continue
            
        # Check no other open-market purchase for same symbol on same date
        cursor.execute("""
            SELECT COUNT(*)
            FROM insider_trades
            WHERE symbol_id = ? AND code = 'P' 
            AND date(filed_ts, 'unixepoch') = date(?, 'unixepoch')
        """, (symbol_id, filed_ts))
        same_date_purchases = cursor.fetchone()[0]
        if same_date_purchases > 1:
            continue
            
        opportunities.append((symbol_id, insider, value, tx_ts, filed_ts, code))
        
        # Get the bar timestamp for disclosure date to query prediction_outcomes
        cursor.execute("""
            SELECT ts
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') = date(?, 'unixepoch')
            LIMIT 1
        """, (symbol_id, filed_ts))
        bar_row = cursor.fetchone()
        if bar_row is None:
            continue
            
        bar_ts = bar_row[0]
        
        # Check if we have a label for horizon=21
        cursor.execute("""
            SELECT up
            FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = 21 AND ts = ?
            LIMIT 1
        """, (symbol_id, bar_ts))
        label_row = cursor.fetchone()
        if label_row is None:
            continue
            
        issued_calls.append({
            'symbol_id': symbol_id,
            'insider': insider,
            'value': value,
            'tx_ts': tx_ts,
            'filed_ts': filed_ts,
            'bar_ts': bar_ts,
            'up': label_row[0]
        })
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        conn.close()
        return
        
    # Split into held-out and sealed era (most recent 20% by filed_ts)
    issued_calls.sort(key=lambda x: x['filed_ts'])
    split_idx = int(0.8 * len(issued_calls))
    held_out = issued_calls[:split_idx]
    sealed_era = issued_calls[split_idx:]
    
    # Calculate stats for held-out set
    issued_count = len(held_out)
    if issued_count == 0:
        print("INSUFFICIENT=1")
        conn.close()
        return
        
    opportunities_count = len(opportunities)
    hits = sum(1 for c in held_out if c['up'] == 1)
    precision = hits / issued_count
    base_rate = hits / issued_count  # Same as precision for UP calls
    
    # Count distinct days
    distinct_days = len(set(datetime.utcfromtimestamp(c['filed_ts']).date() for c in held_out))
    
    # Calculate design effect for effective sample size
    # Group by day
    day_groups = {}
    for c in held_out:
        day = datetime.utcfromtimestamp(c['filed_ts']).date()
        if day not in day_groups:
            day_groups[day] = []
        day_groups[day].append(c['up'])
    
    g = len(day_groups)  # Number of clusters
    m = issued_count / g if g > 0 else 0  # Average cluster size
    
    # Calculate ICC
    p = precision  # Overall proportion
    s_b_sq = 0
    s_w_sq = 0
    
    for day, outcomes in day_groups.items():
        n_i = len(outcomes)
        p_i = sum(outcomes) / n_i
        s_b_sq += n_i * (p_i - p) ** 2
        for y in outcomes:
            s_w_sq += (y - p_i) ** 2
    
    if g > 1:
        s_b_sq /= (g - 1)
    if issued_count > g:
        s_w_sq /= (issued_count - g)
    
    if m > 1:
        icc = (s_b_sq - s_w_sq / m) / (s_b_sq + (m - 1) * s_w_sq)
        design_effect = 1 + (m - 1) * icc
    else:
        design_effect = 1
        
    effective_n = issued_count / design_effect
    
    # Sealed era precision
    if sealed_era:
        sealed_hits = sum(1 for c in sealed_era if c['up'] == 1)
        sealed_precision = sealed_hits / len(sealed_era)
    else:
        sealed_precision = 0.0
    
    # Print results
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")
    
    conn.close()

if __name__ == "__main__":
    main()
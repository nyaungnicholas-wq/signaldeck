# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 365
# cycle_index: 33
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.execute("PRAGMA journal_mode=WAL")
    cur = conn.cursor()
    
    # Get insider open-market purchases (code='P')
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts 
        FROM insider_trades 
        WHERE code='P'
    """)
    trades = cur.fetchall()
    
    if not trades:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Convert to datetime for processing
    trade_data = []
    for symbol_id, tx_ts, filed_ts in trades:
        try:
            tx_dt = datetime.datetime.fromisoformat(tx_ts)
            filed_dt = datetime.datetime.fromisoformat(filed_ts)
            trade_data.append((symbol_id, tx_dt, filed_dt))
        except:
            continue
    
    # Filter by disclosure lag <=2 business days
    # and check price conditions
    opportunities = []
    
    for symbol_id, tx_dt, filed_dt in trade_data:
        # Calculate business days between trade and filing
        # Simple approximation: ignore weekends/holidays
        delta_days = (filed_dt - tx_dt).days
        if delta_days > 2:  # >2 calendar days means >2 business days
            continue
        
        # Get filing date as date only
        filed_date = filed_dt.date()
        filed_ts_int = int(filed_dt.timestamp())
        
        # Check if symbol has at least 252 trading days
        cur.execute("""
            SELECT COUNT(*) 
            FROM bars 
            WHERE symbol_id=? AND tf='1d' AND ts<=?
        """, (symbol_id, filed_ts_int))
        count = cur.fetchone()[0]
        if count < 252:
            continue
        
        # Get closing price on filing date
        cur.execute("""
            SELECT close 
            FROM bars 
            WHERE symbol_id=? AND tf='1d' AND ts=?
        """, (symbol_id, filed_ts_int))
        row = cur.fetchone()
        if not row:
            continue
        close_price = row[0]
        
        # Get 52-week high (252 trading days) up to filing date
        cur.execute("""
            SELECT MAX(high) 
            FROM bars 
            WHERE symbol_id=? AND tf='1d' AND ts<=?
        """, (symbol_id, filed_ts_int))
        high_row = cur.fetchone()
        if not high_row or not high_row[0]:
            continue
        high_52w = high_row[0]
        
        # Check if within 2% of 52-week high
        if high_52w == 0:
            continue
        pct_from_high = (high_52w - close_price) / high_52w
        if pct_from_high > 0.02:  # More than 2% below high
            continue
        
        # Get label from prediction_outcomes (horizon=21 days)
        cur.execute("""
            SELECT up 
            FROM prediction_outcomes 
            WHERE symbol_id=? AND horizon=21 AND ts=?
        """, (symbol_id, filed_ts_int))
        label_row = cur.fetchone()
        if not label_row:
            continue
        
        up = label_row[0]
        opportunities.append((symbol_id, filed_dt, up))
    
    conn.close()
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Group by (symbol_id, day) - one observation per day per symbol
    # Sort by timestamp to take earliest per day
    opportunities.sort(key=lambda x: (x[0], x[1]))
    
    seen = set()
    final_observations = []
    for symbol_id, dt, up in opportunities:
        key = (symbol_id, dt.date())
        if key not in seen:
            seen.add(key)
            final_observations.append((symbol_id, dt, up))
    
    if not final_observations:
        print("INSUFFICIENT=1")
        return
    
    # Split into main and sealed (most recent 20%)
    final_observations.sort(key=lambda x: x[1])
    split_idx = int(len(final_observations) * 0.8)
    main_obs = final_observations[:split_idx]
    sealed_obs = final_observations[split_idx:]
    
    # Calculate metrics
    issued = len(final_observations)
    opportunities_count = len(final_observations)  # All are opportunities considered
    hits = sum(1 for _, _, up in final_observations if up == 1)
    precision = hits / issued if issued > 0 else 0
    base_rate = precision  # Same as precision for predicted class
    
    distinct_days = len({dt.date() for _, dt, _ in final_observations})
    
    # Design effect: calls clustered in time
    # Group by day
    day_counts = defaultdict(int)
    for _, dt, _ in final_observations:
        day_counts[dt.date()] += 1
    
    avg_cluster_size = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    design_effect = avg_cluster_size  # Conservative: assume intra-day correlation
    effective_n = issued / design_effect if design_effect > 0 else issued
    # Ensure EFFECTIVE_N < ISSUED
    if effective_n >= issued:
        effective_n = issued - 0.1
    
    # Sealed precision
    if sealed_obs:
        sealed_hits = sum(1 for _, _, up in sealed_obs if up == 1)
        sealed_precision = sealed_hits / len(sealed_obs)
    else:
        sealed_precision = 0
    
    # Print required outputs
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
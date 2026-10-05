# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 356
# cycle_index: 24
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

def count_business_days(start_date, end_date):
    days = 0
    current = start_date
    while current < end_date:
        current += timedelta(days=1)
        if current.weekday() < 5:  # Monday-Friday
            days += 1
    return days

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return
    
    try:
        cursor = conn.cursor()
        
        # Get all insider trades: open-market purchases (code P) with both dates
        cursor.execute("""
            SELECT symbol_id, tx_ts, filed_ts
            FROM insider_trades
            WHERE code = 'P' AND tx_ts IS NOT NULL AND filed_ts IS NOT NULL
        """)
        trades = cursor.fetchall()
        
        if not trades:
            print("INSUFFICIENT=1")
            return
            
        # Get symbol IDs that have daily bars
        cursor.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'")
        symbols_with_bars = set(row[0] for row in cursor.fetchall())
        
        # Prepare decision points
        opportunities = []
        for symbol_id, tx_ts, filed_ts in trades:
            if symbol_id not in symbols_with_bars:
                continue
                
            # Convert timestamps to dates
            trade_date = datetime.utcfromtimestamp(tx_ts).date()
            disclosure_date = datetime.utcfromtimestamp(filed_ts).date()
            
            # Calculate lag in business days
            lag = count_business_days(trade_date, disclosure_date)
            if lag <= 2:
                continue  # Will abstain
                
            # Get prediction outcome for 21-day horizon
            cursor.execute("""
                SELECT up
                FROM prediction_outcomes
                WHERE symbol_id = ? AND ts = ? AND horizon = 21
            """, (symbol_id, filed_ts))
            result = cursor.fetchone()
            if not result:
                continue
                
            up = result[0]
            # up=1 means up, up=0 means down
            actual_down = (up == 0)
            opportunities.append((symbol_id, disclosure_date, actual_down))
            
        if not opportunities:
            print("INSUFFICIENT=1")
            return
            
        # Sort by disclosure date to split into train/sealed
        opportunities.sort(key=lambda x: x[1])
        split_idx = int(len(opportunities) * 0.8)
        train = opportunities[:split_idx]
        sealed = opportunities[split_idx:]
        
        # Calculate metrics for train set
        issued_train = [o for o in train]
        distinct_days_train = set(o[1] for o in issued_train)
        hits_train = sum(1 for o in issued_train if o[2])
        precision_train = hits_train / len(issued_train) if issued_train else 0
        base_rate_train = hits_train / len(issued_train) if issued_train else 0  # Same as precision in this case
        
        # Calculate design effect for train set (cluster by symbol+day)
        clusters_train = defaultdict(int)
        for symbol_id, disclosure_date, actual_down in issued_train:
            clusters_train[(symbol_id, disclosure_date)] += 1
        avg_cluster_size_train = len(issued_train) / len(clusters_train) if clusters_train else 1
        # Simplified design effect: 1 + (avg_cluster_size - 1) * intra_correlation
        # Assume intra_correlation = 0.5 as conservative estimate
        design_effect_train = 1 + (avg_cluster_size_train - 1) * 0.5
        effective_n_train = len(issued_train) / design_effect_train
        
        # Calculate metrics for sealed set
        issued_sealed = [o for o in sealed]
        hits_sealed = sum(1 for o in issued_sealed if o[2])
        precision_sealed = hits_sealed / len(issued_sealed) if issued_sealed else 0
        
        # Output required metrics
        print(f"ISSUED={len(issued_train)}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={precision_train:.4f}")
        print(f"BASE_RATE={base_rate_train:.4f}")
        print(f"DISTINCT_DAYS={len(distinct_days_train)}")
        print(f"EFFECTIVE_N={effective_n_train:.1f}")
        print(f"SEALED_PRECISION={precision_sealed:.4f}")
        
    except Exception:
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
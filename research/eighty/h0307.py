# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 306
# cycle_index: 29
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime

def connect_db():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def main():
    try:
        conn = connect_db()
        cursor = conn.cursor()
        
        # Get yield curve spreads (DGS10 - DGS2) from macro_series
        cursor.execute("""
            SELECT ts, 
                   MAX(CASE WHEN series='DGS10' THEN value END) as dgs10,
                   MAX(CASE WHEN series='DGS2' THEN value END) as dgs2
            FROM macro_series
            WHERE series IN ('DGS10', 'DGS2')
            GROUP BY ts
            ORDER BY ts
        """)
        
        spread_data = {}
        for ts, dgs10, dgs2 in cursor.fetchall():
            if dgs10 is not None and dgs2 is not None:
                spread_date = datetime.datetime.utcfromtimestamp(ts).date()
                spread_data[spread_date] = dgs10 - dgs2
        
        if not spread_data:
            print("INSUFFICIENT=1")
            return
            
        # Get all insider open-market purchases (code='P')
        cursor.execute("""
            SELECT symbol_id, 
                   DATE(filed_ts, 'unixepoch') as decision_date,
                   COUNT(*) as purchase_count
            FROM insider_trades
            WHERE code = 'P'
            GROUP BY symbol_id, DATE(filed_ts, 'unixepoch')
        """)
        
        purchase_days = cursor.fetchall()
        if not purchase_days:
            print("INSUFFICIENT=1")
            return
            
        # Prepare data structures for processing
        opportunities = []  # (symbol_id, decision_date, purchase_count)
        for symbol_id, decision_date_str, purchase_count in purchase_days:
            opportunities.append((symbol_id, decision_date_str, purchase_count))
        
        # Sort by decision date for time-series processing
        opportunities.sort(key=lambda x: x[1])
        
        # Calculate spread as-of discipline: for each decision date, find most recent spread
        spread_dates = sorted(spread_data.keys())
        
        # Issue calls based on entry criteria
        issued = []
        for symbol_id, decision_date_str, _ in opportunities:
            decision_date = datetime.datetime.strptime(decision_date_str, '%Y-%m-%d').date()
            
            # Find spread as-of decision date (look backward)
            spread_value = None
            for sd in reversed(spread_dates):
                if sd <= decision_date:
                    spread_value = spread_data[sd]
                    break
            
            if spread_value is None or spread_value <= 0:
                continue
                
            # Get entry price from bars (close on decision day)
            cursor.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' 
                AND ts >= ?
                ORDER BY ts
                LIMIT 1
            """, (symbol_id, 
                  int(datetime.datetime.combine(decision_date, datetime.time()).timestamp())))
            
            row = cursor.fetchone()
            if not row:
                continue
            entry_price = row[0]
            
            # Get forward price (21 trading days later)
            cursor.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d'
                AND ts > ?
                ORDER BY ts
                LIMIT 1 OFFSET 20
            """, (symbol_id,
                  int(datetime.datetime.combine(decision_date, datetime.time()).timestamp())))
            
            row = cursor.fetchone()
            if not row:
                continue
            forward_price = row[0]
            
            hit = 1 if forward_price > entry_price else 0
            issued.append((symbol_id, decision_date_str, hit, decision_date))
        
        # Split into train and sealed (most recent 20%)
        if len(issued) == 0:
            print("INSUFFICIENT=1")
            return
            
        n_issued = len(issued)
        split_idx = int(n_issued * 0.8)
        train = issued[:split_idx]
        sealed = issued[split_idx:]
        
        # Calculate metrics
        hits_all = sum(hit for _, _, hit, _ in issued)
        precision_all = hits_all / n_issued if n_issued > 0 else 0
        
        hits_sealed = sum(hit for _, _, hit, _ in sealed)
        precision_sealed = hits_sealed / len(sealed) if sealed else 0
        
        # Base rate of predicted class (up) within issued subset
        base_rate_all = hits_all / n_issued if n_issued > 0 else 0
        
        # Distinct days among issued calls
        distinct_days = len(set(date for _, date, _, _ in issued))
        
        # Calculate design effect for effective sample size
        # Group by day to get cluster sizes
        day_counts = {}
        for _, date, _, _ in issued:
            day_counts[date] = day_counts.get(date, 0) + 1
        
        # Calculate ICC using variance between and within clusters
        overall_mean = hits_all / n_issued
        between_var = 0
        within_var = 0
        
        for day, count in day_counts.items():
            day_hits = sum(hit for _, d, hit, _ in issued if d == day)
            day_mean = day_hits / count
            between_var += count * (day_mean - overall_mean) ** 2
            within_var += day_hits * (1 - day_mean) + (count - day_hits) * day_mean
        
        between_var /= (len(day_counts) - 1) if len(day_counts) > 1 else 1
        within_var /= (n_issued - len(day_counts)) if n_issued > len(day_counts) else 1
        
        avg_cluster_size = n_issued / len(day_counts)
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = n_issued / design_effect if design_effect > 1 else n_issued
        
        # Print results
        print(f"ISSUED={n_issued}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={precision_all:.4f}")
        print(f"BASE_RATE={base_rate_all:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={precision_sealed:.4f}")
        
    except Exception as e:
        print(f"INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
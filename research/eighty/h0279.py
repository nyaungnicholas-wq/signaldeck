# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 278
# cycle_index: 1
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timedelta
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Step 1: Get insider purchases (code='P') with filed_ts (disclosure date)
        cur.execute("""
            SELECT symbol_id, insider, filed_ts 
            FROM insider_trades 
            WHERE code='P'
            ORDER BY filed_ts
        """)
        trades = cur.fetchall()
        
        if not trades:
            print("INSUFFICIENT=1")
            return
            
        # Group trades by symbol_id and insider
        symbol_insiders = defaultdict(lambda: defaultdict(list))
        for symbol_id, insider, filed_ts in trades:
            filed_date = datetime.utcfromtimestamp(filed_ts).date()
            symbol_insiders[symbol_id][insider].append(filed_date)
        
        # Step 2: Find entry signals
        entry_signals = []
        
        for symbol_id, insiders in symbol_insiders.items():
            # Flatten all disclosure dates for this symbol
            all_disclosures = []
            for insider, dates in insiders.items():
                for d in dates:
                    all_disclosures.append((d, insider))
            
            # Sort by disclosure date
            all_disclosures.sort(key=lambda x: x[0])
            
            # Find clusters: 3 distinct insiders within 10 calendar days
            i = 0
            while i < len(all_disclosures):
                # Start a new window
                window_start = all_disclosures[i][0]
                window_end = window_start + timedelta(days=9)
                
                # Collect distinct insiders in this window
                distinct_insiders = set()
                first_three_dates = []
                
                for j in range(i, len(all_disclosures)):
                    disclosure_date, insider = all_disclosures[j]
                    if disclosure_date > window_end:
                        break
                    if insider not in distinct_insiders:
                        distinct_insiders.add(insider)
                        first_three_dates.append(disclosure_date)
                        if len(distinct_insiders) >= 3:
                            break
                
                if len(distinct_insiders) >= 3:
                    # Entry is day after third disclosure
                    entry_date = first_three_dates[2] + timedelta(days=1)
                    entry_signals.append((symbol_id, entry_date))
                    # Skip to after this window to avoid overlapping clusters
                    i = j + 1
                else:
                    i += 1
        
        if not entry_signals:
            print("INSUFFICIENT=1")
            return
            
        # Step 3: Get prices and compute outcomes
        opportunities = []
        for symbol_id, entry_date in entry_signals:
            # Convert entry_date to epoch for bars lookup
            entry_epoch = int(entry_date.timestamp())
            
            # Get entry price (close on entry_date)
            cur.execute("""
                SELECT close FROM bars 
                WHERE symbol_id=? AND tf='1d' AND ts>=?
                ORDER BY ts ASC LIMIT 1
            """, (symbol_id, entry_epoch))
            row = cur.fetchone()
            if not row:
                continue
            entry_price = row[0]
            
            # Get exit price 21 trading days later
            # Use date arithmetic: entry_date + 30 calendar days to cover ~21 trading days
            exit_date = entry_date + timedelta(days=30)
            exit_epoch = int(exit_date.timestamp())
            
            cur.execute("""
                SELECT close FROM bars 
                WHERE symbol_id=? AND tf='1d' AND ts>=?
                ORDER BY ts ASC LIMIT 1
            """, (symbol_id, exit_epoch))
            row = cur.fetchone()
            if not row:
                continue
            exit_price = row[0]
            
            # Calculate return
            fwd_return = (exit_price - entry_price) / entry_price
            up = 1 if fwd_return > 0 else 0
            
            opportunities.append((symbol_id, entry_date, up, fwd_return))
        
        if len(opportunities) < 10:
            print("INSUFFICIENT=1")
            return
            
        # Step 4: Hold out most recent 20% as sealed era
        opportunities.sort(key=lambda x: x[1])
        split_idx = int(len(opportunities) * 0.8)
        train = opportunities[:split_idx]
        sealed = opportunities[split_idx:]
        
        # Step 5: Calculate metrics
        def calculate_metrics(data):
            if not data:
                return 0, 0, 0, 0, 0, 0
            
            issued = len(data)
            hits = sum(1 for _, _, up, _ in data if up == 1)
            precision = hits / issued if issued > 0 else 0
            
            # Base rate: proportion of up in issued subset
            base_rate = precision
            
            # Distinct days
            distinct_days = len(set(entry_date for _, entry_date, _, _ in data))
            
            # Design effect calculation (clustering by day)
            # Count calls per day
            day_counts = defaultdict(int)
            for _, entry_date, _, _ in data:
                day_counts[entry_date] += 1
            
            if len(day_counts) == 0:
                design_effect = 1
            else:
                # Calculate ICC using ANOVA method for binary outcomes
                p = hits / issued if issued > 0 else 0
                total_variance = p * (1 - p)
                
                # Calculate variance between days
                between_variance = 0
                for day, count in day_counts.items():
                    day_hits = sum(1 for _, d, up, _ in data if d == day and up == 1)
                    day_p = day_hits / count if count > 0 else 0
                    between_variance += count * (day_p - p) ** 2
                
                between_variance = between_variance / (len(day_counts) - 1) if len(day_counts) > 1 else 0
                
                # Variance within days
                within_variance = 0
                for day, count in day_counts.items():
                    day_hits = sum(1 for _, d, up, _ in data if d == day and up == 1)
                    within_variance += day_hits * (1 - (day_hits / count)) if count > 0 else 0
                
                within_variance = within_variance / (issued - len(day_counts)) if issued > len(day_counts) else 0
                
                # ICC calculation
                if total_variance == 0:
                    icc = 0
                else:
                    icc = between_variance / total_variance if total_variance > 0 else 0
                
                # Average cluster size
                avg_cluster_size = issued / len(day_counts)
                
                # Design effect
                design_effect = 1 + (avg_cluster_size - 1) * icc
            
            effective_n = issued / design_effect if design_effect > 0 else issued
            
            return issued, precision, base_rate, distinct_days, effective_n, design_effect
        
        # Calculate for all opportunities
        all_issued, all_precision, all_base_rate, all_distinct_days, all_effective_n, all_design_effect = calculate_metrics(opportunities)
        
        # Calculate for sealed era
        sealed_issued, sealed_precision, sealed_base_rate, _, _, _ = calculate_metrics(sealed)
        
        # Step 6: Print results
        print(f"ISSUED={all_issued}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={all_precision:.4f}")
        print(f"BASE_RATE={all_base_rate:.4f}")
        print(f"DISTINCT_DAYS={all_distinct_days}")
        print(f"EFFECTIVE_N={all_effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        # Invariants check
        if all_distinct_days > all_issued:
            print("ERROR: DISTINCT_DAYS > ISSUED", file=sys.stderr)
        if all_effective_n >= all_issued:
            print("ERROR: EFFECTIVE_N >= ISSUED", file=sys.stderr)
            
    except Exception as e:
        print("INSUFFICIENT=1", file=sys.stderr)
        sys.exit(0)
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
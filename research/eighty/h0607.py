# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 606
# cycle_index: 1
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
        
        # 1. Get daily yield curve spread (10Y - 2Y) from FRED
        c.execute("""
            SELECT ts, 
                   MAX(CASE WHEN series='DGS10' THEN value END) as y10,
                   MAX(CASE WHEN series='DGS2' THEN value END) as y2
            FROM macro_series
            WHERE series IN ('DGS10', 'DGS2')
            GROUP BY ts
            ORDER BY ts
        """)
        yields = c.fetchall()
        if len(yields) < 5:
            print("INSUFFICIENT=1")
            return 0
        
        # 2. Identify flat curve periods (spread between -0.5% and 0.5% for >=5 consecutive days)
        flat_days = {}  # ts -> bool (is flat)
        spread_values = {}
        consecutive = 0
        flat_periods = []
        
        for ts, y10, y2 in yields:
            if y10 is None or y2 is None:
                spread = None
                is_flat = False
            else:
                spread = (y10 - y2) / 100  # Convert from percentage to decimal
                is_flat = -0.005 <= spread <= 0.005
            
            spread_values[ts] = spread
            flat_days[ts] = is_flat
            
            if is_flat:
                consecutive += 1
                if consecutive >= 5:
                    flat_periods.append(ts)
            else:
                consecutive = 0
        
        if not flat_periods:
            print("INSUFFICIENT=1")
            return 0
        
        # 3. Create a set of all flat days for quick lookup
        flat_set = set()
        consecutive = 0
        all_ts = sorted(spread_values.keys())
        i = 0
        while i < len(all_ts):
            ts = all_ts[i]
            if flat_days.get(ts, False):
                consecutive += 1
                flat_set.add(ts)
                i += 1
            else:
                consecutive = 0
                i += 1
                # Skip consecutive non-flat days
        
        if not flat_set:
            print("INSUFFICIENT=1")
            return 0
        
        # 4. Get insider purchases (code='P') with proper as-of using filed_ts
        c.execute("""
            SELECT symbol_id, 
                   CAST(filed_ts / 86400 AS INTEGER) as file_day
            FROM insider_trades
            WHERE code = 'P'
            ORDER BY file_day
        """)
        insider_purchases = c.fetchall()
        if not insider_purchases:
            print("INSUFFICIENT=1")
            return 0
        
        # 5. Get daily bars for computing labels
        c.execute("""
            SELECT symbol_id, ts, close
            FROM bars
            WHERE tf = '1d'
            ORDER BY symbol_id, ts
        """)
        bars = c.fetchall()
        if not bars:
            print("INSUFFICIENT=1")
            return 0
        
        # Index bars by symbol_id and ts
        bars_dict = {}
        for sym, ts, close in bars:
            if sym not in bars_dict:
                bars_dict[sym] = {}
            bars_dict[sym][ts] = close
        
        # 6. Process opportunities: (symbol, decision_day) where curve is flat and insider purchase exists
        opportunities = []
        for sym_id, file_day in insider_purchases:
            if file_day in flat_set:
                # Compute 21-day forward return
                sym_bars = bars_dict.get(sym_id, {})
                close_today = sym_bars.get(file_day)
                if close_today is None:
                    continue
                
                # Find close 21 trading days later
                forward_days = sorted([d for d in sym_bars.keys() if d > file_day])[:21]
                if len(forward_days) < 21:
                    continue
                
                close_forward = sym_bars[forward_days[-1]]
                if close_forward is None or close_today == 0:
                    continue
                
                fwd_return = (close_forward - close_today) / close_today
                label = 1 if fwd_return > 0 else 0
                
                opportunities.append({
                    'symbol_id': sym_id,
                    'decision_day': file_day,
                    'label': label,
                    'fwd_return': fwd_return,
                    'close_today': close_today
                })
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return 0
        
        # 7. Hold out most recent 20% as sealed era
        decision_days = sorted(set(opp['decision_day'] for opp in opportunities))
        n_days = len(decision_days)
        if n_days == 0:
            print("INSUFFICIENT=1")
            return 0
        
        cutoff_idx = int(n_days * 0.8)
        cutoff_day = decision_days[cutoff_idx] if cutoff_idx < n_days else decision_days[-1]
        
        main_era = [opp for opp in opportunities if opp['decision_day'] < cutoff_day]
        sealed_era = [opp for opp in opportunities if opp['decision_day'] >= cutoff_day]
        
        if not main_era or not sealed_era:
            print("INSUFFICIENT=1")
            return 0
        
        # 8. Compute metrics for main era
        issued = len(main_era)
        opportunities_count = len(opportunities)  # All opportunities considered
        hits = sum(opp['label'] for opp in main_era)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate: proportion of positive labels within issued calls
        base_rate = precision  # Same as precision in this case
        
        distinct_days = len(set(opp['decision_day'] for opp in main_era))
        
        # 9. Compute design effect and effective sample size
        # Group calls by day
        day_counts = {}
        for opp in main_era:
            d = opp['decision_day']
            day_counts[d] = day_counts.get(d, 0) + 1
        
        m = len(day_counts)
        N = issued
        
        # Compute variance of calls per day
        if m > 1:
            mean_calls = N / m
            var_calls = sum((cnt - mean_calls)**2 for cnt in day_counts.values()) / (m - 1)
            icc_proxy = var_calls / (mean_calls**2) * (N / (N - 1))
            design_effect = 1 + (mean_calls - 1) * icc_proxy
        else:
            design_effect = N
        
        effective_n = N / design_effect if design_effect > 0 else 0
        
        # 10. Compute sealed era precision
        sealed_issued = len(sealed_era)
        sealed_hits = sum(opp['label'] for opp in sealed_era)
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # 11. Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
        conn.close()
        return 0
        
    except Exception as e:
        print(f"INSUFFICIENT=1", file=sys.stderr)
        return 0

if __name__ == "__main__":
    sys.exit(main())
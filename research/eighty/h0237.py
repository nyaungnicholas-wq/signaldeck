import sqlite3
from datetime import datetime
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Get universe of symbols with both daily bars and short_volume
        cursor.execute("""
            SELECT DISTINCT b.symbol_id 
            FROM bars b 
            WHERE b.tf = '1d'
            AND EXISTS (SELECT 1 FROM short_volume sv WHERE sv.symbol_id = b.symbol_id)
        """)
        symbols = [row[0] for row in cursor.fetchall()]
        
        if len(symbols) == 0:
            print("INSUFFICIENT=1")
            return
            
        # Get all relevant data for these symbols
        cursor.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf = '1d' AND symbol_id IN ({})".format(
            ','.join(['?'] * len(symbols))), symbols)
        ts_min, ts_max = cursor.fetchone()
        
        # Get all daily bars for universe
        cursor.execute("""
            SELECT symbol_id, ts, close, volume
            FROM bars
            WHERE tf = '1d'
            AND symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join(['?'] * len(symbols))), symbols)
        bars_data = cursor.fetchall()
        
        # Get all short_volume data
        cursor.execute("""
            SELECT symbol_id, day, short_vol, total_vol
            FROM short_volume
            WHERE symbol_id IN ({})
            ORDER BY symbol_id, day
        """.format(','.join(['?'] * len(symbols))), symbols)
        short_data = cursor.fetchall()
        
        # Get prediction outcomes for horizon 10
        cursor.execute("""
            SELECT symbol_id, ts, up
            FROM prediction_outcomes
            WHERE horizon = 10
            AND symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join(['?'] * len(symbols))), symbols)
        outcomes = cursor.fetchall()
        
        # Organize data by symbol
        bars_by_symbol = defaultdict(list)
        for sid, ts, close, vol in bars_data:
            bars_by_symbol[sid].append((ts, close, vol))
        
        short_by_symbol = defaultdict(dict)
        for sid, day, short_vol, total_vol in short_data:
            short_by_symbol[sid][day] = (short_vol, total_vol)
        
        outcomes_by_symbol = defaultdict(dict)
        for sid, ts, up in outcomes:
            outcomes_by_symbol[sid][ts] = up
        
        # Convert timestamp to date string for short_volume matching
        def ts_to_date(ts):
            return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        
        # Process each symbol to find signals
        all_signals = []
        opportunities = 0
        
        for sid in symbols:
            bar_list = bars_by_symbol.get(sid, [])
            short_dict = short_by_symbol.get(sid, {})
            outcome_dict = outcomes_by_symbol.get(sid, {})
            
            if len(bar_list) < 262:  # Need 252 prior + 10 forward
                continue
                
            for i in range(252, len(bar_list) - 10):
                opportunities += 1
                
                T_ts, T_close, T_vol = bar_list[i]
                T_date = ts_to_date(T_ts)
                
                # Check basic filters
                if T_close < 5:
                    continue
                    
                # Check we have short_volume for T
                if T_date not in short_dict:
                    continue
                    
                # Calculate returns
                # 20-session return through T
                if i < 21:
                    continue
                T_20_close = bar_list[i-20][1]
                ret_20 = (T_close - T_20_close) / T_20_close
                
                if not (-0.15 <= ret_20 <= -0.05):
                    continue
                    
                # Close-to-close return at T
                T_prev_close = bar_list[i-1][1]
                ret_1d = (T_close - T_prev_close) / T_prev_close
                
                if not (-0.005 <= ret_1d <= 0.005):
                    continue
                    
                # Calculate 20-session realized volatility
                returns_20 = []
                for j in range(i-19, i+1):
                    closes = [bar_list[k][1] for k in range(j, j+2)]
                    if len(closes) == 2:
                        returns_20.append((closes[1] - closes[0]) / closes[0])
                
                if len(returns_20) < 20:
                    continue
                    
                vol_20 = (sum((r - sum(returns_20)/len(returns_20))**2 for r in returns_20) / (len(returns_20)-1))**0.5
                
                # Check volatility not in top decile cross-sectionally (we'll check later)
                # Store for cross-sectional check
                if not hasattr(main, 'vol_20_by_date'):
                    main.vol_20_by_date = defaultdict(list)
                main.vol_20_by_date[T_date].append(vol_20)
                
                # Calculate 60-day trailing short volume ratio distribution
                short_ratios_60 = []
                for j in range(max(0, i-59), i+1):
                    d = ts_to_date(bar_list[j][0])
                    if d in short_dict:
                        short_vol, total_vol = short_dict[d]
                        if total_vol > 0:
                            short_ratios_60.append(short_vol / total_vol)
                
                if len(short_ratios_60) < 30:
                    continue
                    
                # Current T's short volume ratio
                T_short_vol, T_total_vol = short_dict[T_date]
                if T_total_vol == 0:
                    continue
                T_ratio = T_short_vol / T_total_vol
                
                # Check if in top 5% of its 60-day distribution
                sorted_ratios = sorted(short_ratios_60)
                percentile = (sum(1 for r in sorted_ratios if r <= T_ratio) / len(sorted_ratios))
                if percentile < 0.95:
                    continue
                    
                # Store signal candidate
                all_signals.append((sid, T_ts, T_date, vol_20))
        
        # Check cross-sectional volatility condition
        if not hasattr(main, 'vol_20_by_date') or len(main.vol_20_by_date) == 0:
            print("INSUFFICIENT=1")
            return
            
        filtered_signals = []
        for sid, ts, date, vol_20 in all_signals:
            vols = main.vol_20_by_date[date]
            if len(vols) < 10:
                continue
            sorted_vols = sorted(vols)
            threshold_idx = int(len(sorted_vols) * 0.9)
            if vol_20 <= sorted_vols[threshold_idx]:
                filtered_signals.append((sid, ts))
        
        if len(filtered_signals) < 30:
            print("INSUFFICIENT=1")
            return
            
        # Get outcomes for signals
        hits = 0
        issued_days = set()
        signal_details = []
        
        for sid, ts in filtered_signals:
            outcome = outcomes_by_symbol.get(sid, {}).get(ts)
            if outcome is not None:
                issued_days.add(ts)
                hits += 1 if outcome else 0
                signal_details.append((ts, outcome))
        
        if len(issued_days) == 0:
            print("INSUFFICIENT=1")
            return
            
        # Split into regular and sealed era (most recent 20%)
        signal_details.sort(key=lambda x: x[0])
        sealed_cutoff_idx = int(len(signal_details) * 0.8)
        sealed_signals = signal_details[sealed_cutoff_idx:]
        regular_signals = signal_details[:sealed_cutoff_idx]
        
        # Calculate metrics
        issued = len(signal_details)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0
        
        # Calculate distinct days (only among issued)
        distinct_days = len(issued_days)
        
        # Calculate design effect (clustering by day)
        day_counts = defaultdict(int)
        for ts, _ in signal_details:
            day_counts[ts] += 1
        
        n_days = len(day_counts)
        if n_days == 0:
            design_effect = 1
        else:
            avg_calls_per_day = issued / n_days
            variance_calls = sum((count - avg_calls_per_day)**2 for count in day_counts.values()) / n_days
            design_effect = 1 + (variance_calls / avg_calls_per_day) if avg_calls_per_day > 0 else 1
        
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Sealed era metrics
        sealed_hits = sum(1 for _, outcome in sealed_signals if outcome)
        sealed_precision = sealed_hits / len(sealed_signals) if len(sealed_signals) > 0 else 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n}")
        print(f"SEALED_PRECISION={sealed_precision}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
        
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
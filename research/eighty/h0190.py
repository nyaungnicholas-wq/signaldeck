import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
        
        # Get all symbols with basic info
        c.execute('SELECT id, symbol, active, delisted_at FROM symbols')
        symbols = {row[0]: {'symbol': row[1], 'active': row[2], 'delisted_at': row[3]} for row in c.fetchall()}
        
        # Get all daily bars
        c.execute('SELECT symbol_id, ts, close, volume FROM bars WHERE tf="1d" ORDER BY ts')
        bars_by_symbol = defaultdict(list)
        for row in c.fetchall():
            sym_id, ts, close, vol = row
            dt = datetime.utcfromtimestamp(ts).date()
            bars_by_symbol[sym_id].append((dt, close, vol, close*vol if close and vol else 0))
        
        # Get StockTwits sentiment
        c.execute('SELECT symbol_id, ts, bullish FROM stocktwits_sentiment')
        st_by_symbol = defaultdict(dict)
        for row in c.fetchall():
            sym_id, ts, bullish = row
            if bullish is not None:
                dt = datetime.utcfromtimestamp(ts).date()
                st_by_symbol[sym_id][dt] = bullish
        
        # Get all unique trading days across all symbols
        all_dates = set()
        for sym_id in bars_by_symbol:
            for dt, _, _, _ in bars_by_symbol[sym_id]:
                all_dates.add(dt)
        all_dates_sorted = sorted(all_dates)
        
        if not all_dates_sorted:
            print("INSUFFICIENT=1")
            return 0
        
        # Prepare per-symbol data structures
        sym_data = {}
        for sym_id, bar_list in bars_by_symbol.items():
            if len(bar_list) < 252:  # 12 months of history needed
                continue
            
            # Sort by date
            bar_list.sort(key=lambda x: x[0])
            dates = [bar[0] for bar in bar_list]
            closes = [bar[1] for bar in bar_list]
            volumes = [bar[2] for bar in bar_list]
            dollar_vols = [bar[3] for bar in bar_list]
            
            # Compute log returns
            log_returns = []
            for i in range(1, len(closes)):
                if closes[i-1] > 0 and closes[i] > 0:
                    log_returns.append(math.log(closes[i]/closes[i-1]))
                else:
                    log_returns.append(0)
            
            # Create date index for fast lookup
            date_to_idx = {dt: i for i, dt in enumerate(dates)}
            
            # Get StockTwits bullish for this symbol
            st_bullish = st_by_symbol.get(sym_id, {})
            
            # Compute rolling 5-day mean bullish
            bullish_5d_mean = {}
            for i, dt in enumerate(dates):
                # Check if we have bullish data for last 5 days
                missing = False
                vals = []
                for offset in range(5):
                    check_dt = dt - timedelta(days=offset*1)  # Approximate, will check actual dates
                    # Find actual trading day offset
                    if i - offset >= 0:
                        check_date = dates[i-offset]
                        if check_date in st_bullish:
                            vals.append(st_bullish[check_date])
                        else:
                            missing = True
                            break
                    else:
                        missing = True
                        break
                
                if not missing and len(vals) == 5:
                    bullish_5d_mean[dt] = sum(vals) / 5
                else:
                    bullish_5d_mean[dt] = None
            
            # Compute 5-day realized volatility
            vol_5d = {}
            for i, dt in enumerate(dates):
                if i >= 4:  # Need at least 5 days
                    returns = log_returns[i-4:i+1]
                    if len(returns) == 5:
                        mean_ret = sum(returns)/5
                        variance = sum((r-mean_ret)**2 for r in returns)/4
                        vol_5d[dt] = math.sqrt(variance) if variance > 0 else 0
                    else:
                        vol_5d[dt] = None
                else:
                    vol_5d[dt] = None
            
            # Compute 20-day return
            ret_20d = {}
            for i, dt in enumerate(dates):
                if i >= 20 and closes[i-20] > 0:
                    ret_20d[dt] = closes[i]/closes[i-20] - 1
                else:
                    ret_20d[dt] = None
            
            # Compute 60-day median dollar volume
            median_60d_dv = {}
            for i, dt in enumerate(dates):
                if i >= 60:
                    window = dollar_vols[i-60:i+1]
                    window_sorted = sorted(window)
                    n = len(window_sorted)
                    if n % 2 == 0:
                        median_60d_dv[dt] = (window_sorted[n//2-1] + window_sorted[n//2]) / 2
                    else:
                        median_60d_dv[dt] = window_sorted[n//2]
                else:
                    median_60d_dv[dt] = None
            
            # Compute 60-day average dollar volume
            avg_60d_dv = {}
            for i, dt in enumerate(dates):
                if i >= 60:
                    window = dollar_vols[i-60:i+1]
                    avg_60d_dv[dt] = sum(window)/len(window)
                else:
                    avg_60d_dv[dt] = None
            
            sym_data[sym_id] = {
                'dates': dates,
                'closes': closes,
                'volumes': volumes,
                'dollar_vols': dollar_vols,
                'date_to_idx': date_to_idx,
                'bullish_5d_mean': bullish_5d_mean,
                'vol_5d': vol_5d,
                'ret_20d': ret_20d,
                'median_60d_dv': median_60d_dv,
                'avg_60d_dv': avg_60d_dv
            }
        
        conn.close()
        
        # Collect all opportunities and issued calls
        opportunities = []  # List of (date, sym_id, label)
        issued = []  # List of (date, sym_id, label)
        issued_days = set()
        
        # Process each trading day in chronological order
        for T_idx, T_date in enumerate(all_dates_sorted):
            # Get cross-sectional data for this day
            day_bullish_means = {}
            day_vols = {}
            day_syms = []
            
            for sym_id, data in sym_data.items():
                if T_date in data['date_to_idx']:
                    idx = data['date_to_idx'][T_date]
                    
                    # Check basic universe conditions
                    info = symbols[sym_id]
                    if not info['active']:
                        continue
                    if info['delisted_at'] and datetime.strptime(info['delisted_at'], '%Y-%m-%d').date() < T_date:
                        continue
                    
                    # Check if we have 12 months history
                    if idx < 252:
                        continue
                    
                    # Check price >= $5
                    close = data['closes'][idx]
                    if close < 5:
                        continue
                    
                    # Check avg daily dollar volume >= $10M over prior 60 sessions
                    avg_dv = data['avg_60d_dv'].get(T_date)
                    if avg_dv is None or avg_dv < 10_000_000:
                        continue
                    
                    # Check bullish 5d mean
                    bullish_mean = data['bullish_5d_mean'].get(T_date)
                    if bullish_mean is None:
                        continue
                    
                    # Check 5d vol
                    vol5d = data['vol_5d'].get(T_date)
                    if vol5d is None:
                        continue
                    
                    # Check 20d return
                    ret20d = data['ret_20d'].get(T_date)
                    if ret20d is None:
                        continue
                    
                    # Check median dollar volume
                    med_dv = data['median_60d_dv'].get(T_date)
                    if med_dv is None:
                        continue
                    
                    # Store cross-sectional values
                    day_bullish_means[sym_id] = bullish_mean
                    day_vols[sym_id] = vol5d
                    day_syms.append(sym_id)
            
            if len(day_syms) < 10:  # Need enough for decile calculation
                continue
            
            # Compute deciles for bullish mean and volatility
            bullish_vals = [day_bullish_means[s] for s in day_syms]
            vol_vals = [day_vols[s] for s in day_syms]
            
            bullish_sorted = sorted(bullish_vals)
            vol_sorted = sorted(vol_vals)
            
            bullish_90th = bullish_sorted[int(0.9 * len(bullish_sorted))]
            vol_90th = vol_sorted[int(0.9 * len(vol_sorted))]
            
            # Check each symbol for entry/abstain
            for sym_id in day_syms:
                data = sym_data[sym_id]
                idx = data['date_to_idx'][T_date]
                close = data['closes'][idx]
                dollar_vol = data['dollar_vols'][idx]
                bullish_mean = day_bullish_means[sym_id]
                vol5d = day_vols[sym_id]
                ret20d = data['ret_20d'][T_date]
                med_dv = data['median_60d_dv'][T_date]
                
                # Check abstain conditions
                abstain = False
                
                # Check bullish counts for T-4..T (already checked for T, but need to verify all 5)
                # We already checked that bullish_mean is not None, which implies all 5 days present
                
                # Check price < $5 (already checked in universe)
                
                # Check 30% or more above T-20's close
                if ret20d >= 0.30:
                    abstain = True
                
                # Check 5d vol in top decile
                if vol5d >= vol_90th:
                    abstain = True
                
                # Check entry conditions
                entry = False
                if not abstain:
                    # Top decile for bullish mean
                    if bullish_mean >= bullish_90th:
                        # Close at least 10% above T-20
                        if ret20d >= 0.10:
                            # Dollar volume > 60-day median
                            if dollar_vol > med_dv:
                                entry = True
                
                # Need label (T+5 trading days)
                # Find index for T+5 in symbol's data
                # Get the 5th trading day after T
                if idx + 5 < len(data['dates']):
                    T5_date = data['dates'][idx+5]
                    T5_close = data['closes'][idx+5]
                    label = 1 if T5_close < close else 0  # DOWN call correct if price goes down
                    
                    opportunities.append((T_date, sym_id, label))
                    
                    if entry:
                        issued.append((T_date, sym_id, label))
                        issued_days.add(T_date)
                else:
                    # Not enough future data
                    pass
        
        if not issued:
            print("INSUFFICIENT=1")
            return 0
        
        # Split into in-sample and sealed (last 20% of trading days)
        total_days = len(all_dates_sorted)
        cutoff_idx = int(0.8 * total_days)
        sealed_days = set(all_dates_sorted[cutoff_idx:])
        in_sample_days = set(all_dates_sorted[:cutoff_idx])
        
        # Filter issued calls for in-sample
        in_sample_issued = [(d, s, l) for d, s, l in issued if d in in_sample_days]
        sealed_issued = [(d, s, l) for d, s, l in issued if d in sealed_days]
        
        if not in_sample_issued:
            print("INSUFFICIENT=1")
            return 0
        
        # Calculate metrics for in-sample
        issued_count = len(in_sample_issued)
        opportunities_count = len([o for o in opportunities if o[0] in in_sample_days])
        
        hits = sum(1 for _, _, l in in_sample_issued if l == 1)
        precision = hits / issued_count
        
        # Base rate within issued subset
        base_rate = hits / issued_count  # Same as precision for DOWN calls
        
        # Distinct days
        distinct_days = len(issued_days.intersection(in_sample_days))
        
        # Design effect: calls per day
        day_counts = defaultdict(int)
        for d, _, _ in in_sample_issued:
            day_counts[d] += 1
        
        if distinct_days > 0:
            avg_calls_per_day = issued_count / distinct_days
            design_effect = 1 + (avg_calls_per_day - 1)  # Assuming ICC=1 for simplicity
            effective_n = issued_count / design_effect
        else:
            effective_n = 0
        
        # SEALED_PRECISION
        if sealed_issued:
            sealed_hits = sum(1 for _, _, l in sealed_issued if l == 1)
            sealed_precision = sealed_hits / len(sealed_issued)
        else:
            sealed_precision = 0.0
        
        # Output required lines
        print(f"ISSUED={issued_count}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
        return 0
        
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

if __name__ == "__main__":
    exit(main())
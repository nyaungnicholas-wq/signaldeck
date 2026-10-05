import sqlite3
import math
from collections import defaultdict
from datetime import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Check for minimum required data
        cursor.execute("SELECT COUNT(*) FROM symbols WHERE market='stocks'")
        stock_count = cursor.fetchone()[0]
        if stock_count < 50:
            print("INSUFFICIENT=1")
            return
        
        cursor.execute("SELECT COUNT(DISTINCT symbol_id) FROM bars WHERE tf='1d'")
        bar_symbols = cursor.fetchone()[0]
        if bar_symbols < 100:
            print("INSUFFICIENT=1")
            return
            
        cursor.execute("SELECT COUNT(DISTINCT symbol_id) FROM sentiment_features")
        sentiment_symbols = cursor.fetchone()[0]
        if sentiment_symbols < 100:
            print("INSUFFICIENT=1")
            return
        
        # Get all trading days from daily bars
        cursor.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
        all_days = [row[0] for row in cursor.fetchall()]
        
        # Find most recent 20% cutoff
        cutoff_idx = int(len(all_days) * 0.8)
        cutoff_ts = all_days[cutoff_idx] if cutoff_idx < len(all_days) else all_days[-1]
        
        # Get insider trades (open market sales) with filed timestamps
        cursor.execute("""
            SELECT symbol_id, filed_ts 
            FROM insider_trades 
            WHERE code='S'
        """)
        insider_sales = defaultdict(list)
        for symbol_id, filed_ts in cursor.fetchall():
            insider_sales[symbol_id].append(filed_ts)
        
        # Get sentiment data by symbol and day (converted to epoch)
        cursor.execute("""
            SELECT symbol_id, 
                   strftime('%s', day) as day_ts,
                   mean_score
            FROM sentiment_features
        """)
        sentiment_by_symbol_day = defaultdict(dict)
        for symbol_id, day_ts, score in cursor.fetchall():
            if score is not None:
                sentiment_by_symbol_day[symbol_id][int(day_ts)] = score
        
        # Get prediction outcomes for T+5 horizon
        cursor.execute("""
            SELECT symbol_id, ts, up
            FROM prediction_outcomes
            WHERE horizon=5
        """)
        labels = {}
        for symbol_id, ts, up in cursor.fetchall():
            labels[(symbol_id, ts)] = up
        
        # Process each symbol
        issued = []
        opportunities = 0
        last_call_day = {}
        
        # Get list of symbols with sufficient data
        cursor.execute("""
            SELECT DISTINCT b.symbol_id 
            FROM bars b 
            JOIN sentiment_features sf ON b.symbol_id = sf.symbol_id
            WHERE b.tf='1d'
        """)
        symbols = [row[0] for row in cursor.fetchall()]
        
        for symbol_id in symbols:
            # Get daily bars for this symbol
            cursor.execute("""
                SELECT ts, close, volume, open, high, low
                FROM bars 
                WHERE symbol_id=? AND tf='1d'
                ORDER BY ts
            """, (symbol_id,))
            bars = cursor.fetchall()
            
            if len(bars) < 253:
                continue
                
            # Build day-indexed data
            day_data = {}
            for ts, close, volume, open_, high, low in bars:
                day_data[ts] = {
                    'close': close, 'volume': volume, 'open': open_,
                    'high': high, 'low': low
                }
            
            # Get sorted timestamps for this symbol
            timestamps = sorted(day_data.keys())
            
            # Calculate 20-day rolling volatility for all days
            vol_20day = {}
            for i in range(20, len(timestamps)):
                recent = [day_data[timestamps[j]]['close'] 
                         for j in range(i-20, i)]
                if len(recent) >= 20:
                    # Calculate daily returns
                    returns = []
                    for j in range(1, len(recent)):
                        if recent[j-1] > 0:
                            returns.append((recent[j] - recent[j-1]) / recent[j-1])
                    if len(returns) >= 19:
                        mean_ret = sum(returns) / len(returns)
                        variance = sum((r - mean_ret)**2 for r in returns) / (len(returns) - 1)
                        vol_20day[timestamps[i]] = math.sqrt(variance)
            
            # Calculate cross-sectional volatility deciles per day
            daily_vol_deciles = defaultdict(list)
            for ts, vol in vol_20day.items():
                daily_vol_deciles[ts].append(vol)
            
            # Process each potential T (starting from 253rd day)
            for t_idx in range(252, len(timestamps)):
                T = timestamps[t_idx]
                
                # Check price >= $5
                if day_data[T]['close'] < 5:
                    continue
                
                # Check close-to-close return
                if t_idx == 0:
                    continue
                prev_close = day_data[timestamps[t_idx-1]]['close']
                if prev_close <= 0:
                    continue
                daily_return = (day_data[T]['close'] - prev_close) / prev_close
                
                # Skip if return outside [-0.5%, +0.5%]
                if abs(daily_return) > 0.005:
                    continue
                
                # Check average daily dollar volume >= $5M over T-60..T-1
                volume_sum = 0
                count_days = 0
                for i in range(max(0, t_idx-60), t_idx):
                    ts_vol = timestamps[i]
                    volume_sum += day_data[ts_vol]['close'] * day_data[ts_vol]['volume']
                    count_days += 1
                
                if count_days > 0:
                    avg_dollar_volume = volume_sum / count_days
                    if avg_dollar_volume < 5_000_000:
                        continue
                
                # Check we have sentiment data for T-2..T
                has_sentiment = True
                for offset in range(-2, 1):
                    if t_idx + offset < 0 or t_idx + offset >= len(timestamps):
                        has_sentiment = False
                        break
                    ts_check = timestamps[t_idx + offset]
                    if symbol_id not in sentiment_by_symbol_day or ts_check not in sentiment_by_symbol_day[symbol_id]:
                        has_sentiment = False
                        break
                
                if not has_sentiment:
                    continue
                
                # Check we have sentiment data for T-252..T-1 for 10th percentile calculation
                sentiment_missing = False
                for i in range(max(0, t_idx-252), t_idx):
                    ts_check = timestamps[i]
                    if symbol_id not in sentiment_by_symbol_day or ts_check not in sentiment_by_symbol_day[symbol_id]:
                        sentiment_missing = True
                        break
                
                if sentiment_missing:
                    continue
                
                # Calculate 3-day mean sentiment for T-2..T
                sentiment_3day_T = 0
                for offset in range(-2, 1):
                    ts_check = timestamps[t_idx + offset]
                    sentiment_3day_T += sentiment_by_symbol_day[symbol_id][ts_check]
                sentiment_3day_T /= 3
                
                # Calculate 3-day means for T-252..T-1
                three_day_means = []
                for i in range(max(2, t_idx-252), t_idx):
                    if i - 2 >= 0:
                        mean_sum = 0
                        for offset in range(-2, 1):
                            ts_check = timestamps[i + offset]
                            if ts_check in sentiment_by_symbol_day[symbol_id]:
                                mean_sum += sentiment_by_symbol_day[symbol_id][ts_check]
                            else:
                                mean_sum = None
                                break
                        if mean_sum is not None:
                            three_day_means.append(mean_sum / 3)
                
                if len(three_day_means) < 30:
                    continue
                
                # Calculate 10th percentile
                sorted_means = sorted(three_day_means)
                p10_idx = int(len(sorted_means) * 0.1)
                p10_value = sorted_means[p10_idx] if p10_idx < len(sorted_means) else None
                
                if p10_value is None or sentiment_3day_T >= p10_value:
                    continue
                
                # Check insider sale in prior 20 trading days
                if symbol_id in insider_sales:
                    # Convert 20 trading days to calendar days (approximate)
                    twenty_days_ago = T - (20 * 86400)  # rough approximation
                    has_insider_sale = any(
                        twenty_days_ago <= filed_ts <= T 
                        for filed_ts in insider_sales[symbol_id]
                    )
                    if not has_insider_sale:
                        continue
                else:
                    continue
                
                # Check 20-day realized volatility not in top decile
                if T in vol_20day:
                    current_vol = vol_20day[T]
                    # Get all volatilities for this day
                    same_day_vols = daily_vol_deciles.get(T, [])
                    if same_day_vols:
                        same_day_vols_sorted = sorted(same_day_vols)
                        top_decile_idx = int(len(same_day_vols_sorted) * 0.9)
                        top_decile_val = same_day_vols_sorted[top_decile_idx] if top_decile_idx < len(same_day_vols_sorted) else float('inf')
                        if current_vol >= top_decile_val:
                            continue
                    else:
                        continue
                else:
                    continue
                
                # Check no call for same symbol in prior 10 trading days
                if symbol_id in last_call_day:
                    days_since_last = (T - last_call_day[symbol_id]) / 86400
                    if days_since_last < 10:
                        continue
                
                # Check we have label
                if (symbol_id, T) not in labels:
                    continue
                
                # All conditions met - issue call
                opportunities += 1
                call = {
                    'symbol_id': symbol_id,
                    'ts': T,
                    'date': datetime.utcfromtimestamp(T).strftime('%Y-%m-%d'),
                    'sealed': T >= cutoff_ts,
                    'label': labels[(symbol_id, T)]
                }
                issued.append(call)
                last_call_day[symbol_id] = T
        
        conn.close()
        
        # Check minimum observations
        non_sealed = [call for call in issued if not call['sealed']]
        if len(non_sealed) < 30:
            print("INSUFFICIENT=1")
            return
        
        # Calculate metrics
        total_issued = len(issued)
        total_opportunities = opportunities
        
        if total_issued == 0:
            print("INSUFFICIENT=1")
            return
        
        # Precision
        hits = sum(1 for call in issued if call['label'] == 1)
        precision = hits / total_issued
        
        # Base rate
        up_labels = sum(1 for call in issued if call['label'] == 1)
        base_rate = up_labels / total_issued
        
        # Distinct days
        distinct_days = len(set(call['date'] for call in issued))
        
        # Design effect
        day_counts = defaultdict(int)
        for call in issued:
            day_counts[call['date']] += 1
        
        total_clusters = len(day_counts)
        if total_clusters > 1:
            avg_cluster_size = total_issued / total_clusters
            # Simplified design effect calculation
            sum_sq = sum(count**2 for count in day_counts.values())
            design_effect = (sum_sq * total_clusters) / (total_issued**2)
            if design_effect < 1:
                design_effect = 1.0
        else:
            design_effect = total_issued
        
        effective_n = total_issued / design_effect
        
        # Sealed precision
        sealed_calls = [call for call in issued if call['sealed']]
        if sealed_calls:
            sealed_hits = sum(1 for call in sealed_calls if call['label'] == 1)
            sealed_precision = sealed_hits / len(sealed_calls)
        else:
            sealed_precision = 0.0
        
        # Print results
        print(f"ISSUED={total_issued}")
        print(f"OPPORTUNITIES={total_opportunities}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
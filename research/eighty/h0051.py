import sqlite3
import statistics
from datetime import datetime, timezone
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Check if we have any dividend cut events in regime_outcomes
    cur.execute("SELECT DISTINCT kind FROM regime_outcomes")
    kinds = [row[0] for row in cur.fetchall()]
    if 'dividend_cut' not in kinds:
        print("INSUFFICIENT=1")
        return
    
    # Get all dividend cut events
    cur.execute("""
        SELECT symbol_id, ts, day 
        FROM regime_outcomes 
        WHERE kind = 'dividend_cut'
    """)
    events = cur.fetchall()
    
    if len(events) < 100:
        print("INSUFFICIENT=1")
        return
    
    # Get symbol info for filtering
    cur.execute("""
        SELECT id, symbol, market 
        FROM symbols 
        WHERE market = 'stocks'
    """)
    symbols_info = {row[0]: (row[1], row[2]) for row in cur.fetchall()}
    
    # Precompute symbol metrics for filtering
    symbol_metrics = {}
    symbol_ids = list(symbols_info.keys())
    
    for symbol_id in symbol_ids:
        # Get latest price (need >= $5)
        cur.execute("""
            SELECT close FROM bars 
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts DESC LIMIT 1
        """, (symbol_id,))
        row = cur.fetchone()
        if not row:
            continue
        last_price = row[0]
        
        # Get 60-day median dollar volume (need >= $10M)
        cur.execute("""
            SELECT close * volume, ts FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts DESC LIMIT 60
        """, (symbol_id,))
        recent_bars = cur.fetchall()
        if len(recent_bars) < 60:
            continue
            
        dollar_volumes = [bar[0] for bar in recent_bars]
        median_dvol = statistics.median(dollar_volumes)
        
        symbol_metrics[symbol_id] = {
            'last_price': last_price,
            'median_dvol': median_dvol,
            'bars': recent_bars
        }
    
    # Filter symbols by criteria
    eligible_symbols = set()
    for sid, metrics in symbol_metrics.items():
        if metrics['last_price'] >= 5 and metrics['median_dvol'] >= 10_000_000:
            eligible_symbols.add(sid)
    
    # Process events
    observations = []
    for symbol_id, event_ts, event_day in events:
        if symbol_id not in eligible_symbols:
            continue
            
        # Get bars around the event
        cur.execute("""
            SELECT ts, close, volume FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (symbol_id,))
        all_bars = cur.fetchall()
        
        if len(all_bars) < 80:  # Need enough history
            continue
            
        # Find T-1 and T
        bar_times = [bar[0] for bar in all_bars]
        try:
            t_idx = bar_times.index(event_ts)
        except ValueError:
            # Event day not exactly in bars, find closest after
            for i, t in enumerate(bar_times):
                if t >= event_ts:
                    t_idx = i
                    break
            else:
                continue
        
        if t_idx < 1 or t_idx >= len(all_bars) - 20:
            continue
            
        t_prev_close = all_bars[t_idx-1][1]  # T-1 close
        t_close = all_bars[t_idx][1]          # T close
        t_volume = all_bars[t_idx][2]         # T volume
        
        # Check price change condition: between -10% and 0%
        price_change = (t_close - t_prev_close) / t_prev_close
        if not (-0.10 <= price_change <= 0):
            continue
            
        # Get 60-day median volume before T
        pre_bars = all_bars[max(0, t_idx-60):t_idx]
        if len(pre_bars) < 60:
            continue
        volumes = [bar[2] for bar in pre_bars]
        median_vol = statistics.median(volumes)
        
        # Check volume condition
        if t_volume <= median_vol:
            continue
            
        # Check 5-day realized volatility (top decile abstention)
        if t_idx >= 5:
            five_day_returns = []
            for i in range(t_idx-4, t_idx+1):
                if i > 0:
                    ret = (all_bars[i][1] - all_bars[i-1][1]) / all_bars[i-1][1]
                    five_day_returns.append(ret)
            if len(five_day_returns) >= 2:
                vol_5d = statistics.stdev(five_day_returns)
                # We'll check cross-sectional later after collecting all observations
        
        # Get T+20 close
        if t_idx + 20 >= len(all_bars):
            continue
        t20_close = all_bars[t_idx + 20][1]
        
        # Label: DOWN if price fell
        label = 1 if t20_close < t_close else 0
        
        observations.append({
            'symbol_id': symbol_id,
            'event_ts': event_ts,
            't_idx': t_idx,
            'all_bars': all_bars,
            'label': label,
            'price_change': price_change
        })
    
    if len(observations) < 50:
        print("INSUFFICIENT=1")
        return
    
    # Sort by time for era split
    observations.sort(key=lambda x: x['event_ts'])
    split_idx = int(len(observations) * 0.8)
    main_era = observations[:split_idx]
    sealed_era = observations[split_idx:]
    
    # Calculate 5-day volatility thresholds cross-sectionally
    vols_5d = []
    for obs in main_era:
        t_idx = obs['t_idx']
        all_bars = obs['all_bars']
        if t_idx >= 5:
            five_day_returns = []
            for i in range(t_idx-4, t_idx+1):
                if i > 0:
                    ret = (all_bars[i][1] - all_bars[i-1][1]) / all_bars[i-1][1]
                    five_day_returns.append(ret)
            if len(five_day_returns) >= 2:
                vol_5d = statistics.stdev(five_day_returns)
                vols_5d.append(vol_5d)
    
    if not vols_5d:
        print("INSUFFICIENT=1")
        return
        
    vols_5d.sort()
    vol_90th = vols_5d[int(len(vols_5d) * 0.9)] if len(vols_5d) >= 10 else vols_5d[-1]
    
    # Filter main_era with volatility check
    filtered_main = []
    for obs in main_era:
        t_idx = obs['t_idx']
        all_bars = obs['all_bars']
        if t_idx >= 5:
            five_day_returns = []
            for i in range(t_idx-4, t_idx+1):
                if i > 0:
                    ret = (all_bars[i][1] - all_bars[i-1][1]) / all_bars[i-1][1]
                    five_day_returns.append(ret)
            if len(five_day_returns) >= 2:
                vol_5d = statistics.stdev(five_day_returns)
                if vol_5d > vol_90th:
                    continue
        filtered_main.append(obs)
    
    # Count independent observations (by UTC day)
    day_counts = defaultdict(int)
    for obs in filtered_main:
        # Convert timestamp to day
        day = datetime.fromtimestamp(obs['event_ts'], tz=timezone.utc).strftime('%Y-%m-%d')
        day_counts[day] += 1
    
    distinct_days = len(day_counts)
    issued = len(filtered_main)
    
    # Calculate precision
    if issued == 0:
        print("INSUFFICIENT=1")
        return
        
    hits = sum(obs['label'] for obs in filtered_main)
    precision = hits / issued
    
    # Base rate of DOWN calls within issued subset
    base_rate = hits / issued  # Same as precision here since all issued are DOWN
    
    # For design effect: assume independence for now
    design_effect = 1.0
    effective_n = issued / design_effect
    
    # Sealed era
    if sealed_era:
        sealed_hits = sum(obs['label'] for obs in sealed_era)
        sealed_precision = sealed_hits / len(sealed_era)
    else:
        sealed_precision = 0.0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(observations)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
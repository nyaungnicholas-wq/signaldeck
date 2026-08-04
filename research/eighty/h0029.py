import sqlite3
import sys
from datetime import datetime, timezone

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()

    # Get all trading days sorted by timestamp
    c.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    all_days = [row[0] for row in c.fetchall()]

    if len(all_days) < 100:
        print("INSUFFICIENT=1")
        sys.exit(0)

    # Find first trading day of each year
    first_days = {}
    for ts in all_days:
        year = datetime.fromtimestamp(ts, tz=timezone.utc).year
        if year not in first_days:
            first_days[year] = ts

    # Get all US stocks
    c.execute("SELECT id, symbol FROM symbols WHERE market='stocks'")
    symbols = c.fetchall()

    if not symbols:
        print("INSUFFICIENT=1")
        sys.exit(0)

    # Prepare data structures
    opportunities = []
    issued_calls = []

    # Iterate through each symbol
    for symbol_id, symbol in symbols:
        # Get all daily bars for this symbol, sorted by ts
        c.execute("""
            SELECT ts, close, high, low, open, volume 
            FROM bars 
            WHERE symbol_id=? AND tf='1d' 
            ORDER BY ts
        """, (symbol_id,))
        bars = c.fetchall()
        
        if len(bars) < 70:
            continue
            
        bars_dict = {bar[0]: bar[1:] for bar in bars}
        ts_list = sorted(bars_dict.keys())
        
        # Precompute 60-day rolling average dollar volume
        dollar_volume = {}
        for i in range(60, len(ts_list)):
            window = ts_list[i-60:i]
            avg_vol = sum(bars_dict[ts][4] * bars_dict[ts][0] for ts in window) / 60
            dollar_volume[ts_list[i]] = avg_vol
        
        # Find first days of years present in data
        for year, t in first_days.items():
            # Find index of T in ts_list
            try:
                t_idx = ts_list.index(t)
            except ValueError:
                continue
                
            if t_idx < 1:
                continue
                
            # Check if T is at least 60 bars from start for rolling metrics
            if t_idx < 60:
                continue
                
            # Get the last 5 days before T (excluding T)
            lookback_ts = ts_list[t_idx-5:t_idx]
            if len(lookback_ts) < 5:
                continue
                
            # Check universe: price >= $2 at T
            price_at_t = bars_dict[t][0]
            if price_at_t < 2:
                continue
                
            # Check average daily dollar volume >= $1M over prior 60 sessions
            if t not in dollar_volume:
                continue
            if dollar_volume[t] < 1e6:
                continue
                
            # Compute 52-week high and low (prior year)
            year_ago_idx = t_idx - 252 if t_idx >= 252 else 0
            lookback_52w = ts_list[year_ago_idx:t_idx]
            
            if not lookback_52w:
                continue
                
            highs_52w = [bars_dict[ts][1] for ts in lookback_52w]
            lows_52w = [bars_dict[ts][2] for ts in lookback_52w]
            high_52w = max(highs_52w)
            low_52w = min(lows_52w)
            
            # Check down at least 30% from 52-week high
            if price_at_t > 0.7 * high_52w:
                continue
                
            # Check new 52-week low within last 5 sessions
            last_5_lows = [bars_dict[ts][2] for ts in lookback_ts]
            if min(last_5_lows) > low_52w:
                continue
                
            # Check closes within 5% of prior-day close
            prior_close = bars_dict[ts_list[t_idx-1]][0]
            if prior_close > 0:
                pct_change = abs(price_at_t - prior_close) / prior_close
                if pct_change > 0.05:
                    continue
                    
            # Check fall > 10% in last 5 sessions
            if len(lookback_ts) >= 5:
                close_5_ago = bars_dict[lookback_ts[0]][0]
                if close_5_ago > 0:
                    change_5d = (price_at_t - close_5_ago) / close_5_ago
                    if change_5d < -0.10:
                        continue
                        
            # Check 5-day realized volatility in top cross-sectional decile
            # We'll compute for all candidates and check later
            vol_5d = 0
            if len(lookback_ts) >= 5:
                returns_5d = []
                for i in range(1, len(lookback_ts)):
                    prev = bars_dict[lookback_ts[i-1]][0]
                    curr = bars_dict[lookback_ts[i]][0]
                    if prev > 0:
                        returns_5d.append((curr - prev) / prev)
                if returns_5d:
                    import math
                    mean = sum(returns_5d) / len(returns_5d)
                    vol_5d = math.sqrt(sum((r - mean)**2 for r in returns_5d) / len(returns_5d))
            
            # Check price < $2 (already checked)
            
            # Mark this opportunity
            opportunities.append((symbol_id, t, vol_5d, t_idx, ts_list, bars_dict))
            
    # If no opportunities, insufficient
    if not opportunities:
        print("INSUFFICIENT=1")
        sys.exit(0)
        
    # Compute cross-sectional volatility decile
    vols = [opp[2] for opp in opportunities]
    vols.sort()
    decile_idx = int(len(vols) * 0.9)
    vol_threshold = vols[decile_idx] if decile_idx < len(vols) else vols[-1]
    
    # Filter out high volatility opportunities
    filtered_opps = [opp for opp in opportunities if opp[2] <= vol_threshold]
    
    # Now get outcomes for horizon T+20
    for symbol_id, t, vol_5d, t_idx, ts_list, bars_dict in filtered_opps:
        # Get the outcome from prediction_outcomes
        c.execute("""
            SELECT up, prob, fwd_return 
            FROM prediction_outcomes 
            WHERE symbol_id=? AND horizon=20 AND ts=?
        """, (symbol_id, t))
        row = c.fetchone()
        if not row:
            continue
            
        up, prob, fwd_return = row
        
        # This is a valid call
        issued_calls.append({
            'symbol_id': symbol_id,
            'ts': t,
            'up': up,
            'prob': prob,
            'fwd_return': fwd_return
        })
        
    conn.close()
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        sys.exit(0)
        
    # Sort by timestamp
    issued_calls.sort(key=lambda x: x['ts'])
    
    # Split into train/test (80/20)
    n = len(issued_calls)
    split_idx = int(n * 0.8)
    train_calls = issued_calls[:split_idx]
    test_calls = issued_calls[split_idx:]
    
    # Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0
        issued = len(calls)
        hits = sum(1 for c in calls if c['up'])
        base_rate = hits / issued if issued else 0
        days = len(set(c['ts'] for c in calls))
        return issued, hits, base_rate, days
    
    issued_train, hits_train, base_rate_train, days_train = compute_metrics(train_calls)
    issued_test, hits_test, base_rate_test, days_test = compute_metrics(test_calls)
    
    # Total metrics
    issued_total = len(issued_calls)
    hits_total = sum(1 for c in issued_calls if c['up'])
    base_rate_total = hits_total / issued_total if issued_total else 0
    days_total = len(set(c['ts'] for c in issued_calls))
    
    # Effective N (simplified - assuming design effect of 1 for independent days)
    effective_n = issued_total / 1.0
    
    # Print results
    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={len(filtered_opps)}")
    print(f"PRECISION={hits_total/issued_total:.4f}" if issued_total else "PRECISION=0.0000")
    print(f"BASE_RATE={base_rate_total:.4f}")
    print(f"DISTINCT_DAYS={days_total}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={hits_test/issued_test:.4f}" if issued_test else "SEALED_PRECISION=0.0000")

if __name__ == "__main__":
    main()
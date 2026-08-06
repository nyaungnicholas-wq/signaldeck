import sqlite3
import math
from datetime import datetime, timedelta

def main():
    db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    db.row_factory = sqlite3.Row
    c = db.cursor()
    
    # Get all symbols with enough history
    c.execute("""
        SELECT symbol_id, MIN(ts) as min_ts
        FROM bars
        WHERE tf='1d'
        GROUP BY symbol_id
        HAVING COUNT(*) >= 252
    """)
    symbols_with_history = {row[0]: row[1] for row in c.fetchall()}
    if not symbols_with_history:
        print("INSUFFICIENT=1")
        return
    
    # Get daily bars with volume and dollar volume
    c.execute("""
        SELECT symbol_id, ts, close, volume, high, low, open
        FROM bars
        WHERE tf='1d'
        ORDER BY symbol_id, ts
    """)
    bars = {}
    for row in c.fetchall():
        sym = row[0]
        if sym not in symbols_with_history:
            continue
        bars.setdefault(sym, []).append({
            'ts': row[1],
            'close': row[2],
            'volume': row[3],
            'high': row[4],
            'low': row[5],
            'open': row[6]
        })
    
    # Get short volume data
    c.execute("""
        SELECT symbol_id, day, short_vol, total_vol, short_pct
        FROM short_volume
        ORDER BY symbol_id, day
    """)
    short_data = {}
    for row in c.fetchall():
        sym = row[0]
        short_data.setdefault(sym, []).append({
            'day': row[1],
            'short_pct': row[4],
            'short_vol': row[2],
            'total_vol': row[3]
        })
    
    # Get fundamentals (EPS)
    c.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric='EPS'
        ORDER BY symbol_id, as_of
    """)
    eps_data = {}
    for row in c.fetchall():
        sym = row[0]
        eps_data.setdefault(sym, []).append({
            'value': row[2],
            'as_of': row[3],
            'fetched_at': row[4]
        })
    
    # Get prediction outcomes for labels
    c.execute("""
        SELECT symbol_id, ts, up, fwd_return, horizon
        FROM prediction_outcomes
        WHERE horizon=20
    """)
    outcomes = {}
    for row in c.fetchall():
        sym = row[0]
        outcomes.setdefault(sym, []).append({
            'ts': row[1],
            'up': row[2],
            'fwd_return': row[3]
        })
    
    # Convert outcomes to dict for quick lookup
    outcome_map = {}
    for sym, out_list in outcomes.items():
        for o in out_list:
            outcome_map[(sym, o['ts'])] = {
                'up': o['up'],
                'fwd_return': o['fwd_return']
            }
    
    # Helper functions
    def get_day_index(bars_list, ts):
        for i, bar in enumerate(bars_list):
            if bar['ts'] == ts:
                return i
        return -1
    
    def get_ts_for_day(bars_list, day_offset, ref_idx):
        if 0 <= ref_idx + day_offset < len(bars_list):
            return bars_list[ref_idx + day_offset]['ts']
        return None
    
    # Process each symbol
    opportunities = []
    for sym in bars:
        if sym not in symbols_with_history:
            continue
        bar_list = bars[sym]
        min_ts = symbols_with_history[sym]
        
        # Get short data for this symbol
        short_list = short_data.get(sym, [])
        short_by_day = {row['day']: row for row in short_list}
        
        # Get EPS data for this symbol
        eps_list = eps_data.get(sym, [])
        
        # Process each potential decision point T
        for i in range(252, len(bar_list)):
            t_bar = bar_list[i]
            t_ts = t_bar['ts']
            t_date = datetime.utcfromtimestamp(t_ts)
            
            # Check basic conditions at T
            if t_bar['close'] < 5:
                continue
            
            # Need 60 prior sessions for volume average
            if i < 60:
                continue
            
            # Calculate 60-day average dollar volume
            total_dv = 0
            for j in range(i-60, i):
                total_dv += bar_list[j]['close'] * bar_list[j]['volume']
            avg_dv = total_dv / 60
            if avg_dv < 10_000_000:
                continue
            
            # Check 20-session trailing gain
            if i < 20:
                continue
            gain_20 = (t_bar['close'] / bar_list[i-20]['close']) - 1
            if gain_20 > 0.30:
                continue
            
            # Calculate 20-session realized volatility at T
            closes_20 = [bar_list[j]['close'] for j in range(i-19, i+1)]
            returns_20 = [(closes_20[k]/closes_20[k-1])-1 for k in range(1, len(closes_20))]
            vol_20 = math.sqrt(sum(r**2 for r in returns_20)/len(returns_20))
            
            # Check T-1 bar exists
            if i < 1:
                continue
            t_minus1_bar = bar_list[i-1]
            t_minus1_ts = t_minus1_bar['ts']
            
            # Get T+20 outcome
            t_plus20_ts = get_ts_for_day(bar_list, 20, i)
            if t_plus20_ts is None:
                continue
            outcome = outcome_map.get((sym, t_plus20_ts))
            if outcome is None:
                continue
            
            # Check close-to-close return condition
            ret = (t_bar['close'] / t_minus1_bar['close']) - 1
            if not (-0.01 <= ret <= 0.03):
                continue
            
            # Volume condition
            vol_median = 0
            vols_60 = [bar_list[j]['volume'] for j in range(i-60, i)]
            vols_60.sort()
            if len(vols_60) % 2 == 0:
                vol_median = (vols_60[len(vols_60)//2-1] + vols_60[len(vols_60)//2]) / 2
            else:
                vol_median = vols_60[len(vols_60)//2]
            if t_bar['volume'] < 1.5 * vol_median:
                continue
            
            # Short interest condition at T-1
            t_minus1_date = datetime.utcfromtimestamp(t_minus1_ts).strftime('%Y-%m-%d')
            if t_minus1_date not in short_by_day:
                continue
            
            # Get 20-session average short percentage up to T-1
            short_20 = []
            count = 0
            for j in range(i-20, i):
                day = datetime.utcfromtimestamp(bar_list[j]['ts']).strftime('%Y-%m-%d')
                if day in short_by_day:
                    short_20.append(short_by_day[day]['short_pct'])
                    count += 1
            if count < 10:  # Need reasonable sample
                continue
            avg_short_pct = sum(short_20)/len(short_20)
            
            # Calculate cross-sectional quartile for short interest
            # Get all 20-day averages for recent quarter
            all_short_avgs = []
            for other_sym in bars:
                if other_sym == sym:
                    continue
                other_bar_list = bars[other_sym]
                other_short = short_data.get(other_sym, [])
                other_short_by_day = {row['day']: row for row in other_short}
                # Find index of T-1 in other symbol
                other_i = -1
                for idx, bar in enumerate(other_bar_list):
                    if bar['ts'] <= t_minus1_ts:
                        other_i = idx
                    else:
                        break
                if other_i < 20:
                    continue
                other_short_20 = []
                other_count = 0
                for j in range(other_i-20, other_i):
                    day = datetime.utcfromtimestamp(other_bar_list[j]['ts']).strftime('%Y-%m-%d')
                    if day in other_short_by_day:
                        other_short_20.append(other_short_by_day[day]['short_pct'])
                        other_count += 1
                if other_count >= 10:
                    all_short_avgs.append(sum(other_short_20)/len(other_short_20))
            
            if not all_short_avgs:
                continue
            all_short_avgs.sort()
            quartile_75 = all_short_avgs[int(len(all_short_avgs)*0.75)]
            if avg_short_pct < quartile_75:
                continue
            
            # Earnings surprise condition
            # Find two most recent EPS values fetched before T
            valid_eps = [e for e in eps_list if 
                        datetime.strptime(e['fetched_at'], '%Y-%m-%d').timestamp() < t_ts]
            if len(valid_eps) < 2:
                continue
            valid_eps.sort(key=lambda x: x['as_of'])
            current_eps = valid_eps[-1]['value']
            prior_eps = valid_eps[-2]['value']
            if prior_eps == 0:
                continue
            surprise = current_eps - prior_eps
            surprise_pct = surprise / abs(prior_eps)
            if surprise_pct < 0.10:
                continue
            
            # All conditions met
            opportunities.append({
                'symbol_id': sym,
                't_ts': t_ts,
                't_date': t_date,
                'up': outcome['up'],
                'fwd_return': outcome['fwd_return']
            })
    
    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Sort by timestamp
    opportunities.sort(key=lambda x: x['t_ts'])
    
    # Split into training and sealed (most recent 20%)
    split_idx = int(len(opportunities) * 0.8)
    train = opportunities[:split_idx]
    sealed = opportunities[split_idx:]
    
    # Filter out calls within 20 trading days of same symbol (abstain condition)
    # First, group by symbol and mark calls to exclude
    exclude_until = {}  # symbol_id -> next allowed timestamp
    issued = []
    for opp in opportunities:
        sym = opp['symbol_id']
        t_ts = opp['t_ts']
        
        # Check if we need to abstain (call within prior 20 days)
        if sym in exclude_until and t_ts < exclude_until[sym]:
            continue
        
        # Issue UP call
        issued.append(opp)
        
        # Set exclusion for 20 trading days ahead
        # Approximate 20 trading days as 28 calendar days
        exclude_until[sym] = t_ts + 28*24*60*60
    
    # Calculate metrics
    issued_up = [o for o in issued if o['up'] == 1]
    issued_down = [o for o in issued if o['up'] == 0]
    
    precision = len(issued_up) / len(issued) if issued else 0
    base_rate = len(issued_up) / len(issued) if issued else 0
    
    # Distinct days among issued calls
    distinct_days = len(set(o['t_date'].strftime('%Y-%m-%d') for o in issued))
    
    # Design effect calculation (cluster by day)
    day_counts = {}
    day_hits = {}
    for o in issued:
        day = o['t_date'].strftime('%Y-%m-%d')
        day_counts[day] = day_counts.get(day, 0) + 1
        if o['up'] == 1:
            day_hits[day] = day_hits.get(day, 0) + 1
    
    n_clusters = len(day_counts)
    if n_clusters > 1:
        total_n = len(issued)
        cluster_sizes = list(day_counts.values())
        m = sum(cluster_sizes) / n_clusters
        
        # Calculate ICC for binary outcome
        p_total = len(issued_up) / total_n
        ss_between = 0
        ss_within = 0
        for day, count in day_counts.items():
            p_day = day_hits.get(day, 0) / count if count > 0 else 0
            ss_between += count * (p_day - p_total)**2
            if count > 1:
                ss_within += count * p_day * (1 - p_day)
        
        ms_between = ss_between / (n_clusters - 1)
        ms_within = ss_within / (total_n - n_clusters)
        
        if ms_within > 0:
            icc = ms_between / (ms_between + ms_within)
            design_effect = 1 + (m - 1) * icc
            effective_n = total_n / design_effect
        else:
            effective_n = total_n
    else:
        effective_n = len(issued)
    
    # Sealed era metrics
    sealed_issued = [o for o in sealed if o['symbol_id'] in {i['symbol_id'] for i in issued}]
    sealed_up = [o for o in sealed_issued if o['up'] == 1]
    sealed_precision = len(sealed_up) / len(sealed_issued) if sealed_issued else 0
    
    # Print results
    print(f"ISSUED={len(issued)}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    db.close()

if __name__ == "__main__":
    main()
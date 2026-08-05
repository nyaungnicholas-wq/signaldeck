import sqlite3
from collections import defaultdict
import math
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Load bars data (1d only)
    cur.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    bars_data = cur.fetchall()
    
    # Load stocktwits data
    cur.execute("SELECT symbol_id, ts, bullish, bearish FROM stocktwits_sentiment ORDER BY symbol_id, ts")
    st_data = cur.fetchall()
    conn.close()
    
    # Organize bars by symbol
    bars_by_sym = defaultdict(dict)
    for sym, ts, close, vol in bars_data:
        bars_by_sym[sym][ts] = (close, vol)
    
    # Organize stocktwits by symbol
    st_by_sym = defaultdict(dict)
    for sym, ts, bull, bear in st_data:
        st_by_sym[sym][ts] = (bull, bear)
    
    # Get sorted timestamps for each symbol
    def get_sorted_ts(sym):
        return sorted(bars_by_sym.get(sym, {}).keys())
    
    # Find symbols present in both tables
    common_symbols = set(bars_by_sym.keys()) & set(st_by_sym.keys())
    
    if not common_symbols:
        print("INSUFFICIENT=1")
        return
    
    # For each symbol, compute all required metrics for each eligible T
    all_opportunities = []  # (symbol, T_ts, is_issued, is_hit, era)
    
    for sym in common_symbols:
        ts_list = get_sorted_ts(sym)
        if len(ts_list) < 504 + 20:  # need 504 prior + 20 forward for label
            continue
        
        # Precompute arrays for efficiency
        closes = [bars_by_sym[sym][ts][0] for ts in ts_list]
        volumes = [bars_by_sym[sym][ts][1] for ts in ts_list]
        
        # Compute 200-session SMA
        sma200 = [None] * len(ts_list)
        for i in range(199, len(ts_list)):
            sma200[i] = sum(closes[i-199:i+1]) / 200
        
        # Compute 20-session median volume
        vol_median20 = [None] * len(ts_list)
        for i in range(19, len(ts_list)):
            vol_median20[i] = sorted(volumes[i-19:i+1])[10]
        
        # Compute 20-session realized volatility (std of daily returns)
        vol20 = [None] * len(ts_list)
        for i in range(19, len(ts_list)):
            rets = []
            for j in range(i-19, i+1):
                if j > 0:
                    rets.append((closes[j] - closes[j-1]) / closes[j-1])
            if len(rets) == 20:
                mean_ret = sum(rets) / 20
                vol20[i] = math.sqrt(sum((r - mean_ret)**2 for r in rets) / 20)
        
        # Compute 60-session average daily dollar volume
        avg_dollar_vol60 = [None] * len(ts_list)
        for i in range(59, len(ts_list)):
            total = sum(closes[j] * volumes[j] for j in range(i-59, i+1))
            avg_dollar_vol60[i] = total / 60
        
        # Compute 5-session average bullish/bearish ratio
        bb_ratio5 = [None] * len(ts_list)
        for i in range(4, len(ts_list)):
            ratios = []
            valid = True
            for delta in range(-4, 1):
                t = ts_list[i + delta]
                if t not in st_by_sym[sym]:
                    valid = False
                    break
                bull, bear = st_by_sym[sym][t]
                if bear == 0:
                    valid = False
                    break
                ratios.append(bull / bear)
            if valid and len(ratios) == 5:
                bb_ratio5[i] = sum(ratios) / 5
        
        # Compute T's close-to-close return
        ret1 = [None] * len(ts_list)
        for i in range(1, len(ts_list)):
            ret1[i] = (closes[i] - closes[i-1]) / closes[i-1]
        
        # Now evaluate each potential T (starting from index 503 to have 504 prior sessions)
        for i in range(503, len(ts_list) - 20):  # need 20 forward for label
            T_ts = ts_list[i]
            
            # Check all entry conditions
            # 1. Close >= $5
            if closes[i] < 5:
                continue
            
            # 2. 504 prior sessions (already satisfied by loop start)
            
            # 3. StockTwits data for T-5..T (bb_ratio5 not None)
            if bb_ratio5[i] is None:
                continue
            
            # 4. 200 SMA available
            if sma200[i] is None:
                continue
            
            # 5. 20-session median volume available
            if vol_median20[i] is None:
                continue
            
            # 6. 20-session volatility available
            if vol20[i] is None:
                continue
            
            # 7. 60-session avg dollar volume available
            if avg_dollar_vol60[i] is None:
                continue
            
            # 8. Avg daily dollar volume >= $5M
            if avg_dollar_vol60[i] < 5_000_000:
                continue
            
            # 9. Close above 200 SMA
            if closes[i] <= sma200[i]:
                continue
            
            # 10. Volume not above 1.5x 20-session median
            if volumes[i] > 1.5 * vol_median20[i]:
                continue
            
            # 11. T's return in [-1%, +1%]
            if ret1[i] is None or ret1[i] < -0.01 or ret1[i] > 0.01:
                continue
            
            # This is an opportunity
            all_opportunities.append({
                'symbol': sym,
                'ts': T_ts,
                'bb_ratio5': bb_ratio5[i],
                'vol20': vol20[i],
                'close': closes[i],
                'index': i,
                'ts_list': ts_list,
                'closes': closes
            })
    
    if not all_opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Determine cross-sectional deciles for bb_ratio5 and vol20 at each timestamp
    # Group opportunities by timestamp
    opps_by_ts = defaultdict(list)
    for opp in all_opportunities:
        opps_by_ts[opp['ts']].append(opp)
    
    # For each timestamp, compute decile thresholds
    for ts, opps in opps_by_ts.items():
        if len(opps) < 10:
            # Not enough for decile, mark all as not in top decile
            for opp in opps:
                opp['bb_top_decile'] = False
                opp['vol_top_decile'] = False
            continue
        
        bb_ratios = sorted([o['bb_ratio5'] for o in opps])
        vol20s = sorted([o['vol20'] for o in opps])
        
        # Top decile threshold (90th percentile)
        bb_thresh = bb_ratios[int(len(bb_ratios) * 0.9)]
        vol_thresh = vol20s[int(len(vol20s) * 0.9)]
        
        for opp in opps:
            opp['bb_top_decile'] = opp['bb_ratio5'] >= bb_thresh
            opp['vol_top_decile'] = opp['vol20'] >= vol_thresh
    
    # Now apply remaining abstention conditions and determine issued calls
    # Track last call per symbol for 20-day cooldown
    last_call_ts = {}
    issued_calls = []
    
    for opp in all_opportunities:
        sym = opp['symbol']
        ts = opp['ts']
        
        # Abstain: vol20 in top cross-sectional decile
        if opp.get('vol_top_decile', False):
            continue
        
        # Abstain: bb_ratio5 not in top cross-sectional decile (entry requires it)
        if not opp.get('bb_top_decile', False):
            continue
        
        # Abstain: call issued for same symbol in prior 20 trading days
        if sym in last_call_ts:
            # Find index of last call and current ts
            last_ts = last_call_ts[sym]
            # Need to check if within 20 trading days
            # We'll use the ts_list from the opportunity
            ts_list = opp['ts_list']
            try:
                last_idx = ts_list.index(last_ts)
                curr_idx = ts_list.index(ts)
                if curr_idx - last_idx <= 20:
                    continue
            except ValueError:
                pass
        
        # This is an issued DOWN call
        # Determine label: T+20 close-to-close return (negative = hit for DOWN call)
        i = opp['index']
        closes = opp['closes']
        if i + 20 < len(closes):
            fwd_ret = (closes[i + 20] - closes[i]) / closes[i]
            is_hit = fwd_ret < 0  # DOWN call hits if price goes down
        else:
            continue  # Not enough forward data
        
        issued_calls.append({
            'symbol': sym,
            'ts': ts,
            'is_hit': is_hit,
            'fwd_ret': fwd_ret
        })
        last_call_ts[sym] = ts
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Hold out most recent 20% as sealed era
    # Sort by timestamp
    issued_calls.sort(key=lambda x: x['ts'])
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    sealed_calls = issued_calls[-n_sealed:]
    main_calls = issued_calls[:-n_sealed]
    
    # Count opportunities (decision points considered)
    # An opportunity is each (symbol, T) that passed initial filters before decile/cooldown
    opportunities_count = len(all_opportunities)
    
    # Compute metrics for main era
    issued_main = len(main_calls)
    hits_main = sum(1 for c in main_calls if c['is_hit'])
    precision_main = hits_main / issued_main if issued_main > 0 else 0
    
    # Base rate within issued subset: proportion of DOWN calls that would hit by chance
    # For DOWN calls, base rate = proportion of negative forward returns in issued subset
    base_rate_main = sum(1 for c in main_calls if c['fwd_ret'] < 0) / issued_main if issued_main > 0 else 0
    
    # Distinct days among issued calls
    distinct_days_main = len(set(c['ts'] for c in main_calls))
    
    # Effective N: issued / design_effect
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Simplified: group by day, compute cluster sizes
    day_counts = defaultdict(int)
    for c in main_calls:
        day_counts[c['ts']] += 1
    if day_counts:
        avg_cluster = sum(day_counts.values()) / len(day_counts)
        # Conservative ICC estimate of 0.1 for financial returns
        icc = 0.1
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n_main = issued_main / design_effect
    else:
        effective_n_main = 0
    
    # Sealed era metrics
    issued_sealed = len(sealed_calls)
    hits_sealed = sum(1 for c in sealed_calls if c['is_hit'])
    sealed_precision = hits_sealed / issued_sealed if issued_sealed > 0 else 0
    
    # Check minimum independent observations (30)
    if effective_n_main < 30:
        print("INSUFFICIENT=1")
        return
    
    # Print results
    print(f"ISSUED={issued_main}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision_main:.6f}")
    print(f"BASE_RATE={base_rate_main:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_main}")
    print(f"EFFECTIVE_N={effective_n_main:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
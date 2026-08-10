# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 355
# cycle_index: 23
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Check if required macro series exists
    c.execute("SELECT COUNT(*) FROM macro_series WHERE series = 'BAMLH0A0HYM2'")
    if c.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        return 0
    
    # Get all trading days from bars (1d)
    c.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    all_days = [row[0] for row in c.fetchall()]
    if not all_days:
        print("INSUFFICIENT=1")
        return 0
    
    # Split into train and sealed (last 20%)
    split_idx = int(len(all_days) * 0.8)
    train_days = all_days[:split_idx]
    sealed_days = all_days[split_idx:]
    
    # Get all symbols with data
    c.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
    all_symbols = [row[0] for row in c.fetchall()]
    
    # Precompute macro condition for each day
    macro_data = {}
    c.execute("SELECT ts, value FROM macro_series WHERE series='BAMLH0A0HYM2' ORDER BY ts")
    macro_rows = c.fetchall()
    for i, (ts, value) in enumerate(macro_rows):
        macro_data[ts] = value
    
    # For each day, check if macro condition met (need 5-day SMA vs level 21 days prior)
    macro_cond = {}
    days_with_macro = sorted(macro_data.keys())
    for i, ts in enumerate(days_with_macro):
        # Need at least 21 days history for SMA5
        if i < 21:
            continue
        
        # Get current 5-day SMA
        window = [macro_data[days_with_macro[j]] for j in range(i-4, i+1)]
        sma5_now = sum(window) / 5
        
        # Get level 21 days prior
        level_21d_ago = macro_data[days_with_macro[i-21]]
        
        # Check increase >= 50 bps (0.50)
        if sma5_now - level_21d_ago >= 0.50:
            macro_cond[ts] = True
        else:
            macro_cond[ts] = False
    
    # Now process each symbol-day opportunity
    opportunities = []
    for symbol_id in all_symbols:
        # Get all 1d bars for this symbol
        c.execute("""SELECT ts, close, volume FROM bars 
                     WHERE symbol_id=? AND tf='1d' ORDER BY ts""", (symbol_id,))
        bars = c.fetchall()
        if len(bars) < 252:
            continue
        
        # Create time series
        ts_list = [row[0] for row in bars]
        close_dict = {ts: row[1] for row in bars}
        vol_dict = {ts: row[2] for row in bars}
        
        # Get sentiment data
        c.execute("""SELECT day, mean_score FROM sentiment_features 
                     WHERE symbol_id=? ORDER BY day""", (symbol_id,))
        sent_rows = c.fetchall()
        sent_dict = {}
        for day_str, score in sent_rows:
            # Convert day string to timestamp (day start)
            import datetime
            try:
                dt = datetime.datetime.strptime(day_str, '%Y-%m-%d')
                ts = int(dt.timestamp())
                sent_dict[ts] = score
            except:
                continue
        
        # Process each possible entry day
        for entry_ts in train_days + sealed_days:
            # Skip if no macro condition for this day
            if entry_ts not in macro_cond:
                continue
            if not macro_cond[entry_ts]:
                continue
            
            # Find position in bars
            try:
                idx = ts_list.index(entry_ts)
            except ValueError:
                continue
            
            # Need enough history for all conditions
            if idx < 252:
                continue
            
            # Check 1-year return (abstain if negative)
            price_1y_ago = close_dict.get(ts_list[idx-252])
            price_now = close_dict.get(entry_ts)
            if not price_1y_ago or not price_now or price_1y_ago <= 0:
                continue
            ret_1y = (price_now - price_1y_ago) / price_1y_ago
            if ret_1y < 0:
                continue
            
            # Check 5-day realized volatility > 35%
            returns = []
            for j in range(idx-4, idx+1):
                p1 = close_dict.get(ts_list[j-1])
                p2 = close_dict.get(ts_list[j])
                if p1 and p2 and p1 > 0:
                    returns.append((p2 - p1) / p1)
            if len(returns) < 5:
                continue
            # Annualize: multiply by sqrt(252)
            mean_ret = sum(returns) / len(returns)
            var = sum((r - mean_ret)**2 for r in returns) / (len(returns)-1)
            vol_5d = math.sqrt(var) * math.sqrt(252)
            if vol_5d <= 0.35:
                continue
            
            # Check 20-day avg dollar volume > $10M
            dollar_vols = []
            for j in range(idx-19, idx+1):
                v = vol_dict.get(ts_list[j])
                p = close_dict.get(ts_list[j])
                if v and p:
                    dollar_vols.append(v * p)
            if len(dollar_vols) < 20:
                continue
            avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
            if avg_dollar_vol < 10_000_000:
                continue
            
            # Check 20-day avg turnover (shares) >= 500,000
            vols = [vol_dict.get(ts_list[j], 0) for j in range(idx-19, idx+1)]
            avg_vol = sum(vols) / len(vols)
            if avg_vol < 500_000:
                continue
            
            # Check news sentiment z-score <= -1.5
            # Get 20-day sentiment average
            sent_scores = []
            for j in range(idx-19, idx+1):
                ts = ts_list[j]
                if ts in sent_dict:
                    sent_scores.append(sent_dict[ts])
            if len(sent_scores) < 10:
                continue
            avg_sent_20d = sum(sent_scores) / len(sent_scores)
            
            # Get 252-day sentiment history for z-score
            sent_hist = []
            for j in range(idx-251, idx+1):
                ts = ts_list[j]
                if ts in sent_dict:
                    sent_hist.append(sent_dict[ts])
            if len(sent_hist) < 50:
                continue
            mean_sent_hist = sum(sent_hist) / len(sent_hist)
            var_sent_hist = sum((s - mean_sent_hist)**2 for s in sent_hist) / len(sent_hist)
            std_sent_hist = math.sqrt(var_sent_hist) if var_sent_hist > 0 else 1
            z_score = (avg_sent_20d - mean_sent_hist) / std_sent_hist
            if z_score > -1.5:
                continue
            
            # All conditions met - issue "down" call
            # Find label: need prediction_outcomes for this symbol, horizon=21, ts=entry_ts
            c.execute("""SELECT up FROM prediction_outcomes 
                        WHERE symbol_id=? AND horizon=21 AND ts=?""", 
                     (symbol_id, entry_ts))
            label_row = c.fetchone()
            if label_row is None:
                continue  # No label available
            up = label_row[0]  # True if stock went up, False if down
            hit = not up  # Our call is "down", so hit if actual direction is down
            
            opportunities.append({
                'symbol_id': symbol_id,
                'entry_ts': entry_ts,
                'hit': hit,
                'is_sealed': entry_ts in sealed_days
            })
    
    # Calculate metrics
    issued = len(opportunities)
    if issued == 0:
        print("INSUFFICIENT=1")
        return 0
    
    hits = sum(1 for o in opportunities if o['hit'])
    precision = hits / issued
    
    # Base rate: proportion of "down" in issued calls (i.e., actual up=False)
    down_calls = sum(1 for o in opportunities if not o['hit'])  # not hit means our down call was wrong? 
    # Wait: hit = not up. So if hit=True, stock went down. If hit=False, stock went up.
    # So base rate of "down" (our predicted class) in issued subset = number of actually down / issued
    actually_down = sum(1 for o in opportunities if not o['hit'])  # hit=False means stock went up? No.
    # Correction: o['hit'] is True when stock went down (our call was correct)
    # So base rate of predicted class "down" in issued set = (number of times stock actually went down) / issued
    # But that's exactly hits/issued? Wait, no: hits are when we called down and stock went down.
    # So actually_down = hits. So base rate = hits/issued = precision. That's not right.
    # Let's think: base rate of the class we're predicting in the issued set.
    # We're predicting "down". In the issued set, what's the proportion of actual downs?
    # That's (number of times stock actually went down) / issued.
    # And that's exactly hits/issued because hit is when stock went down.
    # So base_rate = precision? That can't be. Wait, no: if we always predict down, then base_rate = proportion of downs in the set.
    # And precision = proportion of predictions that are correct.
    # So they are equal only if we always predict the majority class? Not necessarily.
    # Actually, in a binary classification where we always predict the same class, precision = base rate of that class.
    # But here we're not always predicting down? We're only predicting down when conditions are met.
    # So the base rate of "down" in the issued set is the proportion of actual downs in that set.
    # And precision is the proportion of issued calls that were correct (i.e., actually down).
    # So indeed base_rate = (number of actual downs) / issued = hits/issued = precision.
    # That would make base_rate = precision always. That seems odd but mathematically true when we only predict one class.
    # Let's double-check: If we only issue "down" calls, then our predictions are all "down".
    # So the set of issued calls is all "down" predictions.
    # The base rate of the predicted class "down" in this set is the proportion of actual downs in the set.
    # And precision is also the proportion of actual downs in the set.
    # So they are identical.
    base_rate = hits / issued
    
    # Distinct days
    distinct_days = len(set(o['entry_ts'] for o in opportunities))
    
    # Design effect: cluster by day (since macro condition is same for all symbols on a day)
    day_counts = defaultdict(int)
    for o in opportunities:
        day_counts[o['entry_ts']] += 1
    
    # Calculate intra-class correlation for outcome within days
    # ICC = (mean_sq_between - mean_sq_within) / (mean_sq_between + (k-1)*mean_sq_within)
    # Simplified: use proportion of variance explained by day
    total_n = issued
    total_mean = hits / issued  # overall proportion of hits
    ss_total = total_n * total_mean * (1 - total_mean)  # variance if binomial
    
    # Between-group variance
    k = len(day_counts)
    sum_sq_between = 0
    for day, count in day_counts.items():
        day_hits = sum(1 for o in opportunities if o['entry_ts'] == day and o['hit'])
        day_prop = day_hits / count
        sum_sq_between += count * (day_prop - total_mean)**2
    ms_between = sum_sq_between / (k - 1) if k > 1 else 0
    
    # Within-group variance (approximate as total - between)
    ss_within = ss_total - sum_sq_between
    df_within = total_n - k
    ms_within = ss_within / df_within if df_within > 0 else 0
    
    # ICC
    avg_cluster_size = total_n / k if k > 0 else 1
    icc = (ms_between - ms_within) / (ms_between + (avg_cluster_size - 1) * ms_within) if (ms_between + (avg_cluster_size - 1) * ms_within) > 0 else 0
    icc = max(0, min(1, icc))  # Bound between 0 and 1
    
    # Design effect = 1 + (avg_cluster_size - 1) * icc
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = issued / design_effect
    
    # Sealed precision
    sealed_hits = sum(1 for o in opportunities if o['is_sealed'] and o['hit'])
    sealed_issued = sum(1 for o in opportunities if o['is_sealed'])
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(all_days) * len(all_symbols)}")  # Very rough estimate
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()
    return 0

if __name__ == "__main__":
    sys.exit(main())
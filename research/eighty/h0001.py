import sqlite3
import sys
from collections import defaultdict
import math
from datetime import datetime

def main():
    # Connect to database read-only
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    conn.row_factory = sqlite3.Row
    
    # Get symbols with sentiment data (for efficiency)
    try:
        symbols_with_sentiment = set()
        cur = conn.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
        for row in cur:
            symbols_with_sentiment.add(row[0])
        cur.close()
        
        if not symbols_with_sentiment:
            print("INSUFFICIENT=1")
            return
        
        # Get all daily bars for these symbols
        bars_data = {}  # symbol_id -> list of (ts, close, volume)
        cur = conn.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' AND symbol_id IN ({}) ORDER BY symbol_id, ts".format(
            ','.join(str(x) for x in symbols_with_sentiment)))
        for row in cur:
            sym = row[0]
            if sym not in bars_data:
                bars_data[sym] = []
            bars_data[sym].append((row[1], row[2], row[3]))
        cur.close()
        
        # Get sentiment features
        sentiment_data = {}  # symbol_id -> dict of date_str -> mean_score
        cur = conn.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
        for row in cur:
            sym = row[0]
            if sym not in sentiment_data:
                sentiment_data[sym] = {}
            sentiment_data[sym][row[1]] = row[2]
        cur.close()
        
        conn.close()
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    # Convert bars to date-indexed data for each symbol
    # Also compute required indicators
    opportunities = []  # list of (date_str, symbol_id)
    issued_calls = []   # list of (date_str, symbol_id, label) where label=1 if UP
    
    for sym in symbols_with_sentiment:
        if sym not in bars_data or len(bars_data[sym]) < 252:
            continue
        
        # Sort bars by timestamp
        bars = sorted(bars_data[sym], key=lambda x: x[0])
        closes = [b[1] for b in bars]
        volumes = [b[2] for b in bars]
        timestamps = [b[0] for b in bars]
        
        # Convert timestamps to date strings
        dates = [datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d') for ts in timestamps]
        
        # Create date to index mapping
        date_to_idx = {d: i for i, d in enumerate(dates)}
        
        # Get sentiment for this symbol
        sent = sentiment_data.get(sym, {})
        
        # Process each date S (index i)
        for i in range(252, len(dates)):
            S_date = dates[i]
            S_close = closes[i]
            S_volume = volumes[i]
            
            # Universe conditions
            if S_close < 5:
                continue
            
            # Check 12 months history (252 days) - already satisfied by i>=252
            
            # Compute 60-day average dollar volume
            if i < 60:
                continue
            dollar_vols = [closes[j] * volumes[j] for j in range(i-60, i)]
            avg_dollar_vol = sum(dollar_vols) / 60
            if avg_dollar_vol < 10_000_000:
                continue
            
            # Check if sentiment exists for S-4..S (5 days)
            sentiment_scores = []
            sentiment_missing = False
            for offset in range(-4, 1):  # -4, -3, -2, -1, 0
                date_check = dates[i+offset] if i+offset >= 0 else None
                if date_check is None or date_check not in sent:
                    sentiment_missing = True
                    break
                sentiment_scores.append(sent[date_check])
            
            if sentiment_missing:
                continue
            
            # 5-session mean sentiment
            sentiment_mean_5 = sum(sentiment_scores) / 5
            
            # Check 20-day high (S-20..S-1)
            if i < 20:
                continue
            high_20 = max(closes[j] for j in range(i-20, i))
            if S_close <= high_20:
                continue
            
            # Check 60-day volume median
            vol_median_60 = sorted(dollar_vols)[len(dollar_vols)//2]
            S_dollar_vol = S_close * S_volume
            if S_dollar_vol <= vol_median_60:
                continue
            
            # 5-session realized volatility (std of returns)
            if i < 5:
                continue
            returns = [(closes[j] - closes[j-1])/closes[j-1] for j in range(i-4, i+1)]
            mean_ret = sum(returns)/5
            vol_5 = math.sqrt(sum((r-mean_ret)**2 for r in returns)/5)
            
            # Check if S close is 30% or more above S-20 close
            if i >= 20:
                close_20_ago = closes[i-20]
                if S_close >= 1.3 * close_20_ago:
                    continue
            
            # Record opportunity
            opportunities.append((S_date, sym, sentiment_mean_5, vol_5, S_close, i))
    
    if len(opportunities) < 10:
        print("INSUFFICIENT=1")
        return
    
    # Group opportunities by date for cross-sectional deciles
    date_opps = defaultdict(list)
    for opp in opportunities:
        date_opps[opp[0]].append(opp)
    
    # Compute cross-sectional deciles per date
    # For each date, get the 90th percentile threshold for sentiment_mean_5 and vol_5
    thresholds = {}  # date -> (sentiment_threshold, volatility_threshold)
    for date_str, opps in date_opps.items():
        if len(opps) < 10:
            continue
        sentiment_means = [opp[2] for opp in opps]
        volatilities = [opp[3] for opp in opps]
        
        sentiment_sorted = sorted(sentiment_means)
        vol_sorted = sorted(volatilities)
        
        # 90th percentile index (top decile)
        sent_idx = int(0.9 * len(sentiment_sorted))
        vol_idx = int(0.9 * len(vol_sorted))
        
        thresholds[date_str] = (sentiment_sorted[sent_idx], vol_sorted[vol_idx])
    
    # Now evaluate entry conditions
    # We need forward return at S+20
    # Re-group by symbol for quick lookup of bars
    symbol_bars = defaultdict(lambda: defaultdict(float))  # symbol_id -> date -> (close, volume)
    for sym in bars_data:
        bars = bars_data[sym]
        dates = [datetime.utcfromtimestamp(b[0]).strftime('%Y-%m-%d') for b in bars]
        for j, d in enumerate(dates):
            symbol_bars[sym][d] = (closes[j], volumes[j])  # closes from earlier
    
    # Process each opportunity
    issued_calls = []
    for opp in opportunities:
        date_str, sym, sent_mean_5, vol_5, S_close, idx = opp
        
        if date_str not in thresholds:
            continue
        
        sent_thresh, vol_thresh = thresholds[date_str]
        
        # Entry condition: sentiment in top decile
        if sent_mean_5 <= sent_thresh:
            continue
        
        # Already checked high and volume earlier, but double-check
        # (we already filtered these in the loop)
        
        # Get S+20 date
        sym_dates = sorted(symbol_bars[sym].keys())
        date_to_idx = {d: i for i, d in enumerate(sym_dates)}
        if date_str not in date_to_idx:
            continue
        s_idx = date_to_idx[date_str]
        if s_idx + 20 >= len(sym_dates):
            continue
        future_date = sym_dates[s_idx + 20]
        
        # Get forward return
        S_close = symbol_bars[sym][date_str][0]
        future_close = symbol_bars[sym][future_date][0]
        if S_close == 0:
            continue
        fwd_return = (future_close - S_close) / S_close
        label = 1 if fwd_return > 0 else 0
        
        issued_calls.append((date_str, sym, label, fwd_return))
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Sort by date to split sealed era
    issued_calls.sort(key=lambda x: x[0])
    n_total = len(issued_calls)
    cutoff_idx = int(n_total * 0.8)
    non_sealed = issued_calls[:cutoff_idx]
    sealed = issued_calls[cutoff_idx:]
    
    # Compute metrics for non-sealed
    hits_non_sealed = sum(1 for call in non_sealed if call[2] == 1)
    issued_non_sealed = len(non_sealed)
    if issued_non_sealed == 0:
        print("INSUFFICIENT=1")
        return
    
    precision = hits_non_sealed / issued_non_sealed
    
    # Base rate within issued subset
    base_rate = hits_non_sealed / issued_non_sealed  # same as precision for UP-only calls
    
    # Distinct days
    distinct_days = len(set(call[0] for call in issued_calls))
    
    # Design effect: clustering by day
    # Group by day
    day_groups = defaultdict(list)
    for call in non_sealed:
        day_groups[call[0]].append(call[2])
    
    # Compute ICC
    n_clusters = len(day_groups)
    if n_clusters > 1:
        # Overall mean
        overall_mean = sum(call[2] for call in non_sealed) / issued_non_sealed
        
        # Between-cluster variance
        ss_between = 0
        n_per_cluster = []
        for day, labels in day_groups.items():
            n_k = len(labels)
            n_per_cluster.append(n_k)
            mean_k = sum(labels) / n_k
            ss_between += n_k * (mean_k - overall_mean) ** 2
        
        # Within-cluster variance
        ss_within = 0
        for day, labels in day_groups.items():
            mean_k = sum(labels) / len(labels)
            for label in labels:
                ss_within += (label - mean_k) ** 2
        
        # ICC calculation
        df_between = n_clusters - 1
        df_within = issued_non_sealed - n_clusters
        
        if df_between > 0 and df_within > 0:
            ms_between = ss_between / df_between
            ms_within = ss_within / df_within
            icc = (ms_between - ms_within) / (ms_between + (max(n_per_cluster) - 1) * ms_within)
            avg_cluster_size = issued_non_sealed / n_clusters
            design_effect = 1 + (avg_cluster_size - 1) * icc
            effective_n = issued_non_sealed / design_effect
        else:
            effective_n = issued_non_sealed
    else:
        effective_n = issued_non_sealed
    
    # SEALED PRECISION
    hits_sealed = sum(1 for call in sealed if call[2] == 1)
    issued_sealed = len(sealed)
    sealed_precision = hits_sealed / issued_sealed if issued_sealed > 0 else 0
    
    # Print required lines
    print(f"ISSUED={issued_non_sealed}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")

if __name__ == "__main__":
    main()
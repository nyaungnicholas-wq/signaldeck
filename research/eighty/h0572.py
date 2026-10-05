# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 571
# cycle_index: 29
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Get all symbols with their market, active status, and delisted_at
    c.execute('SELECT id, market, active, delisted_at FROM symbols')
    symbols = {row[0]: {'market': row[1], 'active': row[2], 'delisted_at': row[3]} for row in c.fetchall()}
    
    # Get all daily bars with timestamps
    c.execute('SELECT symbol_id, ts, close, volume FROM bars WHERE tf = "1d" ORDER BY symbol_id, ts')
    bars_data = {}
    for row in c.fetchall():
        sid, ts, close, vol = row
        if sid not in bars_data:
            bars_data[sid] = []
        bars_data[sid].append((ts, close, vol))
    
    # Get all sentiment features
    c.execute('SELECT symbol_id, day, mean_score FROM sentiment_features ORDER BY symbol_id, day')
    sentiment_data = {}
    for row in c.fetchall():
        sid, day_str, score = row
        if score is None:
            continue
        if sid not in sentiment_data:
            sentiment_data[sid] = []
        # Convert day string to epoch timestamp for alignment
        day_epoch = int(datetime.datetime.strptime(day_str, '%Y-%m-%d').timestamp())
        sentiment_data[sid].append((day_epoch, score))
    
    # Get prediction outcomes for 10-day horizon
    c.execute('SELECT symbol_id, ts, fwd_return FROM prediction_outcomes WHERE horizon = 10')
    outcomes_data = {}
    for row in c.fetchall():
        sid, ts, fwd_ret = row
        if sid not in outcomes_data:
            outcomes_data[sid] = []
        outcomes_data[sid].append((ts, fwd_ret))
    
    conn.close()
    
    # Process each symbol
    all_candidates = []
    
    for sid in bars_data:
        if sid not in sentiment_data:
            continue
        if len(bars_data[sid]) < 252:
            continue
        if len(sentiment_data[sid]) < 500:
            continue
        
        # Create lookup dictionaries for fast access
        bars_dict = {ts: (close, vol) for ts, close, vol in bars_data[sid]}
        sent_dict = {ts: score for ts, score in sentiment_data[sid]}
        outcomes_dict = {ts: ret for ts, ret in outcomes_data.get(sid, [])}
        
        # Align sentiment and bars by date (unix day)
        # Get all unique trading days as epoch start of day (UTC)
        all_days = set()
        for ts, _, _ in bars_data[sid]:
            all_days.add(ts // 86400 * 86400)  # Normalize to start of UTC day
        for ts, _ in sentiment_data[sid]:
            all_days.add(ts // 86400 * 86400)
        
        all_days_sorted = sorted(all_days)
        
        # Create aligned sequences
        aligned = []
        for day in all_days_sorted:
            # Get the bar for this day (use the latest bar within the day)
            bar_entries = [(ts, close, vol) for ts, close, vol in bars_data[sid] if ts // 86400 * 86400 == day]
            if not bar_entries:
                continue
            # Use the last bar of the day (latest timestamp)
            ts_bar, close, vol = max(bar_entries, key=lambda x: x[0])
            
            # Get sentiment for this day
            sent_entries = [(ts, score) for ts, score in sentiment_data[sid] if ts // 86400 * 86400 == day]
            if sent_entries:
                ts_sent, score = max(sent_entries, key=lambda x: x[0])
                # Use the sentiment timestamp if it's the most recent within the day
                if ts_sent > ts_bar:
                    ts_use = ts_sent
                else:
                    ts_use = ts_bar
            else:
                score = None
                ts_use = ts_bar
            
            aligned.append((day, ts_use, close, vol, score))
        
        if len(aligned) < 252:
            continue
        
        # Check for missing trailing 20 sentiment values and apply entry conditions
        for i in range(21, len(aligned)):  # Need at least 21 days of history
            # Check if we have at least 500 sentiment observations
            sent_count = sum(1 for j in range(i) if aligned[j][4] is not None)
            if sent_count < 500:
                continue
            
            # Get trailing 20 days sentiment
            trailing_sent = [aligned[j][4] for j in range(i-20, i)]
            
            # Skip if any missing in trailing 20
            if any(s is None for s in trailing_sent):
                continue
            
            # Check if all trailing 20 > 0.25
            if not all(s > 0.25 for s in trailing_sent):
                continue
            
            # Check current day sentiment <= -0.25
            current_sent = aligned[i][4]
            if current_sent is None or current_sent > -0.25:
                continue
            
            # Check close move within 1.5%
            if i >= 1:
                prev_close = aligned[i-1][2]
                curr_close = aligned[i][2]
                if prev_close > 0:
                    move = abs(curr_close - prev_close) / prev_close
                    if move > 0.015:
                        continue
                else:
                    continue
            else:
                continue
            
            # Check trailing 20-day average dollar volume >= $1M
            trailing_vols = [aligned[j][3] for j in range(i-20, i)]
            trailing_closes = [aligned[j][2] for j in range(i-20, i)]
            avg_dollar_vol = sum(c * v for c, v in zip(trailing_closes, trailing_vols)) / 20
            if avg_dollar_vol < 1_000_000:
                continue
            
            # Check trailing 252 bar count
            if i < 252:
                continue
            
            # Check if we have outcome for 10-day horizon
            decision_ts = aligned[i][1]
            outcome_ret = outcomes_dict.get(decision_ts)
            if outcome_ret is None:
                continue
            
            # Calculate forward return after 10 trading days
            # Find index 10 trading days later
            forward_idx = i + 10
            if forward_idx >= len(aligned):
                continue
            
            # Get forward return from aligned data
            entry_close = aligned[i][2]
            forward_close = aligned[forward_idx][2]
            if entry_close <= 0:
                continue
            actual_return = (forward_close - entry_close) / entry_close
            
            # Store candidate
            all_candidates.append({
                'sid': sid,
                'decision_ts': decision_ts,
                'decision_day': aligned[i][0],
                'actual_return': actual_return,
                'hit': 1 if actual_return < 0 else 0  # Short hit if return is negative
            })
    
    if not all_candidates:
        print("INSUFFICIENT=1")
        return
    
    # Split into in-sample and sealed era (most recent 20%)
    all_candidates.sort(key=lambda x: x['decision_day'])
    split_idx = int(len(all_candidates) * 0.8)
    in_sample = all_candidates[:split_idx]
    sealed = all_candidates[split_idx:]
    
    # Calculate metrics for in-sample
    issued = len(in_sample)
    if issued == 0:
        print("INSUFFICIENT=1")
        return
    
    hits = sum(c['hit'] for c in in_sample)
    precision = hits / issued
    
    # Base rate: proportion of negative returns in issued subset
    base_rate = sum(1 for c in in_sample if c['actual_return'] < 0) / issued
    
    # Count distinct days
    distinct_days = len(set(c['decision_day'] for c in in_sample))
    
    # Calculate design effect and effective N
    # Group by day
    day_groups = defaultdict(int)
    for c in in_sample:
        day_groups[c['decision_day']] += 1
    
    cluster_sizes = list(day_groups.values())
    n_clusters = len(cluster_sizes)
    if n_clusters > 0:
        avg_cluster_size = issued / n_clusters
        # Calculate intraclass correlation approximation
        # Using the formula: ICC = (MSB - MSW) / (MSB + (m-1)*MSW)
        # Where MSB = variance between clusters, MSW = variance within clusters
        # For simplicity, we'll use a conservative estimate: assume high clustering
        
        # Calculate cluster proportions
        total_variance = 0
        for c in in_sample:
            total_variance += (c['hit'] - precision) ** 2
        
        between_variance = 0
        for day, count in day_groups.items():
            day_hits = sum(1 for c in in_sample if c['decision_day'] == day and c['hit'] == 1)
            day_precision = day_hits / count if count > 0 else 0
            between_variance += count * (day_precision - precision) ** 2
        
        within_variance = total_variance - between_variance
        
        if total_variance > 0:
            icc = max(0, between_variance / total_variance) if total_variance > 0 else 0
        else:
            icc = 0
        
        de = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / de
    else:
        effective_n = issued
    
    # Calculate sealed metrics
    sealed_issued = len(sealed)
    sealed_hits = sum(c['hit'] for c in sealed)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Print required outputs
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(all_candidates)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
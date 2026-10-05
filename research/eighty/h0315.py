# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 314
# cycle_index: 37
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timezone
from collections import defaultdict
import heapq

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all 1d bars
    cur.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    bars = cur.fetchall()
    if not bars:
        print("INSUFFICIENT=1")
        return

    # Build per-symbol data structures
    symbol_data = defaultdict(list)
    for r in bars:
        symbol_data[r['symbol_id']].append((r['ts'], r['close'], r['volume']))

    # Get all symbols with at least 126 daily bars
    eligible_symbols = [sid for sid, data in symbol_data.items() if len(data) >= 126]

    # Find third-Friday closes
    # Convert timestamp to date
    def ts_to_date(ts):
        return datetime.fromtimestamp(ts, tz=timezone.utc).date()

    # Group all bars by date for efficient lookup
    date_to_bars = defaultdict(list)
    for r in bars:
        d = ts_to_date(r['ts'])
        date_to_bars[d].append(r)

    # Get all unique dates sorted
    all_dates = sorted(date_to_bars.keys())

    # Identify third-Friday dates
    third_fridays = []
    for d in all_dates:
        # Check if Friday (weekday 4)
        if d.weekday() == 4:
            # Check if third Friday of month
            if 15 <= d.day <= 21:  # Third Friday is always between 15th and 21st
                third_fridays.append(d)

    if len(third_fridays) < 5:  # Need enough data
        print("INSUFFICIENT=1")
        return

    # For each third-Friday, compute eligible universe and 5-day returns
    decision_points = []  # (date, [(symbol, 5d_return, eligible, bars_available)])
    
    for tf_date in third_fridays:
        # Get index of this date
        tf_idx = all_dates.index(tf_date)
        
        # Check if we have at least 5 days before and 10 days after
        if tf_idx < 5 or tf_idx + 10 >= len(all_dates):
            continue
            
        # Get the 5 previous trading days
        prev_dates = all_dates[tf_idx-5:tf_idx]
        
        symbol_returns = []
        for sid in eligible_symbols:
            if len(symbol_data[sid]) < 126:
                continue
                
            # Get data up to and including third-Friday
            # We need: close on third-Friday and close 5 trading days before
            # Also need 20-day median dollar volume as of third-Friday
            symbol_ts_list = [(ts, close, vol) for ts, close, vol in symbol_data[sid]]
            
            # Find third-Friday bar
            tf_bar = None
            for ts, close, vol in symbol_ts_list:
                if ts_to_date(ts) == tf_date:
                    tf_bar = (ts, close, vol)
                    break
            if not tf_bar:
                continue
                
            # Find bar 5 trading days before third-Friday
            # Need to map the 5 previous dates to actual bars for this symbol
            prev_bars = []
            for prev_date in prev_dates:
                # Find bar for this symbol on this date
                for ts, close, vol in symbol_ts_list:
                    if ts_to_date(ts) == prev_date:
                        prev_bars.append((ts, close, vol))
                        break
                else:
                    prev_bars.append(None)  # No bar for this date
                    
            # Check if any missing or zero-volume bar
            if any(bar is None or bar[2] == 0 for bar in prev_bars):
                continue
                
            # Calculate 5-day return
            # prev_bars[0] is oldest, prev_bars[4] is day before third-Friday
            close_5d_ago = prev_bars[0][1]
            close_tf = tf_bar[1]
            if close_5d_ago == 0:
                continue
            ret_5d = (close_tf - close_5d_ago) / close_5d_ago
            
            # Calculate 20-day median dollar volume as of third-Friday
            # Need 20 trading days ending on third-Friday
            # Get the 20 most recent bars up to and including third-Friday
            recent_bars = []
            for ts, close, vol in symbol_ts_list:
                if ts <= tf_bar[0]:
                    recent_bars.append((ts, close, vol))
            recent_bars.sort(key=lambda x: x[0], reverse=True)  # Most recent first
            
            if len(recent_bars) < 20:
                continue
                
            # Take first 20 (most recent)
            last_20 = recent_bars[:20]
            # Calculate dollar volume for each
            dollar_volumes = [close * vol for ts, close, vol in last_20]
            # Calculate median
            sorted_dv = sorted(dollar_volumes)
            n = len(sorted_dv)
            median_dv = (sorted_dv[n//2] + sorted_dv[(n-1)//2]) / 2
            
            if median_dv <= 20_000_000:  # $20M
                continue
                
            symbol_returns.append((sid, ret_5d, True))
            
        if not symbol_returns:
            continue
            
        # Sort by 5-day return to determine top/bottom 10%
        returns = [r for _, r, _ in symbol_returns]
        returns.sort()
        n = len(returns)
        top_10_idx = int(n * 0.9)
        bottom_10_idx = int(n * 0.1) - 1
        
        # Issue calls
        for sid, ret_5d, _ in symbol_returns:
            direction = None
            if ret_5d >= returns[top_10_idx]:
                direction = 'short'  # Expect reversal down
            elif ret_5d <= returns[bottom_10_idx]:
                direction = 'long'   # Expect reversal up
            else:
                continue  # Abstain (middle 80%)
                
            # Get 10-day forward return
            tf_idx = all_dates.index(tf_date)
            fwd_date_idx = tf_idx + 10
            if fwd_date_idx >= len(all_dates):
                continue
                
            fwd_date = all_dates[fwd_date_idx]
            
            # Find third-Friday close for this symbol
            tf_close = None
            for ts, close, vol in symbol_data[sid]:
                if ts_to_date(ts) == tf_date:
                    tf_close = close
                    break
            if not tf_close:
                continue
                
            # Find forward close
            fwd_close = None
            for ts, close, vol in symbol_data[sid]:
                if ts_to_date(ts) == fwd_date:
                    fwd_close = close
                    break
            if not fwd_close:
                continue
                
            # Calculate forward return
            fwd_ret = (fwd_close - tf_close) / tf_close
            
            # Determine if correct
            correct = False
            if direction == 'long' and fwd_ret > 0:
                correct = True
            elif direction == 'short' and fwd_ret < 0:
                correct = True
                
            decision_points.append({
                'date': tf_date,
                'symbol': sid,
                'direction': direction,
                'correct': correct,
                'fwd_ret': fwd_ret
            })

    if not decision_points:
        print("INSUFFICIENT=1")
        return

    # Hold out most recent 20% as sealed era
    decision_points.sort(key=lambda x: x['date'])
    n_total = len(decision_points)
    n_sealed = int(n_total * 0.2)
    regular_points = decision_points[:-n_sealed] if n_sealed > 0 else decision_points
    sealed_points = decision_points[-n_sealed:] if n_sealed > 0 else []

    # Calculate metrics for regular period
    def calculate_metrics(points):
        if not points:
            return None
            
        issued = len(points)
        
        # Base rate: for long calls, base rate is fraction that had positive fwd_ret
        # For short calls, base rate is fraction that had negative fwd_ret
        long_points = [p for p in points if p['direction'] == 'long']
        short_points = [p for p in points if p['direction'] == 'short']
        
        if not long_points and not short_points:
            return None
            
        # Calculate hits
        hits = sum(1 for p in points if p['correct'])
        precision = hits / issued if issued > 0 else 0
        
        # Calculate base rate within issued subset
        # For long calls: base rate = fraction with fwd_ret > 0
        # For short calls: base rate = fraction with fwd_ret < 0
        # Overall base rate weighted by count in each direction
        long_correct_base = sum(1 for p in long_points if p['fwd_ret'] > 0) / len(long_points) if long_points else 0
        short_correct_base = sum(1 for p in short_points if p['fwd_ret'] < 0) / len(short_points) if short_points else 0
        
        # Weighted average base rate
        if long_points and short_points:
            base_rate = (long_correct_base * len(long_points) + short_correct_base * len(short_points)) / issued
        elif long_points:
            base_rate = long_correct_base
        else:
            base_rate = short_correct_base
            
        # Count distinct days
        distinct_days = len(set(p['date'] for p in points))
        
        # Calculate design effect for effective_n
        # Cluster by date
        date_clusters = defaultdict(list)
        for p in points:
            date_clusters[p['date']].append(1 if p['correct'] else 0)
        
        # Calculate ICC (intraclass correlation) for binary outcomes
        # Overall proportion
        p_overall = precision
        
        # Between-cluster variance
        k = len(date_clusters)
        n = issued
        
        # Calculate cluster means and sizes
        cluster_means = []
        cluster_sizes = []
        for date, outcomes in date_clusters.items():
            cluster_means.append(sum(outcomes) / len(outcomes))
            cluster_sizes.append(len(outcomes))
        
        # Calculate between-cluster variance (weighted)
        between_var = sum(s * (m - p_overall)**2 for s, m in zip(cluster_sizes, cluster_means)) / (k - 1) if k > 1 else 0
        
        # Calculate within-cluster variance
        within_var = 0
        for outcomes in date_clusters.values():
            p_cluster = sum(outcomes) / len(outcomes)
            within_var += sum((x - p_cluster)**2 for x in outcomes)
        within_var /= (n - k) if n > k else 1
        
        # Total variance
        total_var = between_var + within_var
        
        # ICC
        icc = between_var / total_var if total_var > 0 else 0
        
        # Design effect
        avg_cluster_size = n / k if k > 0 else 1
        design_effect = 1 + (avg_cluster_size - 1) * icc
        
        effective_n = issued / design_effect
        
        return {
            'issued': issued,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    metrics = calculate_metrics(regular_points)
    if not metrics:
        print("INSUFFICIENT=1")
        return

    sealed_metrics = calculate_metrics(sealed_points) if sealed_points else None

    # Print required output
    print(f"ISSUED={metrics['issued']}")
    print(f"OPPORTUNITIES={len(regular_points)}")
    print(f"PRECISION={metrics['precision']:.6f}")
    print(f"BASE_RATE={metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={metrics['distinct_days']}")
    print(f"EFFECTIVE_N={metrics['effective_n']:.6f}")
    if sealed_metrics:
        print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")
    else:
        print("SEALED_PRECISION=0.0")

if __name__ == "__main__":
    main()
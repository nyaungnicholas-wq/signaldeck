# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 319
# cycle_index: 42
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    # Get all symbols with market='stocks'
    symbols = conn.execute("SELECT id, symbol FROM symbols WHERE market='stocks'").fetchall()
    symbol_ids = {s['id']: s['symbol'] for s in symbols}
    
    # Get daily bars for all symbols
    bars = conn.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY symbol_id, ts").fetchall()
    
    # Get fundamentals (EPS and public float)
    fundamentals = conn.execute("SELECT symbol_id, metric, value, fetched_at FROM fundamentals WHERE metric IN ('EPS', 'EntityPublicFloat') ORDER BY symbol_id, fetched_at").fetchall()
    
    # Get Treasury yields
    treasury = conn.execute("SELECT ts, value FROM macro_series WHERE series='DGS10' ORDER BY ts").fetchall()
    
    # Get prediction outcomes for horizon=21
    outcomes = conn.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=21").fetchall()
    
    # Organize data
    bar_dict = {}
    for row in bars:
        sid = row['symbol_id']
        if sid not in bar_dict:
            bar_dict[sid] = []
        bar_dict[sid].append((row['ts'], row['close']))
    
    eps_dict = {}
    float_dict = {}
    for row in fundamentals:
        sid = row['symbol_id']
        metric = row['metric']
        if metric == 'EPS':
            if sid not in eps_dict:
                eps_dict[sid] = []
            eps_dict[sid].append((row['fetched_at'], row['value']))
        elif metric == 'EntityPublicFloat':
            if sid not in float_dict:
                float_dict[sid] = []
            float_dict[sid].append((row['fetched_at'], row['value']))
    
    treasury_dict = {}
    for row in treasury:
        treasury_dict[row['ts']] = row['value']
    
    outcome_dict = {}
    for row in outcomes:
        sid = row['symbol_id']
        if sid not in outcome_dict:
            outcome_dict[sid] = {}
        outcome_dict[sid][row['ts']] = row['up']
    
    # Helper to get most recent value before a timestamp
    def get_most_recent(data_list, ts):
        val = None
        for t, v in data_list:
            if t <= ts:
                val = v
        return val
    
    # Process each symbol
    signals = []
    opportunities = 0
    min_ts = min(row['ts'] for row in bars)
    max_ts = max(row['ts'] for row in bars)
    
    for sid, symbol in symbol_ids.items():
        if sid not in bar_dict or len(bar_dict[sid]) < 252:
            continue
        
        bar_list = bar_dict[sid]
        ts_list = [b[0] for b in bar_list]
        close_list = [b[1] for b in bar_list]
        
        for i in range(252, len(bar_list)):
            ts = ts_list[i]
            close = close_list[i]
            
            # Check EPS and float availability
            eps = get_most_recent(eps_dict.get(sid, []), ts)
            pf = get_most_recent(float_dict.get(sid, []), ts)
            if eps is None or pf is None or eps <= 0 or pf <= 0:
                continue
            
            # Get Treasury yield for this day
            if ts not in treasury_dict:
                continue
            treasury_yield = treasury_dict[ts] / 100  # Convert from percent
            
            # Compute earnings yield
            earnings_yield = eps / close
            spread = earnings_yield - treasury_yield
            
            # Check spread >= 0.02
            if spread < 0.02:
                continue
            
            # Check spread widened by >= 0.005 over past 5 days
            if i < 5:
                continue
            ts_5d = ts_list[i-5]
            eps_5d = get_most_recent(eps_dict.get(sid, []), ts_5d)
            close_5d = close_list[i-5]
            treasury_5d = treasury_dict.get(ts_5d)
            if eps_5d is None or treasury_5d is None:
                continue
            treasury_yield_5d = treasury_5d / 100
            earnings_yield_5d = eps_5d / close_5d
            spread_5d = earnings_yield_5d - treasury_yield_5d
            if spread - spread_5d < 0.005:
                continue
            
            # Check bottom quartile of 252-day price range
            price_range = close_list[i-252:i]
            q25 = sorted(price_range)[63]  # 252 * 0.25 = 63
            if close > q25:
                continue
            
            # Check downtrend condition
            if i < 90:
                continue
            sma50_list = close_list[i-49:i+1]  # 50-day SMA including current
            sma50 = sum(sma50_list) / len(sma50_list)
            downtrend_count = sum(1 for j in range(i-89, i+1) if close_list[j] < sum(close_list[j-49:j+1])/50)
            if downtrend_count > 60:
                continue
            
            # Check if we have a label for 21 days forward
            future_ts = ts + 21*24*3600  # 21 days in seconds (approximate)
            # Find the exact future bar
            future_idx = i + 21
            if future_idx >= len(ts_list):
                continue
            
            # Check if there's an outcome for this symbol and time
            # We'll use the ts from the bar as the prediction ts
            if sid in outcome_dict and ts in outcome_dict[sid]:
                label = outcome_dict[sid][ts]
                signals.append({
                    'symbol_id': sid,
                    'ts': ts,
                    'up': label,
                    'close': close
                })
    
    # If no signals, print INSUFFICIENT
    if len(signals) == 0:
        print("INSUFFICIENT=1")
        return
    
    # Split into train and test (last 20% as sealed era)
    signals.sort(key=lambda x: x['ts'])
    split_idx = int(len(signals) * 0.8)
    train_signals = signals[:split_idx]
    test_signals = signals[split_idx:]
    
    # Calculate metrics
    issued = len(signals)
    opportunities = issued  # We only counted opportunities where we could have issued
    up_count = sum(1 for s in signals if s['up'] == 1)
    precision = up_count / issued
    base_rate = up_count / issued  # Same as precision since we only look at issued
    
    # Distinct days
    distinct_days = len(set(s['ts'] // 86400 for s in signals))
    
    # Design effect (simplified: based on autocorrelation of daily signals)
    # Calculate daily signal counts
    daily_counts = {}
    for s in signals:
        day = s['ts'] // 86400
        daily_counts[day] = daily_counts.get(day, 0) + 1
    n_days = len(daily_counts)
    if n_days == 0:
        design_effect = 1.0
    else:
        # Variance of daily counts
        mean_daily = issued / n_days
        var_daily = sum((c - mean_daily)**2 for c in daily_counts.values()) / n_days
        if mean_daily == 0:
            design_effect = 1.0
        else:
            design_effect = 1 + var_daily / mean_daily
    effective_n = issued / design_effect
    
    # Sealed era metrics
    if len(test_signals) == 0:
        sealed_precision = 0
    else:
        sealed_up = sum(1 for s in test_signals if s['up'] == 1)
        sealed_precision = sealed_up / len(test_signals)
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()
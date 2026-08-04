import sqlite3
import sys
import math
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return 0
    
    cur = conn.cursor()
    
    # Check for required tables
    required = {'bars', 'symbols', 'prediction_outcomes'}
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    if not required.issubset(tables):
        print("INSUFFICIENT=1")
        return 0
    
    # Get US stocks symbols
    cur.execute("SELECT id FROM symbols WHERE market = 'stocks' AND active = 1")
    symbol_ids = [row[0] for row in cur.fetchall()]
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return 0
    
    # Get all daily bars for these symbols
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"SELECT symbol_id, ts, open, high, low, close, volume FROM bars WHERE tf = '1d' AND symbol_id IN ({placeholders})", symbol_ids)
    all_bars = cur.fetchall()
    
    # Group bars by symbol and convert to dict with ts as key
    bars_by_symbol = defaultdict(dict)
    for row in all_bars:
        sym_id, ts, open_, high, low, close, vol = row
        bars_by_symbol[sym_id][ts] = {
            'open': open_, 'high': high, 'low': low, 'close': close, 'volume': vol
        }
    
    # Get prediction outcomes with up label
    cur.execute("SELECT symbol_id, ts, prob, up, fwd_return FROM prediction_outcomes WHERE up IS NOT NULL")
    predictions = cur.fetchall()
    
    # Group predictions by symbol
    preds_by_symbol = defaultdict(list)
    for row in predictions:
        sym_id, ts, prob, up, fwd_return = row
        preds_by_symbol[sym_id].append((ts, prob, up, fwd_return))
    
    # For each symbol, sort predictions by ts
    for sym_id in preds_by_symbol:
        preds_by_symbol[sym_id].sort(key=lambda x: x[0])
    
    opportunities = 0
    issued_calls = []  # (day_ts, correct, sym_id)
    
    # Process each symbol
    for sym_id in symbol_ids:
        if sym_id not in preds_by_symbol or sym_id not in bars_by_symbol:
            continue
        
        sym_preds = preds_by_symbol[sym_id]
        sym_bars = bars_by_symbol[sym_id]
        bar_timestamps = sorted(sym_bars.keys())
        
        if len(bar_timestamps) < 60:
            continue
        
        # For each prediction
        for pred_ts, prob, up, fwd_return in sym_preds:
            # Find the corresponding bar
            if pred_ts not in sym_bars:
                continue
            
            bar = sym_bars[pred_ts]
            
            # Get prior 60 trading days' bars before pred_ts
            prior_bars = [t for t in bar_timestamps if t < pred_ts][-60:]
            if len(prior_bars) < 60:
                continue
            
            # Calculate 60-day median volume
            volumes = [sym_bars[t]['volume'] for t in prior_bars]
            median_vol = sorted(volumes)[len(volumes)//2]
            
            # Calculate 60-day average daily dollar volume (price * volume)
            adv60 = 0
            for t in prior_bars:
                price = sym_bars[t]['close']
                vol = sym_bars[t]['volume']
                adv60 += price * vol
            adv60 = adv60 / 60
            
            # Calculate 5-day realized volatility
            recent_5 = [t for t in bar_timestamps if t < pred_ts][-5:]
            if len(recent_5) < 5:
                continue
            
            # Get returns
            returns = []
            for i in range(1, len(recent_5)):
                prev_close = sym_bars[recent_5[i-1]]['close']
                curr_close = sym_bars[recent_5[i]]['close']
                if prev_close > 0:
                    returns.append((curr_close - prev_close) / prev_close)
            
            if len(returns) < 4:
                continue
            
            # Calculate volatility (std dev)
            mean_ret = sum(returns) / len(returns)
            var = sum((r - mean_ret)**2 for r in returns) / len(returns)
            vol_5 = math.sqrt(var) if var > 0 else 0
            
            # Get prior 20 days' bars
            prior_20 = [t for t in bar_timestamps if t < pred_ts][-20:]
            if len(prior_20) < 20:
                continue
            
            # Calculate 20-day price change
            price_20d_ago = sym_bars[prior_20[0]]['close']
            price_now = bar['close']
            pct_change_20d = (price_now - price_20d_ago) / price_20d_ago if price_20d_ago > 0 else 0
            
            # Check entry conditions
            price_condition = 5 <= price_now  # Price >= $5
            volume_condition = bar['volume'] > median_vol  # Volume above 60-day median
            
            # Intraday range condition: close in top half of range
            range_size = bar['high'] - bar['low']
            if range_size <= 0:
                continue
            intraday_position = (bar['close'] - bar['low']) / range_size
            intraday_condition = intraday_position >= 0.5
            
            # Price relative to previous close (we need prior bar)
            if pred_ts not in sym_bars:
                continue
            
            # Get previous bar
            prev_idx = bar_timestamps.index(pred_ts) - 1
            if prev_idx < 0:
                continue
            prev_bar = sym_bars[bar_timestamps[prev_idx]]
            prev_close = prev_bar['close']
            
            if prev_close <= 0:
                continue
            
            price_change = (bar['close'] - prev_close) / prev_close
            price_range_condition = -0.02 <= price_change <= 0.05
            
            # ADV condition (>= $10M average daily dollar volume)
            adv_condition = adv60 >= 10_000_000
            
            # 5-day volatility not in top decile (we'll approximate with 0.3 as threshold)
            vol_condition = vol_5 < 0.3  # Approximate top decile
            
            # Pre-20d rise not > 30%
            rise_condition = pct_change_20d <= 0.30
            
            # Check if another earnings falls within 20 trading days (we don't have data, so we'll assume False)
            # We cannot implement this without earnings dates, so we'll treat as not present
            another_earnings_condition = False
            
            # If all conditions met
            if (price_condition and volume_condition and intraday_condition and 
                price_range_condition and adv_condition and vol_condition and 
                rise_condition and not another_earnings_condition):
                
                opportunities += 1
                
                # Find actual outcome: forward 20 trading days
                post_idx = bar_timestamps.index(pred_ts) + 1
                horizon_end_idx = post_idx + 20
                if horizon_end_idx > len(bar_timestamps):
                    continue
                
                start_close = bar['close']
                end_close = sym_bars[bar_timestamps[horizon_end_idx-1]]['close']
                
                if start_close <= 0:
                    continue
                
                actual_return = (end_close - start_close) / start_close
                correct = actual_return > 0  # UP call
                
                issued_calls.append((pred_ts, correct, sym_id))
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0
    
    # Split into recent 20% and rest
    issued_calls.sort(key=lambda x: x[0])
    split_idx = int(len(issued_calls) * 0.8)
    training_calls = issued_calls[:split_idx]
    sealed_calls = issued_calls[split_idx:]
    
    # Calculate metrics for training set
    issued_count = len(issued_calls)
    training_issued = len(training_calls)
    training_hits = sum(1 for _, correct, _ in training_calls if correct)
    
    # Base rate of predicted class within issued subset
    up_calls = sum(1 for _, correct, _ in issued_calls if correct)
    base_rate = up_calls / issued_count if issued_count > 0 else 0
    
    # Precision
    precision = training_hits / training_issued if training_issued > 0 else 0
    
    # Distinct days
    distinct_days = len(set(pred_ts // 86400 for pred_ts, _, _ in issued_calls))
    
    # Design effect (approximate by assuming 1 observation per day)
    # For simplicity, we'll treat each issued call as independent
    effective_n = issued_count
    
    # Sealed precision
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(1 for _, correct, _ in sealed_calls if correct)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")
    
    conn.close()
    return 0

if __name__ == "__main__":
    sys.exit(main())
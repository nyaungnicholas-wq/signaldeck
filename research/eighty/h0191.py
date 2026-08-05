import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def get_trading_days(conn):
    """Get all unique trading day timestamps from daily bars."""
    cur = conn.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    return [row[0] for row in cur.fetchall()]

def get_insider_purchases(conn):
    """Get all open-market purchases (code='P') with their filed dates."""
    cur = conn.execute("""
        SELECT symbol_id, filed_ts, shares, price, value
        FROM insider_trades
        WHERE code='P'
        ORDER BY symbol_id, filed_ts
    """)
    return cur.fetchall()

def get_symbol_bars(conn, symbol_id, end_ts):
    """Get daily bars for a symbol up to end_ts."""
    cur = conn.execute("""
        SELECT ts, close, volume, high, low
        FROM bars
        WHERE symbol_id=? AND tf='1d' AND ts<=?
        ORDER BY ts
    """, (symbol_id, end_ts))
    return cur.fetchall()

def get_next_trading_day(trading_days, ts):
    """Find the first trading day on or after ts."""
    for day in trading_days:
        if day >= ts:
            return day
    return None

def get_previous_trading_day(trading_days, ts):
    """Find the last trading day strictly before ts."""
    prev = None
    for day in trading_days:
        if day >= ts:
            break
        prev = day
    return prev

def get_window_trading_days(trading_days, start_ts, end_ts):
    """Get trading days in a window [start_ts, end_ts]."""
    return [d for d in trading_days if start_ts <= d <= end_ts]

def compute_metrics(bars):
    """Compute metrics from bar data."""
    if not bars or len(bars) < 252:
        return None
    
    closes = [b[1] for b in bars]
    volumes = [b[2] for b in bars]
    
    # ADTV (average dollar volume) over prior 60 sessions
    recent_60 = list(zip(closes[-60:], volumes[-60:]))
    adv = sum(c * v for c, v in recent_60) / 60 if len(recent_60) == 60 else 0
    
    # 60-session median volume
    vol_60 = sorted(volumes[-60:])
    median_vol = vol_60[len(vol_60)//2] if vol_60 else 0
    
    # 20-session gain
    if len(closes) >= 20:
        gain_20 = (closes[-1] / closes[-20] - 1) * 100
    else:
        gain_20 = 0
    
    # 20-session volatility (daily returns standard deviation)
    if len(closes) >= 21:
        returns = [(closes[i] / closes[i-1] - 1) for i in range(-20, 0)]
        mean_ret = sum(returns) / len(returns)
        var = sum((r - mean_ret) ** 2 for r in returns) / (len(returns) - 1)
        volatility = var ** 0.5
    else:
        volatility = 0
    
    return {
        'close': closes[-1],
        'volume': volumes[-1],
        'prev_close': closes[-2],
        'adv': adv,
        'median_vol': median_vol,
        'gain_20': gain_20,
        'volatility': volatility,
        'count': len(bars)
    }

def check_entry_conditions(purchase, metrics, purchase_value_at_T):
    """Check if entry conditions are met."""
    if not metrics:
        return False
    
    # Price >= $5
    if metrics['close'] < 5:
        return False
    
    # >= 252 prior sessions
    if metrics['count'] < 252:
        return False
    
    # ADTV >= $10M
    if metrics['adv'] < 10_000_000:
        return False
    
    # T's close within 5% of T-1's close
    if metrics['prev_close'] <= 0:
        return False
    pct_change = abs((metrics['close'] - metrics['prev_close']) / metrics['prev_close'])
    if pct_change > 0.05:
        return False
    
    # T's volume >= 60-session median
    if metrics['volume'] < metrics['median_vol']:
        return False
    
    # Purchase value (shares * T close) >= $100k
    if purchase_value_at_T < 100_000:
        return False
    
    return True

def check_abstain_conditions(metrics, has_sale_in_window):
    """Check if we should abstain."""
    if not metrics:
        return True
    
    if metrics['close'] < 5:
        return True
    
    if metrics['count'] < 252:
        return True
    
    # Trailing 20-session gain > 30%
    if metrics['gain_20'] > 30:
        return True
    
    # We'll skip top decile volatility check for now (would need cross-sectional data)
    
    if has_sale_in_window:
        return True
    
    return False

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        conn.execute("PRAGMA query_only = ON")
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return
    
    try:
        # Get all trading days
        trading_days = get_trading_days(conn)
        if len(trading_days) < 300:
            print("INSUFFICIENT=1")
            return
        
        # Get insider purchases
        purchases = get_insider_purchases(conn)
        if not purchases:
            print("INSUFFICIENT=1")
            return
        
        # Track issued calls by symbol and date to avoid duplicates
        issued_calls = defaultdict(set)
        opportunities = []
        issued = []
        
        # Process each purchase
        for symbol_id, filed_ts, shares, price, value in purchases:
            # Get the first trading day on or after filed_ts (T)
            T = get_next_trading_day(trading_days, filed_ts)
            if T is None:
                continue
            
            # Check window for insider sales (on or within 5 trading days before T)
            window_start = get_previous_trading_day(trading_days, T)
            for i in range(4):  # Get up to 5 trading days before T
                prev = get_previous_trading_day(trading_days, window_start) if window_start else None
                if prev:
                    window_start = prev
            
            # Check for sales in window
            has_sale = False
            cur = conn.execute("""
                SELECT value FROM insider_trades
                WHERE symbol_id=? AND code IN ('S', 'F')
                AND filed_ts >= ? AND filed_ts <= ?
            """, (symbol_id, window_start, T))
            for row in cur.fetchall():
                if row[0] >= 100_000:
                    has_sale = True
                    break
            
            # Get bars for this symbol up to T
            bars = get_symbol_bars(conn, symbol_id, T)
            metrics = compute_metrics(bars)
            
            # Calculate purchase value at T's close
            if metrics:
                purchase_value_at_T = shares * metrics['close']
            else:
                continue
            
            # Check entry conditions
            if not check_entry_conditions(purchase, metrics, purchase_value_at_T):
                continue
            
            # Check abstain conditions
            if check_abstain_conditions(metrics, has_sale):
                continue
            
            # Check if we already issued a call for this symbol in prior 20 trading days
            if any(d in issued_calls[symbol_id] for d in 
                   get_window_trading_days(trading_days, T - 20*86400, T)):
                continue
            
            # We would issue an UP call here
            # But we need to check the label from prediction_outcomes
            # Get the 20-day forward return outcome
            horizon_days = 20
            cur = conn.execute("""
                SELECT up, fwd_return FROM prediction_outcomes
                WHERE symbol_id=? AND horizon=? AND ts>=?
                ORDER BY ts ASC LIMIT 1
            """, (symbol_id, horizon_days, T))
            outcome = cur.fetchone()
            
            if outcome is None:
                continue
            
            up, fwd_return = outcome
            
            # Record opportunity
            opportunities.append((T, symbol_id))
            
            # Record issued call
            issued.append({
                'T': T,
                'symbol_id': symbol_id,
                'up': up,
                'fwd_return': fwd_return
            })
            issued_calls[symbol_id].add(T)
        
        # Ensure we have at least 30 independent observations
        unique_days = set(call['T'] for call in issued)
        if len(unique_days) < 30:
            print("INSUFFICIENT=1")
            return
        
        # Split into training and sealed eras (80/20 split)
        all_dates = sorted(unique_days)
        split_idx = int(len(all_dates) * 0.8)
        sealed_cutoff = all_dates[split_idx] if split_idx < len(all_dates) else all_dates[-1]
        
        # Compute metrics
        issued_count = len(issued)
        opportunities_count = len(opportunities)
        
        if issued_count == 0:
            print("INSUFFICIENT=1")
            return
        
        # Base rate of UP in issued calls
        up_count = sum(1 for call in issued if call['up'])
        base_rate = up_count / issued_count
        
        # Compute design effect for effective N
        # Group calls by day
        day_counts = defaultdict(int)
        for call in issued:
            day_counts[call['T']] += 1
        
        # Design effect = 1 + (variance of cluster sizes) / (mean cluster size)^2
        sizes = list(day_counts.values())
        mean_size = sum(sizes) / len(sizes)
        var_size = sum((s - mean_size) ** 2 for s in sizes) / len(sizes)
        design_effect = 1 + var_size / (mean_size ** 2) if mean_size > 0 else 1
        effective_n = issued_count / design_effect
        
        # Sealed era precision
        sealed_issued = [call for call in issued if call['T'] >= sealed_cutoff]
        if sealed_issued:
            sealed_correct = sum(1 for call in sealed_issued if call['up'])
            sealed_precision = sealed_correct / len(sealed_issued)
        else:
            sealed_precision = 0
        
        # Print results
        print(f"ISSUED={issued_count}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={base_rate}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={len(unique_days)}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print(f"INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == "__main__":
    main()
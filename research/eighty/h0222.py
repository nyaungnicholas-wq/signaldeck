#!/usr/bin/env python3
import sqlite3
from collections import defaultdict
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    c = conn.cursor()
    
    # Load trading days (daily bars)
    c.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    trading_days = [row[0] for row in c.fetchall()]
    
    if len(trading_days) < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Load symbols with basic info
    c.execute("SELECT id, symbol, market, delisted_at FROM symbols")
    symbol_info = {}
    for row in c.fetchall():
        symbol_info[row[0]] = {'symbol': row[1], 'market': row[2], 'delisted_at': row[3]}
    
    # Load daily bars for all symbols
    c.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume 
        FROM bars WHERE tf='1d'
        ORDER BY symbol_id, ts
    """)
    
    bars_by_symbol = defaultdict(list)
    for row in c.fetchall():
        symbol_id, ts, open_, high, low, close, volume = row
        bars_by_symbol[symbol_id].append({
            'ts': ts, 'open': open_, 'high': high, 'low': low, 
            'close': close, 'volume': volume
        })
    
    # Load insider trades (code='P' for purchases)
    c.execute("""
        SELECT symbol_id, filed_ts, shares, price 
        FROM insider_trades 
        WHERE code='P' AND value IS NOT NULL
    """)
    
    insider_purchases = defaultdict(list)
    for row in c.fetchall():
        symbol_id, filed_ts, shares, price = row
        insider_purchases[symbol_id].append({
            'filed_ts': filed_ts,
            'shares': shares,
            'price': price,
            'value': shares * price if shares and price else 0
        })
    
    # Load prediction outcomes for labels
    c.execute("""
        SELECT symbol_id, ts, up 
        FROM prediction_outcomes 
        WHERE horizon=20
    """)
    
    outcomes = {}
    for row in c.fetchall():
        symbol_id, ts, up = row
        outcomes[(symbol_id, ts)] = up
    
    conn.close()
    
    # Prepare data structures
    calls = []  # list of (symbol_id, ts, prediction, label)
    opportunities = 0
    days_with_calls = set()
    
    # Process each trading day
    for t_idx, t in enumerate(trading_days):
        if t_idx < 252:  # Need 252 prior sessions
            continue
        
        # Get prior 252 trading days for volume/price checks
        prior_days = trading_days[t_idx-252:t_idx]
        prior_60_days = trading_days[t_idx-60:t_idx]
        prior_5_days = trading_days[t_idx-5:t_idx]
        prior_20_days = trading_days[t_idx-20:t_idx]
        
        # Check each symbol
        for symbol_id, bars in bars_by_symbol.items():
            # Check if symbol has bars on all required days
            bar_dict = {bar['ts']: bar for bar in bars}
            
            if t not in bar_dict:
                continue
            
            current_bar = bar_dict[t]
            
            # Filter conditions
            if current_bar['close'] < 5:
                continue
            
            # Check for 252 prior sessions
            if not all(day in bar_dict for day in prior_days):
                continue
            
            # Check average daily dollar volume over prior 60 sessions
            total_volume = 0
            valid_days = 0
            for day in prior_60_days:
                if day in bar_dict:
                    total_volume += bar_dict[day]['close'] * bar_dict[day]['volume']
                    valid_days += 1
            
            if valid_days < 60:
                continue
            
            avg_daily_dollar_volume = total_volume / valid_days
            if avg_daily_dollar_volume < 5_000_000:
                continue
            
            # Check close above 50-session SMA
            closes = []
            for day in prior_50_days := trading_days[t_idx-50:t_idx]:
                if day in bar_dict:
                    closes.append(bar_dict[day]['close'])
            
            if len(closes) < 50:
                continue
            
            sma_50 = sum(closes) / 50
            if current_bar['close'] <= sma_50:
                continue
            
            # Check close-to-close return between -1% and +1%
            prev_day = trading_days[t_idx-1]
            if prev_day not in bar_dict:
                continue
            
            prev_close = bar_dict[prev_day]['close']
            if prev_close == 0:
                continue
            
            ret = (current_bar['close'] / prev_close) - 1
            if ret < -0.01 or ret > 0.01:
                continue
            
            # Check 20-session realized volatility in top cross-sectional decile
            # (We'll compute this after gathering all eligible symbols for the day)
            # For now, collect eligible symbols
            eligible_symbols = []
            
            # Check insider purchases
            has_insider_purchase = False
            if symbol_id in insider_purchases:
                for purchase in insider_purchases[symbol_id]:
                    if purchase['filed_ts'] in prior_5_days:
                        # Value is shares * current close (not trade price)
                        purchase_value = purchase['shares'] * current_bar['close']
                        if purchase_value >= 100_000:
                            has_insider_purchase = True
                            break
            
            if not has_insider_purchase:
                continue
            
            eligible_symbols.append({
                'symbol_id': symbol_id,
                'current_bar': current_bar,
                'bars': bar_dict,
                'prior_20_days': prior_20_days
            })
        
        # Compute cross-sectional volatility for eligible symbols
        if len(eligible_symbols) < 10:  # Need enough symbols for decile calculation
            continue
        
        # Calculate 20-day volatility for each eligible symbol
        volatilities = []
        for sym_data in eligible_symbols:
            symbol_id = sym_data['symbol_id']
            bar_dict = sym_data['bars']
            prior_20 = sym_data['prior_20_days']
            
            returns = []
            for i in range(1, len(prior_20)):
                if prior_20[i] in bar_dict and prior_20[i-1] in bar_dict:
                    prev_close = bar_dict[prior_20[i-1]]['close']
                    curr_close = bar_dict[prior_20[i]]['close']
                    if prev_close > 0:
                        returns.append(curr_close / prev_close - 1)
            
            if len(returns) >= 20:
                mean_ret = sum(returns) / len(returns)
                variance = sum((r - mean_ret)**2 for r in returns) / (len(returns) - 1)
                vol = variance ** 0.5
                volatilities.append((symbol_id, vol))
        
        if len(volatilities) < 10:
            continue
        
        # Sort by volatility to find top decile
        volatilities.sort(key=lambda x: x[1])
        decile_idx = len(volatilities) // 10
        top_decile = set(sym_id for sym_id, vol in volatilities[decile_idx*9:])
        
        # Process remaining eligible symbols
        for sym_data in eligible_symbols:
            symbol_id = sym_data['symbol_id']
            current_bar = sym_data['current_bar']
            
            # Skip if volatility in top decile
            if symbol_id in top_decile:
                continue
            
            # Check cooldown: no call in prior 20 trading days for this symbol
            prior_call_exists = False
            for call in calls:
                if call[0] == symbol_id:
                    call_ts = call[1]
                    if call_ts in prior_20_days:
                        prior_call_exists = True
                        break
            
            if prior_call_exists:
                continue
            
            opportunities += 1
            
            # Check label availability
            label = outcomes.get((symbol_id, t))
            if label is None:
                continue
            
            # Issue UP call
            calls.append((symbol_id, t, 1, label))
            days_with_calls.add(t)
    
    # Calculate metrics
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Count hits
    hits = sum(1 for call in calls if call[3] == 1)
    precision = hits / len(calls)
    base_rate = hits / len(calls)  # Same as precision for UP calls only
    
    # Split into sealed era (last 20% of trading days)
    cutoff_idx = int(len(trading_days) * 0.8)
    sealed_days = set(trading_days[cutoff_idx:])
    
    sealed_calls = [call for call in calls if call[1] in sealed_days]
    non_sealed_calls = [call for call in calls if call[1] not in sealed_days]
    
    # Design effect calculation
    # Group calls by day
    calls_by_day = defaultdict(list)
    for call in calls:
        calls_by_day[call[1]].append(call[3])
    
    total_calls = len(calls)
    num_days = len(calls_by_day)
    
    if num_days == 0:
        print("INSUFFICIENT=1")
        return
    
    # Calculate ICC (intra-class correlation)
    overall_mean = sum(call[3] for call in calls) / total_calls
    
    between_variance = 0
    within_variance = 0
    
    for
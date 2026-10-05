import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
        
        # Check for required data
        required_tables = ['bars', 'symbols', 'insider_trades']
        for table in required_tables:
            c.execute(f"SELECT count(*) FROM {table}")
            if c.fetchone()[0] == 0:
                print("INSUFFICIENT=1")
                return
        
        # Load daily bars
        c.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
        bar_data = defaultdict(list)
        for sym, ts, close, volume in c.fetchall():
            if close is not None and volume is not None:
                bar_data[sym].append((ts, close, volume))
        
        # Load insider trades (only purchases, code='P')
        c.execute("""SELECT symbol_id, filed_ts, shares, price 
                     FROM insider_trades WHERE code='P' AND shares IS NOT NULL AND price IS NOT NULL""")
        insider_data = defaultdict(list)
        for sym, filed_ts, shares, price in c.fetchall():
            insider_data[sym].append((filed_ts, shares, price))
        
        # Load symbols
        c.execute("SELECT id FROM symbols")
        symbol_ids = set(row[0] for row in c.fetchall())
        
        conn.close()
        
        # Process data into trading days per symbol
        symbol_days = {}
        for sym in bar_data:
            days = []
            for ts, close, volume in bar_data[sym]:
                # Convert ts to date string for easier comparison
                dt = datetime.utcfromtimestamp(ts)
                days.append((ts, dt.strftime('%Y-%m-%d'), close, volume))
            days.sort(key=lambda x: x[0])
            symbol_days[sym] = days
        
        # Process insider trades per symbol: store (filed_date, shares, price)
        insider_by_sym = {}
        for sym in insider_data:
            trades = []
            for filed_ts, shares, price in insider_data[sym]:
                dt = datetime.utcfromtimestamp(filed_ts)
                trades.append((dt.strftime('%Y-%m-%d'), shares, price))
            insider_by_sym[sym] = trades
        
        # Get all unique trading dates across symbols
        all_dates = set()
        for sym in symbol_days:
            for _, date_str, _, _ in symbol_days[sym]:
                all_dates.add(date_str)
        all_dates = sorted(all_dates)
        
        if len(all_dates) < 100:
            print("INSUFFICIENT=1")
            return
        
        # Split into training (80%) and sealed (20%)
        split_idx = int(len(all_dates) * 0.8)
        train_dates = all_dates[:split_idx]
        sealed_dates = all_dates[split_idx:]
        
        # Helper: find index of date in symbol's data
        def get_index(sym, target_date):
            if sym not in symbol_days:
                return -1
            for i, (_, date_str, _, _) in enumerate(symbol_days[sym]):
                if date_str == target_date:
                    return i
            return -1
        
        # Helper: get close price on date
        def get_close(sym, target_date):
            if sym not in symbol_days:
                return None
            for _, date_str, close, _ in symbol_days[sym]:
                if date_str == target_date:
                    return close
            return None
        
        # Helper: get volume on date
        def get_volume(sym, target_date):
            if sym not in symbol_days:
                return None
            for _, date_str, _, volume in symbol_days[sym]:
                if date_str == target_date:
                    return volume
            return None
        
        # Helper: check if a date is a trading day for symbol
        def is_trading_day(sym, target_date):
            return get_close(sym, target_date) is not None
        
        # Process an era (train or sealed)
        def process_era(dates, is_sealed=False):
            issued_calls = []  # (symbol, t_date, outcome)
            opportunities = 0
            last_call_date = {}  # symbol -> last call date index in all_dates
            
            # Precompute volatility cross-sectional decile per date
            # We'll compute 20-day realized volatility for each symbol-date
            vol_by_date = defaultdict(list)  # date -> list of (sym, vol)
            
            for sym in symbol_days:
                days = symbol_days[sym]
                for i in range(len(days)):
                    ts, date_str, close, _ = days[i]
                    if i < 19:  # need 20 days of returns
                        continue
                    # Get last 20 days' closes
                    closes = [days[j][2] for j in range(i-19, i+1)]
                    # Compute daily returns
                    returns = []
                    for j in range(1, len(closes)):
                        if closes[j-1] and closes[j] and closes[j-1] > 0:
                            returns.append((closes[j] - closes[j-1]) / closes[j-1])
                    if len(returns) >= 19:
                        mean_r = sum(returns) / len(returns)
                        var_r = sum((r - mean_r) ** 2 for r in returns) / (len(returns) - 1)
                        vol = math.sqrt(var_r) if var_r > 0 else 0
                        vol_by_date[date_str].append((sym, vol))
            
            # For each date, compute 90th percentile volatility
            vol_threshold = {}
            for date_str, vols in vol_by_date.items():
                vols_sorted = sorted(vols, key=lambda x: x[1])
                idx = int(len(vols_sorted) * 0.9)
                vol_threshold[date_str] = vols_sorted[idx][1] if idx < len(vols_sorted) else float('inf')
            
            # Process each date in the era
            for t_date in dates:
                # Get symbols present on this date
                syms_on_date = []
                for sym in symbol_days:
                    if is_trading_day(sym, t_date):
                        syms_on_date.append(sym)
                
                # Check volatility threshold for this date
                threshold = vol_threshold.get(t_date, float('inf'))
                
                for sym in syms_on_date:
                    opportunities += 1
                    
                    idx = get_index(sym, t_date)
                    if idx < 0:
                        continue
                    
                    # Need at least 252 prior sessions
                    if idx < 251:
                        continue
                    
                    # Get current close
                    close = get_close(sym, t_date)
                    if close is None or close < 5:
                        continue
                    
                    # Check 60-day average dollar volume
                    if idx >= 59:
                        total_dollar_vol = 0
                        count_vol = 0
                        for j in range(idx-59, idx+1):
                            vol = get_volume(sym, symbol_days[sym][j][1])
                            cl = symbol_days[sym][j][2]
                            if vol is not None and cl is not None:
                                total_dollar_vol += vol * cl
                                count_vol += 1
                        if count_vol > 0:
                            avg_dollar_vol = total_dollar_vol / count_vol
                            if avg_dollar_vol < 5_000_000:
                                continue
                        else:
                            continue
                    else:
                        continue
                    
                    # Check 50-session SMA
                    if idx >= 49:
                        sma_sum = 0
                        for j in range(idx-49, idx+1):
                            cl = symbol_days[sym][j][2]
                            if cl is not None:
                                sma_sum += cl
                        sma = sma_sum / 50
                        if close <= sma:
                            continue
                    else:
                        continue
                    
                    # Check close-to-close return
                    if idx >= 1:
                        prev_close = symbol_days[sym][idx-1][2]
                        if prev_close is None or prev_close <= 0:
                            continue
                        ret = (close - prev_close) / prev_close
                        if ret < -0.01 or ret > 0.01:
                            continue
                    else:
                        continue
                    
                    # Check volatility decile
                    if t_date in vol_threshold and close > 0:
                        # Check if this symbol's volatility is in top decile
                        # We need to compute vol for this symbol on this date
                        # Use precomputed vol_by_date
                        vol_list = vol_by_date.get(t_date, [])
                        sym_vol = None
                        for s, v in vol_list:
                            if s == sym:
                                sym_vol = v
                                break
                        if sym_vol is not None and threshold is not None and sym_vol >= threshold:
                            continue
                    
                    # Check last call date for this symbol (within 20 trading days)
                    if sym in last_call_date:
                        last_idx = last_call_date[sym]
                        last_date_str = all_dates[last_idx]
                        # Calculate number of trading days between last_date and t_date
                        last_date_pos = all_dates.index(last_date_str)
                        current_pos = all_dates.index(t_date)
                        trading_days_diff = current_pos - last_date_pos
                        if trading_days_diff <= 20:
                            continue
                    
                    # Check insider purchases in last 5 trading days ending at t_date
                    # Get last 5 trading days for this symbol up to t_date
                    last_5_dates = []
                    for j in range(max(0, idx-4), idx+1):
                        last_5_dates.append(symbol_days[sym][j][1])
                    
                    found_insider = False
                    if sym in insider_by_sym:
                        for insider_date, shares, price in insider_by_sym[sym]:
                            if insider_date in last_5_dates:
                                # Check value: shares * T's close >= 100000
                                value = shares * close
                                if value >= 100_000:
                                    found_insider = True
                                    break
                    
                    if not found_insider:
                        continue
                    
                    # Issue UP call
                    # Look forward 20 trading days for outcome
                    if idx + 20 < len(symbol_days[sym]):
                        future_date = symbol_days[sym][idx+20][1]
                        future_close = get_close(sym, future_date)
                        if future_close is not None:
                            outcome = future_close > close
                        else:
                            outcome = None
                    else:
                        outcome = None
                    
                    issued_calls.append((sym, t_date, outcome))
                    last_call_date[sym] = all
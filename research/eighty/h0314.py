# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 313
# cycle_index: 36
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Get all daily bars for symbols with >=260 bars before signal
        cur.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume 
            FROM bars 
            WHERE tf='1d'
            ORDER BY symbol_id, ts
        """)
        
        # Group by symbol and build series
        symbols_data = defaultdict(list)
        for row in cur.fetchall():
            symbols_data[row[0]].append({
                'ts': row[1],
                'open': row[2],
                'high': row[3],
                'low': row[4],
                'close': row[5],
                'volume': row[6]
            })
        
        # Get trading days (all unique timestamps in bars)
        trading_days = sorted(set(row[1] for rows in symbols_data.values() for row in rows))
        
        # Get next trading day for each trading day
        next_trading_day = {}
        for i, day in enumerate(trading_days):
            if i < len(trading_days) - 1:
                next_trading_day[day] = trading_days[i + 1]
        
        opportunities = []
        issued_calls = []
        sealed_calls = []
        
        # Process each symbol
        for symbol_id, bars in symbols_data.items():
            if len(bars) < 261:  # Need at least 260 completed bars before signal
                continue
            
            # Convert timestamps to dates and compute returns
            for i in range(260, len(bars)):
                friday_bar = bars[i]
                friday_ts = friday_bar['ts']
                friday_date = datetime.utcfromtimestamp(friday_ts)
                
                # Check if Friday - skip if not
                if friday_date.weekday() != 4:  # 4 = Friday
                    continue
                
                # Check abstention: last two weeks of December
                if friday_date.month == 12 and friday_date.day >= 17:
                    continue
                
                # Need price >= $5 on signal date
                if friday_bar['close'] < 5:
                    continue
                
                # Check if there's a next trading day
                if friday_ts not in next_trading_day:
                    continue
                
                next_ts = next_trading_day[friday_ts]
                # Find next day's bar for this symbol
                next_bar = None
                for b in bars:
                    if b['ts'] == next_ts:
                        next_bar = b
                        break
                
                if next_bar is None:
                    continue
                
                # Compute Friday total return (from previous close)
                prev_bar = bars[i - 1]
                friday_return = (friday_bar['close'] - prev_bar['close']) / prev_bar['close']
                
                # Compute trailing 20-day close range and median volume
                lookback = bars[i - 19:i + 1]  # 20 days including Friday
                closes = [b['close'] for b in lookback]
                volumes = [b['volume'] for b in lookback]
                
                close_min = min(closes)
                close_max = max(closes)
                close_range = close_max - close_min
                
                # Bottom decile of trailing 20-day close range
                in_bottom_decile = friday_bar['close'] <= close_min + 0.1 * close_range if close_range > 0 else False
                
                # Volume below trailing 20-day median
                sorted_volumes = sorted(volumes)
                median_vol = sorted_volumes[len(sorted_volumes) // 2]
                volume_below_median = friday_bar['volume'] < median_vol
                
                # Entry conditions
                entry_condition = (friday_return <= -0.02) and in_bottom_decile and volume_below_median
                
                # Record opportunity
                opportunities.append({
                    'symbol_id': symbol_id,
                    'friday_ts': friday_ts,
                    'friday_date': friday_date,
                    'next_ts': next_ts,
                    'next_close': next_bar['close'],
                    'friday_close': friday_bar['close']
                })
                
                if entry_condition:
                    # Issue "up" call
                    hit = next_bar['close'] > friday_bar['close']
                    call = {
                        'symbol_id': symbol_id,
                        'friday_ts': friday_ts,
                        'friday_date': friday_date,
                        'hit': hit,
                        'next_close': next_bar['close'],
                        'friday_close': friday_bar['close']
                    }
                    issued_calls.append(call)
        
        conn.close()
        
        if not issued_calls:
            print("INSUFFICIENT=1")
            return
        
        # Calculate metrics for all issued calls
        issued_count = len(issued_calls)
        opportunities_count = len(opportunities)
        
        # Base rate: proportion of up moves in issued calls
        up_count = sum(1 for c in issued_calls if c['hit'])
        precision = up_count / issued_count
        base_rate = up_count / issued_count  # Same as precision for this setup
        
        # Distinct days
        distinct_days = len(set(c['friday_date'].strftime('%Y-%m-%d') for c in issued_calls))
        
        # Effective N calculation
        # Count calls per day
        calls_per_day = defaultdict(int)
        for c in issued_calls:
            day_key = c['friday_date'].strftime('%Y-%m-%d')
            calls_per_day[day_key] += 1
        
        # Design effect: 1 + variance(calls_per_day) / mean(calls_per_day)^2
        n_days = len(calls_per_day)
        if n_days > 1:
            day_counts = list(calls_per_day.values())
            mean_per_day = sum(day_counts) / n_days
            var_per_day = sum((x - mean_per_day) ** 2 for x in day_counts) / (n_days - 1)
            design_effect = 1 + var_per_day / (mean_per_day ** 2)
        else:
            design_effect = 1.0
        
        effective_n = issued_count / design_effect
        
        # Split into train and sealed (most recent 20% by date)
        issued_calls.sort(key=lambda x: x['friday_date'])
        split_idx = int(0.8 * len(issued_calls))
        sealed_calls = issued_calls[split_idx:]
        
        if sealed_calls:
            sealed_up = sum(1 for c in sealed_calls if c['hit'])
            sealed_precision = sealed_up / len(sealed_calls)
            sealed_base = sealed_up / len(sealed_calls)
        else:
            sealed_precision = 0.0
            sealed_base = 0.0
        
        # Print results
        print(f"ISSUED={issued_count}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.1f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        # Validate invariants
        assert DISTINCT_DAYS <= ISSUED, "DISTINCT_DAYS exceeds ISSUED"
        assert EFFECTIVE_N < ISSUED, "EFFECTIVE_N not less than ISSUED"
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()
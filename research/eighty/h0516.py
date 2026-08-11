# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 515
# cycle_index: 45
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict

def date_to_ts(date_str):
    """Convert 'YYYY-MM-DD' to unix timestamp."""
    return int(datetime.strptime(date_str, '%Y-%m-%d').timestamp())

def ts_to_date(ts):
    """Convert unix timestamp to 'YYYY-MM-DD' string."""
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.execute("PRAGMA journal_mode = WAL;")
        cursor = conn.cursor()
        
        # Get all insider open-market purchases (Form 4, code='P')
        cursor.execute("""
            SELECT symbol_id, filed_ts 
            FROM insider_trades 
            WHERE code = 'P'
            ORDER BY filed_ts
        """)
        trades = cursor.fetchall()
        
        if len(trades) == 0:
            print("INSUFFICIENT=1")
            return
        
        # Get all symbols with news sentiment data
        cursor.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
        symbols_with_news = set(row[0] for row in cursor.fetchall())
        
        # Get all symbols with at least one insider purchase in past 252 days
        # We'll filter per signal date later
        
        signals = []
        
        for symbol_id, signal_ts in trades:
            # Check if symbol has news sentiment
            if symbol_id not in symbols_with_news:
                continue
            
            # Convert signal date to date string for sentiment queries
            signal_date = ts_to_date(signal_ts)
            
            # Check news sentiment condition: 10-day MA > 20-day MA on signal date
            cursor.execute("""
                SELECT mean_score FROM sentiment_features
                WHERE symbol_id = ? AND day <= ?
                ORDER BY day DESC
                LIMIT 20
            """, (symbol_id, signal_date))
            recent_sentiment = [row[0] for row in cursor.fetchall()]
            
            if len(recent_sentiment) < 20:
                continue  # Need at least 20 days of data for 20-day MA
            
            # Calculate 10-day and 20-day moving averages
            ma_10 = sum(recent_sentiment[:10]) / 10.0
            ma_20 = sum(recent_sentiment[:20]) / 20.0
            
            if ma_10 <= ma_20:
                continue  # Condition not met
            
            # Check 13F condition: most recent 13F within past 90 days shows QoQ decline
            # We need to find the most recent quarter with 13F data within 90 days
            # Remember inst_holdings has period (quarter end) and we need to lag by 45 days
            # To avoid lookahead, we only consider 13F data that is at least 45 days old
            signal_date_obj = datetime.utcfromtimestamp(signal_ts)
            ninety_days_ago = signal_date_obj - timedelta(days=90)
            forty_five_days_ago = signal_date_obj - timedelta(days=45)
            
            # Get the most recent period that is between 45 and 90 days before signal
            cursor.execute("""
                SELECT period, SUM(shares) as total_shares
                FROM inst_holdings
                WHERE symbol_id = ?
                AND datetime(period, 'unixepoch', '+45 days') <= ?
                AND datetime(period, 'unixepoch') >= ?
                GROUP BY period
                ORDER BY period DESC
                LIMIT 1
            """, (symbol_id, signal_date, ninety_days_ago.strftime('%Y-%m-%d')))
            current_quarter = cursor.fetchone()
            
            if not current_quarter:
                continue  # No 13F data within required window
            
            current_period, current_shares = current_quarter
            
            # Get the previous quarter
            cursor.execute("""
                SELECT period, SUM(shares) as total_shares
                FROM inst_holdings
                WHERE symbol_id = ?
                AND period < ?
                GROUP BY period
                ORDER BY period DESC
                LIMIT 1
            """, (symbol_id, current_period))
            prev_quarter = cursor.fetchone()
            
            if not prev_quarter:
                continue  # No previous quarter data
            
            prev_period, prev_shares = prev_quarter
            
            # Check for decline in shares held
            if current_shares >= prev_shares:
                continue  # No decline
            
            # Also check that the signal date is within 252 trading days
            # We need at least 252 trading days of price data before signal date
            cursor.execute("""
                SELECT COUNT(*) FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts < ?
            """, (symbol_id, signal_ts))
            trading_days_before = cursor.fetchone()[0]
            
            if trading_days_before < 252:
                continue
            
            # Store signal with all needed info
            signals.append({
                'symbol_id': symbol_id,
                'signal_ts': signal_ts,
                'signal_date': signal_date,
                'entry_date': signal_date  # We'll enter on disclosure date
            })
        
        if len(signals) == 0:
            print("INSUFFICIENT=1")
            return
        
        # Calculate labels (21-day forward return)
        labels = []
        for signal in signals:
            symbol_id = signal['symbol_id']
            signal_ts = signal['signal_ts']
            
            # Get the close price on signal date (entry price)
            cursor.execute("""
                SELECT close, ts FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts ASC
                LIMIT 1
            """, (symbol_id, signal_ts))
            entry_row = cursor.fetchone()
            
            if not entry_row:
                continue
            
            entry_close, entry_ts = entry_row
            entry_date = ts_to_date(entry_ts)
            
            # Get the close price 21 trading days after entry
            cursor.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts > ?
                ORDER BY ts ASC
                LIMIT 21
            """, (symbol_id, entry_ts))
            future_rows = cursor.fetchall()
            
            if len(future_rows) < 21:
                continue  # Insufficient data for label
            
            exit_close = future_rows[20][0]  # 21st day is index 20
            
            # Calculate forward return
            fwd_return = (exit_close - entry_close) / entry_close
            up = 1 if fwd_return > 0 else 0
            
            labels.append({
                'symbol_id': symbol_id,
                'signal_date': entry_date,
                'up': up,
                'fwd_return': fwd_return
            })
        
        if len(labels) == 0:
            print("INSUFFICIENT=1")
            return
        
        # Group by (symbol_id, signal_date) for independent observations
        obs = defaultdict(lambda: {'ups': 0, 'total': 0})
        for label in labels:
            key = (label['symbol_id'], label['signal_date'])
            obs[key]['ups'] += label['up']
            obs[key]['total'] += 1
        
        # Convert to list with binary outcomes (at least one up in the day)
        observations = []
        for key, value in obs.items():
            # If any prediction for that symbol/day was up, count as hit
            observations.append({
                'symbol_id': key[0],
                'signal_date': key[1],
                'hit': 1 if value['ups'] > 0 else 0
            })
        
        # Sort by signal_date for train/test split
        observations.sort(key=lambda x: x['signal_date'])
        
        # Split into train and sealed (most recent 20%)
        n = len(observations)
        split_idx = int(n * 0.8)
        train = observations[:split_idx]
        sealed = observations[split_idx:]
        
        # Calculate metrics
        issued = len(train)
        if issued == 0:
            print("INSUFFICIENT=1")
            return
        
        hits = sum(obs['hit'] for obs in train)
        precision = hits / issued
        
        # Base rate: proportion of ups within issued calls
        # For base rate, we need to know how many were actually up
        # But we only have hits (1 if any up for that symbol/day)
        # We'll calculate base rate as proportion of observations that are hits
        # Actually, base rate should be of the predicted class within the issued subset
        # Here predicted class is "up", so base rate = hits/issued
        base_rate = precision
        
        # Distinct days in issued calls
        distinct_days = len(set(obs['signal_date'] for obs in train))
        
        # Design effect calculation
        # Count calls per day
        calls_per_day = defaultdict(int)
        for obs in train:
            calls_per_day[obs['signal_date']] += 1
        
        daily_counts = list(calls_per_day.values())
        mean_daily = sum(daily_counts) / len(daily_counts)
        var_daily = sum((x - mean_daily) ** 2 for x in daily_counts) / len(daily_counts)
        
        if mean_daily == 0:
            design_effect = 1
        else:
            design_effect = 1 + (var_daily / (mean_daily ** 2))
        
        effective_n = issued / design_effect if design_effect > 1 else issued
        
        # Sealed era precision
        sealed_issued = len(sealed)
        sealed_hits = sum(obs['hit'] for obs in sealed)
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={n}")  # Total observations considered
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
        conn.close()
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
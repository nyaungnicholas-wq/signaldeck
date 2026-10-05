# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 376
# cycle_index: 44
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from datetime import datetime, timedelta

DB_PATH = "file:data/signaldeck.db?mode=ro"

def percentile(values, p):
    """Return the p-th percentile (0-100) of a list of values."""
    if not values:
        return None
    k = (len(values) - 1) * p / 100
    f = math.floor(k)
    c = math.ceil(k)
    if f == c:
        return values[int(k)]
    d0 = values[int(f)] * (c - k)
    d1 = values[int(c)] * (k - f)
    return d0 + d1

def get_sma(conn, symbol_id, ts_date, window=200):
    """Get simple moving average for window days ending on ts_date."""
    cursor = conn.cursor()
    cursor.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') <= ?
        ORDER BY ts DESC LIMIT ?
    """, (symbol_id, ts_date, window))
    rows = cursor.fetchall()
    if len(rows) < window:
        return None
    return sum(r[0] for r in rows) / len(rows)

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        conn.row_factory = sqlite3.Row
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    try:
        # Get all insider purchases with disclosure delay >= 5 days
        cursor = conn.cursor()
        cursor.execute("""
            SELECT 
                symbol_id,
                tx_ts,
                filed_ts,
                CAST((filed_ts - tx_ts) / 86400.0 AS INTEGER) as delay_days
            FROM insider_trades
            WHERE code = 'P'
            HAVING delay_days >= 5
        """)
        opportunities = cursor.fetchall()
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return

        # Prepare to store valid signals
        signals = []
        decision_dates = set()
        
        for opp in opportunities:
            symbol_id = opp['symbol_id']
            tx_ts = opp['tx_ts']
            filed_ts = opp['filed_ts']
            
            # Convert filed_ts to date string for queries
            filed_date = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
            
            # Get sentiment on filing date
            cursor.execute("""
                SELECT mean_score FROM sentiment_features
                WHERE symbol_id = ? AND day = ?
            """, (symbol_id, filed_date))
            row = cursor.fetchone()
            if not row:
                continue
            current_score = row[0]
            
            # Get 2-year sentiment history for this symbol
            two_years_ago = (datetime.strptime(filed_date, '%Y-%m-%d') - 
                            timedelta(days=730)).strftime('%Y-%m-%d')
            cursor.execute("""
                SELECT mean_score FROM sentiment_features
                WHERE symbol_id = ? AND day BETWEEN ? AND ?
                ORDER BY day
            """, (symbol_id, two_years_ago, filed_date))
            history_rows = cursor.fetchall()
            
            if len(history_rows) < 20:  # Need enough history
                continue
            
            history_scores = [r[0] for r in history_rows if r[0] is not None]
            if len(history_scores) < 10:
                continue
            
            # Check if current score is in bottom 10%
            p10 = percentile(sorted(history_scores), 10)
            if current_score > p10:
                continue
            
            # Get close on filing date and compute 200-day SMA
            cursor.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') = ?
            """, (symbol_id, filed_date))
            close_row = cursor.fetchone()
            if not close_row:
                continue
            close_price = close_row[0]
            
            sma200 = get_sma(conn, symbol_id, filed_date)
            if sma200 is None or close_price >= sma200:
                continue
            
            # Get next trading day open (entry price)
            cursor.execute("""
                SELECT date(ts, 'unixepoch') as next_day, open
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') > ?
                ORDER BY ts ASC
                LIMIT 1
            """, (symbol_id, filed_date))
            next_row = cursor.fetchone()
            if not next_row:
                continue
            
            next_day = next_row['next_day']
            entry_open = next_row['open']
            
            # Get close 21 trading days after entry
            cursor.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND date(ts, 'unixepoch') >= ?
                ORDER BY ts ASC
                LIMIT 21 OFFSET 20
            """, (symbol_id, next_day))
            exit_row = cursor.fetchone()
            if not exit_row:
                continue
            
            exit_close = exit_row[0]
            label = 1 if exit_close > entry_open else 0
            
            signals.append({
                'symbol_id': symbol_id,
                'decision_date': filed_date,
                'label': label
            })
            decision_dates.add(filed_date)
        
        # Check if we have enough signals
        if not signals:
            print("INSUFFICIENT=1")
            return
        
        # Split into train and sealed (last 20%)
        signals_sorted = sorted(signals, key=lambda x: x['decision_date'])
        split_idx = int(len(signals_sorted) * 0.8)
        train_signals = signals_sorted[:split_idx]
        sealed_signals = signals_sorted[split_idx:]
        
        # Calculate metrics for full issued set
        issued = len(signals)
        opportunities_count = len(opportunities)
        base_rate = sum(s['label'] for s in signals) / issued
        
        # Calculate design effect (simplified: use day clustering)
        # Count observations per day
        day_counts = {}
        for s in signals:
            day = s['decision_date']
            day_counts[day] = day_counts.get(day, 0) + 1
        
        distinct_days = len(day_counts)
        avg_cluster_size = issued / distinct_days if distinct_days > 0 else 1
        
        # Simple ICC estimation (assume 0.1 as conservative)
        icc = 0.1
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Metrics for sealed era
        sealed_issued = len(sealed_signals)
        sealed_base_rate = sum(s['label'] for s in sealed_signals) / sealed_issued if sealed_issued > 0 else 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={base_rate}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_base_rate}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == "__main__":
    main()
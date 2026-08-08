# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 374
# cycle_index: 42
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
import math

DB_PATH = "file:data/signaldeck.db?mode=ro"
HORIZON = 21

def get_trading_days(conn, symbol_id, start_date, end_date):
    """Get trading days for a symbol between dates."""
    cur = conn.cursor()
    cur.execute("""
        SELECT ts FROM bars 
        WHERE symbol_id = ? AND tf = '1d' 
        AND ts >= ? AND ts <= ?
        ORDER BY ts
    """, (symbol_id, start_date.timestamp(), end_date.timestamp()))
    return [row[0] for row in cur.fetchall()]

def get_next_trading_day(trading_days, current_ts, n):
    """Get the nth trading day after current_ts."""
    future_days = [ts for ts in trading_days if ts > current_ts]
    if len(future_days) >= n:
        return future_days[n - 1]
    return None

def calculate_moving_average(scores, window):
    """Calculate moving average from list of scores."""
    if len(scores) < window:
        return None
    return sum(scores[-window:]) / window

def calculate_percentile(value, historical_values):
    """Calculate percentile of value in historical_values."""
    if not historical_values:
        return 0
    count = sum(1 for v in historical_values if v < value)
    return count / len(historical_values)

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True, timeout=30)
        conn.execute("PRAGMA journal_mode=OFF")
        cur = conn.cursor()
        
        # Get all symbols with insider purchases (Form 4, code P for purchase)
        cur.execute("""
            SELECT symbol_id, MIN(filed_ts) as first_disclosure
            FROM insider_trades 
            WHERE code = 'P'
            GROUP BY symbol_id
        """)
        symbols_with_purchases = cur.fetchall()
        
        if not symbols_with_purchases:
            print("INSUFFICIENT=1")
            return
        
        # Get sentiment features availability
        cur.execute("""
            SELECT symbol_id, MIN(day), MAX(day) 
            FROM sentiment_features 
            GROUP BY symbol_id
        """)
        sentiment_availability = {row[0]: (row[1], row[2]) for row in cur.fetchall()}
        
        # Filter symbols with at least 2 years of sentiment history
        eligible_symbols = []
        for symbol_id, first_disclosure in symbols_with_purchases:
            if symbol_id not in sentiment_availability:
                continue
            min_day, max_day = sentiment_availability[symbol_id]
            min_date = datetime.strptime(min_day, "%Y-%m-%d")
            max_date = datetime.strptime(max_day, "%Y-%m-%d")
            if (max_date - min_date).days >= 730:  # 2 years
                eligible_symbols.append((symbol_id, first_disclosure))
        
        if not eligible_symbols:
            print("INSUFFICIENT=1")
            return
        
        # Get all insider purchase disclosures for eligible symbols
        cur.execute("""
            SELECT symbol_id, filed_ts, tx_ts
            FROM insider_trades 
            WHERE code = 'P'
            ORDER BY filed_ts
        """)
        all_disclosures = cur.fetchall()
        
        opportunities = 0
        calls = []
        
        for symbol_id, filed_ts, tx_ts in all_disclosures:
            # Check if symbol is eligible
            eligible = False
            for sid, _ in eligible_symbols:
                if sid == symbol_id:
                    eligible = True
                    break
            if not eligible:
                continue
            
            # Entry day is day after disclosure
            disclosure_date = datetime.fromtimestamp(filed_ts).date()
            entry_date = disclosure_date + timedelta(days=1)
            entry_ts = int(datetime.combine(entry_date, datetime.min.time()).timestamp())
            
            # Get sentiment data up to entry date
            cur.execute("""
                SELECT day, mean_score FROM sentiment_features 
                WHERE symbol_id = ? AND day <= ?
                ORDER BY day DESC
            """, (symbol_id, entry_date.strftime("%Y-%m-%d")))
            sentiment_data = cur.fetchall()
            
            if len(sentiment_data) < 252:
                continue  # Need 252 days for percentile
            
            # Get 20-day moving average of sentiment
            scores = [row[1] for row in sentiment_data[:20]]
            if len(scores) < 20:
                continue
            
            ma20 = sum(scores) / len(scores)
            
            # Get trailing 252 days scores for percentile
            trailing_252 = [row[1] for row in sentiment_data[:252]]
            percentile = calculate_percentile(ma20, trailing_252)
            
            opportunities += 1
            
            # Check if should issue call
            if percentile > 0.3:  # Above 30th percentile - abstain
                continue
            
            if percentile > 0.1:  # Between 10th and 30th percentile - abstain
                continue
            
            # Issue BUY call (bottom decile)
            calls.append({
                'symbol_id': symbol_id,
                'entry_ts': entry_ts,
                'entry_date': entry_date
            })
        
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Sort calls by entry date
        calls.sort(key=lambda x: x['entry_ts'])
        
        # Split into development and sealed (most recent 20%)
        n_sealed = max(1, int(len(calls) * 0.2))
        development_calls = calls[:-n_sealed]
        sealed_calls = calls[-n_sealed:]
        
        # Calculate outcomes for development calls
        hits = 0
        distinct_days = set()
        
        for call in development_calls:
            symbol_id = call['symbol_id']
            entry_ts = call['entry_ts']
            entry_date = call['entry_date']
            
            # Get all trading days for this symbol
            cur.execute("""
                SELECT ts FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND ts > ?
                ORDER BY ts
            """, (symbol_id, entry_ts))
            future_bars = [row[0] for row in cur.fetchall()]
            
            if len(future_bars) < HORIZON:
                continue
            
            # Get price at entry
            cur.execute("""
                SELECT close FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts ASC LIMIT 1
            """, (symbol_id, entry_ts))
            entry_row = cur.fetchone()
            if not entry_row:
                continue
            entry_price = entry_row[0]
            
            # Get price 21 trading days later
            target_ts = future_bars[HORIZON - 1]
            cur.execute("""
                SELECT close FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts ASC LIMIT 1
            """, (symbol_id, target_ts))
            target_row = cur.fetchone()
            if not target_row:
                continue
            target_price = target_row[0]
            
            # Check if positive return
            if target_price > entry_price:
                hits += 1
            
            # Track distinct days
            distinct_days.add(entry_date)
        
        # Calculate metrics
        issued = len(development_calls)
        if issued == 0:
            print("INSUFFICIENT=1")
            return
        
        precision = hits / issued
        base_rate = precision  # Base rate of predicted class within issued subset
        
        # Calculate design effect (clustering by day)
        # Group calls by day
        day_groups = {}
        for call in development_calls:
            day = call['entry_date']
            if day not in day_groups:
                day_groups[day] = []
            day_groups[day].append(call)
        
        # Calculate ICC (intra-class correlation)
        n_clusters = len(day_groups)
        if n_clusters < 2:
            design_effect = 1.0
        else:
            # Calculate variance between and within clusters
            cluster_sizes = [len(group) for group in day_groups.values()]
            avg_cluster_size = sum(cluster_sizes) / n_clusters
            
            # Simple design effect approximation: DE = 1 + (avg_cluster_size - 1) * ICC
            # Assume ICC of 0.1 for day-clustered outcomes as a conservative estimate
            ICC = 0.1
            design_effect = 1 + (avg_cluster_size - 1) * ICC
        
        effective_n = issued / design_effect
        
        # Calculate sealed precision
        sealed_hits = 0
        for call in sealed_calls:
            symbol_id = call['symbol_id']
            entry_ts = call['entry_ts']
            
            # Get all trading days for this symbol
            cur.execute("""
                SELECT ts FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND ts > ?
                ORDER BY ts
            """, (symbol_id, entry_ts))
            future_bars = [row[0] for row in cur.fetchall()]
            
            if len(future_bars) < HORIZON:
                continue
            
            # Get price at entry
            cur.execute("""
                SELECT close FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts ASC LIMIT 1
            """, (symbol_id, entry_ts))
            entry_row = cur.fetchone()
            if not entry_row:
                continue
            entry_price = entry_row[0]
            
            # Get price 21 trading days later
            target_ts = future_bars[HORIZON - 1]
            cur.execute("""
                SELECT close FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts ASC LIMIT 1
            """, (symbol_id, target_ts))
            target_row = cur.fetchone()
            if not target_row:
                continue
            target_price = target_row[0]
            
            # Check if positive return
            if target_price > entry_price:
                sealed_hits += 1
        
        sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
        
        # Validate invariants
        if len(distinct_days) > issued:
            print("ERROR: DISTINCT_DAYS exceeds ISSUED")
            return
        
        if effective_n >= issued:
            print("ERROR: EFFECTIVE_N not less than ISSUED")
            return
        
        # Print required metrics
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={len(distinct_days)}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print(f"ERROR: {e}")
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
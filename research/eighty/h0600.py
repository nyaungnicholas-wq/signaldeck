# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 599
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime

DB_PATH = 'data/signaldeck.db'

def get_connection():
    return sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)

def get_universe(conn):
    """Return list of symbol_ids meeting data requirements."""
    # 5 years of daily data = 1260 trading days (252 per year)
    # Insider trades present
    # At least 4 quarters of EPS data
    query = """
        SELECT b.symbol_id
        FROM bars b
        WHERE b.tf = '1d'
        GROUP BY b.symbol_id
        HAVING COUNT(*) >= 1260
        AND EXISTS (
            SELECT 1 FROM insider_trades it
            WHERE it.symbol_id = b.symbol_id AND it.code = 'P'
        )
        AND EXISTS (
            SELECT 1 FROM fundamentals f
            WHERE f.symbol_id = b.symbol_id AND f.metric = 'EPS'
            GROUP BY f.symbol_id
            HAVING COUNT(DISTINCT f.as_of) >= 4
        )
        AND EXISTS (
            SELECT 1 FROM symbols s
            WHERE s.id = b.symbol_id AND s.market = 'stocks'
        )
    """
    cur = conn.execute(query)
    return [row[0] for row in cur.fetchall()]

def compute_rsi(conn, symbol_id, lookback=14):
    """Compute RSI for each day for a symbol."""
    query = """
        SELECT ts, close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """
    rows = conn.execute(query, (symbol_id,)).fetchall()
    if len(rows) < lookback + 1:
        return {}
    
    closes = [row[1] for row in rows]
    timestamps = [row[0] for row in rows]
    
    gains = []
    losses = []
    for i in range(1, len(closes)):
        change = closes[i] - closes[i-1]
        gains.append(max(change, 0))
        losses.append(-min(change, 0))
    
    avg_gain = sum(gains[:lookback]) / lookback
    avg_loss = sum(losses[:lookback]) / lookback
    
    rsi_values = {}
    if avg_loss == 0:
        rsi_values[timestamps[lookback]] = 100
    else:
        rs = avg_gain / avg_loss
        rsi_values[timestamps[lookback]] = 100 - (100 / (1 + rs))
    
    for i in range(lookback, len(gains)):
        avg_gain = (avg_gain * (lookback - 1) + gains[i]) / lookback
        avg_loss = (avg_loss * (lookback - 1) + losses[i]) / lookback
        if avg_loss == 0:
            rsi_values[timestamps[i+1]] = 100
        else:
            rs = avg_gain / avg_loss
            rsi_values[timestamps[i+1]] = 100 - (100 / (1 + rs))
    
    return rsi_values

def get_eps_quarters(conn, symbol_id):
    """Get quarterly EPS data ordered by as_of."""
    query = """
        SELECT as_of, value
        FROM fundamentals
        WHERE symbol_id = ? AND metric = 'EPS'
        ORDER BY as_of
    """
    return conn.execute(query, (symbol_id,)).fetchall()

def check_eps_growth_condition(conn, symbol_id, current_ts):
    """Check if trailing four-quarter EPS growth increased for two consecutive quarters."""
    eps_data = get_eps_quarters(conn, symbol_id)
    if len(eps_data) < 8:
        return False
    
    # Convert as_of (quarter end) to timestamp for comparison
    quarters = []
    for as_of_str, value in eps_data:
        try:
            dt = datetime.strptime(as_of_str, '%Y-%m-%d')
            ts = int(dt.timestamp())
            quarters.append((ts, value))
        except:
            continue
    
    # Get EPS values for last 8 quarters before current_ts
    available = [(ts, val) for ts, val in quarters if ts < current_ts]
    if len(available) < 8:
        return False
    
    last_8 = available[-8:]
    
    # Calculate trailing four-quarter growth for two most recent periods
    # Growth = (sum of last 4 quarters) / (sum of previous 4 quarters) - 1
    sum_recent = sum(val for _, val in last_8[4:])
    sum_prev = sum(val for _, val in last_8[:4])
    if sum_prev == 0:
        return False
    
    growth_recent = sum_recent / sum_prev - 1
    
    # Now get one more quarter back to compute previous growth
    if len(available) < 9:
        return False
    
    prev_8 = available[-9:-1]  # quarters 1-8
    sum_recent_prev = sum(val for _, val in prev_8[4:])
    sum_prev_prev = sum(val for _, val in prev_8[:4])
    if sum_prev_prev == 0:
        return False
    
    growth_prev = sum_recent_prev / sum_prev_prev - 1
    
    return growth_recent > growth_prev

def get_insider_purchase_dates(conn, symbol_id):
    """Get dates of insider purchases (code='P') based on filed_ts."""
    query = """
        SELECT filed_ts
        FROM insider_trades
        WHERE symbol_id = ? AND code = 'P'
        ORDER BY filed_ts
    """
    rows = conn.execute(query, (symbol_id,)).fetchall()
    return [row[0] for row in rows]

def get_news_sentiment(conn, symbol_id, current_ts, days=10):
    """Get average news sentiment over last days trading days."""
    # Get trading days (bars) before current_ts
    query = """
        SELECT ts
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts < ?
        ORDER BY ts DESC
        LIMIT ?
    """
    trading_days = [row[0] for row in conn.execute(query, (symbol_id, current_ts, days)).fetchall()]
    if not trading_days:
        return None
    
    # Convert to dates for sentiment_features
    trading_dates = []
    for ts in trading_days:
        dt = datetime.fromtimestamp(ts)
        trading_dates.append(dt.strftime('%Y-%m-%d'))
    
    if not trading_dates:
        return None
    
    placeholders = ','.join(['?' for _ in trading_dates])
    query = f"""
        SELECT mean_score
        FROM sentiment_features
        WHERE symbol_id = ? AND day IN ({placeholders})
    """
    params = [symbol_id] + trading_dates
    scores = [row[0] for row in conn.execute(query, params).fetchall() if row[0] is not None]
    
    if not scores:
        return None
    
    return sum(scores) / len(scores)

def get_label(conn, symbol_id, horizon_ts):
    """Get label from prediction_outcomes."""
    query = """
        SELECT up, fwd_return
        FROM prediction_outcomes
        WHERE symbol_id = ? AND ts = ? AND horizon = 21
    """
    row = conn.execute(query, (symbol_id, horizon_ts)).fetchone()
    if row:
        return row[0]  # up label
    return None

def main():
    conn = get_connection()
    
    try:
        universe = get_universe(conn)
        if not universe:
            print("INSUFFICIENT=1")
            return
        
        calls = []  # (symbol_id, decision_ts, label, is_sealed)
        
        # Get all trading days for universe to iterate
        query = """
            SELECT symbol_id, ts, close
            FROM bars
            WHERE tf = '1d' AND symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join(['?' for _ in universe]))
        
        all_bars = conn.execute(query, universe).fetchall()
        
        # Organize bars by symbol
        symbol_bars = {}
        for symbol_id, ts, close in all_bars:
            if symbol_id not in symbol_bars:
                symbol_bars[symbol_id] = []
            symbol_bars[symbol_id].append((ts, close))
        
        # Get last 20% as sealed era
        all_timestamps = [ts for bars in symbol_bars.values() for ts, _ in bars]
        if not all_timestamps:
            print("INSUFFICIENT=1")
            return
        
        all_timestamps_sorted = sorted(all_timestamps)
        seal_index = int(len(all_timestamps_sorted) * 0.8)
        seal_threshold = all_timestamps_sorted[seal_index]
        
        # Process each symbol
        for symbol_id in universe:
            if symbol_id not in symbol_bars:
                continue
            
            bars = symbol_bars[symbol_id]
            rsi_values = compute_rsi(conn, symbol_id)
            insider_dates = get_insider_purchase_dates(conn, symbol_id)
            
            # Create set of insider purchase timestamps
            insider_ts_set = set()
            for ts in insider_dates:
                dt = datetime.utcfromtimestamp(ts)
                day_start = dt.replace(hour=0, minute=0, second=0)
                insider_ts_set.add(int(day_start.timestamp()))
            
            # Process each trading day for this symbol
            for i, (ts, close) in enumerate(bars):
                # Skip if not enough RSI data
                if ts not in rsi_values:
                    continue
                
                rsi = rsi_values[ts]
                
                # Check RSI condition (entry requires <70)
                if rsi >= 70:
                    continue
                
                # Check if there's an insider purchase on this day
                if ts not in insider_ts_set:
                    continue
                
                # Check EPS growth condition
                if not check_eps_growth_condition(conn, symbol_id, ts):
                    continue
                
                # Check news sentiment (abstain if negative)
                sentiment = get_news_sentiment(conn, symbol_id, ts)
                if sentiment is None or sentiment < 0:
                    continue
                
                # Get label at horizon (21 trading days later)
                # Find the bar 21 trading days ahead
                if i + 21 >= len(bars):
                    continue
                
                horizon_ts = bars[i + 21][0]
                label = get_label(conn, symbol_id, horizon_ts)
                if label is None:
                    continue
                
                is_sealed = ts >= seal_threshold
                calls.append((symbol_id, ts, label, is_sealed))
        
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Compute statistics
        issued = len(calls)
        opportunities = issued  # Each call is an issued opportunity
        hits = sum(1 for _, _, label, _ in calls if label == 1)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate within issued subset
        base_rate = precision  # Same as precision for up calls
        
        # Distinct days
        distinct_days = len(set(ts for _, ts, _, _ in calls))
        
        # Design effect: cluster by day
        day_counts = {}
        for _, ts, _, _ in calls:
            day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
            day_counts[day] = day_counts.get(day, 0) + 1
        
        n_days = len(day_counts)
        design_effect = (issued / n_days) if n_days > 0 else issued
        effective_n = issued / design_effect
        
        # Sealed era statistics
        sealed_calls = [(s, t, l) for s, t, l, sealed in calls if sealed]
        sealed_issued = len(sealed_calls)
        sealed_hits = sum(1 for _, _, l in sealed_calls if l == 1)
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    finally:
        conn.close()

if __name__ == "__main__":
    main()
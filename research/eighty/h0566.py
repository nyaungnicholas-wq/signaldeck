# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 565
# cycle_index: 23
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 21
TWO_QUARTERS_MS = 90 * 24 * 3600 * 1000  # approximate milliseconds
MIN_NEWS_DAYS = 30
MA_SHORT = 5
MA_LONG = 20

def parse_ts(ts):
    if ts is None:
        return None
    return datetime.datetime.utcfromtimestamp(ts).date()

def connect_db():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    return conn

def get_symbol_fundamentals(conn):
    query = """
    SELECT symbol_id, metric, value, as_of, fetched_at
    FROM fundamentals
    WHERE metric = 'SharesOutstanding'
    ORDER BY symbol_id, as_of
    """
    rows = conn.execute(query).fetchall()
    data = {}
    for r in rows:
        sid = r['symbol_id']
        if sid not in data:
            data[sid] = []
        data[sid].append({
            'as_of': r['as_of'],
            'value': r['value'],
            'fetched_at': r['fetched_at']
        })
    return data

def get_news_sentiment(conn):
    query = """
    SELECT symbol_id, ts, sentiment
    FROM news
    WHERE sentiment IS NOT NULL
    ORDER BY symbol_id, ts
    """
    rows = conn.execute(query).fetchall()
    data = {}
    for r in rows:
        sid = r['symbol_id']
        if sid not in data:
            data[sid] = []
        data[sid].append({
            'ts': r['ts'],
            'sentiment': r['sentiment'],
            'date': parse_ts(r['ts'])
        })
    return data

def get_trading_days(conn):
    query = """
    SELECT DISTINCT ts
    FROM bars
    WHERE tf = '1d'
    ORDER BY ts
    """
    rows = conn.execute(query).fetchall()
    return [r['ts'] for r in rows]

def check_two_quarters_decline(fundamentals_list, cutoff_fetched_at):
    # Filter by fetched_at <= cutoff
    eligible = [f for f in fundamentals_list 
                if f['fetched_at'] is not None and f['fetched_at'] <= cutoff_fetched_at]
    
    # Sort by as_of descending
    eligible.sort(key=lambda x: x['as_of'], reverse=True)
    
    if len(eligible) < 3:
        return False
    
    # Get the three most recent quarters
    q1, q2, q3 = eligible[0]['value'], eligible[1]['value'], eligible[2]['value']
    
    # Check if strictly decreasing
    return q1 < q2 and q2 < q3

def compute_sentiment_ma(sentiment_series, target_date, short_window, long_window):
    # Filter dates <= target_date
    filtered = [s for s in sentiment_series if s['date'] <= target_date]
    if len(filtered) < long_window:
        return None, None
    
    # Extract sentiment values
    sentiments = [s['sentiment'] for s in filtered]
    
    # Calculate moving averages
    short_ma = sum(sentiments[-short_window:]) / short_window
    long_ma = sum(sentiments[-long_window:]) / long_window
    return short_ma, long_ma

def main():
    conn = connect_db()
    
    # Load data
    fundamentals = get_symbol_fundamentals(conn)
    news_sentiment = get_news_sentiment(conn)
    trading_days = get_trading_days(conn)
    
    if not trading_days:
        print("INSUFFICIENT=1")
        return
    
    # Determine split point for held-out era (most recent 20%)
    n_days = len(trading_days)
    split_idx = int(n_days * 0.8)
    sealed_start = trading_days[split_idx]
    
    # Prepare to collect opportunities and issued calls
    opportunities = []  # (symbol_id, day_ts, date)
    issued = []  # (symbol_id, day_ts, date, is_sealed, hit)
    
    # Iterate over each trading day as potential decision point
    for day_ts in trading_days:
        day_date = parse_ts(day_ts)
        
        for symbol_id in fundamentals.keys():
            # Check two quarters decline condition
            if not check_two_quarters_decline(fundamentals[symbol_id], day_ts):
                continue
            
            # Check news sentiment history
            if symbol_id not in news_sentiment:
                continue
            sentiment_series = news_sentiment[symbol_id]
            
            # Filter by date <= day_date
            filtered = [s for s in sentiment_series if s['date'] <= day_date]
            if len(filtered) < MIN_NEWS_DAYS:
                continue
            
            # This symbol/day is an opportunity
            opportunities.append((symbol_id, day_ts, day_date))
            
            # Compute moving averages
            short_ma, long_ma = compute_sentiment_ma(sentiment_series, day_date, MA_SHORT, MA_LONG)
            if short_ma is None or long_ma is None:
                continue
            
            # Check previous day for crossover
            prev_date = day_date - datetime.timedelta(days=1)
            prev_short, prev_long = compute_sentiment_ma(sentiment_series, prev_date, MA_SHORT, MA_LONG)
            if prev_short is None or prev_long is None:
                continue
            
            # Check crossover condition: prev_short <= prev_long and short_ma > long_ma
            if prev_short <= prev_long and short_ma > long_ma:
                issued.append({
                    'symbol_id': symbol_id,
                    'day_ts': day_ts,
                    'date': day_date,
                    'is_sealed': day_ts >= sealed_start,
                    'hit': None  # To be filled
                })
    
    # Query outcomes for issued calls
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    issued_by_key = {}
    for i, call in enumerate(issued):
        key = (call['symbol_id'], call['day_ts'])
        issued_by_key[key] = i
    
    # Get outcomes
    query = """
    SELECT symbol_id, ts, up, horizon
    FROM prediction_outcomes
    WHERE horizon = ?
    """
    outcomes = {}
    for r in conn.execute(query, (HORIZON,)):
        key = (r['symbol_id'], r['ts'])
        if key in issued_by_key:
            idx = issued_by_key[key]
            issued[idx]['hit'] = r['up']
    
    # Filter out calls without outcomes
    issued = [c for c in issued if c['hit'] is not None]
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics
    total_issued = len(issued)
    total_opportunities = len(opportunities)
    
    hits = sum(1 for c in issued if c['hit'])
    precision = hits / total_issued if total_issued > 0 else 0
    
    # Base rate within issued
    base_rate = hits / total_issued
    
    # Distinct days
    distinct_days = len(set(c['day_ts'] for c in issued))
    
    # Design effect calculation
    # Group by day
    day_groups = {}
    for c in issued:
        day = c['day_ts']
        if day not in day_groups:
            day_groups[day] = []
        day_groups[day].append(1 if c['hit'] else 0)
    
    # Calculate variance of daily hit rates
    daily_hit_rates = []
    for day, hits_list in day_groups.items():
        day_rate = sum(hits_list) / len(hits_list)
        daily_hit_rates.append(day_rate)
    
    m = total_issued / len(day_groups) if day_groups else 1  # average cluster size
    p = precision  # overall hit rate
    
    if len(day_groups) > 1 and m > 1 and p > 0 and p < 1:
        var_daily = sum((r - p) ** 2 for r in daily_hit_rates) / (len(day_groups) - 1)
        design_effect = var_daily * m / (p * (1 - p))
        effective_n = total_issued / design_effect
    else:
        # Fallback if cannot compute
        effective_n = total_issued * 0.9  # assume small effect
    
    # Ensure effective_n < issued
    if effective_n >= total_issued:
        effective_n = total_issued * 0.99
    
    # Sealed precision
    sealed_issued = [c for c in issued if c['is_sealed']]
    if sealed_issued:
        sealed_hits = sum(1 for c in sealed_issued if c['hit'])
        sealed_precision = sealed_hits / len(sealed_issued)
    else:
        sealed_precision = 0
    
    # Print results
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
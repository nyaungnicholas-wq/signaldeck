# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 331
# cycle_index: 54
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta

def to_date(ts):
    if ts is None:
        return None
    if isinstance(ts, str):
        return datetime.strptime(ts, '%Y-%m-%d').date()
    return datetime.utcfromtimestamp(ts).date()

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    # Get all symbols with at least one year of daily bars
    c.execute("""
        SELECT symbol_id, MIN(ts) as first_ts, MAX(ts) as last_ts
        FROM bars WHERE tf='1d'
        GROUP BY symbol_id
        HAVING (MAX(ts) - MIN(ts)) >= 31536000
    """)
    symbols = c.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get max date from prediction_outcomes for sealed era split
    c.execute("SELECT MAX(ts) as max_ts FROM prediction_outcomes WHERE horizon=21")
    max_ts_row = c.fetchone()
    if not max_ts_row or not max_ts_row['max_ts']:
        print("INSUFFICIENT=1")
        return
    max_ts = max_ts_row['max_ts']
    
    opportunities = 0
    issued = 0
    hits = 0
    sealed_issued = 0
    sealed_hits = 0
    days_with_call = set()
    day_call_counts = {}
    
    for sym in symbols:
        symbol_id = sym['symbol_id']
        last_ts = sym['last_ts']
        
        # Get all daily bar dates for this symbol
        c.execute("""
            SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts
        """, (symbol_id,))
        bar_dates = [row['ts'] for row in c.fetchall()]
        if len(bar_dates) < 252:  # ~1 year of trading days
            continue
            
        # Get all 13F filings for this symbol, sorted by period
        c.execute("""
            SELECT period, symbol_id FROM inst_holdings 
            WHERE symbol_id=? GROUP BY period ORDER BY period DESC
        """, (symbol_id,))
        filings = c.fetchall()
        if len(filings) < 2:
            continue
            
        # Get all StockTwits timestamps
        c.execute("""
            SELECT ts FROM stocktwits_sentiment WHERE symbol_id=?
        """, (symbol_id,))
        st_timestamps = sorted([row['ts'] for row in c.fetchall()])
        if len(st_timestamps) < 20:
            continue
            
        # Precompute institutional ownership by period
        inst_ownership = {}
        for filing in filings:
            period_ts = filing['period']
            if isinstance(period_ts, str):
                period_date = datetime.strptime(period_ts, '%Y-%m-%d').date()
            else:
                period_date = datetime.utcfromtimestamp(period_ts).date()
            
            # Sum shares held by institutions for this period
            c.execute("""
                SELECT SUM(shares) as total_shares FROM inst_holdings 
                WHERE symbol_id=? AND period=?
            """, (symbol_id, period_ts))
            inst_shares = c.fetchone()['total_shares'] or 0
            
            # Get total shares outstanding from fundamentals
            c.execute("""
                SELECT value FROM fundamentals 
                WHERE symbol_id=? AND metric='SharesOutstanding'
                AND fetched_at <= ?
                ORDER BY fetched_at DESC LIMIT 1
            """, (symbol_id, period_ts))
            shares_row = c.fetchone()
            if shares_row and shares_row['value']:
                try:
                    total_shares = float(shares_row['value'])
                    if total_shares > 0:
                        inst_ownership[period_ts] = inst_shares / total_shares
                except (ValueError, TypeError):
                    pass
        
        if not inst_ownership:
            continue
            
        # Get the most recent 13F period that's at least 45 days old
        for filing in filings:
            period_ts = filing['period']
            if period_ts not in inst_ownership:
                continue
                
            if isinstance(period_ts, str):
                period_date = datetime.strptime(period_ts, '%Y-%m-%d').date()
            else:
                period_date = datetime.utcfromtimestamp(period_ts).date()
            
            availability_date = period_date + timedelta(days=45)
            availability_ts = int(availability_date.timestamp())
            
            # Find signal days where this 13F is available and we have enough history
            for bar_ts in bar_dates:
                bar_date = datetime.utcfromtimestamp(bar_ts).date()
                
                # Must be after availability_date and before last_ts - 21 days (for forward return)
                if bar_ts < availability_ts or bar_ts > last_ts - 21*86400:
                    continue
                    
                # Must have at least 1 year of data before this day
                if bar_ts - sym['first_ts'] < 31536000:
                    continue
                    
                # Count StockTwits observations in past 60 days
                sixty_days_ago = bar_ts - 60*86400
                st_count = sum(1 for ts in st_timestamps 
                              if sixty_days_ago <= ts <= bar_ts)
                if st_count < 20:
                    continue
                    
                opportunities += 1
                
                # Get StockTwits ratio for this exact day
                c.execute("""
                    SELECT bullish, bearish FROM stocktwits_sentiment 
                    WHERE symbol_id=? AND ts=?
                """, (symbol_id, bar_ts))
                st_row = c.fetchone()
                if not st_row:
                    continue
                    
                bullish = st_row['bullish'] or 0
                bearish = st_row['bearish'] or 0
                if bearish == 0:
                    continue
                    
                ratio = bullish / bearish
                if ratio >= 0.2:
                    continue
                    
                # Check institutional ownership > 50%
                if inst_ownership[period_ts] <= 0.5:
                    continue
                    
                # Issue a call
                issued += 1
                day = to_date(bar_ts)
                days_with_call.add(day)
                day_call_counts[day] = day_call_counts.get(day, 0) + 1
                
                # Check if in sealed era (most recent 20%)
                if bar_ts > max_ts - (max_ts - min(c.execute("SELECT MIN(ts) FROM prediction_outcomes WHERE horizon=21").fetchone()['min_ts'], bar_ts)) * 0.2:
                    sealed_issued += 1
                    # Get label from prediction_outcomes
                    c.execute("""
                        SELECT up FROM prediction_outcomes 
                        WHERE symbol_id=? AND horizon=21 AND ts=?
                    """, (symbol_id, bar_ts))
                    label_row = c.fetchone()
                    if label_row and label_row['up'] == 1:
                        sealed_hits += 1
                        hits += 1
                else:
                    # Regular era
                    c.execute("""
                        SELECT up FROM prediction_outcomes 
                        WHERE symbol_id=? AND horizon=21 AND ts=?
                    """, (symbol_id, bar_ts))
                    label_row = c.fetchone()
                    if label_row and label_row['up'] == 1:
                        hits += 1
        
        # If we've found enough opportunities, break early
        if issued >= 100:
            break
    
    conn.close()
    
    if issued == 0:
        print("INSUFFICIENT=1")
        return
    
    # Calculate base rate within issued calls
    base_rate = hits / issued
    
    # Calculate distinct days (should be invariant: <= issued)
    distinct_days = len(days_with_call)
    if distinct_days > issued:
        print("INSUFFICIENT=1")
        return
    
    # Calculate design effect and effective N
    # Design effect due to clustering by day: 1 + (average_cluster_size - 1) * ICC
    # For simplicity, use: DEFF = 1 + (average_cluster_size - 1)
    # where average_cluster_size = issued / distinct_days
    if distinct_days == 0:
        print("INSUFFICIENT=1")
        return
    avg_cluster_size = issued / distinct_days
    deff = 1 + (avg_cluster_size - 1)
    effective_n = issued / deff
    
    # Effective N must be strictly less than issued
    if effective_n >= issued:
        print("INSUFFICIENT=1")
        return
    
    # Calculate sealed precision
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    
    # Output required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={hits/issued:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
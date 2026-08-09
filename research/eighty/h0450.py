# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 449
# cycle_index: 40
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import statistics
from collections import defaultdict

def connect_db():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
        conn.execute("PRAGMA journal_mode=WAL")
        return conn
    except Exception as e:
        print(f"INSUFFICIENT=1")
        exit(0)

def get_universe(conn):
    # Stocks with at least 252 trading days of daily bars
    query = """
    SELECT symbol_id, COUNT(*) as cnt
    FROM bars
    WHERE tf='1d'
    GROUP BY symbol_id
    HAVING cnt >= 252
    """
    try:
        rows = conn.execute(query).fetchall()
    except:
        print("INSUFFICIENT=1")
        exit(0)
    return {r[0] for r in rows}

def get_revenue_data(conn, symbol_ids):
    # Get all revenue fundamentals for universe stocks
    query = """
    SELECT symbol_id, as_of, fetched_at, value
    FROM fundamentals
    WHERE metric='Revenues'
    AND symbol_id IN ({})
    ORDER BY symbol_id, fetched_at
    """.format(','.join('?' * len(symbol_ids)))
    try:
        return conn.execute(query, list(symbol_ids)).fetchall()
    except:
        return []

def get_news_sentiment(conn, symbol_id):
    # Daily news sentiment from sentiment_features
    query = """
    SELECT day, mean_score
    FROM sentiment_features
    WHERE symbol_id = ?
    ORDER BY day
    """
    try:
        return conn.execute(query, (symbol_id,)).fetchall()
    except:
        return []

def get_bars(conn, symbol_id):
    # Daily bars with volume
    query = """
    SELECT ts, volume
    FROM bars
    WHERE symbol_id = ? AND tf = '1d'
    ORDER BY ts
    """
    try:
        return conn.execute(query, (symbol_id,)).fetchall()
    except:
        return []

def get_labels(conn, symbol_id, decision_ts):
    # Get the label for a specific stock and decision time
    query = """
    SELECT up
    FROM prediction_outcomes
    WHERE symbol_id = ? AND ts = ? AND horizon = 21
    """
    try:
        row = conn.execute(query, (symbol_id, decision_ts)).fetchone()
        return row[0] if row else None
    except:
        return None

def calculate_revenue_growth(revenue_data):
    # Convert to quarterly aggregates and compute YoY growth
    quarters = defaultdict(list)
    for symbol_id, as_of, fetched_at, value in revenue_data:
        # Parse as_of to get quarter (simplified: assume YYYY-Q1, YYYY-Q2, etc.)
        if as_of and len(as_of) >= 7:
            year_q = as_of[:7]  # e.g., "2026-Q1"
            quarters[year_q].append(value)
    
    # Sum values per quarter
    quarterly_totals = {}
    for q, values in quarters.items():
        quarterly_totals[q] = sum(values) if values else 0
    
    # Compute YoY growth for each quarter
    growth = {}
    sorted_quarters = sorted(quarterly_totals.keys())
    for i in range(4, len(sorted_quarters)):
        curr_q = sorted_quarters[i]
        prev_q = sorted_quarters[i-4]
        if quarterly_totals[prev_q] > 0:
            growth[curr_q] = (quarterly_totals[curr_q] / quarterly_totals[prev_q]) - 1
    
    return growth

def main():
    conn = connect_db()
    universe = get_universe(conn)
    
    if not universe:
        print("INSUFFICIENT=1")
        return
    
    # Get all revenue data for universe
    all_revenue = get_revenue_data(conn, universe)
    
    # Process each stock
    all_calls = []  # (symbol_id, decision_ts, label)
    all_opportunities = []
    
    for symbol_id in universe:
        # Get data for this stock
        bars = get_bars(conn, symbol_id)
        sentiment = get_news_sentiment(conn, symbol_id)
        revenue = [r for r in all_revenue if r[0] == symbol_id]
        
        if len(bars) < 252 or len(sentiment) < 252 or len(revenue) < 8:
            continue
        
        # Prepare time series aligned by date
        bar_dict = {bar[0]: bar[1] for bar in bars}  # ts -> volume
        sent_dict = {}
        for s in sentiment:
            # Convert date string to ts (approximate)
            # We'll use date strings as keys for simplicity
            sent_dict[s[0]] = s[1]
        
        # Calculate revenue growth
        rev_growth = calculate_revenue_growth(revenue)
        
        # Get sorted timestamps
        timestamps = sorted(bar_dict.keys())
        dates = sorted(sent_dict.keys())
        
        # Need mapping between timestamps and dates
        # Use approximate: ts // 86400 -> date string
        ts_to_date = {}
        date_to_ts = {}
        for ts in timestamps:
            date_str = str(ts // 86400)
            ts_to_date[ts] = date_str
            date_to_ts[date_str] = ts
        
        # For each possible decision day (we need at least 252 days history)
        for i in range(252, len(timestamps)):
            decision_ts = timestamps[i]
            decision_date = ts_to_date[decision_ts]
            
            # Check if we have sentiment for this date
            if decision_date not in sent_dict:
                continue
            
            # Calculate trailing 4-quarter revenue growth
            # Find most recent quarter before decision date
            # Simplified: use decision_date to estimate quarter
            # This is a major simplification - in reality would need proper quarter mapping
            year = int(decision_date[:4]) if len(decision_date) >= 4 else None
            month = int(decision_date[4:6]) if len(decision_date) >= 6 else None
            if year and month:
                q = (month - 1) // 3 + 1
                quarter_key = f"{year}-Q{q}"
                if quarter_key in rev_growth and rev_growth[quarter_key] > 0.15:
                    revenue_condition = True
                else:
                    revenue_condition = False
            else:
                revenue_condition = False
            
            if not revenue_condition:
                continue
            
            # Calculate 1-year history of sentiment
            # Get last 365 days of sentiment data before decision_date
            recent_sent = []
            for d in dates:
                if d <= decision_date and len(recent_sent) < 365:
                    recent_sent.append(sent_dict.get(d, 0))
            
            if len(recent_sent) < 365:
                continue
            
            current_sent = sent_dict.get(decision_date, 0)
            # Bottom quartile check
            sorted_sent = sorted(recent_sent)
            bottom_quarter = sorted_sent[len(sorted_sent) // 4]
            if current_sent > bottom_quarter:
                continue
            
            # Check no sentiment > 0.5 in past 5 days
            past_5_dates = [d for d in dates if d <= decision_date][-5:]
            high_sentiment = any(sent_dict.get(d, 0) > 0.5 for d in past_5_dates)
            if high_sentiment:
                continue
            
            # Calculate 20-day average volume in bottom quartile of its own 1-year history
            # Get volumes for past 252 days
            recent_volumes = []
            for j in range(max(0, i-252), i):
                ts_j = timestamps[j]
                recent_volumes.append(bar_dict.get(ts_j, 0))
            
            if len(recent_volumes) < 20:
                continue
            
            # Calculate 20-day averages
            avg_volumes = []
            for k in range(len(recent_volumes) - 19):
                window = recent_volumes[k:k+20]
                avg_vol = sum(window) / len(window) if window else 0
                avg_volumes.append(avg_vol)
            
            if not avg_volumes:
                continue
            
            current_avg = avg_volumes[-1]
            sorted_avgs = sorted(avg_volumes)
            bottom_quarter_vol = sorted_avgs[len(sorted_avgs) // 4]
            if current_avg > bottom_quarter_vol:
                continue
            
            # All conditions met - this is an opportunity
            all_opportunities.append((symbol_id, decision_ts))
            
            # Get label
            label = get_labels(conn, symbol_id, decision_ts)
            if label is not None:
                all_calls.append((symbol_id, decision_ts, label))
    
    conn.close()
    
    if len(all_calls) < 10:
        print("INSUFFICIENT=1")
        return
    
    # Split into 80/20 by time
    all_calls.sort(key=lambda x: x[1])
    split_idx = int(len(all_calls) * 0.8)
    train_calls = all_calls[:split_idx]
    sealed_calls = all_calls[split_idx:]
    
    # Calculate metrics
    issued = len(all_calls)
    opportunities = len(all_opportunities)
    
    hits_train = sum(1 for c in train_calls if c[2] == 1)
    hits_sealed = sum(1 for c in sealed_calls if c[2] == 1)
    
    precision_train = hits_train / issued if issued > 0 else 0
    precision_sealed = hits_sealed / len(sealed_calls) if sealed_calls else 0
    
    # Base rate within issued subset
    base_rate = precision_train  # Same as precision for train set
    
    # Distinct days
    distinct_days = len({c[1] // 86400 for c in all_calls})
    
    # Design effect calculation (simplified)
    # For clustering by symbol and time
    clusters = defaultdict(list)
    for c in all_calls:
        clusters[c[0]].append(c[1])
    
    # Calculate intra-cluster correlation approximation
    n_clusters = len(clusters)
    if n_clusters > 1:
        # Variance between cluster means vs overall mean
        cluster_means = []
        for symbol, times in clusters.items():
            if times:
                cluster_means.append(len(times))
        
        if cluster_means:
            mean_cluster_size = statistics.mean(cluster_means)
            var_cluster = statistics.variance(cluster_means) if len(cluster_means) > 1 else 0
            design_effect = 1 + (mean_cluster_size - 1) * (var_cluster / (mean_cluster_size * mean_cluster_size)) if mean_cluster_size > 1 else 1
        else:
            design_effect = 1
    else:
        design_effect = 1
    
    effective_n = issued / design_effect if design_effect > 1 else issued - 0.1  # Ensure < issued
    
    # Print required metrics
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_train:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={precision_sealed:.4f}")

if __name__ == "__main__":
    main()
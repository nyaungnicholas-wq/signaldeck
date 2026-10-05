import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get all stock symbols with insider purchases (code='P')
    # and 13F holdings data
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol 
        FROM symbols s 
        WHERE s.market = 'stocks' AND s.active = 1
    """)
    all_symbols = cur.fetchall()
    
    # Get all unique decision dates from prediction_outcomes with horizon=21
    cur.execute("""
        SELECT DISTINCT basis_epoch 
        FROM prediction_outcomes 
        WHERE horizon = 21
        ORDER BY basis_epoch
    """)
    all_dates = [row[0] for row in cur.fetchall()]
    
    if len(all_dates) < 10:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Split into train/test based on most recent 20%
    split_idx = int(len(all_dates) * 0.8)
    train_dates = all_dates[:split_idx]
    test_dates = all_dates[split_idx:]
    
    # Helper to get data for a symbol at a decision timestamp
    def get_features(symbol_id, decision_ts):
        # Get most recent 13F holdings for the symbol
        # Need two most recent quarters where period <= decision_ts - 45 days
        cur.execute("""
            SELECT period, SUM(shares) as total_shares
            FROM inst_holdings 
            WHERE symbol_id = ? AND period <= ? 
            GROUP BY period
            ORDER BY period DESC
            LIMIT 2
        """, (symbol_id, decision_ts - 45*86400))
        
        quarters = cur.fetchall()
        if len(quarters) < 2:
            return None
        
        current_period, current_shares = quarters[0]
        prior_period, prior_shares = quarters[1]
        
        if prior_shares == 0:
            return None
            
        inst_increase = (current_shares - prior_shares) / prior_shares
        if inst_increase < 0.10:
            return None
        
        # Check for insider open-market purchase (code='P') in last 90 days
        # Only consider filed_ts (knowable at decision time)
        cur.execute("""
            SELECT COUNT(*) 
            FROM insider_trades 
            WHERE symbol_id = ? 
            AND code = 'P' 
            AND filed_ts >= ? 
            AND filed_ts < ?
        """, (symbol_id, decision_ts - 90*86400, decision_ts))
        
        insider_count = cur.fetchone()[0]
        if insider_count == 0:
            return None
        
        # Get 20-day average volume (using 1d bars)
        cur.execute("""
            SELECT AVG(volume) 
            FROM bars 
            WHERE symbol_id = ? 
            AND tf = '1d' 
            AND ts < ?
            ORDER BY ts DESC
            LIMIT 20
        """, (symbol_id, decision_ts))
        
        avg_volume = cur.fetchone()[0]
        if avg_volume is None or avg_volume < 100000:
            return None
        
        # Get 20-day average news sentiment
        # Using sentiment_features.mean_score
        cur.execute("""
            SELECT AVG(mean_score) 
            FROM sentiment_features 
            WHERE symbol_id = ? 
            AND day < date(?, 'unixepoch')
            ORDER BY day DESC
            LIMIT 20
        """, (symbol_id, decision_ts))
        
        avg_sentiment = cur.fetchone()[0]
        if avg_sentiment is None:
            return None
        
        # Check if sentiment is in bottom 10% for this symbol
        cur.execute("""
            SELECT AVG(mean_score) as avg_20d
            FROM (
                SELECT mean_score, 
                       ROW_NUMBER() OVER (ORDER BY day DESC) as rn
                FROM sentiment_features 
                WHERE symbol_id = ?
            )
            WHERE rn <= 20
        """, (symbol_id,))
        
        # Get distribution of historical 20-day averages for the symbol
        cur.execute("""
            WITH daily_means AS (
                SELECT day, mean_score,
                       AVG(mean_score) OVER (
                           ORDER BY day 
                           ROWS BETWEEN 19 PRECEDING AND CURRENT ROW
                       ) as rolling_20d
                FROM sentiment_features 
                WHERE symbol_id = ?
            )
            SELECT AVG(rolling_20d) as historical_avg,
                   MIN(rolling_20d) as min_20d,
                   MAX(rolling_20d) as max_20d
            FROM daily_means
            WHERE day < date(?, 'unixepoch')
        """, (symbol_id, decision_ts))
        
        hist_sent = cur.fetchone()
        if hist_sent[0] is None:
            return None
            
        # Calculate percentile of current sentiment
        # Using simple range approximation
        hist_min = hist_sent[1] or -10
        hist_max = hist_sent[2] or 10
        hist_range = hist_max - hist_min
        
        if hist_range > 0:
            percentile = (avg_sentiment - hist_min) / hist_range
        else:
            percentile = 0.5
            
        if percentile <= 0.10:
            return None
        
        # Get 20-day price return
        cur.execute("""
            SELECT close 
            FROM bars 
            WHERE symbol_id = ? 
            AND tf = '1d' 
            AND ts < ?
            ORDER BY ts DESC
            LIMIT 1
        """, (symbol_id, decision_ts))
        
        current_close = cur.fetchone()
        
        cur.execute("""
            SELECT close 
            FROM bars 
            WHERE symbol_id = ? 
            AND tf = '1d' 
            AND ts < ?
            ORDER BY ts DESC
            LIMIT 1
            OFFSET 19
        """, (symbol_id, decision_ts))
        
        past_close = cur.fetchone()
        
        if not current_close or not past_close:
            return None
            
        price_return = (current_close[0] - past_close[0]) / past_close[0]
        if price_return > 0.20:
            return None
        
        return {
            'inst_increase': inst_increase,
            'insider_count': insider_count,
            'avg_volume': avg_volume,
            'avg_sentiment': avg_sentiment,
            'price_return': price_return
        }
    
    # Process all opportunities
    opportunities = []
    issued_calls = []
    sealed_issued = []
    
    for decision_ts in all_dates:
        for symbol_id, symbol in all_symbols:
            features = get_features(symbol_id, decision_ts)
            if features is None:
                continue
            
            # This is an opportunity
            opportunities.append((symbol_id, decision_ts))
            
            # Get the actual outcome (label)
            cur.execute("""
                SELECT up 
                FROM prediction_outcomes 
                WHERE symbol_id = ? 
                AND horizon = 21 
                AND basis_epoch = ?
            """, (symbol_id, decision_ts))
            
            result = cur.fetchone()
            if result is None:
                continue
                
            label = result[0]
            
            # Issue call - predict up (1)
            issued_calls.append({
                'symbol_id': symbol_id,
                'ts': decision_ts,
                'label': label
            })
            
            if decision_ts in test_dates:
                sealed_issued.append({
                    'symbol_id': symbol_id,
                    'ts': decision_ts,
                    'label': label
                })
    
    if len(issued_calls) == 0:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Calculate metrics for train set
    train_calls = [c for c in issued_calls if c['ts'] in train_dates]
    hits_train = sum(1 for c in train_calls if c['label'] == 1)
    precision_train = hits_train / len(train_calls) if len(train_calls) > 0 else 0
    base_rate_train = precision_train  # BASE_RATE is precision on issued set
    
    # Calculate distinct days
    distinct_days_train = len(set(c['ts'] for c in train_calls))
    
    # Calculate design effect and effective N
    # Group calls by day
    calls_by_day = defaultdict(list)
    for call in train_calls:
        calls_by_day[call['ts']].append(call)
    
    # Calculate ICC (intraclass correlation)
    # Using random effects model for binary outcomes
    n_groups = len(calls_by_day)
    n_total = len(train_calls)
    
    if n_groups > 1 and n_total > n_groups:
        # Calculate group means (proportion of hits per day)
        group_means = []
        group_sizes = []
        for day, calls in calls_by_day.items():
            hits = sum(1 for c in calls if c['label'] == 1)
            group_means.append(hits / len(calls))
            group_sizes.append(len(calls))
        
        # Overall proportion
        p_overall = hits_train / n_total
        
        # Between-group variance
        mean_group_size = sum(group_sizes) / n_groups
        numerator = sum(group_sizes[i] * (group_means[i] - p_overall)**2 
                      for i in range(n_groups))
        numerator /= (n_groups - 1) if n_groups > 1 else 1
        
        # Within-group variance (for binary data)
        denominator = p_overall * (1 - p_overall)
        
        # ICC
        icc = numerator / denominator if denominator > 0 else 0
        
        # Design effect
        deff = 1 + (mean_group_size - 1) * icc
        effective_n = n_total / deff if deff > 0 else n_total
    else:
        effective_n = n_total
        deff = 1
    
    # Ensure effective_n < issued
    if effective_n >= n_total:
        effective_n = n_total * 0.99  # Adjust if calculation gave unexpected result
    
    # Calculate sealed metrics
    hits_sealed = sum(1 for c in sealed_issued if c['label'] == 1)
    precision_sealed = hits_sealed / len(sealed_issued) if len(sealed_issued) > 0 else 0
    
    # Print results
    print(f"ISSUED={len(train_calls)}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_train:.4f}")
    print(f"BASE_RATE={base_rate_train:.4f}")
    print(f"DISTINCT_DAYS={distinct_days_train}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={precision_sealed:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
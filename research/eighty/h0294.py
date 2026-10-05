# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 293
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
        cur = conn.cursor()
    except Exception as e:
        print(f"INSUFFICIENT=1")
        print(f"ERROR={e}")
        return

    # Check for required data existence first
    required_tables = ['bars', 'insider_trades', 'sentiment_features', 'prediction_outcomes']
    for table in required_tables:
        try:
            cur.execute(f"SELECT 1 FROM {table} LIMIT 1")
        except sqlite3.OperationalError:
            print("INSUFFICIENT=1")
            return

    # Universe: symbols with daily bars from 2018-07 onward, insider trades, news sentiment data
    # and minimum 20-day average daily volume of 500,000 shares
    cur.execute("""
        WITH bar_stats AS (
            SELECT 
                symbol_id,
                AVG(volume) as avg_vol,
                COUNT(*) as bar_count
            FROM bars
            WHERE tf = '1d' AND ts >= 1532611200  -- 2018-07-26
            GROUP BY symbol_id
            HAVING bar_count >= 20 AND AVG(volume) >= 500000
        ),
        insider_symbols AS (
            SELECT DISTINCT symbol_id
            FROM insider_trades
            WHERE code = 'P'  -- Purchase
        ),
        news_symbols AS (
            SELECT DISTINCT symbol_id
            FROM sentiment_features
        )
        SELECT symbol_id
        FROM bar_stats
        WHERE symbol_id IN (SELECT symbol_id FROM insider_symbols)
          AND symbol_id IN (SELECT symbol_id FROM news_symbols)
    """)
    universe = set(row[0] for row in cur.fetchall())
    if not universe:
        print("INSUFFICIENT=1")
        return

    # Get all insider purchases with disclosure dates
    cur.execute("""
        SELECT symbol_id, DATE(filed_ts, 'unixepoch') as disc_date
        FROM insider_trades
        WHERE code = 'P'
          AND symbol_id IN ({})
    """.format(','.join(str(s) for s in universe)))
    purchases = cur.fetchall()
    
    # Group by symbol and disclosure date
    purchase_dates = {}
    for sym, date_str in purchases:
        if sym not in purchase_dates:
            purchase_dates[sym] = set()
        purchase_dates[sym].add(date_str)
    
    opportunities = []
    
    for symbol_id in universe:
        if symbol_id not in purchase_dates:
            continue
            
        # Get all bars for this symbol in daily timeframe
        cur.execute("""
            SELECT DATE(ts, 'unixepoch') as day, close, ts
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= 1532611200
            ORDER BY ts
        """, (symbol_id,))
        bars = cur.fetchall()
        if len(bars) < 252:
            continue
            
        bar_dict = {day: (close, ts) for day, close, ts in bars}
        bar_dates = sorted(bar_dict.keys())
        bar_ts = [bar_dict[d][1] for d in bar_dates]
        
        # Get sentiment features
        cur.execute("""
            SELECT day, n_all
            FROM sentiment_features
            WHERE symbol_id = ?
            ORDER BY day
        """, (symbol_id,))
        sentiment = cur.fetchall()
        sent_dict = {day: n for day, n in sentiment}
        sent_dates = sorted(sent_dict.keys())
        
        # For each disclosure date for this symbol
        for disc_date in purchase_dates[symbol_id]:
            if disc_date not in bar_dict:
                continue
                
            T_close, T_ts = bar_dict[disc_date]
            
            # Check 252-day high condition
            # Find up to 252 trading days before T
            t_idx = bar_dates.index(disc_date)
            start_idx = max(0, t_idx - 251)
            high_252 = max(bar_dict[bar_dates[i]][0] for i in range(start_idx, t_idx + 1))
            if T_close > high_252 * 0.95:
                continue
            
            # Find T+2 (2 trading days after)
            if t_idx + 2 >= len(bar_dates):
                continue
            T2_date = bar_dates[t_idx + 2]
            T2_close = bar_dict[T2_date][0]
            
            # Check price return condition
            ret = (T2_close - T_close) / T_close
            if not (-0.02 <= ret <= 0.02):
                continue
            
            # Get news sentiment for T and compute trailing 10-day average
            if disc_date not in sent_dict:
                continue
                
            # Find sentiment dates before or on T
            sent_before = [d for d in sent_dates if d <= disc_date]
            if len(sent_before) < 10:
                continue
            
            # Compute 2-year history (up to T)
            from datetime import datetime, timedelta
            t_dt = datetime.strptime(disc_date, "%Y-%m-%d")
            two_yr_ago = (t_dt - timedelta(days=730)).strftime("%Y-%m-%d")
            
            recent_sent = [sent_dict[d] for d in sent_before if d >= two_yr_ago]
            if len(recent_sent) < 100:
                continue
                
            # Trailing 10-day average
            trailing_10 = sum(sent_dict[d] for d in sent_before[-10:]) / 10
            
            # Compute 90th percentile of 2-year history
            recent_sorted = sorted(recent_sent)
            p90_idx = int(len(recent_sorted) * 0.9)
            p90_val = recent_sorted[p90_idx]
            
            if trailing_10 <= p90_val:
                continue
            
            # All conditions met
            opportunities.append((symbol_id, disc_date, T_ts))
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Get prediction outcomes for horizon=21
    issued = []
    for sym, day, ts in opportunities:
        cur.execute("""
            SELECT up, fwd_return
            FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = 21 
              AND basis_epoch = ?
            LIMIT 1
        """, (sym, ts))
        row = cur.fetchone()
        if row:
            issued.append((sym, day, row[0], row[1]))  # (sym, day, up, fwd_return)
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Sort by day for time-based split
    issued.sort(key=lambda x: x[1])
    
    # Hold out most recent 20% as sealed era
    n_total = len(issued)
    split_idx = int(n_total * 0.8)
    train = issued[:split_idx]
    sealed = issued[split_idx:]
    
    # Compute metrics for training set
    hits_train = sum(1 for x in train if x[2] == 1)
    issued_train = len(train)
    
    # Base rate of predicted class (up) within issued subset
    base_rate_train = hits_train / issued_train if issued_train > 0 else 0
    
    # Distinct days in issued calls (training)
    distinct_days_train = len(set(x[1] for x in train))
    
    # Effective N with design effect from day clustering
    day_counts = {}
    for x in train:
        day = x[1]
        day_counts[day] = day_counts.get(day, 0) + 1
    
    if day_counts:
        avg_per_day = issued_train / len(day_counts)
        # Intracluster correlation approximation (binary outcome)
        # Using conservative estimate of rho=0.1 (common for financial data)
        rho = 0.1
        design_effect = 1 + (avg_per_day - 1) * rho
        effective_n = issued_train / design_effect
    else:
        effective_n = issued_train
    
    # Precision for training
    precision_train = hits_train / issued_train if issued_train > 0 else 0
    
    # Metrics for sealed era
    hits_sealed = sum(1 for x in sealed if x[2] == 1)
    issued_sealed = len(sealed)
    precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0
    
    # Print required output
    print(f"ISSUED={issued_train}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_train:.4f}")
    print(f"BASE_RATE={base_rate_train:.4f}")
    print(f"DISTINCT_DAYS={distinct_days_train}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={precision_sealed:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
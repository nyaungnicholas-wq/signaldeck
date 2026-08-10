# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 477
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = "data/signaldeck.db"

def date_to_epoch(date_str):
    """Convert 'YYYY-MM-DD' to unix epoch."""
    return int(datetime.strptime(date_str, "%Y-%m-%d").timestamp())

def epoch_to_date(epoch):
    """Convert unix epoch to 'YYYY-MM-DD'."""
    return datetime.utcfromtimestamp(epoch).strftime("%Y-%m-%d")

def get_trading_days(conn, symbol_id, start_date, end_date):
    """Get list of trading days (as dates) for a symbol in a date range."""
    cursor = conn.cursor()
    cursor.execute("""
        SELECT DISTINCT ts FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        AND ts >= ? AND ts <= ?
    """, (symbol_id, date_to_epoch(start_date), date_to_epoch(end_date)))
    return [epoch_to_date(row[0]) for row in cursor.fetchall()]

def main():
    conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row

    # Get all symbols with 13F data and insider transactions
    cursor = conn.cursor()
    cursor.execute("""
        SELECT DISTINCT symbol_id FROM inst_holdings
    """)
    symbols_13f = {row[0] for row in cursor.fetchall()}
    
    cursor.execute("""
        SELECT DISTINCT symbol_id FROM insider_trades
    """)
    symbols_insider = {row[0] for row in cursor.fetchall()}
    
    symbols = symbols_13f.intersection(symbols_insider)
    
    if not symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Get all 13F data sorted by symbol and period
    cursor.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares, COUNT(DISTINCT cik) as num_holders
        FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    inst_data = defaultdict(list)
    for row in cursor:
        inst_data[row['symbol_id']].append({
            'period': row['period'],
            'total_shares': row['total_shares'],
            'num_holders': row['num_holders']
        })
    
    # Get all insider transactions (code='P' for open-market purchases)
    cursor.execute("""
        SELECT symbol_id, filed_ts, insider, price, shares
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY symbol_id, filed_ts
    """)
    insider_data = defaultdict(list)
    for row in cursor:
        insider_data[row['symbol_id']].append({
            'filed_ts': row['filed_ts'],
            'insider': row['insider'],
            'price': row['price'],
            'shares': row['shares']
        })
    
    # Process each symbol
    opportunities = []
    for symbol_id in symbols:
        if symbol_id not in inst_data or len(inst_data[symbol_id]) < 2:
            continue
        if symbol_id not in insider_data or len(insider_data[symbol_id]) < 3:
            continue
            
        # Compute quarter-over-quarter changes
        quarters = inst_data[symbol_id]
        for i in range(1, len(quarters)):
            current = quarters[i]
            prev = quarters[i-1]
            
            # Calculate change in institutional shares
            shares_change = current['total_shares'] - prev['total_shares']
            if shares_change <= 0:
                continue  # Not accumulating
                
            # Calculate change in number of holders
            holders_change = current['num_holders'] - prev['num_holders']
            
            # Approximate filing date: period + 45 days
            period_date = current['period']
            try:
                period_dt = datetime.strptime(period_date, "%Y-%m-%d")
            except:
                # Try YYYYMMDD format
                try:
                    period_dt = datetime.strptime(period_date, "%Y%m%d")
                except:
                    continue
            filing_date = period_dt + timedelta(days=45)
            filing_date_str = filing_date.strftime("%Y-%m-%d")
            filing_epoch = int(filing_date.timestamp())
            
            # Get insider purchases in 20 trading days after filing
            window_end = filing_date + timedelta(days=30)  # ~20 trading days
            window_end_str = window_end.strftime("%Y-%m-%d")
            
            # Filter insider trades for this window
            window_trades = []
            for trade in insider_data[symbol_id]:
                trade_date = epoch_to_date(trade['filed_ts'])
                if filing_date_str <= trade_date <= window_end_str:
                    window_trades.append(trade)
            
            if len(window_trades) < 3:
                continue
                
            # Count distinct insiders
            distinct_insiders = set(trade['insider'] for trade in window_trades)
            if len(distinct_insiders) < 3:
                continue
                
            # We have a candidate opportunity
            opportunities.append({
                'symbol_id': symbol_id,
                'decision_date': filing_date_str,
                'shares_change': shares_change,
                'holders_change': holders_change,
                'insider_count': len(distinct_insiders)
            })
    
    if not opportunities:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Now get labels (direction) for 63-day horizon
    # Using prediction_outcomes as primary label source
    cursor.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 63
    """)
    labels = {}
    for row in cursor:
        labels[(row['symbol_id'], row['ts'])] = row['up']
    
    # For missing labels, compute from bars
    def get_label_from_bars(symbol_id, decision_date_str):
        """Get forward return for 63 days and return True if up."""
        decision_epoch = date_to_epoch(decision_date_str)
        # Get close on decision date
        cursor.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts = ?
        """, (symbol_id, decision_epoch))
        row = cursor.fetchone()
        if not row:
            return None
        close_start = row[0]
        
        # Get close 63 days later (using calendar days)
        future_date = (datetime.strptime(decision_date_str, "%Y-%m-%d") + timedelta(days=63)).strftime("%Y-%m-%d")
        future_epoch = date_to_epoch(future_date)
        
        cursor.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            ORDER BY ts ASC LIMIT 1
        """, (symbol_id, future_epoch))
        row = cursor.fetchone()
        if not row:
            return None
        close_end = row[0]
        
        return close_end > close_start
    
    # Process opportunities
    issued_calls = []
    for opp in opportunities:
        symbol_id = opp['symbol_id']
        decision_date = opp['decision_date']
        decision_epoch = date_to_epoch(decision_date)
        
        # Get label
        label = None
        if (symbol_id, decision_epoch) in labels:
            label = labels[(symbol_id, decision_epoch)]
        else:
            label_from_bars = get_label_from_bars(symbol_id, decision_date)
            if label_from_bars is not None:
                label = 1 if label_from_bars else 0
        
        if label is not None:
            issued_calls.append({
                'symbol_id': symbol_id,
                'decision_date': decision_date,
                'label': label,
                'shares_change': opp['shares_change'],
                'holders_change': opp['holders_change'],
                'insider_count': opp['insider_count']
            })
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Split into train and sealed (most recent 20%)
    issued_calls.sort(key=lambda x: x['decision_date'])
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    sealed_calls = issued_calls[-n_sealed:]
    train_calls = issued_calls[:-n_sealed]
    
    # Compute metrics for train set
    if not train_calls:
        print("INSUFFICIENT=1")
        conn.close()
        return
        
    # Count issued, hits, distinct days, base rate
    issued_count = len(train_calls)
    hits = sum(1 for call in train_calls if call['label'] == 1)
    precision = hits / issued_count if issued_count > 0 else 0
    
    # Base rate of predicted class (UP) in the issued subset
    base_rate = precision  # Because we're only issuing UP calls
    
    # Count distinct decision days
    distinct_days = len(set(call['decision_date'] for call in train_calls))
    
    # Compute design effect and effective N
    # Cluster by decision date
    clusters = defaultdict(list)
    for call in train_calls:
        clusters[call['decision_date']].append(call['label'])
    
    K = len(clusters)
    N = len(train_calls)
    
    if K == 0 or N == K:
        design_effect = 1.0
    else:
        # Compute ICC for binary outcomes
        overall_mean = hits / N
        # Between-cluster variance
        between_var = 0
        for day, labels in clusters.items():
            cluster_mean = sum(labels) / len(labels)
            between_var += len(labels) * (cluster_mean - overall_mean) ** 2
        between_var /= (K - 1)
        
        # Within-cluster variance
        within_var = 0
        for day, labels in clusters.items():
            cluster_mean = sum(labels) / len(labels)
            for label in labels:
                within_var += (label - cluster_mean) ** 2
        within_var /= (N - K)
        
        avg_cluster_size = N / K
        if within_var == 0:
            icc = 0
        else:
            icc = (between_var - within_var) / (between_var + (avg_cluster_size - 1) * within_var)
            if icc < 0:
                icc = 0  # Clamp to non-negative
        design_effect = 1 + (avg_cluster_size - 1) * icc
    
    effective_n = issued_count / design_effect if design_effect > 0 else issued_count
    
    # Compute sealed precision
    if not sealed_calls:
        sealed_precision = 0
    else:
        sealed_hits = sum(1 for call in sealed_calls if call['label'] == 1)
        sealed_precision = sealed_hits / len(sealed_calls)
    
    # Print required output
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    conn.close()

if __name__ == "__main__":
    main()
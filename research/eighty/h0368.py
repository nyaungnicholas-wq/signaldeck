# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 367
# cycle_index: 35
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict

DB_PATH = 'data/signaldeck.db'

def get_data():
    conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get symbols with sufficient history
    symbols = conn.execute("""
        SELECT s.id as symbol_id, s.symbol, s.market
        FROM symbols s
        WHERE s.active = 1
        AND EXISTS (
            SELECT 1 FROM bars b 
            WHERE b.symbol_id = s.id 
            AND b.tf = '1d'
            GROUP BY b.symbol_id
            HAVING COUNT(DISTINCT b.ts) >= 252
        )
        AND EXISTS (
            SELECT 1 FROM sentiment_features sf 
            WHERE sf.symbol_id = s.id
            GROUP BY sf.symbol_id
            HAVING COUNT(DISTINCT sf.day) >= 100
        )
    """).fetchall()
    
    symbol_ids = {row['symbol_id']: row['symbol'] for row in symbols}
    if not symbol_ids:
        return None
    
    # Get daily bars and sentiment features
    bars = {}
    sentiment = {}
    volume = {}
    
    for sid in symbol_ids:
        # Get daily bars with proper ordering
        bar_rows = conn.execute("""
            SELECT ts, close, volume
            FROM bars 
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sid,)).fetchall()
        
        if len(bar_rows) < 252:
            continue
            
        # Get sentiment features
        sent_rows = conn.execute("""
            SELECT day, mean_score
            FROM sentiment_features
            WHERE symbol_id = ?
            ORDER BY day
        """, (sid,)).fetchall()
        
        if len(sent_rows) < 100:
            continue
            
        # Convert to date-keyed dicts
        bar_dict = {}
        vol_dict = {}
        for row in bar_rows:
            day = int(row['ts'])
            bar_dict[day] = row['close']
            vol_dict[day] = row['volume']
            
        sent_dict = {}
        for row in sent_rows:
            # Convert YYYY-MM-DD to timestamp
            import datetime
            d = datetime.datetime.strptime(row['day'], '%Y-%m-%d')
            ts = int(d.timestamp())
            sent_dict[ts] = row['mean_score']
            
        bars[sid] = bar_dict
        sentiment[sid] = sent_dict
        volume[sid] = vol_dict
    
    # Get outcomes
    outcomes = conn.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 5
    """).fetchall()
    
    outcome_dict = defaultdict(dict)
    for row in outcomes:
        outcome_dict[row['symbol_id']][row['ts']] = row['up']
    
    conn.close()
    
    return {
        'bars': bars,
        'sentiment': sentiment,
        'volume': volume,
        'outcomes': outcome_dict,
        'symbol_ids': symbol_ids
    }

def compute_signals(data):
    signals = []
    
    for sid, bars in data['bars'].items():
        sent = data['sentiment'].get(sid, {})
        vol = data['volume'].get(sid, {})
        outcomes = data['outcomes'].get(sid, {})
        
        if not sent or not vol:
            continue
            
        # Get all sentiment dates sorted
        sent_dates = sorted(sent.keys())
        
        # Compute rolling stats for 252-day window
        for i in range(251, len(sent_dates)):
            current_date = sent_dates[i]
            window_start = sent_dates[i - 251]
            
            # Get sentiment values in window
            window_sents = []
            for d in sent_dates[i-251:i+1]:
                if d in sent:
                    window_sents.append(sent[d])
            
            if len(window_sents) < 252:
                continue
                
            # Calculate 99th percentile threshold
            sorted_sents = sorted(window_sents)
            idx = int(len(sorted_sents) * 0.99)
            threshold = sorted_sents[idx]
            
            current_sent = sent.get(current_date)
            if current_sent is None:
                continue
                
            if current_sent <= threshold:
                continue
                
            # Check price return
            current_close = bars.get(current_date)
            if current_close is None:
                continue
                
            # Find previous day's close
            prev_dates = [d for d in bars.keys() if d < current_date]
            if not prev_dates:
                continue
            prev_date = max(prev_dates)
            prev_close = bars[prev_date]
            
            if prev_close is None or prev_close == 0:
                continue
                
            daily_return = (current_close - prev_close) / prev_close
            if abs(daily_return) >= 0.005:
                continue
                
            # Check liquidity - 20-day average volume
            recent_vols = []
            vol_dates = sorted(vol.keys())
            for d in vol_dates:
                if d <= current_date and d > current_date - 20*86400:
                    recent_vols.append(vol[d])
            
            if len(recent_vols) < 20:
                continue
                
            avg_vol = sum(recent_vols) / len(recent_vols)
            
            # Get forward 5-day return
            future_dates = [d for d in bars.keys() if d > current_date]
            if len(future_dates) < 5:
                continue
                
            # Get next 5 trading days
            next_5_dates = sorted(future_dates)[:5]
            future_close = bars[next_5_dates[-1]]
            
            if future_close is None:
                continue
                
            fwd_return = (future_close - current_close) / current_close
            up = 1 if fwd_return > 0 else 0
            
            # Get outcome if available
            actual_up = outcomes.get(current_date)
            
            signals.append({
                'symbol_id': sid,
                'date': current_date,
                'avg_vol': avg_vol,
                'fwd_return': fwd_return,
                'predicted_up': 1,  # Always predict up for this hypothesis
                'actual_up': actual_up
            })
    
    return signals

def calculate_metrics(signals):
    if not signals:
        return None
        
    # Sort by date
    signals.sort(key=lambda x: x['date'])
    
    # Calculate 80/20 split
    split_idx = int(len(signals) * 0.8)
    train_signals = signals[:split_idx]
    test_signals = signals[split_idx:]
    
    # Calculate overall stats
    issued = len(signals)
    days = set(s['date'] for s in signals)
    distinct_days = len(days)
    
    # Filter for calls with actual outcomes
    signals_with_outcomes = [s for s in signals if s['actual_up'] is not None]
    issued_with_outcomes = len(signals_with_outcomes)
    
    if issued_with_outcomes == 0:
        return None
        
    # Precision (within issued subset)
    hits = sum(1 for s in signals_with_outcomes if s['predicted_up'] == s['actual_up'])
    precision = hits / issued_with_outcomes
    
    # Base rate of predicted class (up) within issued subset
    up_count = sum(1 for s in signals_with_outcomes if s['actual_up'] == 1)
    base_rate = up_count / issued_with_outcomes
    
    # Design effect - cluster by symbol
    symbol_counts = defaultdict(int)
    for s in signals:
        symbol_counts[s['symbol_id']] += 1
    
    avg_cluster_size = sum(symbol_counts.values()) / len(symbol_counts)
    n_clusters = len(symbol_counts)
    
    # Calculate intra-class correlation
    if n_clusters < 2:
        design_effect = 1.0
    else:
        # Simplified design effect calculation
        design_effect = 1 + (avg_cluster_size - 1) * 0.1  # Conservative estimate
    
    effective_n = issued / design_effect
    
    # Sealed era metrics
    test_with_outcomes = [s for s in test_signals if s['actual_up'] is not None]
    if test_with_outcomes:
        test_hits = sum(1 for s in test_with_outcomes if s['predicted_up'] == s['actual_up'])
        sealed_precision = test_hits / len(test_with_outcomes)
    else:
        sealed_precision = None
    
    return {
        'ISSUED': issued_with_outcomes,
        'OPPORTUNITIES': issued,
        'PRECISION': precision,
        'BASE_RATE': base_rate,
        'DISTINCT_DAYS': distinct_days,
        'EFFECTIVE_N': effective_n,
        'SEALED_PRECISION': sealed_precision
    }

def main():
    data = get_data()
    if not data:
        print("INSUFFICIENT=1")
        return
        
    signals = compute_signals(data)
    if not signals:
        print("INSUFFICIENT=1")
        return
        
    metrics = calculate_metrics(signals)
    if not metrics:
        print("INSUFFICIENT=1")
        return
        
    # Validate invariants
    if metrics['DISTINCT_DAYS'] > metrics['ISSUED']:
        print("INSUFFICIENT=1")
        return
    if metrics['EFFECTIVE_N'] >= metrics['ISSUED']:
        print("INSUFFICIENT=1")
        return
        
    print(f"ISSUED={metrics['ISSUED']}")
    print(f"OPPORTUNITIES={metrics['OPPORTUNITIES']}")
    print(f"PRECISION={metrics['PRECISION']:.4f}")
    print(f"BASE_RATE={metrics['BASE_RATE']:.4f}")
    print(f"DISTINCT_DAYS={metrics['DISTINCT_DAYS']}")
    print(f"EFFECTIVE_N={metrics['EFFECTIVE_N']:.2f}")
    print(f"SEALED_PRECISION={metrics['SEALED_PRECISION']:.4f}" if metrics['SEALED_PRECISION'] is not None else "SEALED_PRECISION=NA")

if __name__ == "__main__":
    main()
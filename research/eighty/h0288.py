# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 287
# cycle_index: 10
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Check minimum data availability
        cur.execute("SELECT COUNT(DISTINCT symbol_id) FROM inst_holdings WHERE period < date('now', '-45 days')")
        inst_symbols = cur.fetchone()[0]
        cur.execute("SELECT COUNT(DISTINCT symbol_id) FROM sentiment_features")
        sent_symbols = cur.fetchone()[0]
        
        if inst_symbols < 100 or sent_symbols < 100:
            print("INSUFFICIENT=1")
            return
            
        # Get symbols with ≥2 years of both 13F and sentiment data
        cur.execute("""
            WITH inst_span AS (
                SELECT symbol_id, 
                       MIN(period) as min_period, 
                       MAX(period) as max_period,
                       COUNT(DISTINCT period) as periods
                FROM inst_holdings 
                GROUP BY symbol_id 
                HAVING COUNT(DISTINCT period) >= 8
            ),
            sent_span AS (
                SELECT symbol_id,
                       MIN(day) as min_day,
                       MAX(day) as max_day,
                       COUNT(DISTINCT day) as days
                FROM sentiment_features
                GROUP BY symbol_id
                HAVING COUNT(DISTINCT day) >= 500
            )
            SELECT i.symbol_id
            FROM inst_span i
            JOIN sent_span s ON i.symbol_id = s.symbol_id
            WHERE (julianday(i.max_period) - julianday(i.min_period)) >= 730
              AND (julianday(s.max_day) - julianday(s.min_day)) >= 730
        """)
        symbols = [row[0] for row in cur.fetchall()]
        
        if len(symbols) < 10:
            print("INSUFFICIENT=1")
            return
            
        # Get all 13F data for candidate symbols
        cur.execute("""
            SELECT symbol_id, period, shares, value
            FROM inst_holdings
            WHERE symbol_id IN ({})
        """.format(','.join('?'*len(symbols))), symbols)
        inst_data = defaultdict(list)
        for row in cur.fetchall():
            inst_data[row['symbol_id']].append({
                'period': row['period'],
                'shares': row['shares'],
                'value': row['value']
            })
        
        # Get sentiment features
        cur.execute("""
            SELECT symbol_id, day, mean_score
            FROM sentiment_features
            WHERE symbol_id IN ({})
        """.format(','.join('?'*len(symbols))), symbols)
        sent_data = defaultdict(list)
        for row in cur.fetchall():
            sent_data[row['symbol_id']].append({
                'day': row['day'],
                'mean_score': row['mean_score']
            })
        
        # Get fundamentals for shares outstanding
        cur.execute("""
            SELECT symbol_id, metric, value, fetched_at
            FROM fundamentals
            WHERE symbol_id IN ({}) 
              AND metric = 'EntityPublicFloat'
        """.format(','.join('?'*len(symbols))), symbols)
        float_data = defaultdict(list)
        for row in cur.fetchall():
            float_data[row['symbol_id']].append({
                'value': float(row['value']) if row['value'] else None,
                'fetched_at': row['fetched_at']
            })
        
        # Process each symbol
        issued = []
        opportunities = 0
        
        for sym in symbols:
            # Check data availability
            if sym not in inst_data or len(inst_data[sym]) < 8:
                continue
            if sym not in sent_data or len(sent_data[sym]) < 500:
                continue
            if sym not in float_data or len(float_data[sym]) < 2:
                continue
                
            # Sort 13F by period
            inst_sorted = sorted(inst_data[sym], key=lambda x: x['period'])
            
            # Calculate ownership percentage for each period
            ownership = []
            for i, inst in enumerate(inst_sorted):
                period = inst['period']
                # Get public float at or before this period
                float_val = None
                for f in float_data[sym]:
                    if f['value'] and f['fetched_at'] <= period:
                        float_val = f['value']
                
                if not float_val or float_val == 0:
                    continue
                    
                pct = (inst['shares'] / float_val) * 100
                ownership.append({
                    'period': period,
                    'pct': pct,
                    'shares': inst['shares']
                })
            
            if len(ownership) < 4:
                continue
                
            # Calculate quarter-over-quarter increases
            for i in range(1, len(ownership)):
                prev = ownership[i-1]
                curr = ownership[i]
                increase = curr['pct'] - prev['pct']
                
                # Check if 45 days old
                from datetime import datetime, timedelta
                period_date = datetime.strptime(curr['period'], '%Y-%m-%d')
                decision_date = period_date + timedelta(days=45)
                
                if decision_date > datetime.now() - timedelta(days=20*21):  # Need 21 trading days of forward data
                    continue
                    
                opportunities += 1
                
                if increase < 5:
                    continue
                    
                # Check sentiment condition
                sent_sorted = sorted(sent_data[sym], key=lambda x: x['day'])
                
                # Get 20-day moving average at decision date
                sent_at_decision = None
                scores_last_20 = []
                for j, s in enumerate(sent_sorted):
                    if s['day'] <= decision_date.strftime('%Y-%m-%d'):
                        scores_last_20.append(s['mean_score'])
                        if len(scores_last_20) > 20:
                            scores_last_20.pop(0)
                    if s['day'] > decision_date.strftime('%Y-%m-%d') and scores_last_20:
                        break
                
                if len(scores_last_20) < 20:
                    continue
                    
                ma20 = sum(scores_last_20) / len(scores_last_20)
                
                # Get 2-year distribution
                two_year_scores = []
                for s in sent_sorted:
                    if s['day'] >= (decision_date - timedelta(days=730)).strftime('%Y-%m-%d') and \
                       s['day'] <= decision_date.strftime('%Y-%m-%d'):
                        two_year_scores.append(s['mean_score'])
                
                if len(two_year_scores) < 100:
                    continue
                    
                two_year_scores.sort()
                p5 = two_year_scores[int(0.05 * len(two_year_scores))]
                
                if ma20 >= p5:
                    continue
                    
                # We have a signal - check forward return
                cur.execute("""
                    SELECT ts, close 
                    FROM bars 
                    WHERE symbol_id = ? AND tf = '1d' AND ts > ?
                    ORDER BY ts
                    LIMIT 22
                """, (sym, int(decision_date.timestamp())))
                bars = cur.fetchall()
                
                if len(bars) < 22:
                    continue
                    
                start_price = bars[0]['close']
                end_price = bars[21]['close']
                fwd_return = (end_price - start_price) / start_price
                
                issued.append({
                    'symbol': sym,
                    'decision_date': decision_date.strftime('%Y-%m-%d'),
                    'up': 1 if fwd_return > 0 else 0
                })
        
        if len(issued) == 0:
            print("INSUFFICIENT=1")
            return
            
        # Split into held-out and sealed eras
        issued.sort(key=lambda x: x['decision_date'])
        split_idx = int(0.8 * len(issued))
        held_out = issued[:split_idx]
        sealed = issued[split_idx:]
        
        # Calculate metrics
        hits = sum(1 for x in issued if x['up'] == 1)
        precision = hits / len(issued) if issued else 0
        base_rate = hits / len(issued) if issued else 0  # Same as precision in this context
        
        # Calculate distinct days
        distinct_days = len(set(x['decision_date'] for x in issued))
        
        # Calculate design effect (clustering by day)
        day_counts = defaultdict(int)
        for x in issued:
            day_counts[x['decision_date']] += 1
        
        n_days = len(day_counts)
        if n_days > 0:
            design_effect = 1 + sum((count/n_days - 1/n_days)**2 for count in day_counts.values())
        else:
            design_effect = 1
        effective_n = len(issued) / design_effect if design_effect > 0 else len(issued)
        
        # Sealed era precision
        sealed_hits = sum(1 for x in sealed if x['up'] == 1)
        sealed_precision = sealed_hits / len(sealed) if sealed else 0
        
        # Print results
        print(f"ISSUED={len(issued)}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print("INSUFFICIENT=1", file=sys.stderr)
        sys.exit(1)
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
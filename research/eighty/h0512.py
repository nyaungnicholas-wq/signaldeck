# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 511
# cycle_index: 41
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict

def percentile(sorted_list, p):
    if not sorted_list:
        return None
    k = (len(sorted_list) - 1) * p / 100
    f = math.floor(k)
    c = math.ceil(k)
    if f == c:
        return sorted_list[int(k)]
    return sorted_list[f] + (k - f) * (sorted_list[c] - sorted_list[f])

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()
    
    try:
        # Get symbols with 10+ years of daily history (2018-07-26 to now)
        min_daily_bars = 252 * 10
        cursor.execute("""
            SELECT s.id, s.symbol
            FROM symbols s
            JOIN (
                SELECT symbol_id, COUNT(*) as cnt
                FROM bars
                WHERE tf = '1d'
                GROUP BY symbol_id
                HAVING cnt >= ?
            ) b ON s.id = b.symbol_id
            WHERE s.active = 1
            AND s.market = 'stocks'
            ORDER BY s.symbol
        """, (min_daily_bars,))
        symbols = cursor.fetchall()
        
        if not symbols:
            print("INSUFFICIENT=1")
            return
        
        symbol_ids = [s['id'] for s in symbols]
        placeholders = ','.join(['?'] * len(symbol_ids))
        
        # Get all daily bars for these symbols
        cursor.execute(f"""
            SELECT symbol_id, ts, close, volume
            FROM bars
            WHERE tf = '1d'
            AND symbol_id IN ({placeholders})
            ORDER BY symbol_id, ts
        """, symbol_ids)
        all_bars = cursor.fetchall()
        
        # Get insider trades (open-market purchases: code='P')
        cursor.execute(f"""
            SELECT symbol_id, filed_ts, code
            FROM insider_trades
            WHERE code = 'P'
            AND symbol_id IN ({placeholders})
        """, symbol_ids)
        insider_trades = cursor.fetchall()
        
        # Get sentiment features
        cursor.execute(f"""
            SELECT symbol_id, day, mean_score
            FROM sentiment_features
            WHERE symbol_id IN ({placeholders})
            ORDER BY symbol_id, day
        """, symbol_ids)
        sentiment = cursor.fetchall()
        
        # Get prediction outcomes for 21-day horizon
        cursor.execute(f"""
            SELECT symbol_id, ts, up
            FROM prediction_outcomes
            WHERE horizon = 21
            AND symbol_id IN ({placeholders})
        """, symbol_ids)
        outcomes = cursor.fetchall()
        
        # Organize data by symbol
        symbol_bars = defaultdict(list)
        for bar in all_bars:
            sid = bar['symbol_id']
            day = datetime.utcfromtimestamp(bar['ts']).strftime('%Y-%m-%d')
            symbol_bars[sid].append({
                'ts': bar['ts'],
                'day': day,
                'close': bar['close'],
                'volume': bar['volume']
            })
        
        symbol_insider = defaultdict(set)
        for trade in insider_trades:
            sid = trade['symbol_id']
            filed_day = datetime.utcfromtimestamp(trade['filed_ts']).strftime('%Y-%m-%d')
            symbol_insider[sid].add(filed_day)
        
        symbol_sentiment = defaultdict(dict)
        for s in sentiment:
            symbol_sentiment[s['symbol_id']][s['day']] = s['mean_score']
        
        symbol_outcomes = defaultdict(dict)
        for o in outcomes:
            day = datetime.utcfromtimestamp(o['ts']).strftime('%Y-%m-%d')
            symbol_outcomes[o['symbol_id']][day] = o['up']
        
        # For each symbol, compute rolling percentiles and find entry days
        all_calls = []  # (symbol_id, decision_day, outcome_day, hit)
        
        for sid in symbol_ids:
            bars = symbol_bars.get(sid, [])
            if len(bars) < 252:
                continue
            
            # Build arrays for rolling calculations
            days = [b['day'] for b in bars]
            volumes = [b['volume'] for b in bars]
            closes = [b['close'] for b in bars]
            
            # Precompute rolling 252-day volume 20th percentile and sentiment 5th percentile
            # We need sentiment history for this symbol
            sent_history = symbol_sentiment.get(sid, {})
            if not sent_history:
                continue
            
            # For each day with sufficient history, compute percentiles
            for i in range(252, len(bars)):
                decision_day = days[i]
                
                # Volume 20th percentile over past 252 days
                vol_window = volumes[i-252:i]
                vol_p20 = percentile(sorted(vol_window), 20)
                if vol_p20 is None:
                    continue
                current_vol = volumes[i]
                if current_vol < vol_p20:
                    continue  # ABSTAIN: illiquid
                
                # Sentiment 5th percentile over past 252 days
                sent_window = []
                for j in range(i-252, i):
                    d = days[j]
                    if d in sent_history:
                        sent_window.append(sent_history[d])
                if len(sent_window) < 50:  # Need sufficient sentiment data
                    continue
                sent_p5 = percentile(sorted(sent_window), 5)
                if sent_p5 is None:
                    continue
                
                # Check if today's sentiment is below 5th percentile
                today_sent = sent_history.get(decision_day)
                if today_sent is None or today_sent >= sent_p5:
                    continue  # ABSTAIN: sentiment not extreme enough
                
                # Check insider purchase disclosed today
                if decision_day not in symbol_insider.get(sid, set()):
                    continue  # ABSTAIN: no insider purchase disclosed
                
                # Entry criteria met - now check outcome at 21 days
                outcome_idx = i + 21
                if outcome_idx >= len(bars):
                    continue  # Not enough forward data
                
                outcome_day = days[outcome_idx]
                hit = symbol_outcomes.get(sid, {}).get(outcome_day)
                if hit is None:
                    continue  # No label available
                
                all_calls.append({
                    'symbol_id': sid,
                    'decision_day': decision_day,
                    'outcome_day': outcome_day,
                    'hit': hit
                })
        
        if not all_calls:
            print("INSUFFICIENT=1")
            return
        
        # Sort by decision_day
        all_calls.sort(key=lambda x: x['decision_day'])
        
        # Hold out most recent 20% as sealed era
        n_total = len(all_calls)
        n_sealed = max(1, int(n_total * 0.2))
        n_train = n_total - n_sealed
        
        train_calls = all_calls[:n_train]
        sealed_calls = all_calls[n_train:]
        
        # Compute metrics for train set
        issued_train = len(train_calls)
        hits_train = sum(1 for c in train_calls if c['hit'] == 1)
        precision_train = hits_train / issued_train if issued_train > 0 else 0
        
        # Base rate within issued subset
        base_rate_train = hits_train / issued_train if issued_train > 0 else 0
        
        # Distinct days among issued calls
        distinct_days_train = len(set(c['decision_day'] for c in train_calls))
        
        # Design effect: cluster by day, compute effective N
        # Group calls by decision_day
        day_counts = defaultdict(int)
        for c in train_calls:
            day_counts[c['decision_day']] += 1
        
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Use conservative ICC = 0.5 for financial returns clustering
        cluster_sizes = list(day_counts.values())
        avg_cluster_size = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
        icc = 0.5
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n_train = issued_train / design_effect if design_effect > 0 else issued_train
        
        # Sealed era metrics
        issued_sealed = len(sealed_calls)
        hits_sealed = sum(1 for c in sealed_calls if c['hit'] == 1)
        precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0
        
        # Total opportunities = all decision points considered (days with sufficient history)
        # For each symbol, count days where we had 252-day history and sentiment data
        opportunities = 0
        for sid in symbol_ids:
            bars = symbol_bars.get(sid, [])
            sent_history = symbol_sentiment.get(sid, {})
            if len(bars) < 252 or not sent_history:
                continue
            for i in range(252, len(bars)):
                decision_day = bars[i]['day']
                if decision_day in sent_history:
                    opportunities += 1
        
        print(f"ISSUED={issued_train}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision_train:.6f}")
        print(f"BASE_RATE={base_rate_train:.6f}")
        print(f"DISTINCT_DAYS={distinct_days_train}")
        print(f"EFFECTIVE_N={effective_n_train:.6f}")
        print(f"SEALED_PRECISION={precision_sealed:.6f}")
        
    except Exception as e:
        print(f"INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 289
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
        cursor = conn.cursor()
        
        # Get symbols with sufficient price history
        cursor.execute("""
            SELECT symbol_id, MIN(ts) as min_ts, MAX(ts) as max_ts, COUNT(*) as days
            FROM bars WHERE tf='1d'
            GROUP BY symbol_id
            HAVING COUNT(*) >= 252
        """)
        symbol_data = {row[0]: (row[1], row[2], row[3]) for row in cursor.fetchall()}
        
        if not symbol_data:
            print("INSUFFICIENT=1")
            return
        
        # Get symbols with insider trades
        cursor.execute("""
            SELECT DISTINCT symbol_id FROM insider_trades WHERE code='P'
        """)
        insider_symbols = set(row[0] for row in cursor.fetchall())
        
        # Get symbols with sentiment data
        cursor.execute("""
            SELECT DISTINCT symbol_id FROM sentiment_features
        """)
        sentiment_symbols = set(row[0] for row in cursor.fetchall())
        
        # Universe: symbols with all three
        universe = set(symbol_data.keys()) & insider_symbols & sentiment_symbols
        
        if len(universe) < 10:
            print("INSUFFICIENT=1")
            return
        
        # Collect all candidate decision points
        opportunities = []
        
        for symbol_id in universe:
            min_ts, max_ts, days = symbol_data[symbol_id]
            
            # Get price history
            cursor.execute("""
                SELECT ts, close FROM bars 
                WHERE symbol_id=? AND tf='1d'
                ORDER BY ts
            """, (symbol_id,))
            prices = cursor.fetchall()
            
            if len(prices) < 252:
                continue
                
            # Get insider purchases with filed_ts (disclosure date)
            cursor.execute("""
                SELECT filed_ts, tx_ts, price, value
                FROM insider_trades
                WHERE symbol_id=? AND code='P'
                ORDER BY filed_ts
            """, (symbol_id,))
            insider_purchases = cursor.fetchall()
            
            if not insider_purchases:
                continue
                
            # Get sentiment history
            cursor.execute("""
                SELECT day, mean_score FROM sentiment_features
                WHERE symbol_id=?
                ORDER BY day
            """, (symbol_id,))
            sentiment_data = cursor.fetchall()
            
            if len(sentiment_data) < 252:
                continue
            
            # Convert sentiment days to timestamps for alignment
            sentiment_by_ts = {}
            for day, score in sentiment_data:
                # Convert day string to timestamp (assuming YYYY-MM-DD)
                import datetime
                try:
                    dt = datetime.datetime.strptime(day, '%Y-%m-%d')
                    ts = int(dt.timestamp())
                    sentiment_by_ts[ts] = score
                except:
                    continue
            
            # Create aligned price-sentiment series
            aligned = []
            price_dict = {ts: close for ts, close in prices}
            for ts, score in sentiment_by_ts.items():
                if ts in price_dict:
                    aligned.append((ts, price_dict[ts], score))
            
            aligned.sort(key=lambda x: x[0])
            
            if len(aligned) < 252:
                continue
            
            # Create insider disclosure timeline
            insider_timeline = []
            for filed_ts, tx_ts, price, value in insider_purchases:
                insider_timeline.append(filed_ts)
            insider_timeline.sort()
            
            # Iterate through potential decision points
            for i in range(252, len(aligned)):
                decision_ts = aligned[i][0]
                current_price = aligned[i][1]
                current_sentiment = aligned[i][2]
                
                # Check sentiment condition: bottom decile of trailing 252 days
                trailing_sentiments = [aligned[j][2] for j in range(i-251, i+1)]
                if len(trailing_sentiments) < 252:
                    continue
                    
                sorted_sents = sorted(trailing_sentiments)
                p10 = sorted_sents[int(0.1 * len(sorted_sents))]
                
                if current_sentiment > p10:
                    continue
                
                # Check 20-day decline condition
                if i < 20:
                    continue
                price_20d_ago = aligned[i-20][1]
                if price_20d_ago <= 0:
                    continue
                decline_pct = (current_price - price_20d_ago) / price_20d_ago
                if decline_pct > -0.10:  # Not declined 10%
                    continue
                
                # Check insider cluster condition: >=3 purchases within prior 10 calendar days
                ten_days_ago = decision_ts - (10 * 86400)
                recent_purchases = [ts for ts in insider_timeline 
                                   if ten_days_ago <= ts <= decision_ts]
                
                if len(recent_purchases) < 3:
                    continue
                
                # Check price rise from first disclosure
                first_disclosure_ts = min(recent_purchases)
                # Find price closest to first disclosure
                disclosure_price = None
                for ts, price, _ in aligned:
                    if ts >= first_disclosure_ts:
                        disclosure_price = price
                        break
                
                if disclosure_price and disclosure_price > 0:
                    rise_pct = (current_price - disclosure_price) / disclosure_price
                    if rise_pct > 0.05:
                        continue
                
                # All conditions met - issue BUY call
                opportunities.append((symbol_id, decision_ts, 1))  # Will check outcome later
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return
        
        # Get outcomes from prediction_outcomes
        calls_with_outcomes = []
        for symbol_id, decision_ts, _ in opportunities:
            cursor.execute("""
                SELECT up, fwd_return FROM prediction_outcomes
                WHERE symbol_id=? AND ts=? AND horizon=21
                LIMIT 1
            """, (symbol_id, decision_ts))
            result = cursor.fetchone()
            if result:
                calls_with_outcomes.append((symbol_id, decision_ts, result[0], result[1]))
        
        if len(calls_with_outcomes) < 20:
            print("INSUFFICIENT=1")
            return
        
        # Sort by time and split into training/sealed (80/20)
        calls_with_outcomes.sort(key=lambda x: x[1])
        split_idx = int(0.8 * len(calls_with_outcomes))
        training = calls_with_outcomes[:split_idx]
        sealed = calls_with_outcomes[split_idx:]
        
        # Calculate metrics
        total_issued = len(calls_with_outcomes)
        total_opportunities = len(opportunities)
        
        hits = sum(1 for _, _, up, _ in calls_with_outcomes if up)
        precision = hits / total_issued if total_issued > 0 else 0
        
        base_rate = sum(1 for _, _, up, _ in calls_with_outcomes if up) / total_issued
        
        # Distinct days
        distinct_days = len(set(ts for _, ts, _, _ in calls_with_outcomes))
        
        # Design effect: calls clustered by day
        day_counts = defaultdict(int)
        for _, ts, _, _ in calls_with_outcomes:
            day_key = ts // 86400  # Group by day
            day_counts[day_key] += 1
        
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        
        # Compute ICC for binary outcome
        p = base_rate
        # Between-cluster variance
        day_proportions = {}
        for day_key, count in day_counts.items():
            day_hits = sum(1 for _, ts, up, _ in calls_with_outcomes 
                         if ts // 86400 == day_key and up)
            day_proportions[day_key] = day_hits / count if count > 0 else 0
        
        between_var = sum(day_counts[dk] * (day_proportions[dk] - p) ** 2 
                        for dk in day_counts) / (len(day_counts) - 1) if len(day_counts) > 1 else 0
        
        within_var = sum(day_counts[dk] * day_proportions[dk] * (1 - day_proportions[dk]) 
                       for dk in day_counts) / (total_issued - len(day_counts)) if total_issued > len(day_counts) else 0
        
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = total_issued / design_effect
        
        # Sealed era metrics
        sealed_hits = sum(1 for _, _, up, _ in sealed if up)
        sealed_precision = sealed_hits / len(sealed) if sealed else 0
        
        conn.close()
        
        print(f"ISSUED={total_issued}")
        print(f"OPPORTUNITIES={total_opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()
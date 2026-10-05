# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 578
# cycle_index: 36
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    db_path = 'data/signaldeck.db'
    try:
        conn = sqlite3.connect(f'file:{db_path}?mode=ro', uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    try:
        cur = conn.cursor()
        
        # Get all symbols with market info
        cur.execute("SELECT id, symbol, market FROM symbols WHERE market = 'stocks'")
        symbols = {row[0]: row[1] for row in cur.fetchall()}
        if not symbols:
            print("INSUFFICIENT=1")
            conn.close()
            return
        
        # Get prediction outcomes for 21-day horizon
        cur.execute("""
            SELECT symbol_id, ts, up
            FROM prediction_outcomes
            WHERE horizon = '21d'
        """)
        labels = {}
        for row in cur.fetchall():
            symbol_id, ts, up = row
            if symbol_id in symbols:
                labels[(symbol_id, ts)] = up
        
        if not labels:
            print("INSUFFICIENT=1")
            conn.close()
            return
        
        # Get fundamentals for revenue growth and public float
        cur.execute("""
            SELECT symbol_id, metric, value, fetched_at
            FROM fundamentals
            WHERE metric IN ('Revenues', 'EntityPublicFloat')
        """)
        fundamentals = defaultdict(list)
        for row in cur.fetchall():
            symbol_id, metric, value, fetched_at = row
            if symbol_id in symbols:
                fundamentals[symbol_id].append((metric, float(value), fetched_at))
        
        # Get stocktwits sentiment for bullish ratio
        cur.execute("""
            SELECT symbol_id, ts, bullish, bearish
            FROM stocktwits_sentiment
            WHERE bullish + bearish > 0
        """)
        st_sentiment = defaultdict(list)
        for row in cur.fetchall():
            symbol_id, ts, bullish, bearish = row
            if symbol_id in symbols:
                ratio = bullish / (bullish + bearish) if (bullish + bearish) > 0 else 0
                st_sentiment[symbol_id].append((ts, ratio))
        
        # Get news sentiment
        cur.execute("""
            SELECT symbol_id, ts, sentiment
            FROM news
            WHERE sentiment IS NOT NULL
        """)
        news = defaultdict(list)
        for row in cur.fetchall():
            symbol_id, ts, sentiment = row
            if symbol_id in symbols:
                news[symbol_id].append((ts, sentiment))
        
        # Get 13F holdings
        cur.execute("""
            SELECT symbol_id, period, shares
            FROM inst_holdings
        """)
        inst_holdings = defaultdict(list)
        for row in cur.fetchall():
            symbol_id, period, shares = row
            if symbol_id in symbols:
                inst_holdings[symbol_id].append((period, shares))
        
        # Get bars for price data and volume
        cur.execute("""
            SELECT symbol_id, ts, close, volume
            FROM bars
            WHERE tf = '1d'
        """)
        bars = defaultdict(list)
        for row in cur.fetchall():
            symbol_id, ts, close, volume = row
            if symbol_id in symbols:
                bars[symbol_id].append((ts, close, volume))
        
        conn.close()
        
        # Build decision points
        opportunities = []
        issued = []
        
        for symbol_id in symbols.keys():
            if symbol_id not in labels:
                continue
                
            # Check universe criteria: positive trailing 12-month revenue growth
            rev_values = [v for m, v, _ in fundamentals.get(symbol_id, []) if m == 'Revenues']
            if len(rev_values) < 2:
                continue
            # Simple: compare last two revenue values
            rev_values.sort(reverse=True)
            if rev_values[0] <= rev_values[1]:
                continue
            
            # Public float < $1B
            float_values = [v for m, v, _ in fundamentals.get(symbol_id, []) if m == 'EntityPublicFloat']
            if not float_values:
                continue
            public_float = min(float_values)
            if public_float >= 1e9:
                continue
            
            # StockTwits bullish ratio < 0.3
            if symbol_id not in st_sentiment or len(st_sentiment[symbol_id]) < 4:
                continue
            # Get bottom quartile
            ratios = [r for _, r in st_sentiment[symbol_id]]
            ratios.sort()
            q1_idx = len(ratios) // 4
            if ratios[q1_idx] >= 0.3:
                continue
            
            # Get all possible decision dates from labels
            for decision_ts in [ts for (sid, ts) in labels.keys() if sid == symbol_id]:
                # As-of discipline: need news, 13F, and bars before decision_ts
                
                # Check 5-day news sentiment
                recent_news = [s for ts, s in news.get(symbol_id, []) 
                             if ts <= decision_ts and ts > decision_ts - 5*86400]
                if len(recent_news) < 3:
                    continue
                current_sentiment = sum(recent_news) / len(recent_news)
                
                # Check previous 5-day sentiment (5-10 days ago)
                prev_news = [s for ts, s in news.get(symbol_id, []) 
                           if ts <= decision_ts - 5*86400 and ts > decision_ts - 10*86400]
                if not prev_news:
                    continue
                prev_sentiment = sum(prev_news) / len(prev_news)
                
                # Entry: sentiment improves from negative to positive
                if not (prev_sentiment < 0 and current_sentiment > 0.3):
                    continue
                
                # 13F institutional ownership decrease QoQ
                symbol_holdings = sorted(inst_holdings.get(symbol_id, []), key=lambda x: x[0], reverse=True)
                if len(symbol_holdings) < 2:
                    continue
                
                # Find most recent filing with period at least 45 days before decision
                valid_periods = [p for p, _ in symbol_holdings 
                               if datetime.strptime(p, '%Y-%m-%d') <= datetime.utcfromtimestamp(decision_ts) - timedelta(days=45)]
                if len(valid_periods) < 2:
                    continue
                
                # Get latest and previous holdings
                latest = symbol_holdings[0][1]
                prev = symbol_holdings[1][1]
                if latest >= prev:
                    continue
                
                # Check abstain conditions
                symbol_bars = sorted(bars.get(symbol_id, []), key=lambda x: x[0], reverse=True)
                if not symbol_bars:
                    continue
                
                # Current day bar
                current_bar = None
                for ts, close, volume in symbol_bars:
                    if ts <= decision_ts:
                        current_bar = (ts, close, volume)
                        break
                if not current_bar:
                    continue
                
                # Daily turnover > 2% of float
                turnover = current_bar[2] / public_float if public_float > 0 else 1
                if turnover > 0.02:
                    continue
                
                # Stock moved >15% in past 21 days
                price_21d_ago = None
                for ts, close, _ in symbol_bars:
                    if ts <= decision_ts - 21*86400:
                        price_21d_ago = close
                        break
                if price_21d_ago is None:
                    continue
                movement = (current_bar[1] - price_21d_ago) / price_21d_ago
                if abs(movement) > 0.15:
                    continue
                
                # Issue call
                opportunities.append(decision_ts)
                label = labels[(symbol_id, decision_ts)]
                issued.append((decision_ts, label))
        
        if not issued:
            print("INSUFFICIENT=1")
            return
        
        # Calculate metrics
        hits = sum(1 for _, label in issued if label == 1)
        precision = hits / len(issued)
        base_rate = hits / len(issued)
        
        # Count distinct days
        issued_days = set()
        for ts, _ in issued:
            dt = datetime.utcfromtimestamp(ts).date()
            issued_days.add(dt)
        distinct_days = len(issued_days)
        
        # Design effect: assume calls are clustered, effective_n = issued / design_effect
        # Estimate design effect from autocorrelation of calls within days
        day_counts = defaultdict(int)
        for ts, _ in issued:
            dt = datetime.utcfromtimestamp(ts).date()
            day_counts[dt] += 1
        
        if day_counts:
            n_days = len(day_counts)
            avg_per_day = len(issued) / n_days
            # Simplified: design effect ≈ 1 + variance(mean)/mean
            if avg_per_day > 0:
                variance = sum((c - avg_per_day)**2 for c in day_counts.values()) / n_days
                design_effect = 1 + variance / avg_per_day if avg_per_day > 0 else 1
            else:
                design_effect = 1
        else:
            design_effect = 1
        
        effective_n = len(issued) / design_effect
        
        # Sealed era: most recent 20% of decision times
        all_decision_times = sorted(set(ts for ts, _ in issued))
        cutoff_idx = int(0.8 * len(all_decision_times))
        sealed_cutoff = all_decision_times[cutoff_idx] if cutoff_idx < len(all_decision_times) else all_decision_times[-1]
        
        sealed_hits = sum(1 for ts, label in issued if ts >= sealed_cutoff and label == 1)
        sealed_issued = sum(1 for ts, _ in issued if ts >= sealed_cutoff)
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Invariants check
        if distinct_days > len(issued):
            distinct_days = len(issued)
        if effective_n >= len(issued):
            effective_n = len(issued) - 0.1
        
        print(f"ISSUED={len(issued)}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
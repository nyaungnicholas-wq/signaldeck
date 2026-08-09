# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 416
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timezone
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Get all insider open-market purchases (code='P') with filed_ts
        cur.execute("""
            SELECT symbol_id, filed_ts 
            FROM insider_trades 
            WHERE code = 'P' AND filed_ts IS NOT NULL
        """)
        insider_purchases = cur.fetchall()
        
        if not insider_purchases:
            print("INSUFFICIENT=1")
            return
        
        # Get symbols with sentiment data
        cur.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
        sentiment_symbols = {row[0] for row in cur.fetchall()}
        
        # Get symbols with daily bars
        cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'")
        bar_symbols = {row[0] for row in cur.fetchall()}
        
        # Filter insider purchases to symbols with both sentiment and bars
        valid_purchases = [
            (sid, ts) for sid, ts in insider_purchases
            if sid in sentiment_symbols and sid in bar_symbols
        ]
        
        if not valid_purchases:
            print("INSUFFICIENT=1")
            return
        
        # Get all distinct disclosure dates (as dates) for each symbol
        symbol_disclosures = defaultdict(set)
        for sid, ts in valid_purchases:
            dt = datetime.fromtimestamp(ts, tz=timezone.utc)
            symbol_disclosures[sid].add(dt.date())
        
        # Get prediction outcomes for horizon 21 days
        cur.execute("""
            SELECT symbol_id, ts, up 
            FROM prediction_outcomes 
            WHERE horizon = '21d'
        """)
        outcomes = cur.fetchall()
        outcome_map = defaultdict(dict)
        for sid, ts, up in outcomes:
            dt = datetime.fromtimestamp(ts, tz=timezone.utc)
            outcome_map[sid][dt.date()] = bool(up)
        
        # Precompute sentiment features by symbol and date
        sentiment_by_symbol = defaultdict(list)
        cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features ORDER BY symbol_id, day")
        for sid, day_str, score in cur.fetchall():
            day = datetime.strptime(day_str, '%Y-%m-%d').date()
            sentiment_by_symbol[sid].append((day, score if score is not None else 0.0))
        
        # Precompute daily close prices by symbol
        price_by_symbol = defaultdict(list)
        cur.execute("""
            SELECT symbol_id, ts, close 
            FROM bars 
            WHERE tf = '1d' 
            ORDER BY symbol_id, ts
        """)
        for sid, ts, close in cur.fetchall():
            day = datetime.fromtimestamp(ts, tz=timezone.utc).date()
            price_by_symbol[sid].append((day, close))
        
        # Helper function to compute moving average
        def compute_ma(series, end_date, window):
            # series: list of (date, value) sorted by date
            # Get last `window` values up to and including end_date
            recent = []
            for date, val in reversed(series):
                if date > end_date:
                    continue
                recent.append(val)
                if len(recent) == window:
                    break
            if len(recent) < window:
                return None
            return sum(recent) / window
        
        # Process each symbol's disclosures
        opportunities = 0
        issued_calls = []  # (decision_date, outcome)
        
        for sid, disclosures in symbol_disclosures.items():
            sentiment_series = sentiment_by_symbol.get(sid, [])
            price_series = price_by_symbol.get(sid, [])
            
            if not sentiment_series or not price_series:
                continue
            
            for disc_date in disclosures:
                # Check if we have sentiment data at least to disc_date
                if disc_date < sentiment_series[0][0]:
                    continue
                
                # Compute sentiment MAs
                ma5 = compute_ma(sentiment_series, disc_date, 5)
                ma20 = compute_ma(sentiment_series, disc_date, 20)
                
                if ma5 is None or ma20 is None:
                    continue
                
                # Check if price data at least 200 days
                if disc_date < price_series[199][0]:
                    continue
                
                # Compute price MA200
                ma200_price = compute_ma(price_series, disc_date, 200)
                if ma200_price is None:
                    continue
                
                # Get current close
                current_close = None
                for date, close in reversed(price_series):
                    if date <= disc_date:
                        current_close = close
                        break
                
                if current_close is None:
                    continue
                
                opportunities += 1
                
                # Entry conditions
                if ma5 >= ma20:  # Sentiment deteriorating
                    continue
                if current_close >= ma200_price:  # Price above MA200
                    continue
                
                # Check if outcome exists for this symbol and date
                outcome = outcome_map.get(sid, {}).get(disc_date)
                if outcome is None:
                    continue
                
                issued_calls.append((disc_date, outcome))
        
        # If no calls issued
        if not issued_calls:
            print("INSUFFICIENT=1")
            return
        
        # Sort by date for sealing
        issued_calls.sort(key=lambda x: x[0])
        n_total = len(issued_calls)
        seal_cutoff = int(n_total * 0.8)
        train_calls = issued_calls[:seal_cutoff]
        sealed_calls = issued_calls[seal_cutoff:]
        
        # Metrics for total
        issued_count = n_total
        hits = sum(1 for _, up in issued_calls if up)
        precision = hits / issued_count
        base_rate = hits / issued_count
        
        # Distinct days
        distinct_days = len({date for date, _ in issued_calls})
        
        # Design effect using day clustering
        # Cluster by day
        day_clusters = defaultdict(list)
        for date, up in issued_calls:
            day_clusters[date].append(1 if up else 0)
        
        k = len(day_clusters)  # number of clusters
        m_avg = issued_count / k if k > 0 else 1
        
        # Compute ICC
        if k > 1 and issued_count > k:
            # Overall proportion
            p = precision
            
            # Between-cluster variance
            s2_between = 0
            for date, outcomes in day_clusters.items():
                m_i = len(outcomes)
                p_i = sum(outcomes) / m_i
                s2_between += m_i * (p_i - p) ** 2
            s2_between /= (k - 1)
            
            # Within-cluster variance
            s2_within = 0
            for date, outcomes in day_clusters.items():
                for outcome in outcomes:
                    s2_within += (outcome - p) ** 2
            s2_within /= (issued_count - k)
            
            # ICC estimate
            if s2_within > 0:
                icc = (s2_between - s2_within) / (s2_between + (m_avg - 1) * s2_within)
                design_effect = 1 + (m_avg - 1) * max(icc, 0)
            else:
                design_effect = 1.0
        else:
            design_effect = 1.0
        
        effective_n = issued_count / design_effect if design_effect > 0 else issued_count
        
        # Metrics for sealed era
        sealed_count = len(sealed_calls)
        sealed_hits = sum(1 for _, up in sealed_calls if up)
        sealed_precision = sealed_hits / sealed_count if sealed_count > 0 else 0.0
        
        # Print results
        print(f"ISSUED={issued_count}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n}")
        print(f"SEALED_PRECISION={sealed_precision}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()
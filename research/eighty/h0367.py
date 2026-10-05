# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 366
# cycle_index: 34
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 21
SENTIMENT_WINDOW = 252
TERM_SPREAD_WINDOW = 20
TOP_QUANTILE_CUTOFF = 0.05
INSTITUTIONAL_CUTOFF = 0.5
MIN_TRADING_DAYS = SENTIMENT_WINDOW + TERM_SPREAD_WINDOW + HORIZON

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        conn.row_factory = sqlite3.Row
        cursor = conn.cursor()
        
        # Check for required tables
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = [row[0] for row in cursor.fetchall()]
        required_tables = ['macro_series', 'sentiment_features', 'inst_holdings', 'prediction_outcomes', 'bars']
        for t in required_tables:
            if t not in tables:
                print("INSUFFICIENT=1")
                return
        
        # Get macro series for term spread (assuming DGS10 and DGS2 exist)
        cursor.execute("SELECT DISTINCT series FROM macro_series WHERE series IN ('DGS10', 'DGS2')")
        series_rows = cursor.fetchall()
        series_names = [row[0] for row in series_rows]
        if len(series_names) < 2:
            print("INSUFFICIENT=1")
            return
        
        # Get all symbols with sentiment features
        cursor.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
        sentiment_symbols = [row[0] for row in cursor.fetchall()]
        if not sentiment_symbols:
            print("INSUFFICIENT=1")
            return
        
        # Get institutional ownership data (most recent 13F per symbol, lagged 45 days)
        inst_data = {}
        for symbol_id in sentiment_symbols:
            cursor.execute("""
                SELECT period, value 
                FROM inst_holdings 
                WHERE symbol_id = ?
                ORDER BY period DESC 
                LIMIT 1
            """, (symbol_id,))
            row = cursor.fetchone()
            if row:
                period, value = row
                # Convert period to date and add 45 days
                period_date = datetime.strptime(period, '%Y-%m-%d')
                available_date = period_date + timedelta(days=45)
                inst_data[symbol_id] = (available_date, value)
        
        if not inst_data:
            print("INSUFFICIENT=1")
            return
        
        # Compute universe median of institutional ownership
        inst_values = [v for _, v in inst_data.values()]
        inst_median = sorted(inst_values)[len(inst_values) // 2]
        
        # Load macro series into memory (DGS10, DGS2)
        macro_series = defaultdict(dict)
        for series_name in ['DGS10', 'DGS2']:
            cursor.execute("""
                SELECT ts, value 
                FROM macro_series 
                WHERE series = ?
                ORDER BY ts
            """, (series_name,))
            for row in cursor.fetchall():
                ts, value = row
                if value is not None:
                    macro_series[series_name][ts] = float(value)
        
        # Compute term spread time series
        common_ts = sorted(set(macro_series['DGS10'].keys()) & set(macro_series['DGS2'].keys()))
        term_spread = {}
        for ts in common_ts:
            term_spread[ts] = macro_series['DGS10'][ts] - macro_series['DGS2'][ts]
        
        if not term_spread:
            print("INSUFFICIENT=1")
            return
        
        # Sort timestamps
        ts_list = sorted(term_spread.keys())
        ts_to_idx = {ts: i for i, ts in enumerate(ts_list)}
        
        # Load sentiment features for relevant symbols
        sentiment_data = defaultdict(list)  # symbol_id -> [(ts, mean_score)]
        for symbol_id in sentiment_symbols:
            cursor.execute("""
                SELECT ts, mean_score 
                FROM sentiment_features 
                WHERE symbol_id = ? AND mean_score IS NOT NULL
                ORDER BY ts
            """, (symbol_id,))
            for row in cursor.fetchall():
                ts, mean_score = row
                sentiment_data[symbol_id].append((ts, float(mean_score)))
        
        # Load prices for 21-day forward returns
        price_data = defaultdict(dict)  # symbol_id -> {ts: close}
        for symbol_id in sentiment_symbols:
            cursor.execute("""
                SELECT ts, close 
                FROM bars 
                WHERE symbol_id = ? AND tf = '1d' AND close IS NOT NULL
                ORDER BY ts
            """, (symbol_id,))
            for row in cursor.fetchall():
                ts, close = row
                price_data[symbol_id][ts] = float(close)
        
        # Get outcomes for 21-day horizon
        outcomes = defaultdict(dict)  # symbol_id -> {ts: up}
        cursor.execute("""
            SELECT symbol_id, ts, up 
            FROM prediction_outcomes 
            WHERE horizon = ?
        """, (HORIZON,))
        for row in cursor.fetchall():
            symbol_id, ts, up = row
            if up is not None:
                outcomes[symbol_id][ts] = bool(up)
        
        conn.close()
        
        # Collect all opportunities and issued calls
        opportunities = []
        issued_calls = []
        
        # For each symbol with sentiment and institutional data
        for symbol_id in sentiment_symbols:
            if symbol_id not in inst_data or symbol_id not in sentiment_data or symbol_id not in price_data:
                continue
            
            inst_available, inst_value = inst_data[symbol_id]
            inst_above_median = inst_value > inst_median
            
            # Sort sentiment data by timestamp
            sent_list = sorted(sentiment_data[symbol_id])
            if len(sent_list) < SENTIMENT_WINDOW:
                continue
            
            # Create rolling window of sentiment scores
            sent_scores = [s for _, s in sent_list]
            sent_ts = [t for t, _ in sent_list]
            
            # For each possible decision point
            for i in range(SENTIMENT_WINDOW, len(sent_list)):
                decision_ts = sent_ts[i]
                decision_date = datetime.utcfromtimestamp(decision_ts).date()
                
                # Check institutional ownership availability
                if decision_date < inst_available.date():
                    continue
                
                # Compute 252-day rolling percentile for sentiment
                window = sent_scores[i-SENTIMENT_WINDOW:i+1]
                current_score = sent_scores[i]
                # Count how many in window are <= current_score
                count_leq = sum(1 for x in window if x <= current_score)
                percentile = count_leq / len(window)
                
                # Check if sentiment is in bottom 5%
                if percentile > TOP_QUANTILE_CUTOFF:
                    continue
                
                # Check term spread increase (using trading days, approximated by timestamps)
                # Find the timestamp approximately 20 trading days before decision_ts
                # (We use 20 calendar days as rough approximation since we don't have trading calendar)
                approx_20_days_ago = decision_ts - (20 * 86400)
                
                # Find closest available term spread timestamp before approx_20_days_ago
                ts_idx = ts_to_idx.get(decision_ts)
                if ts_idx is None or ts_idx < TERM_SPREAD_WINDOW:
                    continue
                
                current_spread = term_spread[decision_ts]
                past_spread = term_spread[ts_list[ts_idx - TERM_SPREAD_WINDOW]]
                spread_increase = current_spread - past_spread
                
                # Check if spread increased by >= 20 bps (0.20)
                if spread_increase < 0.20:
                    continue
                
                # This is an opportunity that meets entry criteria
                opportunity = (symbol_id, decision_ts)
                opportunities.append(opportunity)
                
                # Find forward price (21 trading days later, approximated)
                # Find the closest timestamp approximately 21 days after decision_ts
                approx_forward_ts = decision_ts + (HORIZON * 86400)
                price_dict = price_data[symbol_id]
                
                # Find closest available price after decision_ts
                forward_ts = None
                for ts in sorted(price_dict.keys()):
                    if ts > decision_ts:
                        forward_ts = ts
                        break
                
                if forward_ts is None or forward_ts - decision_ts > 30 * 86400:  # More than 30 days later
                    continue
                
                # Check if we have an outcome for this symbol and decision timestamp
                if symbol_id in outcomes and decision_ts in outcomes[symbol_id]:
                    up = outcomes[symbol_id][decision_ts]
                    issued_calls.append((symbol_id, decision_ts, up, decision_ts))
        
        # If no opportunities or issued calls, insufficient
        if not opportunities or not issued_calls:
            print("INSUFFICIENT=1")
            return
        
        # Split into regular and sealed era (most recent 20%)
        all_timestamps = sorted(set(ts for _, ts in opportunities))
        split_idx = int(len(all_timestamps) * 0.8)
        split_ts = all_timestamps[split_idx] if split_idx < len(all_timestamps) else all_timestamps[-1]
        
        regular_calls = [c for c in issued_calls if c[3] < split_ts]
        sealed_calls = [c for c in issued_calls if c[3] >= split_ts]
        
        if not regular_calls or not sealed_calls:
            print("INSUFFICIENT=1")
            return
        
        # Calculate metrics for regular calls
        issued = len(issued_calls)
        opportunities_count = len(opportunities)
        hits = sum(1 for _, _, up, _ in issued_calls if up)
        precision = hits / issued
        base_rate = sum(1 for _, _, up, _ in issued_calls if up) / issued
        distinct_days = len(set(ts for _, _, _, ts in issued_calls))
        
        # Calculate design effect using intraclass correlation
        # Group calls by day
        calls_by_day = defaultdict(list)
        for symbol_id, decision_ts, up, ts in issued_calls:
            calls_by_day[ts].append(up)
        
        # Calculate within-day variance and between-day variance
        total_p = hits / issued
        between_day_var = 0
        within_day_var = 0
        total_weight = 0
        
        for day, ups in calls_by_day.items():
            n_day = len(ups)
            if n_day == 0:
                continue
            p_day = sum(1 for up in ups if up) / n_day
            between_day_var += n_day * (p_day - total_p) ** 2
            within_day_var += n_day * p_day * (1 - p_day)
            total_weight += n_day
        
        if total_weight == 0:
            print("INSUFFICIENT=1")
            return
        
        between_day_var /= total_weight
        within_day_var /= total_weight
        
        # Intraclass correlation
        if (between_day_var + within_day_var) == 0:
            rho = 0
        else:
            rho = between_day_var / (between_day_var + within_day_var)
        
        # Design effect
        m_bar = issued / len(calls_by_day) if calls_by_day else 1
        design_effect = 1 + (m_bar - 1) * rho
        effective_n = issued / design_effect
        
        # Calculate sealed era metrics
        sealed_hits = sum(1 for _, _, up, _ in sealed_calls if up)
        sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
        
        # Check if precision meets criteria
        precision_met = precision >= 0.80
        base_rate_met = base_rate <= 0.70
        
        # Wilson score interval lower bound
        z = 1.96  # 95% confidence
        n = issued
        p_hat = precision
        denominator = 1 + z**2 / n
        center = (p_hat + z**2 / (2 * n)) / denominator
        margin = z * math.sqrt((p_hat * (1 - p_hat) + z**2 / (4 * n)) / n) / denominator
        lower_bound = center - margin
        interval_met = lower_bound > base_rate
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
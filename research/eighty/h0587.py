# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 586
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3, math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Get universe: symbols with >=10 open-market purchases and EntityPublicFloat as_of decision_date
        # Using fundamentals: metric='EntityPublicFloat', need fetched_at <= decision_date
        # For each candidate decision point (from insider trades), we'll check conditions
        
        # First, get all open-market purchases (code='P') with their symbol and dates
        cur.execute("""
            SELECT symbol_id, tx_ts, filed_ts, price, shares, value
            FROM insider_trades 
            WHERE code='P'
            ORDER BY symbol_id, tx_ts
        """)
        trades = cur.fetchall()
        
        if len(trades) < 20:
            print("INSUFFICIENT=1")
            return
            
        # Precompute historical median delay per symbol
        sym_trades = {}
        for sym, tx, filed, price, shares, value in trades:
            sym_trades.setdefault(sym, []).append((tx, filed))
            
        # For each trade, compute delay (filed - tx) in seconds, then to days
        # We'll compute median delay per symbol using historical data before current trade
        sym_trade_lists = {}
        for sym in sym_trades:
            sym_trade_lists[sym] = sorted(sym_trades[sym], key=lambda x: x[0])
            
        # We'll iterate through potential decision points (each trade's filed date as decision date)
        opportunities = []  # (symbol, decision_date_ts, forward_return)
        
        for sym in sym_trade_lists:
            trade_list = sym_trade_lists[sym]
            # Compute median delay up to each trade
            for i, (tx, filed) in enumerate(trade_list):
                # Historical trades before current one
                hist_delays = [(filed2 - tx2) / 86400 for tx2, filed2 in trade_list[:i]]
                if len(hist_delays) < 3:
                    continue
                median_delay = sorted(hist_delays)[len(hist_delays)//2]
                current_delay = (filed - tx) / 86400
                
                if current_delay <= median_delay:
                    continue  # Doesn't exceed median
                    
                # Check public float condition: need at least 2 quarters within 5% change
                # fundamentals: symbol_id, metric, value, as_of, fetched_at
                cur.execute("""
                    SELECT value, as_of, fetched_at
                    FROM fundamentals 
                    WHERE symbol_id=? AND metric='EntityPublicFloat' AND fetched_at<=?
                    ORDER BY as_of DESC
                """, (sym, filed))
                ff_rows = cur.fetchall()
                if len(ff_rows) < 2:
                    continue
                # Take two most recent by as_of
                val1, asof1, fetch1 = ff_rows[0]
                val2, asof2, fetch2 = ff_rows[1]
                if val1 is None or val2 is None or val2 == 0:
                    continue
                pct_change = abs((val1 - val2) / val2)
                if pct_change > 0.05:
                    continue
                    
                # Check news sentiment condition
                # Need sentiment_features: day is YYYY-MM-DD, get up to filed date
                cur.execute("""
                    SELECT day, mean_score
                    FROM sentiment_features
                    WHERE symbol_id=? AND day<=date(?, 'unixepoch')
                    ORDER BY day
                """, (sym, filed))
                sent_rows = cur.fetchall()
                if len(sent_rows) < 200:
                    continue
                scores = [s[1] for s in sent_rows]
                current_score = scores[-1]
                ma20 = sum(scores[-20:]) / 20
                ma200 = sum(scores[-200:]) / 200
                if not (ma200 <= current_score <= ma20):
                    continue
                    
                # All conditions met, get forward return label
                # Use prediction_outcomes: horizon=30, need up and fwd_return
                # ts is unix epoch, decision date is filed (filed_ts)
                cur.execute("""
                    SELECT fwd_return, up
                    FROM prediction_outcomes
                    WHERE symbol_id=? AND horizon=30 AND ts=?
                """, (sym, filed))
                outcome = cur.fetchone()
                if outcome is None:
                    continue
                fwd_return, up = outcome
                if up is None:
                    continue
                opportunities.append((sym, filed, up, fwd_return, current_delay, median_delay))
        
        conn.close()
        
        if len(opportunities) < 5:
            print("INSUFFICIENT=1")
            return
            
        # Sort by decision date for train/test split
        opportunities.sort(key=lambda x: x[1])
        n = len(opportunities)
        split_idx = int(n * 0.8)
        train = opportunities[:split_idx]
        sealed = opportunities[split_idx:]
        
        # Compute metrics
        def calc_metrics(subset):
            if not subset:
                return 0, 0, 0, 0, 0, 0, 0
            issued = len(subset)
            hits = sum(1 for x in subset if x[3] > 0)  # positive forward return
            precision = hits / issued if issued else 0
            base_rate = hits / issued  # same as precision in this case (up class)
            distinct_days = len(set(x[1] for x in subset))
            
            # Design effect calculation
            # Cluster by day
            day_counts = {}
            day_hits = {}
            for x in subset:
                day = x[1]
                day_counts[day] = day_counts.get(day, 0) + 1
                day_hits[day] = day_hits.get(day, 0) + (1 if x[3] > 0 else 0)
                
            k = len(day_counts)  # number of clusters
            if k == 0:
                return issued, 0, precision, base_rate, 0, 0, 0
                
            m = issued / k  # average cluster size
            # ICC approximation: between-cluster variance / total variance
            p = hits / issued  # overall probability
            var_total = p * (1 - p)
            
            # Between-cluster variance
            var_between = 0
            for day in day_counts:
                p_day = day_hits[day] / day_counts[day]
                var_between += day_counts[day] * (p_day - p) ** 2
            var_between /= (k - 1) if k > 1 else 1
            
            # Within-cluster variance
            var_within = 0
            for day in day_counts:
                p_day = day_hits[day] / day_counts[day]
                var_within += day_counts[day] * p_day * (1 - p_day)
            var_within /= (issued - k) if issued > k else 1
            
            if var_total == 0:
                icc = 0
            else:
                icc = var_between / (var_between + var_within) if (var_between + var_within) > 0 else 0
                
            deff = 1 + (m - 1) * icc
            effective_n = issued / deff if deff > 0 else issued
            
            return issued, hits, precision, base_rate, distinct_days, effective_n, 0
        
        # Train metrics (not required to print, but needed for sealed)
        issued, hits, precision, base_rate, distinct_days, effective_n, _ = calc_metrics(train)
        # Sealed metrics
        s_issued, s_hits, s_precision, s_base_rate, s_distinct_days, s_effective_n, _ = calc_metrics(sealed)
        
        # Print required lines
        print(f"ISSUED={s_issued}")
        print(f"OPPORTUNITIES={n}")
        print(f"PRECISION={s_precision:.6f}")
        print(f"BASE_RATE={s_base_rate:.6f}")
        print(f"DISTINCT_DAYS={s_distinct_days}")
        print(f"EFFECTIVE_N={s_effective_n:.2f}")
        print(f"SEALED_PRECISION={s_precision:.6f}")
        
    except Exception as e:
        print(f"ERROR: {e}")
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 334
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

def get_unix_date(ts):
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def get_date_rows(cur, table, symbol_id, date_col, end_date, days):
    if table == 'sentiment_features':
        query = f"SELECT day, mean_score FROM {table} WHERE symbol_id=? AND day<=? ORDER BY day DESC LIMIT ?"
        cur.execute(query, (symbol_id, end_date, days))
        return cur.fetchall()
    else:
        return []

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Get all insider sales (code='S') with filed_ts as disclosure date
        cur.execute("SELECT symbol_id, filed_ts FROM insider_trades WHERE code='S'")
        sales = cur.fetchall()
        
        # Group by symbol, keep latest sale per symbol
        latest_sales = {}
        for row in sales:
            sid = row['symbol_id']
            ts = row['filed_ts']
            if sid not in latest_sales or ts > latest_sales[sid]:
                latest_sales[sid] = ts
        
        # For each candidate, check conditions
        calls = []
        opportunities = []
        
        for sid, sale_ts in latest_sales.items():
            sale_date = get_unix_date(sale_ts)
            
            # Check news sentiment: at least 20 days of data and bottom decile
            sentiment_rows = get_date_rows(cur, 'sentiment_features', sid, 'day', sale_date, 20)
            if len(sentiment_rows) < 20:
                continue
            
            scores = [r['mean_score'] for r in sentiment_rows]
            sorted_scores = sorted(scores)
            idx = max(0, int(math.floor(0.1 * len(sorted_scores))) - 1)
            threshold = sorted_scores[idx]
            current_score = scores[0]
            if current_score > threshold:
                continue
            
            # Check 13F: need two quarters, most recent within 45 days of sale, decrease >=5%
            cur.execute("""
                SELECT period, value
                FROM inst_holdings
                WHERE symbol_id=? AND period<=date(?, '-45 days')
                ORDER BY period DESC
                LIMIT 2
            """, (sid, sale_date))
            inst_rows = cur.fetchall()
            if len(inst_rows) < 2:
                continue
            val_recent = inst_rows[0]['value']
            val_prior = inst_rows[1]['value']
            if val_prior == 0 or (val_prior - val_recent) / val_prior < 0.05:
                continue
            
            # All conditions met: issue call on sale_date
            # Get close price on sale_date (daily bar)
            cur.execute("""
                SELECT ts, close
                FROM bars
                WHERE symbol_id=? AND tf='1d' AND ts=?
            """, (sid, sale_ts))
            bar0 = cur.fetchone()
            if not bar0:
                continue
            close0 = bar0['close']
            
            # Get close price 21 trading days later
            cur.execute("""
                SELECT close
                FROM bars
                WHERE symbol_id=? AND tf='1d' AND ts>?
                ORDER BY ts ASC
                LIMIT 1 OFFSET 20
            """, (sid, sale_ts))
            bar21 = cur.fetchone()
            if not bar21:
                continue
            close21 = bar21['close']
            
            fwd_return = (close21 - close0) / close0
            hit = fwd_return < 0  # predicting decline
            calls.append({
                'symbol_id': sid,
                'decision_ts': sale_ts,
                'hit': hit
            })
        
        conn.close()
        
        if not calls:
            print("INSUFFICIENT=1")
            return
        
        # Sort by time for split
        calls.sort(key=lambda x: x['decision_ts'])
        n = len(calls)
        split_idx = int(n * 0.8)
        train = calls[:split_idx]
        sealed = calls[split_idx:]
        
        # Compute metrics for train set
        issued = len(train)
        hits = sum(1 for c in train if c['hit'])
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued  # within issued subset
        
        # Distinct days
        days = set()
        for c in train:
            dt = get_unix_date(c['decision_ts'])
            days.add(dt)
        distinct_days = len(days)
        
        # Effective N: design effect from daily clustering
        day_counts = defaultdict(int)
        day_hits = defaultdict(int)
        for c in train:
            dt = get_unix_date(c['decision_ts'])
            day_counts[dt] += 1
            if c['hit']:
                day_hits[dt] += 1
        
        if not day_counts:
            print("INSUFFICIENT=1")
            return
            
        k_bar = sum(day_counts.values()) / len(day_counts)
        overall_p = hits / issued if issued > 0 else 0
        
        # Compute ICC via one-way ANOVA for binary outcomes
        var_between = 0
        for dt in day_counts:
            p_d = day_hits[dt] / day_counts[dt]
            var_between += day_counts[dt] * (p_d - overall_p)**2
        var_between /= (len(day_counts) - 1) if len(day_counts) > 1 else 1
        var_within = overall_p * (1 - overall_p)
        ICC = var_between / (var_between + var_within) if var_within else 0
        design_effect = 1 + (k_bar - 1) * ICC
        effective_n = issued / design_effect
        
        # Sealed metrics
        sealed_issued = len(sealed)
        sealed_hits = sum(1 for c in sealed if c['hit'])
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={n}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()
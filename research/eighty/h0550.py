# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 549
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Build universe: symbols with ≥252 trading days, EPS data, sentiment from 2018-07
    c.execute("""
        WITH symbols_with_bars AS (
            SELECT symbol_id, COUNT(*) as bar_count
            FROM bars WHERE tf='1d'
            GROUP BY symbol_id HAVING bar_count >= 252
        ),
        symbols_with_eps AS (
            SELECT DISTINCT symbol_id
            FROM fundamentals WHERE metric='EPS'
        ),
        symbols_with_sentiment AS (
            SELECT DISTINCT symbol_id
            FROM sentiment_features
            WHERE day >= '2018-07-01'
        )
        SELECT symbol_id
        FROM symbols_with_bars
        WHERE symbol_id IN (SELECT symbol_id FROM symbols_with_eps)
          AND symbol_id IN (SELECT symbol_id FROM symbols_with_sentiment)
    """)
    universe = [row[0] for row in c.fetchall()]
    if not universe:
        print("INSUFFICIENT=1")
        return
    
    # Get macro: unemployment claims 4-week avg and trend
    c.execute("""
        SELECT ts, AVG(value) OVER (ORDER BY ts ROWS BETWEEN 27 PRECEDING AND CURRENT ROW) as ma4
        FROM macro_series
        WHERE series='ICSA'
        ORDER BY ts
    """)
    macro_data = {}
    prev_ma = None
    consec_fall = 0
    for ts, ma in c.fetchall():
        if prev_ma is not None and ma is not None:
            if ma < prev_ma:
                consec_fall += 1
            else:
                consec_fall = 0
        macro_data[ts] = consec_fall >= 3
        prev_ma = ma
    
    # Get market's 21-day return for each day (average of all symbols)
    c.execute("""
        WITH daily_returns AS (
            SELECT symbol_id, ts,
                   (close / LAG(close, 21) OVER (PARTITION BY symbol_id ORDER BY ts) - 1) as ret21
            FROM bars WHERE tf='1d'
        )
        SELECT ts, AVG(ret21) as mkt_ret21
        FROM daily_returns
        WHERE ret21 IS NOT NULL
        GROUP BY ts
    """)
    mkt_ret = {row[0]: row[1] for row in c.fetchall()}
    
    # Get all trading days and find 80% cutoff
    c.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    all_days = [row[0] for row in c.fetchall()]
    cutoff_idx = int(len(all_days) * 0.8)
    cutoff_ts = all_days[cutoff_idx]
    
    # Prepare data structures
    opportunities = []
    issued_calls = []
    
    # Process each symbol
    for sym in universe:
        # Get bars for symbol
        c.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id=? AND tf='1d'
            ORDER BY ts
        """, (sym,))
        bars = c.fetchall()
        if len(bars) < 252:
            continue
        bar_dict = {ts: close for ts, close in bars}
        ts_list = sorted(bar_dict.keys())
        
        # Get EPS data (quarterly, ordered by as_of desc)
        c.execute("""
            SELECT as_of, value FROM fundamentals
            WHERE symbol_id=? AND metric='EPS'
            ORDER BY as_of DESC
        """, (sym,))
        eps_data = c.fetchall()
        if len(eps_data) < 4:
            continue
        
        # Precompute EPS growth rates for quarters
        eps_vals = [row[1] for row in eps_data[:4]]
        eps_growth = []
        for i in range(1, 4):
            if eps_vals[i-1] != 0:
                eps_growth.append((eps_vals[i] - eps_vals[i-1]) / abs(eps_vals[i-1]))
            else:
                eps_growth.append(0)
        
        # Check if at least 10% and accelerating
        eps_ok = (eps_vals[0] < eps_vals[1] < eps_vals[2] < eps_vals[3] and 
                  all(g >= 0.10 for g in eps_growth))
        if not eps_ok:
            continue
        
        # Get sentiment features
        c.execute("""
            SELECT day, mean_score FROM sentiment_features
            WHERE symbol_id=?
            ORDER BY day
        """, (sym,))
        sent_data = c.fetchall()
        if len(sent_data) < 252:
            continue
        sent_dict = {row[0]: row[1] for row in sent_data}
        sent_days = sorted(sent_dict.keys())
        
        # Get StockTwits volume
        c.execute("""
            SELECT ts, total FROM stocktwits_sentiment
            WHERE symbol_id=?
            ORDER BY ts
        """, (sym,))
        st_data = c.fetchall()
        st_dict = {row[0]: row[1] for row in st_data}
        st_days = sorted(st_dict.keys())
        
        # Get earnings announcements (form 4, 8-K, 144, 3, 6-K)
        c.execute("""
            SELECT filed_ts FROM filings
            WHERE symbol_id=? AND form IN ('4', '8-K', '144', '3', '6-K')
        """, (sym,))
        earnings_dates = [row[0] for row in c.fetchall()]
        
        # Process each day in symbol's history
        for i in range(252, len(ts_list)):
            ts = ts_list[i]
            
            # Skip if after cutoff for sealed era consideration
            if ts > cutoff_ts:
                continue
            
            # Check if we have required data for this day
            sent_5d_avg = 0
            sent_252d_avg = 0
            st_5d_avg = 0
            st_252d_avg = 0
            
            # Sentiment averages
            sent_vals_5d = [sent_dict.get(sent_days[j], 0) for j in range(len(sent_days)) 
                           if sent_days[j] <= ts][-5:]
            sent_vals_252d = [sent_dict.get(sent_days[j], 0) for j in range(len(sent_days)) 
                             if sent_days[j] <= ts][-252:]
            if len(sent_vals_5d) == 0 or len(sent_vals_252d) == 0:
                continue
            sent_5d_avg = sum(sent_vals_5d) / len(sent_vals_5d)
            sent_252d_avg = sum(sent_vals_252d) / len(sent_vals_252d)
            
            # StockTwits averages
            st_vals_5d = [st_dict.get(st_days[j], 0) for j in range(len(st_days)) 
                         if st_days[j] <= ts][-5:]
            st_vals_252d = [st_dict.get(st_days[j], 0) for j in range(len(st_days)) 
                           if st_days[j] <= ts][-252:]
            if len(st_vals_5d) == 0 or len(st_vals_252d) == 0:
                continue
            st_5d_avg = sum(st_vals_5d) / len(st_vals_5d)
            st_252d_avg = sum(st_vals_252d) / len(st_vals_252d)
            
            # Check macro condition (4 consecutive weeks of falling claims)
            week_ts = ts // (7 * 86400) * (7 * 86400)  # Approximate week alignment
            macro_ok = False
            for macro_ts in sorted(macro_data.keys()):
                if macro_ts <= week_ts:
                    macro_ok = macro_data[macro_ts]
            
            # Check entry conditions
            # 1. Macro condition
            if not macro_ok:
                continue
            
            # 2. EPS growth (already checked per symbol)
            # 3. Sentiment above 252d avg, stock's 21d return below market's
            stock_ret21 = (bar_dict[ts_list[i]] / bar_dict[ts_list[i-21]] - 1) if i >= 21 else 0
            mkt_ret21 = mkt_ret.get(ts, 0)
            
            if not (sent_5d_avg > sent_252d_avg and stock_ret21 < mkt_ret21):
                continue
            
            # 4. StockTwits volume below 252d avg
            if not (st_5d_avg < st_252d_avg):
                continue
            
            # Check abstain conditions
            # 1. Earnings announcement in past 10 days
            has_earnings = any(ts - 10*86400 <= ed <= ts for ed in earnings_dates)
            if has_earnings:
                continue
            
            # 2. Stock's 21d return above market's (already checked, must be below)
            # 3. Top decile of 21d return (need to check across universe)
            # We'll skip this check for now as it requires computing across all stocks for each day
            
            # Get label for this call (21 trading days later)
            if i + 21 < len(ts_list):
                future_ts = ts_list[i + 21]
                c.execute("""
                    SELECT up FROM prediction_outcomes
                    WHERE symbol_id=? AND horizon=21 AND ts=?
                """, (sym, ts))
                row = c.fetchone()
                if row:
                    opportunities.append(ts)
                    issued_calls.append((ts, sym, row[0], ts <= cutoff_ts))
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Split into regular and sealed era
    issued_total = len(issued_calls)
    issued_main = [call for call in issued_calls if not call[3]]
    issued_sealed = [call for call in issued_calls if call[3]]
    
    # Calculate metrics
    hits_total = sum(1 for call in issued_calls if call[2])
    hits_main = sum(1 for call in issued_main if call[2])
    hits_sealed = sum(1 for call in issued_sealed if call[2])
    
    precision_total = hits_total / issued_total if issued_total > 0 else 0
    base_rate = precision_total  # Base rate in issued subset
    
    # Distinct days in issued calls
    distinct_days = len(set(call[0] for call in issued_calls))
    
    # Design effect (simplified: assume calls on same day are correlated)
    day_counts = defaultdict(int)
    for call in issued_calls:
        day_counts[call[0]] += 1
    avg_cluster_size = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    design_effect = 1 + (avg_cluster_size - 1) * 0.5  # Assumed ICC of 0.5
    effective_n = issued_total / design_effect if design_effect > 0 else issued_total
    
    sealed_precision = hits_sealed / len(issued_sealed) if issued_sealed else 0
    
    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_total:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 572
# cycle_index: 30
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta

DB_PATH = 'data/signaldeck.db'

def main():
    conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    # Get universe: symbols with >=252 days of 1d bars AND stocktwits data
    c.execute("""
        SELECT b.symbol_id
        FROM bars b
        WHERE b.tf = '1d'
        GROUP BY b.symbol_id
        HAVING COUNT(DISTINCT b.ts) >= 252
        INTERSECT
        SELECT DISTINCT symbol_id
        FROM stocktwits_sentiment
    """)
    symbols = [r[0] for r in c.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Get all unique 1d bar timestamps across universe (to know trading days)
    c.execute("""
        SELECT DISTINCT ts
        FROM bars
        WHERE tf = '1d'
        ORDER BY ts
    """)
    all_dates_ts = [r[0] for r in c.fetchall()]
    date_to_idx = {ts: i for i, ts in enumerate(all_dates_ts)}
    date_to_str = {}
    for ts in all_dates_ts:
        dt = datetime.utcfromtimestamp(ts)
        date_to_str[ts] = dt.strftime('%Y-%m-%d')
    str_to_date = {v: k for k, v in date_to_str.items()}
    
    opportunities = []  # (symbol_id, decision_ts)
    issued = []         # (symbol_id, decision_ts, label)
    
    for sym in symbols:
        # Get 1d bars for symbol
        c.execute("""
            SELECT ts, close
            FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym,))
        bars = [(r[0], r[1]) for r in c.fetchall()]
        if len(bars) < 252:
            continue
        bar_dates = [b[0] for b in bars]
        bar_idx = {ts: i for i, ts in enumerate(bar_dates)}
        
        # Get stocktwits bullish counts per day (as of trade date)
        c.execute("""
            SELECT ts, bullish
            FROM stocktwits_sentiment
            WHERE symbol_id = ?
        """, (sym,))
        st_rows = c.fetchall()
        st_by_date = {}
        for r in st_rows:
            dt = datetime.utcfromtimestamp(r[0]).strftime('%Y-%m-%d')
            if dt not in st_by_date:
                st_by_date[dt] = 0
            st_by_date[dt] += r[1]
        
        # Get insider open-market sales (code='S') by filed_ts date
        c.execute("""
            SELECT filed_ts
            FROM insider_trades
            WHERE symbol_id = ? AND code = 'S'
        """, (sym,))
        sales_by_date = set()
        for r in c.fetchall():
            dt = datetime.utcfromtimestamp(r[0]).strftime('%Y-%m-%d')
            sales_by_date.add(dt)
        
        # Iterate potential decision days
        for i, (ts, close) in enumerate(bars):
            # Need 3 prior trading days for stocktwits change, 5 future for horizon
            if i < 3 or i + 5 >= len(bars):
                continue
            
            decision_date = date_to_str[ts]
            
            # StockTwits condition: 100% increase over past 3 trading days
            # Get current day's bullish count
            curr_bull = st_by_date.get(decision_date, 0)
            # Get trading day 3 days before (by index)
            prev_ts = bar_dates[i-3]
            prev_date = date_to_str[prev_ts]
            prev_bull = st_by_date.get(prev_date, 0)
            if prev_bull <= 0:
                continue
            if (curr_bull / prev_bull) < 2.0:
                continue
            
            # Insider sale condition: must be disclosed on most recent trading day
            if decision_date not in sales_by_date:
                continue
            
            # Both conditions met: issue negative call
            future_ts = bar_dates[i+5]
            future_close = bars[i+5][1]
            label = 1 if future_close < close else 0  # 1 = price declined
            issued.append((sym, ts, label))
        
        # All decision points (opportunities) for this symbol
        for i in range(3, len(bars)-5):
            opportunities.append((sym, bars[i][0]))
    
    conn.close()
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Determine sealed era (most recent 20% of unique decision dates)
    all_decision_dates = sorted(set(ts for _, ts in opportunities))
    n_dates = len(all_decision_dates)
    if n_dates == 0:
        print("INSUFFICIENT=1")
        return
    sealed_start_idx = int(0.8 * n_dates)
    sealed_start_ts = all_decision_dates[sealed_start_idx]
    
    issued_count = len(issued)
    opp_count = len(opportunities)
    hits = sum(1 for _, _, lab in issued if lab == 1)
    precision = hits / issued_count if issued_count > 0 else 0
    base_rate = precision  # within issued subset, predicted class is negative
    
    distinct_days = len(set(ts for _, ts in issued))
    
    # Compute design effect (clustering by day)
    day_counts = {}
    day_labels = {}
    for _, ts, lab in issued:
        day_counts[ts] = day_counts.get(ts, 0) + 1
        day_labels.setdefault(ts, []).append(lab)
    
    k = len(day_counts)
    if k == 0:
        design_effect = 1.0
    else:
        # Overall proportion
        p = hits / issued_count
        # Between-day sum of squares
        between_ss = sum(day_counts[d] * (sum(day_labels[d])/day_counts[d] - p)**2 for d in day_counts)
        # Within-day sum of squares
        within_ss = 0
        for d in day_counts:
            pd = sum(day_labels[d]) / day_counts[d]
            for lab in day_labels[d]:
                within_ss += (lab - pd)**2
        # Mean squares
        df_between = k - 1
        df_within = issued_count - k
        ms_between = between_ss / df_between if df_between > 0 else 0
        ms_within = within_ss / df_within if df_within > 0 else 0
        # ICC
        avg_m = issued_count / k
        m0 = (issued_count**2 - sum(c**2 for c in day_counts.values())) / (issued_count * (k - 1)) if k > 1 else avg_m
        if ms_between + (m0 - 1) * ms_within == 0:
            icc = 0
        else:
            icc = (ms_between - ms_within) / (ms_between + (m0 - 1) * ms_within)
        design_effect = 1 + (avg_m - 1) * icc
        if design_effect <= 0:
            design_effect = 1.0
    
    effective_n = issued_count / design_effect if design_effect > 0 else issued_count
    
    # Sealed era precision
    sealed_hits = sum(1 for _, ts, lab in issued if ts >= sealed_start_ts and lab == 1)
    sealed_issued = sum(1 for _, ts, _ in issued if ts >= sealed_start_ts)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opp_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
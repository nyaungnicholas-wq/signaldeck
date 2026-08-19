# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 828
# cycle_index: 24
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get all CEO/CFO open-market purchases with disclosure delay <= 2 days
    cur.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value,
               tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
          AND filed_ts - tx_ts <= 172800
          AND filed_ts > tx_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # 2. Get all trading days per symbol from bars (tf='1d')
    cur.execute("""
        SELECT symbol_id, ts
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_rows = cur.fetchall()
    
    # Build trading day lookup: symbol_id -> sorted list of (ts, date_str, close)
    # Also need close prices for forward returns
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_data = cur.fetchall()
    
    trading_days = {}  # symbol_id -> list of (ts, date_str, close)
    for row in bars_data:
        sid = row['symbol_id']
        ts = row['ts']
        close = row['close']
        date_str = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        trading_days.setdefault(sid, []).append((ts, date_str, close))

    if not trading_days:
        print("INSUFFICIENT=1")
        return 0

    # 3. Get news counts per symbol per date
    # Only need dates that appear in trades (trade date and filed date)
    trade_dates = set()
    for t in trades:
        trade_dates.add(datetime.utcfromtimestamp(t['tx_ts']).strftime('%Y-%m-%d'))
        trade_dates.add(datetime.utcfromtimestamp(t['filed_ts']).strftime('%Y-%m-%d'))
    
    if not trade_dates:
        print("INSUFFICIENT=1")
        return 0

    # Query news for relevant symbols and dates
    symbols_in_trades = list(set(t['symbol_id'] for t in trades))
    placeholders = ','.join('?' * len(symbols_in_trades))
    date_placeholders = ','.join('?' * len(trade_dates))
    
    cur.execute(f"""
        SELECT symbol_id, date(ts, 'unixepoch') as news_date, COUNT(*) as cnt
        FROM news
        WHERE symbol_id IN ({placeholders})
          AND date(ts, 'unixepoch') IN ({date_placeholders})
        GROUP BY symbol_id, date(ts, 'unixepoch')
    """, symbols_in_trades + list(trade_dates))
    
    news_counts = {}
    for row in cur.fetchall():
        news_counts[(row['symbol_id'], row['news_date'])] = row['cnt']

    # 4. For each trade, check entry conditions
    calls = []  # (decision_ts, symbol_id, trade_date, filed_date, forward_return, label)
    
    # Pre-build a set of qualifying trades per symbol for "prior 21 sessions" check
    # A qualifying trade is one that passes news checks and delay check
    # But we need to check this iteratively or pre-filter
    
    # First, filter trades that pass news and delay checks
    candidate_trades = []
    for t in trades:
        trade_date = datetime.utcfromtimestamp(t['tx_ts']).strftime('%Y-%m-%d')
        filed_date = datetime.utcfromtimestamp(t['filed_ts']).strftime('%Y-%m-%d')
        
        if news_counts.get((t['symbol_id'], trade_date), 0) > 0:
            continue
        if news_counts.get((t['symbol_id'], filed_date), 0) > 0:
            continue
        
        candidate_trades.append({
            'accession': t['accession'],
            'symbol_id': t['symbol_id'],
            'tx_ts': t['tx_ts'],
            'filed_ts': t['filed_ts'],
            'trade_date': trade_date,
            'filed_date': filed_date
        })
    
    if not candidate_trades:
        print("INSUFFICIENT=1")
        return 0

    # Sort by filed_ts (decision time)
    candidate_trades.sort(key=lambda x: x['filed_ts'])

    # For each candidate, check "no qualifying in prior 21 sessions"
    # We need trading day index for each symbol
    trading_day_index = {}
    for sid, days in trading_days.items():
        trading_day_index[sid] = {ts: i for i, (ts, _, _) in enumerate(days)}

    issued_calls = []
    for ct in candidate_trades:
        sid = ct['symbol_id']
        filed_ts = ct['filed_ts']
        
        # Find the trading day index for filed_ts (or nearest prior)
        if sid not in trading_day_index:
            continue
        td_idx_map = trading_day_index[sid]
        td_list = trading_days[sid]
        
        # Find index of filed_ts or nearest prior trading day
        idx = None
        for i, (ts, _, _) in enumerate(td_list):
            if ts <= filed_ts:
                idx = i
            else:
                break
        if idx is None:
            continue  # no trading day on or before filed_ts
        
        # Check prior 21 trading days for other qualifying trades
        # A qualifying trade is one in candidate_trades for same symbol
        # with filed_ts in those prior 21 trading days
        has_prior = False
        for other in candidate_trades:
            if other['symbol_id'] != sid:
                continue
            if other['filed_ts'] >= filed_ts:
                continue
            # Check if other's filed_ts falls within prior 21 trading days
            other_idx = None
            for i, (ts, _, _) in enumerate(td_list):
                if ts <= other['filed_ts']:
                    other_idx = i
                else:
                    break
            if other_idx is not None and (idx - other_idx) <= 21:
                has_prior = True
                break
        
        # ABSTAIN if "No qualifying officer purchase in the prior 21 sessions"
        # i.e., abstain if has_prior is FALSE
        if not has_prior:
            continue
        
        # Now compute 21-day forward return from filed_ts
        # Need close at idx and close at idx + 21
        if idx + 21 >= len(td_list):
            continue  # not enough future data
        
        close_now = td_list[idx][2]
        close_future = td_list[idx + 21][2]
        if close_now <= 0:
            continue
        
        fwd_return = (close_future - close_now) / close_now
        label = 1 if fwd_return > 0 else 0
        
        issued_calls.append({
            'decision_ts': filed_ts,
            'symbol_id': sid,
            'fwd_return': fwd_return,
            'label': label
        })

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # 5. Sort by decision_ts and split: most recent 20% = sealed
    issued_calls.sort(key=lambda x: x['decision_ts'])
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    n_main = n_total - n_sealed
    
    main_calls = issued_calls[:n_main]
    sealed_calls = issued_calls[n_main:]

    # 6. Compute metrics
    # ISSUED = total calls
    # OPPORTUNITIES = candidate_trades considered (decision points)
    # But "opportunities considered" = all candidate trades that passed news/delay?
    # The spec says "count of decision points considered"
    # Each candidate trade is a decision point
    opportunities = len(candidate_trades)
    
    issued = len(issued_calls)
    hits_main = sum(c['label'] for c in main_calls)
    hits_sealed = sum(c['label'] for c in sealed_calls)
    
    precision_main = hits_main / len(main_calls) if main_calls else 0.0
    precision_sealed = hits_sealed / len(sealed_calls) if sealed_calls else 0.0
    
    # Base rate within issued subset
    base_rate = sum(c['label'] for c in issued_calls) / issued if issued else 0.0
    
    # Distinct days among issued calls
    distinct_days = len(set(datetime.utcfromtimestamp(c['decision_ts']).strftime('%Y-%m-%d') for c in issued_calls))
    
    # Effective N: issued / design_effect
    # Design effect > 1 due to clustering. Estimate via intra-cluster correlation.
    # Simple approach: group by symbol and date, compute design effect
    # For simplicity, use Kish's effective sample size: n / (1 + (n-1)*rho)
    # But we need rho. Alternative: effective_n = number of independent clusters
    # Cluster by (symbol, month) or similar.
    # Let's cluster by symbol and week of decision.
    clusters = {}
    for c in issued_calls:
        dt = datetime.utcfromtimestamp(c['decision_ts'])
        week_key = (c['symbol_id'], dt.year, dt.isocalendar()[1])
        clusters.setdefault(week_key, 0)
        clusters[week_key] += 1
    
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Without ICC, use conservative: effective_n = number of clusters
    # But must be < issued. Use min(clusters, issued - 1) or similar.
    n_clusters = len(clusters)
    # Conservative: effective_n = n_clusters (each cluster is one independent obs)
    # But ensure < issued
    effective_n = min(n_clusters, issued - 1) if issued > 1 else 0
    if effective_n >= issued:
        effective_n = issued - 1 if issued > 1 else 0

    # 7. Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_main:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={precision_sealed:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
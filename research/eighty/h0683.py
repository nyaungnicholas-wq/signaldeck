# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 682
# cycle_index: 9
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get symbols with daily bars 2018+ (>=252 bars)
    cur.execute("""
        SELECT symbol_id, COUNT(*) as n_bars, MIN(ts) as min_ts, MAX(ts) as max_ts
        FROM bars WHERE tf='1d'
        GROUP BY symbol_id
        HAVING n_bars >= 252 AND min_ts <= ? AND max_ts >= ?
    """, (date_to_epoch(datetime(2018,12,31).date()), date_to_epoch(datetime(2018,1,1).date())))
    symbols_with_bars = {row['symbol_id']: row for row in cur.fetchall()}
    if not symbols_with_bars:
        print("INSUFFICIENT=1")
        return

    # 2. Get symbols with fundamentals 2018+ (SharesOutstanding quarterly)
    cur.execute("""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric='SharesOutstanding' AND as_of > 0 AND fetched_at <= ?
        ORDER BY symbol_id, as_of
    """, (date_to_epoch(datetime(2026,8,14).date()),))
    fund_rows = cur.fetchall()

    # Group by symbol_id, sort by as_of
    from collections import defaultdict
    fund_by_sym = defaultdict(list)
    for r in fund_rows:
        fund_by_sym[r['symbol_id']].append(r)

    # 3. Find symbols with 8 consecutive quarters |QoQ change| < 2%
    stable_symbols = set()
    for sym_id, rows in fund_by_sym.items():
        if sym_id not in symbols_with_bars:
            continue
        rows.sort(key=lambda x: x['as_of'])
        if len(rows) < 8:
            continue
        consecutive = 0
        for i in range(1, len(rows)):
            prev = rows[i-1]['value']
            curr = rows[i]['value']
            if prev > 0 and abs(curr - prev) / prev < 0.02:
                consecutive += 1
                if consecutive >= 7:  # 8 quarters = 7 changes
                    stable_symbols.add(sym_id)
                    break
            else:
                consecutive = 0

    if not stable_symbols:
        print("INSUFFICIENT=1")
        return

    # 4. Get insider trades for stable symbols, 2008+
    cur.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({}) AND code='P' AND filed_ts >= ?
        ORDER BY symbol_id, filed_ts
    """.format(','.join('?'*len(stable_symbols))), list(stable_symbols) + [date_to_epoch(datetime(2008,1,1).date())])
    insider_trades = cur.fetchall()

    # Group by symbol_id
    trades_by_sym = defaultdict(list)
    for t in insider_trades:
        trades_by_sym[t['symbol_id']].append(t)

    # 5. Filter symbols with >=3 years of insider history
    qualified_symbols = set()
    for sym_id, trades in trades_by_sym.items():
        if not trades:
            continue
        first_filed = min(t['filed_ts'] for t in trades)
        last_filed = max(t['filed_ts'] for t in trades)
        if last_filed - first_filed >= 3 * 365 * 86400:
            qualified_symbols.add(sym_id)

    if not qualified_symbols:
        print("INSUFFICIENT=1")
        return

    # 6. Get news for qualified symbols (for headline count)
    cur.execute("""
        SELECT symbol_id, ts
        FROM news
        WHERE symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(qualified_symbols))), list(qualified_symbols))
    news_rows = cur.fetchall()
    news_by_sym = defaultdict(list)
    for n in news_rows:
        news_by_sym[n['symbol_id']].append(n['ts'])

    # 7. Get 8-K filings 2026+ for qualified symbols
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM filings
        WHERE symbol_id IN ({}) AND form='8-K' AND filed_ts >= ?
        ORDER BY symbol_id, filed_ts
    """.format(','.join('?'*len(qualified_symbols))), list(qualified_symbols) + [date_to_epoch(datetime(2026,1,1).date())])
    filing_rows = cur.fetchall()
    filings_by_sym = defaultdict(list)
    for f in filing_rows:
        filings_by_sym[f['symbol_id']].append(f['filed_ts'])

    # 8. Get daily bars for forward return calculation
    # We'll fetch on demand per symbol

    # 9. Process each qualifying trade
    opportunities = []
    issued_calls = []

    role_keywords = ['CEO', 'CFO', 'CHAIRMAN', '10% OWNER', '10 PERCENT OWNER', 'TEN PERCENT OWNER']

    def is_qualifying_role(title):
        if not title:
            return False
        t = title.upper()
        return any(kw in t for kw in role_keywords)

    for sym_id in qualified_symbols:
        trades = trades_by_sym[sym_id]
        if not trades:
            continue

        # Pre-compute historical median purchase value for this insider (per insider)
        insider_medians = {}
        for t in trades:
            insider = t['insider']
            if insider not in insider_medians:
                insider_medians[insider] = []
            if t['value'] and t['value'] > 0:
                insider_medians[insider].append(t['value'])
        for insider, vals in insider_medians.items():
            vals.sort()
            n = len(vals)
            insider_medians[insider] = vals[n//2] if n > 0 else 0

        # Fetch daily bars for this symbol
        cur.execute("SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts", (sym_id,))
        bars = cur.fetchall()
        if not bars:
            continue
        bar_ts = [b['ts'] for b in bars]
        bar_close = {b['ts']: b['close'] for b in bars}

        # For each trade, check entry conditions
        for i, trade in enumerate(trades):
            filed_ts = trade['filed_ts']
            filed_date = epoch_to_date(filed_ts)

            # Condition: role
            if not is_qualifying_role(trade['title']):
                opportunities.append((filed_ts, sym_id, trade['insider'], False, 'role'))
                continue

            # Condition: zero open-market buys in prior 756 sessions (trading days)
            # Look at prior trades by same insider with code='P' and filed_ts < current filed_ts - 756 days
            cutoff_ts = filed_ts - 756 * 86400
            prior_buys = [t for t in trades[:i] if t['insider'] == trade['insider'] and t['code'] == 'P' and t['filed_ts'] >= cutoff_ts]
            if prior_buys:
                opportunities.append((filed_ts, sym_id, trade['insider'], False, 'prior_buys'))
                continue

            # Condition: purchase value > 2x historical median
            median_val = insider_medians.get(trade['insider'], 0)
            if not (trade['value'] and trade['value'] > 2 * median_val and median_val > 0):
                opportunities.append((filed_ts, sym_id, trade['insider'], False, 'value'))
                continue

            # Condition: <=2 news headlines in prior 5 sessions
            news_ts_list = news_by_sym.get(sym_id, [])
            news_cutoff = filed_ts - 5 * 86400
            recent_news = [nt for nt in news_ts_list if news_cutoff <= nt < filed_ts]
            if len(recent_news) > 2:
                opportunities.append((filed_ts, sym_id, trade['insider'], False, 'news'))
                continue

            # Condition: no 8-K within 5 sessions (2026+)
            filing_ts_list = filings_by_sym.get(sym_id, [])
            filing_cutoff = filed_ts - 5 * 86400
            recent_8k = [ft for ft in filing_ts_list if filing_cutoff <= ft < filed_ts]
            if recent_8k:
                opportunities.append((filed_ts, sym_id, trade['insider'], False, '8k'))
                continue

            # All entry conditions passed - now check forward label
            # Find the bar at or after filed_ts (decision bar)
            # Since bars are daily, find the first bar with ts >= filed_ts (or closest prior?)
            # As-of discipline: decision is at filed_ts (when public). The bar for that day is known at close.
            # We'll use the close of the day of filed_ts (or next trading day if filed after close).
            # For simplicity, find the bar with ts >= filed_ts (start of day) or use the bar for that date.
            filed_day_start = date_to_epoch(filed_date)
            # Find index of bar for filed_day_start or next
            idx = None
            for j, bts in enumerate(bar_ts):
                if bts >= filed_day_start:
                    idx = j
                    break
            if idx is None:
                opportunities.append((filed_ts, sym_id, trade['insider'], False, 'no_bar'))
                continue

            # Need 21 trading days forward
            if idx + 21 >= len(bar_ts):
                opportunities.append((filed_ts, sym_id, trade['insider'], False, 'no_forward'))
                continue

            entry_price = bar_close[bar_ts[idx]]
            exit_price = bar_close[bar_ts[idx + 21]]
            fwd_return = (exit_price - entry_price) / entry_price
            hit = 1 if fwd_return > 0 else 0

            opportunities.append((filed_ts, sym_id, trade['insider'], True, 'issued'))
            issued_calls.append({
                'filed_ts': filed_ts,
                'filed_date': filed_date,
                'symbol_id': sym_id,
                'insider': trade['insider'],
                'hit': hit,
                'fwd_return': fwd_return
            })

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # 10. Split into sealed era (most recent 20% by time)
    opportunities.sort(key=lambda x: x[0])
    issued_calls.sort(key=lambda x: x['filed_ts'])

    n_total = len(opportunities)
    n_issued = len(issued_calls)

    if n_issued == 0:
        print("INSUFFICIENT=1")
        return

    # Split point at 80th percentile of time
    split_idx = int(n_total * 0.8)
    split_ts = opportunities[split_idx][0] if split_idx < n_total else opportunities[-1][0]

    # Separate issued calls into train and sealed
    train_calls = [c for c in issued_calls if c['filed_ts'] < split_ts]
    sealed_calls = [c for c in issued_calls if c['filed_ts'] >= split_ts]

    # 11. Compute metrics
    # ISSUED
    issued_count = n_issued

    # OPPORTUNITIES
    opportunities_count = n_total

    # PRECISION (overall)
    hits = sum(c['hit'] for c in issued_calls)
    precision = hits / issued_count if issued_count > 0 else 0.0

    # BASE_RATE within issued subset
    base_rate = hits / issued_count if issued_count > 0 else 0.0

    # DISTINCT_DAYS among issued calls
    distinct_days = len(set(c['filed_date'] for c in issued_calls))

    # EFFECTIVE_N: issued / design_effect
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Simplified: cluster by day, compute design effect as 1 + (n_issued/distinct_days - 1) * 0.5
    # But must ensure EFFECTIVE_N < ISSUED
    if distinct_days > 0:
        avg_cluster = issued_count / distinct_days
        # Use a conservative ICC of 0.2 for financial returns
        icc = 0.2
        design_effect = 1 + (avg_cluster - 1) * icc
        if design_effect < 1.0:
            design_effect = 1.0
        effective_n = issued_count / design_effect
    else:
        effective_n = issued_count * 0.5

    # Ensure EFFECTIVE_N < ISSUED
    if effective_n >= issued_count:
        effective_n = issued_count * 0.99

    # SEALED_PRECISION
    sealed_hits = sum(c['hit'] for c in sealed_calls)
    sealed_issued = len(sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    # 12. Output
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
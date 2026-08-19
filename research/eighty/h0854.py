# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 853
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def is_officer(title):
    if not title:
        return False
    t = title.upper()
    return 'CEO' in t or 'CFO' in t

def get_professional_sources():
    return {'reuters', 'bloomberg', 'wsj', 'financial times', 'ft.com', 'marketwatch',
            'cnbc', 'yahoo finance', 'seeking alpha', 'benzinga', 'thestreet',
            'investopedia', 'morningstar', 'zacks', 'briefing.com', 'dow jones',
            'associated press', 'ap news', 'business wire', 'pr newswire',
            'globe newswire', 'accesswire', 'ein presswire', 'newsfile'}

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get symbols with daily bars from 2018-07+
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id
        WHERE b.tf = '1d' AND b.ts >= strftime('%s', '2018-07-01')
    """)
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return 0
    symbol_ids = list(symbols.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # 2. Get officer open-market purchases (code='P')
    cur.execute(f"""
        SELECT it.symbol_id, it.accession, it.insider, it.title, it.code,
               it.shares, it.price, it.value, it.tx_ts, it.filed_ts
        FROM insider_trades it
        WHERE it.symbol_id IN ({placeholders})
          AND it.code = 'P'
    """, symbol_ids)
    trades = [dict(row) for row in cur.fetchall()]
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # Filter to officers
    officer_trades = [t for t in trades if is_officer(t['title'])]
    if not officer_trades:
        print("INSUFFICIENT=1")
        return 0

    # 3. Get news for these symbols (professional sources only)
    prof_sources = get_professional_sources()
    cur.execute(f"""
        SELECT n.symbol_id, n.ts, n.source
        FROM news n
        WHERE n.symbol_id IN ({placeholders})
    """, symbol_ids)
    news_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        src = (row['source'] or '').lower()
        if any(p in src for p in prof_sources):
            news_by_symbol[row['symbol_id']].append(row['ts'])
    for sym in news_by_symbol:
        news_by_symbol[sym].sort()

    # 4. Get fundamentals: SharesOutstanding and EPS
    cur.execute(f"""
        SELECT f.symbol_id, f.metric, f.value, f.as_of, f.fetched_at
        FROM fundamentals f
        WHERE f.symbol_id IN ({placeholders})
          AND f.metric IN ('SharesOutstanding', 'EPS')
          AND f.as_of != 0
    """, symbol_ids)
    fund_by_symbol = defaultdict(lambda: {'SharesOutstanding': [], 'EPS': []})
    for row in cur.fetchall():
        if row['fetched_at'] is None or row['as_of'] is None:
            continue
        fund_by_symbol[row['symbol_id']][row['metric']].append({
            'value': row['value'],
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at']
        })
    for sym in fund_by_symbol:
        for metric in fund_by_symbol[sym]:
            fund_by_symbol[sym][metric].sort(key=lambda x: x['as_of'])

    # 5. Get daily bars for label construction (21-day forward return)
    # We'll fetch on demand per symbol to avoid memory issues

    # 6. Process each officer trade as a decision point
    opportunities = []
    issued = []

    for trade in officer_trades:
        sym_id = trade['symbol_id']
        T = trade['filed_ts']  # decision date = disclosure date
        T_date = epoch_to_date(T)

        # Check disclosure delay <= 5 sessions (tx_ts to filed_ts)
        if trade['tx_ts'] and trade['filed_ts']:
            delay_days = (trade['filed_ts'] - trade['tx_ts']) / 86400
            if delay_days > 7:  # ~5 trading sessions
                continue

        # Check news: zero professional headlines on T and T-1..T-5 (6 days)
        news_ts_list = news_by_symbol.get(sym_id, [])
        has_news = False
        for nts in news_ts_list:
            n_date = epoch_to_date(nts)
            if T_date - timedelta(days=5) <= n_date <= T_date:
                has_news = True
                break
        if has_news:
            continue

        # Check fundamentals as-of T (fetched_at <= T)
        so_data = [d for d in fund_by_symbol[sym_id]['SharesOutstanding'] if d['fetched_at'] <= T]
        eps_data = [d for d in fund_by_symbol[sym_id]['EPS'] if d['fetched_at'] <= T]

        if len(so_data) < 4 or len(eps_data) < 5:  # need 3+ QoQ changes = 4 quarters; 3+ YoY accel = 5 quarters
            continue

        # Check SharesOutstanding declined QoQ for >=3 consecutive quarters ending before T
        so_declines = 0
        for i in range(1, len(so_data)):
            if so_data[i]['value'] < so_data[i-1]['value']:
                so_declines += 1
            else:
                so_declines = 0
            if so_declines >= 3:
                break
        if so_declines < 3:
            continue

        # Check EPS YoY growth accelerated for >=3 consecutive quarters ending before T
        # YoY growth for quarter i: (eps[i] - eps[i-4]) / abs(eps[i-4])
        # Acceleration: growth[i] > growth[i-1]
        if len(eps_data) < 5:
            continue
        yoy_growth = []
        for i in range(4, len(eps_data)):
            prev = eps_data[i-4]['value']
            curr = eps_data[i]['value']
            if prev != 0:
                yoy_growth.append((curr - prev) / abs(prev))
            else:
                yoy_growth.append(float('inf') if curr > 0 else 0)
        accel_count = 0
        for i in range(1, len(yoy_growth)):
            if yoy_growth[i] > yoy_growth[i-1]:
                accel_count += 1
            else:
                accel_count = 0
            if accel_count >= 3:
                break
        if accel_count < 3:
            continue

        # All entry conditions met - this is an opportunity
        opportunities.append((sym_id, T, T_date))

        # Build label: 21-day forward return from bars (tf='1d')
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            ORDER BY ts
            LIMIT 22
        """, (sym_id, T))
        bars = cur.fetchall()
        if len(bars) < 22:
            continue  # insufficient forward data
        entry_close = bars[0]['close']
        exit_close = bars[21]['close']
        fwd_return = (exit_close - entry_close) / entry_close
        label = 1 if fwd_return > 0 else 0

        issued.append((sym_id, T, T_date, label))

    if not opportunities:
        print("INSUFFICIENT=1")
        return 0

    # Hold out most recent 20% by decision date
    opportunities.sort(key=lambda x: x[1])
    issued.sort(key=lambda x: x[1])
    n_total = len(issued)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed

    train_issued = issued[:n_train]
    sealed_issued = issued[n_train:]

    # Compute metrics
    def compute_metrics(issued_list):
        if not issued_list:
            return 0, 0, 0, 0, 0
        n = len(issued_list)
        hits = sum(1 for _, _, _, label in issued_list if label == 1)
        precision = hits / n
        base_rate = hits / n  # base rate of predicted class (up) within issued subset
        distinct_days = len(set(d for _, _, d, _ in issued_list))
        # Design effect: cluster by day, compute variance inflation
        day_counts = defaultdict(int)
        for _, _, d, _ in issued_list:
            day_counts[d] += 1
        if len(day_counts) > 1:
            mean_c = n / len(day_counts)
            var_c = sum((c - mean_c) ** 2 for c in day_counts.values()) / len(day_counts)
            deff = 1 + (mean_c - 1) * (var_c / (mean_c ** 2)) if mean_c > 0 else 1
        else:
            deff = n
        effective_n = n / deff if deff > 0 else n
        return n, hits, precision, base_rate, distinct_days, effective_n

    train_n, train_hits, train_prec, train_br, train_dd, train_en = compute_metrics(train_issued)
    sealed_n, sealed_hits, sealed_prec, _, _, _ = compute_metrics(sealed_issued)

    # Overall issued metrics (for reporting)
    total_issued = len(issued)
    total_hits = sum(1 for _, _, _, l in issued if l == 1)
    overall_prec = total_hits / total_issued if total_issued else 0
    overall_br = total_hits / total_issued if total_issued else 0
    overall_dd = len(set(d for _, _, d, _ in issued))
    day_counts = defaultdict(int)
    for _, _, d, _ in issued:
        day_counts[d] += 1
    mean_c = total_issued / len(day_counts) if day_counts else 1
    var_c = sum((c - mean_c) ** 2 for c in day_counts.values()) / len(day_counts) if day_counts else 0
    deff = 1 + (mean_c - 1) * (var_c / (mean_c ** 2)) if mean_c > 0 and var_c > 0 else 1
    overall_en = total_issued / deff if deff > 0 else total_issued

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={overall_prec:.6f}")
    print(f"BASE_RATE={overall_br:.6f}")
    print(f"DISTINCT_DAYS={overall_dd}")
    print(f"EFFECTIVE_N={overall_en:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
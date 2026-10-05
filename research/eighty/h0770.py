# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 769
# cycle_index: 39
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

OFFICER_KEYWORDS = ('CEO', 'CFO', 'COO', 'PRESIDENT', 'CHIEF', 'OFFICER', 'EXECUTIVE', 'VP ', 'VICE PRESIDENT', 'TREASURER', 'CONTROLLER', 'PRINCIPAL')

def is_officer(title: str) -> bool:
    if not title:
        return False
    t = title.upper()
    return any(kw in t for kw in OFFICER_KEYWORDS)

def epoch_to_date(ts: int) -> str:
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def date_to_epoch(d: str) -> int:
    return int(datetime.strptime(d, '%Y-%m-%d').timestamp())

def get_trading_days(conn, start_date: str, end_date: str) -> list:
    cur = conn.execute("""
        SELECT DISTINCT date(ts, 'unixepoch') as d
        FROM bars
        WHERE tf='1d' AND date(ts, 'unixepoch') BETWEEN ? AND ?
        ORDER BY d
    """, (start_date, end_date))
    return [row[0] for row in cur.fetchall()]

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row

    # 1. Get all officer open-market purchases
    cur = conn.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P' AND value > 0
        ORDER BY filed_ts
    """)
    all_trades = [dict(row) for row in cur.fetchall()]
    print(f"Total open-market purchases: {len(all_trades)}", file=sys.stderr)

    # Filter officers
    officer_trades = [t for t in all_trades if is_officer(t['title'])]
    print(f"Officer purchases: {len(officer_trades)}", file=sys.stderr)

    # 2. Build trading day calendar from bars
    cur = conn.execute("SELECT MIN(date(ts, 'unixepoch')), MAX(date(ts, 'unixepoch')) FROM bars WHERE tf='1d'")
    min_bar, max_bar = cur.fetchone()
    all_trading_days = get_trading_days(conn, min_bar, max_bar)
    trading_day_set = set(all_trading_days)
    day_to_idx = {d: i for i, d in enumerate(all_trading_days)}
    print(f"Trading days: {len(all_trading_days)} from {min_bar} to {max_bar}", file=sys.stderr)

    # 3. For each officer trade, compute decision date and check dormancy
    # Group trades by insider for dormancy check
    trades_by_insider = defaultdict(list)
    for t in officer_trades:
        trades_by_insider[t['insider']].append(t)

    # For each insider, sort by tx_ts (trade date)
    for insider, trades in trades_by_insider.items():
        trades.sort(key=lambda x: x['tx_ts'])

    # 4. Load sentiment_features for all symbols we need
    symbol_ids = set(t['symbol_id'] for t in officer_trades)
    sentiment_by_symbol = defaultdict(list)
    for sid in symbol_ids:
        cur = conn.execute("""
            SELECT day, mean_score
            FROM sentiment_features
            WHERE symbol_id = ?
            ORDER BY day
        """, (sid,))
        for row in cur.fetchall():
            sentiment_by_symbol[sid].append((row['day'], row['mean_score']))

    # 5. Process each trade
    opportunities = 0
    issued = []
    issued_by_day = defaultdict(list)

    for t in officer_trades:
        opportunities += 1
        sid = t['symbol_id']
        insider = t['insider']
        tx_ts = t['tx_ts']
        filed_ts = t['filed_ts']
        decision_date = epoch_to_date(filed_ts)

        # As-of discipline: inputs computable at filed_ts
        # Check filing lag <= 1 day (86400 seconds)
        if filed_ts - tx_ts > 86400:
            continue

        # Check personal buying dormancy: no code='P' by same insider in prior 126 trading days
        tx_date = epoch_to_date(tx_ts)
        if tx_date not in day_to_idx:
            continue
        tx_idx = day_to_idx[tx_date]
        if tx_idx < 126:
            continue
        # Check prior trades by same insider
        insider_trades = trades_by_insider[insider]
        # Find index of current trade in insider's sorted list
        curr_idx = next(i for i, tr in enumerate(insider_trades) if tr['accession'] == t['accession'])
        dormant = True
        for i in range(curr_idx - 1, -1, -1):
            prev_tx_date = epoch_to_date(insider_trades[i]['tx_ts'])
            if prev_tx_date not in day_to_idx:
                continue
            if day_to_idx[tx_date] - day_to_idx[prev_tx_date] <= 126:
                dormant = False
                break
            else:
                break  # older than 126 days
        if not dormant:
            continue

        # Check sentiment features
        sent = sentiment_by_symbol.get(sid, [])
        if len(sent) < 252:
            continue
        # Filter to days <= decision_date
        sent_hist = [(d, v) for d, v in sent if d <= decision_date]
        if len(sent_hist) < 252:
            continue
        # 252-day mean
        recent_252 = sent_hist[-252:]
        mean_252 = sum(v for _, v in recent_252) / 252
        # Median of 252-day means up to this point (expanding median)
        # For simplicity, use median of all 252-day windows up to decision_date
        # But that's expensive. Use median of mean_score over last 252 days as threshold? 
        # The hypothesis says "252-day sentiment level below median (persistently negative)"
        # Interpret as: current 252-day mean < median of all 252-day means in history up to decision_date
        # Compute expanding 252-day means
        expanding_means = []
        for i in range(251, len(sent_hist)):
            window = sent_hist[i-251:i+1]
            expanding_means.append(sum(v for _, v in window) / 252)
        if not expanding_means:
            continue
        median_252 = sorted(expanding_means)[len(expanding_means) // 2]
        if mean_252 >= median_252:
            continue

        # 21-day slope > 0
        if len(sent_hist) < 21:
            continue
        recent_21 = sent_hist[-21:]
        x = list(range(21))
        y = [v for _, v in recent_21]
        n = 21
        sum_x = sum(x)
        sum_y = sum(y)
        sum_xy = sum(x[i] * y[i] for i in range(n))
        sum_x2 = sum(xi * xi for xi in x)
        denom = n * sum_x2 - sum_x * sum_x
        if denom == 0:
            continue
        slope = (n * sum_xy - sum_x * sum_y) / denom
        if slope <= 0:
            continue

        # Anti-cluster: no other officer purchase same symbol same decision_date
        # We'll check after collecting all candidates

        # Get 5-day forward return from bars
        # Find bar at decision_date (tf='1d')
        cur = conn.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf='1d' AND date(ts, 'unixepoch') = ?
        """, (sid, decision_date))
        row = cur.fetchone()
        if not row:
            continue
        entry_close = row['close']

        # Find bar 5 trading days later
        if decision_date not in day_to_idx:
            continue
        d_idx = day_to_idx[decision_date]
        if d_idx + 5 >= len(all_trading_days):
            continue
        exit_date = all_trading_days[d_idx + 5]
        cur = conn.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf='1d' AND date(ts, 'unixepoch') = ?
        """, (sid, exit_date))
        row = cur.fetchone()
        if not row:
            continue
        exit_close = row['close']

        fwd_return = (exit_close - entry_close) / entry_close
        label = 1 if fwd_return > 0 else 0

        issued.append({
            'symbol_id': sid,
            'decision_date': decision_date,
            'label': label,
            'fwd_return': fwd_return,
            'insider': insider,
            'accession': t['accession']
        })

    print(f"Candidates before anti-cluster: {len(issued)}", file=sys.stderr)

    # Anti-cluster: dedupe by (symbol_id, decision_date), keep first
    seen = set()
    deduped = []
    for c in issued:
        key = (c['symbol_id'], c['decision_date'])
        if key not in seen:
            seen.add(key)
            deduped.append(c)
    issued = deduped
    print(f"After anti-cluster: {len(issued)}", file=sys.stderr)

    if not issued:
        print("INSUFFICIENT=1")
        return

    # Split by decision_date: most recent 20% as sealed
    decision_dates = sorted(set(c['decision_date'] for c in issued))
    n_sealed_dates = max(1, int(len(decision_dates) * 0.2))
    sealed_dates = set(decision_dates[-n_sealed_dates:])

    train = [c for c in issued if c['decision_date'] not in sealed_dates]
    sealed = [c for c in issued if c['decision_date'] in sealed_dates]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0
        n = len(calls)
        hits = sum(c['label'] for c in calls)
        precision = hits / n
        base_rate = hits / n  # base rate within issued subset
        distinct_days = len(set(c['decision_date'] for c in calls))
        # Design effect: Kish approximation using day-level clustering
        # Group by day
        day_groups = defaultdict(list)
        for c in calls:
            day_groups[c['decision_date']].append(c['label'])
        day_precisions = [sum(g)/len(g) for g in day_groups.values()]
        day_sizes = [len(g) for g in day_groups.values()]
        overall_p = precision
        if overall_p == 0 or overall_p == 1:
            design_effect = 1.0
        else:
            # Variance of mean with clustering
            D = len(day_groups)
            if D <= 1:
                design_effect = 1.0
            else:
                # Between-day variance component
                weighted_var = sum(day_sizes[i] * (day_precisions[i] - overall_p)**2 for i in range(D))
                var_clustered = (D / (D - 1)) * weighted_var / (n * n)
                var_iid = overall_p * (1 - overall_p) / n
                design_effect = var_clustered / var_iid if var_iid > 0 else 1.0
                design_effect = max(1.0, design_effect)
        effective_n = n / design_effect
        return n, precision, base_rate, distinct_days, effective_n

    train_n, train_prec, train_br, train_dd, train_en = compute_metrics(train)
    sealed_n, sealed_prec, sealed_br, sealed_dd, sealed_en = compute_metrics(sealed)

    print(f"ISSUED={train_n}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_br:.6f}")
    print(f"DISTINCT_DAYS={train_dd}")
    print(f"EFFECTIVE_N={train_en:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()
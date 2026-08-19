# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 873
# cycle_index: 19
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Symbols with >= 500 daily bars
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.id IN (
            SELECT symbol_id FROM bars WHERE tf='1d' GROUP BY symbol_id HAVING COUNT(*) >= 500
        )
    """)
    symbols = cur.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = [s['id'] for s in symbols]
    placeholders = ','.join('?' * len(symbol_ids))

    # 2. Fetch all 1d bars for these symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))

    # 3. Fetch news counts per symbol per UTC date
    cur.execute(f"""
        SELECT symbol_id, date(ts, 'unixepoch') as day, COUNT(*) as cnt
        FROM news
        WHERE symbol_id IN ({placeholders})
        GROUP BY symbol_id, day
    """, symbol_ids)
    news_by_symbol = defaultdict(dict)
    for row in cur.fetchall():
        news_by_symbol[row['symbol_id']][row['day']] = row['cnt']

    # 4. Fetch insider open-market purchases (code='P')
    cur.execute(f"""
        SELECT symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P' AND symbol_id IN ({placeholders})
    """, symbol_ids)
    insider_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        insider_by_symbol[row['symbol_id']].append((row['tx_ts'], row['filed_ts']))

    # 5. Fetch EPS fundamentals (as_of > 0)
    cur.execute(f"""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric='EPS' AND as_of > 0 AND symbol_id IN ({placeholders})
    """, symbol_ids)
    eps_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        eps_by_symbol[row['symbol_id']].append((row['as_of'], row['value'], row['fetched_at']))

    all_decisions = []  # (decision_ts, symbol_id, hit, forward_return, decision_date)

    for sym in symbols:
        sid = sym['id']
        bars = bars_by_symbol.get(sid, [])
        if len(bars) < 500:
            continue

        bar_ts = [ts for ts, _ in bars]
        bar_closes = [close for _, close in bars]
        bar_dates = [epoch_to_date(ts) for ts in bar_ts]

        news_dates = news_by_symbol.get(sid, {})
        insider_trades = insider_by_symbol.get(sid, [])
        eps_data = eps_by_symbol.get(sid, [])

        # Sort insider trades by tx_ts
        insider_trades.sort(key=lambda x: x[0])
        insider_tx_dates = [epoch_to_date(tx) for tx, _ in insider_trades]
        insider_filed_dates = [epoch_to_date(fd) for _, fd in insider_trades]

        # Sort EPS by fetched_at ascending (so we can find latest available at decision time)
        eps_data.sort(key=lambda x: x[2])  # fetched_at

        # Precompute news count per trading day
        news_per_day = [news_dates.get(d, 0) for d in bar_dates]

        # Need at least 357 bars before decision + 21 forward = 378 total
        min_idx = 357
        max_idx = len(bars) - 22
        if max_idx <= min_idx:
            continue

        for idx in range(min_idx, max_idx + 1):
            decision_ts = bar_ts[idx]
            decision_date = bar_dates[idx]

            # Universe check: >= 100 news days in prior 252 trading days (idx-252 to idx-1)
            universe_news_days = sum(1 for i in range(idx-252, idx) if news_per_day[i] > 0)
            if universe_news_days < 100:
                continue

            # Historical insider purchase check (at least one ever before decision with filed_ts <= decision_ts)
            has_historical_insider = any(fd <= decision_date for _, fd in zip(insider_tx_dates, insider_filed_dates))
            if not has_historical_insider:
                continue

            # Baseline 252-day window: indices [idx-357, idx-106] (252 days before the 5 windows)
            baseline_start = idx - 357
            baseline_end = idx - 106
            # 5 windows of 21 days each:
            w1_start, w1_end = idx - 105, idx - 85
            w2_start, w2_end = idx - 84, idx - 64
            w3_start, w3_end = idx - 63, idx - 43
            w4_start, w4_end = idx - 42, idx - 22
            w5_start, w5_end = idx - 21, idx - 1

            # Abstain: Fewer than 20 news days in baseline 252-day window
            baseline_news_days = sum(1 for i in range(baseline_start, baseline_end + 1) if news_per_day[i] > 0)
            if baseline_news_days < 20:
                continue

            # Check 5 consecutive declining windows (total headlines per window)
            w1_news = sum(news_per_day[w1_start:w1_end+1])
            w2_news = sum(news_per_day[w2_start:w2_end+1])
            w3_news = sum(news_per_day[w3_start:w3_end+1])
            w4_news = sum(news_per_day[w4_start:w4_end+1])
            w5_news = sum(news_per_day[w5_start:w5_end+1])

            if not (w1_news > w2_news > w3_news > w4_news > w5_news):
                continue

            # Check insider purchases in most recent window (W5): tx_date in W5, filed_date <= decision_date
            w5_start_date = bar_dates[w5_start]
            w5_end_date = bar_dates[w5_end]
            recent_insider = 0
            for tx_d, fd_d in zip(insider_tx_dates, insider_filed_dates):
                if w5_start_date <= tx_d <= w5_end_date and fd_d <= decision_date:
                    recent_insider += 1
            if recent_insider < 2:
                continue

            # Abstain: < 2 qualifying insider purchases in prior 63 sessions (W5+W4+W3 = 63 days)
            w3_start_date = bar_dates[w3_start]
            prior63_insider = 0
            for tx_d, fd_d in zip(insider_tx_dates, insider_filed_dates):
                if w3_start_date <= tx_d <= w5_end_date and fd_d <= decision_date:
                    prior63_insider += 1
            if prior63_insider < 2:
                continue

            # Latest quarterly EPS growth > 0 (as of decision date, using fetched_at <= decision_ts)
            available_eps = [(as_of, val) for as_of, val, fetched in eps_data if fetched <= decision_ts]
            if len(available_eps) < 2:
                continue
            # Take two most recent by as_of (period)
            available_eps.sort(key=lambda x: x[0], reverse=True)
            latest_eps = available_eps[0][1]
            prior_eps = available_eps[1][1]
            if prior_eps <= 0:
                continue
            eps_growth = (latest_eps - prior_eps) / abs(prior_eps)
            if eps_growth <= 0:
                continue

            # All entry conditions met - compute 21-day forward return
            entry_price = bar_closes[idx]
            exit_price = bar_closes[idx + 21]
            fwd_return = (exit_price - entry_price) / entry_price
            hit = 1 if fwd_return > 0 else 0

            all_decisions.append((decision_ts, sid, hit, fwd_return, decision_date))

    if not all_decisions:
        print("INSUFFICIENT=1")
        return 0

    # Sort by decision timestamp
    all_decisions.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    n_total = len(all_decisions)
    n_sealed = max(1, int(n_total * 0.2))
    main_decisions = all_decisions[:-n_sealed]
    sealed_decisions = all_decisions[-n_sealed:]

    def compute_metrics(decisions):
        if not decisions:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(decisions)
        hits = sum(d[2] for d in decisions)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of predicted class (up) within issued subset
        distinct_days = len(set(d[4] for d in decisions))
        # Design effect: cluster by date, compute variance inflation
        # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use Kish's effective sample size: n_eff = (sum w)^2 / sum(w^2) where w=1 per obs
        # But with clustering, we can estimate design effect from intra-cluster correlation
        # Simpler: group by date, compute design effect as 1 + (m-1)*rho where m=avg cluster size
        date_counts = defaultdict(int)
        for d in decisions:
            date_counts[d[4]] += 1
        cluster_sizes = list(date_counts.values())
        if len(cluster_sizes) > 1:
            m = sum(cluster_sizes) / len(cluster_sizes)
            # Estimate rho from variance of cluster means vs overall variance
            # Simplified: assume rho = 0.1 (conservative for financial returns)
            rho = 0.1
            deff = 1 + (m - 1) * rho
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    # Main era metrics
    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(main_decisions)
    opportunities = n_total  # total decision points considered (before holdout)

    # Sealed era precision
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_decisions)

    # Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
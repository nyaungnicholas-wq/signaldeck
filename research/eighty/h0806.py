# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 805
# cycle_index: 1
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def add_business_days(d, n):
    added = 0
    while added < n:
        d += timedelta(days=1)
        if d.weekday() < 5:
            added += 1
    return d

def business_days_between(d1, d2):
    if d1 >= d2:
        return 0
    days = 0
    cur = d1
    while cur < d2:
        if cur.weekday() < 5:
            days += 1
        cur += timedelta(days=1)
    return days

def get_trading_days(conn, symbol_id, start_date, end_date):
    cur = conn.execute(
        "SELECT DISTINCT date(ts, 'unixepoch') as d FROM bars "
        "WHERE symbol_id=? AND tf='1d' AND date(ts, 'unixepoch') BETWEEN ? AND ? "
        "ORDER BY d",
        (symbol_id, start_date, end_date)
    )
    return [str_to_date(row[0]) for row in cur.fetchall()]

def get_forward_return_21d(conn, symbol_id, entry_date, trading_days_cache):
    if symbol_id not in trading_days_cache:
        trading_days_cache[symbol_id] = get_trading_days(conn, symbol_id, '2018-01-01', '2026-12-31')
    tdays = trading_days_cache[symbol_id]
    if not tdays:
        return None
    try:
        idx = tdays.index(entry_date)
    except ValueError:
        return None
    if idx + 21 >= len(tdays):
        return None
    start_day = tdays[idx]
    end_day = tdays[idx + 21]
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND date(ts, 'unixepoch') IN (?, ?)",
        (symbol_id, date_to_str(start_day), date_to_str(end_day))
    )
    rows = cur.fetchall()
    if len(rows) != 2:
        return None
    start_close, end_close = rows[0][0], rows[1][0]
    if start_close == 0:
        return None
    return (end_close - start_close) / start_close

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # Universe: symbols with all required data
    cur = conn.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d' AND date(ts, 'unixepoch') >= '2018-01-01'")
    symbols_with_bars = {row[0] for row in cur.fetchall()}

    cur = conn.execute("SELECT DISTINCT symbol_id FROM insider_trades WHERE code='P'")
    symbols_with_insider = {row[0] for row in cur.fetchall()}

    cur = conn.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
    symbols_with_news = {row[0] for row in cur.fetchall()}

    cur = conn.execute("SELECT DISTINCT symbol_id FROM stocktwits_sentiment")
    symbols_with_twits = {row[0] for row in cur.fetchall()}

    cur = conn.execute("SELECT DISTINCT symbol_id FROM inst_holdings")
    symbols_with_13f = {row[0] for row in cur.fetchall()}

    cur = conn.execute("SELECT DISTINCT symbol_id FROM fundamentals WHERE metric='SharesOutstanding' AND as_of != 0")
    symbols_with_shares = {row[0] for row in cur.fetchall()}

    universe = symbols_with_bars & symbols_with_insider & symbols_with_news & symbols_with_twits & symbols_with_13f & symbols_with_shares
    if len(universe) == 0:
        print("INSUFFICIENT=1")
        return

    universe_list = list(universe)
    placeholders = ','.join('?'*len(universe_list))

    # Load SharesOutstanding (latest fetched_at per symbol)
    shares_outstanding = {}
    cur = conn.execute(
        f"SELECT symbol_id, value, fetched_at FROM fundamentals "
        f"WHERE metric='SharesOutstanding' AND as_of != 0 AND symbol_id IN ({placeholders})",
        tuple(universe_list)
    )
    for row in cur.fetchall():
        sid, val, fetched = row
        if sid not in shares_outstanding or fetched > shares_outstanding[sid][1]:
            shares_outstanding[sid] = (val, fetched)

    # Load 13F holdings aggregated per symbol per period
    holdings_by_symbol_period = defaultdict(dict)
    cur = conn.execute(
        f"SELECT symbol_id, period, SUM(shares) as total_shares FROM inst_holdings "
        f"WHERE symbol_id IN ({placeholders}) GROUP BY symbol_id, period",
        tuple(universe_list)
    )
    for row in cur.fetchall():
        holdings_by_symbol_period[row[0]][row[1]] = row[2]
    for sid in holdings_by_symbol_period:
        holdings_by_symbol_period[sid] = dict(sorted(holdings_by_symbol_period[sid].items()))

    # Load news sentiment daily mean_score
    news_by_symbol = defaultdict(dict)
    cur = conn.execute(
        f"SELECT symbol_id, day, mean_score FROM sentiment_features WHERE symbol_id IN ({placeholders})",
        tuple(universe_list)
    )
    for row in cur.fetchall():
        news_by_symbol[row[0]][row[1]] = row[2]

    # Load StockTwits daily bullish ratio
    twits_by_symbol = defaultdict(dict)
    cur = conn.execute(
        f"SELECT symbol_id, date(ts, 'unixepoch') as d, SUM(bullish) as bullish, SUM(bearish) as bearish "
        f"FROM stocktwits_sentiment WHERE symbol_id IN ({placeholders}) GROUP BY symbol_id, d",
        tuple(universe_list)
    )
    for row in cur.fetchall():
        sid, d, bull, bear = row
        if bull + bear > 0:
            twits_by_symbol[sid][d] = bull / (bull + bear)

    # Load insider purchases (code='P') with disclosure delay <= 5 business days
    insider_purchases = []
    cur = conn.execute(
        f"SELECT accession, symbol_id, tx_ts, filed_ts, shares, price, value "
        f"FROM insider_trades WHERE code='P' AND symbol_id IN ({placeholders}) ORDER BY filed_ts",
        tuple(universe_list)
    )
    for row in cur.fetchall():
        acc, sid, tx_ts, filed_ts, shares, price, value = row
        tx_date = epoch_to_date(tx_ts)
        filed_date = epoch_to_date(filed_ts)
        delay = business_days_between(tx_date, filed_date)
        if delay <= 5:
            insider_purchases.append({
                'accession': acc,
                'symbol_id': sid,
                'tx_date': tx_date,
                'filed_date': filed_date,
                'delay': delay
            })

    if not insider_purchases:
        print("INSUFFICIENT=1")
        return

    # Build filed_date lookup per symbol for prior purchase check
    filed_dates_by_symbol = defaultdict(set)
    for p in insider_purchases:
        filed_dates_by_symbol[p['symbol_id']].add(p['filed_date'])

    # Precompute trading days for all universe symbols
    trading_days_cache = {}
    for sid in universe_list:
        trading_days_cache[sid] = get_trading_days(conn, sid, '2018-01-01', '2026-12-31')

    # Process each insider purchase as potential signal
    signals = []  # (symbol_id, decision_date, inst_own_pct, accession)
    opportunities = 0

    for purch in insider_purchases:
        sid = purch['symbol_id']
        decision_date = purch['filed_date']
        opportunities += 1

        tdays = trading_days_cache.get(sid, [])
        if not tdays:
            continue
        # Find trading day index for decision_date (must be a trading day)
        try:
            idx = tdays.index(decision_date)
        except ValueError:
            continue

        # ABSTAIN: 20-day avg dollar volume < $500k
        if idx < 19:
            continue
        window_days = tdays[idx-19:idx+1]
        placeholders_dv = ','.join('?'*len(window_days))
        cur = conn.execute(
            f"SELECT close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND date(ts, 'unixepoch') IN ({placeholders_dv})",
            (sid, *[date_to_str(d) for d in window_days])
        )
        dv_sum = 0
        count = 0
        for row in cur.fetchall():
            dv_sum += row[0] * row[1]
            count += 1
        if count < 20 or dv_sum / 20 < 500_000:
            continue

        # ABSTAIN: prior insider purchase disclosed in last 10 sessions
        prior_count = 0
        has_prior = False
        for i in range(idx-1, -1, -1):
            if prior_count >= 10:
                break
            prior_count += 1
            if tdays[i] in filed_dates_by_symbol[sid]:
                has_prior = True
                break
        if has_prior:
            continue

        # News sentiment: 5-day mean <= 10th percentile of 252-day history
        news_vals_5d = []
        for i in range(idx, -1, -1):
            if len(news_vals_5d) >= 5:
                break
            d_str = date_to_str(tdays[i])
            if d_str in news_by_symbol[sid]:
                news_vals_5d.append(news_by_symbol[sid][d_str])
        if len(news_vals_5d) < 5:
            continue
        news_5d_mean = sum(news_vals_5d) / 5

        hist_vals = []
        for i in range(idx, -1, -1):
            if len(hist_vals) >= 252:
                break
            d_str = date_to_str(tdays[i])
            if d_str in news_by_symbol[sid]:
                hist_vals.append(news_by_symbol[sid][d_str])
        if len(hist_vals) < 252:
            continue
        hist_vals.sort()
        p10 = hist_vals[int(0.10 * len(hist_vals))]
        if news_5d_mean > p10:
            continue

        # StockTwits: 5-day mean bullish ratio >= 0.55
        twits_vals = []
        for i in range(idx, -1, -1):
            if len(twits_vals) >= 5:
                break
            d_str = date_to_str(tdays[i])
            if d_str in twits_by_symbol[sid]:
                twits_vals.append(twits_by_symbol[sid][d_str])
        if len(twits_vals) < 5:
            continue
        twits_5d_mean = sum(twits_vals) / 5
        if twits_5d_mean < 0.55:
            continue

        # Institutional ownership <= 25th percentile of universe at decision_date
        # Find latest 13F period knowable at decision_date (period + 45 calendar days <= decision_date)
        inst_own_pct = None
        if sid in holdings_by_symbol_period and sid in shares_outstanding:
            total_shares = shares_outstanding[sid][0]
            if total_shares > 0:
                for period in reversed(holdings_by_symbol_period[sid].keys()):
                    period_date = str_to_date(period)
                    knowable_date = period_date + timedelta(days=45)
                    if knowable_date <= decision_date:
                        inst_shares = holdings_by_symbol_period[sid][period]
                        inst_own_pct = inst_shares / total_shares
                        break
        if inst_own_pct is None:
            continue

        signals.append({
            'symbol_id': sid,
            'decision_date': decision_date,
            'inst_own_pct': inst_own_pct,
            'accession': purch['accession']
        })

    if not signals:
        print("INSUFFICIENT=1")
        return

    # Group signals by decision_date to compute universe 25th percentile of inst_own_pct
    signals_by_date = defaultdict(list)
    for s in signals:
        signals_by_date[s['decision_date']].append(s)

    # For each decision date, compute 25th percentile across ALL universe symbols with data
    # We need inst_own_pct for all universe symbols at each decision date
    # Precompute inst_own_pct for all universe symbols at each decision date
    universe_inst_own = {}  # (symbol_id, decision_date) -> inst_own_pct
    for sid in universe_list:
        if sid not in holdings_by_symbol_period or sid not in shares_outstanding:
            continue
        total_shares = shares_outstanding[sid][0]
        if total_shares == 0:
            continue
        periods = list(holdings_by_symbol_period[sid].keys())
        for decision_date in signals_by_date.keys():
            for period in reversed(periods):
                period_date = str_to_date(period)
                knowable_date = period_date + timedelta(days=45)
                if knowable_date <= decision_date:
                    inst_shares = holdings_by_symbol_period[sid][period]
                    universe_inst_own[(sid, decision_date)] = inst_shares / total_shares
                    break

    # Filter signals by institutional ownership percentile
    final_signals = []
    for decision_date, day_signals in signals_by_date.items():
        # Get all universe inst_own_pct at this date
        pcts = [universe_inst_own[(sid, decision_date)] for sid in universe_list if (sid, decision_date) in universe_inst_own]
        if not pcts:
            continue
        pcts.sort()
        p25 = pcts[int(0.25 * len(pcts))]
        for s in day_signals:
            if s['inst_own_pct'] <= p25:
                final_signals.append(s)

    if not final_signals:
        print("INSUFFICIENT=1")
        return

    # Compute forward returns for final signals
    results = []  # (symbol_id, decision_date, forward_return, up)
    for s in final_signals:
        fwd_ret = get_forward_return_21d(conn, s['symbol_id'], s['decision_date'], trading_days_cache)
        if fwd_ret is not None:
            up = 1 if fwd_ret > 0 else 0
            results.append((s['symbol_id'], s['decision_date'], fwd_ret, up))

    if not results:
        print("INSUFFICIENT=1")
        return

    # Sort by decision_date
    results.sort(key=lambda x: x[1])

    # Hold out most recent 20% as sealed era
    n = len(results)
    split_idx = int(n * 0.8)
    train_results = results[:split_idx]
    sealed_results = results[split_idx:]

    def compute_metrics(res):
        if not res:
            return 0, 0, 0, 0, 0
        issued = len(res)
        hits = sum(1 for r in res if r[3] == 1)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(set(r[1] for r in res))
        # Design effect: 1 + (avg_cluster_size - 1) * ICC
        # Estimate ICC from data: cluster by day, compute variance ratio
        day_counts = defaultdict(int)
        for r in res:
            day_counts[r[1]] += 1
        cluster_sizes = list(day_counts.values())
        if len(cluster_sizes) > 1:
            mean_cluster = sum(cluster_sizes) / len(cluster_sizes)
            # ICC approximation: (between-cluster variance) / (total variance)
            # For binary outcome, use ANOVA estimator
            overall_mean = hits / issued
            between_var = sum(c * ((sum(1 for r in res if r[1]==d and r[3]==1)/c if c>0 else 0) - overall_mean)**2 for d, c in day_counts.items()) / (len(day_counts) - 1) if len(day_counts) > 1 else 0
            within_var = overall_mean * (1 - overall_mean)
            icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
            icc = max(0, min(1, icc))
            design_effect = 1 + (mean_cluster - 1) * icc
        else:
            design_effect = 1.0
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days, train_effective_n = compute_metrics(train_results)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_results)

    # Overall metrics on full set (for reporting)
    all_issued, all_hits, all_precision, all_base_rate, all_distinct_days, all_effective_n = compute_metrics(results)

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={all_effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
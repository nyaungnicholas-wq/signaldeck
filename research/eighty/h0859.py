# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 858
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import sys
import math
from datetime import datetime, timedelta
from collections import defaultdict

def business_days_between(start_date, end_date):
    """Count business days between two dates (exclusive of end_date)."""
    count = 0
    current = start_date
    while current < end_date:
        if current.weekday() < 5:
            count += 1
        current += timedelta(days=1)
    return count

def add_business_days(start_date, n):
    """Add n business days to start_date."""
    current = start_date
    added = 0
    while added < n:
        current += timedelta(days=1)
        if current.weekday() < 5:
            added += 1
    return current

def percentile(sorted_values, p):
    """Compute percentile from sorted list."""
    if not sorted_values:
        return None
    k = (len(sorted_values) - 1) * p
    f = math.floor(k)
    c = math.ceil(k)
    if f == c:
        return sorted_values[int(f)]
    return sorted_values[int(f)] * (c - k) + sorted_values[int(c)] * (k - f)

def rolling_std(values, window):
    """Compute rolling standard deviation."""
    result = []
    for i in range(len(values)):
        if i < window - 1:
            result.append(None)
        else:
            window_vals = values[i - window + 1:i + 1]
            mean = sum(window_vals) / window
            var = sum((x - mean) ** 2 for x in window_vals) / window
            result.append(math.sqrt(var))
    return result

def rolling_percentile(values, window, p):
    """Compute rolling percentile."""
    result = []
    for i in range(len(values)):
        if i < window - 1:
            result.append(None)
        else:
            window_vals = sorted(values[i - window + 1:i + 1])
            result.append(percentile(window_vals, p))
    return result

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    conn = sqlite3.connect(db_path, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Fetch sentiment_features daily mean_score
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE day >= '2017-01-01'
        ORDER BY symbol_id, day
    """)
    sentiment_rows = cur.fetchall()

    # Fetch officer (CEO/CFO) open-market purchases
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts, title
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
          AND filed_ts >= strftime('%s', '2018-07-01')
        ORDER BY symbol_id, filed_ts
    """)
    trade_rows = cur.fetchall()

    # Fetch symbols for universe filtering
    cur.execute("SELECT id, symbol FROM symbols WHERE active = 1")
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}

    # Group sentiment by symbol
    sentiment_by_symbol = defaultdict(list)
    for row in sentiment_rows:
        sentiment_by_symbol[row['symbol_id']].append((row['day'], row['mean_score']))

    # Group trades by symbol
    trades_by_symbol = defaultdict(list)
    for row in trade_rows:
        trade_date = datetime.fromtimestamp(row['tx_ts']).date()
        file_date = datetime.fromtimestamp(row['filed_ts']).date()
        trades_by_symbol[row['symbol_id']].append({
            'trade_date': trade_date,
            'file_date': file_date,
            'title': row['title']
        })

    # Compute signals for each symbol
    all_opportunities = []  # (symbol_id, decision_date, signal_date, forward_return)
    all_issued = []  # (symbol_id, decision_date, signal_date, forward_return, hit)

    for symbol_id, sent_data in sentiment_by_symbol.items():
        if symbol_id not in symbols:
            continue
        if len(sent_data) < 273:  # Need 252 for percentile + 21 for rolling std
            continue
        if symbol_id not in trades_by_symbol:
            continue

        sent_data.sort(key=lambda x: x[0])
        dates = [datetime.strptime(d, '%Y-%m-%d').date() for d, _ in sent_data]
        scores = [s for _, s in sent_data]

        # Compute 21-day rolling std
        roll_std = rolling_std(scores, 21)
        # Compute 252-day rolling 10th and 90th percentiles of rolling_std
        valid_std = [(i, v) for i, v in enumerate(roll_std) if v is not None]
        if len(valid_std) < 252:
            continue

        std_values = [v for _, v in valid_std]
        std_indices = [i for i, _ in valid_std]

        p10_roll = rolling_percentile(std_values, 252, 0.10)
        p90_roll = rolling_percentile(std_values, 252, 0.90)

        # Find crossing days: roll_std[t] > p90[t] and roll_std[t-20:t] all < p10[t]
        signal_dates = []
        for idx, (i, std_val) in enumerate(valid_std):
            if idx < 20:
                continue
            if p90_roll[idx] is None or p10_roll[idx] is None:
                continue
            if std_val <= p90_roll[idx]:
                continue
            # Check previous 20 days all below p10
            ok = True
            for j in range(idx - 20, idx):
                if p10_roll[j] is None or std_values[j] >= p10_roll[j]:
                    ok = False
                    break
            if ok:
                signal_dates.append(dates[i])

        if not signal_dates:
            continue

        # For each signal date, check for officer trade in [t-3, t] with filing <= t and delay <= 5 biz days
        trades = trades_by_symbol[symbol_id]
        for signal_date in signal_dates:
            # Convert signal_date to datetime for comparison
            signal_dt = signal_date
            # Check trades
            matched = False
            for trade in trades:
                td = trade['trade_date']
                fd = trade['file_date']
                # Trade date in [signal_date - 3, signal_date] (calendar days, but trading days only)
                # We'll check business days difference
                if td > signal_dt:
                    continue
                biz_diff = business_days_between(td, signal_dt + timedelta(days=1))
                if biz_diff > 3:
                    continue
                # Filing date <= signal date
                if fd > signal_dt:
                    continue
                # Disclosure delay <= 5 business days
                delay = business_days_between(td, fd + timedelta(days=1))
                if delay > 5:
                    continue
                matched = True
                break
            if not matched:
                continue

            # This is an opportunity (decision point)
            # Decision date = signal_date (we act at close of signal_date)
            # Need forward return over 21 trading days
            all_opportunities.append((symbol_id, signal_dt, signal_dt))

    # Fetch bars for forward returns
    # We need close prices for all symbols in opportunities at signal_date and signal_date + 21 trading days
    if not all_opportunities:
        print("INSUFFICIENT=1")
        return

    # Get unique symbols and date ranges needed
    needed_symbols = set(sid for sid, _, _ in all_opportunities)
    min_date = min(d for _, d, _ in all_opportunities)
    max_date = max(d for _, d, _ in all_opportunities)
    # Need up to 21 trading days after max_date
    max_date_plus = add_business_days(max_date, 21)

    placeholders = ','.join('?' * len(needed_symbols))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
          AND symbol_id IN ({placeholders})
          AND ts >= strftime('%s', ?)
          AND ts <= strftime('%s', ?)
        ORDER BY symbol_id, ts
    """, list(needed_symbols) + [min_date.isoformat(), max_date_plus.isoformat()])
    bar_rows = cur.fetchall()

    # Organize bars by symbol_id -> list of (date, close)
    bars_by_symbol = defaultdict(list)
    for row in bar_rows:
        dt = datetime.fromtimestamp(row['ts']).date()
        bars_by_symbol[row['symbol_id']].append((dt, row['close']))

    # Compute forward returns
    opportunities_with_returns = []
    for symbol_id, signal_date, decision_date in all_opportunities:
        bars = bars_by_symbol.get(symbol_id, [])
        if not bars:
            continue
        # Find close on signal_date
        close_t = None
        close_t21 = None
        # Bars are daily, find exact dates
        bar_dict = {d: c for d, c in bars}
        if signal_date not in bar_dict:
            # Find previous trading day
            prev = signal_date
            for _ in range(10):
                prev -= timedelta(days=1)
                if prev in bar_dict:
                    close_t = bar_dict[prev]
                    signal_date_adj = prev
                    break
            if close_t is None:
                continue
        else:
            close_t = bar_dict[signal_date]
            signal_date_adj = signal_date

        # Find close 21 trading days after signal_date_adj
        target_date = add_business_days(signal_date_adj, 21)
        # Find on or after target_date
        for d in sorted(bar_dict.keys()):
            if d >= target_date:
                close_t21 = bar_dict[d]
                break
        if close_t21 is None:
            continue

        fwd_return = (close_t21 - close_t) / close_t
        hit = 1 if fwd_return > 0 else 0
        opportunities_with_returns.append((symbol_id, decision_date, signal_date_adj, fwd_return, hit))

    if not opportunities_with_returns:
        print("INSUFFICIENT=1")
        return

    # Sort by decision_date
    opportunities_with_returns.sort(key=lambda x: x[1])

    # Split into main (80%) and sealed (20% most recent)
    n_total = len(opportunities_with_returns)
    n_sealed = max(1, int(n_total * 0.2))
    main_ops = opportunities_with_returns[:-n_sealed]
    sealed_ops = opportunities_with_returns[-n_sealed:]

    # Evaluate on main
    issued_main = [op for op in main_ops if op[4] == 1]  # Wait, we issue on ALL opportunities that meet criteria
    # Actually, the rule issues a call on EVERY opportunity that meets entry criteria
    # So all opportunities_with_returns are "issued" calls
    # The hypothesis claims precision >= 0.80 on issued calls
    # So issued = all opportunities that passed entry filters
    issued_main = main_ops
    issued_sealed = sealed_ops

    def compute_metrics(ops):
        if not ops:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(ops)
        hits = sum(op[4] for op in ops)
        precision = hits / issued if issued > 0 else 0.0
        # Base rate within issued subset = precision (all calls predict positive)
        base_rate = precision
        # Distinct days
        distinct_days = len(set(op[1] for op in ops))
        # Design effect: cluster by year-month
        clusters = defaultdict(list)
        for op in ops:
            key = op[1].strftime('%Y-%m')
            clusters[key].append(op[4])
        # Compute ICC
        cluster_means = [sum(v)/len(v) for v in clusters.values()]
        cluster_sizes = [len(v) for v in clusters.values()]
        overall_mean = sum(cluster_means) / len(cluster_means) if cluster_means else 0
        # Between-cluster variance
        if len(cluster_means) > 1:
            between_var = sum((m - overall_mean) ** 2 for m in cluster_means) / (len(cluster_means) - 1)
            # Within-cluster variance
            within_var_sum = 0
            total_n = 0
            for i, vals in enumerate(clusters.values()):
                m = cluster_means[i]
                within_var_sum += sum((v - m) ** 2 for v in vals)
                total_n += len(vals)
            within_var = within_var_sum / (total_n - len(clusters)) if total_n > len(clusters) else 0
            if between_var + within_var > 0:
                icc = between_var / (between_var + within_var)
            else:
                icc = 0
        else:
            icc = 0
        avg_cluster_size = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
        design_effect = 1 + (avg_cluster_size - 1) * max(icc, 0)
        if design_effect <= 1:
            design_effect = 1.0001
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_m, hits_m, prec_m, base_m, distinct_m, eff_m = compute_metrics(issued_main)
    issued_s, hits_s, prec_s, base_s, distinct_s, eff_s = compute_metrics(issued_sealed)

    # Overall issued = main + sealed
    issued_all = issued_m + issued_s
    hits_all = hits_m + hits_s
    prec_all = hits_all / issued_all if issued_all > 0 else 0.0
    base_all = prec_all
    distinct_all = len(set(op[1] for op in issued_main + issued_sealed))
    # Effective N for all
    all_clusters = defaultdict(list)
    for op in issued_main + issued_sealed:
        key = op[1].strftime('%Y-%m')
        all_clusters[key].append(op[4])
    cluster_means = [sum(v)/len(v) for v in all_clusters.values()]
    cluster_sizes = [len(v) for v in all_clusters.values()]
    overall_mean = sum(cluster_means) / len(cluster_means) if cluster_means else 0
    if len(cluster_means) > 1:
        between_var = sum((m - overall_mean) ** 2 for m in cluster_means) / (len(cluster_means) - 1)
        within_var_sum = 0
        total_n = 0
        for i, vals in enumerate(all_clusters.values()):
            m = cluster_means[i]
            within_var_sum += sum((v - m) ** 2 for v in vals)
            total_n += len(vals)
        within_var = within_var_sum / (total_n - len(all_clusters)) if total_n > len(all_clusters) else 0
        if between_var + within_var > 0:
            icc = between_var / (between_var + within_var)
        else:
            icc = 0
    else:
        icc = 0
    avg_cluster_size = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
    design_effect = 1 + (avg_cluster_size - 1) * max(icc, 0)
    if design_effect <= 1:
        design_effect = 1.0001
    effective_n_all = issued_all / design_effect

    print(f"ISSUED={issued_all}")
    print(f"OPPORTUNITIES={n_total}")
    print(f"PRECISION={prec_all:.6f}")
    print(f"BASE_RATE={base_all:.6f}")
    print(f"DISTINCT_DAYS={distinct_all}")
    print(f"EFFECTIVE_N={effective_n_all:.2f}")
    print(f"SEALED_PRECISION={prec_s:.6f}")

if __name__ == '__main__':
    main()
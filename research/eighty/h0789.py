# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 788
# cycle_index: 58
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_db():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def business_days_between(start_date, end_date):
    """Count business days from start_date (exclusive) to end_date (inclusive)"""
    count = 0
    current = start_date + timedelta(days=1)
    while current <= end_date:
        if current.weekday() < 5:
            count += 1
        current += timedelta(days=1)
    return count

def add_business_days(start_date, n):
    """Add n business days to start_date"""
    current = start_date
    added = 0
    while added < n:
        current += timedelta(days=1)
        if current.weekday() < 5:
            added += 1
    return current

def is_friday(d):
    return d.weekday() == 4

def linear_slope(values):
    """Compute slope of linear regression for values (y) against x=0,1,2..."""
    n = len(values)
    if n < 2:
        return 0.0
    x = list(range(n))
    sum_x = sum(x)
    sum_y = sum(values)
    sum_xy = sum(x[i] * values[i] for i in range(n))
    sum_x2 = sum(xi * xi for xi in x)
    denom = n * sum_x2 - sum_x * sum_x
    if denom == 0:
        return 0.0
    return (n * sum_xy - sum_x * sum_y) / denom

def main():
    conn = connect_db()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get all officer open-market purchases (code='P') with CEO/CFO titles
    cur.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
          AND tx_ts IS NOT NULL
          AND filed_ts IS NOT NULL
          AND tx_ts > 0
          AND filed_ts > 0
    """)
    trades = cur.fetchall()
    print(f"Found {len(trades)} officer purchases", file=sys.stderr)

    if not trades:
        print("INSUFFICIENT=1")
        return

    # 2. Filter: disclosure within 5 business days of trade
    filtered_trades = []
    for t in trades:
        trade_date = epoch_to_date(t['tx_ts'])
        file_date = epoch_to_date(t['filed_ts'])
        if file_date < trade_date:
            continue
        bd = business_days_between(trade_date, file_date)
        if bd <= 5:
            filtered_trades.append((t, trade_date, file_date))
    print(f"After 5-business-day filter: {len(filtered_trades)}", file=sys.stderr)

    if not filtered_trades:
        print("INSUFFICIENT=1")
        return

    # 3. Load sentiment_features for all relevant symbols/dates
    # We need 5-day windows ending at trade_date and file_date for each trade
    symbol_ids = set(t[0]['symbol_id'] for t in filtered_trades)
    all_dates = set()
    for _, td, fd in filtered_trades:
        for i in range(5):
            all_dates.add(date_to_str(td - timedelta(days=i)))
            all_dates.add(date_to_str(fd - timedelta(days=i)))

    placeholders = ','.join('?' * len(all_dates))
    sym_placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({sym_placeholders})
          AND day IN ({placeholders})
    """, list(symbol_ids) + list(all_dates))
    sentiment_rows = cur.fetchall()

    # Build sentiment lookup: symbol_id -> {day: mean_score}
    sentiment = defaultdict(dict)
    for row in sentiment_rows:
        sentiment[row['symbol_id']][row['day']] = row['mean_score']

    # 4. Compute 5-day sentiment slope at trade_date and file_date
    trades_with_slope = []
    for t, td, fd in filtered_trades:
        sid = t['symbol_id']
        # Get 5 days ending at trade_date
        trade_scores = []
        for i in range(4, -1, -1):  # 4,3,2,1,0 days before
            d = date_to_str(td - timedelta(days=i))
            if d in sentiment[sid]:
                trade_scores.append(sentiment[sid][d])
        # Get 5 days ending at file_date
        file_scores = []
        for i in range(4, -1, -1):
            d = date_to_str(fd - timedelta(days=i))
            if d in sentiment[sid]:
                file_scores.append(sentiment[sid][d])

        if len(trade_scores) == 5 and len(file_scores) == 5:
            trade_slope = linear_slope(trade_scores)
            file_slope = linear_slope(file_scores)
            if trade_slope < -0.02 and file_slope > 0.02:
                trades_with_slope.append((t, td, fd, trade_slope, file_slope))

    print(f"After sentiment slope filter: {len(trades_with_slope)}", file=sys.stderr)

    if not trades_with_slope:
        print("INSUFFICIENT=1")
        return

    # 5. Filter: no other insider open-market trades for symbol in prior 20 sessions
    # Get all open-market trades (code P or S) for these symbols
    cur.execute(f"""
        SELECT symbol_id, tx_ts
        FROM insider_trades
        WHERE symbol_id IN ({sym_placeholders})
          AND code IN ('P', 'S')
          AND tx_ts IS NOT NULL
    """, list(symbol_ids))
    all_om_trades = cur.fetchall()

    om_by_symbol = defaultdict(list)
    for row in all_om_trades:
        om_by_symbol[row['symbol_id']].append(epoch_to_date(row['tx_ts']))

    trades_no_prior = []
    for t, td, fd, tslope, fslope in trades_with_slope:
        sid = t['symbol_id']
        prior_trades = [d for d in om_by_symbol[sid] if d < td]
        # Count business days in prior 20 sessions
        # Need to check if any prior trade within 20 business days before td
        cutoff = td
        count = 0
        has_prior = False
        for d in sorted(prior_trades, reverse=True):
            bd = business_days_between(d, td)
            if bd <= 20:
                has_prior = True
                break
            else:
                break
        if not has_prior:
            trades_no_prior.append((t, td, fd, tslope, fslope))

    print(f"After no-prior-trades filter: {len(trades_no_prior)}", file=sys.stderr)

    if not trades_no_prior:
        print("INSUFFICIENT=1")
        return

    # 6. Compute 252-day realized volatility for each trade date
    # Need daily bars for symbols
    trade_dates_by_symbol = defaultdict(list)
    for t, td, fd, tslope, fslope in trades_no_prior:
        trade_dates_by_symbol[t['symbol_id']].append(td)

    # Get all daily bars for these symbols up to max trade date
    max_date = max(max(dates) for dates in trade_dates_by_symbol.values())
    min_date = min(min(dates) for dates in trade_dates_by_symbol.values())
    # Need 252 trading days before min_date, so go back ~365 calendar days
    start_date = min_date - timedelta(days=400)

    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
          AND symbol_id IN ({sym_placeholders})
          AND ts >= ?
          AND ts <= ?
        ORDER BY symbol_id, ts
    """, list(symbol_ids) + [int(datetime.combine(start_date, datetime.min.time()).timestamp()),
                              int(datetime.combine(max_date, datetime.max.time()).timestamp())])
    bar_rows = cur.fetchall()

    bars_by_symbol = defaultdict(list)
    for row in bar_rows:
        d = epoch_to_date(row['ts'])
        bars_by_symbol[row['symbol_id']].append((d, row['close']))

    # Compute 252-day realized volatility for each trade date
    volatilities = []
    for t, td, fd, tslope, fslope in trades_no_prior:
        sid = t['symbol_id']
        bars = bars_by_symbol.get(sid, [])
        # Get 252 trading days ending at td (exclusive? or inclusive? Use prior 252 days)
        prior_bars = [(d, c) for d, c in bars if d < td]
        if len(prior_bars) >= 252:
            prior_bars = prior_bars[-252:]
            returns = []
            for i in range(1, len(prior_bars)):
                ret = math.log(prior_bars[i][1] / prior_bars[i-1][1])
                returns.append(ret)
            if returns:
                vol = math.sqrt(sum(r*r for r in returns) / len(returns)) * math.sqrt(252)
                volatilities.append((t, td, fd, tslope, fslope, vol))
            else:
                volatilities.append((t, td, fd, tslope, fslope, None))
        else:
            volatilities.append((t, td, fd, tslope, fslope, None))

    # Find top decile threshold
    valid_vols = [v for v in volatilities if v[5] is not None]
    if valid_vols:
        sorted_vols = sorted(v[5] for v in valid_vols)
        top_decile_idx = int(len(sorted_vols) * 0.9)
        top_decile_threshold = sorted_vols[top_decile_idx] if top_decile_idx < len(sorted_vols) else float('inf')
    else:
        top_decile_threshold = float('inf')

    trades_vol_filtered = [v for v in volatilities if v[5] is None or v[5] <= top_decile_threshold]
    print(f"After volatility filter: {len(trades_vol_filtered)}", file=sys.stderr)

    if not trades_vol_filtered:
        print("INSUFFICIENT=1")
        return

    # 7. Filter: symbol must have at least 5 qualifying setups in history (before this trade)
    # This requires counting how many prior trades for same symbol pass all filters up to that point
    # We'll compute this by processing trades chronologically per symbol
    trades_by_symbol = defaultdict(list)
    for v in trades_vol_filtered:
        trades_by_symbol[v[0]['symbol_id']].append(v)

    qualified_trades = []
    for sid, tlist in trades_by_symbol.items():
        tlist.sort(key=lambda x: x[1])  # sort by trade_date
        count = 0
        for v in tlist:
            if count >= 5:
                qualified_trades.append(v)
            count += 1

    print(f"After min-5-history filter: {len(qualified_trades)}", file=sys.stderr)

    if not qualified_trades:
        print("INSUFFICIENT=1")
        return

    # 8. Filter: disclosure date not Friday
    final_trades = [v for v in qualified_trades if not is_friday(v[2])]
    print(f"After Friday filter: {len(final_trades)}", file=sys.stderr)

    if not final_trades:
        print("INSUFFICIENT=1")
        return

    # 9. Compute 21-trading-day forward return from bars
    # Need bars for 21 trading days after trade_date
    opportunities = []
    for v in final_trades:
        t, td, fd, tslope, fslope, vol = v
        sid = t['symbol_id']
        bars = bars_by_symbol.get(sid, [])
        # Find index of first bar on or after trade_date
        future_bars = [(d, c) for d, c in bars if d >= td]
        if len(future_bars) >= 22:  # need 21 trading days forward (22 bars for 21 returns)
            future_bars = future_bars[:22]
            entry_price = future_bars[0][1]
            exit_price = future_bars[21][1]
            fwd_return = (exit_price - entry_price) / entry_price
            up = 1 if fwd_return > 0 else 0
            opportunities.append({
                'symbol_id': sid,
                'trade_date': td,
                'file_date': fd,
                'accession': t['accession'],
                'fwd_return': fwd_return,
                'up': up,
                'trade_slope': tslope,
                'file_slope': fslope,
                'volatility': vol
            })
        else:
            opportunities.append({
                'symbol_id': sid,
                'trade_date': td,
                'file_date': fd,
                'accession': t['accession'],
                'fwd_return': None,
                'up': None,
                'trade_slope': tslope,
                'file_slope': fslope,
                'volatility': vol
            })

    # Filter to only those with valid forward returns
    valid_opps = [o for o in opportunities if o['fwd_return'] is not None]
    print(f"Valid opportunities with forward returns: {len(valid_opps)}", file=sys.stderr)

    if len(valid_opps) < 10:
        print("INSUFFICIENT=1")
        return

    # 10. Sort by trade_date, split 80/20 for sealed era
    valid_opps.sort(key=lambda x: x['trade_date'])
    split_idx = int(len(valid_opps) * 0.8)
    train_opps = valid_opps[:split_idx]
    sealed_opps = valid_opps[split_idx:]

    # 11. Compute metrics on training set (issued calls = all opportunities since no abstention beyond filters)
    # Actually, the hypothesis says "issued calls" - all opportunities that pass filters are issued calls
    # Abstention rate = 1 - (issued / total considered)
    # Total considered = all officer purchases with valid dates and disclosure within 5 days
    total_considered = len(filtered_trades)
    issued = len(train_opps)
    abstention_rate = 1 - (issued / total_considered) if total_considered > 0 else 0

    hits = sum(1 for o in train_opps if o['up'] == 1)
    precision = hits / issued if issued > 0 else 0
    base_rate = hits / issued if issued > 0 else 0  # base rate within issued subset

    # Distinct days among issued calls
    distinct_days = len(set(o['trade_date'] for o in train_opps))

    # Effective N: issued / design_effect
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Simplified: cluster by month, compute design effect
    from collections import Counter
    month_counts = Counter(o['trade_date'].strftime('%Y-%m') for o in train_opps)
    if len(month_counts) > 1:
        avg_cluster = sum(month_counts.values()) / len(month_counts)
        # Estimate ICC from data - use 0.1 as conservative estimate for financial returns
        icc = 0.1
        design_effect = 1 + (avg_cluster - 1) * icc
    else:
        design_effect = 1.5  # conservative
    effective_n = issued / design_effect

    # Sealed precision
    sealed_hits = sum(1 for o in sealed_opps if o['up'] == 1)
    sealed_issued = len(sealed_opps)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0

    # Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={total_considered}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    # Verify invariants
    if distinct_days > issued:
        print(f"ERROR: DISTINCT_DAYS ({distinct_days}) > ISSUED ({issued})", file=sys.stderr)
    if effective_n >= issued:
        print(f"ERROR: EFFECTIVE_N ({effective_n}) >= ISSUED ({issued})", file=sys.stderr)

if __name__ == '__main__':
    main()
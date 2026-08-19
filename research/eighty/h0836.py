# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 835
# cycle_index: 31
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def quarter_start(period_str):
    """period_str like '2024-03-31' -> quarter start date string"""
    dt = datetime.strptime(period_str, '%Y-%m-%d')
    month = dt.month
    if month == 3:
        return f'{dt.year}-01-01'
    elif month == 6:
        return f'{dt.year}-04-01'
    elif month == 9:
        return f'{dt.year}-07-01'
    elif month == 12:
        return f'{dt.year}-10-01'
    return None

def quarter_end(period_str):
    return period_str

def add_days(date_str, days):
    dt = datetime.strptime(date_str, '%Y-%m-%d')
    return (dt + timedelta(days=days)).strftime('%Y-%m-%d')

def is_officer(title):
    if not title:
        return False
    t = title.upper()
    return any(k in t for k in ('CEO', 'CFO', 'CHIEF EXECUTIVE', 'CHIEF FINANCIAL'))

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all symbols with market=stocks, active, with daily bars
    cur.execute("""
        SELECT s.id, s.symbol, s.delisted_at
        FROM symbols s
        WHERE s.market = 'stocks' AND s.active = 1
    """)
    symbols = {row['id']: {'symbol': row['symbol'], 'delisted_at': row['delisted_at']} for row in cur.fetchall()}
    if not symbols:
        print('INSUFFICIENT=1')
        return 0

    # Get daily bars for forward returns (tf='1d')
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_by_symbol = {}
    for row in cur.fetchall():
        sid = row['symbol_id']
        if sid not in bars_by_symbol:
            bars_by_symbol[sid] = []
        bars_by_symbol[sid].append((row['ts'], row['close']))

    # Compute 21-day forward returns for each symbol/date
    # 21 trading days ~ 30 calendar days, but we'll use 21 bars forward
    fwd_returns = {}  # (symbol_id, ts) -> fwd_return
    for sid, bars in bars_by_symbol.items():
        if len(bars) < 252:
            continue
        closes = [b[1] for b in bars]
        timestamps = [b[0] for b in bars]
        for i in range(len(bars) - 21):
            ret = (closes[i + 21] - closes[i]) / closes[i]
            fwd_returns[(sid, timestamps[i])] = ret

    # Get insider trades: officer purchases (code='P')
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts, shares, price, title
        FROM insider_trades
        WHERE code = 'P' AND shares > 0 AND price > 0
    """)
    insider_by_symbol = {}
    for row in cur.fetchall():
        if not is_officer(row['title']):
            continue
        sid = row['symbol_id']
        if sid not in insider_by_symbol:
            insider_by_symbol[sid] = []
        trade_val = row['shares'] * row['price']
        insider_by_symbol[sid].append({
            'tx_ts': row['tx_ts'],
            'filed_ts': row['filed_ts'],
            'shares': row['shares'],
            'price': row['price'],
            'value': trade_val,
            'title': row['title']
        })

    # Sort insider trades by tx_ts
    for sid in insider_by_symbol:
        insider_by_symbol[sid].sort(key=lambda x: x['tx_ts'])

    # Get 13F holdings
    cur.execute("""
        SELECT cik, manager, period, symbol_id, value, shares
        FROM inst_holdings
        ORDER BY symbol_id, period, cik, manager
    """)
    holdings = {}
    for row in cur.fetchall():
        sid = row['symbol_id']
        period = row['period']
        key = (row['cik'], row['manager'])
        if sid not in holdings:
            holdings[sid] = {}
        if period not in holdings[sid]:
            holdings[sid][period] = {}
        holdings[sid][period][key] = {'value': row['value'], 'shares': row['shares']}

    # Get sentiment features
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        ORDER BY symbol_id, day
    """)
    sentiment_by_symbol = {}
    for row in cur.fetchall():
        sid = row['symbol_id']
        if sid not in sentiment_by_symbol:
            sentiment_by_symbol[sid] = {}
        sentiment_by_symbol[sid][row['day']] = row['mean_score']

    # Identify long-term holders (8+ consecutive quarters) and their changes
    # For each symbol, get sorted periods
    signals = []  # (decision_date_ts, symbol_id, fwd_ret, is_hit)

    for sid in symbols:
        if sid not in holdings or sid not in bars_by_symbol:
            continue
        if sid not in insider_by_symbol or not insider_by_symbol[sid]:
            continue
        if sid not in sentiment_by_symbol:
            continue

        periods = sorted(holdings[sid].keys())
        if len(periods) < 9:  # need 8 prior + current
            continue

        # For each period starting from index 8 (9th quarter)
        for i in range(8, len(periods)):
            period = periods[i]
            prev_periods = periods[i-8:i]  # 8 prior consecutive quarters

            # Check which institutions held for all 8 prior quarters
            long_term_holders = set()
            for key in holdings[sid][periods[i-8]]:
                held_all = True
                for p in prev_periods[1:]:
                    if key not in holdings[sid][p]:
                        held_all = False
                        break
                if held_all:
                    long_term_holders.add(key)

            if not long_term_holders:
                continue

            # Check if any long-term holder increased shares in current period
            increased = False
            for key in long_term_holders:
                if key in holdings[sid][period]:
                    prev_shares = holdings[sid][prev_periods[-1]][key]['shares']
                    curr_shares = holdings[sid][period][key]['shares']
                    if curr_shares > prev_shares:
                        increased = True
                        break
            if not increased:
                continue

            # Quarter boundaries
            q_start = quarter_start(period)
            q_end = quarter_end(period)
            if not q_start:
                continue

            # Knowable date: period + 45 days
            knowable_date_str = add_days(period, 45)
            # Convert to timestamp (epoch) for comparison with bars
            knowable_dt = datetime.strptime(knowable_date_str, '%Y-%m-%d')
            knowable_ts = int(knowable_dt.timestamp())

            # Check for officer purchase during this quarter (tx_ts in [q_start, q_end])
            q_start_dt = datetime.strptime(q_start, '%Y-%m-%d')
            q_end_dt = datetime.strptime(q_end, '%Y-%m-%d')
            q_start_ts = int(q_start_dt.timestamp())
            q_end_ts = int(q_end_dt.timestamp())

            officer_trade = None
            for trade in insider_by_symbol[sid]:
                if q_start_ts <= trade['tx_ts'] <= q_end_ts:
                    # Check trade size vs personal history (top 50%)
                    prior_trades = [t for t in insider_by_symbol[sid] if t['tx_ts'] < trade['tx_ts']]
                    if len(prior_trades) < 3:
                        continue
                    prior_values = [t['value'] for t in prior_trades]
                    median_val = sorted(prior_values)[len(prior_values)//2]
                    if trade['value'] >= median_val:
                        officer_trade = trade
                        break

            if not officer_trade:
                continue

            # Check news sentiment: avg mean_score over 20 sessions before knowable_date
            # sentiment_features.day is YYYY-MM-DD string
            knowable_date = knowable_dt.strftime('%Y-%m-%d')
            sentiment_days = []
            for d in range(1, 21):
                check_date = add_days(knowable_date, -d)
                if check_date in sentiment_by_symbol[sid]:
                    sentiment_days.append(sentiment_by_symbol[sid][check_date])
            if len(sentiment_days) < 15:
                continue
            avg_sentiment = sum(sentiment_days) / len(sentiment_days)
            if avg_sentiment >= 0:
                continue

            # Find forward return from knowable_ts (next bar after knowable_ts)
            bars = bars_by_symbol[sid]
            timestamps = [b[0] for b in bars]
            # Find first bar with ts > knowable_ts
            idx = None
            for j, ts in enumerate(timestamps):
                if ts > knowable_ts:
                    idx = j
                    break
            if idx is None or idx + 21 >= len(bars):
                continue

            fwd_ret = fwd_returns.get((sid, timestamps[idx]))
            if fwd_ret is None:
                continue

            # Cross-sectional median for this date
            # Collect all eligible symbols' fwd_ret for this knowable_ts
            # We'll compute median later in batch

            signals.append({
                'decision_ts': knowable_ts,
                'symbol_id': sid,
                'fwd_ret': fwd_ret,
                'period': period
            })

    if not signals:
        print('INSUFFICIENT=1')
        return 0

    # Group by decision_ts to compute cross-sectional median
    from collections import defaultdict
    by_date = defaultdict(list)
    for s in signals:
        by_date[s['decision_ts']].append(s['fwd_ret'])

    date_median = {}
    for dt, rets in by_date.items():
        if len(rets) >= 3:
            date_median[dt] = sorted(rets)[len(rets)//2]

    # Determine hits: fwd_ret > median for that date
    issued = 0
    hits = 0
    decision_days = set()
    for s in signals:
        dt = s['decision_ts']
        if dt not in date_median:
            continue
        issued += 1
        decision_days.add(datetime.fromtimestamp(dt).strftime('%Y-%m-%d'))
        if s['fwd_ret'] > date_median[dt]:
            hits += 1

    if issued == 0:
        print('INSUFFICIENT=1')
        return 0

    # Hold out most recent 20% by decision_ts
    sorted_signals = sorted(signals, key=lambda x: x['decision_ts'])
    split_idx = int(len(sorted_signals) * 0.8)
    train_signals = sorted_signals[:split_idx]
    test_signals = sorted_signals[split_idx:]

    def compute_metrics(sig_list):
        if not sig_list:
            return 0, 0, 0, 0
        by_dt = defaultdict(list)
        for s in sig_list:
            by_dt[s['decision_ts']].append(s['fwd_ret'])
        med = {}
        for dt, rets in by_dt.items():
            if len(rets) >= 3:
                med[dt] = sorted(rets)[len(rets)//2]
        iss = 0
        ht = 0
        days = set()
        for s in sig_list:
            dt = s['decision_ts']
            if dt not in med:
                continue
            iss += 1
            days.add(datetime.fromtimestamp(dt).strftime('%Y-%m-%d'))
            if s['fwd_ret'] > med[dt]:
                ht += 1
        return iss, ht, len(days), med

    train_issued, train_hits, train_days, _ = compute_metrics(train_signals)
    test_issued, test_hits, test_days, _ = compute_metrics(test_signals)

    # Design effect: approximate by 1 + (avg_cluster_size - 1) * autocorr
    # Simple proxy: issued / distinct_days
    design_effect = issued / max(1, len(decision_days))
    effective_n = issued / design_effect if design_effect > 1 else issued - 1

    precision = hits / issued if issued else 0
    base_rate = 0.5  # median split -> base rate 0.5
    sealed_precision = test_hits / test_issued if test_issued else 0

    print(f'ISSUED={issued}')
    print(f'OPPORTUNITIES={len(signals)}')
    print(f'PRECISION={precision:.6f}')
    print(f'BASE_RATE={base_rate:.6f}')
    print(f'DISTINCT_DAYS={len(decision_days)}')
    print(f'EFFECTIVE_N={effective_n:.2f}')
    print(f'SEALED_PRECISION={sealed_precision:.6f}')

    return 0

if __name__ == '__main__':
    sys.exit(main())
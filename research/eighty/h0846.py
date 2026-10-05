# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 845
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
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

def slope_5day(values):
    if len(values) != 5:
        return None
    x = [0,1,2,3,4]
    n = 5
    sum_x = sum(x)
    sum_y = sum(values)
    sum_xy = sum(x[i]*values[i] for i in range(n))
    sum_x2 = sum(xi*xi for xi in x)
    denom = n*sum_x2 - sum_x*sum_x
    if denom == 0:
        return 0
    return (n*sum_xy - sum_x*sum_y) / denom

def rolling_volatility(values, window):
    if len(values) < window:
        return []
    result = []
    for i in range(window-1, len(values)):
        window_vals = values[i-window+1:i+1]
        mean = sum(window_vals)/window
        var = sum((v-mean)**2 for v in window_vals)/window
        result.append(math.sqrt(var))
    return result

def percentile_rank(arr, value):
    if not arr:
        return 0.5
    sorted_arr = sorted(arr)
    count = sum(1 for v in sorted_arr if v <= value)
    return count / len(sorted_arr)

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load symbols (active stocks)
    cur.execute("""
        SELECT id, symbol, delisted_at FROM symbols 
        WHERE market='stocks' AND active=1
    """)
    symbols = {row['id']: {'symbol': row['symbol'], 'delisted_at': row['delisted_at']} for row in cur.fetchall()}
    symbol_ids = list(symbols.keys())
    print(f"Loaded {len(symbols)} active stock symbols", file=sys.stderr)

    # 2. Load daily bars (tf='1d') for all symbols
    cur.execute("""
        SELECT symbol_id, ts, close, volume FROM bars 
        WHERE tf='1d' ORDER BY symbol_id, ts
    """)
    bars_by_sym = defaultdict(list)
    for row in cur.fetchall():
        bars_by_sym[row['symbol_id']].append({
            'ts': row['ts'],
            'date': epoch_to_date(row['ts']),
            'close': row['close'],
            'volume': row['volume'],
            'dollar_vol': row['close'] * row['volume']
        })
    print(f"Loaded bars for {len(bars_by_sym)} symbols", file=sys.stderr)

    # 3. Load insider trades for officers (CEO/CFO) open-market purchases (code='P')
    cur.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
        ORDER BY tx_ts
    """)
    insider_trades = []
    for row in cur.fetchall():
        if row['symbol_id'] in symbols:
            insider_trades.append({
                'accession': row['accession'],
                'symbol_id': row['symbol_id'],
                'insider': row['insider'],
                'title': row['title'],
                'tx_ts': row['tx_ts'],
                'tx_date': epoch_to_date(row['tx_ts']),
                'filed_ts': row['filed_ts'],
                'filed_date': epoch_to_date(row['filed_ts']),
                'delay_days': (row['filed_ts'] - row['tx_ts']) / 86400
            })
    print(f"Loaded {len(insider_trades)} officer open-market purchases", file=sys.stderr)

    # 4. Load revenue data from fundamentals (metric='Revenues')
    cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at FROM fundamentals
        WHERE metric='Revenues' AND as_of > 0
        ORDER BY symbol_id, as_of
    """)
    revenue_by_sym = defaultdict(list)
    for row in cur.fetchall():
        if row['symbol_id'] in symbols:
            revenue_by_sym[row['symbol_id']].append({
                'value': row['value'],
                'as_of': row['as_of'],
                'fetched_at': row['fetched_at'],
                'fetched_date': epoch_to_date(row['fetched_at'])
            })
    print(f"Loaded revenue data for {len(revenue_by_sym)} symbols", file=sys.stderr)

    # 5. Load SharesOutstanding from fundamentals
    cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at FROM fundamentals
        WHERE metric='SharesOutstanding' AND as_of > 0
        ORDER BY symbol_id, as_of
    """)
    shares_out_by_sym = defaultdict(list)
    for row in cur.fetchall():
        if row['symbol_id'] in symbols:
            shares_out_by_sym[row['symbol_id']].append({
                'value': row['value'],
                'as_of': row['as_of'],
                'fetched_at': row['fetched_at'],
                'fetched_date': epoch_to_date(row['fetched_at'])
            })
    print(f"Loaded SharesOutstanding for {len(shares_out_by_sym)} symbols", file=sys.stderr)

    # 6. Load sentiment_features
    cur.execute("""
        SELECT symbol_id, day, mean_score FROM sentiment_features
        ORDER BY symbol_id, day
    """)
    sentiment_by_sym = defaultdict(list)
    for row in cur.fetchall():
        if row['symbol_id'] in symbols:
            sentiment_by_sym[row['symbol_id']].append({
                'day': row['day'],
                'date': str_to_date(row['day']),
                'mean_score': row['mean_score']
            })
    print(f"Loaded sentiment for {len(sentiment_by_sym)} symbols", file=sys.stderr)

    # 7. Load 13F institutional holdings
    cur.execute("""
        SELECT symbol_id, period, SUM(shares) as inst_shares FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    inst_holdings_by_sym = defaultdict(list)
    for row in cur.fetchall():
        if row['symbol_id'] in symbols:
            inst_holdings_by_sym[row['symbol_id']].append({
                'period': row['period'],
                'period_date': str_to_date(row['period']),
                'inst_shares': row['inst_shares']
            })
    print(f"Loaded 13F for {len(inst_holdings_by_sym)} symbols", file=sys.stderr)

    # Helper: get value as of date from time-series list
    def get_as_of(data_list, target_date, date_key='fetched_date', value_key='value'):
        best = None
        for item in data_list:
            if item[date_key] <= target_date:
                best = item[value_key]
            else:
                break
        return best

    def get_as_of_ts(data_list, target_ts, ts_key='fetched_at', value_key='value'):
        best = None
        for item in data_list:
            if item[ts_key] <= target_ts:
                best = item[value_key]
            else:
                break
        return best

    # For each symbol, compute quarterly revenue YoY growth
    revenue_growth_by_sym = {}
    for sid, rev_list in revenue_by_sym.items():
        quarters = []
        for r in rev_list:
            quarters.append({'as_of': r['as_of'], 'value': r['value'], 'fetched_at': r['fetched_at']})
        quarters.sort(key=lambda x: x['as_of'])
        growth = []
        for i, q in enumerate(quarters):
            target_as_of = q['as_of'] - 365*24*3600
            prior_val = None
            for pq in quarters:
                if pq['as_of'] <= target_as_of:
                    prior_val = pq['value']
                else:
                    break
            if prior_val and prior_val > 0:
                yoy = (q['value'] - prior_val) / prior_val
                growth.append({'as_of': q['as_of'], 'fetched_at': q['fetched_at'], 'yoy': yoy})
        revenue_growth_by_sym[sid] = growth

    # For each symbol, compute SharesOutstanding quarterly change
    shares_change_by_sym = {}
    for sid, so_list in shares_out_by_sym.items():
        quarters = []
        for s in so_list:
            quarters.append({'as_of': s['as_of'], 'value': s['value'], 'fetched_at': s['fetched_at']})
        quarters.sort(key=lambda x: x['as_of'])
        changes = []
        for i, q in enumerate(quarters):
            if i >= 1:
                prev = quarters[i-1]['value']
                if prev > 0:
                    chg = (q['value'] - prev) / prev
                    changes.append({'as_of': q['as_of'], 'fetched_at': q['fetched_at'], 'chg': chg})
        shares_change_by_sym[sid] = changes

    # For each symbol, compute sentiment volatility and slope
    sentiment_stats_by_sym = {}
    for sid, sent_list in sentiment_by_sym.items():
        sent_list.sort(key=lambda x: x['date'])
        scores = [s['mean_score'] for s in sent_list]
        dates = [s['date'] for s in sent_list]
        vol_126 = rolling_volatility(scores, 126)
        vol_252 = rolling_volatility(scores, 252)
        slope_5 = []
        for i in range(4, len(scores)):
            slope_5.append(slope_5day(scores[i-4:i+1]))
        sentiment_stats_by_sym[sid] = {
            'dates': dates,
            'scores': scores,
            'vol_126': vol_126,
            'vol_252': vol_252,
            'slope_5': slope_5
        }

    # For each symbol, compute 20-day avg dollar volume
    dollar_vol_20_by_sym = {}
    for sid, bars in bars_by_sym.items():
        bars.sort(key=lambda x: x['ts'])
        dv = [b['dollar_vol'] for b in bars]
        dates = [b['date'] for b in bars]
        avg_20 = []
        for i in range(19, len(dv)):
            avg_20.append(sum(dv[i-19:i+1]) / 20)
        dollar_vol_20_by_sym[sid] = {'dates': dates[19:], 'avg_20': avg_20}

    # For each symbol, compute institutional ownership % (with 45-day lag)
    inst_ownership_by_sym = {}
    for sid, inst_list in inst_holdings_by_sym.items():
        inst_list.sort(key=lambda x: x['period_date'])
        # Get shares outstanding at each period
        so_list = shares_out_by_sym.get(sid, [])
        ownership = []
        for inst in inst_list:
            period_date = inst['period_date']
            # 45-day lag: info available 45 days after period end
            available_date = period_date + timedelta(days=45)
            so = get_as_of(so_list, available_date, 'fetched_date', 'value')
            if so and so > 0:
                pct = inst['inst_shares'] / so
                ownership.append({'period_date': period_date, 'available_date': available_date, 'pct': pct})
        inst_ownership_by_sym[sid] = ownership

    # Build officer sale history for abstention check
    cur.execute("""
        SELECT symbol_id, insider, tx_ts FROM insider_trades
        WHERE code='S' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
        ORDER BY tx_ts
    """)
    officer_sales = defaultdict(list)
    for row in cur.fetchall():
        if row['symbol_id'] in symbols:
            officer_sales[row['symbol_id']].append({
                'insider': row['insider'],
                'tx_ts': row['tx_ts'],
                'tx_date': epoch_to_date(row['tx_ts'])
            })

    # Now evaluate each insider trade as a potential entry
    HORIZON_DAYS = 21  # trading days
    HORIZON_SECONDS = HORIZON_DAYS * 86400  # approximate

    calls = []  # (symbol_id, tx_ts, tx_date, entry_price, horizon_end_ts, horizon_end_date)
    opportunities = 0

    for trade in insider_trades:
        opportunities += 1
        sid = trade['symbol_id']
        tx_ts = trade['tx_ts']
        tx_date = trade['tx_date']
        delay_days = trade['delay_days']

        # ABSTAIN checks
        if delay_days > 5:
            continue
        # Check if same officer sold in prior 252 sessions
        officer_sold = False
        for sale in officer_sales.get(sid, []):
            if sale['insider'] == trade['insider'] and sale['tx_ts'] < tx_ts and (tx_ts - sale['tx_ts']) <= 252*86400:
                officer_sold = True
                break
        if officer_sold:
            continue

        # Check 20-day avg dollar volume >= $1M
        dv_data = dollar_vol_20_by_sym.get(sid)
        if not dv_data:
            continue
        # Find latest avg_20 as of tx_date
        dv_idx = -1
        for i, d in enumerate(dv_data['dates']):
            if d <= tx_date:
                dv_idx = i
            else:
                break
        if dv_idx < 0 or dv_data['avg_20'][dv_idx] < 1_000_000:
            continue

        # Check revenue growth acceleration: 3+ consecutive quarters of increasing YoY growth
        rev_growth = revenue_growth_by_sym.get(sid, [])
        if len(rev_growth) < 4:
            continue
        # Find latest growth as of tx_ts (using fetched_at)
        latest_growth = []
        for g in rev_growth:
            if g['fetched_at'] <= tx_ts:
                latest_growth.append(g['yoy'])
            else:
                break
        if len(latest_growth) < 4:
            continue
        # Check last 4 quarters: each YoY > previous
        accelerated = True
        for i in range(1, 4):
            if latest_growth[-i] <= latest_growth[-i-1]:
                accelerated = False
                break
        if not accelerated:
            continue

        # Check sentiment volatility: 126-day vol in bottom 20% of 252-day history
        sent_stats = sentiment_stats_by_sym.get(sid)
        if not sent_stats:
            continue
        # Find index for tx_date
        sent_idx = -1
        for i, d in enumerate(sent_stats['dates']):
            if d <= tx_date:
                sent_idx = i
            else:
                break
        if sent_idx < 125:  # need at least 126 days for vol_126
            continue
        vol_126_idx = sent_idx - 125  # vol_126 aligned to end of window
        if vol_126_idx >= len(sent_stats['vol_126']):
            continue
        current_vol_126 = sent_stats['vol_126'][vol_126_idx]
        # Get 252-day history of vol_126 for percentile
        hist_start = max(0, vol_126_idx - 251)
        hist_vols = sent_stats['vol_126'][hist_start:vol_126_idx+1]
        if len(hist_vols) < 50:
            continue
        pct_rank = percentile_rank(hist_vols, current_vol_126)
        if pct_rank > 0.20:  # bottom quintile
            continue

        # Check 5-day sentiment slope positive
        if sent_idx < 4:
            continue
        slope_idx = sent_idx - 4
        if slope_idx >= len(sent_stats['slope_5']):
            continue
        if sent_stats['slope_5'][slope_idx] <= 0:
            continue

        # Check institutional ownership in bottom tercile
        inst_own = inst_ownership_by_sym.get(sid, [])
        if not inst_own:
            continue
        # Find latest ownership as of tx_date (available_date <= tx_date)
        latest_pct = None
        for own in inst_own:
            if own['available_date'] <= tx_date:
                latest_pct = own['pct']
            else:
                break
        if latest_pct is None:
            continue
        # Get cross-sectional tercile: need all symbols' latest ownership as of this date
        # For simplicity, compute universe-wide tercile at this date
        all_pcts = []
        for other_sid, other_own in inst_ownership_by_sym.items():
            for own in other_own:
                if own['available_date'] <= tx_date:
                    all_pcts.append(own['pct'])
                else:
                    break
        if len(all_pcts) < 10:
            continue
        tercile_cutoff = sorted(all_pcts)[len(all_pcts)//3]
        if latest_pct > tercile_cutoff:
            continue

        # All conditions met - issue call
        # Get entry price from bars (close on tx_date)
        entry_price = None
        for b in bars_by_sym.get(sid, []):
            if b['date'] == tx_date:
                entry_price = b['close']
                break
            elif b['date'] > tx_date:
                break
        if not entry_price:
            continue

        horizon_end_ts = tx_ts + HORIZON_SECONDS
        horizon_end_date = epoch_to_date(horizon_end_ts)

        calls.append({
            'symbol_id': sid,
            'tx_ts': tx_ts,
            'tx_date': tx_date,
            'entry_price': entry_price,
            'horizon_end_ts': horizon_end_ts,
            'horizon_end_date': horizon_end_date
        })

    print(f"Generated {len(calls)} calls from {opportunities} opportunities", file=sys.stderr)

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Get forward returns from bars for each call
    hits = 0
    issued_dates = set()
    for call in calls:
        sid = call['symbol_id']
        entry_price = call['entry_price']
        horizon_end_ts = call['horizon_end_ts']
        # Find close price at or after horizon_end_ts
        exit_price = None
        for b in bars_by_sym.get(sid, []):
            if b['ts'] >= horizon_end_ts:
                exit_price = b['close']
                break
        if exit_price:
            fwd_return = (exit_price - entry_price) / entry_price
            if fwd_return > 0:
                hits += 1
        issued_dates.add(call['tx_date'])

    # Determine sealed era (most recent 20% of calls by tx_ts)
    calls_sorted = sorted(calls, key=lambda c: c['tx_ts'])
    n_sealed = max(1, len(calls_sorted) // 5)
    sealed_calls = calls_sorted[-n_sealed:]
    main_calls = calls_sorted[:-n_sealed]

    sealed_hits = 0
    for call in sealed_calls:
        sid = call['symbol_id']
        entry_price = call['entry_price']
        horizon_end_ts = call['horizon_end_ts']
        exit_price = None
        for b in bars_by_sym.get(sid, []):
            if b['ts'] >= horizon_end_ts:
                exit_price = b['close']
                break
        if exit_price:
            fwd_return = (exit_price - entry_price) / entry_price
            if fwd_return > 0:
                sealed_hits += 1

    # Compute design effect for EFFECTIVE_N
    # Cluster by date: count calls per date
    date_counts = defaultdict(int)
    for call in calls:
        date_counts[call['tx_date']] += 1
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Approximate ICC from data: use variance of cluster means / total variance
    # Simplified: deff = 1 + (mean_cluster_size - 1) * 0.1 (conservative)
    cluster_sizes = list(date_counts.values())
    mean_cluster = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
    deff = 1 + (mean_cluster - 1) * 0.1
    effective_n = len(calls) / deff

    issued = len(calls)
    precision = hits / issued if issued > 0 else 0
    base_rate = hits / issued if issued > 0 else 0  # base rate of positive class in issued subset
    distinct_days = len(issued_dates)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
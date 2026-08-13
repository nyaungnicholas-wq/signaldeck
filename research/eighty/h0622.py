# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 621
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def connect_db():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def get_quarter(ts):
    d = ts_to_date(ts)
    return (d.year, (d.month - 1) // 3 + 1)

def quarter_key(year, q):
    return year * 10 + q

def prev_quarter(year, q):
    if q == 1:
        return year - 1, 4
    return year, q - 1

def is_business_day(d):
    return d.weekday() < 5

def add_business_days(start_date, n):
    d = start_date
    count = 0
    while count < n:
        d += timedelta(days=1)
        if is_business_day(d):
            count += 1
    return d

def business_days_between(start, end):
    if start > end:
        return 0
    count = 0
    d = start
    while d <= end:
        if is_business_day(d):
            count += 1
        d += timedelta(days=1)
    return count

def main():
    conn = connect_db()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all open-market insider purchases (code='P')
    cur.execute("""
        SELECT accession, symbol_id, tx_ts, filed_ts, shares, price, value
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    insider_trades = cur.fetchall()
    if not insider_trades:
        print("INSUFFICIENT=1")
        return

    # Get symbols info
    cur.execute("SELECT id, symbol, delisted_at FROM symbols")
    symbols = {row['id']: row for row in cur.fetchall()}

    # Get news headlines per symbol per quarter (ts <= filed_ts at decision time)
    # We'll fetch all news and filter per decision
    cur.execute("SELECT symbol_id, ts FROM news ORDER BY symbol_id, ts")
    all_news = cur.fetchall()
    news_by_symbol = defaultdict(list)
    for row in all_news:
        news_by_symbol[row['symbol_id']].append(row['ts'])

    # Get fundamentals (EPS, Revenues) per symbol per quarter with fetched_at
    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('EPS', 'Revenues')
        ORDER BY symbol_id, metric, as_of
    """)
    all_fundamentals = cur.fetchall()
    fund_by_symbol = defaultdict(lambda: defaultdict(list))
    for row in all_fundamentals:
        fund_by_symbol[row['symbol_id']][row['metric']].append({
            'value': row['value'],
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at']
        })

    # Get bars for liquidity (1d bars for dollar volume)
    cur.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf = '1d' ORDER BY symbol_id, ts")
    all_bars = cur.fetchall()
    bars_by_symbol = defaultdict(list)
    for row in all_bars:
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'close': row['close'],
            'volume': row['volume']
        })

    # Get prediction outcomes for horizon=21 (assuming horizon is in trading days)
    cur.execute("""
        SELECT symbol_id, horizon, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
        ORDER BY symbol_id, ts
    """)
    outcomes = cur.fetchall()
    outcomes_by_symbol = defaultdict(list)
    for row in outcomes:
        outcomes_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'up': row['up'],
            'fwd_return': row['fwd_return']
        })

    # Process each insider trade as a decision point
    decisions = []
    for trade in insider_trades:
        symbol_id = trade['symbol_id']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']
        tx_date = ts_to_date(tx_ts)
        filed_date = ts_to_date(filed_ts)

        # Check filing delay <= 5 business days
        if business_days_between(tx_date, filed_date) > 5:
            continue

        # Check symbol exists and not delisted before filed_ts
        sym = symbols.get(symbol_id)
        if not sym:
            continue
        if sym['delisted_at'] and sym['delisted_at'] <= filed_ts:
            continue

        # Liquidity: 20-day median dollar volume > $1M at entry (using bars up to filed_ts)
        bars = bars_by_symbol.get(symbol_id, [])
        recent_bars = [b for b in bars if b['ts'] <= filed_ts][-20:]
        if len(recent_bars) < 20:
            continue
        dollar_vols = [b['close'] * b['volume'] for b in recent_bars]
        dollar_vols.sort()
        median_dv = dollar_vols[len(dollar_vols) // 2]
        if median_dv <= 1_000_000:
            continue

        # News coverage: need at least 12 quarters of history up to filed_ts
        news_ts_list = [ts for ts in news_by_symbol.get(symbol_id, []) if ts <= filed_ts]
        if len(news_ts_list) == 0:
            continue
        news_by_q = defaultdict(int)
        for ts in news_ts_list:
            y, q = get_quarter(ts)
            news_by_q[quarter_key(y, q)] += 1
        # Get sorted quarters
        sorted_qs = sorted(news_by_q.keys())
        if len(sorted_qs) < 12:
            continue
        # Check last 3+ consecutive quarters declined
        news_declined = False
        for i in range(len(sorted_qs) - 2):
            q1, q2, q3 = sorted_qs[i], sorted_qs[i+1], sorted_qs[i+2]
            # Check if consecutive quarters
            y1, qq1 = divmod(q1, 10)
            y2, qq2 = divmod(q2, 10)
            y3, qq3 = divmod(q3, 10)
            if (y2 == y1 and qq2 == qq1 + 1) or (y2 == y1 + 1 and qq1 == 4 and qq2 == 1):
                if (y3 == y2 and qq3 == qq2 + 1) or (y3 == y2 + 1 and qq2 == 4 and qq3 == 1):
                    if news_by_q[q1] > news_by_q[q2] > news_by_q[q3]:
                        news_declined = True
                        break
        if not news_declined:
            continue

        # Fundamentals: EPS and Revenue increased for 3+ consecutive quarters
        # Only use fundamentals with fetched_at <= filed_ts
        eps_data = []
        for f in fund_by_symbol.get(symbol_id, {}).get('EPS', []):
            if f['fetched_at'] <= filed_ts:
                y, q = get_quarter(f['as_of'])
                eps_data.append((quarter_key(y, q), f['value']))
        rev_data = []
        for f in fund_by_symbol.get(symbol_id, {}).get('Revenues', []):
            if f['fetched_at'] <= filed_ts:
                y, q = get_quarter(f['as_of'])
                rev_data.append((quarter_key(y, q), f['value']))

        eps_data.sort()
        rev_data.sort()

        if len(eps_data) < 12 or len(rev_data) < 12:
            continue

        # Check EPS increased for 3+ consecutive quarters
        eps_increased = False
        for i in range(len(eps_data) - 2):
            q1, v1 = eps_data[i]
            q2, v2 = eps_data[i+1]
            q3, v3 = eps_data[i+2]
            y1, qq1 = divmod(q1, 10)
            y2, qq2 = divmod(q2, 10)
            y3, qq3 = divmod(q3, 10)
            if (y2 == y1 and qq2 == qq1 + 1) or (y2 == y1 + 1 and qq1 == 4 and qq2 == 1):
                if (y3 == y2 and qq3 == qq2 + 1) or (y3 == y2 + 1 and qq2 == 4 and qq3 == 1):
                    if v1 < v2 < v3:
                        eps_increased = True
                        break
        if not eps_increased:
            continue

        # Check Revenue increased for 3+ consecutive quarters
        rev_increased = False
        for i in range(len(rev_data) - 2):
            q1, v1 = rev_data[i]
            q2, v2 = rev_data[i+1]
            q3, v3 = rev_data[i+2]
            y1, qq1 = divmod(q1, 10)
            y2, qq2 = divmod(q2, 10)
            y3, qq3 = divmod(q3, 10)
            if (y2 == y1 and qq2 == qq1 + 1) or (y2 == y1 + 1 and qq1 == 4 and qq2 == 1):
                if (y3 == y2 and qq3 == qq2 + 1) or (y3 == y2 + 1 and qq2 == 4 and qq3 == 1):
                    if v1 < v2 < v3:
                        rev_increased = True
                        break
        if not rev_increased:
            continue

        # All conditions met - this is an issued call
        decisions.append({
            'symbol_id': symbol_id,
            'filed_ts': filed_ts,
            'filed_date': filed_date,
            'tx_ts': tx_ts,
            'accession': trade['accession']
        })

    if not decisions:
        print("INSUFFICIENT=1")
        return

    # Match with outcomes (prediction_outcomes at horizon=21)
    # Find outcome where outcome.ts == decision.filed_ts (or closest?)
    # prediction_outcomes.ts is the prediction timestamp, should match decision time
    results = []
    for d in decisions:
        symbol_id = d['symbol_id']
        filed_ts = d['filed_ts']
        # Find matching outcome
        matched = None
        for o in outcomes_by_symbol.get(symbol_id, []):
            if o['ts'] == filed_ts:
                matched = o
                break
        if matched is None:
            # Try closest before? But as-of discipline says we need label at horizon
            # prediction_outcomes should have the forward return resolved
            continue
        results.append({
            'symbol_id': symbol_id,
            'filed_ts': filed_ts,
            'filed_date': d['filed_date'],
            'up': matched['up'],
            'fwd_return': matched['fwd_return']
        })

    if not results:
        print("INSUFFICIENT=1")
        return

    # Hold out most recent 20% by filed_ts as sealed era
    results.sort(key=lambda x: x['filed_ts'])
    n_total = len(results)
    n_sealed = max(1, int(n_total * 0.2))
    sealed = results[-n_sealed:]
    main_results = results[:-n_sealed]

    def compute_metrics(res_list):
        if not res_list:
            return None
        issued = len(res_list)
        hits = sum(1 for r in res_list if r['up'] == 1)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0  # base rate of predicted class (up=1) within issued
        distinct_days = len(set(r['filed_date'] for r in res_list))
        # Design effect: cluster by day, compute effective N
        day_counts = defaultdict(int)
        for r in res_list:
            day_counts[r['filed_date']] += 1
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Simplified: effective_n = issued / design_effect, design_effect > 1
        # Use Kish's effective sample size: n_eff = (sum w_i)^2 / sum w_i^2 where w_i = 1/cluster_size
        # For equal weight per observation but clustered: n_eff = n / (1 + (m-1)*rho)
        # Approximate: if all clusters same size m, n_eff = n * (1 - rho) / (1 + (m-1)*rho)
        # Conservative: assume ICC=0.5, m = avg cluster size
        if len(day_counts) > 1:
            avg_cluster = issued / len(day_counts)
            icc = 0.5
            design_effect = 1 + (avg_cluster - 1) * icc
            effective_n = issued / design_effect
        else:
            effective_n = 1.0
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    main_metrics = compute_metrics(main_results)
    sealed_metrics = compute_metrics(sealed)

    if main_metrics is None:
        print("INSUFFICIENT=1")
        return

    # Opportunities = decision points considered (all insider trades passing filters before outcome match)
    # Actually opportunities should be all decision points where we could have issued (all insider trades meeting entry criteria)
    # But we only have results for those with matched outcomes
    # The spec says OPPORTUNITIES = count of decision points considered
    # Let's count all insider trades that passed the 5-day filter and had enough data (before outcome matching)
    opportunities = len(decisions)

    print(f"ISSUED={main_metrics['issued']}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={main_metrics['precision']:.6f}")
    print(f"BASE_RATE={main_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={main_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={main_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}" if sealed_metrics else "SEALED_PRECISION=0.000000")

if __name__ == '__main__':
    main()
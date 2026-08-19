# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 664
# cycle_index: 20
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import time
from datetime import datetime, timedelta
from collections import defaultdict
import bisect

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(time.mktime(d.timetuple()))

def parse_day_str(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def day_str(d):
    return d.strftime('%Y-%m-%d')

def business_days_between(start_ts, end_ts):
    start = unix_to_date(start_ts)
    end = unix_to_date(end_ts)
    if end < start:
        return 0
    days = 0
    cur = start
    while cur <= end:
        if cur.weekday() < 5:
            days += 1
        cur += timedelta(days=1)
    return days

def add_business_days(start_ts, n):
    d = unix_to_date(start_ts)
    added = 0
    while added < n:
        d += timedelta(days=1)
        if d.weekday() < 5:
            added += 1
    return date_to_unix(d)

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load symbols
    cur.execute("SELECT id, symbol, market, delisted_at FROM symbols WHERE market='stocks'")
    symbols = {row['id']: dict(row) for row in cur.fetchall()}
    symbol_ids = set(symbols.keys())

    # Load insider sales (code='S') with filed_ts in 2012-2024
    start_ts = date_to_unix(datetime(2012, 1, 1).date())
    end_ts = date_to_unix(datetime(2024, 12, 31).date())
    cur.execute("""
        SELECT accession, symbol_id, insider, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='S' AND filed_ts BETWEEN ? AND ? AND symbol_id IN ({})
    """.format(','.join('?'*len(symbol_ids))), [start_ts, end_ts] + list(symbol_ids))
    insider_sales = [dict(row) for row in cur.fetchall()]
    if not insider_sales:
        print("INSUFFICIENT=1")
        return

    # Group by insider for 12-month median sale size
    sales_by_insider = defaultdict(list)
    for s in insider_sales:
        sales_by_insider[(s['symbol_id'], s['insider'])].append(s)

    insider_median = {}
    for key, sales in sales_by_insider.items():
        sales_sorted = sorted(sales, key=lambda x: x['filed_ts'])
        values = [s['value'] for s in sales_sorted]
        # For each sale, compute median of prior 12 months
        for i, s in enumerate(sales_sorted):
            cutoff = s['filed_ts'] - 365*24*3600
            prior_vals = [v for v in values[:i] if sales_sorted[values.index(v)]['filed_ts'] >= cutoff]
            if prior_vals:
                prior_vals.sort()
                insider_median[(key, s['accession'])] = prior_vals[len(prior_vals)//2]
            else:
                insider_median[(key, s['accession'])] = None

    # Load sentiment_features (daily)
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({})
    """.format(','.join('?'*len(symbol_ids))), list(symbol_ids))
    sent_rows = cur.fetchall()
    sent_by_sym = defaultdict(list)
    for r in sent_rows:
        sent_by_sym[r['symbol_id']].append((parse_day_str(r['day']), r['mean_score']))
    for sym in sent_by_sym:
        sent_by_sym[sym].sort(key=lambda x: x[0])

    # Load fundamentals (EPS, Revenues)
    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('EPS','Revenues') AND symbol_id IN ({})
    """.format(','.join('?'*len(symbol_ids))), list(symbol_ids))
    fund_rows = cur.fetchall()
    fund_by_sym = defaultdict(lambda: defaultdict(list))
    for r in fund_rows:
        fund_by_sym[r['symbol_id']][r['metric']].append((r['as_of'], r['fetched_at'], r['value']))
    for sym in fund_by_sym:
        for met in fund_by_sym[sym]:
            fund_by_sym[sym][met].sort(key=lambda x: x[1])  # sort by fetched_at

    # Load bars 1d for price data
    cur.execute("""
        SELECT symbol_id, ts, close, high
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({})
    """.format(','.join('?'*len(symbol_ids))), list(symbol_ids))
    bar_rows = cur.fetchall()
    bars_by_sym = defaultdict(list)
    for r in bar_rows:
        bars_by_sym[r['symbol_id']].append((r['ts'], r['close'], r['high']))
    for sym in bars_by_sym:
        bars_by_sym[sym].sort(key=lambda x: x[0])

    # Load inst_holdings for 13F
    cur.execute("""
        SELECT symbol_id, period, value
        FROM inst_holdings
        WHERE symbol_id IN ({})
    """.format(','.join('?'*len(symbol_ids))), list(symbol_ids))
    hold_rows = cur.fetchall()
    holds_by_sym = defaultdict(list)
    for r in hold_rows:
        holds_by_sym[r['symbol_id']].append((r['period'], r['value']))
    for sym in holds_by_sym:
        holds_by_sym[sym].sort(key=lambda x: x[0])

    # Helper: get sentiment slope condition
    def check_sentiment_condition(symbol_id, decision_date):
        """decision_date is a date object (filed_ts date). Check 20-day slope >0 for 15+ consecutive days, 5-day MA rising."""
        sent = sent_by_sym.get(symbol_id, [])
        if not sent:
            return False
        # Find index of decision_date
        idx = bisect.bisect_right(sent, (decision_date, float('inf'))) - 1
        if idx < 19:  # need 20 days
            return False
        # Compute 20-day slope for each day ending at idx, idx-1, ...
        slopes = []
        for i in range(idx, 19-1, -1):
            window = sent[i-19:i+1]
            x = list(range(20))
            y = [v for _, v in window]
            # linear regression slope
            n = 20
            sum_x = sum(x)
            sum_y = sum(y)
            sum_xy = sum(x[j]*y[j] for j in range(n))
            sum_x2 = sum(x[j]*x[j] for j in range(n))
            denom = n*sum_x2 - sum_x*sum_x
            if denom == 0:
                slope = 0
            else:
                slope = (n*sum_xy - sum_x*sum_y) / denom
            slopes.append(slope)
        slopes.reverse()  # now slopes[i] corresponds to sent[19+i]
        # Check 5-day MA rising
        ma5 = []
        for i in range(19, len(sent)):
            ma5.append(sum(v for _, v in sent[i-4:i+1]) / 5)
        ma5_rising = [ma5[i] > ma5[i-1] for i in range(1, len(ma5))]
        # Need slope > 0 for 15+ consecutive days AND 5-day MA rising on those days
        # Align: slopes index 0 corresponds to sent[19], ma5 index 0 corresponds to sent[19]
        # ma5_rising index 0 corresponds to sent[20] vs sent[19]
        consecutive = 0
        for i in range(len(slopes)):
            if slopes[i] > 0 and (i == 0 or ma5_rising[i-1]):
                consecutive += 1
                if consecutive >= 15:
                    return True
            else:
                consecutive = 0
        return False

    # Helper: get latest quarterly EPS and Revenue YoY as of decision_ts (filed_ts)
    def get_fundamentals_yoy(symbol_id, decision_ts):
        eps_data = fund_by_sym.get(symbol_id, {}).get('EPS', [])
        rev_data = fund_by_sym.get(symbol_id, {}).get('Revenues', [])
        if not eps_data or not rev_data:
            return None, None
        # Filter by fetched_at <= decision_ts
        eps_avail = [(as_of, val) for as_of, fetched, val in eps_data if fetched <= decision_ts]
        rev_avail = [(as_of, val) for as_of, fetched, val in rev_data if fetched <= decision_ts]
        if not eps_avail or not rev_avail:
            return None, None
        # Get most recent quarter for each
        eps_latest = max(eps_avail, key=lambda x: x[0])
        rev_latest = max(rev_avail, key=lambda x: x[0])
        # Find same quarter prior year (as_of - ~365 days)
        eps_yoy = None
        for as_of, val in eps_avail:
            if abs(as_of - (eps_latest[0] - 365*24*3600)) < 45*24*3600:  # within 45 days
                if eps_latest[1] != 0:
                    eps_yoy = (val - eps_latest[1]) / abs(eps_latest[1])
                break
        rev_yoy = None
        for as_of, val in rev_avail:
            if abs(as_of - (rev_latest[0] - 365*24*3600)) < 45*24*3600:
                if rev_latest[1] != 0:
                    rev_yoy = (val - rev_latest[1]) / abs(rev_latest[1])
                break
        return eps_yoy, rev_yoy

    # Helper: check 13F institutional ownership QoQ increase >5%
    def check_13f_condition(symbol_id, decision_ts):
        holds = holds_by_sym.get(symbol_id, [])
        if len(holds) < 2:
            return False
        # Lag 45 days: only use periods where period <= decision_ts - 45 days
        cutoff_ts = decision_ts - 45*24*3600
        avail = [(p, v) for p, v in holds if p <= cutoff_ts]
        if len(avail) < 2:
            return False
        latest = avail[-1]
        prev = avail[-2]
        if prev[1] == 0:
            return False
        qoq = (latest[1] - prev[1]) / prev[1]
        return qoq > 0.05

    # Helper: check price within 5% of 52-week high
    def check_52w_high(symbol_id, decision_ts):
        bars = bars_by_sym.get(symbol_id, [])
        if not bars:
            return True  # abstain if no data
        # Find bar at or before decision_ts
        idx = bisect.bisect_right(bars, (decision_ts, float('inf'), float('inf'))) - 1
        if idx < 0:
            return True
        # Need 252 trading days prior
        if idx < 251:
            return True
        current_close = bars[idx][1]
        max_high = max(bars[i][2] for i in range(idx-251, idx+1))
        if max_high == 0:
            return True
        return current_close >= 0.95 * max_high

    # Helper: get 21-trading-day forward return
    def get_fwd_return_21d(symbol_id, decision_ts):
        bars = bars_by_sym.get(symbol_id, [])
        if not bars:
            return None
        idx = bisect.bisect_right(bars, (decision_ts, float('inf'), float('inf'))) - 1
        if idx < 0:
            return None
        # Use next trading day's close as entry? Hypothesis says "disclosed on day T"
        # Use close of decision day (or next if decision_ts is after close)
        # For simplicity, use close at idx (day of filing)
        entry_close = bars[idx][1]
        # Find bar 21 trading days later
        target_idx = idx + 21
        if target_idx >= len(bars):
            return None
        exit_close = bars[target_idx][1]
        if entry_close == 0:
            return None
        return (exit_close - entry_close) / entry_close

    # Process each insider sale
    opportunities = []
    issued_calls = []

    for sale in insider_sales:
        sym_id = sale['symbol_id']
        filed_ts = sale['filed_ts']
        tx_ts = sale['tx_ts']
        value = sale['value']
        accession = sale['accession']
        insider = sale['insider']

        # Disclosure delay <= 5 business days
        if business_days_between(tx_ts, filed_ts) > 5:
            continue

        # Sale value > insider's 12-month median open-market sale size
        median_val = insider_median.get(((sym_id, insider), accession))
        if median_val is None or value <= median_val:
            continue

        # Sentiment condition
        decision_date = unix_to_date(filed_ts)
        if not check_sentiment_condition(sym_id, decision_date):
            continue

        # Fundamentals: EPS YoY <= 0% AND Revenue YoY <= 5%
        eps_yoy, rev_yoy = get_fundamentals_yoy(sym_id, filed_ts)
        if eps_yoy is None or rev_yoy is None:
            continue
        if not (eps_yoy <= 0 and rev_yoy <= 0.05):
            continue

        # All entry conditions met - this is an opportunity
        opportunities.append({
            'symbol_id': sym_id,
            'filed_ts': filed_ts,
            'accession': accession,
        })

        # Check abstain conditions
        # 1. Fewer than 2 qualifying sales in the quarter
        quarter_start = datetime(decision_date.year, (decision_date.month-1)//3*3 + 1, 1).date()
        quarter_end = (datetime(quarter_start.year + (quarter_start.month==12), (quarter_start.month%12)+1, 1).date() - timedelta(days=1))
        q_start_ts = date_to_unix(quarter_start)
        q_end_ts = date_to_unix(quarter_end)
        # Count qualifying sales in this quarter for this symbol
        qual_in_quarter = sum(1 for o in opportunities if o['symbol_id']==sym_id and q_start_ts <= o['filed_ts'] <= q_end_ts)
        if qual_in_quarter < 2:
            continue

        # 2. 13F institutional ownership increased >5% QoQ
        if check_13f_condition(sym_id, filed_ts):
            continue

        # 3. Price within 5% of 52-week high
        if check_52w_high(sym_id, filed_ts):
            continue

        # All abstain conditions passed - issue call
        fwd_ret = get_fwd_return_21d(sym_id, filed_ts)
        if fwd_ret is None:
            continue
        # Down call: predict negative return
        hit = 1 if fwd_ret < 0 else 0
        issued_calls.append({
            'symbol_id': sym_id,
            'filed_ts': filed_ts,
            'decision_date': decision_date,
            'fwd_ret': fwd_ret,
            'hit': hit,
        })

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # Sort issued calls by filed_ts
    issued_calls.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    in_sample = issued_calls[:-n_sealed]
    sealed = issued_calls[-n_sealed:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0
        issued = len(calls)
        hits = sum(c['hit'] for c in calls)
        precision = hits / issued if issued else 0
        base_rate = hits / issued if issued else 0  # base rate of predicted class (down) within issued
        distinct_days = len(set(c['decision_date'] for c in calls))
        # Design effect: cluster by month
        month_counts = defaultdict(int)
        for c in calls:
            month_key = (c['decision_date'].year, c['decision_date'].month)
            month_counts[month_key] += 1
        if len(month_counts) > 1:
            mean_cluster = sum(month_counts.values()) / len(month_counts)
            var_cluster = sum((v - mean_cluster)**2 for v in month_counts.values()) / len(month_counts)
            deff = 1 + (mean_cluster - 1) * (var_cluster / (mean_cluster**2)) if mean_cluster > 0 else 1
        else:
            deff = 1
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    iss_in, hits_in, prec_in, br_in, dd_in, en_in = compute_metrics(in_sample)
    iss_se, hits_se, prec_se, br_se, dd_se, en_se = compute_metrics(sealed)

    # Overall issued
    issued_all = len(issued_calls)
    hits_all = sum(c['hit'] for c in issued_calls)
    precision_all = hits_all / issued_all if issued_all else 0
    base_rate_all = hits_all / issued_all if issued_all else 0
    distinct_days_all = len(set(c['decision_date'] for c in issued_calls))
    # Design effect overall
    month_counts = defaultdict(int)
    for c in issued_calls:
        month_key = (c['decision_date'].year, c['decision_date'].month)
        month_counts[month_key] += 1
    if len(month_counts) > 1:
        mean_cluster = sum(month_counts.values()) / len(month_counts)
        var_cluster = sum((v - mean_cluster)**2 for v in month_counts.values()) / len(month_counts)
        deff = 1 + (mean_cluster - 1) * (var_cluster / (mean_cluster**2)) if mean_cluster > 0 else 1
    else:
        deff = 1
    effective_n_all = issued_all / deff if deff > 0 else issued_all

    print(f"ISSUED={issued_all}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_all:.6f}")
    print(f"BASE_RATE={base_rate_all:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_all}")
    print(f"EFFECTIVE_N={effective_n_all:.6f}")
    print(f"SEALED_PRECISION={prec_se:.6f}")

if __name__ == '__main__':
    main()
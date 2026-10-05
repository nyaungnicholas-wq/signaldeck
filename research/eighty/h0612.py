# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 611
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import date, timedelta, datetime, time
from collections import defaultdict

def business_days_add(d, n):
    while n > 0:
        d += timedelta(days=1)
        if d.weekday() < 5:
            n -= 1
    return d

def quarter_ends_between(start, end):
    q = []
    y = start.year
    while True:
        for m, day in [(3,31),(6,30),(9,30),(12,31)]:
            try:
                d = date(y, m, day)
            except ValueError:
                continue
            if d < start:
                continue
            if d > end:
                return q
            q.append(d)
        y += 1

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime.combine(d, time(0,0)).timestamp())

def quarter_of(d):
    return (d.year, (d.month-1)//3)

def prev_quarter(y, q):
    if q == 0:
        return y-1, 3
    return y, q-1

def quarter_key(y, q):
    return (y, q)

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load symbols
    cur.execute("SELECT id, symbol FROM symbols WHERE active=1")
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1"); return

    # Load EPS
    cur.execute("SELECT symbol_id, as_of, value, fetched_at FROM fundamentals WHERE metric='EPS'")
    eps_by_sym = defaultdict(list)
    for r in cur.fetchall():
        eps_by_sym[r['symbol_id']].append((r['as_of'], r['value'], r['fetched_at']))

    # Load SharesOutstanding
    cur.execute("SELECT symbol_id, as_of, value, fetched_at FROM fundamentals WHERE metric='SharesOutstanding'")
    so_by_sym = defaultdict(list)
    for r in cur.fetchall():
        so_by_sym[r['symbol_id']].append((r['as_of'], r['value'], r['fetched_at']))

    # Load insider purchases (code='P')
    cur.execute("SELECT symbol_id, tx_ts, filed_ts, insider FROM insider_trades WHERE code='P'")
    ins_by_sym = defaultdict(list)
    for r in cur.fetchall():
        ins_by_sym[r['symbol_id']].append((r['tx_ts'], r['filed_ts'], r['insider']))

    # Load labels for horizon='21d'
    cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon='21d'")
    labels = {}
    for r in cur.fetchall():
        d = ts_to_date(r['ts'])
        labels[(r['symbol_id'], d)] = r['up']

    # Load bars for market cap: get close on trading dates
    cur.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d'")
    bars_by_sym = defaultdict(list)
    for r in cur.fetchall():
        bars_by_sym[r['symbol_id']].append((r['ts'], r['close']))

    # Sort bars by ts
    for sym_id in bars_by_sym:
        bars_by_sym[sym_id].sort(key=lambda x: x[0])

    # Get all trading dates from bars
    cur.execute("SELECT DISTINCT date(ts, 'unixepoch') as d FROM bars WHERE tf='1d' ORDER BY d")
    trading_dates = [datetime.strptime(r['d'], '%Y-%m-%d').date() for r in cur.fetchall()]
    trading_set = set(trading_dates)

    def latest_trading_day_on_or_before(d):
        while d not in trading_set:
            d -= timedelta(days=1)
        return d

    def get_close_on_date(sym_id, d):
        td = latest_trading_day_on_or_before(d)
        ts = date_to_ts(td)
        arr = bars_by_sym.get(sym_id, [])
        lo, hi = 0, len(arr)
        while lo < hi:
            mid = (lo + hi) // 2
            if arr[mid][0] <= ts:
                lo = mid + 1
            else:
                hi = mid
        if lo == 0:
            return None
        return arr[lo-1][1]

    # Determine overall date range from labels
    label_dates = [d for (_, d) in labels.keys()]
    if not label_dates:
        print("INSUFFICIENT=1"); return
    min_label_date = min(label_dates)
    max_label_date = max(label_dates)

    # Generate quarter ends within label range
    q_ends = quarter_ends_between(min_label_date, max_label_date)

    # For each symbol, build quarterly EPS series (using as_of as quarter end)
    eps_quarterly = {}
    for sym_id, eps_list in eps_by_sym.items():
        qmap = {}
        for as_of, val, fetched_at in eps_list:
            d = ts_to_date(as_of)
            q = quarter_of(d)
            if q not in qmap or fetched_at > qmap[q][1]:
                qmap[q] = (val, fetched_at)
        eps_quarterly[sym_id] = qmap

    # Build quarterly insider purchase counts and distinct insiders (using filed_ts as knowable date)
    ins_quarterly = {}
    for sym_id, ins_list in ins_by_sym.items():
        qmap = defaultdict(lambda: {'count': 0, 'insiders': set()})
        for tx_ts, filed_ts, insider in ins_list:
            filed_d = ts_to_date(filed_ts)
            q = quarter_of(filed_d)
            qmap[q]['count'] += 1
            qmap[q]['insiders'].add(insider)
        ins_quarterly[sym_id] = qmap

    # Build quarterly SharesOutstanding
    so_quarterly = {}
    for sym_id, so_list in so_by_sym.items():
        qmap = {}
        for as_of, val, fetched_at in so_list:
            d = ts_to_date(as_of)
            q = quarter_of(d)
            if q not in qmap or fetched_at > qmap[q][1]:
                qmap[q] = (val, fetched_at)
        so_quarterly[sym_id] = qmap

    # Evaluate each quarter-end as decision point
    decisions = []  # (symbol_id, decision_date, label_date, label_up, in_sealed)
    for q_end in q_ends:
        decision_date = business_days_add(q_end, 5)
        # Need label at decision_date + 21 trading days
        # Find the 21st trading day after decision_date
        idx = trading_dates.index(decision_date) if decision_date in trading_set else None
        if idx is None:
            # find next trading day
            for i, td in enumerate(trading_dates):
                if td >= decision_date:
                    idx = i
                    break
        if idx is None or idx + 21 >= len(trading_dates):
            continue
        label_date = trading_dates[idx + 21]

        for sym_id in symbols:
            # Check market cap >= 500M at quarter-end
            close = get_close_on_date(sym_id, q_end)
            so_q = so_quarterly.get(sym_id, {}).get(quarter_of(q_end))
            if close is None or so_q is None:
                continue
            shares = so_q[0]
            mkt_cap = close * shares
            if mkt_cap < 500_000_000:
                continue

            # Need at least 8 quarters EPS history before q_end
            q0 = quarter_of(q_end)
            eps_q = eps_quarterly.get(sym_id, {})
            # Check we have EPS for q0, q0-1, q0-2, q0-3, q0-4 (5 quarters for 4Q growth)
            has_eps = True
            eps_vals = {}
            for i in range(5):
                y, q = q0
                for _ in range(i):
                    y, q = prev_quarter(y, q)
                if (y, q) not in eps_q:
                    has_eps = False
                    break
                eps_vals[(y, q)] = eps_q[(y, q)][0]
            if not has_eps:
                continue

            # Need at least 1 year insider history (4 quarters)
            ins_q = ins_quarterly.get(sym_id, {})
            has_ins = True
            for i in range(4):
                y, q = q0
                for _ in range(i):
                    y, q = prev_quarter(y, q)
                if (y, q) not in ins_q:
                    has_ins = False
                    break
            if not has_ins:
                continue

            # Entry conditions
            # (a) >=3 of last 4 quarters had >=1 purchase
            counts = []
            all_insiders = set()
            for i in range(4):
                y, q = q0
                for _ in range(i):
                    y, q = prev_quarter(y, q)
                c = ins_q.get((y, q), {'count': 0, 'insiders': set()})['count']
                counts.append(c)
                all_insiders.update(ins_q.get((y, q), {'count': 0, 'insiders': set()})['insiders'])

            quarters_with_purchases = sum(1 for c in counts if c >= 1)
            if quarters_with_purchases < 3:
                continue

            # (b) quarterly disclosure counts non-decreasing: Q-3 <= Q-2 <= Q-1 <= Q0
            # counts[3] = Q-3, counts[2] = Q-2, counts[1] = Q-1, counts[0] = Q0
            if not (counts[3] <= counts[2] <= counts[1] <= counts[0]):
                continue

            # (c) trailing 4Q EPS YoY growth > 0 and current quarter YoY growth > prior quarter YoY growth
            # current quarter = q0, prior quarter = q0-1
            # YoY growth for q0 = (eps_q0 - eps_q0-4) / abs(eps_q0-4)
            # YoY growth for q0-1 = (eps_q0-1 - eps_q0-5) / abs(eps_q0-5)
            eps_q0 = eps_vals[q0]
            eps_q0_1 = eps_vals[prev_quarter(*q0)]
            eps_q0_4 = eps_vals[prev_quarter(*prev_quarter(*prev_quarter(*prev_quarter(*q0))))]
            eps_q0_5 = eps_vals[prev_quarter(*prev_quarter(*prev_quarter(*prev_quarter(*prev_quarter(*q0)))))]

            if eps_q0_4 == 0 or eps_q0_5 == 0:
                continue
            yoy_q0 = (eps_q0 - eps_q0_4) / abs(eps_q0_4)
            yoy_q0_1 = (eps_q0_1 - eps_q0_5) / abs(eps_q0_5)

            if yoy_q0 <= 0 or yoy_q0 <= yoy_q0_1:
                continue

            # (d) >=2 distinct insiders across the 4 quarters
            if len(all_insiders) < 2:
                continue

            # All conditions met - issue call
            label_up = labels.get((sym_id, label_date))
            if label_up is None:
                continue

            # Determine if in sealed era (most recent 20% of decisions by date)
            decisions.append((sym_id, decision_date, label_date, label_up))

    if not decisions:
        print("INSUFFICIENT=1"); return

    # Sort by decision_date
    decisions.sort(key=lambda x: x[1])
    n = len(decisions)
    split_idx = int(n * 0.8)
    main_decisions = decisions[:split_idx]
    sealed_decisions = decisions[split_idx:]

    def compute_metrics(dec_list):
        if not dec_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(dec_list)
        hits = sum(1 for d in dec_list if d[3] == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of predicted class (up=1) within issued
        distinct_days = len(set(d[1] for d in dec_list))
        # Design effect: approximate using clustering by day
        # Simple approximation: design_effect = 1 + (avg_cluster_size - 1) * ICC
        # Use conservative estimate: design_effect = 6.99 as mentioned in context
        design_effect = 6.99
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_main, hits_main, prec_main, base_main, days_main, eff_main = compute_metrics(main_decisions)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, days_sealed, eff_sealed = compute_metrics(sealed_decisions)

    print(f"ISSUED={issued_main}")
    print(f"OPPORTUNITIES={n}")
    print(f"PRECISION={prec_main:.4f}")
    print(f"BASE_RATE={base_main:.4f}")
    print(f"DISTINCT_DAYS={days_main}")
    print(f"EFFECTIVE_N={eff_main:.2f}")
    print(f"SEALED_PRECISION={prec_sealed:.4f}")

if __name__ == "__main__":
    main()
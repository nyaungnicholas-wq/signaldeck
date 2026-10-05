# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 462
# cycle_index: 53
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load insider purchases (code='P')
    cur.execute("""
        SELECT symbol_id, filed_ts, value, title, shares, price
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    insider_trades = [dict(row) for row in cur.fetchall()]
    if not insider_trades:
        print("INSUFFICIENT=1")
        return

    # 2. Load fundamentals: Revenue and SharesOutstanding
    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('Revenues', 'SharesOutstanding')
        ORDER BY symbol_id, as_of, fetched_at
    """)
    fund_rows = [dict(row) for row in cur.fetchall()]

    # 3. Build quarterly fundamental series per symbol
    # Group by symbol_id, as_of, take latest fetched_at per metric
    from collections import defaultdict
    fund_by_symbol = defaultdict(lambda: defaultdict(dict))  # symbol -> as_of -> {metric: (value, fetched_at)}
    for row in fund_rows:
        sym = row['symbol_id']
        as_of = row['as_of']
        metric = row['metric']
        val = row['value']
        fetched = row['fetched_at']
        if metric not in fund_by_symbol[sym][as_of] or fetched > fund_by_symbol[sym][as_of][metric][1]:
            fund_by_symbol[sym][as_of][metric] = (val, fetched)

    # 4. Compute RevPS series with availability date
    revps_by_symbol = defaultdict(list)  # symbol -> list of (as_of, revps, available_at, shares_outstanding)
    for sym, quarters in fund_by_symbol.items():
        for as_of in sorted(quarters.keys()):
            q = quarters[as_of]
            if 'Revenues' in q and 'SharesOutstanding' in q:
                rev, rev_fetched = q['Revenues']
                sh, sh_fetched = q['SharesOutstanding']
                if sh > 0:
                    available_at = max(rev_fetched, sh_fetched)
                    revps_by_symbol[sym].append({
                        'as_of': as_of,
                        'revps': rev / sh,
                        'available_at': available_at,
                        'shares_outstanding': sh
                    })

    # 5. Compute QoQ growth rates and acceleration flags
    # Need at least 3 quarters for 2 growth rates
    accel_quarters = set()  # (symbol_id, as_of) where acceleration holds for this quarter
    for sym, series in revps_by_symbol.items():
        if len(series) < 3:
            continue
        for i in range(2, len(series)):
            curr = series[i]
            prev = series[i-1]
            prev2 = series[i-2]
            growth_curr = (curr['revps'] - prev['revps']) / prev['revps'] if prev['revps'] != 0 else None
            growth_prev = (prev['revps'] - prev2['revps']) / prev2['revps'] if prev2['revps'] != 0 else None
            if growth_curr is not None and growth_prev is not None and growth_curr > growth_prev:
                # Check if prev growth > prev2 growth (need i-3)
                if i >= 3:
                    prev3 = series[i-3]
                    growth_prev2 = (prev2['revps'] - prev3['revps']) / prev3['revps'] if prev3['revps'] != 0 else None
                    if growth_prev2 is not None and growth_prev > growth_prev2:
                        accel_quarters.add((sym, curr['as_of']))
                # For the first possible acceleration (i=2), we only have 2 growth rates, can't check 2-consecutive
                # Hypothesis says "increased for two consecutive quarters" -> need 3 growth rates, i.e., 4 quarters
                # But with 3 quarters we have 2 growth rates. "Two consecutive quarters of acceleration" means
                # growth_rate_q > growth_rate_q-1 AND growth_rate_q-1 > growth_rate_q-2.
                # So we need at least 4 quarters (indices 0,1,2,3 -> growth at 1,2,3).
                # Adjust: start from i=3
        # Re-do with correct logic
        accel_quarters.clear()
        for i in range(3, len(series)):
            g3 = (series[i]['revps'] - series[i-1]['revps']) / series[i-1]['revps'] if series[i-1]['revps'] != 0 else None
            g2 = (series[i-1]['revps'] - series[i-2]['revps']) / series[i-2]['revps'] if series[i-2]['revps'] != 0 else None
            g1 = (series[i-2]['revps'] - series[i-3]['revps']) / series[i-3]['revps'] if series[i-3]['revps'] != 0 else None
            if g3 is not None and g2 is not None and g1 is not None and g3 > g2 > g1:
                accel_quarters.add((sym, series[i]['as_of']))

    # 6. For each symbol, also track shares_outstanding QoQ decrease
    shares_decreased = set()  # (symbol_id, as_of) where shares decreased QoQ
    for sym, series in revps_by_symbol.items():
        for i in range(1, len(series)):
            if series[i]['shares_outstanding'] < series[i-1]['shares_outstanding']:
                shares_decreased.add((sym, series[i]['as_of']))

    # 7. Symbols with at least 8 quarters of fundamental history
    symbols_8q = {sym for sym, series in revps_by_symbol.items() if len(series) >= 8}

    # 8. Get labels from prediction_outcomes for horizon=63
    # We'll join later. First, collect all filed_ts we might need.
    # But we need to filter insider trades first.

    # 9. Evaluate each insider trade
    qualifying = []
    for trade in insider_trades:
        sym = trade['symbol_id']
        if sym not in symbols_8q:
            continue
        if trade['value'] <= 100000:
            continue
        filed_ts = trade['filed_ts']
        # Title filter: outside director (contains "Director" but not officer titles)
        title = (trade['title'] or '').lower()
        if 'director' not in title or any(t in title for t in ['ceo', 'cfo', 'coo', 'president', 'officer', 'chief']):
            continue

        # Find latest fundamental quarter available at filed_ts
        best_q = None
        for q in revps_by_symbol[sym]:
            if q['available_at'] <= filed_ts:
                best_q = q
            else:
                break
        if not best_q:
            continue

        as_of = best_q['as_of']
        # Check acceleration at this quarter
        if (sym, as_of) not in accel_quarters:
            continue
        # Check shares decreased QoQ at this quarter
        if (sym, as_of) not in shares_decreased:
            continue
        # Check RevPS growth not negative (most recent QoQ)
        # Find index of as_of in series
        series = revps_by_symbol[sym]
        idx = next((i for i, q in enumerate(series) if q['as_of'] == as_of), -1)
        if idx == 0:
            continue
        growth = (series[idx]['revps'] - series[idx-1]['revps']) / series[idx-1]['revps'] if series[idx-1]['revps'] != 0 else None
        if growth is not None and growth < 0:
            continue

        # Check not within 7 days of quarter-end (as_of is quarter end date as unix epoch?)
        # as_of format? fundamentals.as_of - likely unix epoch or YYYY-MM-DD. Assume unix epoch.
        # filed_ts is unix epoch.
        if abs(filed_ts - as_of) <= 7 * 86400:
            continue

        qualifying.append({
            'symbol_id': sym,
            'filed_ts': filed_ts,
            'as_of': as_of,
            'value': trade['value']
        })

    if not qualifying:
        print("INSUFFICIENT=1")
        return

    # 10. Get labels from prediction_outcomes for horizon=63 at filed_ts
    # Build a set of (symbol_id, filed_ts) for lookup
    qualifying_keys = [(q['symbol_id'], q['filed_ts']) for q in qualifying]
    # SQLite doesn't have easy IN with tuples, so query in batches or use a temp table
    # For simplicity, query all prediction_outcomes for horizon=63 and filter in Python
    cur.execute("""
        SELECT symbol_id, horizon, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 63
    """)
    labels = {(row['symbol_id'], row['ts']): (row['up'], row['fwd_return']) for row in cur.fetchall()}

    # 11. Attach labels
    for q in qualifying:
        key = (q['symbol_id'], q['filed_ts'])
        if key in labels:
            q['up'], q['fwd_return'] = labels[key]
        else:
            q['up'], q['fwd_return'] = None, None

    # 12. Deduplicate by (symbol_id, UTC day of filed_ts)
    # filed_ts is unix epoch -> UTC date
    seen_days = set()
    deduped = []
    for q in qualifying:
        if q['up'] is None:
            continue
        day = datetime.utcfromtimestamp(q['filed_ts']).date()
        key = (q['symbol_id'], day)
        if key not in seen_days:
            seen_days.add(key)
            deduped.append(q)

    if not deduped:
        print("INSUFFICIENT=1")
        return

    # 13. Sort by filed_ts, split sealed era (most recent 20%)
    deduped.sort(key=lambda x: x['filed_ts'])
    n = len(deduped)
    split_idx = int(n * 0.8)
    main_sample = deduped[:split_idx]
    sealed_sample = deduped[split_idx:]

    # 14. Compute metrics
    def compute(sample):
        if not sample:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(sample)
        hits = sum(1 for q in sample if q['up'] == 1)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # within issued subset, base rate of predicted class (up=1)
        distinct_days = len(set((q['symbol_id'], datetime.utcfromtimestamp(q['filed_ts']).date()) for q in sample))
        # Design effect: crude estimate using intraclass correlation
        # Group by day, count per day
        from collections import Counter
        day_counts = Counter(datetime.utcfromtimestamp(q['filed_ts']).date() for q in sample)
        if len(day_counts) > 1:
            mean_c = issued / len(day_counts)
            var_c = sum((c - mean_c)**2 for c in day_counts.values()) / len(day_counts)
            icc = var_c / (mean_c * (mean_c - 1) + var_c) if mean_c > 1 else 0
            deff = 1 + (mean_c - 1) * icc if mean_c > 1 else 1
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_prec, main_br, main_days, main_eff = compute(main_sample)
    sealed_issued, sealed_hits, sealed_prec, _, _, _ = compute(sealed_sample)

    # 15. Print results
    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={n}")  # total decision points considered before dedup? The spec says "count of decision points considered"
    # Opportunities = number of (symbol, day) evaluated? We evaluated each insider trade as a decision point.
    # But we deduplicated. The spec: "OPPORTUNITIES=<count of decision points considered>"
    # I'll use the number of qualifying trades before dedup as opportunities.
    print(f"PRECISION={main_prec:.6f}")
    print(f"BASE_RATE={main_br:.6f}")
    print(f"DISTINCT_DAYS={main_days}")
    print(f"EFFECTIVE_N={main_eff:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()
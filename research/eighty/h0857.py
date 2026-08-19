# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 856
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timedelta

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Universe: symbols with ≥500 daily bars, ≥8 quarterly EPS & Revenue obs, ≥252 days sentiment_features
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.active = 1
    """)
    all_symbols = {row['id']: row['symbol'] for row in cur.fetchall()}

    # Daily bar counts
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING cnt >= 500
    """)
    bar_counts = {row['symbol_id']: row['cnt'] for row in cur.fetchall()}

    # Quarterly fundamental observations (EPS and Revenue)
    cur.execute("""
        SELECT symbol_id, metric, COUNT(DISTINCT as_of) as qcnt
        FROM fundamentals
        WHERE metric IN ('EPS', 'Revenues') AND as_of > 0
        GROUP BY symbol_id, metric
    """)
    fund_quarters = defaultdict(lambda: {'EPS': 0, 'Revenues': 0})
    for row in cur.fetchall():
        fund_quarters[row['symbol_id']][row['metric']] = row['qcnt']

    # Sentiment features days
    cur.execute("""
        SELECT symbol_id, COUNT(DISTINCT day) as dcnt
        FROM sentiment_features
        GROUP BY symbol_id
        HAVING dcnt >= 252
    """)
    sent_days = {row['symbol_id']: row['dcnt'] for row in cur.fetchall()}

    eligible_symbols = []
    for sid in all_symbols:
        if (sid in bar_counts and
            fund_quarters[sid]['EPS'] >= 8 and
            fund_quarters[sid]['Revenues'] >= 8 and
            sid in sent_days):
            eligible_symbols.append(sid)

    if len(eligible_symbols) < 10:
        print("INSUFFICIENT=1")
        return 0

    # 2. Fetch all daily bars for eligible symbols
    placeholders = ','.join('?' * len(eligible_symbols))
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, eligible_symbols)
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append(row)

    # 3. Fetch fundamentals (EPS, Revenues) with fetched_at
    cur.execute(f"""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('EPS', 'Revenues') AND as_of > 0 AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, metric, as_of
    """, eligible_symbols)
    fund_by_symbol = defaultdict(lambda: {'EPS': [], 'Revenues': []})
    for row in cur.fetchall():
        fund_by_symbol[row['symbol_id']][row['metric']].append({
            'value': row['value'],
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at']
        })

    # 4. Fetch news headlines grouped by day (ts -> date)
    cur.execute(f"""
        SELECT symbol_id, ts
        FROM news
        WHERE symbol_id IN ({placeholders})
    """, eligible_symbols)
    news_by_symbol = defaultdict(lambda: defaultdict(int))
    for row in cur.fetchall():
        d = epoch_to_date(row['ts'])
        news_by_symbol[row['symbol_id']][d] += 1

    # 5. Fetch insider open-market sales (code='S') with filed_ts
    cur.execute(f"""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code = 'S' AND symbol_id IN ({placeholders})
    """, eligible_symbols)
    insider_sales_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        insider_sales_by_symbol[row['symbol_id']].append(row['filed_ts'])

    # 6. Process each symbol
    all_opportunities = []  # (decision_ts, symbol_id, hit)
    all_issued = []         # (decision_ts, symbol_id, hit)

    for sid in eligible_symbols:
        bars = bars_by_symbol[sid]
        if len(bars) < 500:
            continue

        # Build maps for fast lookup
        ts_to_bar = {b['ts']: b for b in bars}
        sorted_ts = sorted(ts_to_bar.keys())

        # Precompute 20-day average volume for each day
        vol_20avg = {}
        for i, ts in enumerate(sorted_ts):
            if i >= 20:
                vol_sum = sum(bars[sorted_ts[j]]['volume'] for j in range(i-20, i))
                vol_20avg[ts] = vol_sum / 20.0

        # Fundamentals available at each decision time
        eps_series = fund_by_symbol[sid]['EPS']
        rev_series = fund_by_symbol[sid]['Revenues']
        # Sort by as_of
        eps_series.sort(key=lambda x: x['as_of'])
        rev_series.sort(key=lambda x: x['as_of'])

        # Insider sales filed_ts sorted
        insider_filed = sorted(insider_sales_by_symbol[sid])

        # News days set
        news_days = set(news_by_symbol[sid].keys())

        # Iterate decision points (need 10 prior sessions + 20 for vol avg + 21 for label)
        # Start from index 30 (20 for vol avg + 10 for pattern)
        for i in range(30, len(sorted_ts) - 21):
            decision_ts = sorted_ts[i]
            decision_date = epoch_to_date(decision_ts)

            # --- Condition 1: 10-session price decline ≥5% on volume ≤80% of 20-day avg for ≥8 of 10 sessions ---
            # Sessions: i-9 to i (10 sessions including decision day)
            pattern_ts = sorted_ts[i-9:i+1]
            if len(pattern_ts) != 10:
                continue

            # Price decline over 10 sessions: (close_i - close_i-9) / close_i-9 ≤ -0.05
            close_start = ts_to_bar[pattern_ts[0]]['close']
            close_end = ts_to_bar[pattern_ts[9]]['close']
            if close_start == 0:
                continue
            price_decline = (close_end - close_start) / close_start
            if price_decline > -0.05:
                continue

            # Volume condition for each of 10 sessions
            vol_ok_count = 0
            for j, ts in enumerate(pattern_ts):
                if ts not in vol_20avg:
                    break
                bar = ts_to_bar[ts]
                if bar['volume'] <= 0.8 * vol_20avg[ts]:
                    vol_ok_count += 1
            if vol_ok_count < 8:
                continue

            # --- Condition 2: Zero professional news headlines on ≥8 of those 10 sessions ---
            news_free_count = 0
            for ts in pattern_ts:
                d = epoch_to_date(ts)
                if d not in news_days:
                    news_free_count += 1
            if news_free_count < 8:
                continue

            # --- Condition 3: Latest quarterly EPS and Revenue both show positive QoQ acceleration ---
            # Need fundamentals with fetched_at ≤ decision_ts
            # Get latest 4 quarters for each metric available at decision time
            def get_latest_quarters(series, decision_ts):
                avail = [q for q in series if q['fetched_at'] <= decision_ts]
                if len(avail) < 4:
                    return None
                # Take last 4 by as_of
                avail.sort(key=lambda x: x['as_of'])
                return avail[-4:]

            eps_q = get_latest_quarters(eps_series, decision_ts)
            rev_q = get_latest_quarters(rev_series, decision_ts)
            if not eps_q or not rev_q:
                continue

            def check_acceleration(q):
                # q[0] oldest, q[3] latest
                vals = [x['value'] for x in q]
                if any(v <= 0 for v in vals):
                    return False
                qoq1 = (vals[1] - vals[0]) / vals[0]
                qoq2 = (vals[2] - vals[1]) / vals[1]
                qoq3 = (vals[3] - vals[2]) / vals[2]
                return qoq3 > qoq2 > qoq1 > 0

            if not (check_acceleration(eps_q) and check_acceleration(rev_q)):
                continue

            # --- Condition 4: No insider open-market sales in prior 21 sessions ---
            # Prior 21 sessions: indices i-21 to i-1
            prior_ts = sorted_ts[i-21:i]
            prior_start = prior_ts[0]
            prior_end = prior_ts[-1]
            has_sale = any(prior_start <= ft <= prior_end for ft in insider_filed)
            if has_sale:
                continue

            # All conditions met - issue call
            # Label: 21 trading days forward return
            label_ts = sorted_ts[i + 21]
            entry_close = ts_to_bar[decision_ts]['close']
            exit_close = ts_to_bar[label_ts]['close']
            fwd_return = (exit_close - entry_close) / entry_close
            hit = 1 if fwd_return > 0 else 0

            all_opportunities.append((decision_ts, sid, hit))
            all_issued.append((decision_ts, sid, hit))

    if not all_opportunities:
        print("INSUFFICIENT=1")
        return 0

    # 7. Hold out most recent 20% as sealed era
    all_issued.sort(key=lambda x: x[0])
    n_total = len(all_issued)
    n_sealed = max(1, int(n_total * 0.2))
    sealed = all_issued[-n_sealed:]
    main_era = all_issued[:-n_sealed]

    # 8. Compute metrics
    issued_count = len(all_issued)
    opportunities_count = len(all_opportunities)  # same as issued since we only add when issued

    hits = sum(1 for _, _, h in all_issued if h)
    precision = hits / issued_count if issued_count else 0

    # Base rate within issued subset
    base_rate = hits / issued_count if issued_count else 0

    # Distinct UTC days among issued calls
    issued_dates = set(epoch_to_date(ts) for ts, _, _ in all_issued)
    distinct_days = len(issued_dates)

    # Effective N: issued / design effect
    # Design effect = 1 + (avg cluster size - 1) * ICC
    # Approximate: cluster by day, compute intra-day correlation
    # Simplified: design_effect = issued / distinct_days (since max 1 per day per symbol, but multiple symbols per day)
    # Actually, count calls per day
    day_counts = defaultdict(int)
    for ts, _, _ in all_issued:
        day_counts[epoch_to_date(ts)] += 1
    avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    # Conservative ICC estimate for financial returns ~0.1-0.3, use 0.2
    icc = 0.2
    design_effect = 1 + (avg_cluster - 1) * icc
    effective_n = issued_count / design_effect if design_effect > 0 else issued_count

    # Sealed precision
    sealed_hits = sum(1 for _, _, h in sealed if h)
    sealed_precision = sealed_hits / len(sealed) if sealed else 0

    # 9. Abstain checks
    if issued_count < 30:
        print("INSUFFICIENT=1")
        return 0
    if base_rate > 0.7:
        print("INSUFFICIENT=1")
        return 0

    # 10. Print results
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
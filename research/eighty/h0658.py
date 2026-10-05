# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 657
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Check basic date range feasibility
    cur.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
    max_bar_ts = cur.fetchone()[0]
    if not max_bar_ts:
        print("INSUFFICIENT=1"); return 0
    max_bar_date = epoch_to_date(max_bar_ts)

    cur.execute("SELECT MIN(fetched_at) FROM fundamentals WHERE metric='Revenues' AND as_of > 0")
    row = cur.fetchone()
    min_fetched_ts = row[0] if row and row[0] else None
    if not min_fetched_ts:
        print("INSUFFICIENT=1"); return 0
    min_fetched_date = epoch_to_date(min_fetched_ts)

    # Need decision_date >= min_fetched_date AND decision_date + 63 calendar days <= max_bar_date
    # So feasible window: [min_fetched_date, max_bar_date - 63 days]
    latest_decision_date = max_bar_date - timedelta(days=63)
    if min_fetched_date > latest_decision_date:
        print("INSUFFICIENT=1"); return 0

    # 2. Get all open-market insider purchases (code='P') in feasible window
    cur.execute("""
        SELECT it.symbol_id, it.filed_ts, s.symbol
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P'
          AND it.filed_ts >= ? AND it.filed_ts <= ?
        ORDER BY it.filed_ts
    """, (date_to_epoch(min_fetched_date), date_to_epoch(latest_decision_date)))
    purchases = cur.fetchall()
    if not purchases:
        print("INSUFFICIENT=1"); return 0

    # 3. Pre-load data for efficiency
    # News: count articles per symbol per day (ts -> date)
    cur.execute("SELECT symbol_id, ts FROM news")
    news_by_sym_day = defaultdict(lambda: defaultdict(int))
    for row in cur.fetchall():
        d = epoch_to_date(row['ts'])
        news_by_sym_day[row['symbol_id']][d] += 1

    # Bars 1d: get all trading days per symbol for session definition
    cur.execute("SELECT symbol_id, ts FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    bar_days_by_sym = defaultdict(list)
    for row in cur.fetchall():
        bar_days_by_sym[row['symbol_id']].append(epoch_to_date(row['ts']))

    # Fundamentals: Revenues with fetched_at, as_of
    cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric='Revenues' AND as_of > 0
        ORDER BY symbol_id, as_of
    """)
    rev_by_sym = defaultdict(list)
    for row in cur.fetchall():
        rev_by_sym[row['symbol_id']].append({
            'value': row['value'],
            'as_of': epoch_to_date(row['as_of']),
            'fetched_at': epoch_to_date(row['fetched_at'])
        })

    # 4. Group purchases by disclosure date (filed_ts date)
    purchases_by_day = defaultdict(list)
    for p in purchases:
        d = epoch_to_date(p['filed_ts'])
        purchases_by_day[d].append(p)

    all_calls = []  # (decision_date, symbol_id, symbol, forward_return, up)

    # 5. Process each disclosure day
    for decision_date in sorted(purchases_by_day.keys()):
        day_purchases = purchases_by_day[decision_date]
        sym_ids_today = {p['symbol_id'] for p in day_purchases}

        # Universe criteria check: count symbols with all three data types available as of decision_date
        universe_count = 0
        # We need to check across ALL symbols, not just those with purchases today
        # But for efficiency, check only symbols that have at least some data
        candidate_symbols = set(news_by_sym_day.keys()) & set(bar_days_by_sym.keys()) & set(rev_by_sym.keys())
        for sym_id in candidate_symbols:
            # insider history: any insider_trades row with filed_ts <= decision_date
            cur.execute("SELECT 1 FROM insider_trades WHERE symbol_id=? AND filed_ts<=? LIMIT 1",
                        (sym_id, date_to_epoch(decision_date)))
            if not cur.fetchone():
                continue
            # news coverage: any news before decision_date
            has_news = any(d < decision_date for d in news_by_sym_day[sym_id].keys())
            if not has_news:
                continue
            # revenue data: any revenue with fetched_at <= decision_date
            has_rev = any(r['fetched_at'] <= decision_date for r in rev_by_sym[sym_id])
            if not has_rev:
                continue
            universe_count += 1

        if universe_count < 5:
            continue  # ABSTAIN

        # 6. Compute cross-sectional news quartile on T-1
        t_minus_1 = decision_date - timedelta(days=1)
        # For each symbol, compute 63-session average daily news count ending T-1
        sym_avg_news = {}
        for sym_id in candidate_symbols:
            bar_days = bar_days_by_sym[sym_id]
            # Find index of T-1 or prior trading day
            idx = -1
            for i, bd in enumerate(bar_days):
                if bd <= t_minus_1:
                    idx = i
                else:
                    break
            if idx < 62:  # need 63 sessions (0..62)
                continue
            # Get 63 trading days ending at idx
            session_days = bar_days[idx-62:idx+1]
            # Count news articles on each of those days
            total = sum(news_by_sym_day[sym_id].get(d, 0) for d in session_days)
            avg = total / 63.0
            sym_avg_news[sym_id] = avg

        if len(sym_avg_news) < 4:  # need at least 4 for quartile
            continue
        # Bottom quartile threshold
        sorted_avgs = sorted(sym_avg_news.values())
        q1_idx = max(0, len(sorted_avgs) // 4 - 1)
        bottom_q_threshold = sorted_avgs[q1_idx]

        # 7. Check each purchase on this day
        for p in day_purchases:
            sym_id = p['symbol_id']
            # News condition
            if sym_id not in sym_avg_news or sym_avg_news[sym_id] > bottom_q_threshold:
                continue

            # Revenue acceleration condition
            revs = [r for r in rev_by_sym[sym_id] if r['fetched_at'] <= decision_date]
            if len(revs) < 6:  # need at least 3 quarters + 3 year-ago quarters
                continue
            # Sort by as_of descending (most recent first)
            revs.sort(key=lambda x: x['as_of'], reverse=True)
            # Find three most recent quarters with year-ago data
            # Group by quarter (approximate by 3-month intervals)
            # Simpler: take first 6 entries assuming they are Q-1, Q-1y, Q-2, Q-2y, Q-3, Q-3y
            # But need to verify they are actually year-apart
            quarters = []
            for r in revs:
                quarters.append(r)
                if len(quarters) >= 6:
                    break
            if len(quarters) < 6:
                continue
            # Assume order: Q-1, Q-1y, Q-2, Q-2y, Q-3, Q-3y (by as_of desc)
            # Check year-apart roughly
            try:
                q1, q1y, q2, q2y, q3, q3y = quarters[:6]
                # Verify approximate year gaps
                if abs((q1['as_of'] - q1y['as_of']).days - 365) > 45: continue
                if abs((q2['as_of'] - q2y['as_of']).days - 365) > 45: continue
                if abs((q3['as_of'] - q3y['as_of']).days - 365) > 45: continue
                # YoY growth
                g1 = (q1['value'] - q1y['value']) / q1y['value'] if q1y['value'] else None
                g2 = (q2['value'] - q2y['value']) / q2y['value'] if q2y['value'] else None
                g3 = (q3['value'] - q3y['value']) / q3y['value'] if q3y['value'] else None
                if g1 is None or g2 is None or g3 is None:
                    continue
                if not (g1 > g2 > g3):
                    continue
            except (ValueError, ZeroDivisionError):
                continue

            # 8. Compute 63-session forward return from bars
            bar_days = bar_days_by_sym[sym_id]
            # Find decision bar: first bar on or after decision_date
            start_idx = -1
            for i, bd in enumerate(bar_days):
                if bd >= decision_date:
                    start_idx = i
                    break
            if start_idx == -1 or start_idx + 63 >= len(bar_days):
                continue
            end_idx = start_idx + 63
            # Get close prices
            cur.execute("""
                SELECT close FROM bars
                WHERE symbol_id=? AND tf='1d' AND ts IN (?, ?)
            """, (sym_id,
                  date_to_epoch(bar_days[start_idx]),
                  date_to_epoch(bar_days[end_idx])))
            rows = cur.fetchall()
            if len(rows) != 2:
                continue
            close_start, close_end = rows[0]['close'], rows[1]['close']
            if close_start <= 0:
                continue
            fwd_return = (close_end - close_start) / close_start
            up = 1 if fwd_return > 0 else 0
            all_calls.append((decision_date, sym_id, p['symbol'], fwd_return, up))

    if not all_calls:
        print("INSUFFICIENT=1"); return 0

    # 9. Split into sealed era (most recent 20% by decision_date)
    all_calls.sort(key=lambda x: x[0])
    n = len(all_calls)
    split_idx = int(n * 0.8)
    main_calls = all_calls[:split_idx]
    sealed_calls = all_calls[split_idx:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0
        issued = len(calls)
        hits = sum(c[4] for c in calls)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(set(c[0] for c in calls))
        # Design effect: cluster by day, compute variance inflation
        day_counts = defaultdict(int)
        for c in calls:
            day_counts[c[0]] += 1
        if len(day_counts) > 1:
            mean_c = issued / len(day_counts)
            var_c = sum((c - mean_c)**2 for c in day_counts.values()) / len(day_counts)
            deff = 1 + (var_c / mean_c) if mean_c > 0 else 1
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_m, hits_m, prec_m, br_m, dd_m, en_m = compute_metrics(main_calls)
    issued_s, hits_s, prec_s, br_s, dd_s, en_s = compute_metrics(sealed_calls)

    # 10. Print required lines
    print(f"ISSUED={issued_m}")
    print(f"OPPORTUNITIES={sum(len(v) for v in purchases_by_day.values())}")
    print(f"PRECISION={prec_m:.6f}")
    print(f"BASE_RATE={br_m:.6f}")
    print(f"DISTINCT_DAYS={dd_m}")
    print(f"EFFECTIVE_N={en_m:.2f}")
    print(f"SEALED_PRECISION={prec_s:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
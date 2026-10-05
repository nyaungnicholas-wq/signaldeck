# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 746
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime
from collections import defaultdict, Counter

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
conn.row_factory = sqlite3.Row

trading_dates = [row['ts'] for row in conn.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")]
if not trading_dates:
    print("INSUFFICIENT=1")
    sys.exit(0)
date_to_idx = {ts: i for i, ts in enumerate(trading_dates)}
trading_date_strs = [datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d') for ts in trading_dates]
trading_date_str_to_ts = {ds: ts for ds, ts in zip(trading_date_strs, trading_dates)}

fund_rows = conn.execute("""
    SELECT symbol_id, as_of, fetched_at, metric, value
    FROM fundamentals
    WHERE metric IN ('EPS', 'Revenues') AND as_of > 0
    ORDER BY symbol_id, as_of, fetched_at
""").fetchall()

fund_by_symbol = defaultdict(list)
for row in fund_rows:
    fund_by_symbol[row['symbol_id']].append((row['as_of'], row['fetched_at'], row['metric'], row['value']))

fund_quarterly = defaultdict(list)
for sym, rows in fund_by_symbol.items():
    by_asof = defaultdict(list)
    for as_of, fetched_at, metric, value in rows:
        by_asof[as_of].append((fetched_at, metric, value))
    for as_of, entries in by_asof.items():
        entries.sort(key=lambda x: x[0])
        latest_fetched = entries[-1][0]
        eps = next((v for f, m, v in entries if m == 'EPS' and f == latest_fetched), None)
        rev = next((v for f, m, v in entries if m == 'Revenues' and f == latest_fetched), None)
        if eps is not None and rev is not None:
            fund_quarterly[sym].append((as_of, latest_fetched, eps, rev))
    fund_quarterly[sym].sort(key=lambda x: x[0])

insider_sales = conn.execute("""
    SELECT symbol_id, insider, filed_ts
    FROM insider_trades
    WHERE code = 'S'
    ORDER BY symbol_id, filed_ts
""").fetchall()

insider_by_symbol = defaultdict(list)
for row in insider_sales:
    insider_by_symbol[row['symbol_id']].append((row['filed_ts'], row['insider']))

sentiment_rows = conn.execute("""
    SELECT symbol_id, day, mean_score
    FROM sentiment_features
    WHERE n_all > 0
    ORDER BY symbol_id, day
""").fetchall()

sentiment_by_symbol = defaultdict(list)
for row in sentiment_rows:
    day_str = row['day']
    if day_str in trading_date_str_to_ts:
        sentiment_by_symbol[row['symbol_id']].append((trading_date_str_to_ts[day_str], row['mean_score']))

universe_symbols = set(fund_quarterly.keys()) & set(insider_by_symbol.keys()) & set(sentiment_by_symbol.keys())
universe_symbols = {s for s in universe_symbols if len(fund_quarterly[s]) >= 8}

placeholders = ','.join('?' * len(universe_symbols))
bars_rows = conn.execute(f"""
    SELECT symbol_id, ts, close
    FROM bars
    WHERE tf='1d' AND symbol_id IN ({placeholders})
    ORDER BY symbol_id, ts
""", list(universe_symbols)).fetchall()

bars_by_symbol = defaultdict(dict)
for row in bars_rows:
    bars_by_symbol[row['symbol_id']][row['ts']] = row['close']

calls = []
opportunities = 0

for symbol_id in universe_symbols:
    fund_q = fund_quarterly[symbol_id]
    insider_sales_sym = insider_by_symbol[symbol_id]
    aligned_sentiment = sentiment_by_symbol[symbol_id]
    bars_sym = bars_by_symbol[symbol_id]
    if not bars_sym:
        continue

    sale_trading_indices = []
    for filed_ts, insider in insider_sales_sym:
        filed_date_str = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
        if filed_date_str in trading_date_str_to_ts:
            trade_ts = trading_date_str_to_ts[filed_date_str]
            trade_idx = date_to_idx[trade_ts]
            sale_trading_indices.append((trade_idx, filed_ts, insider, trade_ts))

    clusters = []
    for i, (idx, filed_ts, insider, trade_ts) in enumerate(sale_trading_indices):
        window_start = idx - 9
        insiders_in_window = set()
        for j in range(max(0, window_start), i + 1):
            insiders_in_window.add(sale_trading_indices[j][2])
        if len(insiders_in_window) >= 2:
            clusters.append((idx, filed_ts, insiders_in_window, trade_ts))

    decision_points = {}
    for idx, filed_ts, insiders, trade_ts in clusters:
        if trade_ts not in decision_points:
            decision_points[trade_ts] = (idx, filed_ts, insiders)

    for trade_ts, (trade_idx, filed_ts, insiders) in decision_points.items():
        opportunities += 1
        latest_quarter = None
        for as_of, fetched_at, eps, revenue in fund_q:
            if fetched_at <= filed_ts:
                latest_quarter = (as_of, fetched_at, eps, revenue)
            else:
                break
        if latest_quarter is None:
            continue
        as_of, fetched_at, eps, revenue = latest_quarter
        if filed_ts - fetched_at > 60 * 86400:
            continue

        q_idx = None
        for i, (a, f, e, r) in enumerate(fund_q):
            if a == as_of and f == fetched_at:
                q_idx = i
                break
        if q_idx is None or q_idx < 11:
            continue

        trailing_4q_eps = sum(fund_q[i][2] for i in range(q_idx - 3, q_idx + 1))
        trailing_4q_rev = sum(fund_q[i][3] for i in range(q_idx - 3, q_idx + 1))
        if trailing_4q_rev == 0:
            continue
        current_margin = trailing_4q_eps / trailing_4q_rev

        margins_12q = []
        for i in range(q_idx - 11, q_idx + 1):
            e4 = sum(fund_q[j][2] for j in range(i - 3, i + 1))
            r4 = sum(fund_q[j][3] for j in range(i - 3, i + 1))
            if r4 != 0:
                margins_12q.append(e4 / r4)
        if not margins_12q or current_margin < max(margins_12q) - 1e-10:
            continue

        if q_idx < 6:
            continue
        growth = []
        for i in [q_idx, q_idx - 1, q_idx - 2]:
            rev_now = fund_q[i][3]
            rev_yr_ago = fund_q[i - 4][3]
            if rev_yr_ago != 0:
                growth.append((rev_now - rev_yr_ago) / rev_yr_ago)
            else:
                growth.append(None)
        if None in growth:
            continue
        if not (growth[0] < growth[1] < growth[2]):
            continue

        sent_up_to = [(ts, score) for ts, score in aligned_sentiment if ts <= trade_ts]
        if len(sent_up_to) < 252:
            continue
        recent_21 = sent_up_to[-21:]
        mean_21 = sum(s for _, s in recent_21) / 21
        scores_252 = [s for _, s in sent_up_to[-252:]]
        scores_252_sorted = sorted(scores_252)
        tercile_low = scores_252_sorted[83]
        tercile_high = scores_252_sorted[167]
        if not (tercile_low <= mean_21 <= tercile_high):
            continue

        if trade_idx + 63 < len(trading_dates):
            start_ts = trade_ts
            end_ts = trading_dates[trade_idx + 63]
            if start_ts in bars_sym and end_ts in bars_sym:
                start_px = bars_sym[start_ts]
                end_px = bars_sym[end_ts]
                if start_px > 0:
                    fwd_return = (end_px - start_px) / start_px
                    label_down = 1 if fwd_return < 0 else 0
                    calls.append((trade_ts, symbol_id, label_down))

if not calls:
    print("INSUFFICIENT=1")
    sys.exit(0)

calls.sort(key=lambda x: x[0])
n_total = len(calls)
n_sealed = max(1, int(n_total * 0.2))
sealed_calls = calls[-n_sealed:]
train_calls = calls[:-n_sealed]

def compute_metrics(call_list):
    if not call_list:
        return 0, 0, 0.0, 0.0, 0, 0.0
    issued = len(call_list)
    hits = sum(1 for _, _, label in call_list if label == 1)
    precision = hits / issued if issued > 0 else 0.0
    base_rate = hits / issued if issued > 0 else 0.0
    distinct_days = len(set(datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d') for ts, _, _ in call_list))
    week_counts = Counter()
    for ts, _, _ in call_list:
        dt = datetime.utcfromtimestamp(ts)
        week_key = (dt.year, dt.isocalendar()[1])
        week_counts[week_key] += 1
    if len(week_counts) > 1:
        avg_cluster = issued / len(week_counts)
        rho = 0.2
        design_effect = 1 + (avg_cluster - 1) * rho
        effective_n = issued / design_effect
    else:
        effective_n = issued * 0.5
    return issued, hits, precision, base_rate, distinct_days, effective_n

issued_train, hits_train, prec_train, base_train, distinct_train, eff_train = compute_metrics(train_calls)
issued_sealed, hits_sealed, prec_sealed, base_sealed, distinct_sealed, eff_sealed = compute_metrics(sealed_calls)

total_issued = issued_train + issued_sealed
total_hits = hits_train + hits_sealed
total_precision = total_hits / total_issued if total_issued > 0 else 0.0
total_base_rate = total_hits / total_issued if total_issued > 0 else 0.0
total_distinct = distinct_train + distinct_sealed
total_effective = eff_train + eff_sealed

print(f"ISSUED={total_issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={total_precision:.6f}")
print(f"BASE_RATE={total_base_rate:.6f}")
print(f"DISTINCT_DAYS={total_distinct}")
print(f"EFFECTIVE_N={total_effective:.2f}")
print(f"SEALED_PRECISION={prec_sealed:.6f}")
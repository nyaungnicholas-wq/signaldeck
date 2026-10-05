# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 802
# cycle_index: 72
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, date
from collections import defaultdict
import statistics

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    symbols = [row['symbol_id'] for row in conn.execute("""
        SELECT DISTINCT i.symbol_id
        FROM insider_trades i
        JOIN sentiment_features s ON i.symbol_id = s.symbol_id
        WHERE i.code = 'P'
    """)]

    if not symbols:
        print("INSUFFICIENT=1")
        return

    placeholders = ','.join('?' * len(symbols))

    news_data = defaultdict(list)
    for row in conn.execute(f"""
        SELECT symbol_id, day, n_all
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbols):
        day = datetime.strptime(row['day'], '%Y-%m-%d').date()
        news_data[row['symbol_id']].append((day, row['n_all']))

    insider_data = defaultdict(list)
    for row in conn.execute(f"""
        SELECT symbol_id, code, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code IN ('P', 'S')
        ORDER BY symbol_id, filed_ts
    """, symbols):
        filed_date = datetime.utcfromtimestamp(row['filed_ts']).date()
        insider_data[row['symbol_id']].append((filed_date, row['code']))

    bars_data = defaultdict(list)
    for row in conn.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE symbol_id IN ({placeholders}) AND tf = '1d'
        ORDER BY symbol_id, ts
    """, symbols):
        ts_date = datetime.utcfromtimestamp(row['ts']).date()
        bars_data[row['symbol_id']].append((ts_date, row['close']))

    conn.close()

    all_events = []
    all_opportunities = []

    for symbol_id in symbols:
        news = news_data.get(symbol_id, [])
        insiders = insider_data.get(symbol_id, [])
        bars = bars_data.get(symbol_id, [])

        if not news or not insiders or not bars:
            continue

        news.sort(key=lambda x: x[0])
        news_days = [d for d, _ in news]
        news_counts = [c for _, c in news]

        p25 = {}
        for i, day in enumerate(news_days):
            if i >= 252:
                window = news_counts[i-252:i]
                if len(window) >= 252:
                    p25[day] = statistics.quantiles(window, n=4)[0]

        bars.sort(key=lambda x: x[0])
        trading_days = [d for d, _ in bars]
        day_to_close = {d: c for d, c in bars}
        day_to_idx = {d: i for i, d in enumerate(trading_days)}

        insider_by_date = defaultdict(list)
        for filed_date, code in insiders:
            insider_by_date[filed_date].append(code)

        for filed_date, codes in insider_by_date.items():
            has_p = 'P' in codes
            has_s = 'S' in codes
            if has_p and has_s:
                continue
            if not has_p:
                continue

            if filed_date not in p25:
                continue

            news_count = None
            for d, c in news:
                if d == filed_date:
                    news_count = c
                    break
            if news_count is None:
                continue

            all_opportunities.append((filed_date, symbol_id))

            if news_count < p25[filed_date]:
                decision_idx = None
                for i, td in enumerate(trading_days):
                    if td >= filed_date:
                        decision_idx = i
                        break
                if decision_idx is None:
                    continue
                if decision_idx + 21 >= len(trading_days):
                    continue

                t0 = trading_days[decision_idx]
                t21 = trading_days[decision_idx + 21]
                close_t0 = day_to_close[t0]
                close_t21 = day_to_close[t21]

                if close_t0 <= 0:
                    continue

                ret = (close_t21 - close_t0) / close_t0
                label = 1 if ret > 0 else 0
                all_events.append((filed_date, label, symbol_id))

    if not all_events:
        print("INSUFFICIENT=1")
        return

    all_events.sort(key=lambda x: x[0])
    all_opportunities.sort(key=lambda x: x[0])

    n_total = len(all_events)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed

    train_events = all_events[:n_train]
    sealed_events = all_events[n_train:]

    train_opportunities = [o for o in all_opportunities if o[0] <= train_events[-1][0]] if train_events else []
    sealed_opportunities = [o for o in all_opportunities if o[0] > train_events[-1][0]] if train_events else all_opportunities

    issued_train = len(train_events)
    hits_train = sum(1 for _, label, _ in train_events if label == 1)
    precision_train = hits_train / issued_train if issued_train > 0 else 0.0
    base_rate_train = hits_train / issued_train if issued_train > 0 else 0.0

    distinct_days_train = len(set(d for d, _, _ in train_events))

    design_effect = 1.0
    if issued_train > 1:
        overlaps = 0
        for i, (di, _, _) in enumerate(train_events):
            for j, (dj, _, _) in enumerate(train_events):
                if i != j:
                    diff = abs((di - dj).days)
                    if diff <= 21:
                        overlaps += 1
        avg_overlap = overlaps / issued_train
        design_effect = 1.0 + avg_overlap

    effective_n = issued_train / design_effect if design_effect > 0 else issued_train

    sealed_issued = len(sealed_events)
    sealed_hits = sum(1 for _, label, _ in sealed_events if label == 1)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    print(f"ISSUED={issued_train}")
    print(f"OPPORTUNITIES={len(train_opportunities)}")
    print(f"PRECISION={precision_train:.6f}")
    print(f"BASE_RATE={base_rate_train:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_train}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
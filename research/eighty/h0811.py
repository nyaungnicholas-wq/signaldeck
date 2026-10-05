# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 810
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    # Data availability: bars from 2018-07-26, insider_trades from 2008, news from 2012
    # Use period where all three overlap well: 2018-07-26 to 2024-12-31
    start_ts = int(datetime(2018, 7, 26, tzinfo=timezone.utc).timestamp())
    end_ts = int(datetime(2024, 12, 31, 23, 59, 59, tzinfo=timezone.utc).timestamp())

    # Sealed era: most recent 20% of time range
    split_ts = start_ts + int(0.8 * (end_ts - start_ts))

    # Get all insider open-market purchases (code='P') by officers/10% owners in range
    purchases = conn.execute("""
        SELECT it.symbol_id, it.filed_ts as disclosure_ts, it.tx_ts as trade_ts,
               it.shares, it.price, it.value, it.insider, it.title
        FROM insider_trades it
        WHERE it.code = 'P'
        AND it.filed_ts >= ? AND it.filed_ts <= ?
        ORDER BY it.filed_ts
    """, (start_ts, end_ts)).fetchall()

    if not purchases:
        print("INSUFFICIENT=1")
        return

    symbol_ids = list(set(p['symbol_id'] for p in purchases))

    # Pre-fetch daily bars (tf='1d') and news for all relevant symbols
    # Need bars from start_ts - 300 days (for 252-day history) to end_ts + 30 days (for forward return)
    bar_start = start_ts - 86400 * 300
    bar_end = end_ts + 86400 * 30

    bars_by_symbol = {}
    for sym_id in symbol_ids:
        rows = conn.execute("""
            SELECT ts, close, volume FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
            ORDER BY ts
        """, (sym_id, bar_start, bar_end)).fetchall()
        if len(rows) >= 252:
            bars_by_symbol[sym_id] = rows

    news_by_symbol = {}
    for sym_id in symbol_ids:
        rows = conn.execute("""
            SELECT ts FROM news WHERE symbol_id = ? AND ts >= ? AND ts <= ?
        """, (sym_id, bar_start, end_ts)).fetchall()
        if rows:
            counts = {}
            for r in rows:
                day = r['ts'] // 86400 * 86400
                counts[day] = counts.get(day, 0) + 1
            news_by_symbol[sym_id] = counts

    results = []
    opportunities = 0

    for pur in purchases:
        sym_id = pur['symbol_id']
        if sym_id not in bars_by_symbol or sym_id not in news_by_symbol:
            continue

        bars = bars_by_symbol[sym_id]
        news_counts = news_by_symbol[sym_id]

        disc_ts = pur['disclosure_ts']
        disc_day = disc_ts // 86400 * 86400

        # Officer or 10% owner check
        title = (pur['title'] or '').upper()
        is_officer = any(t in title for t in ['CEO', 'CFO', 'PRESIDENT', 'CHIEF EXECUTIVE', 'CHIEF FINANCIAL'])
        is_10pct = '10%' in title or 'TEN PERCENT' in title
        if not (is_officer or is_10pct):
            continue

        opportunities += 1

        # Find disclosure day in bars
        bar_days = [b['ts'] for b in bars]
        prices = {b['ts']: b['close'] for b in bars}
        volumes = {b['ts']: b['volume'] for b in bars}

        try:
            idx = bar_days.index(disc_day)
        except ValueError:
            prior = [d for d in bar_days if d <= disc_day]
            if not prior:
                continue
            disc_day = max(prior)
            idx = bar_days.index(disc_day)

        # Need 63 prior days for history, 21 forward for label
        if idx < 63 or idx + 21 >= len(bar_days):
            continue

        # Disclosure delay (days)
        delay_days = (pur['disclosure_ts'] - pur['trade_ts']) / 86400
        if delay_days > 1.5:  # minimal delay ~1 day
            continue

        # Volume on disclosure day: below 63-day median
        vol_63d = [volumes.get(bar_days[idx - i], 0) for i in range(1, 64)]
        vol_median = sorted(vol_63d)[len(vol_63d) // 2]
        if volumes.get(disc_day, 0) > vol_median:
            continue

        # 63-day price decline > 10%
        price_now = prices[disc_day]
        price_63d = prices[bar_days[idx - 63]]
        if (price_now - price_63d) / price_63d > -0.10:
            continue

        # 63-day avg news count in bottom quartile of 252-day history
        news_63d = [news_counts.get(bar_days[idx - i], 0) for i in range(1, 64)]
        avg_news_63d = sum(news_63d) / 63

        hist_len = min(252, idx)
        news_252d = [news_counts.get(bar_days[idx - i], 0) for i in range(1, hist_len + 1)]
        if len(news_252d) < 63:
            continue
        q1 = sorted(news_252d)[len(news_252d) // 4]
        if avg_news_63d > q1:
            continue

        # 21-day forward return
        price_fwd = prices[bar_days[idx + 21]]
        fwd_return = (price_fwd - price_now) / price_now
        up = 1 if fwd_return > 0 else 0

        era = 'sealed' if disc_ts >= split_ts else 'train'
        results.append({
            'symbol_id': sym_id,
            'disclosure_day': disc_day,
            'disclosure_ts': disc_ts,
            'up': up,
            'era': era
        })

    if not results:
        print("INSUFFICIENT=1")
        return

    train = [r for r in results if r['era'] == 'train']
    sealed = [r for r in results if r['era'] == 'sealed']

    issued_total = len(results)
    issued_train = len(train)
    issued_sealed = len(sealed)

    if issued_train == 0:
        print("INSUFFICIENT=1")
        return

    hits_train = sum(r['up'] for r in train)
    precision_train = hits_train / issued_train
    base_rate_train = hits_train / issued_train  # base rate of positive class within issued subset

    distinct_days_train = len(set(r['disclosure_day'] for r in train))
    distinct_days_total = len(set(r['disclosure_day'] for r in results))

    # Design effect via ICC on train set
    if distinct_days_train > 1 and issued_train > distinct_days_train:
        day_groups = {}
        for r in train:
            day_groups.setdefault(r['disclosure_day'], []).append(r['up'])
        day_means = [sum(v)/len(v) for v in day_groups.values()]
        grand_mean = sum(day_means) / len(day_means)
        n_per_day = [len(v) for v in day_groups.values()]
        avg_n = sum(n_per_day) / len(n_per_day)
        between_var = sum(n * (m - grand_mean)**2 for n, m in zip(n_per_day, day_means)) / (issued_train - len(day_groups))
        within_var = sum(sum((x - m)**2 for x in v) for v, m in zip(day_groups.values(), day_means)) / (issued_train - len(day_groups))
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        design_effect = 1 + (avg_n - 1) * icc
        design_effect = max(design_effect, 1.0)
        effective_n_train = issued_train / design_effect
    else:
        effective_n_train = min(distinct_days_train, issued_train - 1) if issued_train > 1 else 1

    # Total effective N
    if distinct_days_total > 1 and issued_total > distinct_days_total:
        day_groups = {}
        for r in results:
            day_groups.setdefault(r['disclosure_day'], []).append(r['up'])
        day_means = [sum(v)/len(v) for v in day_groups.values()]
        grand_mean = sum(day_means) / len(day_means)
        n_per_day = [len(v) for v in day_groups.values()]
        avg_n = sum(n_per_day) / len(n_per_day)
        between_var = sum(n * (m - grand_mean)**2 for n, m in zip(n_per_day, day_means)) / (issued_total - len(day_groups))
        within_var = sum(sum((x - m)**2 for x in v) for v, m in zip(day_groups.values(), day_means)) / (issued_total - len(day_groups))
        icc = between_var / (between_var + within_var) if (between_var + within_var) > 0 else 0
        design_effect = 1 + (avg_n - 1) * icc
        design_effect = max(design_effect, 1.0)
        effective_n_total = issued_total / design_effect
    else:
        effective_n_total = min(distinct_days_total, issued_total - 1) if issued_total > 1 else 1

    sealed_precision = sum(r['up'] for r in sealed) / issued_sealed if issued_sealed > 0 else 0.0

    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_train:.6f}")
    print(f"BASE_RATE={base_rate_train:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_total}")
    print(f"EFFECTIVE_N={effective_n_total:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
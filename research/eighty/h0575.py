# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 574
# cycle_index: 32
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

DB = 'file:data/signaldeck.db?mode=ro'

def query(conn, sql, params=()):
    cur = conn.execute(sql, params)
    return [dict(zip([d[0] for d in cur.description], row)) for row in cur.fetchall()]

def main():
    try:
        conn = sqlite3.connect(DB, uri=True, timeout=10)
    except Exception as e:
        print(f"INSUFFICIENT=1\nError connecting: {e}")
        return

    # 1. Universe: symbols with >=1yr daily bars and news sentiment, market cap >= $100M, not penny
    min_date_unix = 1532534400  # 2018-07-26 in unix seconds

    # Get symbols with enough daily bars
    symbols = query(conn, """
        SELECT s.id, s.symbol
        FROM symbols s
        JOIN (
            SELECT symbol_id, COUNT(*) as n_days
            FROM bars
            WHERE tf='1d' AND open > 0
            GROUP BY symbol_id
            HAVING n_days >= 252
        ) b ON b.symbol_id = s.id
        WHERE s.active = 1 AND s.market = 'stocks'
    """)

    # Filter by market cap >= $100M using fundamentals (EntityPublicFloat)
    valid_symbols = []
    for sym in symbols:
        rows = query(conn, """
            SELECT value FROM fundamentals
            WHERE symbol_id = ? AND metric = 'EntityPublicFloat'
            ORDER BY fetched_at DESC LIMIT 1
        """, (sym['id'],))
        if rows and float(rows[0]['value']) >= 1e8:
            valid_symbols.append(sym)

    # Filter by having news sentiment data
    symbols_with_sentiment = query(conn, """
        SELECT DISTINCT symbol_id FROM sentiment_features
    """)
    sentiment_ids = {r['symbol_id'] for r in symbols_with_sentiment}
    valid_symbols = [s for s in valid_symbols if s['id'] in sentiment_ids]

    if not valid_symbols:
        print("INSUFFICIENT=1")
        return

    # 2. Load all daily bars and sentiment for valid symbols
    symbol_ids = [s['id'] for s in valid_symbols]
    placeholders = ','.join(['?'] * len(symbol_ids))

    bars = query(conn, f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars WHERE tf='1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)

    sentiments = query(conn, f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbol_ids)

    # Group by symbol
    bars_by_sym = defaultdict(list)
    for b in bars:
        bars_by_sym[b['symbol_id']].append(b)

    sentiment_by_sym = defaultdict(list)
    for s in sentiments:
        # day is 'YYYY-MM-DD', convert to unix
        y, m, d = map(int, s['day'].split('-'))
        # Use simple epoch calculation (UTC)
        epoch = (y - 1970) * 31557600 + (m - 1) * 2629800 + (d - 1) * 86400
        sentiment_by_sym[s['symbol_id']].append({'ts': epoch, 'score': s['mean_score']})

    # 3. Load prediction_outcomes for 21-day horizon labels
    outcomes = query(conn, """
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes WHERE horizon = 21
    """)

    # Group outcomes by symbol
    outcome_by_sym = defaultdict(list)
    for o in outcomes:
        outcome_by_sym[o['symbol_id']].append(o)

    # 4. Process each symbol day by day
    issued = []
    opportunities = 0

    for sym_id, sym_bars in bars_by_sym.items():
        if len(sym_bars) < 202:  # Need 200-day MA + buffer
            continue
        if sym_id not in sentiment_by_sym:
            continue

        sym_sentiments = sorted(sentiment_by_sym[sym_id], key=lambda x: x['ts'])
        sym_outcomes = sorted(outcome_by_sym.get(sym_id, []), key=lambda x: x['ts'])

        # Precompute sentiment MA and slope
        sent_by_ts = {s['ts']: s['score'] for s in sym_sentiments}

        # Iterate over possible decision days (skip first 200 for 200-day MA)
        for i in range(200, len(sym_bars)):
            bar = sym_bars[i]
            dt = bar['ts']
            close = bar['close']

            # Check if we have label for 21 days forward
            horizon_ts = dt + 21 * 86400
            # Find label
            label_row = None
            for o in sym_outcomes:
                if o['ts'] >= dt and o['ts'] <= horizon_ts + 86400:
                    # Use the outcome closest to dt but after
                    if o['ts'] >= dt:
                        label_row = o
                        break
            if not label_row:
                continue

            opportunities += 1

            # Helper to get sentiment score for a given ts
            def get_sentiment(ts):
                # Find closest day in sentiment data
                best = None
                for s in sym_sentiments:
                    if s['ts'] <= ts:
                        best = s
                    else:
                        break
                return best['score'] if best else None

            # 1. Round number condition: within 1% of round number for 3 of past 5 sessions
            near_round = 0
            round_nums = []
            for j in range(5):
                if i - j < 0:
                    break
                p = sym_bars[i - j]['close']
                # Find nearest round number (multiple of 5, 10, 25, 50, 100)
                for base in [100, 50, 25, 10, 5]:
                    rn = round(p / base) * base
                    if rn > 0 and abs(p - rn) / rn <= 0.01:
                        near_round += 1
                        round_nums.append(rn)
                        break
            if near_round < 3:
                continue

            # 2. 10-day MA of sentiment positive and increasing (slope > 0)
            # Get last 10 sentiment scores
            sent_scores = []
            for j in range(10):
                if i - j < 0:
                    break
                ts_prev = sym_bars[i - j]['ts']
                s = get_sentiment(ts_prev)
                if s is not None:
                    sent_scores.append(s)
            if len(sent_scores) < 10:
                continue
            ma10 = sum(sent_scores) / 10
            if ma10 <= 0:
                continue

            # Compute slope: simple linear regression on last 10 points
            n = len(sent_scores)
            x_mean = (n - 1) / 2
            y_mean = sum(sent_scores) / n
            numerator = sum((i - x_mean) * (sent_scores[i] - y_mean) for i in range(n))
            denominator = sum((i - x_mean) ** 2 for i in range(n))
            slope = numerator / denominator if denominator != 0 else 0
            if slope <= 0:
                continue

            # 3. Stock has not closed above the round number in past 10 sessions
            # Use the round number from the condition (first found)
            if round_nums:
                target_rn = round_nums[0]
                above = False
                for j in range(10):
                    if i - j < 0:
                        break
                    if sym_bars[i - j]['close'] > target_rn:
                        above = True
                        break
                if above:
                    continue

            # ABSTAIN conditions
            # a) Price has already closed above round number in past 5 sessions
            abstain = False
            if round_nums:
                for j in range(5):
                    if i - j < 0:
                        break
                    if sym_bars[i - j]['close'] > target_rn:
                        abstain = True
                        break
            if abstain:
                continue

            # b) News sentiment negative or flat (slope ≤ 0) – already checked above

            # c) Stock below 200-day MA
            ma200 = sum(sym_bars[i-j]['close'] for j in range(200)) / 200
            if close < ma200:
                continue

            # If all conditions met, issue call
            issued.append({
                'symbol_id': sym_id,
                'day_ts': dt,
                'label_up': label_row['up'],
                'fwd_return': label_row['fwd_return']
            })

    if not issued:
        print("INSUFFICIENT=1")
        return

    # 5. Split into train (80%) and sealed (20%)
    issued.sort(key=lambda x: x['day_ts'])
    cut_idx = int(len(issued) * 0.8)
    train = issued[:cut_idx]
    sealed = issued[cut_idx:]

    # 6. Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, set()
        correct = sum(1 for c in calls if c['label_up'] == 1)
        days = {c['day_ts'] // 86400 for c in calls}
        return correct, len(calls), days

    # 7. Effective N with design effect (cluster by day)
    def effective_n(calls):
        if not calls:
            return 0
        # Group by day
        day_groups = defaultdict(list)
        for c in calls:
            day = c['day_ts'] // 86400
            day_groups[day].append(c)

        n_clusters = len(day_groups)
        total = len(calls)
        if n_clusters == 0 or total == 0:
            return 0

        # Compute ICC for binary outcome
        p = sum(c['label_up'] for c in calls) / total
        if p <= 0 or p >= 1:
            return total  # All same class, ICC undefined

        # ANOVA approach
        ssb = 0
        ssw = 0
        for day, group in day_groups.items():
            m = len(group)
            p_d = sum(c['label_up'] for c in group) / m
            ssb += m * (p_d - p) ** 2
            ssw += m * p_d * (1 - p_d)

        msb = ssb / (n_clusters - 1) if n_clusters > 1 else 0
        msw = ssw / (total - n_clusters) if total > n_clusters else 0

        if msb + msw == 0:
            return total

        icc = (msb - msw) / (msb + (total/n_clusters - 1) * msw) if msw != 0 else 0
        icc = max(0, min(1, icc))

        avg_m = total / n_clusters
        design_effect = 1 + (avg_m - 1) * icc
        return total / design_effect

    # Compute stats
    train_correct, train_issued, train_days = compute_metrics(train)
    sealed_correct, sealed_issued, sealed_days = compute_metrics(sealed)

    total_correct = train_correct + sealed_correct
    total_issued = len(issued)

    # Base rate within issued calls
    base_rate = total_correct / total_issued if total_issued > 0 else 0

    # Effective N
    eff_n = effective_n(issued)

    # Print required lines
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={total_correct / total_issued:.6f}" if total_issued else "PRECISION=0")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={len({c['day_ts'] // 86400 for c in issued})}")
    print(f"EFFECTIVE_N={eff_n:.2f}")
    print(f"SEALED_PRECISION={sealed_correct / sealed_issued:.6f}" if sealed_issued else "SEALED_PRECISION=0")

if __name__ == "__main__":
    main()
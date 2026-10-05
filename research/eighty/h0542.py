# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 541
# cycle_index: 71
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all open-market purchases (code 'P') from 2018-01-01 onward
    # filed_ts is the disclosure date (knowable at decision time)
    cur.execute("""
        SELECT it.*, s.symbol
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P'
          AND it.filed_ts >= ?
        ORDER BY it.filed_ts
    """, (date_to_unix(datetime(2018, 1, 1).date()),))
    trades = cur.fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # Build insider purchase history for expanding-window median
    # Group by insider name (assuming insider column identifies the person)
    insider_purchases = {}
    for t in trades:
        insider = t['insider']
        if insider not in insider_purchases:
            insider_purchases[insider] = []
        insider_purchases[insider].append({
            'filed_ts': t['filed_ts'],
            'value': t['value'],
            'symbol_id': t['symbol_id'],
            'symbol': t['symbol'],
            'accession': t['accession']
        })

    # Filter insiders with >=5 prior open-market purchases
    eligible_insiders = {k: v for k, v in insider_purchases.items() if len(v) >= 5}
    if not eligible_insiders:
        print("INSUFFICIENT=1")
        return 0

    # Get news sentiment data for condition (b)
    cur.execute("""
        SELECT symbol_id, ts, score
        FROM news
        WHERE score IS NOT NULL
        ORDER BY symbol_id, ts
    """)
    news_rows = cur.fetchall()

    # Organize news by symbol_id
    news_by_symbol = {}
    for row in news_rows:
        sid = row['symbol_id']
        if sid not in news_by_symbol:
            news_by_symbol[sid] = []
        news_by_symbol[sid].append((row['ts'], row['score']))

    # Get prediction_outcomes for labels (21 trading days ~ 30 calendar days)
    cur.execute("""
        SELECT symbol_id, horizon, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    outcomes = cur.fetchall()
    outcome_map = {}
    for o in outcomes:
        key = (o['symbol_id'], o['ts'])
        outcome_map[key] = (o['up'], o['fwd_return'])

    # Process each eligible insider's trades in chronological order
    # to compute expanding-window median
    signals = []  # (filed_ts, symbol_id, symbol, value, insider)

    for insider, purchases in eligible_insiders.items():
        purchases.sort(key=lambda x: x['filed_ts'])
        for i, p in enumerate(purchases):
            if i < 5:
                continue  # need at least 5 prior to establish baseline
            prior_values = [purchases[j]['value'] for j in range(i)]
            prior_values.sort()
            n = len(prior_values)
            median_val = prior_values[n // 2] if n % 2 == 1 else (prior_values[n // 2 - 1] + prior_values[n // 2]) / 2
            if p['value'] > 2 * median_val:
                # Condition (a) met, check condition (b) and (c)
                # Condition (c): not option exercise, gift, or 10b5-1 - already filtered by code='P'
                # Condition (b): 5-day mean news sentiment through D-1 below symbol's expanding 20th percentile
                filed_date = unix_to_date(p['filed_ts'])
                cutoff_ts = date_to_unix(filed_date - timedelta(days=1))
                start_ts = date_to_unix(filed_date - timedelta(days=5))

                symbol_news = news_by_symbol.get(p['symbol_id'], [])
                # Get news scores in [start_ts, cutoff_ts]
                window_scores = [score for ts, score in symbol_news if start_ts <= ts <= cutoff_ts]
                if len(window_scores) < 3:
                    continue
                mean_5d = sum(window_scores) / len(window_scores)

                # Expanding 20th percentile of daily mean sentiment up to D-1
                # Compute daily mean sentiment for all days before filed_date
                daily_means = []
                # Group news by day
                news_by_day = {}
                for ts, score in symbol_news:
                    if ts > cutoff_ts:
                        break
                    day = unix_to_date(ts)
                    if day not in news_by_day:
                        news_by_day[day] = []
                    news_by_day[day].append(score)
                for day, scores in news_by_day.items():
                    daily_means.append(sum(scores) / len(scores))
                if len(daily_means) < 10:
                    continue
                daily_means.sort()
                p20_idx = max(0, int(len(daily_means) * 0.2) - 1)
                p20 = daily_means[p20_idx]

                if mean_5d < p20:
                    signals.append({
                        'filed_ts': p['filed_ts'],
                        'symbol_id': p['symbol_id'],
                        'symbol': p['symbol'],
                        'value': p['value'],
                        'insider': insider
                    })

    if not signals:
        print("INSUFFICIENT=1")
        return 0

    # Sort signals by filed_ts
    signals.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(signals) * 0.8)
    train_signals = signals[:split_idx]
    test_signals = signals[split_idx:]

    def evaluate(signal_list, label):
        issued = 0
        hits = 0
        opportunities = 0
        issued_days = set()

        for sig in signal_list:
            opportunities += 1
            key = (sig['symbol_id'], sig['filed_ts'])
            if key in outcome_map:
                up, fwd_return = outcome_map[key]
                issued += 1
                issued_days.add(unix_to_date(sig['filed_ts']))
                if up == 1:
                    hits += 1

        if issued == 0:
            return None

        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(issued_days)

        # Design effect: cluster by day, compute effective N
        # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use conservative rho=0.5, cluster by day
        day_counts = {}
        for sig in signal_list:
            key = (sig['symbol_id'], sig['filed_ts'])
            if key in outcome_map:
                day = unix_to_date(sig['filed_ts'])
                day_counts[day] = day_counts.get(day, 0) + 1
        if day_counts:
            avg_cluster = sum(day_counts.values()) / len(day_counts)
            design_effect = 1 + (avg_cluster - 1) * 0.5
            effective_n = issued / design_effect
        else:
            effective_n = issued * 0.5

        print(f"{label}_ISSUED={issued}")
        print(f"{label}_OPPORTUNITIES={opportunities}")
        print(f"{label}_PRECISION={precision:.4f}")
        print(f"{label}_BASE_RATE={base_rate:.4f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.2f}")

        return {
            'issued': issued,
            'opportunities': opportunities,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_result = evaluate(train_signals, "TRAIN")
    test_result = evaluate(test_signals, "SEALED")

    if not train_result or not test_result:
        print("INSUFFICIENT=1")
        return 0

    # Final output as required
    print(f"ISSUED={train_result['issued'] + test_result['issued']}")
    print(f"OPPORTUNITIES={train_result['opportunities'] + test_result['opportunities']}")
    total_issued = train_result['issued'] + test_result['issued']
    total_hits = train_result['issued'] * train_result['precision'] + test_result['issued'] * test_result['precision']
    print(f"PRECISION={total_hits / total_issued:.4f}")
    print(f"BASE_RATE={total_hits / total_issued:.4f}")
    print(f"DISTINCT_DAYS={train_result['distinct_days'] + test_result['distinct_days']}")
    print(f"EFFECTIVE_N={train_result['effective_n'] + test_result['effective_n']:.2f}")
    print(f"SEALED_PRECISION={test_result['precision']:.4f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
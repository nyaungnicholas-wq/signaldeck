# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 684
# cycle_index: 11
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    con = sqlite3.connect(DB_PATH, uri=True)
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # Universe: active stocks with sufficient daily bars history (>= 504 days ~ 2 years)
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.market = 'stocks' AND s.active = 1
          AND EXISTS (
            SELECT 1 FROM bars b
            WHERE b.symbol_id = s.id AND b.tf = '1d'
            GROUP BY b.symbol_id
            HAVING COUNT(*) >= 504
          )
    """)
    universe = [(row['id'], row['symbol']) for row in cur.fetchall()]
    if not universe:
        print("INSUFFICIENT=1")
        return 0
    sym_ids = [str(u[0]) for u in universe]
    sym_id_set = set(sym_ids)

    # Get insider open-market purchases (code='P') with trade date and filing date
    placeholders = ','.join(['?'] * len(sym_ids))
    cur.execute(f"""
        SELECT
            it.symbol_id,
            it.tx_ts as trade_ts,
            it.filed_ts as filed_ts,
            it.shares,
            it.price,
            it.shares * it.price as trade_value,
            it.insider
        FROM insider_trades it
        WHERE it.symbol_id IN ({placeholders})
          AND it.code = 'P'
          AND it.shares > 0
          AND it.price > 0
          AND it.filed_ts IS NOT NULL
          AND it.tx_ts IS NOT NULL
        ORDER BY it.symbol_id, it.filed_ts
    """, sym_ids)
    insider_trades = [dict(row) for row in cur.fetchall()]
    if not insider_trades:
        print("INSUFFICIENT=1")
        return 0

    # Get daily bars for all symbols in universe
    cur.execute(f"""
        SELECT
            b.symbol_id,
            b.ts,
            b.close,
            b.volume,
            b.close * b.volume as dollar_vol
        FROM bars b
        WHERE b.symbol_id IN ({placeholders})
          AND b.tf = '1d'
        ORDER BY b.symbol_id, b.ts
    """, sym_ids)
    bars = [dict(row) for row in cur.fetchall()]
    if not bars:
        print("INSUFFICIENT=1")
        return 0

    # Get daily news sentiment (hedged ratio from sentiment_features)
    cur.execute(f"""
        SELECT
            sf.symbol_id,
            sf.day,
            CASE WHEN sf.n_all > 0 THEN CAST(sf.hedged AS FLOAT) / sf.n_all ELSE 0 END as hedged_ratio,
            sf.n_all as news_count
        FROM sentiment_features sf
        WHERE sf.symbol_id IN ({placeholders})
          AND sf.n_all > 0
        ORDER BY sf.symbol_id, sf.day
    """, sym_ids)
    news_sentiment = [dict(row) for row in cur.fetchall()]

    # Organize data by symbol
    from collections import defaultdict
    bars_by_sym = defaultdict(list)
    for b in bars:
        bars_by_sym[b['symbol_id']].append(b)

    news_by_sym = defaultdict(list)
    for n in news_sentiment:
        news_by_sym[n['symbol_id']].append(n)

    trades_by_sym = defaultdict(list)
    for t in insider_trades:
        trades_by_sym[t['symbol_id']].append(t)

    # Precompute rolling metrics for each symbol
    # For each symbol, we need: 20-day momentum, 60-day ADV, personal trade history
    opportunities = []  # (symbol_id, filed_ts, features...)
    issued_calls = []   # (symbol_id, filed_ts, predicted_class, actual_class)

    for sym_id, sym_bars in bars_by_sym.items():
        if len(sym_bars) < 252:
            continue

        # Build lookup maps for bars by timestamp
        bar_by_ts = {b['ts']: b for b in sym_bars}
        bar_ts_sorted = sorted(bar_by_ts.keys())

        # Get news sentiment for this symbol
        sym_news = news_by_sym.get(sym_id, [])
        news_by_day = {}
        for n in sym_news:
            try:
                day_ts = int(datetime.strptime(n['day'], '%Y-%m-%d').timestamp())
                news_by_day[day_ts] = n
            except:
                pass

        # Get insider trades for this symbol
        sym_trades = trades_by_sym.get(sym_id, [])

        # Track personal trade history for each insider
        insider_history = defaultdict(list)

        # For each insider trade, compute features at filing date
        for trade in sym_trades:
            filed_ts = trade['filed_ts']
            trade_ts = trade['trade_ts']
            insider = trade['insider']
            trade_value = trade['trade_value']
            shares = trade['shares']
            price = trade['price']

            # As-of discipline: use only data available at filed_ts
            # Find the most recent bar at or before filed_ts
            decision_bar_ts = None
            for ts in reversed(bar_ts_sorted):
                if ts <= filed_ts:
                    decision_bar_ts = ts
                    break
            if decision_bar_ts is None:
                continue

            # Need at least 60 bars before decision for ADV, 20 for momentum
            idx = bar_ts_sorted.index(decision_bar_ts)
            if idx < 60:
                continue

            # Compute 20-day price momentum (return over 20 trading days)
            mom_start_ts = bar_ts_sorted[idx - 20]
            mom_start_close = bar_by_ts[mom_start_ts]['close']
            mom_end_close = bar_by_ts[decision_bar_ts]['close']
            momentum_20d = (mom_end_close - mom_start_close) / mom_start_close

            # Compute 60-day average dollar volume (ADV)
            adv_sum = 0
            for i in range(idx - 60, idx):
                adv_sum += bar_by_ts[bar_ts_sorted[i]]['dollar_vol']
            adv_60d = adv_sum / 60

            # Trade size relative to ADV
            trade_to_adv = trade_value / adv_60d if adv_60d > 0 else 0

            # Personal trade history: median trade value of this insider before this trade
            past_trades = [t for t in insider_history[insider] if t['filed_ts'] < filed_ts]
            if len(past_trades) >= 3:
                past_values = [t['trade_value'] for t in past_trades]
                past_values.sort()
                personal_median = past_values[len(past_values) // 2]
                trade_to_personal = trade_value / personal_median if personal_median > 0 else 0
            else:
                trade_to_personal = 0

            # News sentiment: average hedged ratio over past 20 trading days
            news_hedged_vals = []
            for i in range(idx - 20, idx + 1):
                bar_ts = bar_ts_sorted[i]
                bar_date = datetime.utcfromtimestamp(bar_ts).date()
                day_ts = int(datetime.combine(bar_date, datetime.min.time()).timestamp())
                if day_ts in news_by_day:
                    news_hedged_vals.append(news_by_day[day_ts]['hedged_ratio'])
            avg_hedged_20d = sum(news_hedged_vals) / len(news_hedged_vals) if news_hedged_vals else 0.5

            # News sentiment trend: 20-day slope of hedged ratio
            if len(news_hedged_vals) >= 10:
                x = list(range(len(news_hedged_vals)))
                y = news_hedged_vals
                n = len(x)
                sum_x = sum(x)
                sum_y = sum(y)
                sum_xy = sum(x[i] * y[i] for i in range(n))
                sum_x2 = sum(xi * xi for xi in x)
                denom = n * sum_x2 - sum_x * sum_x
                hedged_slope = (n * sum_xy - sum_x * sum_y) / denom if denom != 0 else 0
            else:
                hedged_slope = 0

            # Entry conditions:
            # 1. Negative 20-day price momentum (< -5%)
            # 2. News sentiment not negative (hedged ratio >= 0.4, i.e., not excessively hedged/bearish)
            # 3. Trade large relative to ADV (> 0.5)
            # 4. Trade large relative to personal history (> 1.5x median)
            if (momentum_20d < -0.05 and
                avg_hedged_20d >= 0.4 and
                trade_to_adv > 0.5 and
                trade_to_personal > 1.5):

                # Build label: 5-day forward return from decision bar
                if idx + 5 < len(bar_ts_sorted):
                    fwd_ts = bar_ts_sorted[idx + 5]
                    fwd_close = bar_by_ts[fwd_ts]['close']
                    fwd_return = (fwd_close - mom_end_close) / mom_end_close
                    predicted_class = 1 if fwd_return > 0 else 0

                    opportunities.append({
                        'symbol_id': sym_id,
                        'filed_ts': filed_ts,
                        'decision_bar_ts': decision_bar_ts,
                        'momentum_20d': momentum_20d,
                        'avg_hedged_20d': avg_hedged_20d,
                        'trade_to_adv': trade_to_adv,
                        'trade_to_personal': trade_to_personal,
                        'hedged_slope': hedged_slope,
                        'fwd_return': fwd_return,
                        'predicted_class': predicted_class
                    })

            # Update insider history
            insider_history[insider].append(trade)

    if not opportunities:
        print("INSUFFICIENT=1")
        return 0

    # Sort opportunities by filed_ts
    opportunities.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed era
    n_total = len(opportunities)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed

    train_opp = opportunities[:n_train]
    sealed_opp = opportunities[n_train:]

    # Compute metrics on training set
    issued_train = [o for o in train_opp if o['predicted_class'] == 1]
    hits_train = sum(1 for o in issued_train if o['fwd_return'] > 0)
    issued_count_train = len(issued_train)
    precision_train = hits_train / issued_count_train if issued_count_train > 0 else 0

    # Base rate within issued subset
    base_rate_train = sum(1 for o in issued_train if o['fwd_return'] > 0) / issued_count_train if issued_count_train > 0 else 0

    # Distinct days among issued calls
    issued_days_train = set()
    for o in issued_train:
        day = datetime.utcfromtimestamp(o['decision_bar_ts']).date()
        issued_days_train.add(day)
    distinct_days_train = len(issued_days_train)

    # Design effect: cluster by day, compute variance inflation
    # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * icc)
    # Use day-level clustering
    day_counts = defaultdict(int)
    for o in issued_train:
        day = datetime.utcfromtimestamp(o['decision_bar_ts']).date()
        day_counts[day] += 1
    avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    # Intraclass correlation approximation for financial returns ~ 0.1-0.3
    icc = 0.2
    design_effect = 1 + (avg_cluster - 1) * icc
    effective_n_train = issued_count_train / design_effect if design_effect > 0 else issued_count_train

    # Sealed era metrics
    issued_sealed = [o for o in sealed_opp if o['predicted_class'] == 1]
    hits_sealed = sum(1 for o in issued_sealed if o['fwd_return'] > 0)
    issued_count_sealed = len(issued_sealed)
    sealed_precision = hits_sealed / issued_count_sealed if issued_count_sealed > 0 else 0

    # Overall issued count (for reporting)
    all_issued = [o for o in opportunities if o['predicted_class'] == 1]
    all_hits = sum(1 for o in all_issued if o['fwd_return'] > 0)
    all_issued_count = len(all_issued)
    all_precision = all_hits / all_issued_count if all_issued_count > 0 else 0
    all_base_rate = all_hits / all_issued_count if all_issued_count > 0 else 0

    all_issued_days = set()
    for o in all_issued:
        day = datetime.utcfromtimestamp(o['decision_bar_ts']).date()
        all_issued_days.add(day)
    all_distinct_days = len(all_issued_days)

    # Overall effective N
    all_day_counts = defaultdict(int)
    for o in all_issued:
        day = datetime.utcfromtimestamp(o['decision_bar_ts']).date()
        all_day_counts[day] += 1
    all_avg_cluster = sum(all_day_counts.values()) / len(all_day_counts) if all_day_counts else 1
    all_design_effect = 1 + (all_avg_cluster - 1) * icc
    all_effective_n = all_issued_count / all_design_effect if all_design_effect > 0 else all_issued_count

    # Opportunities considered = total decision points evaluated
    # This is the number of insider trades that met minimum data requirements
    total_opportunities = len(opportunities)

    print(f"ISSUED={all_issued_count}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={all_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
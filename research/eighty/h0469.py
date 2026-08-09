# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 468
# cycle_index: 59
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import bisect

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Universe: symbols with insider trades and 1d bars
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN insider_trades it ON it.symbol_id = s.id
        JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
        WHERE s.active = 1
    """)
    universe = [(row['id'], row['symbol']) for row in cur.fetchall()]
    if not universe:
        print("INSUFFICIENT=1")
        return
    symbol_ids = [str(s[0]) for s in universe]
    placeholders = ','.join('?' * len(symbol_ids))

    # 2. All trading days (1d bars distinct ts)
    cur.execute("SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts")
    trading_days = [row['ts'] for row in cur.fetchall()]
    if not trading_days:
        print("INSUFFICIENT=1")
        return
    trading_day_dates = [datetime.utcfromtimestamp(ts).date() for ts in trading_days]
    td_index = {ts: i for i, ts in enumerate(trading_days)}

    # 3. STLFSI weekly (Friday values)
    cur.execute("SELECT ts, value FROM macro_series WHERE series = 'STLFSI' ORDER BY ts")
    stlfs_rows = cur.fetchall()
    stlfs_weekly = {}
    for row in stlfs_rows:
        dt = datetime.utcfromtimestamp(row['ts'])
        days_to_fri = (4 - dt.weekday()) % 7
        fri = dt + timedelta(days=days_to_fri)
        fri_ts = int(fri.replace(hour=0, minute=0, second=0, microsecond=0).timestamp())
        stlfs_weekly[fri_ts] = row['value']

    fridays = sorted(stlfs_weekly.keys())
    fri_values = [stlfs_weekly[f] for f in fridays]

    decline_5 = {}
    for i in range(4, len(fridays)):
        if all(fri_values[i-j] < fri_values[i-j-1] for j in range(4)):
            decline_5[fridays[i]] = True

    td_to_decline5 = {}
    fri_idx = 0
    for td in trading_days:
        while fri_idx < len(fridays) and fridays[fri_idx] <= td:
            fri_idx += 1
        if fri_idx > 0:
            latest_fri = fridays[fri_idx - 1]
            td_to_decline5[td] = decline_5.get(latest_fri, False)
        else:
            td_to_decline5[td] = False

    # 4. Insider purchases for universe symbols
    cur.execute(f"""
        SELECT symbol_id, insider, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, tx_ts
    """, symbol_ids)
    insider_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        insider_by_symbol[row['symbol_id']].append({
            'insider': row['insider'],
            'tx_ts': row['tx_ts'],
            'filed_ts': row['filed_ts']
        })

    # 5. Prediction outcomes for horizon=21
    cur.execute(f"""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21 AND symbol_id IN ({placeholders})
    """, symbol_ids)
    outcomes_by_symbol = defaultdict(dict)
    for row in cur.fetchall():
        outcomes_by_symbol[row['symbol_id']][row['ts']] = row['up']

    # 6. Daily bars for universe symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)

    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))

    conn.close()

    # Precompute insider trade trading day indices
    for sym_id, trades in insider_by_symbol.items():
        for t in trades:
            tx_date = datetime.utcfromtimestamp(t['tx_ts']).date()
            idx = bisect.bisect_left(trading_day_dates, tx_date)
            if idx < len(trading_day_dates) and trading_day_dates[idx] == tx_date:
                t['td_idx'] = idx
            else:
                t['td_idx'] = -1

    all_decisions = []  # (call_ts, symbol_id, issued, hit)

    for symbol_id, symbol_name in universe:
        bars = bars_by_symbol.get(symbol_id, [])
        if len(bars) < 252:
            continue

        bar_ts = [b[0] for b in bars]
        bar_close = [b[1] for b in bars]

        sma = [None] * len(bars)
        window_sum = sum(bar_close[:252])
        sma[251] = window_sum / 252
        for i in range(252, len(bars)):
            window_sum += bar_close[i] - bar_close[i-252]
            sma[i] = window_sum / 252

        insiders = insider_by_symbol.get(symbol_id, [])
        outcomes = outcomes_by_symbol.get(symbol_id, {})

        for i in range(251, len(bars)):
            call_ts = bar_ts[i]
            close = bar_close[i]
            sma_val = sma[i]

            if close >= sma_val:
                all_decisions.append((call_ts, symbol_id, False, None))
                continue

            if not td_to_decline5.get(call_ts, False):
                all_decisions.append((call_ts, symbol_id, False, None))
                continue

            td_idx = td_index.get(call_ts)
            if td_idx is None or td_idx < 4:
                all_decisions.append((call_ts, symbol_id, False, None))
                continue

            window_start_idx = td_idx - 4
            window_end_idx = td_idx

            distinct_insiders = set()
            for trade in insiders:
                t_idx = trade.get('td_idx', -1)
                if window_start_idx <= t_idx <= window_end_idx and trade['filed_ts'] <= call_ts:
                    distinct_insiders.add(trade['insider'])

            if len(distinct_insiders) < 2:
                all_decisions.append((call_ts, symbol_id, False, None))
                continue

            hit = outcomes.get(call_ts)
            if hit is None:
                all_decisions.append((call_ts, symbol_id, True, None))
                continue

            all_decisions.append((call_ts, symbol_id, True, bool(hit)))

    if not all_decisions:
        print("INSUFFICIENT=1")
        return

    all_decisions.sort(key=lambda x: x[0])

    issued_decisions = [d for d in all_decisions if d[2]]
    if not issued_decisions:
        print("INSUFFICIENT=1")
        return

    n_total = len(all_decisions)
    split_idx = int(n_total * 0.8)
    train_decisions = all_decisions[:split_idx]
    sealed_decisions = all_decisions[split_idx:]

    train_issued = [d for d in train_decisions if d[2]]
    sealed_issued = [d for d in sealed_decisions if d[2]]

    if not train_issued:
        print("INSUFFICIENT=1")
        return

    train_hits = sum(1 for d in train_issued if d[3] is True)
    train_misses = sum(1 for d in train_issued if d[3] is False)
    train_issued_with_outcome = train_hits + train_misses

    if train_issued_with_outcome == 0:
        print("INSUFFICIENT=1")
        return

    precision = train_hits / train_issued_with_outcome
    base_rate = train_hits / train_issued_with_outcome

    train_issued_dates = set(datetime.utcfromtimestamp(d[0]).date() for d in train_issued)
    distinct_days = len(train_issued_dates)

    # Design effect via weekly clustering
    from collections import Counter
    week_hits = defaultdict(list)
    for d in train_issued:
        if d[3] is not None:
            dt = datetime.utcfromtimestamp(d[0])
            week_key = (dt.year, dt.isocalendar()[1])
            week_hits[week_key].append(1 if d[3] else 0)

    if len(week_hits) > 1:
        cluster_sizes = [len(v) for v in week_hits.values()]
        avg_cluster = sum(cluster_sizes) / len(cluster_sizes)
        cluster_means = [sum(v)/len(v) for v in week_hits.values()]
        overall_mean = sum(cluster_means) / len(cluster_means)
        between_var = sum((m - overall_mean)**2 for m in cluster_means) / (len(cluster_means) - 1) if len(cluster_means) > 1 else 0
        within_var = sum(sum((x - m)**2 for x in v) for v, m in zip(week_hits.values(), cluster_means)) / (sum(cluster_sizes) - len(cluster_sizes)) if sum(cluster_sizes) > len(cluster_sizes) else 0
        if within_var > 0 and between_var >= 0:
            icc = (between_var - within_var) / (between_var + (avg_cluster - 1) * within_var) if (between_var + (avg_cluster - 1) * within_var) > 0 else 0
        else:
            icc = 0
        icc = max(0, icc)
        design_effect = 1 + (avg_cluster - 1) * icc
    else:
        design_effect = 1.001

    design_effect = max(design_effect, 1.001)
    effective_n = len(train_issued) / design_effect

    sealed_precision = 0.0
    sealed_issued_with_outcome = [d for d in sealed_issued if d[3] is not None]
    if sealed_issued_with_outcome:
        sealed_hits = sum(1 for d in sealed_issued_with_outcome if d[3])
        sealed_precision = sealed_hits / len(sealed_issued_with_outcome)

    print(f"ISSUED={len(train_issued)}")
    print(f"OPPORTUNITIES={n_total}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()
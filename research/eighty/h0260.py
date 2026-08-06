import sqlite3
import sys
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get all insider purchase filings (code='P') with filed_ts as decision date
    cur.execute("""
        SELECT symbol_id, insider, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY symbol_id, filed_ts
    """)
    insider_rows = cur.fetchall()
    if not insider_rows:
        print("INSUFFICIENT=1")
        return 0

    # Group by symbol_id and filed_ts (date) to count unique insiders per day
    insider_by_sym_date = defaultdict(set)
    for row in insider_rows:
        date_key = row['filed_ts'] // 86400  # unix day
        insider_by_sym_date[(row['symbol_id'], date_key)].add(row['insider'])

    # 2. Find candidate decision points: >=3 unique insiders in T-5..T window
    candidates = []
    for (sym_id, day), insiders in insider_by_sym_date.items():
        # Count unique insiders in 5-day window ending at day
        window_insiders = set()
        for d in range(day - 4, day + 1):
            window_insiders.update(insider_by_sym_date.get((sym_id, d), set()))
        if len(window_insiders) >= 3:
            candidates.append((sym_id, day, day * 86400))  # (symbol_id, day_key, filed_ts)

    if not candidates:
        print("INSUFFICIENT=1")
        return 0

    # 3. Get daily bars for all symbols in candidates
    sym_ids = list(set(c[0] for c in candidates))
    placeholders = ','.join('?' * len(sym_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, sym_ids)
    bar_rows = cur.fetchall()

    # Organize bars by symbol_id
    bars_by_sym = defaultdict(list)
    for row in bar_rows:
        bars_by_sym[row['symbol_id']].append({
            'ts': row['ts'],
            'day': row['ts'] // 86400,
            'open': row['open'],
            'high': row['high'],
            'low': row['low'],
            'close': row['close'],
            'volume': row['volume']
        })

    # 4. Get prediction outcomes for horizon=20 (T+20 trading days)
    # We need to match on symbol_id and ts (decision timestamp)
    cur.execute("""
        SELECT symbol_id, horizon, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 20
    """)
    outcome_rows = cur.fetchall()
    outcomes = {}
    for row in outcome_rows:
        outcomes[(row['symbol_id'], row['ts'])] = (row['up'], row['fwd_return'])

    # 5. Process each candidate
    calls = []  # (symbol_id, decision_ts, decision_day, label_up, label_fwd)
    last_call_day = {}  # symbol_id -> last call day

    for sym_id, decision_day, decision_ts in candidates:
        bars = bars_by_sym.get(sym_id, [])
        if len(bars) < 253:  # need 252 prior + current
            continue

        # Find bar index for decision_day (T)
        # We need bar at T-1 for entry conditions (close at T-1)
        # Decision is at filed_ts (T), but bars are daily. We need the bar for the trading day of T.
        # Since filed_ts is unix timestamp, find the bar with day <= decision_day
        bar_idx = None
        for i, b in enumerate(bars):
            if b['day'] <= decision_day:
                bar_idx = i
            else:
                break
        if bar_idx is None or bar_idx < 252:  # need 252 prior completed sessions
            continue

        bar_T = bars[bar_idx]  # bar at or before decision day
        bar_Tm1 = bars[bar_idx - 1]  # prior session (T-1)

        # Universe checks at T
        close_T = bar_T['close']
        if close_T < 5.0:
            continue

        # ADV over T-60..T-1 (60 sessions ending at T-1)
        if bar_idx < 60:
            continue
        adv_bars = bars[bar_idx - 60:bar_idx]
        adv = sum(b['close'] * b['volume'] for b in adv_bars) / 60.0
        if adv < 5_000_000:
            continue

        # Entry conditions at T-1
        # 252-day high as of T-1
        high_252 = max(b['high'] for b in bars[bar_idx - 252:bar_idx])
        drawdown = (high_252 - bar_Tm1['close']) / high_252
        if drawdown < 0.20:
            continue

        # 20-day return through T-1
        if bar_idx < 20:
            continue
        ret_20 = (bar_Tm1['close'] - bars[bar_idx - 20]['close']) / bars[bar_idx - 20]['close']
        if ret_20 > -0.15:
            continue

        # Abstain: call issued for same symbol in prior 20 trading days
        if sym_id in last_call_day and (bar_Tm1['day'] - last_call_day[sym_id]) < 20:
            continue

        # We'll compute volatility decile cross-sectionally later
        # For now, collect candidate with needed data for vol calc
        calls.append({
            'symbol_id': sym_id,
            'decision_ts': decision_ts,
            'decision_day': bar_T['day'],
            'bar_idx': bar_idx,
            'bars': bars,
            'close_T': close_T,
        })

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # 6. Compute 20-session realized volatility at T for each candidate (cross-sectional decile)
    # Volatility = std of 20 daily log returns ending at T
    import math
    for c in calls:
        idx = c['bar_idx']
        bars = c['bars']
        if idx < 20:
            c['vol'] = None
            continue
        rets = []
        for i in range(idx - 19, idx + 1):
            r = math.log(bars[i]['close'] / bars[i - 1]['close'])
            rets.append(r)
        mean_ret = sum(rets) / len(rets)
        var = sum((r - mean_ret) ** 2 for r in rets) / len(rets)
        c['vol'] = math.sqrt(var) * math.sqrt(252)  # annualized

    # Filter out None vols
    calls_with_vol = [c for c in calls if c['vol'] is not None]
    if not calls_with_vol:
        print("INSUFFICIENT=1")
        return 0

    # Cross-sectional decile at each decision day
    # Group by decision_day
    by_day = defaultdict(list)
    for c in calls_with_vol:
        by_day[c['decision_day']].append(c)

    for day, day_calls in by_day.items():
        vols = sorted(c['vol'] for c in day_calls)
        n = len(vols)
        if n == 0:
            continue
        # Top decile threshold (90th percentile)
        thresh_idx = int(0.9 * n)
        if thresh_idx >= n:
            thresh_idx = n - 1
        threshold = vols[thresh_idx]
        for c in day_calls:
            c['vol_top_decile'] = c['vol'] > threshold

    # 7. Final filtering and label matching
    issued = []
    for c in calls_with_vol:
        if c.get('vol_top_decile', False):
            continue
        # Match outcome
        key = (c['symbol_id'], c['decision_ts'])
        if key not in outcomes:
            # Try matching by day (since prediction_outcomes ts might be bar timestamp)
            # Find closest bar ts <= decision_ts
            bar_ts = c['bars'][c['bar_idx']]['ts']
            key2 = (c['symbol_id'], bar_ts)
            if key2 in outcomes:
                key = key2
            else:
                continue
        up, fwd = outcomes[key]
        issued.append({
            'symbol_id': c['symbol_id'],
            'decision_ts': c['decision_ts'],
            'decision_day': c['decision_day'],
            'up': up,
            'fwd_return': fwd,
        })
        last_call_day[c['symbol_id']] = c['decision_day']

    if len(issued) < 30:
        print("INSUFFICIENT=1")
        return 0

    # 8. Split into sealed era (most recent 20%) and training
    issued.sort(key=lambda x: x['decision_ts'])
    n_total = len(issued)
    n_sealed = max(1, int(0.2 * n_total))
    sealed = issued[-n_sealed:]
    train = issued[:-n_sealed]

    # 9. Compute metrics
    def compute_metrics(calls_list):
        if not calls_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        n = len(calls_list)
        hits = sum(1 for c in calls_list if c['up'] == 1)
        precision = hits / n if n > 0 else 0.0
        base_rate = hits / n if n > 0 else 0.0  # base rate of UP within issued subset
        distinct_days = len(set(c['decision_day'] for c in calls_list))
        # Design effect: 1 + (avg_cluster_size - 1) * ICC
        # Approximate: group by day, compute cluster sizes
        day_counts = defaultdict(int)
        for c in calls_list:
            day_counts[c['decision_day']] += 1
        cluster_sizes = list(day_counts.values())
        avg_cluster = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
        # Conservative ICC estimate for financial returns ~0.1-0.3
        icc = 0.2
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = n / design_effect
        return n, hits, precision, base_rate, distinct_days, effective_n

    # Overall metrics (on full issued set for reporting)
    n_issued, n_hits, precision, base_rate, distinct_days, effective_n = compute_metrics(issued)
    sealed_n, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed)

    # Opportunities: total decision points considered (candidates passing universe/entry before abstain)
    # We need to count all (symbol, day) that were evaluated
    # This is the number of candidates that passed universe and entry checks
    opportunities = len(calls_with_vol)  # those that passed up to vol check

    # Abstention rate = 1 - issued / opportunities
    # But we need abstention rate >= 0.95 per claim
    # That means issued / opportunities <= 0.05

    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
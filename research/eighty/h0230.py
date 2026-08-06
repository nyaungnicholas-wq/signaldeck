import sqlite3
import sys
from collections import defaultdict, deque
from array import array
from math import sqrt
from datetime import datetime, timezone

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load all symbols
    cur.execute("SELECT id, symbol, delisted_at FROM symbols")
    symbols = {row['id']: {'symbol': row['symbol'], 'delisted_at': row['delisted_at']} for row in cur.fetchall()}
    symbol_ids = list(symbols.keys())

    # Load all 1d bars ordered by ts, symbol_id
    cur.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY ts, symbol_id
    """)
    rows = cur.fetchall()

    # Group bars by day (ts)
    bars_by_day = defaultdict(list)
    for row in rows:
        bars_by_day[row['ts']].append(row)

    trading_days = sorted(bars_by_day.keys())
    if not trading_days:
        print("INSUFFICIENT=1")
        return 0

    # Per-symbol rolling history: keep last 252 bars (ts, close, high, low, volume, dollar_vol)
    hist = {sid: {
        'ts': array('q'),
        'close': array('d'),
        'high': array('d'),
        'low': array('d'),
        'volume': array('d'),
        'dollar_vol': array('d')
    } for sid in symbol_ids}

    # Track calls issued per symbol (last 20 trading days)
    symbol_call_days = {sid: deque() for sid in symbol_ids}

    # All issued calls: list of (symbol_id, ts, day_idx)
    issued_calls = []

    # All opportunities (symbol-days that passed universe filters)
    opportunities = 0

    # Process each trading day in chronological order
    for day_idx, ts in enumerate(trading_days):
        day_bars = bars_by_day[ts]

        # Update histories with today's bars
        for bar in day_bars:
            sid = bar['symbol_id']
            h = hist[sid]
            h['ts'].append(bar['ts'])
            h['close'].append(bar['close'])
            h['high'].append(bar['high'])
            h['low'].append(bar['low'])
            h['volume'].append(bar['volume'])
            h['dollar_vol'].append(bar['close'] * bar['volume'])
            # Keep only last 252
            if len(h['ts']) > 252:
                for k in h:
                    del h[k][0]

        # Universe filtering for this day
        universe_symbols = []
        for bar in day_bars:
            sid = bar['symbol_id']
            h = hist[sid]
            if len(h['ts']) < 252:
                continue
            if bar['close'] < 5.0:
                continue
            # Avg dollar volume over T-60..T-1 (last 60 bars excluding today)
            if len(h['dollar_vol']) < 61:
                continue
            avg_dollar_vol = sum(h['dollar_vol'][-61:-1]) / 60.0
            if avg_dollar_vol < 5_000_000:
                continue
            # No missing bars in T-60..T (we have 61 bars if len >= 61)
            universe_symbols.append(sid)

        if len(universe_symbols) < 30:
            continue

        opportunities += len(universe_symbols)

        # Compute time-series features for universe symbols
        features = {}
        for sid in universe_symbols:
            h = hist[sid]
            # 5-session close-to-close return through T (T-4 to T)
            ret5 = h['close'][-1] / h['close'][-5] - 1.0
            # 20-session realized volatility (std of daily returns over last 20 days)
            rets = []
            for i in range(-20, 0):
                rets.append(h['close'][i] / h['close'][i-1] - 1.0)
            mean_ret = sum(rets) / 20.0
            var = sum((r - mean_ret) ** 2 for r in rets) / 20.0
            vol20 = sqrt(var)
            # Volume quintile: today's volume vs T-60..T-1
            vol_today = h['volume'][-1]
            past_vols = list(h['volume'][-61:-1])
            past_vols.sort()
            idx = int(0.8 * len(past_vols))
            vol_thresh = past_vols[idx] if past_vols else 0
            vol_quintile = vol_today >= vol_thresh
            # 1-day return
            ret1 = h['close'][-1] / h['close'][-2] - 1.0
            # High >= 2% above close
            high_cond = h['high'][-1] >= 1.02 * h['close'][-1]
            # Close <= midpoint of high-low
            mid = (h['high'][-1] + h['low'][-1]) / 2.0
            close_cond = h['close'][-1] <= mid
            # Return in [-0.5%, 0.5%]
            ret_cond = -0.005 <= ret1 <= 0.005

            features[sid] = {
                'ret5': ret5,
                'vol20': vol20,
                'vol_quintile': vol_quintile,
                'ret1': ret1,
                'high_cond': high_cond,
                'close_cond': close_cond,
                'ret_cond': ret_cond,
                'ts': ts
            }

        # Cross-sectional rankings
        ret5_vals = [(sid, f['ret5']) for sid, f in features.items()]
        ret5_vals.sort(key=lambda x: x[1], reverse=True)
        n = len(ret5_vals)
        top_decile_idx = max(1, int(0.1 * n))
        top_ret5_set = set(sid for sid, _ in ret5_vals[:top_decile_idx])

        vol20_vals = [(sid, f['vol20']) for sid, f in features.items()]
        vol20_vals.sort(key=lambda x: x[1], reverse=True)
        top_vol_decile_idx = max(1, int(0.1 * n))
        top_vol20_set = set(sid for sid, _ in vol20_vals[:top_vol_decile_idx])

        # Check entry and abstain conditions
        for sid, f in features.items():
            # Abstain: 20-session vol in top cross-sectional decile
            if sid in top_vol20_set:
                continue
            # Abstain: call issued for same symbol in prior 20 trading days
            if symbol_call_days[sid] and (day_idx - symbol_call_days[sid][-1]) < 20:
                continue
            # Entry conditions
            if not f['ret_cond']:
                continue
            if not f['high_cond']:
                continue
            if not f['close_cond']:
                continue
            if sid not in top_ret5_set:
                continue
            if not f['vol_quintile']:
                continue

            # All conditions met - issue DOWN call
            issued_calls.append((sid, ts, day_idx))
            symbol_call_days[sid].append(day_idx)
            # Keep only last 20
            while len(symbol_call_days[sid]) > 1 and (day_idx - symbol_call_days[sid][0]) >= 20:
                symbol_call_days[sid].popleft()

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # Fetch labels from prediction_outcomes for issued calls
    # Horizon = 20 (trading days), ts matches decision ts
    call_keys = [(sid, ts) for sid, ts, _ in issued_calls]
    placeholders = ','.join('(?,?)' for _ in call_keys)
    params = [item for pair in call_keys for item in pair]
    cur.execute(f"""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 20 AND (symbol_id, ts) IN ({placeholders})
    """, params)
    labels = {(row['symbol_id'], row['ts']): row['up'] for row in cur.fetchall()}

    # Attach labels to calls
    labeled_calls = []
    for sid, ts, day_idx in issued_calls:
        key = (sid, ts)
        if key in labels:
            labeled_calls.append((sid, ts, day_idx, labels[key]))

    if not labeled_calls:
        print("INSUFFICIENT=1")
        return 0

    # Determine sealed era: most recent 20% of trading days that had opportunities
    # We need the set of days where at least one opportunity existed
    opportunity_days = set()
    # Re-scan to find days with opportunities (or track during main loop)
    # Simpler: use the day_idx of issued calls and all days processed
    # But sealed era is "most recent 20% of the sample" - sample = opportunities (symbol-days)
    # Hold out by time: most recent 20% of trading days in the data
    total_days = len(trading_days)
    sealed_start_idx = int(0.8 * total_days)
    sealed_days = set(trading_days[sealed_start_idx:])

    # Split calls
    main_calls = []
    sealed_calls = []
    for sid, ts, day_idx, up in labeled_calls:
        if ts in sealed_days:
            sealed_calls.append((sid, ts, day_idx, up))
        else:
            main_calls.append((sid, ts, day_idx, up))

    # Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(1 for _, _, _, up in calls if up == 0)  # DOWN call correct when up=0
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate of predicted class (DOWN) within issued subset
        distinct_days = len(set(ts for _, ts, _, _ in calls))
        # Design effect: intra-cluster correlation
        # Group by day
        day_groups = defaultdict(list)
        for _, ts, _, up in calls:
            day_groups[ts].append(1 if up == 0 else 0)
        k_vals = [len(v) for v in day_groups.values()]
        if len(k_vals) <= 1:
            deff = 1.01
        else:
            n_total = sum(k_vals)
            n_bar = n_total / len(k_vals)
            p = hits / n_total
            # Between-cluster variance
            p_d = [sum(v)/len(v) for v in day_groups.values()]
            msb = sum(k * (pd - p) ** 2 for k, pd in zip(k_vals, p_d)) / (len(k_vals) - 1)
            # Within-cluster variance
            msw = sum(k * pd * (1 - pd) for k, pd in zip(k_vals, p_d)) / (n_total - len(k_vals))
            if msb + msw > 0:
                rho = (msb - msw) / (msb + (n_bar - 1) * msw) if (msb + (n_bar - 1) * msw) != 0 else 0
                rho = max(0, min(rho, 1))
            else:
                rho = 0
            deff = 1 + (n_bar - 1) * rho
            if deff <= 1:
                deff = 1.01
        effective_n = issued / deff
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_metrics(main_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    # Overall metrics (for output)
    all_calls = main_calls + sealed_calls
    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(all_calls)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
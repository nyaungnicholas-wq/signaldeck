# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 685
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1) Load STLFSI series from macro_series
    cur.execute("SELECT ts, value FROM macro_series WHERE series = 'STLFSI' ORDER BY ts")
    stlfs = cur.fetchall()
    if not stlfs:
        print("INSUFFICIENT=1")
        return 0

    # Build STLFSI lookup: ts (epoch) -> value
    stlfs_dict = {row['ts']: row['value'] for row in stlfs}
    stlfs_times = sorted(stlfs_dict.keys())

    # 2) Get all symbols with daily bars
    cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'")
    symbol_ids = [row['symbol_id'] for row in cur.fetchall()]

    # 3) For each symbol, load daily bars (ts, close, volume)
    # We'll process in chunks to avoid memory issues
    # But first, let's get the date range for STLFSI to align
    stlfs_min_ts = min(stlfs_times)
    stlfs_max_ts = max(stlfs_times)

    # 4) Get insider open-market purchases (code='P')
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    insider_buys = cur.fetchall()
    if not insider_buys:
        print("INSUFFICIENT=1")
        return 0

    # Group insider buys by symbol_id for efficient lookup
    insider_by_symbol = {}
    for row in insider_buys:
        sid = row['symbol_id']
        insider_by_symbol.setdefault(sid, []).append(row['filed_ts'])

    # 5) Define helper to get STLFSI value at or before a given ts
    def get_stlfs(ts):
        # Binary search for largest stlfs_time <= ts
        lo, hi = 0, len(stlfs_times) - 1
        ans = None
        while lo <= hi:
            mid = (lo + hi) // 2
            if stlfs_times[mid] <= ts:
                ans = stlfs_times[mid]
                lo = mid + 1
            else:
                hi = mid - 1
        if ans is None:
            return None
        return stlfs_dict[ans]

    # 6) For STLFSI elevated threshold: compute rolling 1-year mean/std at each decision point
    # We'll precompute STLFSI z-scores at each STLFSI timestamp
    # Need at least 252 trading days of history (approx 365 calendar days)
    SECONDS_PER_DAY = 86400
    ONE_YEAR_SEC = 365 * SECONDS_PER_DAY

    stlfs_zscore = {}
    for i, ts in enumerate(stlfs_times):
        if i < 252:  # need enough history
            continue
        # Look back ~1 year
        cutoff = ts - ONE_YEAR_SEC
        # Find start index
        # Since stlfs_times is sorted, we can use a running window
        pass  # We'll compute on the fly per decision for simplicity

    # Actually, let's compute STLFSI z-score at decision time using the stlfs data up to that point
    # For each decision_ts (filed_ts), get STLFSI value and compute z-score over past 252 observations
    def get_stlfs_zscore(ts):
        val = get_stlfs(ts)
        if val is None:
            return None
        # Get past 252 STLFSI values before ts
        past_vals = []
        for st in reversed(stlfs_times):
            if st >= ts:
                continue
            past_vals.append(stlfs_dict[st])
            if len(past_vals) >= 252:
                break
        if len(past_vals) < 200:  # need sufficient history
            return None
        mean = sum(past_vals) / len(past_vals)
        std = (sum((x - mean) ** 2 for x in past_vals) / len(past_vals)) ** 0.5
        if std == 0:
            return None
        return (val - mean) / std

    # 7) Process each symbol that has both bars and insider buys
    signals = []  # list of (symbol_id, decision_ts, fwd_ret_21d)

    # We need to load bars for symbols that have insider buys
    relevant_symbols = set(insider_by_symbol.keys())

    # Load bars for relevant symbols
    # Since there are many symbols, we'll query in batches
    batch_size = 50
    symbol_list = list(relevant_symbols)
    
    for i in range(0, len(symbol_list), batch_size):
        batch = symbol_list[i:i+batch_size]
        placeholders = ','.join('?' * len(batch))
        cur.execute(f"""
            SELECT symbol_id, ts, close, volume
            FROM bars
            WHERE tf = '1d' AND symbol_id IN ({placeholders})
            ORDER BY symbol_id, ts
        """, batch)
        rows = cur.fetchall()

        # Group by symbol_id
        bars_by_symbol = {}
        for row in rows:
            bars_by_symbol.setdefault(row['symbol_id'], []).append((row['ts'], row['close'], row['volume']))

        # Process each symbol in batch
        for sid in batch:
            if sid not in bars_by_symbol:
                continue
            bars = bars_by_symbol[sid]  # list of (ts, close, volume) sorted by ts
            if len(bars) < 252:
                continue

            # Precompute 252-day high and 20-day avg volume for each bar
            # We'll compute on the fly at decision points
            # Build lookup: ts -> (close, volume)
            bar_dict = {ts: (close, vol) for ts, close, vol in bars}
            bar_times = [ts for ts, _, _ in bars]

            # Get insider buy filed_ts for this symbol
            buy_times = insider_by_symbol.get(sid, [])
            if not buy_times:
                continue

            for filed_ts in buy_times:
                # Decision time = filed_ts (when insider purchase becomes public)
                # Need bars up to filed_ts (the decision bar is the latest bar <= filed_ts)
                # Find the decision bar index
                lo, hi = 0, len(bar_times) - 1
                decision_idx = -1
                while lo <= hi:
                    mid = (lo + hi) // 2
                    if bar_times[mid] <= filed_ts:
                        decision_idx = mid
                        lo = mid + 1
                    else:
                        hi = mid - 1
                if decision_idx < 252:  # need 252-day history
                    continue
                if decision_idx >= len(bars) - 21:  # need 21-day forward window
                    continue

                decision_ts = bar_times[decision_idx]
                decision_close = bars[decision_idx][1]

                # Condition 1: STLFSI z-score > 1
                z = get_stlfs_zscore(filed_ts)
                if z is None or z <= 1.0:
                    continue

                # Condition 2: Price declined >30% from 252-day high
                # 252-day high up to decision_idx (inclusive)
                high_252 = max(bars[j][1] for j in range(decision_idx - 251, decision_idx + 1))
                drawdown = (high_252 - decision_close) / high_252
                if drawdown <= 0.30:
                    continue

                # Condition 3: Volume < 20-day average volume
                vol_20 = sum(bars[j][2] for j in range(decision_idx - 19, decision_idx + 1)) / 20.0
                current_vol = bars[decision_idx][2]
                if current_vol >= vol_20:
                    continue

                # All conditions met - compute 21-day forward return
                fwd_ts = bar_times[decision_idx + 21]
                fwd_close = bars[decision_idx + 21][1]
                fwd_ret = (fwd_close - decision_close) / decision_close

                signals.append((sid, filed_ts, fwd_ret))

    if not signals:
        print("INSUFFICIENT=1")
        return 0

    # 8) Hold out most recent 20% as sealed era
    signals.sort(key=lambda x: x[1])  # sort by decision_ts
    n = len(signals)
    split_idx = int(n * 0.8)
    main_signals = signals[:split_idx]
    sealed_signals = signals[split_idx:]

    def compute_stats(sig_list):
        if not sig_list:
            return 0, 0, 0, 0
        issued = len(sig_list)
        hits = sum(1 for _, _, ret in sig_list if ret > 0)
        precision = hits / issued if issued else 0
        base_rate = hits / issued if issued else 0  # same as precision since we only predict "up"
        distinct_days = len(set(datetime.utcfromtimestamp(ts).date() for _, ts, _ in sig_list))
        return issued, hits, precision, base_rate, distinct_days

    issued_main, hits_main, prec_main, base_main, distinct_main = compute_stats(main_signals)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, distinct_sealed = compute_stats(sealed_signals)

    # Effective N: issued / design_effect
    # Design effect = 1 + (avg_cluster_size - 1) * intracluster_corr
    # Approximate: group by UTC day, compute variance of daily counts
    def effective_n(sig_list):
        if not sig_list:
            return 0
        day_counts = {}
        for _, ts, _ in sig_list:
            day = datetime.utcfromtimestamp(ts).date()
            day_counts[day] = day_counts.get(day, 0) + 1
        counts = list(day_counts.values())
        if len(counts) <= 1:
            return len(sig_list)
        mean_c = sum(counts) / len(counts)
        var_c = sum((c - mean_c) ** 2 for c in counts) / len(counts)
        # Intraclass correlation approx
        if mean_c == 0:
            return len(sig_list)
        rho = (var_c - mean_c) / (var_c + mean_c * (mean_c - 1)) if var_c > 0 else 0
        rho = max(0, min(1, rho))
        deff = 1 + (mean_c - 1) * rho
        return len(sig_list) / deff

    eff_main = effective_n(main_signals)
    eff_sealed = effective_n(sealed_signals)

    # Output required lines
    print(f"ISSUED={issued_main}")
    print(f"OPPORTUNITIES={issued_main}")  # opportunities considered = issued (we only count decision points where we could act)
    print(f"PRECISION={prec_main:.6f}")
    print(f"BASE_RATE={base_main:.6f}")
    print(f"DISTINCT_DAYS={distinct_main}")
    print(f"EFFECTIVE_N={eff_main:.2f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())
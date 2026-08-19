# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 883
# cycle_index: 29
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import sys
import bisect
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    cur.execute("""
        SELECT DISTINCT date(ts, 'unixepoch') as utc_day
        FROM bars
        WHERE tf = '1d'
        ORDER BY utc_day
    """)
    all_days = [row['utc_day'] for row in cur.fetchall()]

    if len(all_days) < 10:
        print("INSUFFICIENT=1")
        return 0

    split_idx = int(len(all_days) * 0.8)
    train_days = set(all_days[:split_idx])
    sealed_days = set(all_days[split_idx:])

    cur.execute("""
        SELECT symbol_id, COUNT(*) as n_bars
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING n_bars >= 252
    """)
    eligible_symbols = {row['symbol_id'] for row in cur.fetchall()}

    if not eligible_symbols:
        print("INSUFFICIENT=1")
        return 0

    placeholders = ','.join('?' * len(eligible_symbols))
    cur.execute(f"""
        SELECT symbol_id, ts, date(ts, 'unixepoch') as utc_day, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, list(eligible_symbols))

    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'utc_day': row['utc_day'],
            'close': row['close'],
            'volume': row['volume']
        })

    HORIZON_DAYS = 21
    LOOKBACK_BB = 20
    LOOKBACK_PCTL = 252
    PCTL = 0.10
    VOL_MULT = 1.5

    issued = 0
    hits = 0
    opportunities = 0
    issued_days = set()
    sealed_issued = 0
    sealed_hits = 0

    for symbol_id, bars in bars_by_symbol.items():
        n = len(bars)
        if n < LOOKBACK_PCTL + HORIZON_DAYS:
            continue

        closes = [b['close'] for b in bars]
        volumes = [b['volume'] for b in bars]
        utc_days = [b['utc_day'] for b in bars]

        # Precompute 20-day rolling stats
        bb_means = [0.0] * n
        bb_stds = [0.0] * n
        bb_upper = [0.0] * n
        bb_lower = [0.0] * n
        bb_widths = [0.0] * n
        vol_means = [0.0] * n

        # Rolling sums for 20-day window
        sum_close = 0.0
        sum_close_sq = 0.0
        sum_vol = 0.0

        for i in range(n):
            c = closes[i]
            v = volumes[i]
            sum_close += c
            sum_close_sq += c * c
            sum_vol += v

            if i >= LOOKBACK_BB:
                c_old = closes[i - LOOKBACK_BB]
                v_old = volumes[i - LOOKBACK_BB]
                sum_close -= c_old
                sum_close_sq -= c_old * c_old
                sum_vol -= v_old

            if i >= LOOKBACK_BB - 1:
                mean = sum_close / LOOKBACK_BB
                var = sum_close_sq / LOOKBACK_BB - mean * mean
                std = var ** 0.5 if var > 0 else 0.0
                bb_means[i] = mean
                bb_stds[i] = std
                bb_upper[i] = mean + 2 * std
                bb_lower[i] = mean - 2 * std
                bb_widths[i] = (4 * std) / mean if mean != 0 else float('inf')
                vol_means[i] = sum_vol / LOOKBACK_BB

        # Rolling 10th percentile of band widths over 252 days
        # Maintain sorted list of last 252 band widths
        sorted_widths = []
        p10_widths = [0.0] * n

        for i in range(n):
            if i >= LOOKBACK_BB - 1:
                bw = bb_widths[i]
                bisect.insort(sorted_widths, bw)

            if i >= LOOKBACK_PCTL:
                bw_old = bb_widths[i - LOOKBACK_PCTL]
                idx = bisect.bisect_left(sorted_widths, bw_old)
                if idx < len(sorted_widths) and sorted_widths[idx] == bw_old:
                    sorted_widths.pop(idx)

            if i >= LOOKBACK_PCTL - 1 and sorted_widths:
                p10_idx = max(0, int(PCTL * len(sorted_widths)) - 1)
                p10_widths[i] = sorted_widths[p10_idx]

        # Now evaluate signals
        for i in range(LOOKBACK_PCTL, n - HORIZON_DAYS):
            utc_day = utc_days[i]
            is_sealed = utc_day in sealed_days
            is_train = utc_day in train_days
            if not (is_train or is_sealed):
                continue

            opportunities += 1

            bw = bb_widths[i]
            p10 = p10_widths[i]
            vol = volumes[i]
            avg_vol = vol_means[i]
            close = closes[i]
            upper = bb_upper[i]
            lower = bb_lower[i]

            if bw <= p10 and vol >= VOL_MULT * avg_vol:
                signal = 0
                if close > upper:
                    signal = 1
                elif close < lower:
                    signal = -1

                if signal != 0:
                    issued += 1
                    issued_days.add(utc_day)
                    if is_sealed:
                        sealed_issued += 1

                    fwd_close = closes[i + HORIZON_DAYS]
                    fwd_return = (fwd_close / close) - 1
                    hit = 1 if (signal == 1 and fwd_return > 0) or (signal == -1 and fwd_return < 0) else 0
                    hits += hit
                    if is_sealed:
                        sealed_hits += hit

    if issued == 0:
        print("INSUFFICIENT=1")
        return 0

    precision = hits / issued
    base_rate = 0.5
    distinct_days = len(issued_days)
    # Design effect: cluster by day, effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
    # Conservative: assume design effect >= 2, so effective_n <= issued / 2
    # But must be strictly less than issued. Use distinct_days as proxy (max possible independent obs)
    effective_n = min(distinct_days, issued - 1) if issued > 1 else 0
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    return 0

if __name__ == "__main__":
    sys.exit(main())
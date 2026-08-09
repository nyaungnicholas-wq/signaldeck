# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 409
# cycle_index: 77
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict
import math

def utc_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load all symbols with daily bars
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id
        WHERE b.tf = '1d'
    """)
    symbols = [(row['id'], row['symbol']) for row in cur.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return

    # Load all daily bars for these symbols
    symbol_ids = [s[0] for s in symbols]
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'open': row['open'],
            'high': row['high'],
            'low': row['low'],
            'close': row['close'],
            'volume': row['volume'],
            'date': utc_date(row['ts'])
        })

    # Load prediction outcomes for horizon=20 (20 trading days)
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 20
    """)
    outcomes = {}
    for row in cur.fetchall():
        outcomes[(row['symbol_id'], row['ts'])] = row['up']

    all_calls = []  # (symbol_id, ts, date, hit)
    opportunities = 0

    for symbol_id, bars in bars_by_symbol.items():
        n = len(bars)
        if n < 520:  # need 500 history + 20 for indicators
            continue

        # Precompute arrays for speed
        closes = [b['close'] for b in bars]
        highs = [b['high'] for b in bars]
        lows = [b['low'] for b in bars]
        volumes = [b['volume'] for b in bars]
        dates = [b['date'] for b in bars]
        tss = [b['ts'] for b in bars]

        # 200-day SMA
        sma200 = [None] * n
        sum200 = sum(closes[:200])
        sma200[199] = sum200 / 200
        for i in range(200, n):
            sum200 += closes[i] - closes[i-200]
            sma200[i] = sum200 / 200

        # 20-day average dollar volume
        avg_dollar_vol20 = [None] * n
        sum_dv20 = sum(closes[i] * volumes[i] for i in range(20))
        avg_dollar_vol20[19] = sum_dv20 / 20
        for i in range(20, n):
            sum_dv20 += closes[i] * volumes[i] - closes[i-20] * volumes[i-20]
            avg_dollar_vol20[i] = sum_dv20 / 20

        # 20-day return
        ret20 = [None] * n
        for i in range(20, n):
            if closes[i-20] > 0:
                ret20[i] = closes[i] / closes[i-20] - 1

        # 20-day average volume
        avg_vol20 = [None] * n
        sum_vol20 = sum(volumes[:20])
        avg_vol20[19] = sum_vol20 / 20
        for i in range(20, n):
            sum_vol20 += volumes[i] - volumes[i-20]
            avg_vol20[i] = sum_vol20 / 20

        # Scan for entry at day t (index i)
        # Need at least 10 days before t for inside-day coil (t-9..t-1)
        # So i >= 10, and also need 500 history before t => i >= 500
        for i in range(500, n):
            opportunities += 1

            # Universe filters at t
            if closes[i] < 2.0:
                continue
            if avg_dollar_vol20[i] is None or avg_dollar_vol20[i] < 5_000_000:
                continue

            # Abstain: price below 200-day SMA
            if sma200[i] is not None and closes[i] < sma200[i]:
                continue

            # Abstain: prior 20-day return > +20%
            if ret20[i] is not None and ret20[i] > 0.20:
                continue

            # Check 10-day inside-day coil: days t-9 .. t-1 (indices i-9 .. i-1)
            # Each day must be inside prior day's range
            coil_ok = True
            highest_high = -1.0
            lowest_low = float('inf')
            for j in range(i-9, i):
                # Day j inside day j-1
                if not (highs[j] <= highs[j-1] and lows[j] >= lows[j-1]):
                    coil_ok = False
                    break
                # Abstain: any of the 10 inside days gapped below prior day's low
                # Gap down: open[j] < low[j-1] (but we don't have open for j-1? Wait, we have open for each day)
                # "gapped below the prior day's low" means day j's open < day j-1's low
                if bars[j]['open'] < lows[j-1]:
                    coil_ok = False
                    break
                if highs[j] > highest_high:
                    highest_high = highs[j]
                if lows[j] < lowest_low:
                    lowest_low = lows[j]
            
            if not coil_ok:
                continue

            # Day t (index i) conditions:
            # 1. Closes above highest high of those 10 days
            if closes[i] <= highest_high:
                continue

            # 2. Day t's range at least 2x day t-1's range
            range_t = highs[i] - lows[i]
            range_t1 = highs[i-1] - lows[i-1]
            if range_t < 2 * range_t1:
                continue

            # 3. Day t volume at least 1.5x its 20-day average
            if avg_vol20[i] is None or volumes[i] < 1.5 * avg_vol20[i]:
                continue

            # 4. Close in top quintile of day t's range
            # Top quintile: close >= low + 0.8 * range
            if closes[i] < lows[i] + 0.8 * range_t:
                continue

            # All conditions met - issue UP call at close of t
            ts = tss[i]
            hit = outcomes.get((symbol_id, ts), None)
            if hit is None:
                # No label available
                continue

            all_calls.append({
                'symbol_id': symbol_id,
                'ts': ts,
                'date': dates[i],
                'hit': hit
            })

    if not all_calls:
        print("INSUFFICIENT=1")
        return

    # Sort by timestamp
    all_calls.sort(key=lambda x: x['ts'])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(all_calls) * 0.8)
    main_calls = all_calls[:split_idx]
    sealed_calls = all_calls[split_idx:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(c['hit'] for c in calls)
        precision = hits / issued
        base_rate = precision  # base rate within issued subset = hit rate
        distinct_days = len(set(c['date'] for c in calls))
        
        # Design effect for EFFECTIVE_N
        # Group by date
        day_groups = defaultdict(list)
        for c in calls:
            day_groups[c['date']].append(c['hit'])
        
        k = len(day_groups)
        N = issued
        if k <= 1:
            deff = 1.01  # minimal clustering
        else:
            # Estimate ICC using ANOVA method for binary data
            p = hits / N
            # Between-group variance
            between_sum = 0.0
            within_sum = 0.0
            for day, day_hits in day_groups.items():
                n_d = len(day_hits)
                p_d = sum(day_hits) / n_d
                between_sum += n_d * (p_d - p) ** 2
                within_sum += n_d * p_d * (1 - p_d)
            
            msb = between_sum / (k - 1) if k > 1 else 0
            msw = within_sum / (N - k) if N > k else 0
            
            # Average cluster size (adjusted)
            sum_n2 = sum(len(v)**2 for v in day_groups.values())
            n0 = (N - sum_n2 / N) / (k - 1) if k > 1 else 1
            
            if msb > 0 and msw >= 0:
                icc = (msb - msw) / (msb + (n0 - 1) * msw) if (msb + (n0 - 1) * msw) > 0 else 0
                icc = max(0, min(icc, 1))
            else:
                icc = 0
            
            deff = 1 + (n0 - 1) * icc
            deff = max(deff, 1.01)  # ensure > 1
        
        effective_n = N / deff
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_main, hits_main, precision_main, base_rate_main, distinct_days_main, effective_n_main = compute_metrics(main_calls)
    issued_sealed, hits_sealed, precision_sealed, base_rate_sealed, distinct_days_sealed, effective_n_sealed = compute_metrics(sealed_calls)

    # Overall metrics (for reporting)
    issued_total = len(all_calls)
    hits_total = sum(c['hit'] for c in all_calls)
    precision_total = hits_total / issued_total if issued_total else 0
    base_rate_total = precision_total
    distinct_days_total = len(set(c['date'] for c in all_calls))
    
    # Design effect for total
    day_groups_total = defaultdict(list)
    for c in all_calls:
        day_groups_total[c['date']].append(c['hit'])
    k_total = len(day_groups_total)
    N_total = issued_total
    if k_total <= 1:
        deff_total = 1.01
    else:
        p_total = hits_total / N_total
        between_sum = 0.0
        within_sum = 0.0
        for day, day_hits in day_groups_total.items():
            n_d = len(day_hits)
            p_d = sum(day_hits) / n_d
            between_sum += n_d * (p_d - p_total) ** 2
            within_sum += n_d * p_d * (1 - p_d)
        msb = between_sum / (k_total - 1)
        msw = within_sum / (N_total - k_total) if N_total > k_total else 0
        sum_n2 = sum(len(v)**2 for v in day_groups_total.values())
        n0 = (N_total - sum_n2 / N_total) / (k_total - 1)
        if msb > 0 and msw >= 0:
            icc = (msb - msw) / (msb + (n0 - 1) * msw) if (msb + (n0 - 1) * msw) > 0 else 0
            icc = max(0, min(icc, 1))
        else:
            icc = 0
        deff_total = 1 + (n0 - 1) * icc
        deff_total = max(deff_total, 1.01)
    effective_n_total = N_total / deff_total

    # Print required lines
    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_total:.6f}")
    print(f"BASE_RATE={base_rate_total:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_total}")
    print(f"EFFECTIVE_N={effective_n_total:.2f}")
    print(f"SEALED_PRECISION={precision_sealed:.6f}")

if __name__ == "__main__":
    main()
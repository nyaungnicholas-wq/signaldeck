# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 757
# cycle_index: 27
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Universe: symbols with >=252 daily bars, insider history, avg daily dollar vol > $1M
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        JOIN (
            SELECT symbol_id, COUNT(*) as n_bars, AVG(close * volume) as avg_dollar_vol
            FROM bars
            WHERE tf = '1d'
            GROUP BY symbol_id
            HAVING n_bars >= 252 AND avg_dollar_vol > 1000000
        ) b ON s.id = b.symbol_id
        WHERE EXISTS (SELECT 1 FROM insider_trades WHERE symbol_id = s.id)
    """)
    universe = [(row['id'], row['symbol']) for row in cur.fetchall()]
    if not universe:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = [u[0] for u in universe]

    # 2. Load all daily bars for universe symbols
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
            'volume': row['volume']
        })

    # 3. Load all insider trades for universe symbols (code='P' for purchases)
    cur.execute(f"""
        SELECT accession, symbol_id, insider, code, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code = 'P'
        ORDER BY symbol_id, filed_ts
    """, symbol_ids)
    insider_by_symbol = defaultdict(list)
    all_insider_purchases = []  # for prior purchase check
    for row in cur.fetchall():
        insider_by_symbol[row['symbol_id']].append({
            'accession': row['accession'],
            'insider': row['insider'],
            'filed_ts': row['filed_ts']
        })
        all_insider_purchases.append({
            'insider': row['insider'],
            'filed_ts': row['filed_ts']
        })

    # Sort all insider purchases by filed_ts for prior check
    all_insider_purchases.sort(key=lambda x: x['filed_ts'])

    # 4. For each symbol, compute rolling 20-day avg volume, volatility, detect hammers
    hammer_days = []  # list of (symbol_id, hammer_idx, hammer_ts, hammer_date_str)
    vol_20_list = []  # for top decile calculation: (symbol_id, hammer_idx, vol_20)

    for sym_id, bars in bars_by_symbol.items():
        n = len(bars)
        if n < 21:
            continue

        # Precompute daily returns for volatility
        returns = [0.0] * n
        for i in range(1, n):
            prev_close = bars[i-1]['close']
            if prev_close > 0:
                returns[i] = (bars[i]['close'] - prev_close) / prev_close

        # Rolling 20-day volume average and volatility
        vol_sum = 0.0
        ret_sq_sum = 0.0
        for i in range(n):
            vol_sum += bars[i]['volume']
            ret_sq_sum += returns[i] * returns[i]
            if i >= 20:
                vol_sum -= bars[i-20]['volume']
                ret_sq_sum -= returns[i-20] * returns[i-20]

            if i >= 20:
                avg_vol_20 = vol_sum / 20.0
                vol_20 = math.sqrt(ret_sq_sum / 20.0) if ret_sq_sum > 0 else 0.0

                # Hammer detection on day i
                o, h, l, c, v = bars[i]['open'], bars[i]['high'], bars[i]['low'], bars[i]['close'], bars[i]['volume']
                real_body = abs(c - o)
                lower_shadow = min(o, c) - l
                daily_range = h - l
                if daily_range > 0 and real_body > 0:
                    close_top_25 = (c - l) / daily_range > 0.75
                    lower_shadow_2x = lower_shadow > 2 * real_body
                    vol_spike = v > 1.5 * avg_vol_20
                    if close_top_25 and lower_shadow_2x and vol_spike:
                        hammer_days.append((sym_id, i, bars[i]['ts']))
                        vol_20_list.append((sym_id, i, vol_20))

    if not hammer_days:
        print("INSUFFICIENT=1")
        return 0

    # 5. Compute top decile threshold for 20-day volatility across all hammer events
    vol_20_list.sort(key=lambda x: x[2])
    p90_idx = int(len(vol_20_list) * 0.9)
    vol_top_decile_threshold = vol_20_list[p90_idx][2] if vol_20_list else float('inf')

    # 6. For each hammer, check insider purchases within 3 trading days after
    # Build mapping from symbol_id to list of (bar_idx, ts) for quick lookup
    hammer_by_symbol = defaultdict(list)
    for sym_id, idx, ts in hammer_days:
        hammer_by_symbol[sym_id].append((idx, ts))

    # For prior hammer check: for each symbol, get sorted hammer indices
    hammer_indices_by_symbol = {sym: sorted([idx for idx, _ in lst]) for sym, lst in hammer_by_symbol.items()}

    # For prior insider purchase check: build set of insiders with prior purchases before each filed_ts
    # We'll check per event by scanning all_insider_purchases

    issued_calls = []  # (decision_ts, symbol_id, hammer_idx, insider, label)

    for sym_id, hammer_list in hammer_by_symbol.items():
        bars = bars_by_symbol[sym_id]
        insider_trades = insider_by_symbol.get(sym_id, [])
        hammer_indices = hammer_indices_by_symbol[sym_id]

        for hammer_idx, hammer_ts in hammer_list:
            # Abstention: prior hammer within 5 trading days
            prior_hammer = False
            for prev_idx in hammer_indices:
                if prev_idx >= hammer_idx:
                    break
                if hammer_idx - prev_idx <= 5:
                    prior_hammer = True
                    break
            if prior_hammer:
                continue

            # Check insider purchases filed within 3 trading days after hammer
            # Trading days = subsequent bar indices
            for trade in insider_trades:
                filed_ts = trade['filed_ts']
                insider = trade['insider']

                # Find filing day index (first bar with ts >= filed_ts date)
                # filed_ts is unix epoch; bars.ts is unix epoch for the day
                # We need filing date to be within 3 trading days after hammer_idx
                # i.e., filing bar index in [hammer_idx+1, hammer_idx+3]
                filing_idx = None
                for j in range(hammer_idx + 1, min(hammer_idx + 4, len(bars))):
                    if bars[j]['ts'] >= filed_ts:
                        filing_idx = j
                        break
                if filing_idx is None:
                    continue

                # Abstention: insider has zero prior open-market purchases
                has_prior = False
                for p in all_insider_purchases:
                    if p['insider'] == insider and p['filed_ts'] < filed_ts:
                        has_prior = True
                        break
                if not has_prior:
                    continue

                # Abstention: 20-day volatility at hammer in top decile
                # Find vol_20 for this hammer
                hammer_vol = None
                for v_sym, v_idx, v_val in vol_20_list:
                    if v_sym == sym_id and v_idx == hammer_idx:
                        hammer_vol = v_val
                        break
                if hammer_vol is not None and hammer_vol > vol_top_decile_threshold:
                    continue

                # All checks passed - issue call
                decision_ts = bars[filing_idx]['ts']
                # Compute 21-day forward return from decision close
                exit_idx = filing_idx + 21
                if exit_idx < len(bars):
                    entry_px = bars[filing_idx]['close']
                    exit_px = bars[exit_idx]['close']
                    if entry_px > 0:
                        fwd_return = (exit_px - entry_px) / entry_px
                        label = 1 if fwd_return > 0 else 0
                        issued_calls.append({
                            'decision_ts': decision_ts,
                            'symbol_id': sym_id,
                            'hammer_idx': hammer_idx,
                            'insider': insider,
                            'label': label
                        })

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # 7. Sort by decision_ts
    issued_calls.sort(key=lambda x: x['decision_ts'])

    # 8. Hold out most recent 20% as sealed era
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    train_calls = issued_calls[:-n_sealed]
    sealed_calls = issued_calls[-n_sealed:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(c['label'] for c in calls)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # predicted class is "up" for all calls
        # Distinct UTC days among issued calls
        distinct_days = len(set(c['decision_ts'] // 86400 for c in calls))
        # Design effect: simple clustering by day
        # Group by day, compute intra-cluster correlation
        day_groups = defaultdict(list)
        for c in calls:
            day = c['decision_ts'] // 86400
            day_groups[day].append(c['label'])
        # Estimate ICC (intraclass correlation) for binary data
        # Using ANOVA estimator: ICC = (MSB - MSW) / (MSB + (k-1)*MSW)
        # where k = avg cluster size
        k_values = [len(v) for v in day_groups.values()]
        if len(k_values) > 1 and sum(k_values) > len(k_values):
            overall_mean = hits / issued
            # Between-group sum of squares
            ssb = sum(len(v) * ((sum(v)/len(v)) - overall_mean)**2 for v in day_groups.values())
            # Within-group sum of squares
            ssw = sum(sum((x - sum(v)/len(v))**2 for x in v) for v in day_groups.values())
            df_b = len(day_groups) - 1
            df_w = issued - len(day_groups)
            if df_b > 0 and df_w > 0:
                msb = ssb / df_b
                msw = ssw / df_w
                k_avg = issued / len(day_groups)
                icc = (msb - msw) / (msb + (k_avg - 1) * msw) if (msb + (k_avg - 1) * msw) > 0 else 0
                icc = max(0, min(1, icc))
                design_effect = 1 + (k_avg - 1) * icc
            else:
                design_effect = 1.0
        else:
            design_effect = 1.0
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days, train_effective_n = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    # Overall metrics (on full set for reporting)
    total_issued, total_hits, total_precision, total_base_rate, total_distinct_days, total_effective_n = compute_metrics(issued_calls)

    # Opportunities = total decision points considered (hammer days that had insider trades within 3 days before abstentions)
    # We need to count all hammer days that had at least one insider purchase within 3 days (before abstentions)
    opportunities = 0
    for sym_id, hammer_list in hammer_by_symbol.items():
        insider_trades = insider_by_symbol.get(sym_id, [])
        for hammer_idx, hammer_ts in hammer_list:
            for trade in insider_trades:
                filed_ts = trade['filed_ts']
                filing_idx = None
                bars = bars_by_symbol[sym_id]
                for j in range(hammer_idx + 1, min(hammer_idx + 4, len(bars))):
                    if bars[j]['ts'] >= filed_ts:
                        filing_idx = j
                        break
                if filing_idx is not None:
                    opportunities += 1
                    break  # count once per hammer

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base_rate:.6f}")
    print(f"DISTINCT_DAYS={total_distinct_days}")
    print(f"EFFECTIVE_N={total_effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())
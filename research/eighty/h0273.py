import sqlite3
import math
import statistics
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load all 1d bars
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    rows = cur.fetchall()

    # Group by symbol
    symbol_bars = defaultdict(list)
    for r in rows:
        symbol_bars[r['symbol_id']].append((r['ts'], r['close'], r['volume']))

    # Load labels for horizon=10
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 10
    """)
    label_rows = cur.fetchall()
    labels = {(r['symbol_id'], r['ts']): r['up'] for r in label_rows}

    # For each symbol, compute metrics
    all_opportunities = []  # (ts, symbol_id, bar_idx, close, adv, ret60, vol60, vol20, ret60_hist, vol60_hist, vol20_hist)
    symbol_data = {}

    for sym_id, bars in symbol_bars.items():
        n = len(bars)
        if n < 253:  # need 252 prior + current
            continue

        ts_list = [b[0] for b in bars]
        close_list = [b[1] for b in bars]
        vol_list = [b[2] for b in bars]

        # Daily returns
        daily_ret = [0.0] * n
        for i in range(1, n):
            daily_ret[i] = close_list[i] / close_list[i-1] - 1.0

        # 60-day return
        ret60 = [None] * n
        for i in range(60, n):
            ret60[i] = close_list[i] / close_list[i-60] - 1.0

        # 60-day realized vol (std of 60 daily returns ending at i)
        vol60 = [None] * n
        for i in range(59, n):
            window = daily_ret[i-59:i+1]
            vol60[i] = statistics.stdev(window) if len(window) > 1 else 0.0

        # 20-day realized vol
        vol20 = [None] * n
        for i in range(19, n):
            window = daily_ret[i-19:i+1]
            vol20[i] = statistics.stdev(window) if len(window) > 1 else 0.0

        # ADV over T-60..T-1 (60 days)
        adv60 = [None] * n
        for i in range(60, n):
            dollar_vols = [close_list[j] * vol_list[j] for j in range(i-60, i)]
            adv60[i] = sum(dollar_vols) / 60.0

        # Store for later percentile calculations
        symbol_data[sym_id] = {
            'ts': ts_list,
            'close': close_list,
            'ret60': ret60,
            'vol60': vol60,
            'vol20': vol20,
            'adv60': adv60,
            'n': n
        }

        # Collect opportunities (i >= 252)
        for i in range(252, n):
            if ret60[i] is None or vol60[i] is None or vol20[i] is None or adv60[i] is None:
                continue
            all_opportunities.append((ts_list[i], sym_id, i))

    if not all_opportunities:
        print("INSUFFICIENT=1")
        return

    # Sort opportunities by timestamp
    all_opportunities.sort(key=lambda x: x[0])

    # Split into in-sample (first 80%) and sealed (last 20%)
    split_idx = int(len(all_opportunities) * 0.8)
    in_sample_ops = all_opportunities[:split_idx]
    sealed_ops = all_opportunities[split_idx:]

    def process_era(ops, era_name):
        if not ops:
            return {
                'issued': 0, 'opportunities': 0, 'hits': 0,
                'issued_days': set(), 'preliminary_calls': []
            }

        # For each opportunity, compute rolling percentiles using prior 252 days
        preliminary = []  # (ts, sym_id, bar_idx, vol20, close, label)
        opportunities_count = 0

        for ts, sym_id, idx in ops:
            opportunities_count += 1
            data = symbol_data[sym_id]
            close = data['close'][idx]
            adv = data['adv60'][idx]
            ret60_val = data['ret60'][idx]
            vol60_val = data['vol60'][idx]
            vol20_val = data['vol20'][idx]

            # Basic universe filters (abstain conditions)
            if close < 5.0 or adv < 5_000_000.0:
                continue

            # Rolling history: prior 252 days (indices idx-252 to idx-1)
            hist_start = idx - 252
            hist_end = idx  # exclusive
            ret60_hist = [data['ret60'][j] for j in range(hist_start, hist_end) if data['ret60'][j] is not None]
            vol60_hist = [data['vol60'][j] for j in range(hist_start, hist_end) if data['vol60'][j] is not None]
            vol20_hist = [data['vol20'][j] for j in range(hist_start, hist_end) if data['vol20'][j] is not None]

            if len(ret60_hist) < 252 or len(vol60_hist) < 252 or len(vol20_hist) < 252:
                continue

            # Percentiles
            ret60_pct = sum(1 for v in ret60_hist if v <= ret60_val) / len(ret60_hist)
            vol60_pct = sum(1 for v in vol60_hist if v <= vol60_val) / len(vol60_hist)
            vol20_median = statistics.median(vol20_hist)

            # Entry conditions
            if not (ret60_pct >= 0.75 and vol60_pct <= 0.40 and vol20_val < vol20_median):
                continue

            # Get label
            label = labels.get((sym_id, ts))
            if label is None:
                continue

            preliminary.append((ts, sym_id, idx, vol20_val, close, label))

        # Cross-sectional decile filter: group by ts
        by_ts = defaultdict(list)
        for p in preliminary:
            by_ts[p[0]].append(p)

        after_cross = []
        for ts, group in by_ts.items():
            vol20_vals = [g[3] for g in group]
            if len(vol20_vals) >= 2:
                p90 = sorted(vol20_vals)[int(0.9 * (len(vol20_vals) - 1))]
            else:
                p90 = float('inf')
            for g in group:
                if g[3] <= p90:
                    after_cross.append(g)

        # Cooldown filter: no call for same symbol in prior 20 trading days
        last_call_idx = {}
        after_cooldown = []
        for ts, sym_id, idx, vol20_val, close, label in after_cross:
            last = last_call_idx.get(sym_id, -1000)
            if idx - last > 20:
                after_cooldown.append((ts, sym_id, idx, vol20_val, close, label))
                last_call_idx[sym_id] = idx

        # Fewer than 30 independent observations remain filter
        # Process in chronological order, abstain if remaining ops in this era < 30
        # But "independent observations" = issued calls. We don't know future issued.
        # Interpret as: if remaining opportunities in era < 30, abstain.
        # However, we already filtered opportunities. Let's use remaining preliminary calls?
        # The rule says "fewer than 30 independent observations remain" as abstention condition.
        # Since we process chronologically, we can count how many preliminary calls remain after current.
        # But preliminary calls are not yet filtered by cross/cooldown.
        # Simpler: after all filters except this, if total issued in era < 30, but that's circular.
        # I'll apply: when processing after_cooldown in chronological order, if the number of
        # after_cooldown items remaining (including current) < 30, abstain.
        after_cooldown.sort(key=lambda x: x[0])
        issued_calls = []
        n_remaining = len(after_cooldown)
        for i, call in enumerate(after_cooldown):
            remaining = n_remaining - i
            if remaining < 30:
                break
            issued_calls.append(call)

        # Compute metrics
        issued = len(issued_calls)
        hits = sum(1 for c in issued_calls if c[5] == 1)
        issued_days = set(c[0] for c in issued_calls)

        return {
            'issued': issued,
            'opportunities': opportunities_count,
            'hits': hits,
            'issued_days': issued_days,
            'preliminary_calls': issued_calls
        }

    in_sample = process_era(in_sample_ops, 'in_sample')
    sealed = process_era(sealed_ops, 'sealed')

    # Combine for overall metrics (but report sealed separately)
    total_issued = in_sample['issued'] + sealed['issued']
    total_opportunities = in_sample['opportunities'] + sealed['opportunities']
    total_hits = in_sample['hits'] + sealed['hits']
    all_issued_days = in_sample['issued_days'] | sealed['issued_days']

    if total_issued == 0:
        print("INSUFFICIENT=1")
        return

    precision = total_hits / total_issued
    base_rate = precision  # base rate of predicted class (UP) within issued subset
    distinct_days = len(all_issued_days)

    # Design effect: cluster by day, compute variance inflation
    # For simplicity, use Kish's effective sample size: n_eff = (sum w)^2 / sum w^2
    # where w is number of calls per day
    day_counts = defaultdict(int)
    for c in in_sample['preliminary_calls'] + sealed['preliminary_calls']:
        day_counts[c[0]] += 1
    if day_counts:
        sum_w = sum(day_counts.values())
        sum_w2 = sum(v*v for v in day_counts.values())
        design_effect = sum_w2 / sum_w if sum_w > 0 else 1.0
        effective_n = total_issued / design_effect if design_effect > 0 else total_issued
    else:
        effective_n = 0.0

    sealed_precision = sealed['hits'] / sealed['issued'] if sealed['issued'] > 0 else 0.0

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()
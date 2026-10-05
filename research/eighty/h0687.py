# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 686
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from bisect import bisect_right, bisect_left
from statistics import median, stdev
from collections import defaultdict
import math

def utc_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def find_last_le(arr, x, key=lambda v: v[0]):
    lo, hi = 0, len(arr)
    while lo < hi:
        mid = (lo + hi) // 2
        if key(arr[mid]) <= x:
            lo = mid + 1
        else:
            hi = mid
    return lo - 1

def find_last_le_date(arr, x):
    lo, hi = 0, len(arr)
    while lo < hi:
        mid = (lo + hi) // 2
        if arr[mid][0] <= x:
            lo = mid + 1
        else:
            hi = mid
    return lo - 1

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load symbols with SharesOutstanding fundamentals
    cur.execute("""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding' AND as_of > 0
        ORDER BY symbol_id, as_of
    """)
    so_rows = cur.fetchall()
    if not so_rows:
        print("INSUFFICIENT=1")
        return

    so_by_sym = defaultdict(list)
    for r in so_rows:
        so_by_sym[r['symbol_id']].append((r['as_of'], r['value'], r['fetched_at']))

    # Load daily bars for symbols with fundamentals
    sym_ids = tuple(so_by_sym.keys())
    if len(sym_ids) == 1:
        ph = "(?)"
    else:
        ph = "(" + ",".join("?" * len(sym_ids)) + ")"
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN {ph}
        ORDER BY symbol_id, ts
    """, sym_ids)
    bar_rows = cur.fetchall()
    bars_by_sym = defaultdict(list)
    bar_dates_by_sym = defaultdict(list)
    for r in bar_rows:
        bars_by_sym[r['symbol_id']].append((r['ts'], r['close'], r['volume']))
        bar_dates_by_sym[r['symbol_id']].append(utc_date(r['ts']))

    # Load insider sales (code='S')
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code = 'S'
        ORDER BY symbol_id, filed_ts
    """)
    insider_by_sym = defaultdict(list)
    for r in cur.fetchall():
        insider_by_sym[r['symbol_id']].append(r['filed_ts'])

    # Load sentiment_features (daily mean_score)
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE mean_score IS NOT NULL
        ORDER BY symbol_id, day
    """)
    sent_by_sym = defaultdict(dict)
    for r in cur.fetchall():
        sent_by_sym[r['symbol_id']][r['day']] = r['mean_score']

    # Precompute 20-day return volatility for each symbol at each bar index >= 20
    vol_by_sym = defaultdict(dict)
    for sym, bars in bars_by_sym.items():
        if len(bars) < 21:
            continue
        closes = [b[1] for b in bars]
        rets = [(closes[i] - closes[i-1]) / closes[i-1] for i in range(1, len(closes))]
        for i in range(20, len(rets)):
            window = rets[i-19:i+1]
            if len(window) == 20:
                vol_by_sym[sym][i] = stdev(window) if len(set(window)) > 1 else 0.0

    # Identify decision points: QoQ SharesOutstanding > 5%
    decisions = []  # (decision_ts, symbol_id, bar_idx, decision_date)
    for sym, quarters in so_by_sym.items():
        if len(quarters) < 2:
            continue
        bars = bars_by_sym.get(sym, [])
        if len(bars) < 252:
            continue
        for i in range(1, len(quarters)):
            prev_asof, prev_val, _ = quarters[i-1]
            curr_asof, curr_val, fetched_at = quarters[i]
            if prev_val <= 0:
                continue
            qoq = (curr_val - prev_val) / prev_val
            if qoq <= 0.05:
                continue
            decision_ts = fetched_at
            decision_date = utc_date(decision_ts)
            # Find bar index at or before decision_ts
            idx = find_last_le(bars, decision_ts)
            if idx < 252:
                continue
            decisions.append((decision_ts, sym, idx, decision_date))

    if not decisions:
        print("INSUFFICIENT=1")
        return

    # Sort decisions by time
    decisions.sort(key=lambda x: x[0])

    # For each decision, evaluate entry and abstain conditions
    calls = []  # (decision_ts, sym, idx, decision_date, hit)
    opportunities = 0

    for decision_ts, sym, idx, decision_date in decisions:
        opportunities += 1
        bars = bars_by_sym[sym]
        # Entry: 20-day return > 0
        if idx < 20:
            continue
        ret20 = (bars[idx][1] - bars[idx-20][1]) / bars[idx-20][1]
        if ret20 <= 0:
            continue
        # Entry: 20-day median volume > 252-day median volume
        vol20 = [bars[j][2] for j in range(idx-19, idx+1)]
        vol252 = [bars[j][2] for j in range(idx-251, idx+1)]
        if median(vol20) <= median(vol252):
            continue

        # Abstain: insider open-market sale in prior 10 sessions
        if idx >= 10:
            ts_10d_ago = bars[idx-10][0]
            sales = insider_by_sym.get(sym, [])
            # binary search for sales in (ts_10d_ago, decision_ts]
            lo = bisect_left(sales, ts_10d_ago)
            hi = bisect_right(sales, decision_ts)
            if lo < hi:
                continue

        # Abstain: 5-day news sentiment MA < 0 (using sentiment_features)
        sent_scores = []
        for d in range(5):
            check_date = decision_date
            # go back d trading days using bar dates
            if idx - d < 0:
                break
            day_str = bar_dates_by_sym[sym][idx - d].isoformat()
            score = sent_by_sym.get(sym, {}).get(day_str)
            if score is not None:
                sent_scores.append(score)
        if len(sent_scores) >= 3:  # require at least 3 days
            if sum(sent_scores) / len(sent_scores) < 0:
                continue

        # Abstain: top volatility decile
        # Compute volatility for all symbols at this decision_date
        vols = []
        for s in so_by_sym.keys():
            if s not in vol_by_sym:
                continue
            s_bars = bars_by_sym.get(s, [])
            if not s_bars:
                continue
            s_idx = find_last_le(s_bars, decision_ts)
            if s_idx >= 20 and s_idx in vol_by_sym[s]:
                vols.append(vol_by_sym[s][s_idx])
        if vols:
            vols.sort()
            p90 = vols[int(0.9 * (len(vols) - 1))]
            sym_vol = vol_by_sym[sym].get(idx, 0)
            if sym_vol >= p90:
                continue

        # All conditions passed - issue call
        # Label: 21-day forward return
        if idx + 21 < len(bars):
            fwd_ret = (bars[idx+21][1] - bars[idx][1]) / bars[idx][1]
            hit = 1 if fwd_ret > 0 else 0
            calls.append((decision_ts, sym, idx, decision_date, hit))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Split: most recent 20% sealed
    calls.sort(key=lambda x: x[0])
    n = len(calls)
    split_idx = int(n * 0.8)
    main_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(c[4] for c in call_list)
        precision = hits / issued if issued else 0.0
        base_rate = precision  # base rate of UP within issued subset
        distinct_days = len(set(c[3] for c in call_list))
        # Design effect: 1 + (avg_cluster_size - 1) * rho, assume rho=0.5
        calls_per_day = defaultdict(int)
        for c in call_list:
            calls_per_day[c[3]] += 1
        avg_cluster = issued / distinct_days if distinct_days else 1
        design_effect = 1 + (avg_cluster - 1) * 0.5
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_prec, main_br, main_dd, main_en = compute_metrics(main_calls)
    sealed_issued, sealed_hits, sealed_prec, _, _, _ = compute_metrics(sealed_calls)

    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={main_prec:.6f}")
    print(f"BASE_RATE={main_br:.6f}")
    print(f"DISTINCT_DAYS={main_dd}")
    print(f"EFFECTIVE_N={main_en:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()
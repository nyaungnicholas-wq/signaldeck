# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 728
# cycle_index: 55
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    con = connect()
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # 1. Fetch all officer purchases (code='P', CEO/CFO)
    cur.execute("""
        SELECT symbol_id, filed_ts, insider, title, shares, price, value
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
          AND filed_ts IS NOT NULL
        ORDER BY filed_ts
    """)
    officer_buys = [dict(r) for r in cur.fetchall()]
    if not officer_buys:
        print("INSUFFICIENT=1")
        return

    # 2. Fetch all officer sales for abstain
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code = 'S'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
          AND filed_ts IS NOT NULL
    """)
    officer_sales = [(r['symbol_id'], r['filed_ts']) for r in cur.fetchall()]
    sales_by_sym = defaultdict(list)
    for sym, ts in officer_sales:
        sales_by_sym[sym].append(ts)

    # 3. Get relevant symbols
    symbols = set(b['symbol_id'] for b in officer_buys)
    sym_list = ','.join('?' for _ in symbols)

    # 4. Fetch daily bars for relevant symbols (tf='1d')
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({sym_list})
        ORDER BY symbol_id, ts
    """, list(symbols))
    bars_by_sym = defaultdict(list)
    for r in cur.fetchall():
        bars_by_sym[r['symbol_id']].append((r['ts'], r['close']))

    # 5. Fetch sentiment_features for relevant symbols
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({sym_list})
        ORDER BY symbol_id, day
    """, list(symbols))
    sent_by_sym = defaultdict(dict)
    for r in cur.fetchall():
        sent_by_sym[r['symbol_id']][r['day']] = r['mean_score']

    # Precompute bar dates and indices for each symbol
    bar_dates_by_sym = {}
    bar_closes_by_sym = {}
    ts_to_idx_by_sym = {}
    for sym, bars in bars_by_sym.items():
        dates = [unix_to_date(ts) for ts, _ in bars]
        closes = [c for _, c in bars]
        bar_dates_by_sym[sym] = dates
        bar_closes_by_sym[sym] = closes
        ts_to_idx_by_sym[sym] = {ts: i for i, (ts, _) in enumerate(bars)}

    # Helper: find prior trading day index for a given date
    def find_prior_bar_idx(sym, target_date):
        dates = bar_dates_by_sym.get(sym, [])
        if not dates:
            return None
        lo, hi = 0, len(dates) - 1
        ans = None
        while lo <= hi:
            mid = (lo + hi) // 2
            if dates[mid] <= target_date:
                ans = mid
                lo = mid + 1
            else:
                hi = mid - 1
        return ans

    # Helper: compute 20-day realized volatility ending at idx (inclusive)
    def realized_vol(closes, idx, window=20):
        if idx < window - 1:
            return None
        rets = []
        for i in range(idx - window + 1, idx + 1):
            if closes[i-1] > 0:
                rets.append(math.log(closes[i] / closes[i-1]))
        if len(rets) < 2:
            return None
        mean = sum(rets) / len(rets)
        var = sum((r - mean) ** 2 for r in rets) / (len(rets) - 1)
        return math.sqrt(var) * math.sqrt(252)  # annualized

    # Helper: compute 80th percentile of 20-day vol over 252-day history ending at idx
    def vol_percentile_80(closes, idx, lookback=252, window=20):
        if idx < lookback - 1:
            return None
        vols = []
        for i in range(idx - lookback + 1, idx + 1):
            v = realized_vol(closes, i, window)
            if v is not None:
                vols.append(v)
        if not vols:
            return None
        vols.sort()
        return vols[int(0.8 * (len(vols) - 1))]

    # Process each officer buy
    signals = []  # (decision_date, symbol_id, filed_ts, forward_return, hit)
    for b in officer_buys:
        sym = b['symbol_id']
        filed_ts = b['filed_ts']
        filed_date = unix_to_date(filed_ts)

        # Find decision bar (prior trading day)
        idx = find_prior_bar_idx(sym, filed_date)
        if idx is None or idx < 756:  # need 3 years history
            continue
        decision_date = bar_dates_by_sym[sym][idx]
        decision_close = bar_closes_by_sym[sym][idx]

        # Check 3-year low (756 trading days)
        low_3y = min(bar_closes_by_sym[sym][idx-756:idx+1])
        if decision_close > low_3y * 1.001:  # allow tiny float tolerance
            continue

        # Check news sentiment: 5-day mean > 10-day mean AND 10-day mean < 0
        # sentiment_features day is 'YYYY-MM-DD', decision_date is date object
        sent_dates = sorted(sent_by_sym.get(sym, {}).keys())
        if not sent_dates:
            continue
        # Find sentiment entries for D-10 to D-1
        d_str = date_to_str(decision_date)
        sent_vals_10 = []
        sent_vals_5 = []
        for sd in sent_dates:
            if sd < d_str:
                days_diff = (decision_date - str_to_date(sd)).days
                if 1 <= days_diff <= 10:
                    sent_vals_10.append(sent_by_sym[sym][sd])
                if 1 <= days_diff <= 5:
                    sent_vals_5.append(sent_by_sym[sym][sd])
        if len(sent_vals_10) < 5 or len(sent_vals_5) < 3:
            continue
        mean_10 = sum(sent_vals_10) / len(sent_vals_10)
        mean_5 = sum(sent_vals_5) / len(sent_vals_5)
        if not (mean_10 < 0 and mean_5 > mean_10):
            continue

        # Abstain: officer sale in prior 21 calendar days
        sales = sales_by_sym.get(sym, [])
        if any(filed_ts - 21*86400 <= s_ts < filed_ts for s_ts in sales):
            continue

        # Abstain: other officer purchase in prior 63 calendar days (excluding this one)
        other_buys = [ob for ob in officer_buys if ob['symbol_id'] == sym and ob['filed_ts'] != filed_ts]
        if any(filed_ts - 63*86400 <= ob['filed_ts'] < filed_ts for ob in other_buys):
            continue

        # Abstain: 20-day vol > 80th percentile of 252-day history
        vol_20 = realized_vol(bar_closes_by_sym[sym], idx, 20)
        vol_80 = vol_percentile_80(bar_closes_by_sym[sym], idx, 252, 20)
        if vol_20 is None or vol_80 is None or vol_20 > vol_80:
            continue

        # Compute 21-day forward return
        if idx + 21 >= len(bar_closes_by_sym[sym]):
            continue
        fwd_close = bar_closes_by_sym[sym][idx + 21]
        fwd_ret = (fwd_close / decision_close) - 1
        hit = 1 if fwd_ret > 0 else 0

        signals.append((decision_date, sym, filed_ts, fwd_ret, hit))

    if not signals:
        print("INSUFFICIENT=1")
        return

    # Sort by decision date
    signals.sort(key=lambda x: x[0])

    # Split 80/20 by time (sealed = most recent 20%)
    n = len(signals)
    split_idx = int(n * 0.8)
    train_signals = signals[:split_idx]
    sealed_signals = signals[split_idx:]

    def compute_metrics(sig_list, label):
        if not sig_list:
            return
        issued = len(sig_list)
        hits = sum(s[4] for s in sig_list)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (up) within issued
        distinct_days = len(set((s[0], s[1]) for s in sig_list))  # (date, symbol) pairs

        # Design effect: cluster by month
        month_counts = defaultdict(int)
        for s in sig_list:
            month_key = (s[0].year, s[0].month)
            month_counts[month_key] += 1
        counts = list(month_counts.values())
        if len(counts) > 1:
            mean_c = sum(counts) / len(counts)
            var_c = sum((c - mean_c) ** 2 for c in counts) / (len(counts) - 1)
            design_effect = max(1.0, var_c / mean_c) if mean_c > 0 else 1.0
        else:
            design_effect = 1.0
        effective_n = issued / design_effect

        print(f"{label}_ISSUED={issued}")
        print(f"{label}_PRECISION={precision:.6f}")
        print(f"{label}_BASE_RATE={base_rate:.6f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.2f}")

    # Overall metrics
    issued = len(signals)
    opportunities = len(officer_buys)  # decision points considered
    hits = sum(s[4] for s in signals)
    precision = hits / issued
    base_rate = hits / issued
    distinct_days = len(set((s[0], s[1]) for s in signals))

    month_counts = defaultdict(int)
    for s in signals:
        month_key = (s[0].year, s[0].month)
        month_counts[month_key] += 1
    counts = list(month_counts.values())
    if len(counts) > 1:
        mean_c = sum(counts) / len(counts)
        var_c = sum((c - mean_c) ** 2 for c in counts) / (len(counts) - 1)
        design_effect = max(1.0, var_c / mean_c) if mean_c > 0 else 1.0
    else:
        design_effect = 1.0
    effective_n = issued / design_effect

    sealed_hits = sum(s[4] for s in sealed_signals)
    sealed_precision = sealed_hits / len(sealed_signals) if sealed_signals else 0.0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    # Invariants check
    if distinct_days > issued:
        print("ERROR: DISTINCT_DAYS > ISSUED", file=sys.stderr)
    if effective_n >= issued:
        print("ERROR: EFFECTIVE_N >= ISSUED", file=sys.stderr)

if __name__ == '__main__':
    main()
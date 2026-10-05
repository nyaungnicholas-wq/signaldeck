# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 721
# cycle_index: 48
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import sys
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

OFFICER_KEYWORDS = ('CEO', 'CFO', 'COO', 'PRESIDENT', 'VICE PRESIDENT', 'VP ', 'CHIEF', 'OFFICER', 'TREASURER', 'SECRETARY', 'CONTROLLER', 'PRINCIPAL')
DIRECTOR_KEYWORDS = ('DIRECTOR', 'CHAIRMAN', 'CHAIR', 'BOARD')

def is_officer_or_director(title: str) -> bool:
    if not title:
        return False
    t = title.upper()
    return any(k in t for k in OFFICER_KEYWORDS) or any(k in t for k in DIRECTOR_KEYWORDS)

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load insider purchases (code='P') by officers/directors
    cur.execute("""
        SELECT symbol_id, filed_ts, tx_ts, shares, price, value, insider, title
        FROM insider_trades
        WHERE code = 'P' AND filed_ts IS NOT NULL AND tx_ts IS NOT NULL
    """)
    insider_rows = cur.fetchall()
    if not insider_rows:
        print("INSUFFICIENT=1")
        return

    # Load fundamentals (EPS, Revenues) with fetched_at <= decision_ts
    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('EPS','Revenues') AND fetched_at IS NOT NULL AND as_of > 0
    """)
    fund_rows = cur.fetchall()
    if not fund_rows:
        print("INSUFFICIENT=1")
        return

    # Organize fundamentals by symbol
    fund_by_symbol = defaultdict(lambda: {'EPS': [], 'Revenues': []})
    for r in fund_rows:
        fund_by_symbol[r['symbol_id']][r['metric']].append((r['as_of'], r['value'], r['fetched_at']))

    # Sort each metric by as_of (period)
    for sym in fund_by_symbol:
        for m in ('EPS', 'Revenues'):
            fund_by_symbol[sym][m].sort(key=lambda x: x[0])

    # Load daily bars for forward returns and volume
    cur.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d'
    """)
    bar_rows = cur.fetchall()
    if not bar_rows:
        print("INSUFFICIENT=1")
        return

    bars_by_symbol = defaultdict(list)
    for r in bar_rows:
        bars_by_symbol[r['symbol_id']].append((r['ts'], r['close'], r['volume']))
    for sym in bars_by_symbol:
        bars_by_symbol[sym].sort(key=lambda x: x[0])

    # Helper: find bar index for a given ts (unix epoch)
    def find_bar_idx(bars, ts):
        # bars sorted by ts; find last bar with ts <= decision_ts
        lo, hi = 0, len(bars) - 1
        ans = -1
        while lo <= hi:
            mid = (lo + hi) // 2
            if bars[mid][0] <= ts:
                ans = mid
                lo = mid + 1
            else:
                hi = mid - 1
        return ans

    # Helper: compute QoQ growth rates from quarterly values sorted by as_of
    def compute_growth_rates(quarterly):
        # quarterly: list of (as_of, value, fetched_at) sorted by as_of
        rates = []
        for i in range(1, len(quarterly)):
            prev_val = quarterly[i-1][1]
            curr_val = quarterly[i][1]
            if prev_val != 0:
                rates.append((quarterly[i][0], (curr_val - prev_val) / abs(prev_val)))
            else:
                rates.append((quarterly[i][0], None))
        return rates  # list of (as_of, growth_rate)

    # Helper: check acceleration (growth rate increasing) for n consecutive quarters
    def check_acceleration(rates, n):
        if len(rates) < n:
            return False
        for i in range(-n, 0):
            if rates[i][1] is None or rates[i-1][1] is None:
                return False
            if rates[i][1] <= rates[i-1][1]:
                return False
        return True

    # Helper: check deceleration (growth rate decreasing) for n consecutive quarters
    def check_deceleration(rates, n):
        if len(rates) < n:
            return False
        for i in range(-n, 0):
            if rates[i][1] is None or rates[i-1][1] is None:
                return False
            if rates[i][1] >= rates[i-1][1]:
                return False
        return True

    # Process each insider purchase
    qualified = []  # (decision_ts, symbol_id, fwd_return, up)
    for r in insider_rows:
        sym = r['symbol_id']
        filed_ts = r['filed_ts']
        tx_ts = r['tx_ts']
        title = r['title'] or ''

        if not is_officer_or_director(title):
            continue
        if filed_ts == tx_ts:  # no disclosure lag
            continue

        # Get fundamentals available at filed_ts
        fdata = fund_by_symbol.get(sym)
        if not fdata:
            continue
        eps_avail = [(a,v,f) for (a,v,f) in fdata['EPS'] if f <= filed_ts]
        rev_avail = [(a,v,f) for (a,v,f) in fdata['Revenues'] if f <= filed_ts]
        if len(eps_avail) < 3 or len(rev_avail) < 4:  # need 3+ quarters for 2+ decel, 4+ for 3+ accel
            continue

        eps_rates = compute_growth_rates(eps_avail)
        rev_rates = compute_growth_rates(rev_avail)

        # Check latest quarter not >90 days stale (as_of vs filed_ts)
        latest_rev_asof = rev_avail[-1][0]
        if filed_ts - latest_rev_asof > 90 * 86400:
            continue

        if not check_acceleration(rev_rates, 3):
            continue
        if not check_deceleration(eps_rates, 2):
            continue

        # Check 20-day avg dollar volume >= $1M before decision
        bars = bars_by_symbol.get(sym, [])
        idx = find_bar_idx(bars, filed_ts)
        if idx < 19:
            continue
        vol_sum = 0.0
        for i in range(idx-19, idx+1):
            close = bars[i][1]
            vol = bars[i][2]
            vol_sum += close * vol
        avg_dollar_vol = vol_sum / 20.0
        if avg_dollar_vol < 1_000_000:
            continue

        # Compute 63-day forward return (63 trading days ~ 3 months)
        if idx + 63 >= len(bars):
            continue
        entry_close = bars[idx][1]
        exit_close = bars[idx + 63][1]
        fwd_return = (exit_close - entry_close) / entry_close
        up = 1 if fwd_return > 0 else 0

        qualified.append((filed_ts, sym, fwd_return, up))

    if not qualified:
        print("INSUFFICIENT=1")
        return

    # Sort by decision time
    qualified.sort(key=lambda x: x[0])

    # Split: most recent 20% as sealed era
    n = len(qualified)
    split_idx = int(n * 0.8)
    main = qualified[:split_idx]
    sealed = qualified[split_idx:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(c[3] for c in calls)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (up) within issued subset
        distinct_days = len(set(c[0] // 86400 for c in calls))
        # Design effect: 1 + (avg_cluster_size - 1) * rho, approximate with day clustering
        day_counts = defaultdict(int)
        for c in calls:
            day_counts[c[0] // 86400] += 1
        if len(day_counts) > 1:
            avg_cluster = issued / len(day_counts)
            rho = 0.5  # conservative intra-day correlation
            deff = 1 + (avg_cluster - 1) * rho
        else:
            deff = issued  # all same day
        effective_n = issued / deff if deff > 0 else 0
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_prec, main_br, main_days, main_eff = compute_metrics(main)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_eff = compute_metrics(sealed)

    # Overall issued = main + sealed
    total_issued = main_issued + sealed_issued
    total_hits = main_hits + sealed_hits
    total_prec = total_hits / total_issued if total_issued else 0
    total_br = total_hits / total_issued if total_issued else 0
    total_days = len(set(c[0] // 86400 for c in qualified))
    # Effective N overall
    day_counts_all = defaultdict(int)
    for c in qualified:
        day_counts_all[c[0] // 86400] += 1
    if len(day_counts_all) > 1:
        avg_cluster = total_issued / len(day_counts_all)
        deff = 1 + (avg_cluster - 1) * 0.5
    else:
        deff = total_issued
    total_eff = total_issued / deff if deff > 0 else 0

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={len(insider_rows)}")
    print(f"PRECISION={total_prec:.6f}")
    print(f"BASE_RATE={total_br:.6f}")
    print(f"DISTINCT_DAYS={total_days}")
    print(f"EFFECTIVE_N={total_eff:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()